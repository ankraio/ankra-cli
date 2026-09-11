package cmd

// Waiting on a pipeline run, and watching one (ankra-w1i58): the part of the
// run lifecycle that does not depend on who started the run. `run --wait` and
// `rerun --wait` only ever waited on the run they had just dispatched, but
// most runs are started by a push or pull request webhook, so `pipeline get`
// waits on - or streams the state changes of - any run, named by its id or
// selected by what a CI job or an agent actually knows about it: the commit,
// the branch, the trigger.
//
// The platform publishes no stream of run or step state (the only SSE route
// under go/internal/pipelineapi is the step log relay), so every mode here
// reads the run detail on an interval. One read answers the run with every
// step, and the pipeline routes carry no rate limit, so the interval is the
// CLI's own choice rather than something each caller has to tune.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// pipelineRunWaitPollInterval is how often a wait or a watch re-reads the run
// while it is in flight. It is a var only so the tests can shorten it;
// nothing else writes it.
var pipelineRunWaitPollInterval = 3 * time.Second

// pipelineRunAppearWaitBound is how long a wait or a watch that selected its
// run by --head-sha/--branch/--trigger waits for a matching run to appear
// when no --timeout bounds it. The webhook that creates a push or pull
// request run lands seconds after the event, so a caller asking right after
// a push is usually just early and waiting is the answer - but a run whose
// event matched none of the pipeline's triggers never appears at all, and
// waiting for it without end is the hang this bound exists to prevent. It is
// a var only so the tests can shorten it; nothing else writes it.
var pipelineRunAppearWaitBound = 10 * time.Minute

// maxConsecutivePipelineRunReadFailures is how many failed reads in a row a
// wait rides out once it has read the run at least once. A wait of forty-five
// minutes spans platform deploys, and one refused connection partway through
// must not be reported the way a failed run is. At the poll interval above
// this is about a minute of the platform being unreachable before the wait
// gives up with the last read's own error.
const maxConsecutivePipelineRunReadFailures = 20

// pipelineRunSelectionProbeLimit is how many matching runs a selection
// without --latest asks for: two would be enough to know the answer is
// ambiguous, and a few more let the refusal name the candidates.
const pipelineRunSelectionProbeLimit = 5

// The two kinds of line --watch writes: a change of the run's own state, or a
// change of one step attempt's.
const (
	pipelineRunWatchEventRun  = "run"
	pipelineRunWatchEventStep = "step"
)

// registerPipelineWaitTimeoutFlag adds --timeout to a command that can wait on
// a run. Zero, the default, waits as long as the run takes: the platform
// concludes every run eventually (each step carries its own timeout, and a
// stranded one is reaped), so all an unbounded wait can outlast is the
// caller's patience, and Ctrl+C answers that.
func registerPipelineWaitTimeoutFlag(command *cobra.Command) {
	command.Flags().Duration("timeout", 0,
		"Give up waiting after this long and exit 5, e.g. 45m (default: wait until the run concludes)")
}

// pipelineWaitTimeoutFromFlags reads --timeout for a command about to decide
// whether it waits. A timeout on an invocation that does not wait is refused
// rather than ignored, since it would otherwise read as a bound the command
// never applies; waitFlags names the flags that would make it wait.
func pipelineWaitTimeoutFromFlags(command *cobra.Command, isWaiting bool, waitFlags string) (time.Duration, error) {
	timeout, _ := command.Flags().GetDuration("timeout")
	if timeout < 0 {
		return 0, withExitCode(exitUsage, fmt.Errorf("--timeout must be a positive duration, got %s", timeout))
	}
	if command.Flags().Changed("timeout") && !isWaiting {
		return 0, withExitCode(exitUsage, fmt.Errorf("--timeout bounds a wait: pass it with %s", waitFlags))
	}
	return timeout, nil
}

