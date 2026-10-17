package cmd

// Time saved (ankra-efrnzf.20): how much CI time path filters and promoted
// publishes kept a pipeline from spending. `ankra pipeline get` prints one
// run's figure under its steps, and `ankra pipeline time-saved` totals a
// repository or the organisation over the last 7 or 30 days.
//
// Every figure is the platform's estimate, made when the run was planned or
// skipped, from the stage's own recent history in the repository. A saving
// with no history is "unknown" and is never added to a total. The time is
// runner time (step-seconds), not wall-clock time.

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// pipelineGetOutput is `ankra pipeline get -o json|yaml`: the run detail
// exactly as before, with time_saved added after its last field. TimeSaved is
// null when the platform has nothing to say (older than the route, or the
// read failed and said so on stderr).
type pipelineGetOutput struct {
	client.PipelineRunDetail `yaml:",inline"`
	TimeSaved                *client.PipelineRunTimeSaved `json:"time_saved" yaml:"time_saved"`
}

// readPipelineRunTimeSaved reads a run's time saved for `pipeline get`. It
// never fails the command: a 404 (a platform older than the route) is
// "nothing to show", and any other error is one line on stderr.
func readPipelineRunTimeSaved(command *cobra.Command, selector client.PipelineSelector,
	runID string) *client.PipelineRunTimeSaved {
	saved, readError := apiClient.GetPipelineRunTimeSaved(command.Context(), selector, runID)
	if readError != nil {
		if !errors.Is(readError, client.ErrPipelineTimeSavedUnavailable) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Note: could not read the time this run saved: %v\n", readError)
		}
		return nil
	}
	return saved
}

// renderPipelineGet writes `pipeline get`'s answer: the structured body with
// time_saved added, or the readable detail followed by the time saved.
func renderPipelineGet(command *cobra.Command, format outputFormat, detail *client.PipelineRunDetail,
	timeSaved *client.PipelineRunTimeSaved, selector client.PipelineSelector) error {
	out := command.OutOrStdout()
	if format != outputDefault {
		return encodeStructured(out, format, pipelineGetOutput{PipelineRunDetail: *detail, TimeSaved: timeSaved})
	}
	printPipelineRunDetail(out, *detail, selector)
	printPipelineRunTimeSaved(out, timeSaved)
	return nil
}

// printPipelineRunTimeSaved prints one line with the run's total and a line
// per saving under it. A run that saved nothing that counts, one planned
// before savings were recorded, and a platform older than the route all
// print nothing.
func printPipelineRunTimeSaved(out io.Writer, saved *client.PipelineRunTimeSaved) {
	if saved == nil || len(saved.Savings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nTime saved: %s\n", pipelineRunTimeSavedSummaryLine(*saved))
	for _, saving := range saved.Savings {
		_, _ = fmt.Fprintf(out, "  %s: %s, %s (%s)\n", pipelineTimeSavedSubject(saving),
			pipelineTimeSavedReason(saving), pipelineTimeSavedEstimate(saving.SavedSeconds),
			pipelineTimeSavedBasis(saving))
	}
}

// pipelineRunTimeSavedSummaryLine is "about 14 min of CI time (3 stages
// skipped, 1 unknown)".
func pipelineRunTimeSavedSummaryLine(saved client.PipelineRunTimeSaved) string {
	total := "unknown"
	if saved.EstimatedCount > 0 {
		total = describeSavedSeconds(saved.SavedSeconds) + " of CI time"
		if cpu := describeSavedCPUMinutes(saved.SavedCPUMinutes); cpu != "" {
			total += " and " + cpu
		}
	}
	skipped := 0
	isWholeRun := false
	for _, saving := range saved.Savings {
		if saving.Reason == "run_paths_excluded" {
			isWholeRun = true
			continue
		}
		skipped++
	}
	var parts []string
	if isWholeRun {
		parts = append(parts, "the whole run was skipped")
	}
	if skipped > 0 {
		noun := "stages"
		if skipped == 1 {
			noun = "stage"
		}
		parts = append(parts, fmt.Sprintf("%d %s skipped", skipped, noun))
	}
	if saved.UnknownCount > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", saved.UnknownCount))
	}
	return total + " (" + strings.Join(parts, ", ") + ")"
}

