package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var forceDelete bool

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

		if !forceDelete {
			fmt.Printf("Are you sure you want to delete cluster %q? This action is irreversible! (y/N): ", name)
			resp, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			resp = strings.TrimSpace(strings.ToLower(resp))
			if resp != "y" && resp != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		clusterInfo, lookupErr := apiClient.GetCluster(name)
		if lookupErr == nil {
			switch clusterInfo.Kind {
			case "hetzner", "ovh", "upcloud":
				fmt.Printf(
					"Cluster %q is a %s cloud cluster and cannot be deleted with this command.\n"+
						"Run 'ankra cluster %s deprovision %s' instead so the cloud resources are released.\n",
					name, clusterInfo.Kind, clusterInfo.Kind, clusterInfo.ID,
				)
				return fmt.Errorf("refusing to delete cloud cluster %q via generic delete", name)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := apiClient.DeleteCluster(ctx, name); err != nil {
			errorString := err.Error()
			if strings.Contains(errorString, "status 422") || strings.Contains(errorString, "status 404") {

				fmt.Printf("Cluster %s does not exist, either %s is wrong or it's already been deleted.\n", name, name)
				return nil
			}
			if strings.Contains(errorString, "status 409") {
				return fmt.Errorf("delete refused: %s", errorString)
			}
			return fmt.Errorf("could not delete cluster %q", name)
		}

		fmt.Printf("Cluster %q deleted successfully.\n", name)
		return nil
	},
}

func init() {
	deleteClusterCmd.Flags().
		BoolVarP(&forceDelete, "force", "f", false, "Skip confirmation prompt")

	deleteCmd.AddCommand(deleteClusterCmd)
	rootCmd.AddCommand(deleteCmd)
}
