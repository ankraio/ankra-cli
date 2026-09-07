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
//
// The fourth way is not a source of output but a wait for one. The moment a
// person asks to follow a step is usually the moment the run was dispatched,
// when the step is still blocked on its dependencies or waiting for the claim
// scan and the relay answers 404 "This step has not started, so it has no log
// stream yet" (go/internal/usecase/pipelines.ErrStepHasNoExecution). Failing
// there sent people back to run the same command again by hand, so --follow
// now polls the run until the step has something to show and then attaches
// exactly as it always did. Without --follow the immediate refusal stands:
// a one-shot read that silently blocked for half an hour would be worse.

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

// The PipelineStep.Status values this command branches on. They are the whole
// vocabulary (enginekit/pipelinerun's StepStatus* set, mirrored by the status
// CHECK constraint in migration pipe_004), which is what lets
// pipelineStepIsWaitingToStart enumerate the states worth waiting in:
// "concluded" is the only terminal one, and how a step ended - skipped,
// cancelled, timed out - is a separate outcome column, not a status of its
// own.
const (
	// pipelineStepStatusBlocked is a step still waiting on its dependencies.
	pipelineStepStatusBlocked = "blocked"
	// pipelineStepStatusPending is a step whose dependencies are satisfied
	// and which the claim scan has not taken yet.
	pipelineStepStatusPending = "pending"
	// pipelineStepStatusRunning is a claimed step. Its execution ids land a
	// moment after the claim, so it is briefly running with no log stream.
	pipelineStepStatusRunning = "running"
	// pipelineStepStatusConcluded is a settled step, shared by the
	// archive-log branch below and readPipelineStep's callers.
	pipelineStepStatusConcluded = "concluded"
)

// pipelineRunStatusConcluded is the PipelineRun.Status value a settled run
// carries. It is the same word a settled step carries but a different
// column, and the wait below needs the run's own answer: a run that finished
// without ever dispatching the step is the one case where waiting longer
// cannot help.
const pipelineRunStatusConcluded = "concluded"

// pipelineStepStartPollInterval is how often `logs --follow` re-reads the run
// while it waits for a step that has not been dispatched yet. Five seconds
// is slower than the two-second reconnect delay above on purpose: nothing is
// being missed while a step is blocked, and a fleet of CI shells tailing
// their own steps should not poll the run route harder than the step's own
// scheduler moves it. It is a var only so the tests can shorten it; nothing
// else writes it.
var pipelineStepStartPollInterval = 5 * time.Second

// pipelineStepStartWaitBound is how long one `logs --follow` invocation will
// spend waiting for its step to start, in total. A step can sit blocked
// behind a queue that is never going to drain - a concurrency group held by
// another run, an agent with no CI workers - and a --follow that never
// returns is worse than one that says what it saw, so the wait is bounded at
// thirty minutes and then reports the same "has not started" refusal a bare
// `logs` call gives immediately.
//
// It is a budget carried across waits rather than a fresh deadline for each
// one, because a step can be waited for more than once: a retry sends it back
// to pending, and thirty minutes per attempt would let a step that keeps
// being retried hold the command open indefinitely in thirty-minute steps -
// exactly the hang the bound exists to prevent. Only time spent waiting is
// spent from it; a step that starts and streams for an hour before being
// retried still has whatever it had left. It is a var only so the tests can
// shorten it; nothing else writes it.
var pipelineStepStartWaitBound = 30 * time.Minute

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
first.

