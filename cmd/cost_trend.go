package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// The widest the cluster and event cells of `cost events` grow before they
// wrap, so the table fits a 100-column terminal. They are wrapped in the cell
// rather than through a column WidthMax for the reason the ledger gives: a
// merged note row would be folded to the first column's width.
const (
	costEventsClusterWidthMax = 20
	costEventsSubjectWidthMax = 30
)

var costTrendCmd = &cobra.Command{
	Use:   "trend",
	Short: "The fleet's run rate per day, with the days coverage changed drawn apart from spend",
	Long: `Read the organisation's cloud cost run rate per UTC day over every priced
cluster - the line the portal draws under Cost.

A cost snapshot exists only for an hour a cluster was priced, so a cluster
missing from a day was not priced that day: it did not cost nothing. Each day
says how many clusters it covers, and a day with no priced cluster is "not
priced", never zero. A day where some priced clusters were only partly
priced (a node or billed resource with no price) is a floor.

When the set of priced clusters changes - a cluster is created, deleted,
stopped or loses its pricing - the line moves without anything being spent.
Those days are listed apart, with the clusters that entered and left and what
that moved the line by: a coverage move, not a spend move. The last day is
the current run rate, the same figure 'ankra cost summary' shows.

--days sets the window (the platform keeps 35 days of snapshots and serves at
most 34; 30 when omitted). Pass -o json (or yaml) for the full document.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost trend
  ankra cost trend --days 7
  ankra cost trend -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		days, daysError := costTrendDaysFlag(cmd)
		if daysError != nil {
			return daysError
		}
		trend, err := apiClient.GetFleetCostTrend(days)
		if err != nil {
			return costTrendReadError(err, "GET /api/v1/org/cloud-cost/trend", "the fleet cost trend", "reading the fleet cost trend")
		}
		if rendered, err := renderStructured(cmd, trend); rendered || err != nil {
			return err
		}
		renderFleetCostTrend(cmd.OutOrStdout(), trend)
		return nil
	},
}

var costEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "What moved the cost line: releases, node changes, stops and starts, decisions, coverage and resolved waste",
	Long: `Read the events that moved the organisation's cost run rate, newest first:
application releases, node-count changes, scheduled and manual stops and
starts, executed cost decisions, clusters entering or leaving pricing, and
resolved cloud waste.

A cluster's run rate is metered hourly, so an event's move is how the
cluster's run rate changed between the priced hour before it and the first
priced hour at least 30 minutes after it. When the event was alone in that
window the move is its own. When several events share the window (a release
and the scale-up it caused) the move is theirs together and each row says
"shared" rather than claim a split. With no priced hour on one side the move
is unknown, never zero. A coverage event's move is coverage, not spend; a
resolved finding's is its own monthly cost. The platform's note on an event
sits under its row.

A kind of event the platform could not read is named above the table: its
events are missing, not quiet. The platform serves at most the newest 500.

--days sets the window (at most 34; 30 when omitted). Pass -o json (or yaml)
for the full document.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost events
  ankra cost events --days 7
  ankra cost events -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		days, daysError := costTrendDaysFlag(cmd)
		if daysError != nil {
			return daysError
		}
		events, err := apiClient.GetCostEvents(days)
		if err != nil {
			return costTrendReadError(err, "GET /api/v1/org/cloud-cost/events", "the cost events", "reading the cost events")
		}
		if rendered, err := renderStructured(cmd, events); rendered || err != nil {
			return err
		}
		renderCostEvents(cmd.OutOrStdout(), events)
		return nil
	},
}

func init() {
	costTrendCmd.Flags().Int("days", 0, "How many UTC days to cover, ending today (1-34; the platform's default of 30 when omitted)")
	costEventsCmd.Flags().Int("days", 0, "How many UTC days to cover, ending today (1-34; the platform's default of 30 when omitted)")
	registerStructuredOutputFlags(costTrendCmd, costEventsCmd)
	costCmd.AddCommand(costTrendCmd)
	costCmd.AddCommand(costEventsCmd)
}

// costTrendDaysFlag returns --days when it was given, or 0 so the platform
// applies its own default window. The upper bound is the platform's (its
// snapshot retention), so it is left to the route to answer.
func costTrendDaysFlag(cmd *cobra.Command) (int, error) {
	if !cmd.Flags().Changed("days") {
		return 0, nil
	}
	days, _ := cmd.Flags().GetInt("days")
	if days < 1 {
		return 0, withExitCode(exitUsage, errors.New("--days must be at least 1"))
	}
	return days, nil
}

