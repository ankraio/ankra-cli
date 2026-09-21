package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
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
	Short: "Read cloud cost: the fleet rollup, a cluster's estimate, the savings model and the pricing settings",
	Long: `Read the organisation's cloud cost - the same figures the portal shows under
Cost - for reporting and automation.

Every hour Ankra prices each cluster from its node inventory at the provider
list price and allocates the result to namespaces by their CPU and memory
share, so each team sees the share it drives. The fleet summary rolls every
priced cluster up by provider and lists the costliest clusters; a cluster
read adds the component breakdown, the namespace allocation and the daily
trend. The savings model reads the biggest priced clusters and proposes one
lever per cluster (right-size idle capacity, reduce unallocated run rate, or
an off-hours schedule for non-production) with the monthly saving each is
worth. Pricing settings (display currency, effective discount, network
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

var costSavingsCmd = &cobra.Command{
	Use:   "savings",
	Short: "Savings recommendations per cluster with their monthly saving, plus the clusters the model could not price",
	Long: `Read the organisation's savings model - the same recommendations the portal
shows under Cost.

The model analyses the biggest priced clusters and proposes the levers that
apply to each: right-size idle capacity, reduce run rate no namespace claims,
or an off-hours schedule (weeknights and weekends) for a known non-production
cluster with no enabled power schedule. A cluster can carry several. The total
counts each cluster once, at its best lever, so it is smaller than the sum of
the rows when a cluster has more than one. Clusters the model could not analyse are listed rather than
treated as having nothing to save: unpriced (never had a cost snapshot),
stale (metering stopped over a day ago) and unreadable on this pass. The
waste summary counts the open cloud-waste findings.

Every figure is a list-price estimate in the organisation's display currency.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost savings
  ankra cost savings -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		savings, err := apiClient.GetCloudSavings()
		if err != nil {
			return cloudSavingsReadError(err)
		}
		if rendered, err := renderStructured(cmd, savings); rendered || err != nil {
			return err
		}
		renderCloudSavings(cmd.OutOrStdout(), savings)
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

// costSavingsLever names a recommendation's lever the way the portal does.
func costSavingsLever(recommendation client.CloudSavingsRecommendation) string {
	switch recommendation.Kind {
	case "right_size_idle":
		return "Right-size idle capacity"
	case "reduce_unallocated":
		if recommendation.Evidence.UnallocatedSharePercent != nil {
			return fmt.Sprintf("Reduce unallocated run rate (%d%% unclaimed)", *recommendation.Evidence.UnallocatedSharePercent)
		}
		return "Reduce unallocated run rate"
	case "off_hours_schedule":
		return "Off-hours schedule (weeknights and weekends)"
	default:
		return recommendation.Kind
	}
}

func pluralCount(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func renderCloudSavingsClusters(out io.Writer, heading string, clusters []client.CloudSavingsCluster, withKind bool) {
	if len(clusters) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, heading)
	writer := newCostTable(out)
	if withKind {
		writer.AppendHeader(table.Row{"Cluster", "Kind", "Cluster ID"})
	} else {
		writer.AppendHeader(table.Row{"Cluster", "Cluster ID"})
	}
	for _, cluster := range clusters {
		if withKind {
			writer.AppendRow(table.Row{cluster.ClusterName, cluster.Kind, cluster.ClusterID})
		} else {
			writer.AppendRow(table.Row{cluster.ClusterName, cluster.ClusterID})
		}
	}
	writer.Render()
}

// renderCloudSavingsOffHoursHint names the stop/start schedule each
// off-hours saving was computed for. The model uses one schedule today, but
// the hint groups clusters by their own pair so a second schedule could never
// be printed under the first one's crons.
func renderCloudSavingsOffHoursHint(out io.Writer, recommendations []client.CloudSavingsRecommendation) {
	type cronPair struct{ stop, start string }
	clustersByPair := map[cronPair][]string{}
	pairs := []cronPair{}
	for _, recommendation := range recommendations {
		if recommendation.Kind != "off_hours_schedule" || recommendation.Evidence.StopCron == nil || recommendation.Evidence.StartCron == nil {
			continue
		}
		pair := cronPair{stop: *recommendation.Evidence.StopCron, start: *recommendation.Evidence.StartCron}
		if _, seen := clustersByPair[pair]; !seen {
			pairs = append(pairs, pair)
		}
		clustersByPair[pair] = append(clustersByPair[pair], recommendation.ClusterName)
	}
	for _, pair := range pairs {
		_, _ = fmt.Fprintf(out, "Off-hours saving for %s is computed for stop %q / start %q; create them with ankra cluster power-schedules create --action stop|start --cron.\n",
			strings.Join(clustersByPair[pair], ", "), pair.stop, pair.start)
	}
}

func renderCloudSavingsWaste(out io.Writer, waste client.CloudSavingsWaste, currency string) {
	_, _ = fmt.Fprintln(out)
	switch {
	case !waste.Available:
		_, _ = fmt.Fprintln(out, "Waste: the scan could not be read (this is not the same as no waste).")
	case !waste.HasData:
		_, _ = fmt.Fprintln(out, "Waste: no scan yet.")
	case waste.FindingCount == 0:
		_, _ = fmt.Fprintln(out, "Waste: no open findings.")
	default:
		line := fmt.Sprintf("Waste: %s open, %s/mo", pluralCount(waste.FindingCount, "finding"),
			formatCostCents(waste.TotalMonthlyCostCents, currency))
		if waste.UnpricedFindingCount > 0 {
			line += fmt.Sprintf(" (%d unpriced, not in that figure)", waste.UnpricedFindingCount)
		}
		if waste.ScannedAt != nil && *waste.ScannedAt != "" {
			line += " · scanned " + *waste.ScannedAt
		}
		_, _ = fmt.Fprintln(out, line)
	}
}

