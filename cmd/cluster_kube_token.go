package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type execCredentialStatus struct {
	Token               string `json:"token"`
	ExpirationTimestamp string `json:"expirationTimestamp,omitempty"`
}

type execCredential struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Status     execCredentialStatus `json:"status"`
}

var clusterKubeTokenCmd = &cobra.Command{
	Use:   "kube-token",
	Short: "Print a Kubernetes ExecCredential for use as a kubeconfig credential plugin",
	Long: `Print a short-lived Kubernetes ExecCredential so kubectl can authenticate to the
Ankra cluster gateway.

This command is intended to be invoked by kubectl as a client-go credential plugin,
for example in a kubeconfig:

  users:
  - name: ankra
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1
        command: ankra
        args: ["cluster", "kube-token", "--cluster", "<cluster-name-or-id>", "--org", "<organisation-id>"]

Pinning --org to the cluster's organisation ID keeps the entry working when
your selected organisation differs from the cluster's ('ankra cluster
kubeconfig add' writes it automatically).

It prints JSON to stdout and never prompts; run 'ankra login' first.`,
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		clusterID, err := resolveKubeTokenClusterID(clusterFlag)
		if err != nil {
			return err
		}

		kubeToken, err := apiClient.GetClusterKubeToken(context.Background(), clusterID)
		if err != nil {
			return err
		}

		credential := execCredential{
			APIVersion: "client.authentication.k8s.io/v1",
			Kind:       "ExecCredential",
			Status: execCredentialStatus{
				Token:               kubeToken.Token,
				ExpirationTimestamp: normalizeExpirationTimestamp(kubeToken.ExpiresAt),
			},
		}
		output, err := json.Marshal(credential)
		if err != nil {
			return err
		}
		fmt.Println(string(output))
		return nil
	},
}

func resolveKubeTokenClusterID(clusterFlag string) (string, error) {
	if clusterFlag != "" {
		cluster, err := apiClient.GetCluster(clusterFlag)
		if err == nil {
			return cluster.ID, nil
		}
		if isLikelyClusterID(clusterFlag) {
			return clusterFlag, nil
		}
		return "", fmt.Errorf("cluster %q not found; pass a cluster name or ID (not the kubeconfig context name): %w", clusterFlag, err)
	}
	cluster, err := loadSelectedCluster()
	if err != nil {
		return "", fmt.Errorf("no cluster specified and no active cluster selected; pass --cluster <name|id>")
	}
	return cluster.ID, nil
}

func normalizeExpirationTimestamp(expiresAt string) string {
	if expiresAt == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}
	return parsed.UTC().Format(time.RFC3339)
}

func init() {
	clusterKubeTokenCmd.Flags().String("cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterCmd.AddCommand(clusterKubeTokenCmd)
}
