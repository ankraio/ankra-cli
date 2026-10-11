package cmd

// The run lifecycle: dispatch, list, get, cancel, rerun. Each RunE here
// resolves its own selector from --application/--repository and then calls
// the shared runPipeline* function; cmd/application_pipeline.go calls the
// same functions with a selector forced from a leading <application-id>
// argument, so the two surfaces cannot drift. Waiting on a run and watching
// one live in cmd/pipeline_wait.go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newPipelineRunCommand() *cobra.Command {
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Dispatch a manual pipeline run",
		Long: `Dispatch a manual run of a pipeline's stored definition.

A run executes one commit, and Ankra checks it against the repository's own
host before anything is queued: a --sha that is not reachable from --ref (or
the repository's default branch) on that repository is refused.

Without --sha, the commit is:

  - the working directory's HEAD, when the checkout is the repository the
    pipeline builds and --ref is not given. --application is read from the
    checkout too, so 'ankra pipeline run' on its own runs what you are
    looking at;
  - otherwise the tip of --ref, or of the default branch, which Ankra reads
    from the repository's host when it dispatches.

A checkout of any other repository never supplies the commit, so
'ankra pipeline run --application other-app' from the wrong directory runs
other-app's default branch, not your local HEAD.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			target, targetError := resolvePipelineTarget(command)
			if targetError != nil {
				return targetError
			}
			return runPipelineDispatch(command, target)
		},
	}
	registerPipelineSelectorFlags(runCommand)
	registerPipelineRunDispatchFlags(runCommand)
	registerStructuredOutputFlags(runCommand)
	return runCommand
}

// registerPipelineRunDispatchFlags is shared by `pipeline run` and
// `application pipeline run`.
func registerPipelineRunDispatchFlags(command *cobra.Command) {
	command.Flags().String("ref", "", "Git reference to run at (defaults to the repository's default branch)")
	command.Flags().String("sha", "", "Full commit sha to run (defaults to HEAD of a checkout of this pipeline's repository, else the tip of --ref)")
	command.Flags().StringArray("input", nil, "Dispatch input as key=value (repeatable)")
	command.Flags().String("reason", "", "Human note recorded on the run")
	command.Flags().String("spec-file", "", "Run this pipeline definition instead of the stored one (requires pipelines.manage)")
	command.Flags().Bool("wait", false, "Wait for the run to conclude before returning")
	registerPipelineWaitTimeoutFlag(command)
}

// localHeadCommit answers the working directory's HEAD commit and the branch
// it is on, or empty strings when there is no checkout to read. A detached
// HEAD has a commit and no branch name, which is exactly what the dispatch
// wants: the sha is what runs, and the ref is a label on it.
func localHeadCommit(requestContext context.Context) (string, string) {
	repositoryRoot, rootError := executeGit(requestContext, ".", "rev-parse", "--show-toplevel")
	if rootError != nil {
		return "", ""
	}
	headSHA, shaError := executeGit(requestContext, repositoryRoot, "rev-parse", "HEAD")
	if shaError != nil {
		return "", ""
	}
	headSHA = strings.TrimSpace(headSHA)
	if headSHA == "" {
		return "", ""
	}
	branch, branchError := executeGit(requestContext, repositoryRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if branchError != nil || strings.TrimSpace(branch) == "HEAD" {
		return headSHA, ""
	}
	return headSHA, strings.TrimSpace(branch)
}

// checkoutBuildsSelectedPipeline answers whether the working directory is a
// checkout of the repository the selected pipeline builds: the one case in
// which its HEAD is a commit of that pipeline's repository.
//
// A target inferred from the working directory already knows: the walk that
// inferred it matched the checkout's origin to the application, and asking
// the listing again would only rediscover that at the same cost - or, when a
// page of the second walk failed, forget it (ankra-4dq9l). Only a target the
// user named by --application needs the listing read, and it is read once.
//
// A --repository selector names a pipeline repository by id, and there is no
// lookup from that id to "owner/name" here, so a checkout cannot be matched
// to it and the answer is no. So is every incomplete answer from the listing:
// sending a foreign commit is worse than letting the platform read the tip.
func checkoutBuildsSelectedPipeline(requestContext context.Context, target pipelineTarget) bool {
	if target.checkoutIsRepository {
		return true
	}
	if target.selector.ApplicationID == "" {
		return false
	}
	_, applicationIDs, _, known := checkoutApplications(requestContext)
	if !known {
		return false
	}
	for _, applicationID := range applicationIDs {
		if strings.EqualFold(applicationID, target.selector.ApplicationID) {
			return true
		}
	}
	return false
}

// runPipelineDispatch reads the dispatch flags and drives the shared
// CreatePipelineRun call; used by both `pipeline run` and
// `application pipeline run`. The target carries what resolving the selector
// learned about the working directory, so the applications listing is walked
// at most once per dispatch: by the inference that produced the target, or
// by the checkout match for a target the user named, never both.
func runPipelineDispatch(command *cobra.Command, target pipelineTarget) error {
	selector := target.selector
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	ref, _ := command.Flags().GetString("ref")
	sha, _ := command.Flags().GetString("sha")
	reason, _ := command.Flags().GetString("reason")
	specFile, _ := command.Flags().GetString("spec-file")
	rawInputs, _ := command.Flags().GetStringArray("input")
	wait, _ := command.Flags().GetBool("wait")
	timeout, timeoutError := pipelineWaitTimeoutFromFlags(command, wait, "--wait")
	if timeoutError != nil {
		return timeoutError
	}

	sha = strings.TrimSpace(sha)
	ref = strings.TrimSpace(ref)
	if sha == "" {
		// A user standing in the checkout has the sha under their cursor, and
		// asking them to paste `git rev-parse HEAD` back is a step the
		// command can take off them (ankra-ctsmd). The checkout answers only
		// when it is the whole question:
		//   - a --ref the user named is NOT paired with whatever the working
		//     directory has checked out: running "release-2.0" from a checkout
		//     sitting on main would dispatch main's commit under that ref;
		//   - a checkout of a DIFFERENT repository answers nothing: its HEAD
		//     is not a commit of the pipeline being run (PLA-863).
		if ref == "" {
			localSHA, localRef := localHeadCommit(command.Context())
			if localSHA != "" && checkoutBuildsSelectedPipeline(command.Context(), target) {
				sha = localSHA
				ref = localRef
				_, _ = fmt.Fprintf(command.ErrOrStderr(),
					"Running at the working directory's HEAD %s (pass --sha to choose another).\n", sha)
			}
		}
		if sha == "" {
			// No commit named and none to read: the dispatch goes out without
			// one and the platform reads the tip of the ref, or of the
			// repository's default branch, from the repository's host
			// (verifyRunProvenance in cluster, since cluster#2612). That is a
			// live read at dispatch, not a commit the platform stored earlier.
			target := "the repository's default branch"
			if ref != "" {
				target = ref
			}
			_, _ = fmt.Fprintf(command.ErrOrStderr(),
				"Running at the tip of %s, resolved by Ankra at dispatch (pass --sha to run a specific commit).\n", target)
		}
	}
	inputs, inputsError := parsePipelineInputFlags(rawInputs)
	if inputsError != nil {
		return inputsError
	}
	var specYAML string
	if strings.TrimSpace(specFile) != "" {
		contents, readError := readApplicationFile(specFile)
		if readError != nil {
			return readError
		}
		specYAML = string(contents)
	}

	result, createError := apiClient.CreatePipelineRun(command.Context(), selector, client.CreatePipelineRunRequest{
		Ref:      ref,
		HeadSHA:  sha,
		Inputs:   inputs,
		Reason:   strings.TrimSpace(reason),
		SpecYAML: specYAML,
	})
	if createError != nil {
		return createError
	}

	if !wait {
		if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Run #%d queued: %s\nFollow it with 'ankra pipeline get %s' or 'ankra pipeline logs %s --follow'.\n",
			result.RunNumber, result.PipelineRunID, result.PipelineRunID, result.PipelineRunID)
		return nil
	}

	runWait := startPipelineRunWait(command.Context(), timeout)
	defer runWait.stop()
	detail, waitError := waitForPipelineRunConclusion(command, selector, result.PipelineRunID, runWait)
	if waitError != nil {
		return waitError
	}
	return renderConcludedPipelineRun(command, format, detail, selector)
}

// sleepInterrupted waits, or stops early when the command is interrupted. A
// bare time.Sleep would hold a Ctrl+C until the next network call noticed the
// cancelled context, which for a poll interval means the user waits for a
// command they already stopped.
func sleepInterrupted(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// pipelineRunConclusionError reports a non-success conclusion as an error so
// `--wait` exits non-zero on a failed run, the way a CI step should.
//
// A run that concluded with no outcome recorded at all is reported as that
// rather than as a named failure: it is the same distinction the rest of this
// lane keeps, and telling someone their run "concluded -" sends them looking
// for an outcome nothing wrote.
//
// The word is pipelineRunStateLabel's - the one `pipeline get` and `pipeline
// list` print - so a superseded run concludes "superseded" here too, and the
// line names the run that took its place instead of repeating the class and
// the platform's sentence, neither of which names one. The exit code does not
// change: a superseded run is a cancelled outcome, and exits as one.
func pipelineRunConclusionError(run client.PipelineRun) error {
	outcome := pipelineOptionalString(run.Outcome)
	if outcome == pipelineOutcomeSuccess {
		return nil
	}
	if run.Outcome == nil || strings.TrimSpace(*run.Outcome) == "" {
		return fmt.Errorf("run #%d concluded without recording an outcome", run.RunNumber)
	}
	message := fmt.Sprintf("run #%d concluded %s", run.RunNumber, pipelineRunStateLabel(run))
	if pipelineRunIsSuperseded(run) {
		return fmt.Errorf("%s %s", message, pipelineRunSupersessionPhrase(run))
	}
	if errorClass := pipelineRunErrorClass(run); errorClass != "" {
		message += " (" + errorClass + ")"
	}
	if run.ErrorMessage != nil && *run.ErrorMessage != "" {
		message += ": " + *run.ErrorMessage
	}
	return fmt.Errorf("%s", message)
}

func newPipelineListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List a pipeline's runs",
		Long: `List a pipeline's runs, newest first.

--status filters on the run's lifecycle - queued, running or concluded - not
on how it ended: every finished run is concluded, and its verdict is the
separate 'outcome' field (success, failure, cancelled, timed_out, skipped or
infra_error), which the STATUS column prints in place of the status once
there is one - except for a run a newer run superseded, which reads
'superseded' rather than 'cancelled'. 'ankra pipeline get --help' documents
both fields, supersession and the run's 'authority_state'.

--latest-per-branch answers the other question a listing is usually opened
for: not the last N runs of everything, but the newest run of each branch.
It prints the same table as 'ankra pipeline branches' - see that command's
help - and cannot be combined with the run filters, which narrow runs rather
than refs.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineList(command, selector)
		},
	}
	registerPipelineSelectorFlags(listCommand)
	registerPipelineListFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

// registerPipelineListFlags is shared by `pipeline list` and
// `application pipeline list`.
func registerPipelineListFlags(command *cobra.Command) {
	command.Flags().String("status", "", "Filter by run status: queued, running, or concluded")
	command.Flags().String("trigger", "", "Filter by trigger: push, pull_request, tag, schedule, manual, api, agent, or rerun")
	command.Flags().String("branch", "", "Filter by trigger branch")
	command.Flags().String("head-sha", "", "Filter by the exact full commit sha")
	command.Flags().String("cursor", "", "Page cursor from a previous listing's next_cursor")
	command.Flags().Int("limit", 0, "Maximum number of runs to return (server default 50, max 100); with --latest-per-branch, the number of branches (server default 20)")
	command.Flags().Bool("latest-per-branch", false,
		"List one row per branch with the newest run on each, the table 'ankra pipeline branches' prints")
	command.Flags().Bool("all", false,
		"With --latest-per-branch, include branches with no run in the last 14 days")
}

// pipelineListRunFilterFlags are the flags that only mean anything when the
// listing is over runs. --latest-per-branch answers a different question -
// one row per ref, every trigger and status folded into it - so combining the
// two is refused rather than silently ignoring the filter.
var pipelineListRunFilterFlags = []string{"status", "trigger", "branch", "head-sha"}

func runPipelineList(command *cobra.Command, selector client.PipelineSelector) error {
	latestPerBranch, _ := command.Flags().GetBool("latest-per-branch")
	if latestPerBranch {
		for _, filterName := range pipelineListRunFilterFlags {
			if command.Flags().Changed(filterName) {
				return withExitCode(exitUsage, fmt.Errorf(
					"--%s filters runs and --latest-per-branch lists branches; "+
						"drop one of the two", filterName))
			}
		}
		return runPipelineBranches(command, selector)
	}
	if command.Flags().Changed("all") {
		return withExitCode(exitUsage,
			errors.New("--all only applies with --latest-per-branch"))
	}
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	status, _ := command.Flags().GetString("status")
	trigger, _ := command.Flags().GetString("trigger")
	branch, _ := command.Flags().GetString("branch")
	headSHA, _ := command.Flags().GetString("head-sha")
	cursor, _ := command.Flags().GetString("cursor")
	limit, limitError := pipelinePageLimitFromFlags(command)
	if limitError != nil {
		return limitError
	}

	page, listError := apiClient.ListPipelineRuns(command.Context(), selector, client.ListPipelineRunsOptions{
		Status:  strings.TrimSpace(status),
		Trigger: strings.TrimSpace(trigger),
		Branch:  strings.TrimSpace(branch),
		HeadSHA: strings.TrimSpace(headSHA),
		Cursor:  strings.TrimSpace(cursor),
		Limit:   limit,
	})
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, page)
	}
	if len(page.Runs) == 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No pipeline runs found.")
		return nil
	}
	renderPipelineRunTable(command.OutOrStdout(), page.Runs)
	if page.NextCursor != nil {
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "\nMore runs available: pass --cursor %s to see the next page.\n", *page.NextCursor)
	}
	return nil
}

func renderPipelineRunTable(out io.Writer, runs []client.PipelineRun) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "RUN #", "STATUS", "TRIGGER", "REF", "SHA", "QUEUED"})
	for _, run := range runs {
		writer.AppendRow(table.Row{
			run.ID,
			run.RunNumber,
			renderPipelineRunState(run),
			run.Trigger,
			run.TriggerRef,
			pipelineShortSHA(run.HeadSHA),
			formatTimeAgo(run.QueuedAt),
		})
	}
	writer.Render()
}

// pipelineGetLongHelp is the help both `pipeline get` and
// `application pipeline get` carry.
const pipelineGetLongHelp = `Show a pipeline run's detail - or wait for it to conclude, or watch it.

