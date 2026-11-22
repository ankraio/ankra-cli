package cmd

// `ankra pipeline release-queue` and `ankra pipeline resource-suggestions`
// (ankra-hq41xb.22): how many releases of a pipeline are in flight, and what
// the stages of a run's definition actually used against what they ask for.
// Both only read.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineResourceSuggestionsMaximumWindow is the most attempts per stage the
// platform sizes from.
const pipelineResourceSuggestionsMaximumWindow = 100

func newPipelineReleaseQueueCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "release-queue",
		Short: "Count the releases of a pipeline that are queued or running",
		Long: `Count the release runs of a pipeline's branch that are queued or running,
and name the oldest one. A release behind others waits for them, so this is
the answer to "why has my merge not shipped yet".

The counts say what the pipeline is doing, not whether a deployment is live.
It needs the pipelines.read permission.`,
		Example: `  ankra pipeline release-queue --application my-app
  ankra pipeline release-queue --repository <repository-id> -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			queue, readError := apiClient.GetPipelineReleaseQueue(command.Context(), selector)
			if readError != nil {
				if errors.Is(readError, client.ErrPipelineReleaseQueueUnavailable) {
					return withExitCode(exitNotFound, errors.New(
						"no release queue found: the pipeline does not exist, or the platform does not report release queues yet"))
				}
				return readError
			}
			output := command.OutOrStdout()
			if format != outputDefault {
				return encodeStructured(output, format, queue)
			}
			_, _ = fmt.Fprintf(output, "Branch:  %s\nQueued:  %d\nRunning: %d\n",
				pipelineStringOrDash(pipelineTestText(queue.Branch)), queue.Queued, queue.Running)
			if oldestRunID := strings.TrimSpace(queue.OldestRunID); oldestRunID != "" {
				_, _ = fmt.Fprintf(output, "Oldest:  %s (ankra pipeline get %s%s)\n", oldestRunID, oldestRunID,
					pipelineSelectorArguments(selector))
			}
			_, _ = fmt.Fprintf(output, "Checked: %s\n", queue.CheckedAt)
			return nil
		},
	}
	registerPipelineSelectorFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func newPipelineResourceSuggestionsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "resource-suggestions <run>",
		Short: "Show right-sizing advice for the stages of a pipeline run",
		Long: `Show right-sizing advice for the stages of the pipeline definition a run was
planned from: for each stage, the 95th percentile of what its latest
successful attempts used plus headroom, against what the stage asks for.

Memory is sized from the kernel's memory peak; a stage whose peak reached its
limit is "at limit" and gets no suggestion, because the limit hid what it
needed. CPU is sized from mean cores. A stage that is not listed has no
successful attempt with a usage report yet, which is not the same as using
nothing. The suggested values go in the stage's resources.memory and
resources.cpu in .ankra/pipeline.yaml; nothing is written for you.

It needs the pipelines.read permission.`,
		Example: `  ankra pipeline resource-suggestions <run-id> --application my-app
  ankra pipeline resource-suggestions <run-id> --repository <repository-id> --window 50 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			window, _ := command.Flags().GetInt("window")
			if command.Flags().Changed("window") && (window < 1 || window > pipelineResourceSuggestionsMaximumWindow) {
				return withExitCode(exitUsage, fmt.Errorf("--window must be between 1 and %d",
					pipelineResourceSuggestionsMaximumWindow))
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			runID := strings.TrimSpace(arguments[0])
			suggestions, readError := apiClient.GetPipelineRunResourceSuggestions(command.Context(), selector, runID, window)
			if readError != nil {
				if errors.Is(readError, client.ErrPipelineResourceSuggestionsUnavailable) {
					return withExitCode(exitNotFound, fmt.Errorf(
						"no resource suggestions found for run %s: the run does not exist, or the platform does not report resource suggestions yet",
						runID))
				}
				return readError
			}
			output := command.OutOrStdout()
			if format != outputDefault {
				return encodeStructured(output, format, suggestions)
			}
			printPipelineResourceSuggestions(output, *suggestions)
			return nil
		},
	}
	command.Flags().Int("window", 0, "Latest successful attempts of each stage to size from, 1 to 100 (default: the platform's, 20)")
	registerPipelineSelectorFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func printPipelineResourceSuggestions(output io.Writer, suggestions client.PipelineResourceSuggestions) {
	if len(suggestions.Stages) == 0 {
		_, _ = fmt.Fprintln(output, "No stage of this run's definition has a successful attempt with a usage report yet.")
		return
	}
	stageTable := newPipelineTestsTable(output, table.Row{"STAGE", "SAMPLES", "MEMORY", "MEMORY ADVICE", "CPU", "CPU ADVICE"})
	for _, stage := range suggestions.Stages {
		stageTable.AppendRow(table.Row{
			pipelineTestText(stage.Stage),
			stage.SampleCount,
			pipelineResourceStatus(stage.Memory.Status),
			pipelineResourceAdvice(stage.Memory.Status, stage.Memory.Suggested),
			pipelineResourceStatus(stage.CPU.Status),
			pipelineResourceAdvice(stage.CPU.Status, stage.CPU.Suggested),
		})
	}
	stageTable.Render()
	_, _ = fmt.Fprintf(output, "Sized from the p%d of each stage's latest %d successful attempts, plus %d%% headroom.\n",
		suggestions.Method.Percentile, suggestions.Window, suggestions.Method.HeadroomPercent)
}

func pipelineResourceStatus(status string) string {
	return pipelineStringOrDash(strings.ReplaceAll(pipelineTestText(status), "_", " "))
}

// pipelineResourceAdvice is the value to set, or why there is none: a stage
// at its limit needs a higher limit before its need can be measured.
func pipelineResourceAdvice(status string, suggested *string) string {
	if value := strings.TrimSpace(pipelineStringValue(suggested)); value != "" {
		return "set " + pipelineTestText(value)
	}
	switch status {
	case "right_sized":
		return "keep"
	case "at_limit":
		return "raise the limit, then measure again"
	case "insufficient_samples":
		return "not enough samples yet"
	default:
		return "-"
	}
}
