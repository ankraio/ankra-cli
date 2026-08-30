package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// costHoursPerMonth is the basis the platform prices hourly components on
// (365 x 24 / 12), so a monthly figure derived here matches the portal.
const costHoursPerMonth = 730

const costTopNamespaceRows = 15

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Read cloud cost: the fleet rollup, a cluster's estimate and the pricing settings",
	Long: `Read the organisation's cloud cost - the same figures the portal shows under
Cost - for reporting and automation.

Every hour Ankra prices each cluster from its node inventory at the provider
list price and allocates the result to namespaces by their CPU and memory
share, so each team sees the share it drives. The fleet summary rolls every
priced cluster up by provider and lists the costliest clusters; a cluster
read adds the component breakdown, the namespace allocation and the daily
trend. Pricing settings (display currency, effective discount, network
egress estimate) apply to every figure.

Pass -o json (or yaml) for the full API document.`,
}

var costSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Fleet cost rollup: projected month end, month to date, run rate, by provider and costliest clusters",
	Args:  cobra.NoArgs,
	Example: `  ankra cost summary
  ankra cost summary -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		summary, err := apiClient.GetFleetCloudCost()
		if err != nil {
			return fmt.Errorf("reading fleet cost: %w", err)
		}
		if rendered, err := renderStructured(cmd, summary); rendered || err != nil {
			return err
		}
		renderFleetCloudCost(cmd.OutOrStdout(), summary)
		return nil
	},
}

var costClusterCmd = &cobra.Command{
	Use:   "cluster <name-or-id>",
	Short: "A cluster's cost estimate: totals, component breakdown, namespace allocation and readiness",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost cluster prod-eu
  ankra cost cluster 1834920e-3001-4157-8938-33c447031033 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}
		cost, err := apiClient.GetClusterCost(clusterID)
		if err != nil {
			return fmt.Errorf("reading cluster cost: %w", err)
		}
		if rendered, err := renderStructured(cmd, cost); rendered || err != nil {
			return err
		}
		renderClusterCost(cmd.OutOrStdout(), args[0], cost)
		return nil
	},
}

var costSettingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Pricing settings: display currency, effective discount and the network egress estimate",
}

var costSettingsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's pricing settings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := apiClient.GetCostSettings()
		if err != nil {
			return fmt.Errorf("reading cost settings: %w", err)
		}
		if rendered, err := renderStructured(cmd, settings); rendered || err != nil {
			return err
		}
		renderCostSettings(cmd.OutOrStdout(), settings)
		return nil
	},
}

var costSettingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the pricing settings (organisation admins only)",
	Long: `Change one or more pricing settings. Only the flags you pass change; the
