package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var getOperationsCmd = &cobra.Command{
	Use:   "operations [operation ID]",
	Short: "Get operations for the active cluster; optionally, provide an operation ID for details",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster is selected. Run 'ankra select cluster' to select one.")
			return
		}
		ops, err := client.ListClusterOperations(apiToken, baseURL, cluster.ID)
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

func init() {
	getCmd.AddCommand(getOperationsCmd)
}
