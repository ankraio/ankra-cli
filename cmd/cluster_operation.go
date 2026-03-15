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

var clusterOperationsCmd = &cobra.Command{
	Use:   "operations",
	Short: "Manage operations",
	Long:  "Commands to list and cancel operations.",
}

var clusterOperationsListCmd = &cobra.Command{
	Use:   "list [operation ID]",
	Short: "List operations for the active cluster; optionally, provide an operation ID for details",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster is selected. Run 'ankra cluster select' to select one.")
			return
		}
		ops, err := apiClient.ListClusterOperations(cluster.ID)
		if err != nil {
			fmt.Printf("Error listing operations: %v\n", err)
			return
		}
		if len(ops) == 0 {
			fmt.Println("No operations found for the active cluster.")
			return
		}

		if len(args) > 0 {
			opID := strings.TrimSpace(args[0])
			var foundOp *client.OperationResponseListItem
			for _, op := range ops {
				if op.ID == opID {
					foundOp = &op
					break
				}
			}
			if foundOp == nil {
				fmt.Printf("Operation with ID %s not found in the active cluster.\n", opID)
				return
			}
			fmt.Printf("Operation Details:\n")
			fmt.Printf("  ID: %s\n", foundOp.ID)
			fmt.Printf("  Name: %s\n", foundOp.Name)
			status := foundOp.Status
			switch strings.ToLower(status) {
			case "success":
				status = "✓ " + status
			case "failed":
				status = "✗ " + status
			}
			fmt.Printf("  Status: %s\n", status)
			fmt.Printf("  Created At: %s\n", formatTimeAgo(foundOp.CreatedAt))
			fmt.Printf("  Updated At: %s\n", formatTimeAgo(foundOp.UpdatedAt))
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Status", "Created At", "Updated At"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 30},
			{Number: 2, WidthMin: 30},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 20},
			{Number: 5, WidthMin: 20},
		})
		for _, op := range ops {
			status := op.Status
			switch strings.ToLower(status) {
			case "success":
				status = text.FgGreen.Sprint("✓ " + status)
			case "failed":
				status = text.FgRed.Sprint("✗ " + status)
			default:
				status = text.FgYellow.Sprint("⟳ " + status)
			}
			t.AppendRow(table.Row{
				op.ID,
				op.Name,
				status,
				formatTimeAgo(op.CreatedAt),
				formatTimeAgo(op.UpdatedAt),
			})
		}
		t.Render()
	},
}

var clusterOperationsCancelCmd = &cobra.Command{
	Use:   "cancel <operation_id>",
	Short: "Cancel a running operation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		operationID := args[0]

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := apiClient.CancelOperation(ctx, operationID)
		if err != nil {
			fmt.Printf("Error cancelling operation: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Operation '%s' cancelled successfully!\n", operationID)
		}
	},
}

var clusterOperationsCancelJobCmd = &cobra.Command{
	Use:   "cancel-job <operation_id> <job_id>",
	Short: "Cancel a specific job within an operation",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		operationID := args[0]
		jobID := args[1]

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := apiClient.CancelJob(ctx, operationID, jobID)
		if err != nil {
			fmt.Printf("Error cancelling job: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Job '%s' cancelled successfully!\n", jobID)
		}
	},
}

var clusterOperationsJobsCmd = &cobra.Command{
	Use:   "jobs <operation_id>",
	Short: "List jobs for a specific operation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		operationID := args[0]
		jobKind, _ := cmd.Flags().GetString("kind")
		since, _ := cmd.Flags().GetString("since")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster is selected. Run 'ankra cluster select' to select one.")
			return
		}

		var opts *client.ListOperationJobsOptions
		if jobKind != "" || since != "" {
			opts = &client.ListOperationJobsOptions{
				JobKind:          jobKind,
				FromUTCTimestamp: since,
			}
		}

		response, err := apiClient.ListOperationJobs(cluster.ID, operationID, opts)
		if err != nil {
			fmt.Printf("Error listing jobs: %v\n", err)
			return
		}

		if len(response.Jobs) == 0 {
			fmt.Println("No jobs found for this operation.")
			return
		}

		if response.OperationInformation != nil {
			opStatus := response.OperationInformation.Status
			switch strings.ToLower(opStatus) {
			case "success":
				opStatus = text.FgGreen.Sprint("✓ " + opStatus)
			case "failed":
				opStatus = text.FgRed.Sprint("✗ " + opStatus)
			default:
				opStatus = text.FgYellow.Sprint("⟳ " + opStatus)
			}
			fmt.Printf("Operation: %s  Status: %s\n\n", response.OperationInformation.Name, opStatus)
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Status", "Created At", "Updated At"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 30},
			{Number: 2, WidthMin: 25},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 20},
			{Number: 5, WidthMin: 20},
		})

		for _, job := range response.Jobs {
			jobStatus := job.Status
			switch strings.ToLower(jobStatus) {
			case "success":
				jobStatus = text.FgGreen.Sprint("✓ " + jobStatus)
			case "failed":
				jobStatus = text.FgRed.Sprint("✗ " + jobStatus)
			default:
				jobStatus = text.FgYellow.Sprint("⟳ " + jobStatus)
			}
			t.AppendRow(table.Row{
				job.ID,
				job.Name,
				jobStatus,
				formatTimeAgo(job.CreatedAt),
				formatTimeAgo(job.UpdatedAt),
			})
		}
		t.Render()
	},
}

func init() {
	clusterOperationsJobsCmd.Flags().String("kind", "", "Filter by job kind")
	clusterOperationsJobsCmd.Flags().String("since", "", "Show jobs updated since this UTC timestamp")

	clusterOperationsCmd.AddCommand(clusterOperationsListCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsCancelJobCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsJobsCmd)

	clusterCmd.AddCommand(clusterOperationsCmd)
}
