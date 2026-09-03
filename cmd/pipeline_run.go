package cmd

// The run lifecycle: dispatch, list, get, cancel, rerun. Each RunE here
// resolves its own selector from --application/--repository and then calls
// the shared runPipeline* function; cmd/application_pipeline.go calls the
// same functions with a selector forced from a leading <application-id>
// argument, so the two surfaces cannot drift.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineRunWaitPollInterval is how often `run --wait` and `rerun --wait`
// re-read the run while it is in flight.
const pipelineRunWaitPollInterval = 3 * time.Second

func newPipelineRunCommand() *cobra.Command {
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Dispatch a manual pipeline run",
		Long: `Dispatch a manual run of a pipeline's stored definition.

--sha is mandatory: resolving a ref to a commit belongs to the trigger lane
(push/PR/tag webhooks), so a dispatch that names no commit is refused rather
than run against whatever commit the platform happens to have stored last.`,
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
	command.Flags().String("sha", "", "Full commit sha to run (required)")
	command.Flags().StringArray("input", nil, "Dispatch input as key=value (repeatable)")
	command.Flags().String("reason", "", "Human note recorded on the run")
	command.Flags().String("spec-file", "", "Run this pipeline definition instead of the stored one (requires pipelines.manage)")
	command.Flags().Bool("wait", false, "Wait for the run to conclude before returning")
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
		return withExitCode(exitUsage, fmt.Errorf("--sha is required: a pipeline run needs the full commit sha to run at"))
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

	detail, waitError := waitForPipelineRunConclusion(command, selector, result.PipelineRunID)
	if waitError != nil {
		return waitError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, detail)
	}
	printPipelineRunDetail(command.OutOrStdout(), *detail)
	return pipelineRunConclusionError(detail.PipelineRun)
}

// waitForPipelineRunConclusion polls GetPipelineRun until the run's status is
// "concluded". There is no --timeout: a person who wants to give up presses
// Ctrl+C, the same contract 'cluster operations list --watch' already gives.
func waitForPipelineRunConclusion(command *cobra.Command, selector client.PipelineSelector,
	runID string) (*client.PipelineRunDetail, error) {
	progress := command.ErrOrStderr()
	announced := ""
	for {
		detail, getError := apiClient.GetPipelineRun(command.Context(), selector, runID)
		if getError != nil {
			return nil, getError
		}
		if detail.Status != announced {
			_, _ = fmt.Fprintf(progress, "Run #%d is %s.\n", detail.RunNumber, detail.Status)
			announced = detail.Status
		}
		if detail.Status == "concluded" {
			return detail, nil
		}
		time.Sleep(pipelineRunWaitPollInterval)
	}
}

// pipelineRunConclusionError reports a non-success conclusion as an error so
// `--wait` exits non-zero on a failed run, the way a CI step should.
func pipelineRunConclusionError(run client.PipelineRun) error {
	outcome := pipelineOptionalString(run.Outcome)
	if outcome == "success" {
		return nil
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
	limit, _ := command.Flags().GetInt("limit")

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
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineGet(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(getCommand)
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runPipelineGet(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	detail, getError := apiClient.GetPipelineRun(command.Context(), selector, strings.TrimSpace(runID))
	if getError != nil {
		return getError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, detail)
	}
	printPipelineRunDetail(command.OutOrStdout(), *detail)
	return nil
}

func printPipelineRunDetail(out io.Writer, detail client.PipelineRunDetail) {
	_, _ = fmt.Fprintf(out, "Run #%d (%s)\n", detail.RunNumber, detail.ID)
	_, _ = fmt.Fprintf(out, "  Status:    %s\n", renderColouredStatus(pipelineOutcomeLabel(detail.Status, detail.Outcome)))
	_, _ = fmt.Fprintf(out, "  Trigger:   %s (%s)\n", detail.Trigger, detail.TriggerRef)
	_, _ = fmt.Fprintf(out, "  Commit:    %s\n", detail.HeadSHA)
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
	rerunCommand.Flags().Bool("wait", false, "Wait for the new run to conclude before returning")
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
	detail, waitError := waitForPipelineRunConclusion(command, selector, result.PipelineRunID)
	if waitError != nil {
		return waitError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, detail)
	}
	printPipelineRunDetail(command.OutOrStdout(), *detail)
	return pipelineRunConclusionError(detail.PipelineRun)
}
