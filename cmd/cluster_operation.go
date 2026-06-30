package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

const (
	defaultExecutionsPageSize = 50
	executionRequestTimeout   = 30 * time.Second
	defaultWatchInterval      = 5 * time.Second
	// minWatchInterval keeps a misconfigured --interval from hammering the API
	// with a tight polling loop.
	minWatchInterval = 1 * time.Second
)

// isTerminalExecutionStatus reports whether an execution (or step) status will
// not change again, so a --watch loop knows when to stop polling.
func isTerminalExecutionStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "failed", "critical", "error", "cancelled", "canceled", "timeout":
		return true
	default:
		return false
	}
}

// clampWatchInterval keeps the watch poll cadence at a sane floor: non-positive
// values fall back to the default, and anything below minWatchInterval is
// raised to it so a stray --interval cannot tight-loop the API.
func clampWatchInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultWatchInterval
	}
	if interval < minWatchInterval {
		return minWatchInterval
	}
	return interval
}

// clearScreen emits the ANSI clear sequence only when stdout is an interactive
// terminal, so piping --watch output to a file or pager does not litter it with
// escape codes.
func clearScreen() {
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Print("\033[H\033[2J")
}

var clusterOperationsCmd = &cobra.Command{
	Use:     "operations",
	Aliases: []string{"executions"},
	Short:   "Manage executions (deployments and platform writes)",
	Long:    "Commands to list, inspect, retry, and cancel executions and their steps.",
}

var clusterOperationsListCmd = &cobra.Command{
	Use:     "list [execution ID]",
	Aliases: []string{"ls"},
	Short:   "List executions for the active cluster; optionally, provide an ID for details",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		statusFlag, _ := cmd.Flags().GetStringSlice("status")
		failedOnly, _ := cmd.Flags().GetBool("failed")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = defaultExecutionsPageSize
		}

		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		watch, _ := cmd.Flags().GetBool("watch")
		intervalFlag, _ := cmd.Flags().GetDuration("interval")
		interval := clampWatchInterval(intervalFlag)

		if watch && format != outputDefault {
			return fmt.Errorf("--watch cannot be combined with -o %s; structured output is rendered once", format)
		}

		statusList := statusFlag
		if failedOnly {
			statusList = append(statusList, "failed", "critical")
		}
		options := client.ListExecutionsOptions{
			ClusterID:  cluster.ID,
			StatusList: statusList,
			Page:       1,
			PageSize:   limit,
		}

		executionID := ""
		if len(args) > 0 {
			executionID = strings.TrimSpace(args[0])
		}

		if format != outputDefault {
			return renderExecutionsStructured(format, options, executionID)
		}

		if !watch {
			_, err := renderExecutionsOnce(options, executionID)
			return err
		}

		for {
			clearScreen()
			fmt.Printf("Watching executions (every %s, press Ctrl+C to stop) - %s\n\n",
				interval, time.Now().Format("15:04:05"))
			keepWatching, err := renderExecutionsOnce(options, executionID)
			if err != nil {
				return err
			}
			if !keepWatching {
				fmt.Println("\nAll executions have reached a terminal state. Stopping watch.")
				return nil
			}
			time.Sleep(interval)
		}
	},
}

// renderExecutionsOnce prints either the executions table or a single
// execution detail, returning whether any rendered execution is still active
// (used to decide whether a --watch loop keeps polling).
func renderExecutionsOnce(options client.ListExecutionsOptions, executionID string) (keepWatching bool, err error) {
	if executionID != "" {
		detail, err := apiClient.GetExecution(executionID)
		if err != nil {
			return false, fmt.Errorf("fetching execution %s: %w", executionID, err)
		}
		printExecutionDetail(detail)
		return !isTerminalExecutionStatus(detail.Execution.Status), nil
	}

	response, err := apiClient.ListExecutions(options)
	if err != nil {
		return false, fmt.Errorf("listing executions: %w", err)
	}
	if len(response.Result) == 0 {
		fmt.Println("No executions found for the active cluster.")
		return false, nil
	}
	renderExecutionsTable(response.Result)

	for _, execution := range response.Result {
		if !isTerminalExecutionStatus(execution.Status) {
			return true, nil
		}
	}
	return false, nil
}

