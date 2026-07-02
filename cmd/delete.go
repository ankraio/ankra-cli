package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
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

		clusterInfo, lookupErr := apiClient.GetCluster(name)
		if lookupErr == nil {
			switch clusterInfo.Kind {
			case "hetzner", "ovh", "upcloud":
				fmt.Printf(
					"Cluster %q is a %s cloud cluster and cannot be deleted with this command.\n"+
						"Run 'ankra cluster deprovision %s' instead so the cloud resources are released.\n",
					name, clusterInfo.Kind, clusterInfo.ID,
				)
				return fmt.Errorf("refusing to delete cloud cluster %q via generic delete", name)
			}
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

func init() {
	deleteClusterCmd.Flags().
		BoolVarP(&forceDelete, "force", "f", false, "Skip confirmation prompt")
	deleteClusterCmd.Flags().
		BoolVar(&dryRunDelete, "dry-run", false, "Show what would be deleted without calling the API")
	setDryRunOffline(deleteClusterCmd)

	deleteCmd.AddCommand(deleteClusterCmd)
	rootCmd.AddCommand(deleteCmd)
}
