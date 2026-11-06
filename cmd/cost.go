package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"ankra/internal/client"
	"ankra/internal/hiddenunicode"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// costHoursPerMonth is the basis the platform prices hourly components on
// (365 x 24 / 12), so a monthly figure derived here matches the portal.
const costHoursPerMonth = 730

const costTopNamespaceRows = 15

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Read cloud cost: the fleet rollup, a cluster's estimate, the savings model, the measured ledger and the pricing settings",
	Long: `Read the organisation's cloud cost - the same figures the portal shows under
Cost - for reporting and automation.

Every hour Ankra prices each cluster from its node inventory at the provider
list price and allocates the result to namespaces by their CPU and memory
share, so each team sees the share it drives. The fleet summary rolls every
priced cluster up by provider and lists the costliest clusters; its month to
date also counts what clusters spent this month before they were deleted or
stopped reporting, and names them, since no provider row includes it; a cluster
read adds the component breakdown, the namespace allocation and the daily
trend. The savings model reads the biggest priced clusters and proposes one
lever per cluster (right-size idle capacity, reduce unallocated run rate, or
an off-hours schedule for non-production) with the monthly saving each is
worth. The ledger follows each approved cost change through to the saving
measured seven days after it ran. The display currency and the effective
discount apply to every figure. The network egress estimate setting is stored,
not metered yet: no figure includes network egress today.

The fleet reads (summary, savings, ledger, trend, events, reconcile, budgets,
and the clusters 'autopilot get' lists) cover the project you act in (see
'ankra project current'); pass --all-projects to read the whole
organisation, or the global --project to read another project. Pass -o json
(or yaml) for the full API document.`,
}

// costAllProjectsFlag widens a fleet cost read to the whole organisation.
const costAllProjectsFlag = "all-projects"

// registerCostProjectScopeFlag adds --all-projects to the fleet cost reads
// that take ?project_id=.
func registerCostProjectScopeFlag(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().Bool(costAllProjectsFlag, false, "Read the whole organisation instead of the project you act in")
	}
}

// applyCostProjectScope sets the fleet cost reads' ?project_id= for this
// command: all with --all-projects, otherwise none, so a scope set by an
// earlier command on the same client never carries over. A token pinned to
// a project is refused by the platform (403) for all: the pin is the
// credential's confinement.
func applyCostProjectScope(cmd *cobra.Command) {
	scope := ""
	if allProjects, _ := cmd.Flags().GetBool(costAllProjectsFlag); allProjects {
		scope = client.CostProjectScopeAll
	}
	apiClient.SetCostProjectScope(scope)
}

var costSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Fleet cost rollup: projected month end, month to date, run rate, by provider, by project and costliest clusters",
	Args:  cobra.NoArgs,
	Example: `  ankra cost summary
  ankra cost summary --all-projects
  ankra cost summary --by credential
  ankra cost summary --by project --all-projects
  ankra cost summary -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		breakdown, _ := cmd.Flags().GetString("by")
		breakdown = strings.ToLower(strings.TrimSpace(breakdown))
		if breakdown != "" && !containsString(fleetCostBreakdowns, breakdown) {
			return withExitCode(exitUsage, fmt.Errorf("--by must be one of %s, not %q", strings.Join(fleetCostBreakdowns, ", "), breakdown))
		}
		applyCostProjectScope(cmd)
		summary, err := apiClient.GetFleetCloudCost()
		if err != nil {
			return fmt.Errorf("reading fleet cost: %w", err)
		}
		if rendered, err := renderStructured(cmd, summary); rendered || err != nil {
			return err
		}
		if breakdown == "" {
			renderFleetCloudCost(cmd.OutOrStdout(), summary)
			return nil
		}
		renderFleetCloudCostBreakdown(cmd.OutOrStdout(), summary, breakdown)
		return nil
	},
}

// The groupings `cost summary --by` renders on their own.
const (
	fleetCostByProvider   = "provider"
	fleetCostByCredential = "credential"
	fleetCostByProject    = "project"
)

var fleetCostBreakdowns = []string{fleetCostByProvider, fleetCostByCredential, fleetCostByProject}

var costSavingsCmd = &cobra.Command{
	Use:   "savings",
	Short: "Savings recommendations per cluster with their monthly saving, plus the clusters the model could not price",
	Long: `Read the organisation's savings model - the same recommendations the portal
shows under Cost.

The model analyses the biggest priced clusters (up to the organisation's
analysed-cluster limit, see 'ankra cost settings', and within the platform's
analysis time budget) and proposes the levers that apply to each: right-size idle capacity, reduce run rate no namespace claims,
or an off-hours schedule (weeknights and weekends) for a known non-production
cluster with no enabled power schedule. A cluster can carry several. The total
counts each cluster once, at its best lever, so it is smaller than the sum of
the rows when a cluster has more than one. Each row's Basis says what its
saving rests on: measured (observed usage planned a right-size step), billed
(an off-hours stop on billed compute) or estimate (capacity less requests,
with no usage measured). The total counts only measured and billed levers;
what the estimates add is shown beside it and never counted. Clusters the
model could not analyse are listed rather than treated as having nothing to
save: unpriced (never had a cost snapshot), stale (metering stopped over a
day ago), unreadable on this pass, and offline (the agent has not checked
in, so nothing is recommended for them). The waste summary counts the open
cloud-waste findings.

Every figure is a list-price estimate in the organisation's display currency.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost savings
  ankra cost savings --all-projects
  ankra cost savings -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyCostProjectScope(cmd)
		savings, err := apiClient.GetCloudSavings()
		if err != nil {
			return cloudSavingsReadError(err)
		}
		if rendered, err := renderStructured(cmd, savings); rendered || err != nil {
			return err
		}
		renderCloudSavings(cmd.OutOrStdout(), savings, readCloudSavingsScanFailures(cmd.ErrOrStderr(), savings.Waste))
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
	Short: "Pricing settings: display currency, effective discount, the network egress estimate and the analysed-cluster limit",
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
only and every fleet and cluster figure re-prices on the next read.

