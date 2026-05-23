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
)

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
		cluster, err := loadSelectedCluster()
		if err != nil {
			return errNoClusterSelected{}
		}

		statusFlag, _ := cmd.Flags().GetStringSlice("status")
		failedOnly, _ := cmd.Flags().GetBool("failed")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = defaultExecutionsPageSize
		}

		statusList := statusFlag
		if failedOnly {
			statusList = append(statusList, "failed", "critical")
		}

		response, err := apiClient.ListExecutions(client.ListExecutionsOptions{
			ClusterID:  cluster.ID,
			StatusList: statusList,
			Page:       1,
			PageSize:   limit,
		})
		if err != nil {
			return fmt.Errorf("listing executions: %w", err)
		}
		if len(response.Result) == 0 {
			fmt.Println("No executions found for the active cluster.")
			return nil
		}

		if len(args) > 0 {
			executionID := strings.TrimSpace(args[0])
			return renderExecutionDetail(executionID)
		}

		renderExecutionsTable(response.Result)
		return nil
	},
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
			fmt.Printf("Execution '%s' cancelled (status: %s)\n", result.ExecutionID, result.Status)
			return nil
		}

		result, err := apiClient.BatchCancelExecutions(ctx, args)
		if err != nil {
			return fmt.Errorf("cancelling executions: %w", err)
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

		detail, err := apiClient.GetExecution(executionID)
		if err != nil {
			return fmt.Errorf("fetching execution: %w", err)
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

func renderExecutionDetail(executionID string) error {
	detail, err := apiClient.GetExecution(executionID)
	if err != nil {
		return fmt.Errorf("fetching execution %s: %w", executionID, err)
	}

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
	return nil
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

	clusterOperationsCmd.AddCommand(clusterOperationsListCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelStepCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsStepsCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsRetryCmd)

	clusterCmd.AddCommand(clusterOperationsCmd)
}
