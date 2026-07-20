package cmd

import (
	"fmt"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var scalewayCmd = &cobra.Command{
	Use:   "scaleway",
	Short: "Manage Scaleway clusters",
	Long:  "Manage the lifecycle of Scaleway clusters.",
}

var scalewayStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop a Scaleway cluster",
	Long:  "Stop a Scaleway cluster by terminating its compute while preserving its configuration so it can be re-provisioned later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, arguments []string) error {
		clusterID := arguments[0]
		result, stopError := apiClient.StopScalewayCluster(clusterID)
		if stopError != nil {
			return fmt.Errorf("stopping Scaleway cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Scaleway cluster stop initiated."))
		} else {
			fmt.Println("Cluster stop request submitted.")
		}
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		return nil
	},
}

var scalewayStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped Scaleway cluster",
	Long:  "Re-provision a stopped Scaleway cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		clusterID := arguments[0]
		scope, scopeError := command.Flags().GetString("scope")
		if scopeError != nil {
			return fmt.Errorf("reading scope: %w", scopeError)
		}
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, startError := apiClient.StartScalewayCluster(clusterID, scope)
		if startError != nil {
			return fmt.Errorf("starting Scaleway cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("Scaleway cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

func init() {
	scalewayStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")
	scalewayCmd.AddCommand(scalewayStopCmd)
	scalewayCmd.AddCommand(scalewayStartCmd)
	clusterCmd.AddCommand(scalewayCmd)
}