Name the run by its id, or select it by what you know about it. Push and pull
request runs are started by the webhook, so a CI job or an agent usually knows
the commit rather than the run id: --head-sha, --branch and --trigger narrow
the pipeline's runs the way 'pipeline list' filters them. Exactly one match is
that run; when several match, --latest takes the newest, and without it the
command lists them instead of guessing. --latest on its own is the pipeline's
newest run.

--wait blocks until the run concludes, then prints its final detail. --watch
prints every state change of the run and of each of its steps as it is seen,
until the run concludes; with -o json each change is one JSON object on its
own line, carrying the run or step as the platform answered it. --timeout
bounds either one; without it they wait as long as the run takes. When a
selection matches no run yet, --wait and --watch wait for one to appear - the
webhook that creates it can land a moment after you ask - for up to
--timeout, or ten minutes when no --timeout is set.

Exit codes: 0 when the run succeeded; 1 when it concluded any other way, or
the platform could not be read; 2 when a selection matches several runs and
--latest was not given; 3 when nothing matches; 5 when --timeout ran out or,
with --exit-code, when the run has not concluded yet. Without --wait, --watch
or --exit-code the command exits 0 whatever the run's outcome, as it always
has.

Status and outcome (-o json): 'status' is the lifecycle, never the verdict.
A run's status is queued, running or concluded; a step's is blocked (waiting
on its dependencies), pending (ready, not yet claimed), running or concluded.
How the work ended is the separate 'outcome' field, which is null until the
status is concluded and then always one of: success, failure (the work
itself failed), cancelled, timed_out, skipped (it never ran - a dependency
did not succeed, a condition or the trigger filter excluded it) or
infra_error (Ankra failed, not the work). The same two fields with the same
vocabulary sit on the run and on each step, mirroring GitHub Actions'
status/conclusion split - so a monitor that filters on status finds every
finished run under concluded, and must read outcome for whether it passed.
The human-readable Status line and STATUS column print the outcome once
there is one and the status until then, with a glyph that says which:
✓ success, ✗ failure / timed_out / infra_error, ⊘ cancelled, ○ skipped, and
⟳ only for a run or step that has not concluded (○ for a blocked step).

Superseded runs: a run cancelled because a NEWER run took its concurrency
group reads 'superseded' rather than 'cancelled' - on the Status line, in
the STATUS column, on the last line of --watch and in the conclusion --wait
reports - and 'pipeline get' names the run that took its place. Nobody
stopped that run, and the run worth looking at is the newer one. Only the
word differs: --wait, --watch and --exit-code exit 1 for a superseded run as
for any cancelled one. In -o json the fields are 'error_class'
("superseded"), 'superseded_by_run_id' and 'superseded_by_run_number';
'outcome' stays "cancelled", so a script filtering on outcome alone still
finds these runs and must read the class to tell them from a run somebody
cancelled.