other settings keep their current values. The route is organisation-admin
only and every fleet and cluster figure re-prices on the next read.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost settings set --currency eur
  ankra cost settings set --discount 12.5
  ankra cost settings set --include-egress=false`,
	RunE: func(cmd *cobra.Command, args []string) error {
		currencyChanged := cmd.Flags().Changed("currency")
		discountChanged := cmd.Flags().Changed("discount")
		egressChanged := cmd.Flags().Changed("include-egress")
		if !currencyChanged && !discountChanged && !egressChanged {
			return withExitCode(exitUsage, errors.New("pass at least one of --currency, --discount or --include-egress"))
		}
		current, err := apiClient.GetCostSettings()
		if err != nil {
			return fmt.Errorf("reading cost settings: %w", err)
		}
		update := *current
		if currencyChanged {
			currency, _ := cmd.Flags().GetString("currency")
			update.Currency = strings.ToLower(strings.TrimSpace(currency))
		}
		if discountChanged {
			discount, _ := cmd.Flags().GetFloat64("discount")
			if discount < 0 || discount > 100 {
				return withExitCode(exitUsage, errors.New("--discount must be between 0 and 100 (percent)"))
			}
			update.EffectiveDiscountPct = discount
		}
		if egressChanged {
			includeEgress, _ := cmd.Flags().GetBool("include-egress")
			update.IncludeNetworkEgressEstimate = includeEgress
		}
		saved, err := apiClient.UpdateCostSettings(update)
		if err != nil {
			return fmt.Errorf("updating cost settings: %w", err)
		}
		if rendered, err := renderStructured(cmd, saved); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Cost settings updated.")
		renderCostSettings(out, saved)
		return nil
	},
}

// formatCostCents renders integer cents in the display currency.
func formatCostCents(cents int64, currency string) string {
	return fmt.Sprintf("%s%.2f", currencySymbol(currency), float64(cents)/100)
}

func costProviderLabel(provider string) string {
	switch provider {
	case "aws":
		return "Amazon Web Services"
	case "gcp":
		return "Google Cloud"
	case "azure":
		return "Microsoft Azure"
	case "hetzner":
		return "Hetzner Cloud"
	case "ovh":
		return "OVHcloud"
	case "upcloud":
		return "UpCloud"
	case "scaleway":
		return "Scaleway"
	case "":
		return "unknown provider"
	default:
		return provider
	}
}

func newCostTable(out io.Writer) table.Writer {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	return writer
}

func pluralClusters(count int) string {
	if count == 1 {
		return "1 cluster"
	}
	return fmt.Sprintf("%d clusters", count)
}

func renderFleetCloudCost(out io.Writer, summary *client.FleetCloudCost) {
	if summary.ClusterCount == 0 {
		_, _ = fmt.Fprintln(out, "No priced clusters yet.")
		_, _ = fmt.Fprintln(out, "Estimates appear once a cluster on AWS, Google Cloud, Azure, Hetzner, OVHcloud, UpCloud or Scaleway has reported pricing in the last day; AWS, Google Cloud and Azure clusters need a connected cloud credential.")
		return
	}
	currency := summary.Currency
	_, _ = fmt.Fprintf(out, "Cloud cost (%s): %s projected month end · %s month to date · %s/mo run rate\n",
		strings.ToUpper(currency), formatCostCents(summary.ProjectedMonthEndCents, currency),
		formatCostCents(summary.MonthToDateCents, currency), formatCostCents(summary.MonthlyCostEstimateCents, currency))
	_, _ = fmt.Fprintf(out, "  %s priced across %d provider(s)\n", pluralClusters(summary.ClusterCount), len(summary.ByProvider))
	if len(summary.ByProvider) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "By provider:")
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"Provider", "Clusters", "Run rate/mo", "Month to date", "Projected"})
		for _, provider := range summary.ByProvider {
			writer.AppendRow(table.Row{
				costProviderLabel(provider.Provider),
				provider.ClusterCount,
				formatCostCents(provider.MonthlyCostEstimateCents, currency),
				formatCostCents(provider.MonthToDateCents, currency),
				formatCostCents(provider.ProjectedMonthEndCents, currency),
			})
		}
		writer.Render()
	}
	if len(summary.TopClusters) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Costliest clusters:")
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"#", "Cluster", "Provider", "Projected", "Month to date", "Confidence", "Cluster ID"})
		for index, cluster := range summary.TopClusters {
			writer.AppendRow(table.Row{
				index + 1,
				cluster.ClusterName,
				costProviderLabel(cluster.Provider),
				formatCostCents(cluster.ProjectedMonthEndCents, currency),
				formatCostCents(cluster.MonthToDateCents, currency),
				cluster.ConfidenceLevel,
				cluster.ClusterID,
			})
		}
		writer.Render()
	}
}

// costReadinessExplanation mirrors the portal's readiness copy so a missing
// estimate reads as why it is missing, never as a zero.
func costReadinessExplanation(readiness *client.CostReadiness) string {
	if readiness == nil {
		return "no estimate yet."
	}
	provider := "this provider"
	if readiness.Provider != nil && *readiness.Provider != "" {
		provider = costProviderLabel(*readiness.Provider)
	}
	switch readiness.State {
	case "ready":
		return "the estimate is ready; the next snapshot will carry figures."
	case "no_credential":
		return fmt.Sprintf("no cloud credential for %s is connected. Add one under Credentials to price this cluster.", provider)
	case "unsupported_provider":
		return fmt.Sprintf("cost is not estimated for %s yet.", provider)
	case "cluster_offline":
		return "the cluster is offline, so its node inventory cannot sync."
	case "awaiting_nodes":
		return "node inventory is still syncing."
	case "awaiting_pricing":
		return fmt.Sprintf("pricing for %s is still being collected.", provider)
	case "estimate_pending":
		return "everything is in place; the first estimate lands with the next hourly snapshot."
	default:
		return fmt.Sprintf("readiness is %q.", readiness.State)
	}
}

func renderClusterCost(out io.Writer, clusterReference string, cost *client.ClusterCost) {
	if !cost.HasData || cost.Summary == nil {
		_, _ = fmt.Fprintf(out, "No cost estimate for %s: %s\n", clusterReference, costReadinessExplanation(cost.Readiness))
		return
	}
	summary := cost.Summary
	currency := summary.Currency
	_, _ = fmt.Fprintf(out, "Cost for %s (%s, %s): %s projected month end · %s month to date · %s/mo run rate · %s/hr\n",
		clusterReference, costProviderLabel(summary.Provider), strings.ToUpper(currency),
		formatCostCents(summary.ProjectedMonthEndCents, currency), formatCostCents(summary.MonthToDateCents, currency),
		formatCostCents(summary.MonthlyCostEstimateCents, currency), formatCostCents(summary.HourlyCostCents, currency))
	coverage := fmt.Sprintf("%d of %d nodes priced", summary.PricedNodeCount, summary.TotalNodeCount)
	if summary.CoverageIncomplete {
		coverage += " (coverage incomplete - the estimate understates the true figure)"
	}
	_, _ = fmt.Fprintf(out, "  confidence %s · %s · %d volumes (%d unpriced)", summary.ConfidenceLevel, coverage,
		summary.StorageVolumeCount, summary.UnpricedVolumeCount)
	if summary.AppliedDiscountPct > 0 {
		_, _ = fmt.Fprintf(out, " · %.1f%% discount applied", summary.AppliedDiscountPct)
	}
	if summary.SnapshotAt != "" {
		_, _ = fmt.Fprintf(out, " · snapshot %s", summary.SnapshotAt)
	}
	_, _ = fmt.Fprintln(out)

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Run rate by component (per month):")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Component", "Per month"})
	monthly := func(hourlyCents int64) string {
		return formatCostCents(hourlyCents*costHoursPerMonth, currency)
	}
	writer.AppendRow(table.Row{"Compute (on-demand)", monthly(summary.ComputeOnDemandCents)})
	if summary.ComputeSpotCents > 0 {
		writer.AppendRow(table.Row{"Compute (spot)", monthly(summary.ComputeSpotCents)})
	}
	writer.AppendRow(table.Row{"Storage", monthly(summary.StorageCents)})
	writer.AppendRow(table.Row{"Network", monthly(summary.NetworkCents)})
	writer.AppendRow(table.Row{"Control plane", monthly(summary.ControlPlaneCents)})
	if summary.InfrastructureCents > 0 {
		writer.AppendRow(table.Row{"Bastion & VMs", monthly(summary.InfrastructureCents)})
	}
	writer.AppendRow(table.Row{"of which idle", monthly(summary.IdleHourlyCents)})
	writer.AppendRow(table.Row{"Unallocated", monthly(summary.UnallocatedHourlyCents)})
	writer.Render()

	if len(cost.Namespaces) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Namespace allocation (top %d of %d):\n", min(len(cost.Namespaces), costTopNamespaceRows), len(cost.Namespaces))
		namespaceWriter := newCostTable(out)
		namespaceWriter.AppendHeader(table.Row{"Namespace", "Per month", "CPU share", "Memory share", "Source"})
		for index, namespace := range cost.Namespaces {
			if index >= costTopNamespaceRows {
				break
			}
			namespaceWriter.AppendRow(table.Row{
				namespace.Namespace,
				formatCostCents(namespace.AllocatedMonthlyCents, currency),
				fmt.Sprintf("%.1f%%", namespace.CPUShare*100),
				fmt.Sprintf("%.1f%%", namespace.MemoryShare*100),
				namespace.AllocationSource,
			})
		}
		namespaceWriter.Render()
	}
	if len(cost.Trend) > 0 {
		first := cost.Trend[0]
		last := cost.Trend[len(cost.Trend)-1]
		_, _ = fmt.Fprintf(out, "\nTrend: %d daily points, %s/mo on %s -> %s/mo on %s\n", len(cost.Trend),
			formatCostCents(first.MonthlyCostEstimateCents, currency), first.Day,
			formatCostCents(last.MonthlyCostEstimateCents, currency), last.Day)
	}
}

func renderCostSettings(out io.Writer, settings *client.CostSettings) {
	egress := "off"
	if settings.IncludeNetworkEgressEstimate {
		egress = "on"
	}
	_, _ = fmt.Fprintf(out, "Display currency: %s\n", strings.ToUpper(settings.Currency))
	_, _ = fmt.Fprintf(out, "Effective discount: %g%%\n", settings.EffectiveDiscountPct)
	_, _ = fmt.Fprintf(out, "Network egress estimate: %s\n", egress)
}

func init() {
	costSettingsSetCmd.Flags().String("currency", "", "Display currency: usd, eur or gbp")
	costSettingsSetCmd.Flags().Float64("discount", 0, "Effective discount in percent (0-100), applied on top of list prices; 10 means 10%")
	costSettingsSetCmd.Flags().Bool("include-egress", false, "Include an estimated network egress charge (pass --include-egress=false to drop it)")
	registerStructuredOutputFlags(costSummaryCmd, costClusterCmd, costSettingsGetCmd, costSettingsSetCmd)
	costSettingsCmd.AddCommand(costSettingsGetCmd)
	costSettingsCmd.AddCommand(costSettingsSetCmd)
	costCmd.AddCommand(costSummaryCmd)
	costCmd.AddCommand(costClusterCmd)
	costCmd.AddCommand(costSettingsCmd)
	rootCmd.AddCommand(costCmd)
}