--include-egress is stored, not metered yet: no figure includes network egress
today, whichever way it is set.

--analysed-cluster-limit sets how many of the costliest priced clusters the
savings model analyses (1 to 50); --analysed-cluster-limit default returns it
to the platform default of 8. Without the flag the limit is not sent, so it
keeps its stored value.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost settings set --currency eur
  ankra cost settings set --discount 12.5
  ankra cost settings set --include-egress=false
  ankra cost settings set --analysed-cluster-limit 20
  ankra cost settings set --analysed-cluster-limit default`,
	RunE: func(cmd *cobra.Command, args []string) error {
		currencyChanged := cmd.Flags().Changed("currency")
		discountChanged := cmd.Flags().Changed("discount")
		egressChanged := cmd.Flags().Changed("include-egress")
		limitChanged := cmd.Flags().Changed("analysed-cluster-limit")
		if !currencyChanged && !discountChanged && !egressChanged && !limitChanged {
			return withExitCode(exitUsage, errors.New("pass at least one of --currency, --discount, --include-egress or --analysed-cluster-limit"))
		}
		var limitChange *client.AnalysedClusterLimitChange
		if limitChanged {
			raw, _ := cmd.Flags().GetString("analysed-cluster-limit")
			change, parseError := parseAnalysedClusterLimit(raw)
			if parseError != nil {
				return parseError
			}
			limitChange = change
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
		// The limit read back is the one in effect; restating it would turn
		// the default into the organisation's own. Only the flag changes it.
		update.AnalysedClusterLimit = nil
		update.AnalysedClusterLimitChange = limitChange
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

// formatCostCents renders integer cents in the display currency. The sign
// goes before the symbol ("-$42.00", not "$-42.00"): a measured saving can be
// negative when the run rate rose, and it must read as a negative amount.
// Integer arithmetic keeps large figures exact.
func formatCostCents(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, currencySymbol(currency), cents/100, cents%100)
}

// formatOptionalCostCents is formatCostCents for a figure the platform may
// not know. Absent is not zero: an unknown renders as "—", never "0.00".
func formatOptionalCostCents(cents *int64, currency string) string {
	if cents == nil {
		return "—"
	}
	return formatCostCents(*cents, currency)
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
	case "ankracloud", "ankracloud_k8s":
		return "Ankra Cloud"
	case "digitalocean":
		return "DigitalOcean"
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

// costCount is a count with its thousands grouped: 1,440.
func costCount(count int) string {
	digits := strconv.Itoa(count)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return sign + digits
}

// costCountOf is a grouped count with the word that agrees with it.
func costCountOf(count int, singular string, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return costCount(count) + " " + plural
}

// costMonthFloorMark marks a table figure built on a month so far that is
// short of metered hours: it is at least what it says.
const costMonthFloorMark = "≥ "

// costMonthHours words the hours a month-to-date figure holds
// (ankra-bn86k4.4.8.2): "Month so far covers 718 of 720 hours; 2 hours were
// not metered, so the figure is a floor." Short reports that hours are
// missing, so the month to date and the month end built on it are at least
// what they say. It says nothing when the platform did not count the hours
// (nil: a platform that predates them, or a sum with a cluster it could not
// count), which is not a whole month, and nothing when every hour was metered
// by a reading of its own, the ordinary case. The counts decide, never the
// platform's is_short or note, so the line cannot name "0 hours" missing.
func costMonthHours(hours *client.MonthHours) (line string, short bool) {
	if hours == nil || hours.HoursElapsed <= 0 {
		return "", false
	}
	metered := min(max(hours.HoursMetered, 0), hours.HoursElapsed)
	missing := hours.HoursElapsed - metered
	short = missing > 0
	estimated := min(max(hours.HoursEstimated, 0), metered)
	if !short && estimated == 0 {
		return "", false
	}
	// Summed over several clusters the platform counts cluster hours: two
	// clusters for a day are 48 of them.
	unit := "hours"
	if hours.HoursElapsed == 1 {
		unit = "hour"
	}
	if hours.ClusterCount > 1 {
		unit = "cluster " + unit
	}
	if short {
		across := ""
		if hours.ClusterCount > 1 && hours.ShortClusterCount > 0 {
			across = " across " + costCountOf(hours.ShortClusterCount, "cluster", "clusters")
		}
		line = fmt.Sprintf("Month so far covers %s of %s %s; %s not metered%s, so the figure is a floor.",
			costCount(metered), costCount(hours.HoursElapsed), unit, costCountOf(missing, "hour was", "hours were"), across)
	} else {
		line = fmt.Sprintf("Month so far covers all %s %s.", costCount(hours.HoursElapsed), unit)
	}
	switch {
	case estimated > 0 && hours.HoursMeasured != nil:
		// How the covered hours divide once the platform counts the measured
		// ones (cluster#5909, ankra-bn86k4.4.8.4.2).
		measured := min(max(*hours.HoursMeasured, 0), metered)
		estimatedWord := "hours"
		if estimated == 1 {
			estimatedWord = "hour"
		}
		line += fmt.Sprintf(" Of the covered hours, %s measured and %s estimated: no metering ran for the estimated %s, so the next reading was copied.",
			costCountOf(measured, "was", "were"), costCount(estimated), estimatedWord)
	case estimated > 0:
		// A platform that predates hours_measured: the measured hours are
		// unknown, so only the estimated ones are named, never "the rest
		// were measured".
		estimates := "are estimates"
		if estimated == 1 {
			estimates = "is an estimate"
		}
		line += fmt.Sprintf(" %s of the covered hours %s: no metering ran then, so the next reading was copied.",
			costCount(estimated), estimates)
	}
	return line, short
}

// costMonthAtLeast is "at least " before a figure built on a month so far
// that is short of metered hours, and "" otherwise.
func costMonthAtLeast(short bool) string {
	if short {
		return "at least "
	}
	return ""
}

func renderFleetCloudCost(out io.Writer, summary *client.FleetCloudCost) {
	if !renderFleetCloudCostHeader(out, summary) {
		return
	}
	currency := summary.Currency
	renderFleetCostByProvider(out, summary, currency)
	renderFleetCostByProject(out, summary, currency)
	if len(summary.TopClusters) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Costliest clusters:")
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"#", "Cluster", "Provider", "Projected", "Month to date", "Confidence", "Cluster ID"})
		anyShort := false
		for index, cluster := range summary.TopClusters {
			mark := ""
			if _, short := costMonthHours(cluster.MonthHours); short {
				mark, anyShort = costMonthFloorMark, true
			}
			writer.AppendRow(table.Row{
				index + 1,
				cluster.ClusterName,
				costProviderLabel(cluster.Provider),
				mark + formatCostCents(cluster.ProjectedMonthEndCents, currency),
				mark + formatCostCents(cluster.MonthToDateCents, currency),
				cluster.ConfidenceLevel,
				cluster.ClusterID,
			})
		}
		writer.Render()
		if anyShort {
			_, _ = fmt.Fprintf(out, "%smarks a cluster whose month so far is short of metered hours, so its figures are floors.\n",
				costMonthFloorMark)
		}
	}
	renderFleetCostUnmetered(out, summary)
}

// renderFleetCloudCostHeader prints the totals, when they were computed
// (whenever the platform sent generated_at, metered clusters or not), the
// hours of the month the month to date holds when some were not metered,
// the spend of clusters no longer metered and the scope line, and reports
// false (after saying so) when nothing is priced yet. Nothing is priced only
// when no cluster is metered and nothing in scope spent anything this month:
// a project whose only cluster was deleted still spent what it spent.
func renderFleetCloudCostHeader(out io.Writer, summary *client.FleetCloudCost) bool {
	if summary.ClusterCount == 0 && summary.MonthToDateCents == 0 && len(summary.UnmeteredClusters) == 0 {
		_, _ = fmt.Fprintln(out, "No priced clusters yet.")
		_, _ = fmt.Fprintln(out, "Estimates appear once a cluster on AWS, Google Cloud, Azure, Hetzner, OVHcloud, UpCloud or Scaleway has reported pricing in the last day; AWS, Google Cloud and Azure clusters need a connected cloud credential.")
		if line := fleetCostUnpricedLine(summary, false); line != "" {
			_, _ = fmt.Fprintln(out, line)
		}
		if line := fleetCostNotReportedLine(summary, false); line != "" {
			_, _ = fmt.Fprintln(out, line)
		}
		return false
	}
	currency := summary.Currency
	monthHours, monthShort := costMonthHours(summary.MonthHours)
	atLeast := costMonthAtLeast(monthShort)
	_, _ = fmt.Fprintf(out, "Cloud cost (%s): %s%s projected month end · %s%s month to date · %s/mo run rate\n",
		strings.ToUpper(currency), atLeast, formatCostCents(summary.ProjectedMonthEndCents, currency),
		atLeast, formatCostCents(summary.MonthToDateCents, currency), formatCostCents(summary.MonthlyCostEstimateCents, currency))
	generatedAt := ""
	if summary.GeneratedAt != nil {
		generatedAt = *summary.GeneratedAt
	}
	if summary.ClusterCount > 0 {
		asOf := ""
		if generatedAt != "" {
			asOf = " · as of " + generatedAt
		}
		_, _ = fmt.Fprintf(out, "  %s priced across %d provider(s)%s\n", pluralClusters(summary.ClusterCount), len(summary.ByProvider), asOf)
	} else {
		_, _ = fmt.Fprintln(out, "  No cluster is metered now, so the run rate is zero; the month to date is what clusters no longer metered spent this month.")
		if generatedAt != "" {
			_, _ = fmt.Fprintf(out, "  Figures as of %s.\n", generatedAt)
		}
	}
	if monthHours != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", monthHours)
	}
	for _, line := range fleetCostUnmeteredLines(summary) {
		_, _ = fmt.Fprintf(out, "  %s\n", line)
	}
	if line := fleetCostUnpricedLine(summary, true); line != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", line)
	}
	if line := fleetCostNotReportedLine(summary, true); line != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", line)
	}
	if scopeLine := fleetCostScopeLine(summary.Scope); scopeLine != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", scopeLine)
	}
	return true
}

// fleetCostUnpricedLine says how many clusters in scope have no price, and,
// when figures are shown above it, what that means for them: "2 clusters in
// scope have no price from the last day, so what they cost is unknown, not
// zero: the run rate and the projected month end leave them out and are
// floors." A count of 0 gets no line: the line is about clusters that are
// there, never a claim that none are. A platform that did not send the count
// (nil) gets no line here either; fleetCostNotReportedLine says that instead.
func fleetCostUnpricedLine(summary *client.FleetCloudCost, figuresShown bool) string {
	if summary.UnpricedClusterCount == nil || *summary.UnpricedClusterCount <= 0 {
		return ""
	}
	count := *summary.UnpricedClusterCount
	subject, costClause, pronoun, floorClause := pluralClusters(count)+" in scope have", "they cost", "them", ""
	if count == 1 {
		subject, costClause, pronoun = "1 cluster in scope has", "it costs", "it"
	}
	if figuresShown {
		floorClause = ": the run rate and the projected month end leave " + pronoun + " out and are floors"
	}
	return fmt.Sprintf("%s no price from the last day, so what %s is unknown, not zero%s.", subject, costClause, floorClause)
}

// fleetCostNotReportedLine says which readings that qualify the totals the
// platform did not send (ankra-bn86k4.8.25): the count of clusters in scope
// it could not price, and the hours the month so far holds. Saying nothing
// would read as "every cluster priced, every hour metered", which a platform
// that predates them cannot claim. Only a missing reading counts: a count of
// 0 says every cluster is priced, and month hours that were sent speak for
// themselves through costMonthHours. With no figures shown (nothing priced
// yet) only the clusters half applies.
func fleetCostNotReportedLine(summary *client.FleetCloudCost, figuresShown bool) string {
	unpricedUnknown := summary.UnpricedClusterCount == nil
	hoursUnknown := figuresShown && summary.MonthHours == nil
	switch {
	case unpricedUnknown && hoursUnknown:
		return "The platform did not say how many clusters in scope it could not price, or whether every hour of the month so far " +
			"was metered, so these figures may be short of what was spent."
	case unpricedUnknown && figuresShown:
		return "The platform did not say how many clusters in scope it could not price, so these figures may leave some out."
	case unpricedUnknown:
		return "The platform did not say how many clusters in scope it could not price."
	case hoursUnknown:
		return "The platform did not say whether every hour of the month so far was metered, so the month to date may be short of what was spent."
	}
	return ""
}

// The reasons a cluster's spend is counted but no longer metered.
const (
	fleetUnmeteredDeleted          = "deleted"
	fleetUnmeteredStoppedReporting = "stopped reporting"
	fleetUnmeteredMovedOut         = "moved to another project"
)

// fleetUnmeteredReason is why a cluster's month is in the totals with no
// fresh snapshot: deleted this month, moved to another project after
// spending under the one read, or - any other live cluster - stopped
// reporting.
func fleetUnmeteredReason(cluster client.FleetUnmeteredCluster) string {
	switch {
	case cluster.IsDeleted:
		return fleetUnmeteredDeleted
	case cluster.IsMovedOut:
		return fleetUnmeteredMovedOut
	default:
		return fleetUnmeteredStoppedReporting
	}
}

// fleetCostUnmeteredLines name the spend the month to date carries from
// clusters no longer metered, which no provider row includes: "€336.16 of
// the month to date is from 27 clusters that were deleted or stopped
// reporting this month (19 deleted, 8 stopped reporting) ...". A live
// cluster that stopped reporting may still be running, so its cost since it
// was last metered is in no figure and the month end is a floor. A platform
// that predates the field (nil) gets no line: it counted no such spend.
func fleetCostUnmeteredLines(summary *client.FleetCloudCost) []string {
	if len(summary.UnmeteredClusters) == 0 {
		return nil
	}
	reasons := []string{fleetUnmeteredDeleted, fleetUnmeteredStoppedReporting, fleetUnmeteredMovedOut}
	spentByReason := map[string]int{}
	spendingCount := 0
	stoppedCount := 0
	var spentCents int64
	for _, cluster := range summary.UnmeteredClusters {
		reason := fleetUnmeteredReason(cluster)
		if reason == fleetUnmeteredStoppedReporting {
			stoppedCount++
		}
		if cluster.MonthToDateCents > 0 {
			spendingCount++
			spentByReason[reason]++
			spentCents += cluster.MonthToDateCents
		}
	}
	if summary.UnmeteredMonthToDateCents != nil {
		spentCents = *summary.UnmeteredMonthToDateCents
	}
	lines := []string{}
	if spendingCount > 0 && spentCents > 0 {
		verbs, parts := []string{}, []string{}
		for _, reason := range reasons {
			count := spentByReason[reason]
			if count == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%d %s", count, reason))
			switch {
			case reason == fleetUnmeteredDeleted && spendingCount == 1:
				verbs = append(verbs, "was deleted")
			case reason == fleetUnmeteredDeleted:
				verbs = append(verbs, "were deleted")
			default:
				verbs = append(verbs, reason)
			}
		}
		line := fmt.Sprintf("%s of the month to date is from %s that %s this month", formatCostCents(spentCents, summary.Currency),
			pluralClusters(spendingCount), joinAlternatives(verbs))
		if len(parts) > 1 {
			line += " (" + strings.Join(parts, ", ") + ")"
		}
		lines = append(lines, line+"; no provider row includes it.")
	}
	if stoppedCount > 0 {
		pronoun := "they were"
		if stoppedCount == 1 {
			pronoun = "it was"
		}
		lines = append(lines, fmt.Sprintf("What the %s that stopped reporting cost since %s last metered is not in these figures, so the projected month end is a floor.",
			pluralClusters(stoppedCount), pronoun))
	}
	return lines
}

// joinAlternatives joins phrases as "a", "a or b", "a, b or c".
func joinAlternatives(phrases []string) string {
	if len(phrases) <= 1 {
		return strings.Join(phrases, "")
	}
	return strings.Join(phrases[:len(phrases)-1], ", ") + " or " + phrases[len(phrases)-1]
}

// fleetCostUnmeteredRows is how many unmetered clusters the summary lists;
// -o json carries every one.
const fleetCostUnmeteredRows = 10

// renderFleetCostUnmetered lists the clusters whose spend the month to date
// carries without a fresh snapshot, costliest first, with why and when each
// was last metered. It ranks them itself before the row cap: the platform
// answers costliest first today, but an older platform makes no such
// promise, and a cap over an unranked list could hide the biggest spend
// behind "… and N more".
func renderFleetCostUnmetered(out io.Writer, summary *client.FleetCloudCost) {
	if len(summary.UnmeteredClusters) == 0 {
		return
	}
	currency := summary.Currency
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Deleted or stopped reporting this month (in the month to date, in no provider row):")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Why", "Provider", "Month to date", "Last metered", "Cluster ID"})
	listed := append([]client.FleetUnmeteredCluster{}, summary.UnmeteredClusters...)
	sort.SliceStable(listed, func(first int, second int) bool {
		return listed[first].MonthToDateCents > listed[second].MonthToDateCents
	})
	if len(listed) > fleetCostUnmeteredRows {
		listed = listed[:fleetCostUnmeteredRows]
	}
	for _, cluster := range listed {
		provider := "—"
		if cluster.Provider != nil {
			provider = costProviderLabel(*cluster.Provider)
		}
		lastMetered := "—"
		if cluster.LastSnapshotAt != nil && *cluster.LastSnapshotAt != "" {
			lastMetered = *cluster.LastSnapshotAt
		}
		writer.AppendRow(table.Row{
			cluster.ClusterName,
			fleetUnmeteredReason(cluster),
			provider,
			formatCostCents(cluster.MonthToDateCents, currency),
			lastMetered,
			cluster.ClusterID,
		})
	}
	writer.Render()
	if hidden := len(summary.UnmeteredClusters) - len(listed); hidden > 0 {
		_, _ = fmt.Fprintf(out, "… and %d more (-o json lists every one)\n", hidden)
	}
}

// renderFleetCloudCostBreakdown is `cost summary --by`: the totals and the
// one grouping asked for. A grouping the platform did not serve says so
// rather than printing an empty table. The clusters no longer metered are
// listed after any grouping: the header's month to date carries their spend
// and its gap line counts them, so the view that shows the money also shows
// whose it is.
func renderFleetCloudCostBreakdown(out io.Writer, summary *client.FleetCloudCost, breakdown string) {
	if !renderFleetCloudCostHeader(out, summary) {
		return
	}
	currency := summary.Currency
	rendered := false
	switch breakdown {
	case fleetCostByProvider:
		rendered = renderFleetCostByProvider(out, summary, currency)
	case fleetCostByCredential:
		rendered = renderFleetCostByCredential(out, summary, currency)
	case fleetCostByProject:
		rendered = renderFleetCostByProject(out, summary, currency)
	}
	if !rendered {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "No by-%s breakdown in this rollup.\n", breakdown)
	}
	renderFleetCostUnmetered(out, summary)
}

func renderFleetCostByProvider(out io.Writer, summary *client.FleetCloudCost, currency string) bool {
	if len(summary.ByProvider) == 0 {
		return false
	}
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
	return true
}

func renderFleetCostByCredential(out io.Writer, summary *client.FleetCloudCost, currency string) bool {
	if len(summary.ByCredential) == 0 {
		return false
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "By credential:")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Credential", "Provider", "Clusters", "Run rate/mo", "Month to date", "Projected"})
	for _, credential := range summary.ByCredential {
		writer.AppendRow(table.Row{
			fleetCostCredentialLabel(credential),
			costProviderLabel(credential.Provider),
			credential.ClusterCount,
			formatCostCents(credential.MonthlyCostEstimateCents, currency),
			formatCostCents(credential.MonthToDateCents, currency),
			formatCostCents(credential.ProjectedMonthEndCents, currency),
		})
	}
	writer.Render()
	return true
}

func renderFleetCostByProject(out io.Writer, summary *client.FleetCloudCost, currency string) bool {
	if len(summary.ByProject) == 0 {
		return false
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "By project:")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Project", "Clusters", "Run rate/mo", "Month to date", "Projected"})
	for _, project := range summary.ByProject {
		writer.AppendRow(table.Row{
			fleetCostProjectLabel(project),
			project.ClusterCount,
			formatCostCents(project.MonthlyCostEstimateCents, currency),
			formatCostCents(project.MonthToDateCents, currency),
			formatCostCents(project.ProjectedMonthEndCents, currency),
		})
	}
	writer.Render()
	return true
}

// fleetCostCredentialLabel names a by-credential row; clusters no
// credential provisioned are one unnamed bucket.
func fleetCostCredentialLabel(credential client.FleetCredentialCost) string {
	if credential.CredentialName != nil && *credential.CredentialName != "" {
		return *credential.CredentialName
	}
	if credential.CredentialID != nil && *credential.CredentialID != "" {
		return *credential.CredentialID
	}
	return "(no credential)"
}

// fleetCostScopeLine says which project the rollup covered, or "" for a
// platform that reported no scope. A partial scope is said in words: every
// figure above it is a floor. It is said even when the scope names no
// project, because a cut-short rollup without it reads as the whole one.
func fleetCostScopeLine(scope *client.FleetCostScope) string {
	label := costScopeLabel(scope)
	if label == "" {
		if scope == nil || !scope.Partial {
			return ""
		}
		return "Scope: partial, only the clusters you may read are counted, so every figure is a floor"
	}
	line := "Scope: " + label
	if scope.Partial {
		line += " (partial: only the clusters you may read are counted, so every figure is a floor)"
	}
	return line
}

// costScopeLabel names what a cost read covered ("project payments", "every
// project of the organisation"), or "" when the platform named nothing.
func costScopeLabel(scope *client.FleetCostScope) string {
	if scope == nil {
		return ""
	}
	switch {
	case scope.AllProjects:
		return "every project of the organisation"
	case scope.ProjectName != nil && *scope.ProjectName != "":
		return "project " + *scope.ProjectName
	case scope.ProjectID != nil && *scope.ProjectID != "":
		return "project " + *scope.ProjectID
	default:
		return ""
	}
}

// fleetCostProjectLabel names a by-project row; clusters outside any
// project are one unnamed bucket.
func fleetCostProjectLabel(project client.FleetProjectCost) string {
	if project.ProjectName != nil && *project.ProjectName != "" {
		return *project.ProjectName
	}
	if project.ProjectID != nil && *project.ProjectID != "" {
		return *project.ProjectID
	}
	return "(no project)"
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

// costComponents is one cluster's run rate by component, per month, in cents.
type costComponents struct {
	computeOnDemand, computeSpot, storage, network, controlPlane, infrastructure, idle, unallocated int64
}

// costComponentMonths reads the platform's monthly components when it sends
// all eight (ankra-bn86k4.4.23): they come from the full-precision hourly
// figures, so a part under a cent an hour (a stopped cluster's volumes) keeps
// its month, and the six parts add up to the monthly run rate. Otherwise every
// component is its whole-cent hourly figure times 730, exactly as before. The
// two are never mixed in one table.
func costComponentMonths(summary *client.ClusterCostSummary) costComponents {
	monthly := []*int64{
		summary.ComputeOnDemandMonthlyCents, summary.ComputeSpotMonthlyCents, summary.StorageMonthlyCents,
		summary.NetworkMonthlyCents, summary.ControlPlaneMonthlyCents, summary.InfrastructureMonthlyCents,
		summary.IdleMonthlyCents, summary.UnallocatedMonthlyCents,
	}
	sent := true
	for _, part := range monthly {
		sent = sent && part != nil
	}
	if sent {
		return costComponents{
			computeOnDemand: *summary.ComputeOnDemandMonthlyCents, computeSpot: *summary.ComputeSpotMonthlyCents,
			storage: *summary.StorageMonthlyCents, network: *summary.NetworkMonthlyCents,
			controlPlane: *summary.ControlPlaneMonthlyCents, infrastructure: *summary.InfrastructureMonthlyCents,
			idle: *summary.IdleMonthlyCents, unallocated: *summary.UnallocatedMonthlyCents,
		}
	}
	return costComponents{
		computeOnDemand: summary.ComputeOnDemandCents * costHoursPerMonth,
		computeSpot:     summary.ComputeSpotCents * costHoursPerMonth,
		storage:         summary.StorageCents * costHoursPerMonth,
		network:         summary.NetworkCents * costHoursPerMonth,
		controlPlane:    summary.ControlPlaneCents * costHoursPerMonth,
		infrastructure:  summary.InfrastructureCents * costHoursPerMonth,
		idle:            summary.IdleHourlyCents * costHoursPerMonth,
		unallocated:     summary.UnallocatedHourlyCents * costHoursPerMonth,
	}
}

func renderClusterCost(out io.Writer, clusterReference string, cost *client.ClusterCost) {
	if !cost.HasData || cost.Summary == nil {
		_, _ = fmt.Fprintf(out, "No cost estimate for %s: %s\n", clusterReference, costReadinessExplanation(cost.Readiness))
		return
	}
	summary := cost.Summary
	currency := summary.Currency
	monthHours, monthShort := costMonthHours(summary.MonthHours)
	atLeast := costMonthAtLeast(monthShort)
	_, _ = fmt.Fprintf(out, "Cost for %s (%s, %s): %s%s projected month end · %s%s month to date · %s/mo run rate · %s/hr\n",
		clusterReference, costProviderLabel(summary.Provider), strings.ToUpper(currency),
		atLeast, formatCostCents(summary.ProjectedMonthEndCents, currency), atLeast, formatCostCents(summary.MonthToDateCents, currency),
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
	if monthHours != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", monthHours)
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Run rate by component (per month):")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Component", "Per month"})
	parts := costComponentMonths(summary)
	writer.AppendRow(table.Row{"Compute (on-demand)", formatCostCents(parts.computeOnDemand, currency)})
	if parts.computeSpot > 0 {
		writer.AppendRow(table.Row{"Compute (spot)", formatCostCents(parts.computeSpot, currency)})
	}
	writer.AppendRow(table.Row{"Storage", formatCostCents(parts.storage, currency)})
	writer.AppendRow(table.Row{"Network", formatCostCents(parts.network, currency)})
	writer.AppendRow(table.Row{"Control plane", formatCostCents(parts.controlPlane, currency)})
	if parts.infrastructure > 0 {
		writer.AppendRow(table.Row{"Bastion & VMs", formatCostCents(parts.infrastructure, currency)})
	}
	writer.AppendRow(table.Row{"of which idle", formatCostCents(parts.idle, currency)})
	writer.AppendRow(table.Row{"Unallocated", formatCostCents(parts.unallocated, currency)})
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

// cloudSavingsScanFailures is what the waste listing said about the scans a
// savings read's scan_incomplete stands for: the failed scans, or that they
// could not be read.
type cloudSavingsScanFailures struct {
	failures []client.CloudWasteCredentialError
	isUnread bool
}

// cloudSavingsScanIncomplete reports whether the platform said the last scan
// of an account bearing on the read did not finish. A platform that predates
// scan_incomplete never says so, and its waste line reads as it always did.
func cloudSavingsScanIncomplete(waste client.CloudSavingsWaste) bool {
	return waste.Available && waste.ScanIncomplete != nil && *waste.ScanIncomplete
}

// readCloudSavingsScanFailures reads which scans did not finish, only when
// the savings read says one did: a complete scan, and a platform that
// predates scan_incomplete, cost no extra request and get nil. A listing
// that cannot be read does not fail the command, since the savings read
// already said the scan is incomplete; the reason goes to stderr.
func readCloudSavingsScanFailures(stderr io.Writer, waste client.CloudSavingsWaste) *cloudSavingsScanFailures {
	if !cloudSavingsScanIncomplete(waste) {
		return nil
	}
	failures, readError := apiClient.GetCloudWasteCredentialErrors()
	if readError != nil {
		_, _ = fmt.Fprintf(stderr, "Reading which waste scan did not finish failed: %v\n", readError)
		return &cloudSavingsScanFailures{isUnread: true}
	}
	return &cloudSavingsScanFailures{failures: failures}
}

// renderCloudSavingsWaste is the waste line. When the platform says the last
// scan did not finish, no findings is unknown rather than none and a count
// may be short, so the line says that and the failed scans are named under
// it. Otherwise the line is the one this command always printed.
func renderCloudSavingsWaste(out io.Writer, waste client.CloudSavingsWaste, currency string, scan *cloudSavingsScanFailures) {
	_, _ = fmt.Fprintln(out)
	isIncomplete := cloudSavingsScanIncomplete(waste)
	switch {
	case !waste.Available:
		_, _ = fmt.Fprintln(out, "Waste: the scan could not be read (this is not the same as no waste).")
	case !waste.HasData && !isIncomplete:
		_, _ = fmt.Fprintln(out, "Waste: no scan yet.")
	case waste.FindingCount == 0 && isIncomplete:
		_, _ = fmt.Fprintln(out, "Waste: none listed, but the last scan did not finish, so that is unknown, not no waste.")
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
		if isIncomplete {
			_, _ = fmt.Fprintln(out, "The last scan did not finish, so findings may be missing from that count.")
		}
	}
	if isIncomplete {
		renderCloudSavingsScanFailures(out, scan)
	}
}

// renderCloudSavingsScanFailures names the scans that did not finish: a
// credential by its name and provider with the provider's error, a scan no
// credential of the organisation stands behind by its provider alone (not a
// deleted credential: the platform does not say that), and the scans outside
// the reader's projects as one line that names none, as the platform gives
// them. Names and errors are the platform's and the provider's text, flattened
// to one line. A listing that names no failed scan, as a platform that
// predates credential_errors answers, or one read after a rescan finished, is
// said in a line of its own: which scan did not finish is then unknown, and
// the line under an incomplete scan never just ends.
func renderCloudSavingsScanFailures(out io.Writer, scan *cloudSavingsScanFailures) {
	if scan == nil {
		return
	}
	if scan.isUnread {
		_, _ = fmt.Fprintln(out, "  Which scan did not finish could not be read.")
		return
	}
	if len(scan.failures) == 0 {
		_, _ = fmt.Fprintln(out, "  Which scan did not finish is not listed by this platform.")
		return
	}
	for _, failed := range scan.failures {
		if failed.CredentialOutsideProjects {
			_, _ = fmt.Fprintln(out, "  Did not finish: a scan by a credential outside your projects.")
			continue
		}
		name, _ := hiddenunicode.Line(failed.CredentialName)
		subject := "a credential Ankra has no name for"
		switch {
		case name != "" && failed.Provider != "":
			subject = name + " (" + costProviderLabel(failed.Provider) + ")"
		case name != "":
			subject = name
		case failed.Provider != "":
			subject += " (" + costProviderLabel(failed.Provider) + ")"
		}
		line := "  Did not finish: " + subject
		if reason, _ := hiddenunicode.Line(failed.Error); reason != "" {
			line += ": " + reason
		}
		_, _ = fmt.Fprintln(out, line)
	}
}

// cloudSavingsBasis is what a recommendation's saving rests on, as the
// Basis column says it: measured, billed or estimate for the three values
// the platform defines, and "unknown" for none or for any other value, so a
// confidence this CLI does not know never reads as a basis the total counts.
func cloudSavingsBasis(recommendation client.CloudSavingsRecommendation) string {
	if recommendation.Confidence == nil {
		return "unknown"
	}
	switch *recommendation.Confidence {
	case "measured", "billed":
		return *recommendation.Confidence
	case "estimated":
		return "estimate"
	default:
		return "unknown"
	}
}

// savingsReportsConfidence says whether the platform reports what each
// saving rests on: it sent the estimate figure or a confidence on any
// recommendation. Either is enough, so a platform that omits one of them
// is not read as an older one whose total counts every lever.
func savingsReportsConfidence(savings *client.CloudSavings) bool {
	if savings.EstimatedMonthlySavingsCents != nil {
		return true
	}
	for _, recommendation := range savings.Recommendations {
		if recommendation.Confidence != nil {
			return true
		}
	}
	return false
}

// renderCloudSavings is the human savings read. scan is what the waste
// listing said about an incomplete scan, nil when the read says none.
func renderCloudSavings(out io.Writer, savings *client.CloudSavings, scan *cloudSavingsScanFailures) {
	currency := savings.Currency
	// A platform that reports confidence counts only measured and billed levers
	// in the total and says what the estimates add apart from it (ankra-tp6xp);
	// an older one counts every lever, and its output is left as it was.
	reportsConfidence := savingsReportsConfidence(savings)
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
		renderCloudSavingsClusters(out, cloudSavingsStaleHeading(savings.Thresholds), savings.StaleClusters, true)
		renderCloudSavingsWaste(out, savings.Waste, currency, scan)
		return
	}
	_, _ = fmt.Fprintf(out, "Cloud savings (%s): %s/mo across %s", strings.ToUpper(currency),
		formatCostCents(savings.TotalMonthlySavingsCents, currency),
		pluralCount(len(savings.Recommendations), "recommendation"))
	if savings.EstimatedMonthlySavingsCents != nil && *savings.EstimatedMonthlySavingsCents > 0 {
		_, _ = fmt.Fprintf(out, ", plus %s/mo estimated (not counted: no usage measured)",
			formatCostCents(*savings.EstimatedMonthlySavingsCents, currency))
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %d of %s analysed", savings.AnalysedClusterCount, pluralClusters(savings.PricedClusterCount))
	budgetExhausted := savings.AnalysisBudgetExhausted != nil && *savings.AnalysisBudgetExhausted
	switch {
	case savings.UnanalysedClusterCount > 0 && budgetExhausted:
		budget := "the analysis time budget"
		if seconds := savings.Thresholds.AnalysisBudgetSeconds; seconds != nil && *seconds > 0 {
			budget = fmt.Sprintf("the %ds analysis time budget", *seconds)
		}
		_, _ = fmt.Fprintf(out, " (%d not analysed: %s ran out before every one of the %d biggest was read)",
			savings.UnanalysedClusterCount, budget, savings.Thresholds.AnalysedClusterLimit)
	case savings.UnanalysedClusterCount > 0:
		_, _ = fmt.Fprintf(out, " (%d not analysed: only the %d biggest are)", savings.UnanalysedClusterCount,
			savings.Thresholds.AnalysedClusterLimit)
	case savings.Thresholds.AnalysedClusterLimit > 0:
		_, _ = fmt.Fprintf(out, " (limit %d)", savings.Thresholds.AnalysedClusterLimit)
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
		writer := newCostTable(out)
		if reportsConfidence {
			_, _ = fmt.Fprintln(out, "Recommendations (a cluster can carry several; the total counts each cluster once, at its best measured or billed lever, and never an estimate):")
			writer.AppendHeader(table.Row{"#", "Cluster", "Environment", "Lever", "Savings/mo", "Basis", "Share", "Run rate/mo", "Cluster ID"})
		} else {
			_, _ = fmt.Fprintln(out, "Recommendations (a cluster can carry several; the total counts each cluster once, at its best lever):")
			writer.AppendHeader(table.Row{"#", "Cluster", "Environment", "Lever", "Savings/mo", "Share", "Run rate/mo", "Cluster ID"})
		}
		for index, recommendation := range savings.Recommendations {
			environment := "-"
			if recommendation.Environment != nil && *recommendation.Environment != "" {
				environment = *recommendation.Environment
			}
			row := table.Row{
				index + 1,
				recommendation.ClusterName,
				environment,
				costSavingsLever(recommendation),
				formatCostCents(recommendation.MonthlySavingsCents, currency),
			}
			if reportsConfidence {
				row = append(row, cloudSavingsBasis(recommendation))
			}
			writer.AppendRow(append(row,
				fmt.Sprintf("%d%%", recommendation.SharePercent),
				formatCostCents(recommendation.MonthlyCostCents, currency),
				recommendation.ClusterID,
			))
		}
		writer.Render()
		renderCloudSavingsOffHoursHint(out, savings.Recommendations)
	}

	renderCloudSavingsClusters(out, "Unpriced clusters (no cost snapshot yet):", savings.UnpricedClusters, true)
	renderCloudSavingsClusters(out, cloudSavingsStaleHeading(savings.Thresholds), savings.StaleClusters, true)
	renderCloudSavingsClusters(out, "Unreadable clusters (breakdown could not be read on this pass):", savings.UnreadableClusters, false)
	renderCloudSavingsClusters(out, "Offline clusters (agent not checked in; nothing is recommended for them until it is):", savings.OfflineClusters, false)
	renderCloudSavingsWaste(out, savings.Waste, currency, scan)
}

func renderCostSettings(out io.Writer, settings *client.CostSettings) {
	egress := "off"
	if settings.IncludeNetworkEgressEstimate {
		egress = "on"
	}
	_, _ = fmt.Fprintf(out, "Display currency: %s\n", strings.ToUpper(settings.Currency))
	_, _ = fmt.Fprintf(out, "Effective discount: %g%%\n", settings.EffectiveDiscountPct)
	// Nothing meters network egress yet (ankra-bn86k4.8.21): the setting is
	// stored and moves no figure, so the line says so whichever way it is set.
	_, _ = fmt.Fprintf(out, "Network egress estimate: %s (stored, not metered yet: no figure includes network egress today)\n", egress)
	if settings.AnalysedClusterLimit == nil {
		_, _ = fmt.Fprintln(out, "Analysed-cluster limit: unknown (this platform does not report it)")
	} else {
		_, _ = fmt.Fprintf(out, "Analysed-cluster limit: %d (the savings model analyses the %d costliest priced clusters)\n",
			*settings.AnalysedClusterLimit, *settings.AnalysedClusterLimit)
	}
}

// analysedClusterLimitMaximum is the most clusters the savings model may be
// told to analyse (the settings route's bound).
const analysedClusterLimitMaximum = 50

// parseAnalysedClusterLimit reads --analysed-cluster-limit: a whole number
// from 1 to 50 sets the limit, and "default" returns it to the platform's
// default.
func parseAnalysedClusterLimit(raw string) (*client.AnalysedClusterLimitChange, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "default" {
		return &client.AnalysedClusterLimitChange{Default: true}, nil
	}
	limit, parseError := strconv.Atoi(value)
	if parseError != nil || limit < 1 || limit > analysedClusterLimitMaximum {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"--analysed-cluster-limit must be a whole number from 1 to %d, or default for the platform's default (got %q)",
			analysedClusterLimitMaximum, raw))
	}
	return &client.AnalysedClusterLimitChange{Limit: limit}, nil
}

// cloudSavingsStaleHeading names the stale clusters by the platform's own
// staleness window when it reports one.
func cloudSavingsStaleHeading(thresholds client.CloudSavingsThresholds) string {
	if thresholds.SnapshotStaleAfterHours != nil && *thresholds.SnapshotStaleAfterHours > 0 {
		return fmt.Sprintf("Stale clusters (no cost snapshot in the last %d hours, so metering has stopped):", *thresholds.SnapshotStaleAfterHours)
	}
	return "Stale clusters (metering stopped over a day ago):"
}

func init() {
	costSettingsSetCmd.Flags().String("currency", "", "Display currency: usd, eur or gbp")
	costSettingsSetCmd.Flags().Float64("discount", 0, "Effective discount in percent (0-100), applied on top of list prices; 10 means 10%")
	costSettingsSetCmd.Flags().Bool("include-egress", false, "Whether figures should include a network egress estimate once egress is metered (stored, not metered yet: no figure includes it today; --include-egress=false clears it)")
	costSettingsSetCmd.Flags().String("analysed-cluster-limit", "", "How many of the costliest priced clusters the savings model analyses (1-50), or default for the platform's 8")
	costSummaryCmd.Flags().String("by", "", "Show only one breakdown: provider, credential or project (default: the full rollup)")
	registerStructuredOutputFlags(costSummaryCmd, costSavingsCmd, costClusterCmd, costSettingsGetCmd, costSettingsSetCmd)
	registerCostProjectScopeFlag(costSummaryCmd, costSavingsCmd)
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