func renderCloudSavings(out io.Writer, savings *client.CloudSavings) {
	currency := savings.Currency
	if savings.PricedClusterCount == 0 && len(savings.Recommendations) == 0 {
		// A stale cluster was priced before its metering stalled, so "yet" would
		// misname it; the two absences read differently.
		if len(savings.StaleClusters) > 0 {
			_, _ = fmt.Fprintf(out, "No cluster is priced right now (%s with stalled metering), so there is nothing to recommend.\n",
				pluralClusters(len(savings.StaleClusters)))
		} else {
			_, _ = fmt.Fprintln(out, "No priced clusters yet, so there is nothing to recommend.")
		}
		_, _ = fmt.Fprintln(out, "Estimates appear once a cluster on AWS, Google Cloud, Azure, Hetzner, OVHcloud, UpCloud or Scaleway has reported pricing in the last day; AWS, Google Cloud and Azure clusters need a connected cloud credential.")
		renderCloudSavingsClusters(out, "Unpriced clusters (no cost snapshot yet):", savings.UnpricedClusters, true)
		renderCloudSavingsClusters(out, "Stale clusters (metering stopped over a day ago):", savings.StaleClusters, true)
		renderCloudSavingsWaste(out, savings.Waste, currency)
		return
	}
	_, _ = fmt.Fprintf(out, "Cloud savings (%s): %s/mo across %s\n", strings.ToUpper(currency),
		formatCostCents(savings.TotalMonthlySavingsCents, currency),
		pluralCount(len(savings.Recommendations), "recommendation"))
	_, _ = fmt.Fprintf(out, "  %d of %s analysed", savings.AnalysedClusterCount, pluralClusters(savings.PricedClusterCount))
	if savings.UnanalysedClusterCount > 0 {
		_, _ = fmt.Fprintf(out, " (%d not analysed: only the %d biggest are)", savings.UnanalysedClusterCount,
			savings.Thresholds.AnalysedClusterLimit)
	}
	_, _ = fmt.Fprintf(out, " · %d unpriced · %d stale", savings.UnpricedClusterCount, savings.StaleClusterCount)
	if len(savings.UnreadableClusters) > 0 {
		_, _ = fmt.Fprintf(out, " · %d unreadable", len(savings.UnreadableClusters))
	}
	if savings.GeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · generated %s", savings.GeneratedAt)
	}
	_, _ = fmt.Fprintln(out)

	if len(savings.Recommendations) == 0 {
		_, _ = fmt.Fprintf(out, "\nNo recommendation clears the %s/mo minimum on the analysed clusters.\n",
			formatCostCents(savings.Thresholds.MinimumSavingsCents, currency))
	} else {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Recommendations (a cluster can carry several; the total counts each cluster once, at its best lever):")
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"#", "Cluster", "Environment", "Lever", "Savings/mo", "Share", "Run rate/mo", "Cluster ID"})
		for index, recommendation := range savings.Recommendations {
			environment := "-"
			if recommendation.Environment != nil && *recommendation.Environment != "" {
				environment = *recommendation.Environment
			}
			writer.AppendRow(table.Row{
				index + 1,
				recommendation.ClusterName,
				environment,
				costSavingsLever(recommendation),
				formatCostCents(recommendation.MonthlySavingsCents, currency),
				fmt.Sprintf("%d%%", recommendation.SharePercent),
				formatCostCents(recommendation.MonthlyCostCents, currency),
				recommendation.ClusterID,
			})
		}
		writer.Render()
		renderCloudSavingsOffHoursHint(out, savings.Recommendations)
	}

	renderCloudSavingsClusters(out, "Unpriced clusters (no cost snapshot yet):", savings.UnpricedClusters, true)
	renderCloudSavingsClusters(out, "Stale clusters (metering stopped over a day ago):", savings.StaleClusters, true)
	renderCloudSavingsClusters(out, "Unreadable clusters (breakdown could not be read on this pass):", savings.UnreadableClusters, false)
	renderCloudSavingsWaste(out, savings.Waste, currency)
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
	registerStructuredOutputFlags(costSummaryCmd, costSavingsCmd, costClusterCmd, costSettingsGetCmd, costSettingsSetCmd)
	costSettingsCmd.AddCommand(costSettingsGetCmd)
	costSettingsCmd.AddCommand(costSettingsSetCmd)
	costCmd.AddCommand(costSummaryCmd)
	costCmd.AddCommand(costSavingsCmd)
	costCmd.AddCommand(costClusterCmd)
	costCmd.AddCommand(costSettingsCmd)
	rootCmd.AddCommand(costCmd)
}

// cloudSavingsReadError maps the one 404 this route can answer with: a
// platform that predates the savings model serves no /api/v1/org/cloud-cost/
// savings at all, and "request failed: status 404" reads as an auth or
// token problem. The status alone decides, for the reason
// aiRemediationPolicyReadError gives.
func cloudSavingsReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, errors.New(
			"this platform does not serve the savings model to API tokens: "+
				"GET /api/v1/org/cloud-cost/savings is not registered, so this platform predates it. "+
				"The recommendations are readable in the portal under Cost"))
	}
	return fmt.Errorf("reading cloud savings: %w", readError)
}