// renderExecutionsStructured prints the list response (or a single execution
// detail) using a structured -o format (json or yaml).
func renderExecutionsStructured(format outputFormat, options client.ListExecutionsOptions, executionID string) error {
	if executionID != "" {
		detail, err := apiClient.GetExecution(executionID)
		if err != nil {
			return fmt.Errorf("fetching execution %s: %w", executionID, err)
		}
		return encodeStructured(os.Stdout, format, detail)
	}
	response, err := apiClient.ListExecutions(options)
	if err != nil {
		return fmt.Errorf("listing executions: %w", err)
	}
	return encodeStructured(os.Stdout, format, response.Result)
}

var clusterOperationsCancelCmd = &cobra.Command{
	Use:     "cancel <execution_id> [<execution_id>...]",
	Short:   "Cancel one or more running executions",
	Aliases: []string{"stop"},
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), executionRequestTimeout)
		defer cancel()

		if len(args) == 1 {
			result, err := apiClient.CancelExecution(ctx, args[0])
			if err != nil {
				return fmt.Errorf("cancelling execution: %w", err)
			}
			if rendered, err := renderStructured(cmd, result); rendered || err != nil {
				return err
			}
			fmt.Printf("Execution '%s' cancelled (status: %s)\n", result.ExecutionID, result.Status)
			return nil
		}

		result, err := apiClient.BatchCancelExecutions(ctx, args)
		if err != nil {
			return fmt.Errorf("cancelling executions: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			if err == nil && len(result.NotFound) > 0 {
				return fmt.Errorf("%d execution(s) not found", len(result.NotFound))
			}
			return err
		}
		if len(result.Cancelled) > 0 {
			fmt.Printf("Cancelled (%d):\n", len(result.Cancelled))
			for _, id := range result.Cancelled {
				fmt.Printf("  ✓ %s\n", id)
			}
		}
		if len(result.NotRunning) > 0 {
			fmt.Printf("Not running (%d):\n", len(result.NotRunning))
			for _, id := range result.NotRunning {
				fmt.Printf("  - %s\n", id)
			}
		}
		if len(result.NotFound) > 0 {
			fmt.Printf("Not found (%d):\n", len(result.NotFound))
			for _, id := range result.NotFound {
				fmt.Printf("  ✗ %s\n", id)
			}
			return fmt.Errorf("%d execution(s) not found", len(result.NotFound))
		}
		return nil
	},
}

var clusterOperationsCancelStepCmd = &cobra.Command{
	Use:     "cancel-step <execution_id> <step_id>",
	Aliases: []string{"cancel-job"},
	Short:   "Cancel a specific step within an execution",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), executionRequestTimeout)
		defer cancel()

		result, err := apiClient.CancelExecutionStep(ctx, args[0], args[1])
		if err != nil {
			return fmt.Errorf("cancelling step: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}
		fmt.Printf("Step '%s' cancelled (status: %s)\n", result.StepID, result.Status)
		return nil
	},
}

var clusterOperationsRetryCmd = &cobra.Command{
	Use:   "retry <execution_id>",
	Short: "Retry a terminal execution (failed/cancelled/timeout)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), executionRequestTimeout)
		defer cancel()

		result, err := apiClient.RetryExecution(ctx, args[0])
		if err != nil {
			return fmt.Errorf("retrying execution: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}
		fmt.Printf("Retry queued: %s (status: %s)\n", result.ID, result.Status)
		return nil
	},
}

var clusterOperationsStepsCmd = &cobra.Command{
	Use:     "steps <execution_id>",
	Aliases: []string{"jobs"},
	Short:   "List steps for a specific execution",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		executionID := args[0]

		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		detail, err := apiClient.GetExecution(executionID)
		if err != nil {
			return fmt.Errorf("fetching execution: %w", err)
		}

		if format != outputDefault {
			return encodeStructured(os.Stdout, format, detail)
		}

		fmt.Printf("Execution: %s  Status: %s\n",
			detail.Execution.DisplayName,
			renderColouredStatus(detail.Execution.Status),
		)
		if detail.Execution.ErrorExcerpt != nil && *detail.Execution.ErrorExcerpt != "" {
			fmt.Printf("Error: %s\n", *detail.Execution.ErrorExcerpt)
		}
		fmt.Println()

		if len(detail.Steps) == 0 {
			fmt.Println("No steps found for this execution.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Status", "Error", "Created At", "Updated At"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 30},
			{Number: 2, WidthMin: 25},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 30, WidthMax: 60},
			{Number: 5, WidthMin: 20},
			{Number: 6, WidthMin: 20},
		})

		for _, step := range detail.Steps {
			errExcerpt := ""
			if step.ErrorExcerpt != nil {
				errExcerpt = truncateString(*step.ErrorExcerpt, 80)
			}
			t.AppendRow(table.Row{
				step.ID,
				step.Name,
				renderColouredStatus(step.Status),
				errExcerpt,
				formatOptionalTime(step.CreatedAt),
				formatOptionalTime(step.UpdatedAt),
			})
		}
		t.Render()
		return nil
	},
}

