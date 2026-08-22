package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var clusterPlaygroundCmd = &cobra.Command{
	Use:   "playground",
	Short: "Create, inspect and destroy your organisation's playground",
	Long: "The playground is a real, writable Kubernetes environment Ankra provisions for you - " +
		"a virtual cluster on Ankra's own infrastructure, with the agent already installed. " +
		"Every organisation may hold one; it expires after a period of inactivity.",
}

var clusterPlaygroundCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create your organisation's playground",
	Long: "Create the organisation's playground. Provisioning runs in the background: poll " +
		"`ankra cluster playground status <cluster_id>` until the phase reaches ready.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.CreatePlayground()
		if err != nil {
			return fmt.Errorf("creating the playground: %w", err)
		}
		fmt.Printf("Playground created.\n")
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nProvisioning takes a couple of minutes. Follow it with:\n")
		fmt.Printf("  ankra cluster playground status %s\n", result.ClusterID)
		return nil
	},
}

var clusterPlaygroundStatusCmd = &cobra.Command{
	Use:   "status <cluster_id>",
	Short: "Show the provisioning phase of a playground",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.GetPlaygroundStatus(args[0])
		if err != nil {
			return fmt.Errorf("reading the playground status: %w", err)
		}
		fmt.Printf("Cluster ID: %s\n", status.ClusterID)
		fmt.Printf("Phase:      %s\n", status.Phase)
		fmt.Printf("Expires at: %s\n", status.ExpiresAt)
		if status.StatusMessage != nil && *status.StatusMessage != "" {
			fmt.Printf("Message:    %s\n", *status.StatusMessage)
		}
		return nil
	},
}

var clusterPlaygroundDestroyCmd = &cobra.Command{
	Use:   "destroy <cluster_id>",
	Short: "Destroy your organisation's playground",
	Long: "Tear the organisation's playground down. Teardown runs in the background: poll " +
		"`ankra cluster playground status <cluster_id>` until the phase reaches removed.\n\n" +
		"This is also how a refused `ankra org domain set` is cleared. A playground publishes a " +
		"wildcard DNS record in the organisation's zone, and that record is reconciled rather " +
		"than written once - deleting it with `ankra org dns delete` only lasts until the " +
		"provisioner's next pass. Destroying the environment is what removes it for good.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.DestroyPlayground(args[0])
		if err != nil {
			return fmt.Errorf("destroying the playground: %w", err)
		}
		// cmd.OutOrStdout() rather than fmt.Printf: the destroy verb is the
		// one this group's output is asserted on, and a bare Printf writes
		// past the writer a test sets.
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cluster ID: %s\n", result.ClusterID)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Phase:      %s\n", result.Phase)
		return nil
	},
}

func init() {
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundCreateCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundStatusCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundDestroyCmd)
	clusterCmd.AddCommand(clusterPlaygroundCmd)
}
