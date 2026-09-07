package cmd

// A pipeline step's output, three ways. A running step is followed live over
// the step log relay (go/internal/pipelineapi/streams.go) - see
// internal/client/pipeline_logs.go for that wire contract. A step that has
// already concluded reads differently: this command instead fetches its
// durable step_log artifact (enginekit/pipelineartifacts.KindStepLog,
// uploaded when the step concluded) through the same artifacts list and
// presigned download cmd/pipeline_artifacts.go uses, and prints it whole -
// mirroring the portal's usePipelineStepArtifactLog. That listing is
// keyset-paged, so the search follows its cursor rather than read the first
// page as the run's whole record.
//
// The third way exists because that archive is not guaranteed. Archiving a
// step log needs a ready backup vault; an organisation without one has
// nowhere to put the object (enginekit/pipelineartifacts.ErrNoVault), so the
// step is dispatched with uploads disabled and no artifact row is ever
// minted - which used to leave a concluded step's output unreadable from the
// CLI altogether. When the run's artifacts are read to the end and hold no
// step_log for the step, the command replays that step's output from the
// platform's retained log stream instead (follow=false), and stops when the
// replay is drained.
//
// --follow only ever applies to the live relay: a concluded step's log,
// archived or replayed, is a fixed record, so there is nothing left to
// follow. --replay is its counterpart on a running step, asking the platform
// for the output produced before this command connected.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// pipelineLogReplayIdleTimeout bounds the replay of a concluded step's
// retained output. A platform older than that contract ignores follow=false
// and holds the connection open on keepalives forever, so waiting for the
// stream to end would hang the command with nothing to show for it; after
// this long with no frame - from the connection when none ever arrives, from
// the last frame otherwise - the command stops and says so. It is a var only
// so the tests can shorten it; nothing else writes it.
var pipelineLogReplayIdleTimeout = 15 * time.Second

func newPipelineLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <run>",
		Short: "Show a pipeline step's output",
		Long: `Show a pipeline step's output.

A step that has already concluded prints its complete log in one shot -
--follow does nothing extra for it, since there is nothing left to produce.
Its archived log is read when the run has one; archiving needs a ready backup
vault, and when there is none the command replays the step's output from the
platform's retained log stream instead and stops when that runs out.

A step that is still running is followed over the live log stream: without
--follow, the command tails the step until it concludes and then stops; with
--follow it keeps reconnecting through a dropped stream instead of giving up.
A live connection starts from the moment it connects unless you pass
--replay, which asks the platform for the output the step already produced
first.`,
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
	command.Flags().Bool("replay", false,
		"Also show the output a running step produced before this command connected "+
			"(a concluded step's log is always shown whole)")
}