// pipelineRunWait is one wait's budget. The --timeout deadline, when there is
// one, rides on requestContext so it bounds an in-flight read as well as the
// sleeps between reads; parentContext is kept to tell that deadline apart
// from the command being interrupted.
type pipelineRunWait struct {
	requestContext context.Context
	cancel         context.CancelFunc
	parentContext  context.Context
	timeout        time.Duration
}

// startPipelineRunWait begins a wait bounded by timeout, or by nothing but the
// command's own context when timeout is zero. The caller must call stop.
func startPipelineRunWait(parentContext context.Context, timeout time.Duration) *pipelineRunWait {
	wait := &pipelineRunWait{parentContext: parentContext, timeout: timeout}
	if timeout > 0 {
		wait.requestContext, wait.cancel = context.WithTimeout(parentContext, timeout)
	} else {
		wait.requestContext, wait.cancel = context.WithCancel(parentContext)
	}
	return wait
}

func (wait *pipelineRunWait) stop() {
	wait.cancel()
}

// hasExpired reports whether --timeout, and not an interrupt, ended the wait.
func (wait *pipelineRunWait) hasExpired() bool {
	return wait.parentContext.Err() == nil && errors.Is(wait.requestContext.Err(), context.DeadlineExceeded)
}

// sleep waits one poll interval, returning early with the context's error
// when the deadline passes or the command is interrupted.
func (wait *pipelineRunWait) sleep() error {
	return sleepInterrupted(wait.requestContext, pipelineRunWaitPollInterval)
}

// waitForPipelineRunConclusion polls the run until it concludes, saying on
// stderr each status it passes through. It is the wait `run --wait`,
// `rerun --wait` and `get --wait` share.
func waitForPipelineRunConclusion(command *cobra.Command, selector client.PipelineSelector, runID string,
	wait *pipelineRunWait) (*client.PipelineRunDetail, error) {
	progress := command.ErrOrStderr()
	announcedStatus := ""
	return pollPipelineRun(command, selector, runID, wait, func(detail *client.PipelineRunDetail) error {
		if detail.Status != announcedStatus {
			_, _ = fmt.Fprintf(progress, "Run #%d is %s.\n", detail.RunNumber, detail.Status)
			announcedStatus = detail.Status
		}
		return nil
	})
}

// pollPipelineRun reads the run until it concludes, handing every read to
// observe, and answers the concluded run. Every wait and watch goes through
// it, so they agree on when a run is over, on the deadline, and on which
// failed reads are worth riding out.
//
// Only reads after the first successful one are retried. A first read that
// fails is the command's answer as it stands - a mistyped id or a refused
// token will not improve - while a failure after the run has been read once
// is most often the platform restarting under a long wait.
func pollPipelineRun(command *cobra.Command, selector client.PipelineSelector, runID string,
	wait *pipelineRunWait, observe func(detail *client.PipelineRunDetail) error) (*client.PipelineRunDetail, error) {
	progress := command.ErrOrStderr()
	var lastRead *client.PipelineRunDetail
	failedReads := 0
	for {
		detail, getError := apiClient.GetPipelineRun(wait.requestContext, selector, runID)
		if getError == nil && detail == nil {
			getError = fmt.Errorf("the platform answered no detail for run %s", runID)
		}
		switch {
		case getError == nil:
			if failedReads > 0 {
				_, _ = fmt.Fprintf(progress, "Read run #%d again after %d failed attempts.\n",
					detail.RunNumber, failedReads)
			}
			failedReads = 0
			lastRead = detail
			if observeError := observe(detail); observeError != nil {
				return nil, observeError
			}
			if detail.Status == pipelineRunStatusConcluded {
				return detail, nil
			}
		case wait.hasExpired():
			return nil, pipelineRunWaitExpiredError(runID, lastRead, wait.timeout)
		case lastRead == nil || !pipelineRunReadIsWorthRetrying(wait, getError) ||
			failedReads >= maxConsecutivePipelineRunReadFailures:
			return nil, getError
		default:
			failedReads++
			if failedReads == 1 {
				_, _ = fmt.Fprintf(progress, "Could not read run #%d (%v); still waiting.\n",
					lastRead.RunNumber, getError)
			}
		}
		if sleepError := wait.sleep(); sleepError != nil {
			if wait.hasExpired() {
				return nil, pipelineRunWaitExpiredError(runID, lastRead, wait.timeout)
			}
			return nil, sleepError
		}
	}
}