A step that has not started yet has no log stream. With --follow the command
waits for it - saying what it is blocked on - and attaches as soon as the
step starts; a step that concludes without ever starting prints its outcome
and whatever log it does have. One invocation spends at most 30 minutes
waiting, in total across every time the step goes back to waiting. Without
--follow the command says the step has not started and stops.`,
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
	command.Flags().Bool("follow", false,
		"Wait for the step to start if it has not, then keep streaming, "+
			"reconnecting through transient stream faults, until it concludes")
	command.Flags().Bool("replay", false,
		"Also show the output a running step produced before this command connected "+
			"(a concluded step's log is always shown whole)")
}

func runPipelineLogs(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	stepReference, _ := command.Flags().GetString("step")
	follow, _ := command.Flags().GetBool("follow")
	runID = strings.TrimSpace(runID)

	step, resolveError := resolvePipelineStep(command, selector, runID, strings.TrimSpace(stepReference))
	if resolveError != nil {
		return resolveError
	}
	remainingWait := pipelineStepStartWaitBound
	// A loop rather than a straight branch, because the step this command is
	// attached to can move between these three answers under it: one waited
	// for starts (or concludes without starting), and one being tailed can
	// be superseded by a retry. Every pass re-decides from the step row in
	// hand, so no state is reachable only through the one it arrived from.
	for {
		if step.Status == pipelineStepStatusConcluded {
			return runPipelineLogsFromArchive(command, selector, runID, step)
		}
		if !pipelineStepHasLogStream(step) {
			if !follow || !pipelineStepIsWaitingToStart(step) {
				return pipelineStepNotStartedError(step, runID)
			}
			startedStep, unspentWait, waitError := waitForPipelineStepToStart(command, selector,
				runID, step, remainingWait)
			if waitError != nil {
				return waitError
			}
			remainingWait = unspentWait
			step = startedStep
			continue
		}
		nextStep, hasNextStep, streamError := runPipelineLogsFromLiveStream(command, selector,
			runID, step, follow)
		if streamError != nil {
			return streamError
		}
		if !hasNextStep {
			return nil
		}
		step = nextStep
	}
}

// runPipelineLogsFromLiveStream tails a started step over the relay,
// reconnecting while --follow is set. It returns (step, true, nil) when the
// row it was reading stops answering for the step and the caller should
// re-decide from another one: a retry that superseded this attempt with a
// fresh row, or - should the platform ever replan a row in place - one back
// in a pre-dispatch state. The relay answers both the same 404 it answers a
// step that never ran, so under --follow that is a step to pick up again
// rather than a live tail to report as failed.
func runPipelineLogsFromLiveStream(command *cobra.Command, selector client.PipelineSelector, runID string,
	step client.PipelineStep, follow bool) (client.PipelineStep, bool, error) {
	// Only an explicit --replay reaches the wire: the flag's own default is
	// indistinguishable from not passing it, and the route reads an absent
	// `replay` as "decide from the step's status", which is today's
	// behaviour. A resume cursor outranks it server-side, so leaving it set
	// across reconnects cannot re-send output already printed.
	isReplaying, _ := command.Flags().GetBool("replay")
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
					return client.PipelineStep{}, false, sleepError
				}
				continue
			}
			if follow {
				if superseding, wasSuperseded := pipelineStepSupersedingAttempt(command, selector,
					runID, step); wasSuperseded {
					return superseding, true, nil
				}
			}
			return client.PipelineStep{}, false, streamError
		}

		for event := range events {
			printPipelineLogEvent(out, progress, event)
			if event.Type == "line" {
				lastSeq = event.Seq
			}
		}

		refreshed, _, statusError := readPipelineStep(command, selector, runID, step.StepKey)
		if statusError != nil {
			return client.PipelineStep{}, false, statusError
		}
		if follow && refreshed.ID != step.ID {
			// A retry superseded the attempt this connection was reading, so
			// the row in hand can no longer answer for the step. Hand the new
			// attempt back rather than reconnect: it has its own id, its own
			// stream and its own sequence numbering.
			return refreshed, true, nil
		}
		if refreshed.Status == pipelineStepStatusConcluded {
			_, _ = fmt.Fprintln(progress, "Log stream ended: the step has concluded.")
			return client.PipelineStep{}, false, nil
		}
		if !follow {
			_, _ = fmt.Fprintln(progress, "Log stream ended.")
			return client.PipelineStep{}, false, nil
		}
		if pipelineStepIsWaitingToStart(refreshed) {
			return refreshed, true, nil
		}
		// Every reconnect waits, not only a faulted one: a proxy that closes
		// each connection promptly would otherwise be reconnected to as fast
		// as it hangs up. The wait is interruptible so Ctrl+C stops --follow
		// at once rather than at the next network call.
		if sleepError := sleepInterrupted(command.Context(), pipelineLogStreamReconnectDelay); sleepError != nil {
			return client.PipelineStep{}, false, sleepError
		}
	}
}

// waitForPipelineStepToStart holds `logs --follow` open until the step it was
// asked for has something to show. It returns the step to act on: one that
// reached an execution, for the caller to attach the live stream to, or one
// that concluded without ever starting, whose outcome is printed here and
// whose log the caller reads the way it reads any other concluded step's.
//
// It stops early on a run that concluded without dispatching the step (no
// amount of waiting produces a log then), on Ctrl+C through the interruptible
// sleep, and when remainingWait runs out - which reports the same refusal a
// bare `logs` call gives immediately, since giving up is exactly the state
// the command started in.
//
// remainingWait is what is left of pipelineStepStartWaitBound, and the
// unspent part comes back with the step so a later wait for the same step
// continues the same budget instead of restarting it.
func waitForPipelineStepToStart(command *cobra.Command, selector client.PipelineSelector, runID string,
	step client.PipelineStep, remainingWait time.Duration) (client.PipelineStep, time.Duration, error) {
	progress := command.ErrOrStderr()
	announcedReason := ""
	deadline := time.Now().Add(remainingWait)
	for {
		if !time.Now().Before(deadline) {
			return client.PipelineStep{}, 0, pipelineStepNotStartedError(step, runID)
		}
		// One line per distinct reason, not one per poll: a step blocked for
		// twenty minutes must not print two hundred and forty identical
		// lines into whatever is capturing this command's stderr.
		if reason := pipelineStepWaitReason(step); reason != announcedReason {
			_, _ = fmt.Fprintf(progress, "Waiting for step %q to start (%s).\n", step.StepKey, reason)
			announcedReason = reason
		}
		if sleepError := sleepInterrupted(command.Context(), pipelineStepStartPollInterval); sleepError != nil {
			return client.PipelineStep{}, 0, sleepError
		}
		refreshed, runStatus, readError := readPipelineStep(command, selector, runID, step.StepKey)
		if readError != nil {
			return client.PipelineStep{}, 0, readError
		}
		step = refreshed
		if step.Status == pipelineStepStatusConcluded {
			_, _ = fmt.Fprintln(progress, pipelineStepConcludedWhileWaitingLine(step))
			return step, unspentPipelineStepWait(deadline), nil
		}
		if pipelineStepHasLogStream(step) {
			return step, unspentPipelineStepWait(deadline), nil
		}
		// A state the wait does not sit in - a status this build does not
		// know, or one that lost its execution without concluding - is the
		// not-started answer rather than more polling.
		if !pipelineStepIsWaitingToStart(step) {
			return client.PipelineStep{}, 0, pipelineStepNotStartedError(step, runID)
		}
		// Checked after the step, so a run whose last step concluded in the
		// same poll is read from that step rather than from the run.
		if runStatus == pipelineRunStatusConcluded {
			return client.PipelineStep{}, 0, withExitCode(exitNotFound,
				fmt.Errorf("run %s concluded without starting step %q, so it has no log stream - "+
					"check 'ankra pipeline get %s' for what the run did", runID, step.StepKey, runID))
		}
	}
}

// unspentPipelineStepWait is what is left of a wait's budget, floored at zero
// so a deadline already passed hands back nothing rather than a negative
// duration a later wait would read as an enormous one.
func unspentPipelineStepWait(deadline time.Time) time.Duration {
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// pipelineStepWaitReason says why a step has not started yet, in the words
// the wait line prints. A blocked step names the steps it is waiting on,
// because "blocked" on its own does not tell anyone whether waiting is worth
// it; every other state is already its own answer.
func pipelineStepWaitReason(step client.PipelineStep) string {
	if step.Status == pipelineStepStatusBlocked && len(step.DependsOn) > 0 {
		return "blocked on: " + strings.Join(step.DependsOn, ", ")
	}
	if step.Status == "" {
		return "not dispatched yet"
	}
	return step.Status
}

// pipelineStepConcludedWhileWaitingLine reports how a step the wait was
// watching finished. A step that concludes without ever starting - skipped
// because a dependency did not succeed, cancelled with its run, or refused
// before dispatch - has no output to explain itself with, so its outcome and
// the platform's own error message are the whole answer.
func pipelineStepConcludedWhileWaitingLine(step client.PipelineStep) string {
	outcome := "no outcome recorded"
	if step.Outcome != nil && *step.Outcome != "" {
		outcome = *step.Outcome
	}
	if step.ErrorMessage != nil && *step.ErrorMessage != "" {
		return fmt.Sprintf("Step %q concluded while waiting for it to start: %s - %s",
			step.StepKey, outcome, *step.ErrorMessage)
	}
	return fmt.Sprintf("Step %q concluded while waiting for it to start: %s.", step.StepKey, outcome)
}

// pipelineStepNotStartedError is the answer for a step with no log stream:
// the immediate refusal a bare `logs` call gives, and the one the bounded
// wait gives up with. Deliberately the same sentence and the same exit code
// in both cases - "the step has not started" is the same fact whether the
// command established it in one read or in thirty minutes of them, and a
// script that already branches on it should not have to learn a second
// answer to keep working.
func pipelineStepNotStartedError(step client.PipelineStep, runID string) error {
	return fmt.Errorf("step %q has not started, so it has no log stream yet - "+
		"check 'ankra pipeline get %s' for its status", step.StepKey, runID)
}

// pipelineStepSupersedingAttempt reports the step row to act on when the one
// this command was tailing could not be opened. It answers the run's newest
// attempt of the same step key when that row can say something the failed
// connection could not: a different row from the one that failed (a retry), a
// row back to waiting to start, or a row that has concluded.
//
// The relay answers the first two the same 404 it answers a step that never
// ran ("This step has not started, so it has no log stream yet"), and that
// refusal carries no error code to match on - so the step's own state is read
// instead of the sentence. The concluded case is there because a step can
// settle in the gap between resolving it and opening its stream, and under
// --follow the useful answer for a step that finished is its log, not the
// refusal the relay happened to answer on the way.
//
// A read that itself fails answers false, and so does a step that is still
// the newest attempt, still running, with an execution of its own: the
// stream's own error is the better one to report then.
func pipelineStepSupersedingAttempt(command *cobra.Command, selector client.PipelineSelector, runID string,
	step client.PipelineStep) (client.PipelineStep, bool) {
	refreshed, _, readError := readPipelineStep(command, selector, runID, step.StepKey)
	if readError != nil {
		return client.PipelineStep{}, false
	}
	if refreshed.ID != step.ID || refreshed.Status == pipelineStepStatusConcluded ||
		pipelineStepIsWaitingToStart(refreshed) {
		return refreshed, true
	}
	return client.PipelineStep{}, false
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
			// An id names one attempt row exactly, so it is answered as
			// given - that is how a lost attempt's own log stays readable.
			if step.ID == stepReference {
				return step, nil
			}
		}
		// A key names the step, and a retried step is several rows under one
		// key; the newest attempt is the one doing the work now.
		if newest, wasFound := newestPipelineStepAttempt(detail.Steps, stepReference); wasFound {
			return newest, nil
		}
		return client.PipelineStep{}, withExitCode(exitNotFound,
			fmt.Errorf("no step %q on run %s - run 'ankra pipeline get %s' to see the planned steps",
				stepReference, runID, runID))
	}
	// Counted by step key, not by row: a retried step is several rows under
	// one key, and a run with one step that Ankra retried once must not be
	// reported as a run with two steps to choose between.
	stepKeys := []string{}
	seenKeys := map[string]bool{}
	for _, step := range detail.Steps {
		if !seenKeys[step.StepKey] {
			seenKeys[step.StepKey] = true
			stepKeys = append(stepKeys, step.StepKey)
		}
	}
	switch len(stepKeys) {
	case 0:
		return client.PipelineStep{}, fmt.Errorf("run %s has no planned steps yet", runID)
	case 1:
		newest, _ := newestPipelineStepAttempt(detail.Steps, stepKeys[0])
		return newest, nil
	default:
		return client.PipelineStep{}, withExitCode(exitUsage,
			fmt.Errorf("run %s has %d steps - pass --step to name the one to follow", runID, len(stepKeys)))
	}
}

// readPipelineStep re-reads a step's newest attempt, and the status of the
// run carrying it. It is a full run fetch because the API has no single-step
// read on this surface; the run detail is small enough that reading it once
// per disconnect, or once per wait interval, is not a cost worth a dedicated
// route for. The run's status comes back with the step because a step stuck
// before dispatch and a run that finished without ever dispatching it look
// identical from the step row alone.
//
// It re-reads by step key rather than by the row id it was following,
// because a step id does not survive a retry: the platform concludes the
// lost attempt and inserts a fresh row at attempt+1
// (enginekit/pipelinerun's insertRetryAttempt, used by both
// RetryStepAfterInfraError and ReapStrandedSteps), keeping the lost attempt
// on the run as evidence. Following the id would report a retried step as
// concluded on the attempt Ankra itself threw away.
func readPipelineStep(command *cobra.Command, selector client.PipelineSelector, runID string,
	stepKey string) (step client.PipelineStep, runStatus string, readError error) {
	detail, getError := apiClient.GetPipelineRun(command.Context(), selector, runID)
	if getError != nil {
		return client.PipelineStep{}, "", getError
	}
	newest, wasFound := newestPipelineStepAttempt(detail.Steps, stepKey)
	if !wasFound {
		return client.PipelineStep{}, "", withExitCode(exitNotFound,
			fmt.Errorf("step %q is no longer on run %s", stepKey, runID))
	}
	return newest, detail.Status, nil
}

// newestPipelineStepAttempt picks the live row for a step key: a run carries
// every attempt of a retried step, oldest first, and only the last of them
// describes what the step is doing now. The attempt number is compared rather
// than the listing's order taken on trust, since which row answers for the
// step is not a detail worth inheriting from a server-side ORDER BY.
func newestPipelineStepAttempt(steps []client.PipelineStep, stepKey string) (client.PipelineStep, bool) {
	var newest client.PipelineStep
	wasFound := false
	for _, candidate := range steps {
		if candidate.StepKey != stepKey {
			continue
		}
		if !wasFound || candidate.Attempt > newest.Attempt {
			newest, wasFound = candidate, true
		}
	}
	return newest, wasFound
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
		// 404 only, deliberately: the download's other refusals are not an
		// object the retained stream could answer for instead. Its 410 in
		// particular says the retention sweep removed the object, and
		// artifact retention is counted in whole days with a floor of one
		// (pipelineartifacts.EffectiveRetentionDays), so a swept artifact is
		// already at least as old as the stream's entire window
		// (pipelinerun.OutputRetention, 24h) - the frames are gone too.
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
	sawStreamFault := false
	for {
		select {
		case <-streamContext.Done():
			return streamContext.Err()
		case event, isStreamOpen := <-events:
			if !isStreamOpen {
				// Said as the stream's answer, not as the step's: inside the
				// retention window an empty replay and a step that printed
				// nothing are the same thing from here. A replay that
				// faulted is not said at all - the fault is already on
				// stderr, and a read that broke observed nothing about the
				// output either way.
				if printedLines == 0 && !sawStreamFault {
					_, _ = fmt.Fprintf(progress,
						"The platform's retained log stream held no output for step %q.\n", step.StepKey)
				}
				return nil
			}
			printPipelineLogEvent(out, progress, event)
			switch event.Type {
			case "line":
				printedLines++
			case "error":
				sawStreamFault = true
			}
			// A bare Reset, deliberately. This module's go directive is
			// 1.25, and from go1.23 a timer's channel is unbuffered and
			// drained by Stop and Reset, so a tick that fired while this
			// case was being chosen cannot survive into the next select.
			// The pre-1.23 "if !Stop() { <-C }" idiom would be wrong here -
			// under these semantics that receive can block.
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

// pipelineStepIsWaitingToStart reports whether a step has no log stream yet
// but is in a state the scheduler still moves it out of - so one is coming
// and is worth waiting for.
//
// It names the states it waits in rather than asking what the step is not.
// A step that concluded without ever reaching an execution (skipped because
// a dependency failed, cancelled with its run) also has no stream, and
// "anything without an execution" would spend the whole wait bound on one
// that can never start. The same reasoning covers a status this build does
// not know: an addition to the vocabulary is at least as likely to be
// terminal as pre-dispatch, so it is not waited on either. "running" is
// included for the narrow race it is - the row is claimed and its execution
// ids land a moment later.
func pipelineStepIsWaitingToStart(step client.PipelineStep) bool {
	if pipelineStepHasLogStream(step) {
		return false
	}
	switch step.Status {
	case pipelineStepStatusBlocked, pipelineStepStatusPending, pipelineStepStatusRunning:
		return true
	default:
		return false
	}
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
