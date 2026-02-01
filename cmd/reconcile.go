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

// clusterCmd is the parent command for cluster operations
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster operations",
	Long:  `Commands for managing and operating on clusters.`,
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all clusters",
	Run: func(cmd *cobra.Command, args []string) {
		response, err := client.ListClusters(apiToken, baseURL, 0, 0)
		if err != nil {
			fmt.Printf("Error listing clusters: %v\n", err)
			return
		}
		if len(response.Result) == 0 {
			fmt.Println("No clusters found.")
			return
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Kube Version", "Nodes", "Control Planes", "State", "Kind", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 10},
			{Number: 3, WidthMin: 5},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 10},
			{Number: 7, WidthMin: 15},
		})
		for _, cluster := range response.Result {
			state := cluster.State
			if strings.ToLower(state) == "online" {
				state = text.FgGreen.Sprint(state)
			}

			t.AppendRow(table.Row{
				cluster.Name,
				cluster.KubeVersion,
				cluster.Nodes,
				cluster.ControlPlanes,
				state,
				cluster.Kind,
				formatTimeAgo(cluster.CreatedAt),
			})
		}
		t.Render()
	},
}

var clusterGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get details of a specific cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		cluster, err := client.GetCluster(apiToken, baseURL, name)
		if err != nil {
			fmt.Printf("Error fetching cluster details for %s: %v\n", name, err)
			return
		}
		fmt.Printf("Cluster Details:\n")
		fmt.Printf("  ID: %s\n", cluster.ID)
		fmt.Printf("  Name: %s\n", cluster.Name)
		fmt.Printf("  Environment: %s\n", cluster.Environment)
		fmt.Printf("  Kube Version: %s\n", cluster.KubeVersion)
		fmt.Printf("  State: %s\n", cluster.State)
		fmt.Printf("  Status: %v\n", cluster.Status)
	},
}

var clusterReconcileCmd = &cobra.Command{
	Use:   "reconcile [cluster_name]",
	Short: "Trigger cluster reconciliation",
	Long: `Trigger a reconciliation for a cluster to sync desired state with actual state.

If no cluster name is provided, uses the currently selected cluster.
If a cluster name is provided, reconciles that specific cluster.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var clusterID string
		var clusterName string

		if len(args) > 0 {
			// Cluster name provided - look it up
			clusterName = args[0]
			cluster, err := client.GetCluster(apiToken, baseURL, clusterName)
			if err != nil {
				fmt.Printf("Error finding cluster %s: %v\n", clusterName, err)
				return
			}
			clusterID = cluster.ID
		} else {
			// Use selected cluster
			selected, err := loadSelectedCluster()
			if err != nil {
				fmt.Println("No cluster specified and no cluster selected.")
				fmt.Println("Either provide a cluster name or select one first with 'ankra cluster select'")
				return
			}
			clusterID = selected.ID
			clusterName = selected.Name
		}

		fmt.Printf("Triggering reconciliation for cluster: %s\n", clusterName)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := client.TriggerReconcile(ctx, apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Printf("Error triggering reconcile: %v\n", err)
			return
		}

		if result.Success {
			fmt.Println("Reconciliation triggered successfully!")
		} else {
			fmt.Printf("Reconciliation request completed: %s\n", result.Message)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
	},
}


func init() {
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterReconcileCmd)
	rootCmd.AddCommand(clusterCmd)
}