// pipelineRunReadIsWorthRetrying separates a read that failed because the
// platform could not be reached or answered with a server fault - which a
// deploy or a dropped connection produces, and the next read usually does
// not - from one no number of retries will change: the credentials or the
// role were refused, the request itself was wrong, or the command was
// interrupted.
func pipelineRunReadIsWorthRetrying(wait *pipelineRunWait, readError error) bool {
	if wait.parentContext.Err() != nil {
		return false
	}
	if errors.Is(readError, client.ErrUnauthorized) {
		return false
	}
	var permissionDenied *client.PermissionDeniedError
	if errors.As(readError, &permissionDenied) {
		return false
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode >= 400 && unexpected.StatusCode < 500 &&
		unexpected.StatusCode != http.StatusRequestTimeout && unexpected.StatusCode != http.StatusTooManyRequests {
		return false
	}
	return true
}

// pipelineRunWaitExpiredError is the answer when --timeout runs out before
// the run concludes. It exits 5, the code the scripting contract reserves for
// a wait that expired, and says the run is still going: giving up waiting
// does not cancel it.
func pipelineRunWaitExpiredError(runID string, lastRead *client.PipelineRunDetail, timeout time.Duration) error {
	if lastRead == nil {
		return withExitCode(exitWaitTimeout, fmt.Errorf("gave up after %s without reading run %s", timeout, runID))
	}
	return withExitCode(exitWaitTimeout, fmt.Errorf(
		"run #%d had not concluded after %s (last seen %s); it is still going - wait again with 'ankra pipeline get %s --wait'",
		lastRead.RunNumber, timeout, lastRead.Status, runID))
}

// renderConcludedPipelineRun prints a waited-for run's final detail and
// answers its conclusion as the command's result, so a run that did not
// succeed exits non-zero in every output format. The structured branch used
// to return straight after encoding, which made `run --wait -o json` exit 0
// on a failed run - the one thing --wait exists to report. The error reaches
// stderr, so stdout still holds only the document.
func renderConcludedPipelineRun(command *cobra.Command, format outputFormat, detail *client.PipelineRunDetail) error {
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, detail); encodeError != nil {
			return encodeError
		}
	} else {
		printPipelineRunDetail(command.OutOrStdout(), *detail)
	}
	return pipelineRunConclusionError(detail.PipelineRun)
}

// pipelineRunExitCodeError is --exit-code's answer for a run read once: its
// conclusion when it has one, and exit 5 while it has none. A run still in
// flight must not exit 0 - a script gating a deploy on this command would
// read that as a pass - and it is not a failure either, so it takes the code
// a wait that ran out takes: no conclusion yet, ask again.
func pipelineRunExitCodeError(run client.PipelineRun) error {
	if run.Status != pipelineRunStatusConcluded {
		return withExitCode(exitWaitTimeout, fmt.Errorf("run #%d has not concluded yet (status: %s)",
			run.RunNumber, run.Status))
	}
	return pipelineRunConclusionError(run)
}

// pipelineRunSelection names a run by what the caller knows about it rather
// than its id: the --head-sha/--branch/--trigger filters the run listing
// already takes, and whether the newest match is wanted when several match.
type pipelineRunSelection struct {
	HeadSHA  string
	Branch   string
	Trigger  string
	IsLatest bool
}

func pipelineRunSelectionFromFlags(command *cobra.Command) pipelineRunSelection {
	headSHA, _ := command.Flags().GetString("head-sha")
	branch, _ := command.Flags().GetString("branch")
	trigger, _ := command.Flags().GetString("trigger")
	isLatest, _ := command.Flags().GetBool("latest")
	return pipelineRunSelection{
		HeadSHA:  strings.TrimSpace(headSHA),
		Branch:   strings.TrimSpace(branch),
		Trigger:  strings.TrimSpace(trigger),
		IsLatest: isLatest,
	}
}

