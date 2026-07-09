package cmd

import (
	"fmt"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type k8sVersionUpgrade func(clusterID, targetVersion string, force bool) (*client.UpgradeK8sVersionResult, error)

// upgradeFunctionForKind maps a cluster's kind (as returned by the backend) to
// the provider-specific Kubernetes version upgrade call. Only the cloud-managed
// kinds support Kubernetes upgrades; imported/ankra/sandbox clusters do not.
func upgradeFunctionForKind(kind string) (k8sVersionUpgrade, bool) {
	switch kind {
	case "hetzner":
		return apiClient.UpgradeHetznerK8sVersion, true
	case "ovh":
		return apiClient.UpgradeOvhK8sVersion, true
	case "upcloud":
		return apiClient.UpgradeUpcloudK8sVersion, true
	case "digitalocean":
		return apiClient.UpgradeDigitaloceanK8sVersion, true
	default:
		return nil, false
	}
}

var clusterUpgradeForce bool

var clusterUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <target_version>",
	Short: "Upgrade the Kubernetes version of a cloud cluster",
	Long: `Upgrade the Kubernetes version on all nodes in a cloud cluster.

The cloud provider (Hetzner, OVH, UpCloud, or DigitalOcean) is detected automatically from
the cluster, so you do not need to remember which provider it runs on. Both
k3s and kubeadm clusters are supported; list the available target versions
with 'ankra cluster k3s-versions' or 'ankra cluster kubeadm-versions'.

Nodes upgrade one at a time (control plane first, then workers): each node is
cordoned, drained respecting PodDisruptionBudgets, upgraded, and gated on
being Ready at the target version before the rollout moves on. An etcd
snapshot is taken before the control plane upgrade. A drain blocked by a
PodDisruptionBudget aborts the rollout; pass --force to proceed anyway.
Downgrades and skipping minor versions are not supported.

Examples:
  ankra cluster upgrade 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 v1.36.1+k3s1
  ankra cluster upgrade 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 v1.33.2   # kubeadm`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		targetVersion := args[1]

		cluster, err := apiClient.GetClusterByID(clusterID)
		if err != nil {
			return fmt.Errorf("looking up cluster %q: %w", clusterID, err)
		}

		upgrade, supported := upgradeFunctionForKind(cluster.Kind)
		if !supported {
			return fmt.Errorf(
				"cluster %q (kind %q) does not support Kubernetes version upgrades. Only Hetzner, OVH, and UpCloud clusters can be upgraded with this command",
				clusterID, cluster.Kind)
		}

		result, err := upgrade(clusterID, targetVersion, clusterUpgradeForce)
		if err != nil {
			return fmt.Errorf("upgrading Kubernetes version: %w", err)
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
		if result.OperationID != nil && *result.OperationID != "" {
			fmt.Printf("  Operation:        %s\n", *result.OperationID)
			fmt.Printf("Follow progress with 'ankra cluster operations list %s --cluster %s'.\n",
				*result.OperationID, clusterID)
		}
		return nil
	},
}

func init() {
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeForce, "force", false,
		"proceed even when a node drain is blocked (bypasses PodDisruptionBudget failures)")
	clusterCmd.AddCommand(clusterUpgradeCmd)
}
