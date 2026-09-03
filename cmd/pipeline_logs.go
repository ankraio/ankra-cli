package cmd

// The step log relay: go/internal/pipelineapi/streams.go over the shared
// execution_output JetStream stream. See internal/client/pipeline_logs.go for
// the wire contract and its one real limitation: a fresh connection has no
// history to replay, so this command only ever shows output produced from the
// moment it connects.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// pipelineLogStreamReconnectDelay is how long `logs --follow` waits before
// reconnecting after the relay's own error frame or a stream fault, so a
// transient disconnect does not spin the CLI in a tight retry loop.
const pipelineLogStreamReconnectDelay = 2 * time.Second

func newPipelineLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <run>",
		Short: "Show a pipeline step's live output",
		Long: `Show a pipeline step's live output over the log relay.

The relay has no history yet: a fresh connection only sees lines produced
from the moment it connects, not what the step already printed before then
(the durable per-step log artifact this will read from instead has not
shipped). Without --follow, the command tails the step until it concludes and
then stops; with --follow it does the same, so the difference is mainly
diagnostic - a bare 'logs' exits with the step's outcome, 'logs --follow'
keeps reconnecting through a dropped stream instead of giving up.`,
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
				time.Sleep(retryAfter)
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
		// as it hangs up.
		time.Sleep(pipelineLogStreamReconnectDelay)
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
			return step.Status == "concluded", nil
		}
	}
	return false, withExitCode(exitNotFound, fmt.Errorf("step %s is no longer on run %s", stepID, runID))
}