// costTrendReadError maps the one 404 these routes can answer with: a
// platform that predates them serves no such route at all, and "request
// failed: status 404" reads as an auth or token problem. The status alone
// decides, as in cloudSavingsReadError.
func costTrendReadError(readError error, route string, subject string, operation string) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, fmt.Errorf(
			"this platform does not serve %s: %s is not registered, so this platform predates it", subject, route))
	}
	return fmt.Errorf("%s: %w", operation, readError)
}

// formatCostTrendDelta renders a move in the run rate with its sign, so a
// rise and a fall read apart ("+€40.00", "-€12.00").
func formatCostTrendDelta(cents int64, currency string) string {
	if cents > 0 {
		return "+" + formatCostCents(cents, currency)
	}
	return formatCostCents(cents, currency)
}

// formatCostTrendRunRate is a day's run rate; a day no cluster was priced is
// unknown, so it reads "not priced", never a zero.
func formatCostTrendRunRate(cents *int64, currency string) string {
	if cents == nil {
		return "not priced"
	}
	return formatCostCents(*cents, currency)
}

func costTrendClusterCount(count int) string {
	if count == 1 {
		return "1 cluster priced"
	}
	return fmt.Sprintf("%d clusters priced", count)
}

// costTrendCoverageClusters lists the clusters on one side of a coverage
// change, one per line, with the run rate each carried.
func costTrendCoverageClusters(clusters []client.CostCoverageChangeCluster, currency string) string {
	if len(clusters) == 0 {
		return "—"
	}
	lines := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		lines = append(lines, fmt.Sprintf("%s (%s/mo)", cluster.ClusterName, formatCostCents(cluster.MonthlyCostEstimateCents, currency)))
	}
	return strings.Join(lines, "\n")
}

// costTrendFirstPriced says when a cluster was first priced: exact, the
// earliest priced hour still known (priced by then, possibly long before), or
// unknown.
func costTrendFirstPriced(cluster client.FleetCostTrendCluster) string {
	switch {
	case cluster.FirstPricedAt == nil || *cluster.FirstPricedAt == "":
		return "unknown"
	case cluster.FirstPricedExact:
		return *cluster.FirstPricedAt
	default:
		return "by " + *cluster.FirstPricedAt
	}
}

