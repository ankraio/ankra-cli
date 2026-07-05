package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var clusterKubeadmVersionsCmd = &cobra.Command{
	Use:   "kubeadm-versions",
	Short: "List available kubeadm (vanilla Kubernetes) versions for cluster creation and upgrades",
	Long:  "List the upstream Kubernetes versions the platform can provision or upgrade kubeadm-distribution clusters to. Use one of these values with `--kubernetes-version` on `ankra cluster <provider> create --distribution kubeadm`, or with `ankra cluster upgrade <cluster_id> <target_version>`.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ListKubeadmVersions()
		if err != nil {
			return fmt.Errorf("listing kubeadm versions: %w", err)
		}
		if result.StableVersion != "" {
			fmt.Printf("Stable: %s\n", result.StableVersion)
		}
		if len(result.Versions) == 0 {
			fmt.Println("No kubeadm versions available.")
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
	clusterCmd.AddCommand(clusterKubeadmVersionsCmd)
}
