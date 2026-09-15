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
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineDispatch(command, selector)
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
// A --repository selector names a pipeline repository by id, and there is no
// lookup from that id to "owner/name" here, so a checkout cannot be matched
// to it and the answer is no. So is every incomplete answer from the listing:
// sending a foreign commit is worse than letting the platform read the tip.
func checkoutBuildsSelectedPipeline(requestContext context.Context, selector client.PipelineSelector) bool {
	if selector.ApplicationID == "" {
		return false
	}
	_, applicationIDs, _, known := checkoutApplications(requestContext)
	if !known {
		return false
	}
	for _, applicationID := range applicationIDs {
		if strings.EqualFold(applicationID, selector.ApplicationID) {
			return true
		}
	}
	return false
}

// runPipelineDispatch reads the dispatch flags and drives the shared
// CreatePipelineRun call; used by both `pipeline run` and
// `application pipeline run`.
func runPipelineDispatch(command *cobra.Command, selector client.PipelineSelector) error {
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
			if localSHA != "" && checkoutBuildsSelectedPipeline(command.Context(), selector) {
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
	return renderConcludedPipelineRun(command, format, detail)
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
		return renderConcludedPipelineRun(command, format, detail)
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
		printPipelineRunDetail(command.OutOrStdout(), *detail)
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

func printPipelineRunDetail(out io.Writer, detail client.PipelineRunDetail) {
	_, _ = fmt.Fprintf(out, "Run #%d (%s)\n", detail.RunNumber, detail.ID)
	_, _ = fmt.Fprintf(out, "  Status:    %s\n", renderPipelineRunState(detail.PipelineRun))
	printPipelineRunSupersession(out, detail.PipelineRun)
	_, _ = fmt.Fprintf(out, "  Trigger:   %s (%s)\n", detail.Trigger, detail.TriggerRef)
	_, _ = fmt.Fprintf(out, "  Commit:    %s\n", detail.HeadSHA)
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
	writer.AppendHeader(table.Row{"STEP", "STAGE", "KIND", "STATUS", "EXIT"})
	for _, step := range detail.Steps {
		exitCode := "-"
		if step.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *step.ExitCode)
		}
		writer.AppendRow(table.Row{
			step.StepKey,
			step.Stage,
			step.Kind,
			renderPipelineState(step.Status, step.Outcome),
			exitCode,
		})
	}
	writer.Render()
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
--failed-only restricts the new run to the steps that did not succeed and
whatever depended on them.`,
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
	command.Flags().Bool("failed-only", false, "Re-run only the steps that did not succeed, and whatever depends on them")
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
	return renderConcludedPipelineRun(command, format, detail)
}
