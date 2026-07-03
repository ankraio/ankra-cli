package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var clusterK3sVersionsCmd = &cobra.Command{
	Use:   "k3s-versions",
	Short: "List available k3s (Kubernetes) versions for cluster upgrades",
	Long:  "List the k3s versions the platform can provision or upgrade to. Use one of these values with `ankra cluster upgrade <cluster_id> <target_version>` (the provider is detected automatically).",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ListK3sVersions()
		if err != nil {
			return fmt.Errorf("listing k3s versions: %w", err)
		}
		if result.StableVersion != "" {
			fmt.Printf("Stable: %s\n", result.StableVersion)
		}
		if len(result.Versions) == 0 {
			fmt.Println("No k3s versions available.")
			return nil
		}
		fmt.Println("Available versions:")
		for _, v := range result.Versions {
			fmt.Printf("  %-22s channel=%s\n", v.Version, v.Channel)
		}
		return nil
	},
}

func init() {
	clusterCmd.AddCommand(clusterK3sVersionsCmd)
}
