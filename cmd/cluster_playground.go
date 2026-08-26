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

var clusterPlaygroundCreateSize string

var clusterPlaygroundCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create your organisation's playground",
	Long: "Create the organisation's playground, optionally at a paid size from " +
		"`ankra cluster playground plans`. Without --size the free trial is created. " +
		"Provisioning runs in the background: poll " +
		"`ankra cluster playground status <cluster_id>` until the phase reaches ready.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.CreatePlayground(clusterPlaygroundCreateSize)
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

var clusterPlaygroundPlansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List the playground sizes you can order",
	Long: "List every playground size with its monthly price and whether the pool can place " +
		"it right now. Order one with `ankra cluster playground create --size <id>`.",
	RunE: func(cmd *cobra.Command, args []string) error {
		catalog, err := apiClient.ListPlaygroundPlans()
		if err != nil {
			return fmt.Errorf("listing the playground plans: %w", err)
		}
		writer := cmd.OutOrStdout()
		for _, plan := range catalog.Plans {
			price := "free"
			if plan.PriceMonthlyCents > 0 {
				price = fmt.Sprintf("%s%.2f/mo", currencySymbol(plan.Currency), float64(plan.PriceMonthlyCents)/100)
			}
			availability := ""
			if !plan.Available {
				availability = "  (at capacity right now)"
			}
			_, _ = fmt.Fprintf(writer, "%-8s  %2d vCPU / %2.0f GB RAM / %3d GB storage  %10s%s\n",
				plan.ID, plan.Vcpus, plan.MemoryGB, plan.StorageGB, price, availability)
		}
		_, _ = fmt.Fprintf(writer, "\nOrder one with: ankra cluster playground create --size <id>\n")
		if !catalog.OrganisationHasPaidPlan {
			_, _ = fmt.Fprintf(writer,
				"Paid sizes need a billing plan - choose one on the billing page first.\n")
		}
		return nil
	},
}

// currencySymbol renders the catalog's currency for terminal output.
func currencySymbol(currency string) string {
	switch currency {
	case "eur":
		return "€"
	case "usd":
		return "$"
	default:
		return currency + " "
	}
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
	clusterPlaygroundCreateCmd.Flags().StringVar(&clusterPlaygroundCreateSize, "size", "",
		"size to order, from `ankra cluster playground plans` (default: the free trial)")
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundCreateCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundPlansCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundStatusCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundDestroyCmd)
	clusterCmd.AddCommand(clusterPlaygroundCmd)
}
