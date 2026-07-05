package cmd

import (
	"fmt"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterK3sVersionsCmd = &cobra.Command{
	Use:   "k3s-versions",
	Short: "List available k3s (Kubernetes) versions for cluster upgrades",
	Long:  "List the k3s versions the platform can provision or upgrade to. Use one of these values with `ankra cluster upgrade <cluster_id> <target_version>` (the provider is detected automatically).",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runListVersions("k3s", apiClient.ListK3sVersions)
	},
}

// runListVersions fetches and renders a distribution's version listing; the
// k3s and kubeadm commands share it so their output cannot drift.
func runListVersions(distro string, fetch func() (*client.ListVersionsResult, error)) error {
	result, err := fetch()
	if err != nil {
		return fmt.Errorf("listing %s versions: %w", distro, err)
	}
	if result.StableVersion != "" {
		fmt.Printf("Stable: %s\n", result.StableVersion)
	}
	if len(result.Versions) == 0 {
		fmt.Printf("No %s versions available.\n", distro)
		return nil
	}
	fmt.Println("Available versions:")
	for _, v := range result.Versions {
		fmt.Printf("  %-22s channel=%s\n", v.Version, v.Channel)
	}
	return nil
}

func init() {
	clusterCmd.AddCommand(clusterK3sVersionsCmd)
}
