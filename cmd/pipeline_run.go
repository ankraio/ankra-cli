package cmd

// The run lifecycle: dispatch, list, get, cancel, rerun. Each RunE here
// resolves its own selector from --application/--repository and then calls
// the shared runPipeline* function; cmd/application_pipeline.go calls the
// same functions with a selector forced from a leading <application-id>
// argument, so the two surfaces cannot drift.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineRunWaitPollInterval is how often `--wait` re-reads the run while it
// is in flight. It is a var only so the tests can shorten it; nothing else
// writes it.
var pipelineRunWaitPollInterval = 3 * time.Second

// registerPipelineWaitFlags adds the --wait pair every command that can block
// on a run shares. The timeout defaults to zero, which is "no bound": waiting
// forever and giving up on Ctrl+C is the contract `run --wait` shipped with,
// and a default that quietly abandoned a long build would change what an
// existing invocation means. A caller who wants a bound names one.
func registerPipelineWaitFlags(command *cobra.Command, waitUsage string) {
	command.Flags().Bool("wait", false, waitUsage)
	command.Flags().Duration("timeout", 0,
		"How long --wait blocks before giving up (default: no limit; expiry exits 5)")
}

// pipelineWaitContext derives the context a --wait poll loop runs under,
// bounding it by --timeout when one was named.
func pipelineWaitContext(command *cobra.Command) (context.Context, context.CancelFunc, error) {
	timeout, timeoutFlagError := command.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return nil, nil, fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	if timeout < 0 {
		return nil, nil, withExitCode(exitUsage,
			fmt.Errorf("--timeout must not be negative, got %s", timeout))
	}
	if timeout == 0 {
		return command.Context(), func() {}, nil
	}
	waitContext, cancelWait := context.WithTimeout(command.Context(), timeout)
	return waitContext, cancelWait, nil
}

func newPipelineRunCommand() *cobra.Command {
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Dispatch a manual pipeline run",
		Long: `Dispatch a manual run of a pipeline's stored definition.

A run is always dispatched at one named commit: resolving a ref to a commit
belongs to the trigger lane (push/PR/tag webhooks), so a dispatch never runs
against whatever commit the platform happens to have stored last. Inside a Git
checkout the commit is read from HEAD, and --application is read from the
repository the checkout points at, so 'ankra pipeline run' on its own runs
what you are looking at. Outside a checkout, name them with --sha and --application.`,
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
	command.Flags().String("sha", "", "Full commit sha to run (defaults to the working directory's HEAD)")
	command.Flags().StringArray("input", nil, "Dispatch input as key=value (repeatable)")
	command.Flags().String("reason", "", "Human note recorded on the run")
	command.Flags().String("spec-file", "", "Run this pipeline definition instead of the stored one (requires pipelines.manage)")
	registerPipelineWaitFlags(command, "Wait for the run to conclude before returning")
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

	sha = strings.TrimSpace(sha)
	if sha == "" {
		// The commit still has to be named - resolving a ref belongs to the
		// trigger lane, and that contract is unchanged. What changed is who
		// names it: a user standing in the checkout has the sha under their
		// cursor, and asking them to paste `git rev-parse HEAD` back is a
		// step the command can take off them (ankra-ctsmd). Outside a
		// checkout there is nothing to read and --sha is required exactly as
		// before.
		// A --ref the user named is NOT paired with whatever the working
		// directory happens to have checked out: asking to run "release-2.0"
		// from a checkout sitting on main would otherwise dispatch main's
		// commit under the release ref, which is worse than being asked for
		// the sha. The checkout answers only when it is the whole question.
		localSHA, localRef := "", ""
		if strings.TrimSpace(ref) == "" {
			localSHA, localRef = localHeadCommit(command.Context())
		}
		if localSHA == "" {
			return withExitCode(exitUsage, fmt.Errorf(
				"--sha is required: a pipeline run needs the full commit sha to run at"))
		}
		sha = localSHA
		ref = localRef
		_, _ = fmt.Fprintf(command.ErrOrStderr(),
			"Running at the working directory's HEAD %s (pass --sha to choose another).\n", sha)
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
		Ref:      strings.TrimSpace(ref),
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

	return waitForAndReportPipelineRun(command, selector, result.PipelineRunID, format)
}

// waitForAndReportPipelineRun blocks until the run concludes, renders it in
// whichever format was asked for, and then reports its conclusion through the
// exit code.
//
// Rendering and the conclusion are two separate answers, and the structured
// branch used to return before giving the second one: `--wait -o json` on a
// failed run printed the failure and exited 0, which is precisely the
// invocation a CI script uses and precisely the case pipelineRunConclusionError
// exists for. The verdict is now reported the same way whatever the caller
// asked stdout to look like.
func waitForAndReportPipelineRun(command *cobra.Command, selector client.PipelineSelector,
	runID string, format outputFormat) error {
	detail, waitError := waitForPipelineRunConclusion(command, selector, runID)
	if waitError != nil {
		return waitError
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, detail); encodeError != nil {
			return encodeError
		}
	} else {
		printPipelineRunDetail(command.OutOrStdout(), *detail)
	}
	return pipelineRunConclusionError(detail.PipelineRun)
}