func (selection pipelineRunSelection) isEmpty() bool {
	return selection.filterDescription() == "" && !selection.IsLatest
}

// filterDescription renders the selection's filters for the lines that say
// which run was picked or what is being waited for, or "" when it has none.
func (selection pipelineRunSelection) filterDescription() string {
	filters := []string{}
	if selection.HeadSHA != "" {
		filters = append(filters, "head_sha "+selection.HeadSHA)
	}
	if selection.Branch != "" {
		filters = append(filters, "branch "+selection.Branch)
	}
	if selection.Trigger != "" {
		filters = append(filters, "trigger "+selection.Trigger)
	}
	return strings.Join(filters, ", ")
}

// subject renders the runs the selection matches, as a noun phrase.
func (selection pipelineRunSelection) subject() string {
	if filters := selection.filterDescription(); filters != "" {
		return "a run with " + filters
	}
	return "a run of this pipeline"
}

// resolvePipelineRunSelection answers the id of the run a selection names.
// Exactly one matching run is that run. Several are refused unless --latest
// asked for the newest, since guessing which of two builds of one commit the
// caller meant would report the wrong one's outcome. None is not-found -
// unless the command is about to wait (wait is non-nil), in which case the
// run is most likely a webhook a moment behind the caller, and the command
// waits for it to appear. Once one listing has succeeded, that wait rides
// out failed listings the way pollPipelineRun rides out failed reads, since
// it can span a platform deploy just the same; a first listing that fails is
// the answer as it stands, since a refused filter will not improve.
func resolvePipelineRunSelection(command *cobra.Command, selector client.PipelineSelector,
	selection pipelineRunSelection, wait *pipelineRunWait) (string, error) {
	progress := command.ErrOrStderr()
	options := client.ListPipelineRunsOptions{
		HeadSHA: selection.HeadSHA,
		Branch:  selection.Branch,
		Trigger: selection.Trigger,
		Limit:   pipelineRunSelectionProbeLimit,
	}
	if selection.IsLatest {
		options.Limit = 1
	}
	requestContext := command.Context()
	if wait != nil {
		requestContext = wait.requestContext
	}
	appearDeadline := time.Now().Add(pipelineRunAppearWaitBound)
	hasAnnouncedWait := false
	hasListed := false
	failedListings := 0
	for {
		page, listError := apiClient.ListPipelineRuns(requestContext, selector, options)
		switch {
		case listError == nil:
			hasListed = true
			failedListings = 0
			if page == nil {
				page = &client.PipelineRunList{}
			}
			if len(page.Runs) > 1 && !selection.IsLatest {
				return "", pipelineRunSelectionAmbiguousError(selection, page)
			}
			if len(page.Runs) > 0 {
				chosen := page.Runs[0]
				qualifier := "the only"
				if selection.IsLatest {
					qualifier = "the newest"
				}
				_, _ = fmt.Fprintf(progress, "Using run #%d (%s), %s %s.\n", chosen.RunNumber, chosen.ID,
					qualifier, strings.TrimPrefix(selection.subject(), "a "))
				return chosen.ID, nil
			}
			if wait == nil {
				return "", withExitCode(exitNotFound, fmt.Errorf(
					"there is no %s - see its runs with 'ankra pipeline list'", strings.TrimPrefix(selection.subject(), "a ")))
			}
			if !hasAnnouncedWait {
				_, _ = fmt.Fprintf(progress, "Waiting for %s to appear.\n", selection.subject())
				hasAnnouncedWait = true
			}
		case wait != nil && wait.hasExpired():
			return "", pipelineRunSelectionExpiredError(selection, wait.timeout)
		case wait == nil || !hasListed || !pipelineRunReadIsWorthRetrying(wait, listError) ||
			failedListings >= maxConsecutivePipelineRunReadFailures:
			return "", listError
		default:
			failedListings++
			if failedListings == 1 {
				_, _ = fmt.Fprintf(progress, "Could not list the pipeline's runs (%v); still waiting.\n", listError)
			}
		}
		if wait.timeout == 0 && !time.Now().Before(appearDeadline) {
			return "", pipelineRunSelectionExpiredError(selection, pipelineRunAppearWaitBound)
		}
		if sleepError := wait.sleep(); sleepError != nil {
			if wait.hasExpired() {
				return "", pipelineRunSelectionExpiredError(selection, wait.timeout)
			}
			return "", sleepError
		}
	}
}

