package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var forceDelete bool
var dryRunDelete bool

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a resource",
	Long:  "Delete a resource (e.g., a cluster) from the Ankra platform.",
}

var deleteClusterCmd = &cobra.Command{
	Use:   "cluster NAME",
	Short: "Delete a cluster by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if dryRunDelete {
			fmt.Printf("Would delete cluster %q; no changes applied (--dry-run).\n", name)
			return nil
		}

		if err := confirmPrompt(os.Stdin, os.Stdout,
			fmt.Sprintf("Are you sure you want to delete cluster %q? This action is irreversible! (y/N): ", name),
			forceDelete); err != nil {
			return err
		}

		// The generic delete must only ever reach a cluster whose kind is
		// known to allow it: a cloud cluster deleted here would lose its
		// record while its servers keep running and billing. So the kind is
		// read first, and when it cannot be read nothing is deleted - a
		// transient lookup failure must never bypass the check.
		clusterInfo, lookupErr := apiClient.GetCluster(name)
		if lookupErr != nil {
			if errors.Is(lookupErr, client.ErrClusterNotFound) {
				return withExitCode(exitNotFound,
					fmt.Errorf("cluster %q does not exist: the name is wrong or it has already been deleted", name))
			}
			// Wrapped, so the exit code is the lookup failure's own class
			// (6 for credentials, 7 for a role, 1 for a platform error).
			return fmt.Errorf("could not read what kind of cluster %q is, so nothing was deleted "+
				"(a cloud cluster must be deprovisioned instead); try again: %w", name, lookupErr)
		}
		if refusal := deleteClusterKindRefusal(name, clusterInfo); refusal != nil {
			return refusal
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := apiClient.DeleteCluster(ctx, name); err != nil {
			errorString := err.Error()
			if strings.Contains(errorString, "status 422") || strings.Contains(errorString, "status 404") {
				return withExitCode(exitNotFound,
					fmt.Errorf("cluster %q does not exist: the name is wrong or it has already been deleted", name))
			}
			if strings.Contains(errorString, "status 409") {
				return fmt.Errorf("delete refused: %s", errorString)
			}
			return fmt.Errorf("could not delete cluster %q: %w", name, err)
		}

		fmt.Printf("Cluster %q deleted successfully.\n", name)
		return nil
	},
}

// managedClusterKinds are the provider-managed Kubernetes kinds, removed with
// `ankra cluster managed delete`; the kind doubles as its --provider value.
var managedClusterKinds = map[string]bool{
	"doks": true, "uks": true, "gke": true, "ovh_mks": true,
	"aks": true, "eks": true, "kapsule": true, "ankracloud_k8s": true,
}

// deleteClusterKindRefusal is the refusal for a cluster `ankra delete
// cluster` must not remove, or nil when its kind allows the generic delete.
// A cluster whose kind the platform did not report is refused too: the
// delete proceeds only on a kind that was actually read.
func deleteClusterKindRefusal(name string, cluster client.ClusterListItem) error {
	kind := cluster.Kind
	switch {
	case kind == "":
		return fmt.Errorf("the platform did not report what kind of cluster %q is, so nothing was deleted "+
			"(a cloud cluster must be deprovisioned instead); try again", name)
	case isCloudClusterKind(cloudClusterKind(kind)):
		// A cloud cluster is removed by deprovisioning it, so naming one
		// here is a usage error (exit 2), like any other command run
		// against a cluster of the wrong kind.
		return withExitCode(exitUsage, fmt.Errorf(
			"%s is a cloud cluster (its kind is %s), which `ankra delete cluster` cannot remove; "+
				"run `ankra cluster deprovision %s` instead so its cloud resources are released",
			name, kind, cluster.ID))
	case managedClusterKinds[kind]:
		return withExitCode(exitUsage, fmt.Errorf(
			"%s is a managed Kubernetes cluster (its kind is %s), which `ankra delete cluster` cannot remove; "+
				"run `ankra cluster managed delete %s --provider %s` instead so its cloud resources are released",
			name, kind, cluster.ID, kind))
	case kind == "playground":
		return withExitCode(exitUsage, fmt.Errorf(
			"%s is a playground, which `ankra delete cluster` cannot remove; "+
				"run `ankra cluster playground destroy %s` instead so its environment is released",
			name, cluster.ID))
	}
	return nil
}

func init() {
	deleteClusterCmd.Flags().
		BoolVarP(&forceDelete, "force", "f", false, "Skip confirmation prompt")
	deleteClusterCmd.Flags().
		BoolVar(&dryRunDelete, "dry-run", false, "Show what would be deleted without calling the API")
	setDryRunOffline(deleteClusterCmd)

	deleteCmd.AddCommand(deleteClusterCmd)
	rootCmd.AddCommand(deleteCmd)
}
