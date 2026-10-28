package cmd

import (
	"github.com/spf13/cobra"
)

var clusterKubeadmVersionsCmd = &cobra.Command{
	Use:   "kubeadm-versions",
	Short: "List available kubeadm (vanilla Kubernetes) versions for cluster creation and upgrades",
	Long:  "List the upstream Kubernetes versions the platform can provision or upgrade kubeadm-distribution clusters to. Use one of these values with `--kubernetes-version` on `ankra cluster <provider> create --distribution kubeadm`, or with `ankra cluster upgrade <cluster_id> <target_version>`.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runListVersions("kubeadm", apiClient.ListKubeadmVersions)
	},
}

func init() {
	clusterCmd.AddCommand(clusterKubeadmVersionsCmd)
}