// pipelineRunSelectionAmbiguousError refuses a selection several runs match,
// naming them so the caller can pick one or narrow the match.
func pipelineRunSelectionAmbiguousError(selection pipelineRunSelection, page *client.PipelineRunList) error {
	candidates := make([]string, 0, len(page.Runs))
	for _, run := range page.Runs {
		candidates = append(candidates, fmt.Sprintf("#%d %s (%s, %s)",
			run.RunNumber, run.ID, run.Trigger, pipelineOutcomeLabel(run.Status, run.Outcome)))
	}
	count := fmt.Sprintf("%d runs", len(page.Runs))
	if page.NextCursor != nil && *page.NextCursor != "" {
		count = fmt.Sprintf("more than %d runs", len(page.Runs))
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"%s have %s: %s - pass --latest to take the newest, narrow the match with --trigger or --branch, "+
			"or name the run by its id", count, selection.filterDescription(), strings.Join(candidates, "; ")))
}

// pipelineRunSelectionExpiredError is the answer when no run matching the
// selection appeared within the wait. It exits 5 like every expired wait.
func pipelineRunSelectionExpiredError(selection pipelineRunSelection, waited time.Duration) error {
	return withExitCode(exitWaitTimeout, fmt.Errorf(
		"no %s appeared within %s - check that the pipeline's triggers match that event "+
			"('ankra pipeline validate' shows what a push and a pull request would run), or pass --timeout to wait longer",
		strings.TrimPrefix(selection.subject(), "a "), waited))
}

// pipelineRunWatchEvent is one line `get --watch` writes: a state of the run,
// or of one step attempt, that the command observed for the first time.
// Status, PreviousStatus and Outcome are lifted to the top level so a
// consumer can filter on them without knowing which kind of object changed;
// Run or Step carries the whole object as the platform answered it.
// PreviousStatus is null on the first observation of each run and step.
type pipelineRunWatchEvent struct {
	Event          string               `json:"event"`
	ObservedAt     string               `json:"observed_at"`
	PipelineRunID  string               `json:"pipeline_run_id"`
	RunNumber      int64                `json:"run_number"`
	StepKey        string               `json:"step_key,omitempty"`
	Status         string               `json:"status"`
	PreviousStatus *string              `json:"previous_status"`
	Outcome        *string              `json:"outcome"`
	Run            *client.PipelineRun  `json:"run,omitempty"`
	Step           *client.PipelineStep `json:"step,omitempty"`
}

// pipelineStateKey is what makes an observation a change: a run or step whose
// status and outcome are both what they were has nothing new to say, even
// when a timestamp on it moved.
type pipelineStateKey struct {
	status  string
	outcome string
}

func pipelineStateKeyOf(status string, outcome *string) pipelineStateKey {
	return pipelineStateKey{status: status, outcome: pipelineOptionalString(outcome)}
}