func runPipelineLogs(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	stepReference, _ := command.Flags().GetString("step")
	follow, _ := command.Flags().GetBool("follow")
	isReplaying, _ := command.Flags().GetBool("replay")
	runID = strings.TrimSpace(runID)

	step, resolveError := resolvePipelineStep(command, selector, runID, strings.TrimSpace(stepReference))
	if resolveError != nil {
		return resolveError
	}
	if step.Status == pipelineStepStatusConcluded {
		return runPipelineLogsFromArchive(command, selector, runID, step)
	}
	if !pipelineStepHasLogStream(step) {
		return fmt.Errorf("step %q has not started, so it has no log stream yet - "+
			"check 'ankra pipeline get %s' for its status", step.StepKey, runID)
	}

	// Only an explicit --replay reaches the wire: the flag's own default is
	// indistinguishable from not passing it, and the route reads an absent
	// `replay` as "decide from the step's status", which is today's
	// behaviour. A resume cursor outranks it server-side, so leaving it set
	// across reconnects cannot re-send output already printed.
	streamOptions := client.StepLogStreamOptions{}
	if command.Flags().Changed("replay") {
		streamOptions.IsReplaying = &isReplaying
	}

	out := command.OutOrStdout()
	progress := command.ErrOrStderr()
	var lastSeq int64
	for {
		streamOptions.FromSequence = lastSeq
		events, streamError := apiClient.StreamPipelineStepLogs(command.Context(), selector, runID, step.ID,
			streamOptions)
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
			printPipelineLogEvent(out, progress, event)
			if event.Type == "line" {
				lastSeq = event.Seq
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

// runPipelineLogsFromArchive prints a concluded step's complete log, from
// its durable step_log artifact where the run has one. Mirrors the portal's
// usePipelineStepArtifactLog: find the run's step_log artifact for this step,
// then branch on its own Status, since "no artifact" and each of the
// artifact's three non-terminal-success states are different facts a caller
// must not collapse into "no log". A run with no step_log at all, or one
// whose object the download cannot find, falls through to the platform's
// retained log stream instead of reporting the step as having printed
// nothing.
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
			// The retained stream is not tried either, for the same reason -
			// the archive is still the better copy if it is there.
			_, _ = fmt.Fprintf(progress,
				"Stopped after %d pages of run %s's artifacts without finding a log for step %q;"+
					" list them with 'ankra pipeline artifacts %s'.\n",
				pipelineArtifactPageBudget, runID, step.StepKey, runID)
			return nil
		}
		if !pipelineStepHasLogStream(step) {
			_, _ = fmt.Fprintf(progress, "No archived log was recorded for step %q.\n", step.StepKey)
			return nil
		}
		_, _ = fmt.Fprintf(progress,
			"No archived log was recorded for step %q (archiving one needs a ready backup vault);"+
				" replaying the platform's retained log stream instead.\n", step.StepKey)
		return runPipelineLogsFromRetainedStream(command, selector, runID, step)
	}

	switch logArtifact.Status {
	case client.PipelineArtifactStatusUploaded:
		// Streamed straight through rather than buffered: a step log is
		// whatever the build printed, which for a verbose one is tens of
		// megabytes, and holding all of it to write it once buys nothing.
		// Counting what reached stdout is what makes the fallback below safe:
		// a download that failed halfway has already printed part of the log,
		// and replaying the stream on top of it would show those lines twice.
		countedOutput := &countingWriter{destination: out}
		downloadError := apiClient.DownloadPipelineArtifact(command.Context(), selector,
			logArtifact.ID, countedOutput)
		if downloadError == nil {
			return nil
		}
		if countedOutput.written == 0 && pipelineArtifactIsNotFound(downloadError) &&
			pipelineStepHasLogStream(step) {
			_, _ = fmt.Fprintf(progress,
				"Step %q's archived log is recorded but the platform cannot find it;"+
					" replaying the retained log stream instead.\n", step.StepKey)
			return runPipelineLogsFromRetainedStream(command, selector, runID, step)
		}
		return downloadError
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
//
// A step row carries at most one live step_log, but not necessarily only
// one row: re-dispatching the same step supersedes whatever it had already
// minted, marking that row failed with a superseding reason and writing a
// fresh one. The listing is oldest-first, so the step's real log is the
// last matching row, not the first - taking the first would report a
// superseded upload's failure as the step's log. The walk therefore keeps
// the newest match it has seen, and returns early only on a match that
// cannot have been superseded, since supersession always leaves the row
// failed.
func findPipelineStepLogArtifact(command *cobra.Command, selector client.PipelineSelector, runID string,
	stepID string) (artifact *client.PipelineArtifact, wasFullyRead bool, findError error) {
	options := client.ListPipelineArtifactsOptions{Limit: pipelineArtifactPageSize}
	var newest *client.PipelineArtifact
	for page := 0; page < pipelineArtifactPageBudget; page++ {
		list, listError := apiClient.ListPipelineArtifacts(command.Context(), selector, runID, options)
		if listError != nil {
			return nil, false, listError
		}
		for index := range list.Artifacts {
			candidate := list.Artifacts[index]
			if candidate.Kind != client.PipelineArtifactKindStepLog ||
				candidate.StepID == nil || *candidate.StepID != stepID {
				continue
			}
			newest = &list.Artifacts[index]
			if candidate.Status != client.PipelineArtifactStatusFailed {
				return newest, true, nil
			}
		}
		if list.NextCursor == nil || *list.NextCursor == "" {
			return newest, true, nil
		}
		options.Cursor = *list.NextCursor
	}
	return newest, false, nil
}

// runPipelineLogsFromRetainedStream prints a concluded step's output from the
// platform's retained log stream, for the case its archived log is not there
// to read. follow=false asks the relay to replay the step's retained history
// and then end, so this is a one-shot print rather than the reconnecting
// tail a running step gets.
//
// The idle guard is the whole reason this is not a plain `for range events`.
// A platform that predates the replay contract ignores follow=false, opens a
// live tail on a step that will never publish again, and keeps the
// connection alive with keepalives indefinitely; the CLI must not hang
// waiting for an end that is not coming. Stopping is reported rather than
// dressed up as the end of the log, because a truncated replay and a
// complete one are not the same answer.
func runPipelineLogsFromRetainedStream(command *cobra.Command, selector client.PipelineSelector,
	runID string, step client.PipelineStep) error {
	out := command.OutOrStdout()
	progress := command.ErrOrStderr()

	streamContext, cancelStream := context.WithCancel(command.Context())
	isNotFollowing := false
	events, streamError := apiClient.StreamPipelineStepLogs(streamContext, selector, runID, step.ID,
		client.StepLogStreamOptions{IsFollowing: &isNotFollowing})
	if streamError != nil {
		cancelStream()
		// The platform's own sentence is the whole message - it already says
		// the output aged out and that an archived log needs a ready backup
		// vault - and a log that is gone is a missing resource, not a
		// failure worth retrying, so it exits like every other not-found.
		var noLongerRetained *client.PipelineLogNoLongerRetainedError
		if errors.As(streamError, &noLongerRetained) {
			return withExitCode(exitNotFound, streamError)
		}
		return streamError
	}
	// The client's reader goroutine blocks once its channel fills, so a
	// replay abandoned at the idle guard is cancelled and drained rather
	// than left running behind the command.
	defer func() {
		cancelStream()
		for range events {
		}
	}()

	idleTimer := time.NewTimer(pipelineLogReplayIdleTimeout)
	defer idleTimer.Stop()
	printedLines := 0
	for {
		select {
		case <-streamContext.Done():
			return streamContext.Err()
		case event, isStreamOpen := <-events:
			if !isStreamOpen {
				if printedLines == 0 {
					// Said as the stream's answer, not as the step's: inside
					// the retention window an empty replay and a step that
					// printed nothing are the same thing from here.
					_, _ = fmt.Fprintf(progress,
						"The platform's retained log stream held no output for step %q.\n", step.StepKey)
				}
				return nil
			}
			printPipelineLogEvent(out, progress, event)
			if event.Type == "line" {
				printedLines++
			}
			idleTimer.Reset(pipelineLogReplayIdleTimeout)
		case <-idleTimer.C:
			_, _ = fmt.Fprintf(progress,
				"The platform did not end step %q's log replay after %ds without output;"+
					" it is older than the replay this command asked for. Stopping here.\n",
				step.StepKey, int(pipelineLogReplayIdleTimeout.Seconds()))
			return nil
		}
	}
}

// printPipelineLogEvent writes one decoded relay frame: output lines to
// stdout so a redirected log holds only the step's own output, and the
// relay's faults to stderr.
func printPipelineLogEvent(out io.Writer, progress io.Writer, event client.PipelineLogEvent) {
	switch event.Type {
	case "line":
		_, _ = fmt.Fprintf(out, "[%s] %s\n", event.Stream, event.Line)
	case "error":
		_, _ = fmt.Fprintf(progress, "Log stream fault: %s\n", event.Error)
	}
}

// pipelineStepHasLogStream reports whether a step ever reached an execution,
// which is what gives it a subject on the log stream at all. A step that
// never started has no stream to open and no archive to read.
func pipelineStepHasLogStream(step client.PipelineStep) bool {
	return step.ExecutionID != nil && step.ExecutionStepID != nil
}

// pipelineArtifactIsNotFound reports whether an artifact download failed
// because the platform could not find the artifact, as opposed to a refusal
// that describes a state the caller has to report as it stands.
func pipelineArtifactIsNotFound(downloadError error) bool {
	var refusal *client.PipelineArtifactDownloadError
	return errors.As(downloadError, &refusal) && refusal.StatusCode == http.StatusNotFound
}

// countingWriter passes writes through and records how many bytes reached the
// destination, so a caller can tell a failure that printed nothing from one
// that printed half a log.
type countingWriter struct {
	destination io.Writer
	written     int64
}

func (writer *countingWriter) Write(payload []byte) (int, error) {
	count, writeError := writer.destination.Write(payload)
	writer.written += int64(count)
	return count, writeError
}
