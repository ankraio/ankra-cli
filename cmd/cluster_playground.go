package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var clusterPlaygroundCmd = &cobra.Command{
	Use:   "playground",
	Short: "Create and inspect your organisation's playground",
	Long: "The playground is a real, writable Kubernetes environment Ankra provisions for you - " +
		"a virtual cluster on Ankra's own infrastructure, with the agent already installed. " +
		"Every organisation may hold one; it expires after a period of inactivity.",
}

var clusterPlaygroundCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create your organisation's playground",
	Long: "Create the organisation's playground. Provisioning runs in the background: poll " +
		"`ankra cluster playground status <cluster_id>` until the phase reaches ready.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.CreatePlayground()
		if err != nil {
			return fmt.Errorf("creating the playground: %w", err)
		}
		fmt.Printf("Playground created.\n")
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nProvisioning takes a couple of minutes. Follow it with:\n")
		fmt.Printf("  ankra cluster playground status %s\n", result.ClusterID)
		return nil
	},
}

var clusterPlaygroundStatusCmd = &cobra.Command{
	Use:   "status <cluster_id>",
	Short: "Show the provisioning phase of a playground",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.GetPlaygroundStatus(args[0])
		if err != nil {
			return fmt.Errorf("reading the playground status: %w", err)
		}
		fmt.Printf("Cluster ID: %s\n", status.ClusterID)
		fmt.Printf("Phase:      %s\n", status.Phase)
		fmt.Printf("Expires at: %s\n", status.ExpiresAt)
		if status.StatusMessage != nil && *status.StatusMessage != "" {
			fmt.Printf("Message:    %s\n", *status.StatusMessage)
		}
		return nil
	},
}

func init() {
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundCreateCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundStatusCmd)
	clusterCmd.AddCommand(clusterPlaygroundCmd)
}