func renderFleetCostTrend(out io.Writer, trend *client.FleetCostTrend) {
	currency := trend.Currency
	var first *client.FleetCostTrendPoint
	for index := range trend.Points {
		if trend.Points[index].MonthlyCostEstimateCents != nil {
			first = &trend.Points[index]
			break
		}
	}
	if first == nil {
		// Read, and nothing was priced on any day: that is an answer, not a
		// run rate of nothing.
		_, _ = fmt.Fprintf(out, "No cluster was priced on any day of the last %d days, so there is no run rate to follow.\n", trend.Days)
		_, _ = fmt.Fprintln(out, "Estimates appear once a cluster on AWS, Google Cloud, Azure, Hetzner, OVHcloud, UpCloud or Scaleway has reported pricing; AWS, Google Cloud and Azure clusters need a connected cloud credential.")
		return
	}
	last := trend.Points[len(trend.Points)-1]
	lastLabel := "on " + last.Day
	if last.Current {
		lastLabel = "now"
	}
	_, _ = fmt.Fprintf(out, "Fleet run rate (%s), last %d days", strings.ToUpper(currency), trend.Days)
	if trend.GeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · generated %s", trend.GeneratedAt)
	}
	_, _ = fmt.Fprintln(out)
	lastRunRate := "not priced"
	if last.MonthlyCostEstimateCents != nil {
		lastRunRate = formatCostCents(*last.MonthlyCostEstimateCents, currency) + "/mo"
	}
	_, _ = fmt.Fprintf(out, "  %s/mo on %s (%s) -> %s %s (%s)\n",
		formatCostCents(*first.MonthlyCostEstimateCents, currency), first.Day, costTrendClusterCount(first.PricedClusterCount),
		lastRunRate, lastLabel, costTrendClusterCount(last.PricedClusterCount))
	if len(trend.CoverageChanges) == 0 {
		_, _ = fmt.Fprintln(out, "  No coverage change: the same clusters were priced on every day of the window.")
	} else {
		var coverageTotal int64
		for _, change := range trend.CoverageChanges {
			coverageTotal += change.CoverageDeltaMonthlyCents
		}
		_, _ = fmt.Fprintf(out, "  %s moved the line by %s/mo in total: clusters entering or leaving pricing, not spend.\n",
			pluralCount(len(trend.CoverageChanges), "coverage change"), formatCostTrendDelta(coverageTotal, currency))
	}
	hasFloor := false
	for _, point := range trend.Points {
		if point.FullyPricedClusterCount < point.PricedClusterCount {
			hasFloor = true
			break
		}
	}
	if hasFloor {
		_, _ = fmt.Fprintln(out, "  A day with fewer fully priced clusters than priced ones is a floor: some of their nodes or billed resources had no price.")
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Run rate per day (UTC):")
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{
		{Number: 2, Align: text.AlignRight},
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
	})
	writer.AppendHeader(table.Row{"Day", "Run rate/mo", "Priced", "Fully priced"})
	for _, point := range trend.Points {
		day := point.Day
		if point.Current {
			day += " (now)"
		}
		writer.AppendRow(table.Row{day, formatCostTrendRunRate(point.MonthlyCostEstimateCents, currency),
			point.PricedClusterCount, point.FullyPricedClusterCount})
	}
	writer.Render()

	if len(trend.CoverageChanges) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Coverage changes (clusters entering or leaving pricing; they move the line, not spend):")
		changeWriter := newCostTable(out)
		changeWriter.SetColumnConfigs([]table.ColumnConfig{{Number: 5, Align: text.AlignRight}})
		changeWriter.AppendHeader(table.Row{"Day", "Priced", "Entered", "Left", "Moved the line"})
		for _, change := range trend.CoverageChanges {
			changeWriter.AppendRow(table.Row{
				change.Day,
				fmt.Sprintf("%d -> %d", change.PricedClusterCountBefore, change.PricedClusterCountAfter),
				costTrendCoverageClusters(change.Entered, currency),
				costTrendCoverageClusters(change.Left, currency),
				formatCostTrendDelta(change.CoverageDeltaMonthlyCents, currency) + "/mo",
			})
		}
		changeWriter.Render()
	}

	if len(trend.Clusters) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Clusters priced in the window:")
		clusterWriter := newCostTable(out)
		clusterWriter.SetColumnConfigs([]table.ColumnConfig{{Number: 2, Align: text.AlignRight}})
		clusterWriter.AppendHeader(table.Row{"Cluster", "Priced days", "In the window", "First priced"})
		for _, cluster := range trend.Clusters {
			name := cluster.ClusterName
			if cluster.Deleted {
				name += " (deleted)"
			}
			span := cluster.FirstDay
			if cluster.LastDay != "" && cluster.LastDay != cluster.FirstDay {
				span += " -> " + cluster.LastDay
			}
			clusterWriter.AppendRow(table.Row{name, cluster.PricedDays, span, costTrendFirstPriced(cluster)})
		}
		clusterWriter.Render()
	}
}

// costEventKindLabel names an event kind for a table row.
func costEventKindLabel(kind string) string {
	switch kind {
	case "application_release":
		return "Release"
	case "node_count":
		return "Node count"
	case "power_schedule":
		return "Power schedule"
	case "power_manual":
		return "Manual power"
	case "decision":
		return "Cost decision"
	case "coverage":
		return "Coverage"
	case "waste_resolved":
		return "Waste resolved"
	case "":
		return "Event"
	default:
		// A kind this CLI predates reads as its words, capitalised by rune.
		words := []rune(strings.ReplaceAll(kind, "_", " "))
		return string(unicode.ToUpper(words[0])) + string(words[1:])
	}
}

// costEventSourceLabel names a kind of event as a source, for the line that
// says which could not be read.
func costEventSourceLabel(kind string) string {
	switch kind {
	case "application_release":
		return "application releases"
	case "node_count":
		return "node-count changes"
	case "power_schedule":
		return "scheduled stops and starts"
	case "power_manual":
		return "manual stops and starts"
	case "decision":
		return "cost decisions"
	case "coverage":
		return "coverage changes"
	case "waste_resolved":
		return "resolved waste"
	default:
		return strings.ReplaceAll(kind, "_", " ")
	}
}

