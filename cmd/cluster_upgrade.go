package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type k8sVersionUpgrade func(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error)

// upgradeFunctionForKind maps a cluster's kind (as returned by the backend) to
// the provider-specific Kubernetes version upgrade call. Only the cloud-managed
// kinds support k3s upgrades; imported/ankra/sandbox clusters do not.
func upgradeFunctionForKind(kind string) (k8sVersionUpgrade, bool) {
	switch kind {
	case "hetzner":
		return apiClient.UpgradeHetznerK8sVersion, true
	case "ovh":
		return apiClient.UpgradeOvhK8sVersion, true
	case "upcloud":
		return apiClient.UpgradeUpcloudK8sVersion, true
	default:
		return nil, false
	}
}

var clusterUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <target_version>",
	Short: "Upgrade the Kubernetes version of a cloud cluster",
	Long: `Upgrade the Kubernetes (k3s) version on all nodes in a cloud cluster.

The cloud provider (Hetzner, OVH, or UpCloud) is detected automatically from
the cluster, so you do not need to remember which provider it runs on. List
the available target versions with 'ankra cluster k3s-versions'.

Example:
  ankra cluster upgrade 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 v1.36.1+k3s1`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		targetVersion := args[1]

		cluster, err := apiClient.GetClusterByID(clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error looking up cluster %q: %v\n", clusterID, err)
			os.Exit(1)
		}

		upgrade, supported := upgradeFunctionForKind(cluster.Kind)
		if !supported {
			fmt.Fprintf(os.Stderr,
				"Cluster %q (kind %q) does not support Kubernetes version upgrades. Only Hetzner, OVH, and UpCloud clusters can be upgraded with this command.\n",
				clusterID, cluster.Kind)
			os.Exit(1)
		}

		result, err := upgrade(clusterID, targetVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading Kubernetes version: %v\n", err)
			os.Exit(1)
		}

		previousVersion := "none"
		if result.PreviousVersion != nil {
			previousVersion = *result.PreviousVersion
		}
		fmt.Printf("Kubernetes version upgrade initiated.\n")
		fmt.Printf("  Provider:         %s\n", cluster.Kind)
		fmt.Printf("  Previous version: %s\n", previousVersion)
		fmt.Printf("  New version:      %s\n", text.FgGreen.Sprint(result.NewVersion))
		fmt.Printf("  Nodes affected:   %d\n", result.NodesAffected)
	},
}

func init() {
	clusterCmd.AddCommand(clusterUpgradeCmd)
}