Cancelled runs (-o json 'cancelled_by', 'cancel_reason', 'cancelled_at';
the Cancelled line): who stopped a run, why and when. 'cancelled_by' is the actor
in the same form 'requested_by' uses - "user:<id>" for a cancel pressed in
the portal, the CLI or the API, "github:<login>" for the Cancel action on a
pull request's check run, "concurrency:<run id>" for a supersession - and
'cancel_reason' is one of "user_requested", "source_control", "superseded",
"pull_request_closed" (the run's pull request merged or closed; no actor).
All three are null for a run nobody cancelled and for one cancelled before
Ankra recorded this, which is "not recorded", never "the platform did it".

Authority (-o json 'authority_state', the Authority line): the protected
authority the run executed under. 'approved' - the default branch's
definition declares no protected section, or an administrator approved
exactly what it declares, and the head changes nothing. 'unapproved' - a run
of the default branch whose definition changed authority no administrator
has approved yet; it executes under the last approved authority, or under
none when nothing was ever approved. 'changed_on_head' - a run of another
branch or a pull request whose definition declares different authority; it
executes under the default branch's, and the head's change is ignored and
reported. null - the planner never resolved authority for this run: it is
still queued, it concluded skipped because its trigger filter excluded every
stage before planning, or it was planned before authority was recorded.
null is "not recorded", never "no authority".`

func newPipelineGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get [run]",
		Short: "Show a pipeline run's detail, or wait on or watch it",
		Long:  pipelineGetLongHelp,
		Example: `  # Block until the pull request run for a commit concludes; exit non-zero unless it passed
  ankra pipeline get --head-sha "$(git rev-parse HEAD)" --trigger pull_request --latest --wait --timeout 45m

  # React to each run and step state change as it happens, one JSON object per line
  ankra pipeline get <run-id> --watch -o json

  # Read a run once and branch on it: 0 passed, 1 did not, 5 still running
  ankra pipeline get <run-id> --exit-code`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			runID := ""
			if len(arguments) == 1 {
				runID = arguments[0]
			}
			return runPipelineGet(command, selector, runID)
		},
	}
	registerPipelineSelectorFlags(getCommand)
	registerPipelineGetFlags(getCommand)
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

// registerPipelineGetFlags is shared by `pipeline get` and
// `application pipeline get`.
func registerPipelineGetFlags(command *cobra.Command) {
	command.Flags().Bool("wait", false,
		"Wait for the run to conclude, then print its final detail; exits non-zero unless it succeeded")
	command.Flags().Bool("watch", false,
		"Print each run and step state change as it happens until the run concludes (-o json: one JSON object per line)")
	command.Flags().Bool("exit-code", false,
		"Exit 1 when the run concluded without succeeding, and 5 while it has not concluded "+
			"(--wait and --watch always exit this way)")
	registerPipelineWaitTimeoutFlag(command)
	command.Flags().String("head-sha", "", "Select the run for this full commit sha instead of naming its id")
	command.Flags().String("branch", "", "Select the run for this trigger branch instead of naming its id")
	command.Flags().String("trigger", "",
		"Select the run with this trigger: push, pull_request, tag, schedule, manual, api, agent, or rerun")
	command.Flags().Bool("latest", false,
		"Take the newest matching run when several match (on its own: the pipeline's newest run)")
	command.Flags().String("step", "",
		"Also print the per-cache table of the step with this key: each cache's outcome, reason, size and save")
}

// runPipelineGet shows one run: named by runID, or - when runID is empty -
// selected by the --head-sha/--branch/--trigger/--latest flags. --wait and
// --watch then hold the command until the run concludes; see
// cmd/pipeline_wait.go.
func runPipelineGet(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	isWaiting, _ := command.Flags().GetBool("wait")
	isWatching, _ := command.Flags().GetBool("watch")
	isExitCodeRequested, _ := command.Flags().GetBool("exit-code")
	if isWatching && format == outputYAML {
		return withExitCode(exitUsage, errors.New(
			"--watch writes one JSON object per line: pass -o json, or no -o for readable lines"))
	}
	timeout, timeoutError := pipelineWaitTimeoutFromFlags(command, isWaiting || isWatching, "--wait or --watch")
	if timeoutError != nil {
		return timeoutError
	}
	runID = strings.TrimSpace(runID)
	selection := pipelineRunSelectionFromFlags(command)
	switch {
	case runID != "" && !selection.isEmpty():
		return withExitCode(exitUsage, errors.New(
			"name the run by its id or select it with --head-sha, --branch, --trigger and --latest, not both"))
	case runID == "" && selection.isEmpty():
		return withExitCode(exitUsage, errors.New(
			"pass the run's id, or select a run with --latest (narrowed by --head-sha, --branch or --trigger)"))
	}

	var runWait *pipelineRunWait
	if isWaiting || isWatching {
		runWait = startPipelineRunWait(command.Context(), timeout)
		defer runWait.stop()
	}
	if runID == "" {
		selectedID, selectionError := resolvePipelineRunSelection(command, selector, selection, runWait)
		if selectionError != nil {
			return selectionError
		}
		runID = selectedID
	}

	switch {
	case isWatching:
		return watchPipelineRun(command, selector, runID, format, runWait)
	case isWaiting:
		detail, waitError := waitForPipelineRunConclusion(command, selector, runID, runWait)
		if waitError != nil {
			return waitError
		}
		return renderConcludedPipelineRun(command, format, detail, selector)
	}

	detail, getError := apiClient.GetPipelineRun(command.Context(), selector, runID)
	if getError != nil {
		return getError
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, detail); encodeError != nil {
			return encodeError
		}
	} else {
		printPipelineRunDetail(command.OutOrStdout(), *detail, selector)
		if stepKey, _ := command.Flags().GetString("step"); strings.TrimSpace(stepKey) != "" {
			if stepError := printPipelineStepCaches(command.OutOrStdout(), *detail, strings.TrimSpace(stepKey)); stepError != nil {
				return stepError
			}
		}
	}
	if isExitCodeRequested {
		return pipelineRunExitCodeError(detail.PipelineRun)
	}
	return nil
}

// printPipelineRunWaiting prints what a queued run is waiting for, under the
// time it has been queued for (ankra-a0yh3).
//
// Until the server derived this, "Queued: 14 minutes ago" was the whole answer
// `ankra pipeline get` had - which told the reader the one thing they could
// already see. A server that could not derive a reason says so rather than
// printing nothing: no line at all reads as "nothing is blocking this run",
// which is the answer a failed read must never give.
func printPipelineRunWaiting(out io.Writer, detail client.PipelineRunDetail) {
	if message := strings.TrimSpace(detail.QueueReasonMessage); message != "" {
		_, _ = fmt.Fprintf(out, "  Waiting:   %s\n", message)
		return
	}
	if unavailable := strings.TrimSpace(detail.QueueReasonUnavailable); unavailable != "" {
		_, _ = fmt.Fprintf(out, "  Waiting:   %s\n", unavailable)
	}
}

// printPipelineRunPromotion prints what a push run did about promotion
// (ankra-q573dh.27): published a pull request run's tested image, naming that
// run, or why it built in full. A run that never asked to be promoted, and
// any run read from a server older than the fields, prints nothing.
func printPipelineRunPromotion(out io.Writer, detail client.PipelineRunDetail) {
	if detail.PromotionOutcome == nil || strings.TrimSpace(*detail.PromotionOutcome) == "" {
		return
	}
	line := strings.TrimSpace(*detail.PromotionOutcome)
	if detail.PromotionMessage != nil && strings.TrimSpace(*detail.PromotionMessage) != "" {
		line = strings.TrimSpace(*detail.PromotionMessage)
	}
	switch {
	case detail.PromotedFromRunNumber != nil:
		line += fmt.Sprintf(" (from run #%d)", *detail.PromotedFromRunNumber)
	case detail.PromotedFromRunID != nil && *detail.PromotedFromRunID != "":
		line += fmt.Sprintf(" (from run %s)", *detail.PromotedFromRunID)
	}
	_, _ = fmt.Fprintf(out, "  Promotion: %s\n", line)
}

func printPipelineRunDetail(out io.Writer, detail client.PipelineRunDetail, selector client.PipelineSelector) {
	_, _ = fmt.Fprintf(out, "Run #%d (%s)\n", detail.RunNumber, detail.ID)
	_, _ = fmt.Fprintf(out, "  Status:    %s\n", renderPipelineRunState(detail.PipelineRun))
	printPipelineRunSupersession(out, detail.PipelineRun)
	printPipelineRunCancellation(out, detail.PipelineRun)
	_, _ = fmt.Fprintf(out, "  Trigger:   %s (%s)\n", detail.Trigger, detail.TriggerRef)
	_, _ = fmt.Fprintf(out, "  Commit:    %s\n", detail.HeadSHA)
	printPipelineRunPromotion(out, detail)
	printPipelineRunAuthority(out, detail)
	printPipelineRunFailure(out, detail.PipelineRun)
	_, _ = fmt.Fprintf(out, "  Queued:    %s\n", formatTimeAgo(detail.QueuedAt))
	printPipelineRunWaiting(out, detail)
	if detail.StartedAt != nil {
		_, _ = fmt.Fprintf(out, "  Started:   %s\n", formatTimeAgo(*detail.StartedAt))
	}
	if detail.FinishedAt != nil {
		_, _ = fmt.Fprintf(out, "  Finished:  %s\n", formatTimeAgo(*detail.FinishedAt))
	}
	_, _ = fmt.Fprintln(out)
	if len(detail.Steps) == 0 {
		_, _ = fmt.Fprintln(out, "No steps planned yet.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"STEP", "ATTEMPT", "STAGE", "KIND", "EXECUTOR", "STATUS", "EXIT", "RAN ON", "CACHE"})
	for _, step := range detail.Steps {
		exitCode := "-"
		if step.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *step.ExitCode)
		}
		writer.AppendRow(table.Row{
			step.StepKey,
			step.Attempt,
			step.Stage,
			step.Kind,
			renderPipelineStepExecutor(step),
			renderPipelineState(step.Status, step.Outcome),
			exitCode,
			renderPipelineStepNode(step),
			renderPipelineStepCacheResult(step),
		})
	}
	writer.Render()
	printPipelineStepQueueing(out, detail)
	printPipelineStepResourceUse(out, detail)
	printPipelinePlatformBuilderSteps(out, detail)
	printPipelineSupersededAttempts(out, detail, selector)
}

// renderPipelineStepCacheResult is the step's cache_result: hit, partial,
// restore_failed, miss or disabled. A step that recorded no cache answer at
// all - unknown with no per-cache report, which is every step without caches
// - reads "-" rather than a word that looks like a finding.
func renderPipelineStepCacheResult(step client.PipelineStep) string {
	cacheResult := strings.TrimSpace(step.CacheResult)
	if cacheResult == "" || (cacheResult == "unknown" && len(step.Caches) == 0) {
		return "-"
	}
	return cacheResult
}

// printPipelineStepCaches prints the per-cache table of the step a --step key
// names: each cache's outcome, why a restore did not land, how long it took
// and how large it was, and whether the step's archive was saved. Its latest
// attempt is the one shown.
func printPipelineStepCaches(out io.Writer, detail client.PipelineRunDetail, stepKey string) error {
	var selected *client.PipelineStep
	for index := range detail.Steps {
		step := &detail.Steps[index]
		if step.StepKey != stepKey {
			continue
		}
		if selected == nil || step.Attempt > selected.Attempt {
			selected = step
		}
	}
	if selected == nil {
		return withExitCode(exitUsage, fmt.Errorf("run #%d has no step %q", detail.RunNumber, stepKey))
	}
	_, _ = fmt.Fprintf(out, "\nCaches of %s (attempt %d): %s\n", selected.StepKey, selected.Attempt,
		renderPipelineStepCacheResult(*selected))
	if len(selected.Caches) == 0 {
		_, _ = fmt.Fprintln(out, "  No per-cache report was recorded for this step.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"PATH", "OUTCOME", "REASON", "RESTORED", "TOOK", "SAVED", "DETAIL"})
	for _, cache := range selected.Caches {
		reason := cache.Reason
		if reason == "" {
			reason = "-"
		}
		restored := "-"
		if cache.Bytes > 0 {
			restored = formatPipelineCacheBytes(cache.Bytes)
		}
		took := "-"
		if cache.DurationMilliseconds > 0 {
			took = (time.Duration(cache.DurationMilliseconds) * time.Millisecond).String()
		}
		saved := "no"
		if cache.Saved {
			saved = "yes (" + formatPipelineCacheBytes(cache.SavedBytes) + ")"
		}
		writer.AppendRow(table.Row{cache.Path, cache.Outcome, reason, restored, took, saved, cache.Error})
	}
	writer.Render()
	return nil
}

// formatPipelineCacheBytes states a size in the largest binary unit that
// keeps it at or above one.
func formatPipelineCacheBytes(sizeBytes int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(sizeBytes)
	unitIndex := 0
	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}
	if unitIndex == 0 {
		return fmt.Sprintf("%d B", sizeBytes)
	}
	return fmt.Sprintf("%.1f %s", size, units[unitIndex])
}

// renderPipelineStepNode is the node a step's pod ran on, as the agent
// reported it. A step that never reached a node, and every step on a platform
// that predates recording it, reads "-" rather than a blank cell.
func renderPipelineStepNode(step client.PipelineStep) string {
	if nodeName := strings.TrimSpace(step.NodeName); nodeName != "" {
		return nodeName
	}
	return "-"
}

// printPipelineStepQueueing says how long each step waited for a CI slot on
// its cluster, and which steps were handed back to the queue, under the table.
//
// A step's clock starts when the agent picks it up, not when Ankra hands it
// over: the time between dispatched_at and started_at is the step waiting
// for one of the agent's CI slots, and it is the number that says whether a
// slow pipeline needs more workers. A step handed over and not yet picked up
// is waiting right now, which is said as such rather than left blank.
//
// A step the agent gave back unrun - the node its run's workspace is pinned
// to was full - reads pending again, which alone looks like nothing ever
// tried it; its deferral count says it did.
//
// Nothing is printed when no step carries either fact, which is every run on
// a platform older than the fields.
func printPipelineStepQueueing(out io.Writer, detail client.PipelineRunDetail) {
	lines := []string{}
	for _, step := range detail.Steps {
		if wait, isMeasured := pipelineStepQueueWait(step); isMeasured {
			lines = append(lines, fmt.Sprintf("  %s: waited %s for a CI slot", step.StepKey, wait))
		} else if step.DispatchedAt != nil && step.StartedAt == nil && step.Status == pipelineStepStatusRunning {
			lines = append(lines, fmt.Sprintf("  %s: waiting for a CI slot since %s", step.StepKey,
				formatTimeAgo(*step.DispatchedAt)))
		}
		if line, isObserved := pipelineStepPodPendingLine(step); isObserved {
			lines = append(lines, line)
		}
		if step.DeferredCount != nil && *step.DeferredCount > 0 {
			lines = append(lines, fmt.Sprintf("  %s: returned to the queue %s (its node was full)",
				step.StepKey, pluralTimes(*step.DeferredCount)))
		}
	}
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nQueueing:")
	for _, line := range lines {
		_, _ = fmt.Fprintln(out, line)
	}
}

// printPipelineStepResourceUse says, under the table, what each step's
// container used against what it asked its node for (ankra-q573dh.5): its
// memory peak against its memory request, and the cores it used on average
// against its CPU request. A request far above the use is capacity the step's
// node held for nothing; a stage's suggested resources come from the API's
// resource suggestions for the run.
//
// The memory peak counts page cache, so a peak equal to the request says the
// step filled its room, not that it needed it, and the line says so. The CPU
// average needs the script's run time, which older agents do not report; the
// CPU half is left out then rather than guessed from the step's duration,
// which includes its Pending wait. Nothing is printed when no step carries a
// usage report, which is every run on a platform or agent older than it.
func printPipelineStepResourceUse(out io.Writer, detail client.PipelineRunDetail) {
	lines := []string{}
	for _, step := range detail.Steps {
		if line, isReported := pipelineStepResourceUseLine(step); isReported {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nResources used:")
	for _, line := range lines {
		_, _ = fmt.Fprintln(out, line)
	}
}

// pipelineStepResourceUseLine is one step's line of the resources section.
func pipelineStepResourceUseLine(step client.PipelineStep) (string, bool) {
	parts := []string{}
	if step.MemoryPeakBytes != nil {
		memory := "memory peak " + formatPipelineCacheBytes(*step.MemoryPeakBytes)
		if step.MemoryRequestBytes != nil && *step.MemoryRequestBytes > 0 {
			memory += fmt.Sprintf(" of %s requested (%d%%)", formatPipelineCacheBytes(*step.MemoryRequestBytes),
				*step.MemoryPeakBytes*100 / *step.MemoryRequestBytes)
			if *step.MemoryPeakBytes*100 >= *step.MemoryRequestBytes*95 {
				memory += ", which is its whole request: page cache fills spare room, so this is not what it needed"
			}
		}
		if step.MemoryLimitHits != nil && *step.MemoryLimitHits > 0 {
			memory += fmt.Sprintf(", met its memory limit %d time(s)", *step.MemoryLimitHits)
		}
		if step.MemoryOOMKills != nil && *step.MemoryOOMKills > 0 {
			memory += fmt.Sprintf(", %d process(es) killed for memory", *step.MemoryOOMKills)
		}
		parts = append(parts, memory)
	}
	// A run under a second is mostly its shell starting, so its average is
	// not read - the same floor the platform's right-sizing applies.
	used := step.CPUUsageMicroseconds
	elapsed := step.UsageElapsedMicroseconds
	if used != nil && elapsed != nil && *elapsed >= 1_000_000 {
		cpu := fmt.Sprintf("CPU %.2f cores on average", float64(*used)/float64(*elapsed))
		if step.CPURequestMillicores != nil && *step.CPURequestMillicores > 0 {
			cpu += fmt.Sprintf(" of %.2f requested", float64(*step.CPURequestMillicores)/1000)
		}
		if throttled := step.CPUThrottledMicroseconds; throttled != nil && *throttled > 0 {
			cpu += fmt.Sprintf(", held at its CPU limit for %s",
				(time.Duration(*throttled) * time.Microsecond).Round(time.Second))
		}
		parts = append(parts, cpu)
	}
	if len(parts) == 0 {
		return "", false
	}
	return "  " + step.StepKey + ": " + strings.Join(parts, "; "), true
}

// pipelineStepQueueWait is the time between Ankra handing a step to the agent
// and the agent starting it. It is unmeasured unless both timestamps are
// present and parse, and a start before the hand-off - clocks that disagree -
// is unmeasured rather than a negative wait.
func pipelineStepQueueWait(step client.PipelineStep) (time.Duration, bool) {
	if step.DispatchedAt == nil || step.StartedAt == nil {
		return 0, false
	}
	dispatchedAt, dispatchedError := time.Parse(time.RFC3339, *step.DispatchedAt)
	startedAt, startedError := time.Parse(time.RFC3339, *step.StartedAt)
	if dispatchedError != nil || startedError != nil || startedAt.Before(dispatchedAt) {
		return 0, false
	}
	return startedAt.Sub(dispatchedAt).Round(time.Second), true
}

// pipelineStepPodPendingLine says how long a step's pod sat Pending in the
// cluster before the step's own container ran, and where that time went: the
// scheduler placing it, its workspace volume attaching, its image pulling.
// It is the wait the CI slot line cannot show - the agent has already picked
// the step up - and on a busy cluster it is the larger of the two.
//
// A missing container start is an absence of observation, not proof the
// container never ran: the agent reads it off its pod polls, and a pod that
// started and went between two polls leaves no start behind. So a step that
// succeeded says nothing about it (its container plainly ran), a step still
// in flight says nothing (it may yet start), a step with no outcome says
// nothing (no verdict is not a failure), and only a concluded step that
// did not succeed - the one stuck Pending until it timed out or was
// cancelled - says its container was never seen to start, with the least
// time it sat Pending when the step's end is known. Nothing is said for a
// step whose agent reported no pod at all, which is every step on an agent
// or a platform older than the fields.
func pipelineStepPodPendingLine(step client.PipelineStep) (string, bool) {
	createdAt, isCreatedKnown := parsePipelineStepTime(step.PodCreatedAt)
	if !isCreatedKnown {
		return "", false
	}
	containerStartedAt, isStartKnown := parsePipelineStepTime(step.ContainerStartedAt)
	if !isStartKnown {
		if step.Status != pipelineStepStatusConcluded || step.Outcome == nil ||
			*step.Outcome == pipelineOutcomeSuccess {
			return "", false
		}
		if finishedAt, isFinishedKnown := parsePipelineStepTime(step.FinishedAt); isFinishedKnown &&
			finishedAt.After(createdAt) {
			return fmt.Sprintf("  %s: Pending at least %s in the cluster; its container was never seen to start",
				step.StepKey, nonNegativeDuration(finishedAt.Sub(createdAt))), true
		}
		return fmt.Sprintf("  %s: its container was never seen to start", step.StepKey), true
	}
	phases := []string{}
	scheduledAt, isScheduledKnown := parsePipelineStepTime(step.PodScheduledAt)
	if isScheduledKnown {
		phases = append(phases, "scheduling "+nonNegativeDuration(scheduledAt.Sub(createdAt)).String())
	}
	if attachedAt, isAttachedKnown := parsePipelineStepTime(step.WorkspaceAttachedAt); isAttachedKnown && isScheduledKnown {
		phases = append(phases, "volumes "+nonNegativeDuration(attachedAt.Sub(scheduledAt)).String())
	}
	if pulledAt, isPulledKnown := parsePipelineStepTime(step.ImagePulledAt); isPulledKnown {
		if pullStartedAt, isPullStartKnown := parsePipelineStepTime(step.ImagePullStartedAt); isPullStartKnown {
			phases = append(phases, "image pull "+nonNegativeDuration(pulledAt.Sub(pullStartedAt)).String())
		} else {
			phases = append(phases, "image already on the node")
		}
	}
	line := fmt.Sprintf("  %s: Pending %s in the cluster before it ran", step.StepKey,
		nonNegativeDuration(containerStartedAt.Sub(createdAt)))
	if len(phases) > 0 {
		line += " (" + strings.Join(phases, ", ") + ")"
	}
	return line, true
}

// parsePipelineStepTime reads one of a step's optional timestamps, with or
// without fractional seconds.
func parsePipelineStepTime(value *string) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	parsed, parseError := time.Parse(time.RFC3339Nano, *value)
	if parseError != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// nonNegativeDuration rounds a span to the second, reading two clocks that
// disagree by a moment as no wait rather than a negative one.
func nonNegativeDuration(span time.Duration) time.Duration {
	if span < 0 {
		return 0
	}
	return span.Round(time.Second)
}

// pluralTimes renders a count of occurrences: "once", "2 times".
func pluralTimes(count int) string {
	if count == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", count)
}

// renderPipelineStepExecutor is the lane the platform placed a step on, in
// the platform's own vocabulary: in_cluster, platform_builders or platform.
//
// It was on the wire from the start (PipelineStep.Executor) and nothing ever
// printed it, so the one run detail a person reads - `ankra pipeline get` -
// could not say that a build had left their cluster for Ankra's builders.
// That is the fact every other answer about such a step depends on: why there
// is no live log, why no node name, why a Dockerfile that builds on their own
// agent behaves differently here (PLA-868).
//
// Printed verbatim rather than mapped to a phrase, for the reason
// printPipelineRunFailure prints its class verbatim: it is the same token
// `-o json` and the API spell, the vocabulary grows on the server, and a
// mapper that has not been taught a new lane renders it as nothing at all.
// A step nothing has dispatched yet carries no executor, which prints as "-":
// "not placed on a lane yet", never "in cluster".
func renderPipelineStepExecutor(step client.PipelineStep) string {
	if executor := strings.TrimSpace(step.Executor); executor != "" {
		return executor
	}
	return "-"
}

// printPipelinePlatformBuilderSteps says what the platform_builders rows
// above mean, under the table that now shows them.
//
// The column alone answers "where did this run", but not the question that
// brings someone to this command: where is the log. A step on Ankra's
// builders has no live stream to tail - the lane opens no execution for the
// relay - so `pipeline logs` on it can only answer with the archive, and only
// once the step concludes. Saying so here is what keeps a reader from tailing
// a running build for twenty minutes and concluding Ankra is stuck.
//
// Nothing is printed for a run whose steps all ran in its own cluster, which
// is most runs.
func printPipelinePlatformBuilderSteps(out io.Writer, detail client.PipelineRunDetail) {
	stepKeys := []string{}
	seen := map[string]bool{}
	for _, step := range detail.Steps {
		if step.Executor != pipelineExecutorPlatformBuilders || seen[step.StepKey] {
			continue
		}
		seen[step.StepKey] = true
		stepKeys = append(stepKeys, step.StepKey)
	}
	if len(stepKeys) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nOn Ankra's platform builders: %s\n", strings.Join(stepKeys, ", "))
	_, _ = fmt.Fprintln(out, "  This run's cluster could not build these steps, so Ankra's own builders took")
	_, _ = fmt.Fprintln(out, "  them. They have no live log stream; 'ankra pipeline logs' prints the archived")
	_, _ = fmt.Fprintln(out, "  log of one once it concludes.")
}

// printPipelineSupersededAttempts explains the rows above that a retry
// replaced: every step row that is not the newest attempt of its step key.
//
// A retried step is several rows under one key, and the table prints all of
// them because the run carries all of them - the lost attempt is kept on the
// run as evidence (enginekit/pipelinerun's insertRetryAttempt). Until now
// those rows were indistinguishable: same key, same stage, same kind, and
// neither the attempt number nor the row id to tell one from the other. So a
// run whose build failed once and succeeded on Ankra's own retry showed two
// build rows, and the only thing that said WHY the first one failed - the
// attempt's own error class and message, both already in this payload - was
// never printed anywhere.
//
// It matters most for the log. `pipeline logs --step <key>` resolves a key to
// the newest attempt (resolvePipelineStep), which is the right default and
// also means the failed attempt's log is reachable only by that attempt's row
// id - an id nothing printed. PLA-871's reporter watched both build steps of
// a run fail once and pass on the retry, and could not read either first
// attempt. The command is spelled out per attempt rather than described,
// because the id is the part nobody can guess.
//
// Nothing is printed for a run with no retried step, which is almost every
// run.
func printPipelineSupersededAttempts(out io.Writer, detail client.PipelineRunDetail,
	selector client.PipelineSelector) {
	newestAttempts := map[string]int16{}
	for _, step := range detail.Steps {
		if attempt, seen := newestAttempts[step.StepKey]; !seen || step.Attempt > attempt {
			newestAttempts[step.StepKey] = step.Attempt
		}
	}
	superseded := []client.PipelineStep{}
	for _, step := range detail.Steps {
		if step.Attempt < newestAttempts[step.StepKey] {
			superseded = append(superseded, step)
		}
	}
	if len(superseded) == 0 {
		return
	}
	// Ordered here rather than taken from the payload, for the same reason
	// newestPipelineStepAttempt compares attempt numbers instead of trusting
	// the listing's order: which row comes first is a server-side ORDER BY
	// this lane has no guarantee about. A block whose whole subject is the
	// chronology of a retried step is the last place to print attempt 2
	// above attempt 1 because a query happened to return it that way.
	sort.SliceStable(superseded, func(first, second int) bool {
		if superseded[first].StepKey != superseded[second].StepKey {
			return superseded[first].StepKey < superseded[second].StepKey
		}
		return superseded[first].Attempt < superseded[second].Attempt
	})
	_, _ = fmt.Fprintf(out, "\nEarlier attempts (%d), superseded by a retry:\n", len(superseded))
	for _, step := range superseded {
		_, _ = fmt.Fprintf(out, "  %s attempt %d: %s\n", step.StepKey, step.Attempt,
			renderPipelineState(step.Status, step.Outcome))
		if errorClass := pipelineStepErrorClass(step); errorClass != "" {
			_, _ = fmt.Fprintf(out, "    Class: %s\n", errorClass)
		}
		if step.ErrorMessage != nil && strings.TrimSpace(*step.ErrorMessage) != "" {
			_, _ = fmt.Fprintf(out, "    Error: %s\n", strings.TrimSpace(*step.ErrorMessage))
		}
		_, _ = fmt.Fprintf(out, "    Log:   ankra pipeline logs %s%s --step %s\n",
			detail.ID, pipelineSelectorArguments(selector), step.ID)
	}
}

// pipelineStepErrorClass is one step attempt's recorded error class, or ""
// when the server recorded none - the step-level twin of
// pipelineRunErrorClass, and absent for the same reason: a class the server
// never wrote is not a class called "".
func pipelineStepErrorClass(step client.PipelineStep) string {
	if step.ErrorClass == nil {
		return ""
	}
	return strings.TrimSpace(*step.ErrorClass)
}

// printPipelineRunSupersession names the run that took a superseded run's
// concurrency group, under the Status line it qualifies.
//
// A superseded run is the one cancelled run nobody chose to stop, and the
// run a person should be looking at instead is the one that replaced it.
// Without this the status read "cancelled" and the whole explanation was the
// server's sentence on the Error line, which names no run - so the author of
// the superseded run went looking for whoever had cancelled it.
//
// A server that reports the supersession but not the number - the newer run
// has since been removed by retention - says so without naming a run, rather
// than printing an id nobody can quote or, worse, "#0". A server too old to
// report supersessions at all prints nothing here, which is "this platform
// does not answer the question", never "this run was not superseded".
//
// This line is the whole of what the detail says about the supersession:
// printPipelineRunFailure prints no Class or Error line for a superseded run,
// since the class is the word the Status line already carries and the
// platform's message ("A newer run took this run's concurrency group.") is
// this line without the run number. Printing all three said one thing three
// ways (ankra-ohzw6).
func printPipelineRunSupersession(out io.Writer, run client.PipelineRun) {
	if !pipelineRunIsSuperseded(run) {
		return
	}
	_, _ = fmt.Fprintf(out, "  Superseded: %s\n", pipelineRunSupersessionPhrase(run))
}

// pipelineRunIsSuperseded reports whether a newer run took this run's
// concurrency group: the platform records that as the superseded error class
// on a cancelled run, and nothing else marks it.
func pipelineRunIsSuperseded(run client.PipelineRun) bool {
	return pipelineRunErrorClass(run) == pipelineErrorClassSuperseded
}

// pipelineRunSupersessionPhrase names the run that took a superseded run's
// place, as the phrase every line about the supersession ends with: "by run
// #18", or - when the platform reports the supersession but no longer the
// run, which retention removed - "by a newer run the platform no longer
// reports". It never prints a number nothing reported.
func pipelineRunSupersessionPhrase(run client.PipelineRun) string {
	if run.SupersededByRunNumber == nil {
		return "by a newer run the platform no longer reports"
	}
	return fmt.Sprintf("by run #%d", *run.SupersededByRunNumber)
}

// printPipelineRunCancellation names who stopped a cancelled run and why,
// under the Status line that says only that it was stopped (ankra-57z1w).
//
// A cancelled run used to end "status concluded, outcome cancelled,
// error_class null" with nothing saying whether that was the concurrency
// policy, a queue watchdog, a person in the portal or the platform - and with
// several sessions and webhooks acting on one repository, that is the
// difference between "expected" and "something is wrong" (PLA-866 ask j,
// Smartoptics). The platform now records the actor and a reason code, and this
// is where a reader of `ankra pipeline get` sees them.
//
// A superseded run prints nothing here: printPipelineRunSupersession already
// names the run that took its group, which is the same fact in the words that
// run's author needs, and printing both said one thing twice.
//
// A server too old to report any of this prints nothing, which is "this
// platform does not answer the question" - never "nobody cancelled it".
func printPipelineRunCancellation(out io.Writer, run client.PipelineRun) {
	if pipelineRunIsSuperseded(run) {
		return
	}
	actor := optionalPipelineRunField(run.CancelledBy)
	reason := optionalPipelineRunField(run.CancelReason)
	if actor == "" && reason == "" {
		return
	}
	var line string
	switch {
	case actor != "" && reason != "":
		line = fmt.Sprintf("by %s (%s)", actor, reason)
	case actor != "":
		line = "by " + actor
	default:
		line = reason
	}
	if cancelledAt := optionalPipelineRunField(run.CancelledAt); cancelledAt != "" {
		line += " at " + cancelledAt
	}
	_, _ = fmt.Fprintf(out, "  Cancelled: %s\n", line)
}

// optionalPipelineRunField is a nullable run string as a plain one: "" for a
// field the server did not answer, which every caller of it treats as "not
// recorded" and prints nothing for.
func optionalPipelineRunField(field *string) string {
	if field == nil {
		return ""
	}
	return strings.TrimSpace(*field)
}

// printPipelineRunFailure prints how a run failed: the error class the server
// recorded, then the message.
//
// The class is printed because it is the only part of a failure that is a
// fixed vocabulary rather than prose, and it names WHOSE failure the run was -
// step_failed is the repository's, registry_push_failed is the organisation's
// image registry, platform_build_infra and build_runtime_confined are Ankra's.
// It was on the wire from the start and only `-o json` ever showed it, so a
// caller reading the run the way a caller does - `ankra pipeline get <run>` -
// saw the message alone. For a platform-builders build that message used to be
// a fragment of the BuildKit transcript, and Smartoptics spent a day auditing
// their own Dockerfiles over a 401 from their own Harbor because nothing on
// the run said registry_push_failed (PLA-851, ankra-edt1b).
//
// The class is printed verbatim - the same token `ankra pipeline get -o json`,
// `ankra application build get` and the API all spell - rather than mapped to
// a sentence: the message is already the sentence, the vocabulary grows on the
// server (build_runtime_confined, build_fallback_unsupported and
// registry_push_failed all arrived after this command shipped), and a mapper
// that has not been taught a new class renders it as nothing at all. A class
// the reader does not recognise is still a search term; a blank is not.
//
// The message keeps its own line breaks and is not indented past the first
// line. A platform build's message carries the tail of the build transcript
// whole, deliberately (cluster's platformBuildClassMessage), and re-indenting
// somebody's build output to line up a label would corrupt the one copy of it
// Ankra keeps.
//
// A superseded run prints neither line: its class is the word its Status line
// already reads, and its message is the Superseded line without the run
// number (see printPipelineRunSupersession). Nothing failed in that run, and
// a Class and an Error under it read as if something had.
func printPipelineRunFailure(out io.Writer, run client.PipelineRun) {
	if pipelineRunIsSuperseded(run) {
		return
	}
	if errorClass := pipelineRunErrorClass(run); errorClass != "" {
		_, _ = fmt.Fprintf(out, "  Class:     %s\n", errorClass)
	}
	if run.ErrorMessage != nil && *run.ErrorMessage != "" {
		_, _ = fmt.Fprintf(out, "  Error:     %s\n", *run.ErrorMessage)
	}
}

// pipelineRunErrorClass is the run's recorded error class, or "" when the
// server recorded none. A successful run has none, and neither has a run that
// failed before anything classified it, which is "not recorded" rather than a
// class called "" - so nothing is printed for either.
func pipelineRunErrorClass(run client.PipelineRun) string {
	if run.ErrorClass == nil {
		return ""
	}
	return strings.TrimSpace(*run.ErrorClass)
}

// printPipelineRunAuthority prints the run's recorded authority state
// (ankra-vn0bd.10.8) when the server recorded one: whose protected sections
// the run executed and, for a state other than "approved", the approve
// command for the definition the server says can be approved. A run planned
// before authority tracking existed carries no state and this prints nothing,
// matching how the wire field is null rather than empty.
//
// The id in the approve command is ApproveDefinitionID, never
// AuthorityDefinitionID. The latter names the definition the run's trusted
// authority was drawn from - already approved, or the default branch's own
// when it protects nothing - and approving it is refused or changes nothing. PLA-855's
// reporter did exactly that, because the only id this printed sat next to the
// word "approving" (ankra-erdtu). The approvable one is the repository's
// CURRENT default-branch definition, which the server resolves and reports as
// approve_definition_id, null when there is nothing the approve route would
// accept. A server older than that field reports nothing either, so its
// absence prints no id rather than a guess.
//
// Authority is recorded when a run is planned, so an approval changes the
// runs planned after it, never the state printed here.
func printPipelineRunAuthority(out io.Writer, detail client.PipelineRunDetail) {
	if detail.AuthorityState == nil || *detail.AuthorityState == "" {
		return
	}
	isApproved := *detail.AuthorityState == "approved"
	_, _ = fmt.Fprintf(out, "  Authority: %s\n", *detail.AuthorityState)
	if detail.AuthorityDefinitionID != nil && *detail.AuthorityDefinitionID != "" {
		note := ""
		if !isApproved {
			note = " (already trusted - not the definition to approve)"
		}
		_, _ = fmt.Fprintf(out, "             trusted authority taken from definition %s%s\n",
			*detail.AuthorityDefinitionID, note)
	}
	if isApproved {
		return
	}
	if detail.ApproveDefinitionID != nil && *detail.ApproveDefinitionID != "" {
		_, _ = fmt.Fprintf(out, "             to approve the default branch's current definition for runs planned "+
			"after it: ankra pipeline definitions approve %s\n", *detail.ApproveDefinitionID)
		return
	}
	_, _ = fmt.Fprintln(out, "             no definition to approve was reported for this run: the default branch's "+
		"current definition is already approved or cannot be approved, or the server predates reporting it")
}

func newPipelineCancelCommand() *cobra.Command {
	cancelCommand := &cobra.Command{
		Use:     "cancel <run>",
		Aliases: []string{"stop"},
		Short:   "Cancel a pipeline run that has not concluded",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineCancel(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(cancelCommand)
	registerStructuredOutputFlags(cancelCommand)
	return cancelCommand
}

func runPipelineCancel(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	run, cancelError := apiClient.CancelPipelineRun(command.Context(), selector, strings.TrimSpace(runID))
	if cancelError != nil {
		return cancelError
	}
	if rendered, renderError := renderStructured(command, run); rendered || renderError != nil {
		return renderError
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Run #%d cancelled (status: %s)\n", run.RunNumber, run.Status)
	return nil
}

func newPipelineRerunCommand() *cobra.Command {
	rerunCommand := &cobra.Command{
		Use:   "rerun <run>",
		Short: "Re-run a concluded pipeline run",
		Long: `Open a new run from a run that already happened.

The new run is a fresh run, not a retry of the old one: the old run's outcome
stays the record of what happened, and 'rerun_of_run_id' ties the two together.
--failed-only restricts the new run to the steps that did not succeed,
whatever depended on them, and the steps they build on. The new run starts
from an empty workspace, so the checkout and every upstream step whose output
a repeated step reads run again with it; steps that succeeded and that nothing
repeated needs (a publish after the failure, say) do not. A failed-only re-run
that could not run a step that failed - a push-only stage, which a re-run's
trigger filters out - is refused with the reason rather than passing without it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineRerun(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(rerunCommand)
	registerPipelineRerunFlags(rerunCommand)
	registerStructuredOutputFlags(rerunCommand)
	return rerunCommand
}

// registerPipelineRerunFlags is shared by `pipeline rerun` and
// `application pipeline rerun`.
func registerPipelineRerunFlags(command *cobra.Command) {
	command.Flags().Bool("failed-only", false, "Re-run only the steps that did not succeed, whatever depends on them, and the steps they build on (checkout included)")
	command.Flags().Bool("wait", false, "Wait for the new run to conclude before returning")
	registerPipelineWaitTimeoutFlag(command)
}

func runPipelineRerun(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	failedOnly, _ := command.Flags().GetBool("failed-only")
	wait, _ := command.Flags().GetBool("wait")
	timeout, timeoutError := pipelineWaitTimeoutFromFlags(command, wait, "--wait")
	if timeoutError != nil {
		return timeoutError
	}

	result, rerunError := apiClient.RerunPipelineRun(command.Context(), selector, strings.TrimSpace(runID), failedOnly)
	if rerunError != nil {
		return rerunError
	}
	if !wait {
		if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Run #%d queued: %s\n", result.RunNumber, result.PipelineRunID)
		return nil
	}
	runWait := startPipelineRunWait(command.Context(), timeout)
	defer runWait.stop()
	detail, waitError := waitForPipelineRunConclusion(command, selector, result.PipelineRunID, runWait)
	if waitError != nil {
		return waitError
	}
	return renderConcludedPipelineRun(command, format, detail, selector)
}