func renderExecutionsTable(executions []client.ExecutionSummary) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "Name", "Status", "Steps (✓/✗/⟳)", "Error", "Created At", "Updated At"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, WidthMin: 30},
		{Number: 2, WidthMin: 30},
		{Number: 3, WidthMin: 12},
		{Number: 4, WidthMin: 16},
		{Number: 5, WidthMin: 30, WidthMax: 60},
		{Number: 6, WidthMin: 20},
		{Number: 7, WidthMin: 20},
	})

	for _, execution := range executions {
		summary := fmt.Sprintf("%d/%d/%d",
			execution.StepSummary.Succeeded,
			execution.StepSummary.Failed,
			execution.StepSummary.Running,
		)
		errExcerpt := ""
		if execution.ErrorExcerpt != nil {
			errExcerpt = truncateString(*execution.ErrorExcerpt, 80)
		}
		t.AppendRow(table.Row{
			execution.ID,
			execution.DisplayName,
			renderColouredStatus(execution.Status),
			summary,
			errExcerpt,
			formatOptionalTime(execution.CreatedAt),
			formatOptionalTime(execution.UpdatedAt),
		})
	}
	t.Render()
}

func printExecutionDetail(detail client.ExecutionDetail) {
	fmt.Printf("Execution Details:\n")
	fmt.Printf("  ID: %s\n", detail.Execution.ID)
	fmt.Printf("  Name: %s\n", detail.Execution.DisplayName)
	fmt.Printf("  Type: %s\n", detail.Execution.Type)
	fmt.Printf("  Scope: %s\n", detail.Execution.Scope)
	fmt.Printf("  Status: %s\n", renderColouredStatus(detail.Execution.Status))
	if detail.Execution.ErrorExcerpt != nil && *detail.Execution.ErrorExcerpt != "" {
		fmt.Printf("  Error: %s\n", *detail.Execution.ErrorExcerpt)
	}
	fmt.Printf("  Steps: %d total (✓ %d / ✗ %d / ⟳ %d / pending %d)\n",
		detail.StepSummary.Total,
		detail.StepSummary.Succeeded,
		detail.StepSummary.Failed,
		detail.StepSummary.Running,
		detail.StepSummary.Pending,
	)
	fmt.Printf("  Created At: %s\n", formatOptionalTime(detail.Execution.CreatedAt))
	fmt.Printf("  Updated At: %s\n", formatOptionalTime(detail.Execution.UpdatedAt))
}

func renderColouredStatus(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return text.FgGreen.Sprint("✓ " + status)
	case "failed", "critical":
		return text.FgRed.Sprint("✗ " + status)
	case "cancelled", "cancelling", "stopping":
		return text.FgHiBlack.Sprint("⊘ " + status)
	default:
		return text.FgYellow.Sprint("⟳ " + status)
	}
}

func formatOptionalTime(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return formatTimeAgo(*value)
}

func truncateString(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen-1] + "…"
}

func init() {
	clusterOperationsListCmd.Flags().StringSlice("status", nil, "Filter by execution status (repeatable). Examples: failed, critical, running")
	clusterOperationsListCmd.Flags().Bool("failed", false, "Shortcut for --status failed --status critical")
	clusterOperationsListCmd.Flags().Int("limit", defaultExecutionsPageSize, "Maximum number of executions to return (max 100)")
	clusterOperationsListCmd.Flags().BoolP("watch", "w", false,
		"Continuously poll and refresh until all executions reach a terminal state")
	clusterOperationsListCmd.Flags().Duration("interval", defaultWatchInterval,
		"Polling interval used when --watch is set")
	clusterOperationsListCmd.Flags().StringP("output", "o", "",
		"Output format: json or yaml (default: human-readable)")
	clusterOperationsStepsCmd.Flags().StringP("output", "o", "",
		"Output format: json or yaml (default: human-readable)")
	registerStructuredOutputFlags(
		clusterOperationsCancelCmd,
		clusterOperationsCancelStepCmd,
		clusterOperationsRetryCmd,
	)

	clusterOperationsCmd.AddCommand(clusterOperationsListCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelStepCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsStepsCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsRetryCmd)

	clusterCmd.AddCommand(clusterOperationsCmd)
}