func pipelineTimeSavedSubject(saving client.PipelineTimeSavedEntry) string {
	switch {
	case saving.Reason == "run_paths_excluded" || (saving.StepKey == "" && saving.Stage == ""):
		return "whole run"
	case saving.StepKey != "":
		return saving.StepKey
	default:
		return saving.Stage
	}
}

func pipelineTimeSavedReason(saving client.PipelineTimeSavedEntry) string {
	switch saving.Reason {
	case "path_filter":
		return "left out by a path filter"
	case "dependency_skipped":
		if saving.CauseStage != nil && strings.TrimSpace(*saving.CauseStage) != "" {
			return fmt.Sprintf("skipped because %s was left out by a path filter", strings.TrimSpace(*saving.CauseStage))
		}
		return "skipped because a stage it needs was left out by a path filter"
	case "promoted":
		return "not run again, the publish was promoted"
	case "run_paths_excluded":
		return "the trigger's paths matched none of the changed files"
	default:
		return saving.Reason
	}
}

func pipelineTimeSavedBasis(saving client.PipelineTimeSavedEntry) string {
	switch saving.Basis {
	case "stage_history":
		return fmt.Sprintf("median of %s of this stage", describeSuccessfulRuns(saving.SampleCount))
	case "step_history":
		return fmt.Sprintf("median of %s of this step", describeSuccessfulRuns(saving.SampleCount))
	case "promoted_source_run":
		if saving.SourceRunID != nil && strings.TrimSpace(*saving.SourceRunID) != "" {
			return "its time in run " + strings.TrimSpace(*saving.SourceRunID)
		}
		return "its time in the run it was promoted from"
	case "run_history":
		return fmt.Sprintf("median of %s of this trigger", describeSuccessfulRuns(saving.SampleCount))
	case "no_history":
		return "no history to estimate from"
	default:
		return saving.Basis
	}
}

func describeSuccessfulRuns(count int) string {
	if count == 1 {
		return "1 successful run"
	}
	return fmt.Sprintf("%d successful runs", count)
}

func pipelineTimeSavedEstimate(seconds *int64) string {
	if seconds == nil {
		return "unknown"
	}
	return describeSavedSeconds(*seconds)
}

// describeSavedSeconds is "about 45 s", "about 14 min" or "about 2 h 5 min".
func describeSavedSeconds(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("about %d s", max(seconds, 0))
	case seconds < 3600:
		return fmt.Sprintf("about %d min", int64(math.Round(float64(seconds)/60)))
	}
	minutes := int64(math.Round(float64(seconds) / 60))
	if minutes%60 == 0 {
		return fmt.Sprintf("about %d h", minutes/60)
	}
	return fmt.Sprintf("about %d h %d min", minutes/60, minutes%60)
}

func describeSavedCPUMinutes(minutes *float64) string {
	if minutes == nil || *minutes <= 0 {
		return ""
	}
	return fmt.Sprintf("about %s CPU-minutes", formatFloatTrim(*minutes))
}

func formatFloatTrim(value float64) string {
	if value >= 10 {
		return fmt.Sprintf("%d", int64(math.Round(value)))
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
}

func newPipelineTimeSavedCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "time-saved",
		Short: "Show how much CI time path filters and promoted publishes saved",
		Long: `Show how much CI time path filters and promoted publishes saved over the
last 7 or 30 days: for one pipeline with --application or --repository, or
for the whole organisation with neither.

Each figure is an estimate the platform made when a run was planned or
skipped, from the stage's own recent history in the repository: the median of
its newest 20 successful runs. A stage with no history is counted as unknown
and is never added to a total. The time is runner time (step-seconds), not
wall-clock time. Only runs planned after the platform started recording this
are counted.

'ankra pipeline get <run>' shows what one run saved.`,
		Example: `  # The organisation over the last 30 days
  ankra pipeline time-saved

  # One application's pipeline over the last 7 days, as JSON
  ankra pipeline time-saved --application my-app --window 7d -o json`,
		Args: cobra.NoArgs,
		RunE: runPipelineTimeSaved,
	}
	registerPipelineSelectorFlags(command)
	command.Flags().String("window", "30d", "How far back to count: 7d or 30d")
	registerStructuredOutputFlags(command)
	return command
}