// costEventMove is an event's move on the run rate, qualified by how it was
// measured. An unknown move is named, never printed as zero.
func costEventMove(event client.CostEvent, currency string) string {
	signed := func(cents *int64) string {
		return formatCostTrendDelta(*cents, currency)
	}
	switch event.DeltaStatus {
	case "isolated":
		if event.DeltaMonthlyCents != nil {
			return signed(event.DeltaMonthlyCents)
		}
	case "shared":
		if event.WindowDeltaMonthlyCents != nil {
			return signed(event.WindowDeltaMonthlyCents) + " (shared)"
		}
		return "unknown (shared)"
	case "no_snapshots":
		return "unknown"
	case "coverage":
		if event.DeltaMonthlyCents != nil {
			return signed(event.DeltaMonthlyCents) + " (coverage)"
		}
	case "own_cost":
		if event.DeltaMonthlyCents != nil {
			return signed(event.DeltaMonthlyCents) + " (own cost)"
		}
	case "unpriced":
		return "unpriced"
	case "not_applied":
		return "not applied"
	}
	// A status this CLI predates, or one without its figure: show what the
	// platform measured, qualified by the status, and unknown otherwise.
	qualifier := ""
	if event.DeltaStatus != "" {
		qualifier = " (" + strings.ReplaceAll(event.DeltaStatus, "_", " ") + ")"
	}
	switch {
	case event.DeltaMonthlyCents != nil:
		return signed(event.DeltaMonthlyCents) + qualifier
	case event.WindowDeltaMonthlyCents != nil:
		return signed(event.WindowDeltaMonthlyCents) + qualifier
	default:
		return "unknown" + qualifier
	}
}

func costEventCluster(event client.CostEvent) string {
	switch {
	case event.ClusterName != nil && *event.ClusterName != "":
		return *event.ClusterName
	case event.ClusterID != nil && *event.ClusterID != "":
		return "cluster " + (*event.ClusterID)[:min(8, len(*event.ClusterID))]
	default:
		return "(account)"
	}
}

// costEventWhen renders the event's time in UTC to the minute, or the wire
// value when it does not parse.
func costEventWhen(at string) string {
	parsed, parseError := time.Parse(time.RFC3339, at)
	if parseError != nil {
		return at
	}
	return parsed.UTC().Format("2006-01-02 15:04")
}

func costEventWhat(event client.CostEvent) string {
	what := costEventKindLabel(event.Kind)
	if subject := strings.TrimSpace(event.Subject); subject != "" {
		what += ": " + subject
	}
	if event.Actor != nil && strings.TrimSpace(*event.Actor) != "" {
		what += " (by " + strings.TrimSpace(*event.Actor) + ")"
	}
	return what
}

func renderCostEvents(out io.Writer, events *client.CostEvents) {
	currency := events.Currency
	unavailable := []string{}
	for _, source := range events.Sources {
		if !source.Available {
			unavailable = append(unavailable, costEventSourceLabel(source.Kind))
		}
	}
	_, _ = fmt.Fprintf(out, "Cost events (%s), last %d days: %s", strings.ToUpper(currency), events.Days,
		pluralCount(len(events.Events), "event"))
	if events.GeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · generated %s", events.GeneratedAt)
	}
	_, _ = fmt.Fprintln(out)
	if len(unavailable) > 0 {
		_, _ = fmt.Fprintf(out, "  Could not be read: %s. Those events are missing below, not quiet.\n", strings.Join(unavailable, ", "))
	}
	if len(events.Events) == 0 {
		if len(unavailable) > 0 {
			_, _ = fmt.Fprintln(out, "No event from the sources that could be read.")
		} else {
			_, _ = fmt.Fprintf(out, "No event moved the cost line in the last %d days.\n", events.Days)
		}
		return
	}

	header := table.Row{"When (UTC)", "Cluster", "What happened", "Move/mo"}
	rows := make([]table.Row, 0, len(events.Events))
	for _, event := range events.Events {
		rows = append(rows, table.Row{
			costEventWhen(event.At),
			text.WrapSoft(costEventCluster(event), costEventsClusterWidthMax),
			text.WrapSoft(costEventWhat(event), costEventsSubjectWidthMax),
			costEventMove(event, currency),
		})
	}
	// A note spans the whole row under the one it explains, wrapped to the
	// table's own width so it never widens it (see renderCloudLedger).
	noteWidth := len(header) - 1
	for column := range header {
		width := text.LongestLineLen(fmt.Sprint(header[column]))
		for _, row := range rows {
			width = max(width, text.LongestLineLen(fmt.Sprint(row[column])))
		}
		noteWidth += width
	}

	_, _ = fmt.Fprintln(out)
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{{Number: 4, Align: text.AlignRight}})
	writer.AppendHeader(header)
	for index, event := range events.Events {
		writer.AppendRow(rows[index])
		if note := strings.TrimSpace(event.Note); note != "" {
			wrapped := wrapCostLedgerNote(note, noteWidth)
			writer.AppendRow(table.Row{wrapped, wrapped, wrapped, wrapped},
				table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
		}
	}
	writer.Render()
	if events.Truncated {
		_, _ = fmt.Fprintf(out, "(the platform serves the newest %d events; older ones in the window are not shown)\n", len(events.Events))
	}
}
