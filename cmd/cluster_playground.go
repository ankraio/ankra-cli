package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var clusterPlaygroundCmd = &cobra.Command{
	Use:   "playground",
	Short: "Create, inspect and destroy your organisation's playground",
	Long: "The playground is a real, writable Kubernetes cluster Ankra creates for your organisation " +
		"on Ankra Cloud, with the Ankra agent already installed. Every organisation may hold one. " +
		"With a payment method on the organisation's Ankra Cloud account, Ankra Cloud bills the " +
		"playground to that account. Without one, the account gets a free trial while Ankra Cloud " +
		"still offers it one, and Ankra Cloud deletes the trial when it ends unless a payment method " +
		"is added first.",
}

var clusterPlaygroundCreateSize string

var clusterPlaygroundCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create your organisation's playground",
	Long: "Create the organisation's playground on Ankra Cloud, at the size " +
		"`ankra cluster playground plans` lists. Provisioning runs in the background: poll " +
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
	Long: "List the size a new playground is created at; create it with " +
		"`ankra cluster playground create`. The sizes an existing playground can move to are " +
		"Ankra Cloud plans this list does not show: `ankra cluster playground resize` names them " +
		"when it refuses a size.",
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
		_, _ = fmt.Fprintf(writer, "\nCreate it with: ankra cluster playground create\n")
		return nil
	},
}

var clusterPlaygroundResizeSize string

var clusterPlaygroundResizeCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "resize <cluster_id|name>",
	Short: "Move a playground to another Ankra Cloud plan",
	Long: "Move the playground's server to another Ankra Cloud plan in place. Ankra Cloud stops the " +
		"server, moves it to the new plan and starts it again, so nothing on the playground answers " +
		"for a few minutes (Ankra Cloud allows up to 30). Ankra Cloud bills the new plan to your " +
		"Ankra Cloud account from the change. A plan the playground cannot move to is refused with " +
		"the plans it can. A free trial keeps its size: add a payment method to your Ankra Cloud " +
		"account and it becomes an ordinary playground you can resize. Requires permission to " +
		"manage the organisation's billing.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if clusterPlaygroundResizeSize == "" {
			return fmt.Errorf("--size is required: name the Ankra Cloud plan to move to; " +
				"a plan this playground cannot move to is refused with the plans it can")
		}
		clusterID, err := resolveClusterArg(args[0])
		if err != nil {
			return err
		}
		result, err := apiClient.ResizePlayground(clusterID, clusterPlaygroundResizeSize)
		if err != nil {
			return fmt.Errorf("resizing the playground: %w", err)
		}
		writer := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(writer, "Playground resize started: Ankra Cloud restarts the server on the new plan.\n")
		_, _ = fmt.Fprintf(writer, "  Cluster ID: %s\n", result.ClusterID)
		_, _ = fmt.Fprintf(writer, "  Size:       %s (%d vCPU / %.0f GB)\n",
			result.Plan.DisplayName, result.Plan.Vcpus, result.Plan.MemoryGB)
		if result.Plan.PriceMonthlyCents > 0 {
			_, _ = fmt.Fprintf(writer, "  Price:      %s%.2f/mo (Ankra Cloud bills it to your Ankra Cloud account from the change)\n",
				currencySymbol(result.Plan.Currency), float64(result.Plan.PriceMonthlyCents)/100)
		}
		return nil
	},
})

// currencySymbol renders the catalog's currency for terminal output.
func currencySymbol(currency string) string {
	switch currency {
	case "eur":
		return "€"
	case "usd":
		return "$"
	case "gbp":
		return "£"
	default:
		return currency + " "
	}
}

var clusterPlaygroundStatusCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "status <cluster_id|name>",
	Short: "Show the provisioning phase of a playground",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterArg(args[0])
		if err != nil {
			return err
		}
		status, err := apiClient.GetPlaygroundStatus(clusterID)
		if err != nil {
			return fmt.Errorf("reading the playground status: %w", err)
		}
		if handled, renderError := renderStructured(cmd, status); handled || renderError != nil {
			return renderError
		}
		fmt.Printf("Cluster ID: %s\n", status.ClusterID)
		fmt.Printf("Phase:      %s\n", status.Phase)
		if status.Plan != nil {
			memory := status.Plan.MemoryGB
			memoryLabel := fmt.Sprintf("%.0f", memory)
			if memory != float64(int(memory)) {
				memoryLabel = fmt.Sprintf("%.1f", memory)
			}
			fmt.Printf("Size:       %s (%d vCPU / %s GB)\n", status.Plan.DisplayName, status.Plan.Vcpus, memoryLabel)
			if status.Plan.PriceMonthlyCents > 0 {
				fmt.Printf("Price:      %s%.2f/mo\n", currencySymbol(status.Plan.Currency),
					float64(status.Plan.PriceMonthlyCents)/100)
			}
		}
		// A paid order does not idle-expire; showing its expires_at would
		// promise a teardown the platform never performs.
		isPaidOrder := status.Plan != nil && status.Plan.OrderedAt != nil
		if expiry := formatPlaygroundDeadline(status.ExpiresAt, time.Now()); expiry != "" && !isPaidOrder {
			fmt.Printf("Expires at: %s\n", expiry)
		}
		if status.IsTrial && status.TrialExpiresAt != nil {
			if trialEnd := formatPlaygroundDeadline(*status.TrialExpiresAt, time.Now()); trialEnd != "" {
				fmt.Printf("Trial ends: %s\n", trialEnd)
			}
		}
		if status.StatusMessage != nil && *status.StatusMessage != "" {
			fmt.Printf("Message:    %s\n", *status.StatusMessage)
		}
		return nil
	},
})

// formatPlaygroundDeadline renders a playground deadline with the time left
// on it, "" when the API sent none so the caller omits the line instead of
// printing an empty label (an Ankra Cloud playground has no idle expiry).
// An unparseable timestamp is printed as it arrived rather than swallowed.
func formatPlaygroundDeadline(timestamp string, now time.Time) string {
	timestamp = strings.TrimSpace(timestamp)
	if timestamp == "" {
		return ""
	}
	deadline, parseError := time.Parse(time.RFC3339, timestamp)
	if parseError != nil {
		return timestamp
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return timestamp + " (expired)"
	}
	return timestamp + " (in " + formatRemaining(remaining) + ")"
}

// formatRemaining renders a positive duration to the two largest units a
// reader plans around: days and hours, hours and minutes, or minutes.
func formatRemaining(remaining time.Duration) string {
	totalMinutes := int(remaining.Minutes())
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes / 60) % 24
	minutes := totalMinutes % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return "under a minute"
	}
}

var clusterPlaygroundDestroyCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "destroy <cluster_id|name>",
	Short: "Destroy your organisation's playground",
	Long: "Tear the organisation's playground down. Everything deployed in it, including its " +
		"storage, is deleted and cannot be recovered, so the command asks first; pass --yes to " +
		"skip the prompt in scripts. Teardown runs in the background: poll " +
		"`ankra cluster playground status <cluster_id>` until the phase reaches removed.\n\n" +
		"This is also how a refused `ankra org domain set` is cleared. A playground publishes a " +
		"wildcard DNS record in the organisation's zone, and that record is reconciled rather " +
		"than written once - deleting it with `ankra org dns delete` only lasts until the " +
		"provisioner's next pass. Destroying the environment is what removes it for good.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterArg(args[0])
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		// A name resolves to an id before the request; the prompt names both
		// so what the user confirms is the cluster the API is asked to destroy.
		target := clusterTarget(args[0], clusterID)
		// The prompt goes to stderr so `-o json` keeps stdout parseable.
		if err := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Destroy playground %s? Everything deployed in it, including its storage, "+
				"is deleted and cannot be recovered. [y/N]: ", target),
			yes); err != nil {
			return err
		}
		result, err := apiClient.DestroyPlayground(clusterID)
		if err != nil {
			return fmt.Errorf("destroying the playground: %w", err)
		}
		if handled, renderError := renderStructured(cmd, result); handled || renderError != nil {
			return renderError
		}
		// cmd.OutOrStdout() rather than fmt.Printf: the destroy verb is the
		// one this group's output is asserted on, and a bare Printf writes
		// past the writer a test sets.
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cluster ID: %s\n", result.ClusterID)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Phase:      %s\n", result.Phase)
		return nil
	},
})

func init() {
	clusterPlaygroundCreateCmd.Flags().StringVar(&clusterPlaygroundCreateSize, "size", "",
		"size to create at (default: the size 'ankra cluster playground plans' lists)")
	clusterPlaygroundResizeCmd.Flags().StringVar(&clusterPlaygroundResizeSize, "size", "",
		"the Ankra Cloud plan to move to, for example starter-4c-8g")
	clusterPlaygroundDestroyCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clusterPlaygroundStatusCmd, clusterPlaygroundDestroyCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundCreateCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundPlansCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundResizeCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundStatusCmd)
	clusterPlaygroundCmd.AddCommand(clusterPlaygroundDestroyCmd)
	clusterCmd.AddCommand(clusterPlaygroundCmd)
}