func runPipelineTimeSaved(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	window, _ := command.Flags().GetString("window")
	window = strings.TrimSpace(window)
	if window != "7d" && window != "30d" {
		return withExitCode(exitUsage, fmt.Errorf("--window must be 7d or 30d, got %q", window))
	}
	// Neither flag reads the organisation: unlike the run commands, this one
	// does not infer a pipeline from the working directory.
	selector := client.PipelineSelector{}
	applicationReference, _ := command.Flags().GetString("application")
	repositoryID, _ := command.Flags().GetString("repository")
	if strings.TrimSpace(applicationReference) != "" || strings.TrimSpace(repositoryID) != "" {
		resolved, selectorError := resolvePipelineSelector(command)
		if selectorError != nil {
			return selectorError
		}
		selector = resolved
	}
	summary, readError := apiClient.GetPipelineTimeSavedSummary(command.Context(), selector, window)
	if readError != nil {
		if errors.Is(readError, client.ErrPipelineTimeSavedUnavailable) {
			return withExitCode(exitNotFound, errors.New("this platform does not report the time pipelines saved yet"))
		}
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, summary)
	}
	printPipelineTimeSavedSummary(command.OutOrStdout(), *summary, selector)
	return nil
}

func printPipelineTimeSavedSummary(out io.Writer, summary client.PipelineTimeSavedSummary,
	selector client.PipelineSelector) {
	period := "the last 30 days"
	if summary.Window == "7d" {
		period = "the last 7 days"
	}
	if summary.Runs == 0 {
		_, _ = fmt.Fprintf(out, "Time saved in %s: nothing recorded.\n", period)
		return
	}
	runs := "runs"
	if summary.Runs == 1 {
		runs = "run"
	}
	total := describeSavedSeconds(summary.SavedSeconds) + " of CI time"
	if cpu := describeSavedCPUMinutes(summary.SavedCPUMinutes); cpu != "" {
		total += " and " + cpu
	}
	line := fmt.Sprintf("Time saved in %s: %s across %d %s", period, total, summary.Runs, runs)
	if summary.UnknownCount > 0 {
		line += fmt.Sprintf(" (%d skipped with no history, not counted)", summary.UnknownCount)
	}
	_, _ = fmt.Fprintln(out, line)

	if len(summary.Reasons) > 0 {
		_, _ = fmt.Fprintln(out, "\nBy reason:")
		for _, reason := range summary.Reasons {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", pipelineTimeSavedReasonLabel(reason.Reason),
				describeSavedSeconds(reason.SavedSeconds))
		}
	}
	isOrganisation := selector.RepositoryID == "" && selector.ApplicationID == ""
	if isOrganisation && len(summary.Repositories) > 0 {
		_, _ = fmt.Fprintln(out, "\nBy repository:")
		for _, repository := range summary.Repositories {
			name := repository.FullName
			if strings.TrimSpace(name) == "" {
				name = repository.RepositoryID
			}
			_, _ = fmt.Fprintf(out, "  %s: %s (%d %s)\n", name, describeSavedSeconds(repository.SavedSeconds),
				repository.Runs, pluralRuns(repository.Runs))
		}
	}
}

func pluralRuns(count int) string {
	if count == 1 {
		return "run"
	}
	return "runs"
}

func pipelineTimeSavedReasonLabel(reason string) string {
	switch reason {
	case "path_filter":
		return "stages left out by a path filter"
	case "dependency_skipped":
		return "stages skipped behind a path filter"
	case "promoted":
		return "steps a promoted publish did not run again"
	case "run_paths_excluded":
		return "whole runs skipped by the trigger's paths"
	default:
		return reason
	}
}
