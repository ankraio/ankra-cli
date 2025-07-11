package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

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

		if apiToken == "" {
			return errors.New("API token not provided; use --token or set ANKRA_API_TOKEN")
		}

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

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := client.DeleteCluster(ctx, apiToken, baseURL, name); err != nil {
			if strings.Contains(err.Error(), "status 422") || strings.Contains(err.Error(), "status 404") {

				fmt.Printf("Cluster %s does not exist, either %s is wrong or it's already been deleted.\n", name, name)
				return nil
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