// waitForPipelineRunConclusion polls GetPipelineRun until the run's status is
// "concluded". Without --timeout it waits as long as the run takes and a
// person who wants to give up presses Ctrl+C, the same contract
// 'cluster operations list --watch' already gives; with one, an expired budget
// is the scripting contract's wait-timeout exit rather than a bare context
// error, so a caller can tell "still running" from "the platform said no".
func waitForPipelineRunConclusion(command *cobra.Command, selector client.PipelineSelector,
	runID string) (*client.PipelineRunDetail, error) {
	waitContext, cancelWait, contextError := pipelineWaitContext(command)
	if contextError != nil {
		return nil, contextError
	}
	defer cancelWait()

	progress := command.ErrOrStderr()
	announced := ""
	for {
		detail, getError := apiClient.GetPipelineRun(waitContext, selector, runID)
		if getError != nil {
			if expiryError := pipelineWaitExpired(command, waitContext, runID); expiryError != nil {
				return nil, expiryError
			}
			return nil, getError
		}
		if detail.Status != announced {
			_, _ = fmt.Fprintf(progress, "Run #%d is %s.\n", detail.RunNumber, detail.Status)
			announced = detail.Status
		}
		if detail.Status == pipelineRunStatusConcluded {
			return detail, nil
		}
		if sleepError := sleepInterrupted(waitContext, pipelineRunWaitPollInterval); sleepError != nil {
			if expiryError := pipelineWaitExpired(command, waitContext, runID); expiryError != nil {
				return nil, expiryError
			}
			return nil, sleepError
		}
	}
}

// pipelineWaitExpired answers the wait-timeout error when --timeout is what
// ended the wait, and nil when something else did. A Ctrl+C cancels the
// command's own context and must not be reported as an expired budget, so the
// distinction is drawn on which context is done: the outer one being live
// while the derived one is not leaves only the timeout.
func pipelineWaitExpired(command *cobra.Command, waitContext context.Context, runID string) error {
	if command.Context().Err() != nil || waitContext.Err() == nil {
		return nil
	}
	return withExitCode(exitWaitTimeout, fmt.Errorf(
		"--timeout expired while waiting for run %s; it keeps running - follow it with 'ankra pipeline get %s'",
		runID, runID))
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
func pipelineRunConclusionError(run client.PipelineRun) error {
	outcome := pipelineOptionalString(run.Outcome)
	if outcome == "success" {
		return nil
	}
	if run.Outcome == nil || strings.TrimSpace(*run.Outcome) == "" {
		return fmt.Errorf("run #%d concluded without recording an outcome", run.RunNumber)
	}
	message := fmt.Sprintf("run #%d concluded %s", run.RunNumber, outcome)
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
		Args:    cobra.NoArgs,
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
	command.Flags().Int("limit", 0, "Maximum number of runs to return (server default 50, max 100)")
}

func runPipelineList(command *cobra.Command, selector client.PipelineSelector) error {
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
			renderColouredStatus(pipelineOutcomeLabel(run.Status, run.Outcome)),
			run.Trigger,
			run.TriggerRef,
			pipelineShortSHA(run.HeadSHA),
			formatTimeAgo(run.QueuedAt),
		})
	}
	writer.Render()
}

func newPipelineGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <run>",
		Short: "Show a pipeline run's detail",
		Long: `Show a pipeline run's detail.

--wait blocks until the run concludes and then prints it, which is what a
webhook-started run needs: 'run --wait' and 'rerun --wait' can only wait on a
run they dispatched themselves, and a push or pull_request run was dispatched
by the trigger lane. Bound it with --timeout; without one it waits as long as
the run takes.

A run's outcome reaches the exit code only when you ask for it, because a bare
'get' is a read and scripts already depend on it succeeding whatever it finds.
--wait asks for it implicitly - waiting for a verdict and then discarding it
is not a thing to make a caller write - and --exit-code asks for it without
waiting, so a run that is still going exits 0 and a concluded one exits 1
unless its outcome is success. Either way the detail is printed first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineGet(command, selector, arguments[0])
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
	registerPipelineWaitFlags(command, "Wait for the run to conclude before printing it")
	command.Flags().Bool("exit-code", false,
		"Exit non-zero when the run has concluded with an outcome other than success (implied by --wait)")
}

func runPipelineGet(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	runID = strings.TrimSpace(runID)
	wait, _ := command.Flags().GetBool("wait")
	if wait {
		return waitForAndReportPipelineRun(command, selector, runID, format)
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

	exitOnOutcome, _ := command.Flags().GetBool("exit-code")
	if !exitOnOutcome || detail.Status != pipelineRunStatusConcluded {
		// A run that has not concluded has no outcome to report, and reporting
		// "not success yet" as a failure would make --exit-code answer "did it
		// fail" with "it has not finished". That is what --wait is for.
		return nil
	}
	return pipelineRunConclusionError(detail.PipelineRun)
}

func printPipelineRunDetail(out io.Writer, detail client.PipelineRunDetail) {
	_, _ = fmt.Fprintf(out, "Run #%d (%s)\n", detail.RunNumber, detail.ID)
	_, _ = fmt.Fprintf(out, "  Status:    %s\n", renderColouredStatus(pipelineOutcomeLabel(detail.Status, detail.Outcome)))
	_, _ = fmt.Fprintf(out, "  Trigger:   %s (%s)\n", detail.Trigger, detail.TriggerRef)
	_, _ = fmt.Fprintf(out, "  Commit:    %s\n", detail.HeadSHA)
	printPipelineRunAuthority(out, detail.PipelineRun)
	if detail.ErrorMessage != nil && *detail.ErrorMessage != "" {
		_, _ = fmt.Fprintf(out, "  Error:     %s\n", *detail.ErrorMessage)
	}
	_, _ = fmt.Fprintf(out, "  Queued:    %s\n", formatTimeAgo(detail.QueuedAt))
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
			renderColouredStatus(pipelineOutcomeLabel(step.Status, step.Outcome)),
			exitCode,
		})
	}
	writer.Render()
}

// printPipelineRunAuthority prints the run's recorded authority state
// (ankra-vn0bd.10.8) when the server recorded one: whose protected sections
// the run executed and, for a state other than "approved", that an
// administrator's approval would change it. A run planned before authority
// tracking existed carries no state and this prints nothing, matching how
// the wire field is null rather than empty.
//
// This deliberately never turns AuthorityDefinitionID into an
// "ankra pipeline definitions approve <id>" command. That field names the
// definition the run's CURRENTLY TRUSTED authority was drawn from - already
// approved, or the default branch's own when it protects nothing - never the
// definition an "unapproved" or "changed_on_head" run is waiting on: that is
// always the repository's CURRENT default-branch definition
// (enginekit/pipelinerun.CurrentDefaultBranchDefinition, resolved
// server-side for the pull request status comment and not carried on this
// response at all). Naming AuthorityDefinitionID as "the one to approve"
// would be wrong exactly when it matters most: for "unapproved" it is
// typically an older definition an administrator already approved (or
// empty, when none ever was), and for "changed_on_head" it is at best the
// default branch's own already-approved definition - approving either 409s
// rather than fixing anything.
func printPipelineRunAuthority(out io.Writer, run client.PipelineRun) {
	if run.AuthorityState == nil || *run.AuthorityState == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "  Authority: %s\n", *run.AuthorityState)
	if run.AuthorityDefinitionID != nil && *run.AuthorityDefinitionID != "" {
		_, _ = fmt.Fprintf(out, "             trusted authority taken from definition %s\n", *run.AuthorityDefinitionID)
	}
	if *run.AuthorityState != "approved" {
		_, _ = fmt.Fprintln(out, "             an administrator approving the repository's current default-branch "+
			"definition would update this - see the pull request's status comment for its id, or "+
			"'ankra pipeline definitions get <id>' once you have one")
	}
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
	rerunCommand.Flags().Bool("failed-only", false, "Re-run only the steps that did not succeed, and whatever depends on them")
	registerPipelineWaitFlags(rerunCommand, "Wait for the new run to conclude before returning")
	registerStructuredOutputFlags(rerunCommand)
	return rerunCommand
}

func runPipelineRerun(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	failedOnly, _ := command.Flags().GetBool("failed-only")
	wait, _ := command.Flags().GetBool("wait")

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
	return waitForAndReportPipelineRun(command, selector, result.PipelineRunID, format)
}