// watchPipelineRun writes one event per run or step state change until the
// run concludes: a line of text each by default, or one JSON object per line
// with -o json. The first read writes the state of the run and every step it
// has, so a consumer never has to read the run separately to know where it
// started. Within one read, step events come before the run's own, so the
// run's conclusion is always the last line. A retried step is a new attempt
// row with its own id, so it appears as a step seen for the first time.
//
// Like --wait, the command exits non-zero unless the run succeeded.
func watchPipelineRun(command *cobra.Command, selector client.PipelineSelector, runID string,
	format outputFormat, wait *pipelineRunWait) error {
	out := command.OutOrStdout()
	var encoder *json.Encoder
	if format == outputJSON {
		encoder = json.NewEncoder(out)
	}
	emit := func(event pipelineRunWatchEvent) error {
		if encoder != nil {
			return encoder.Encode(event)
		}
		_, writeError := fmt.Fprintln(out, pipelineRunWatchLine(event))
		return writeError
	}

	stepStates := map[string]pipelineStateKey{}
	var runState pipelineStateKey
	hasSeenRun := false
	detail, pollError := pollPipelineRun(command, selector, runID, wait, func(detail *client.PipelineRunDetail) error {
		observedAt := time.Now().UTC().Format(time.RFC3339)
		for index := range detail.Steps {
			step := detail.Steps[index]
			current := pipelineStateKeyOf(step.Status, step.Outcome)
			previous, hasSeenStep := stepStates[step.ID]
			if hasSeenStep && previous == current {
				continue
			}
			stepStates[step.ID] = current
			event := pipelineRunWatchEvent{
				Event:         pipelineRunWatchEventStep,
				ObservedAt:    observedAt,
				PipelineRunID: detail.ID,
				RunNumber:     detail.RunNumber,
				StepKey:       step.StepKey,
				Status:        step.Status,
				Outcome:       step.Outcome,
				Step:          &step,
			}
			if hasSeenStep {
				previousStatus := previous.status
				event.PreviousStatus = &previousStatus
			}
			if emitError := emit(event); emitError != nil {
				return emitError
			}
		}
		current := pipelineStateKeyOf(detail.Status, detail.Outcome)
		if hasSeenRun && current == runState {
			return nil
		}
		run := detail.PipelineRun
		event := pipelineRunWatchEvent{
			Event:         pipelineRunWatchEventRun,
			ObservedAt:    observedAt,
			PipelineRunID: detail.ID,
			RunNumber:     detail.RunNumber,
			Status:        detail.Status,
			Outcome:       detail.Outcome,
			Run:           &run,
		}
		if hasSeenRun {
			previousStatus := runState.status
			event.PreviousStatus = &previousStatus
		}
		hasSeenRun, runState = true, current
		return emit(event)
	})
	if pollError != nil {
		return pollError
	}
	return pipelineRunConclusionError(detail.PipelineRun)
}

// pipelineRunWatchLine renders one event as a line of text: when it was seen,
// what changed, and - once a step or the run has concluded - its exit code
// and the platform's own error message. The state is printed as a plain word
// rather than through renderColouredStatus, whose palette predates the
// pipeline outcomes (it paints "failure" in the in-progress yellow) and
// writes terminal escapes into a stream that is as often a CI log as a
// terminal.
func pipelineRunWatchLine(event pipelineRunWatchEvent) string {
	label := fmt.Sprintf("run #%d", event.RunNumber)
	var exitCode *int32
	var errorMessage *string
	switch {
	case event.Step != nil:
		label = "step " + event.StepKey
		if event.Step.Attempt > 1 {
			label += fmt.Sprintf(" (attempt %d)", event.Step.Attempt)
		}
		exitCode = event.Step.ExitCode
		errorMessage = event.Step.ErrorMessage
	case event.Run != nil:
		errorMessage = event.Run.ErrorMessage
	}
	line := fmt.Sprintf("%s  %s  %s", event.ObservedAt, label, pipelineOutcomeLabel(event.Status, event.Outcome))
	if event.Status != pipelineRunStatusConcluded {
		return line
	}
	if exitCode != nil {
		line += fmt.Sprintf("  exit %d", *exitCode)
	}
	if errorMessage != nil && *errorMessage != "" {
		line += "  " + *errorMessage
	}
	return line
}
