package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type (
	sshKeysGetFunc    func(clusterID string) (*client.ClusterSSHKeysResult, error)
	sshKeysSetFunc    func(clusterID string, sshKeyCredentialIDs []string) (*client.UpdateClusterSSHKeysResult, error)
	sshKeysResyncFunc func(clusterID string) (*client.ResyncSSHKeysResult, error)
)

// resolveSSHKeysClusterKind looks up the cluster and confirms it is a
// cloud-managed kind that supports cluster SSH key management, exiting with a
// clear message otherwise.
func resolveSSHKeysClusterKind(clusterID string) string {
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil {
		fmt.Fprintf(os.Stderr, "Error looking up cluster %q: %v\n", clusterID, lookupError)
		os.Exit(1)
	}
	switch cluster.Kind {
	case "hetzner", "ovh", "upcloud":
		return cluster.Kind
	default:
		fmt.Fprintf(os.Stderr,
			"Cluster %q (kind %q) does not support SSH key management. Only Hetzner, OVH, and UpCloud clusters can use this command.\n",
			clusterID, cluster.Kind)
		os.Exit(1)
		return ""
	}
}

func sshKeysGetForKind(kind string) sshKeysGetFunc {
	switch kind {
	case "hetzner":
		return apiClient.GetHetznerClusterSSHKeys
	case "ovh":
		return apiClient.GetOvhClusterSSHKeys
	case "upcloud":
		return apiClient.GetUpcloudClusterSSHKeys
	}
	return nil
}

func sshKeysSetForKind(kind string) sshKeysSetFunc {
	switch kind {
	case "hetzner":
		return apiClient.UpdateHetznerClusterSSHKeys
	case "ovh":
		return apiClient.UpdateOvhClusterSSHKeys
	case "upcloud":
		return apiClient.UpdateUpcloudClusterSSHKeys
	}
	return nil
}

func sshKeysResyncForKind(kind string) sshKeysResyncFunc {
	switch kind {
	case "hetzner":
		return apiClient.ResyncHetznerClusterSSHKeys
	case "ovh":
		return apiClient.ResyncOvhClusterSSHKeys
	case "upcloud":
		return apiClient.ResyncUpcloudClusterSSHKeys
	}
	return nil
}

var clusterSSHKeysCmd = &cobra.Command{
	Use:     "ssh-keys",
	Aliases: []string{"ssh-key"},
	Short:   "Manage SSH keys attached to a cloud cluster",
	Long: `Get, set, and re-sync the SSH key credentials authorised to access a cloud
cluster's nodes.

The cloud provider (Hetzner, OVH, or UpCloud) is detected automatically from
the cluster.`,
}

var clusterSSHKeysGetCmd = &cobra.Command{
	Use:   "get <cluster_id>",
	Short: "Show SSH keys attached to a cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		kind := resolveSSHKeysClusterKind(clusterID)

		result, getError := sshKeysGetForKind(kind)(clusterID)
		if getError != nil {
			fmt.Fprintf(os.Stderr, "Error fetching SSH keys: %v\n", getError)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
		}

		if len(result.SSHKeyCredentialIDs) == 0 {
			fmt.Println("Attached SSH keys: none")
		} else {
			fmt.Println("Attached SSH key credential IDs:")
			for _, id := range result.SSHKeyCredentialIDs {
				fmt.Printf("  %s\n", id)
			}
		}

		if len(result.AvailableSSHKeys) > 0 {
			fmt.Println("\nAvailable SSH key credentials:")
			for _, key := range result.AvailableSSHKeys {
				fmt.Printf("  %-38s  %s\n", key.CredentialID, key.Name)
			}
		}
	},
}

var clusterSSHKeysSetCmd = &cobra.Command{
	Use:   "set <cluster_id>",
	Short: "Set the SSH keys attached to a cluster",
	Long: `Replace the SSH key credentials attached to a cluster. Changes take effect on
the next reconciliation and are applied to running nodes.

Pass --clear to remove all user SSH keys (the Ankra-managed key always remains).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		clear, _ := cmd.Flags().GetBool("clear")
		sshKeyCredentialIDs, _ := cmd.Flags().GetStringSlice("ssh-key-credential-ids")

		if clear {
			sshKeyCredentialIDs = []string{}
		} else if len(sshKeyCredentialIDs) == 0 {
			fmt.Fprintln(os.Stderr, "Provide at least one --ssh-key-credential-ids value, or pass --clear to remove all user SSH keys")
			os.Exit(1)
		}

		kind := resolveSSHKeysClusterKind(clusterID)

		result, setError := sshKeysSetForKind(kind)(clusterID, sshKeyCredentialIDs)
		if setError != nil {
			fmt.Fprintf(os.Stderr, "Error updating SSH keys: %v\n", setError)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
		}

		fmt.Println(text.FgGreen.Sprint("SSH keys updated. Changes apply on next reconciliation."))
		if len(result.SSHKeyCredentialIDs) == 0 {
			fmt.Println("Attached SSH keys: none (Ankra-managed key retained)")
			return
		}
		fmt.Println("Attached SSH key credential IDs:")
		for _, id := range result.SSHKeyCredentialIDs {
			fmt.Printf("  %s\n", id)
		}
	},
}

var clusterSSHKeysResyncCmd = &cobra.Command{
	Use:   "resync <cluster_id>",
	Short: "Re-sync a cluster's SSH keys with the cloud provider",
	Long: `Re-sync the cluster's SSH key with the cloud provider. Use this to repair a
stale provider-side SSH key reference (for example when the key was deleted and
re-created in the provider console) that blocks new node creation, and to
re-apply the authorised keys to running nodes.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		kind := resolveSSHKeysClusterKind(clusterID)

		result, resyncError := sshKeysResyncForKind(kind)(clusterID)
		if resyncError != nil {
			fmt.Fprintf(os.Stderr, "Error re-syncing SSH keys: %v\n", resyncError)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
		}

		fmt.Println(text.FgGreen.Sprint("SSH key re-sync triggered. Keys are repaired and re-applied on the next reconciliation."))
		for _, id := range result.ResourceIDs {
			fmt.Printf("  %s\n", id)
		}
	},
}

func init() {
	clusterSSHKeysSetCmd.Flags().StringSlice("ssh-key-credential-ids", nil, "SSH key credential IDs (comma-separated or repeated)")
	clusterSSHKeysSetCmd.Flags().Bool("clear", false, "Remove all user SSH keys (the Ankra-managed key is retained)")

	registerStructuredOutputFlags(
		clusterSSHKeysGetCmd,
		clusterSSHKeysSetCmd,
		clusterSSHKeysResyncCmd,
	)

	clusterSSHKeysCmd.AddCommand(clusterSSHKeysGetCmd)
	clusterSSHKeysCmd.AddCommand(clusterSSHKeysSetCmd)
	clusterSSHKeysCmd.AddCommand(clusterSSHKeysResyncCmd)

	clusterCmd.AddCommand(clusterSSHKeysCmd)
}
