package cmd

// A pipeline step's output, two ways. A running step is followed live over
// the step log relay (go/internal/pipelineapi/streams.go, over the shared
// execution_output JetStream stream) - see internal/client/pipeline_logs.go
// for that wire contract and its one real limitation: a fresh connection has
// no history to replay, so a live connection only ever shows output produced
// from the moment it connects. A step that has already concluded reads
// differently: this command instead fetches its durable step_log artifact
// (enginekit/pipelineartifacts.KindStepLog, uploaded when the step
// concluded) through the same artifacts list and presigned download
// cmd/pipeline_artifacts.go uses, and prints it whole - mirroring the
// portal's usePipelineStepArtifactLog. That listing is keyset-paged, so the
// search follows its cursor rather than read the first page as the run's
// whole record. --follow only ever applies to the live relay: a concluded
// step's log is a fixed, complete record, so there is nothing left to
// follow.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// pipelineStepStatusConcluded is the PipelineStep.Status value a settled
// step carries, shared by the archive-log branch below and
// pipelineStepConcluded's own poll.
const pipelineStepStatusConcluded = "concluded"

// pipelineLogStreamReconnectDelay is how long `logs --follow` waits before
// reconnecting after the relay's own error frame or a stream fault, so a
// transient disconnect does not spin the CLI in a tight retry loop.
const pipelineLogStreamReconnectDelay = 2 * time.Second

// pipelineArtifactPageSize is the page findPipelineStepLogArtifact asks for:
// the route's own ceiling (enginekit/pipelineartifacts.MaxListLimit), so the
// walk makes as few round trips as the server allows.
const pipelineArtifactPageSize = 100

// pipelineArtifactPageBudget bounds that walk. A run's step logs are written
// oldest-first alongside its declared artifacts, so the one this command
// wants is usually on the first page; the budget exists so a pathological
// run (or a server that keeps handing back a cursor) cannot turn one 'logs'
// call into an unbounded walk. At the page size above that is 5000 artifacts
// before the search gives up, and giving up is reported as a capped read
// rather than as an absent log.
const pipelineArtifactPageBudget = 50

func newPipelineLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <run>",
		Short: "Show a pipeline step's output",
		Long: `Show a pipeline step's output.

A step that has already concluded prints its complete, archived log in one
shot - --follow does nothing extra for it, since there is nothing left to
produce. A step that is still running is followed over the live log relay
instead: without --follow, the command tails the step until it concludes and
then stops; with --follow it keeps reconnecting through a dropped stream
instead of giving up. The relay itself has no history - a fresh connection
only sees lines produced from the moment it connects - which is exactly why
a concluded step reads its archived log instead.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineLogs(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(logsCommand)
	registerPipelineLogsFlags(logsCommand)
	return logsCommand
}

// registerPipelineLogsFlags is shared by `pipeline logs` and
// `application pipeline logs`.
func registerPipelineLogsFlags(command *cobra.Command) {
	command.Flags().String("step", "", "Step key to follow (required when the run has more than one step)")
	command.Flags().Bool("follow", false, "Keep streaming, reconnecting through transient stream faults, until the step concludes")
}

func runPipelineLogs(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	stepReference, _ := command.Flags().GetString("step")
	follow, _ := command.Flags().GetBool("follow")
	runID = strings.TrimSpace(runID)

	step, resolveError := resolvePipelineStep(command, selector, runID, strings.TrimSpace(stepReference))
	if resolveError != nil {
		return resolveError
	}
	if step.Status == pipelineStepStatusConcluded {
		return runPipelineLogsFromArchive(command, selector, runID, step)
	}
	if step.ExecutionID == nil || step.ExecutionStepID == nil {
		return fmt.Errorf("step %q has not started, so it has no log stream yet - "+
			"check 'ankra pipeline get %s' for its status", step.StepKey, runID)
	}

	out := command.OutOrStdout()
	progress := command.ErrOrStderr()
	var lastSeq int64
	for {
		events, streamError := apiClient.StreamPipelineStepLogs(command.Context(), selector, runID, step.ID, lastSeq)
		if streamError != nil {
			var unavailable *client.PipelineLogStreamUnavailableError
			if errors.As(streamError, &unavailable) && follow {
				// A 503 that carries no Retry-After, or a zero one, must not
				// turn --follow into a tight reconnect loop against the relay.
				retryAfter := time.Duration(unavailable.RetryAfterSeconds) * time.Second
				if retryAfter < pipelineLogStreamReconnectDelay {
					retryAfter = pipelineLogStreamReconnectDelay
				}
				_, _ = fmt.Fprintf(progress, "Log stream unavailable (%s); retrying in %ds.\n",
					unavailable.Detail, int(retryAfter.Seconds()))
				if sleepError := sleepInterrupted(command.Context(), retryAfter); sleepError != nil {
					return sleepError
				}
				continue
			}
			return streamError
		}

		for event := range events {
			switch event.Type {
			case "line":
				_, _ = fmt.Fprintf(out, "[%s] %s\n", event.Stream, event.Line)
				lastSeq = event.Seq
			case "error":
				_, _ = fmt.Fprintf(progress, "Log stream fault: %s\n", event.Error)
			}
		}

		concluded, statusError := pipelineStepConcluded(command, selector, runID, step.ID)
		if statusError != nil {
			return statusError
		}
		if concluded {
			_, _ = fmt.Fprintln(progress, "Log stream ended: the step has concluded.")
			return nil
		}
		if !follow {
			_, _ = fmt.Fprintln(progress, "Log stream ended.")
			return nil
		}
		// Every reconnect waits, not only a faulted one: a proxy that closes
		// each connection promptly would otherwise be reconnected to as fast
		// as it hangs up. The wait is interruptible so Ctrl+C stops --follow
		// at once rather than at the next network call.
		if sleepError := sleepInterrupted(command.Context(), pipelineLogStreamReconnectDelay); sleepError != nil {
			return sleepError
		}
	}
}

// resolvePipelineStep finds the step a logs invocation names: the exact step
// key when --step was given, or the run's only step when it has just one.
// More than one step with no --step is a usage error - guessing which one the
// user meant would show them the wrong build's output.
func resolvePipelineStep(command *cobra.Command, selector client.PipelineSelector, runID string,
	stepReference string) (client.PipelineStep, error) {
	detail, getError := apiClient.GetPipelineRun(command.Context(), selector, runID)
	if getError != nil {
		return client.PipelineStep{}, getError
	}
	if stepReference != "" {
		for _, step := range detail.Steps {
			if step.StepKey == stepReference || step.ID == stepReference {
				return step, nil
			}
		}
		return client.PipelineStep{}, withExitCode(exitNotFound,
			fmt.Errorf("no step %q on run %s - run 'ankra pipeline get %s' to see the planned steps",
				stepReference, runID, runID))
	}
	switch len(detail.Steps) {
	case 0:
		return client.PipelineStep{}, fmt.Errorf("run %s has no planned steps yet", runID)
	case 1:
		return detail.Steps[0], nil
	default:
		return client.PipelineStep{}, withExitCode(exitUsage,
			fmt.Errorf("run %s has %d steps - pass --step to name the one to follow", runID, len(detail.Steps)))
	}
}

// pipelineStepConcluded re-reads one step's status. It is a full run fetch
// because the API has no single-step read on this surface; the run detail is
// small enough that polling it once per disconnect is not a cost worth a
// dedicated route for.
func pipelineStepConcluded(command *cobra.Command, selector client.PipelineSelector, runID string,
	stepID string) (bool, error) {
	detail, getError := apiClient.GetPipelineRun(command.Context(), selector, runID)
	if getError != nil {
		return false, getError
	}
	for _, step := range detail.Steps {
		if step.ID == stepID {
			return step.Status == pipelineStepStatusConcluded, nil
		}
	}
	return false, withExitCode(exitNotFound, fmt.Errorf("step %s is no longer on run %s", stepID, runID))
}

// runPipelineLogsFromArchive prints a concluded step's complete log from its
// durable step_log artifact instead of opening the live relay, which would
// see nothing for a step that already finished (DeliverNewPolicy - see the
// package doc above). Mirrors the portal's usePipelineStepArtifactLog: find
// the run's step_log artifact for this step, then branch on its own Status,
// since "no artifact" and each of the artifact's three non-terminal-success
// states are different facts a caller must not collapse into "no log".
func runPipelineLogsFromArchive(command *cobra.Command, selector client.PipelineSelector, runID string,
	step client.PipelineStep) error {
	out := command.OutOrStdout()
	progress := command.ErrOrStderr()

	logArtifact, wasFullyRead, findError := findPipelineStepLogArtifact(command, selector, runID, step.ID)
	if findError != nil {
		return findError
	}
	if logArtifact == nil {
		if !wasFullyRead {
			// The search stopped at its own page cap, so absence was never
			// observed: say the read was capped rather than report a log
			// that may well exist on a page this command declined to fetch.
			_, _ = fmt.Fprintf(progress,
				"Stopped after %d pages of run %s's artifacts without finding a log for step %q;"+
					" list them with 'ankra pipeline artifacts %s'.\n",
				pipelineArtifactPageBudget, runID, step.StepKey, runID)
			return nil
		}
		_, _ = fmt.Fprintf(progress, "No archived log was recorded for step %q.\n", step.StepKey)
		return nil
	}

	switch logArtifact.Status {
	case client.PipelineArtifactStatusUploaded:
		// Streamed straight through rather than buffered: a step log is
		// whatever the build printed, which for a verbose one is tens of
		// megabytes, and holding all of it to write it once buys nothing.
		return apiClient.DownloadPipelineArtifact(command.Context(), selector, logArtifact.ID, out)
	case client.PipelineArtifactStatusPending:
		_, _ = fmt.Fprintf(progress,
			"Step %q has concluded; its log is still being archived - try again shortly.\n", step.StepKey)
		return nil
	case client.PipelineArtifactStatusFailed:
		detail := logArtifact.ErrorMessage
		if detail == "" {
			detail = "Ankra could not archive this log."
		}
		return fmt.Errorf("step %q's log was not archived: %s", step.StepKey, detail)
	case client.PipelineArtifactStatusExpired:
		return withExitCode(exitNotFound,
			fmt.Errorf("step %q's log has expired and was removed from storage", step.StepKey))
	default:
		_, _ = fmt.Fprintf(progress, "Step %q's log artifact is in an unrecognised state (%s).\n",
			step.StepKey, logArtifact.Status)
		return nil
	}
}

// findPipelineStepLogArtifact walks the run's artifact pages for the given
// step's step_log row. It returns the artifact when it finds one, and
// otherwise reports through wasFullyRead whether the run's artifacts were
// read to the end (a genuine absence) or the page budget ran out first (an
// answer the caller must not state as absence).
func findPipelineStepLogArtifact(command *cobra.Command, selector client.PipelineSelector, runID string,
	stepID string) (artifact *client.PipelineArtifact, wasFullyRead bool, findError error) {
	options := client.ListPipelineArtifactsOptions{Limit: pipelineArtifactPageSize}
	for page := 0; page < pipelineArtifactPageBudget; page++ {
		list, listError := apiClient.ListPipelineArtifacts(command.Context(), selector, runID, options)
		if listError != nil {
			return nil, false, listError
		}
		for index := range list.Artifacts {
			candidate := list.Artifacts[index]
			if candidate.Kind == client.PipelineArtifactKindStepLog &&
				candidate.StepID != nil && *candidate.StepID == stepID {
				return &list.Artifacts[index], true, nil
			}
		}
		if list.NextCursor == nil || *list.NextCursor == "" {
			return nil, true, nil
		}
		options.Cursor = *list.NextCursor
	}
	return nil, false, nil
}
