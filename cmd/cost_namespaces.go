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

// costNamespacesRowLimit is how many namespaces the table lists; the rest are
// counted under it, and -o json carries them all.
const costNamespacesRowLimit = 25

// costNamespacesTrendMaxBuckets is the longest series drawn as a trend: one
// character per bucket still fits a terminal at 35 days, not at 168 hours.
const costNamespacesTrendMaxBuckets = 48

// The trend's marks: an unmetered bucket is unknown, never a zero, and a
// metered bucket the namespace had nothing in is a zero, never a low bar.
const (
	costNamespacesUnknownMark = '·'
	costNamespacesZeroMark    = '_'
)

var costNamespacesTrendMarks = []rune("▁▂▃▄▅▆▇█")

var costNamespacesCmd = &cobra.Command{
	Use:   "namespaces <cluster-name-or-id>",
	Short: "A cluster's cost per namespace over time: day by day (35 days kept) or hour by hour",
	Long: `Read how a cluster's cost was allocated to each of its namespaces over time,
from the hourly metering (the platform keeps 35 days).

--days sets the window (1-35; 30 when omitted) and --granularity the bucket:
day (the default) or hour (at most 7 days; 7 when --days is omitted).

The metering runs once an hour. A bucket none of whose hours were metered is
unknown for every namespace, drawn '·' and never counted as zero; a namespace
missing from a metered bucket was allocated nothing there, drawn '_'. The
header says how many buckets were metered. TOTAL sums the metered hours,
LATEST is the current (partial) bucket and PEAK the highest bucket. The trend
is drawn per namespace, scaled to its own peak, for windows of at most 48
buckets. Nothing is extrapolated: these are list-price estimates, not the bill.

The costliest 25 namespaces are listed. Pass -o json (or yaml) for every
namespace and every bucket.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra cost namespaces prod-eu
  ankra cost namespaces prod-eu --days 7
  ankra cost namespaces prod-eu --days 2 --granularity hour
  ankra cost namespaces prod-eu -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		days, daysError := costTrendDaysFlag(cmd)
		if daysError != nil {
			return daysError
		}
		granularity, _ := cmd.Flags().GetString("granularity")
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}
		history, err := apiClient.GetNamespaceCostHistory(clusterID, days, granularity)
		if err != nil {
			return costNamespacesReadError(err)
		}
		if rendered, err := renderStructured(cmd, history); rendered || err != nil {
			return err
		}
		renderNamespaceCostHistory(cmd.OutOrStdout(), args[0], history)
		return nil
	},
}

// costNamespacesClusterNotFound is the route's answer for a cluster outside
// the caller's organisation.
const costNamespacesClusterNotFound = "Cluster not found"

// costNamespacesReadError keeps the platform's own words: a window it refuses
// is a usage error in its sentence, a cluster outside the organisation is
// named as such, and a platform with no such route predates it.
func costNamespacesReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) {
		switch {
		case unexpected.StatusCode == http.StatusBadRequest && unexpected.Detail != "":
			return withExitCode(exitUsage, errors.New(unexpected.Detail))
		case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == costNamespacesClusterNotFound:
			// The route's own not-found. A platform without the route also
			// answers 404, sometimes with a detail ("Not Found."), which is
			// why only this sentence is read as a missing cluster.
			return withExitCode(exitNotFound, errors.New("the cluster was not found in this organisation"))
		}
	}
	return costTrendReadError(readError, "GET /api/v1/org/clusters/{cluster_id}/cost/namespaces/history",
		"the namespace cost history", "reading the namespace cost history")
}

func renderNamespaceCostHistory(out io.Writer, clusterReference string, history *client.NamespaceCostHistory) {
	unit := "day"
	if history.Granularity == "hour" {
		unit = "hour"
	}
	_, _ = fmt.Fprintf(out, "Namespace cost of %s over the last %s by the %s, in %s\n", clusterReference,
		pluralCount(history.Days, "day"), unit, strings.ToUpper(history.Currency))
	metered := 0
	for _, bucket := range history.Buckets {
		if bucket.AttributedHours > 0 {
			metered++
		}
	}
	switch {
	case len(history.Buckets) == 0:
		_, _ = fmt.Fprintln(out, "The window holds no bucket.")
		return
	case metered == 0:
		_, _ = fmt.Fprintf(out, "No %s of this window was metered, so every namespace's cost is unknown, not zero.\n", unit)
		return
	case metered < len(history.Buckets):
		_, _ = fmt.Fprintf(out, "%d of %d %ss were metered; the other %d are unknown (%c), not zero.\n",
			metered, len(history.Buckets), unit, len(history.Buckets)-metered, costNamespacesUnknownMark)
	default:
		_, _ = fmt.Fprintf(out, "All %d %ss were metered.\n", len(history.Buckets), unit)
	}
	if len(history.Namespaces) == 0 {
		_, _ = fmt.Fprintln(out, "No namespace was allocated any cost in the metered hours.")
		return
	}
	drawTrend := len(history.Buckets) <= costNamespacesTrendMaxBuckets
	_, _ = fmt.Fprintln(out)
	tableWriter := newCostTable(out)
	header := table.Row{"NAMESPACE", "TOTAL", "LATEST", "PEAK"}
	if drawTrend {
		header = append(header, "TREND")
	}
	tableWriter.AppendHeader(header)
	for index, series := range history.Namespaces {
		if index == costNamespacesRowLimit {
			break
		}
		row := table.Row{series.Namespace, formatCostCents(series.TotalCents, history.Currency),
			costNamespacesLatest(series, history.Currency), costNamespacesPeak(series, history.Currency)}
		if drawTrend {
			row = append(row, costNamespacesTrend(series.CostCents))
		}
		tableWriter.AppendRow(row)
	}
	tableWriter.Render()
	if hidden := len(history.Namespaces) - costNamespacesRowLimit; hidden > 0 {
		_, _ = fmt.Fprintf(out, "\n%s more not listed; -o json has every namespace.\n", pluralCount(hidden, "namespace"))
	}
	if !drawTrend {
		_, _ = fmt.Fprintf(out, "\nThe trend is drawn for at most %d buckets; -o json has all %d.\n",
			costNamespacesTrendMaxBuckets, len(history.Buckets))
	}
}

// costNamespacesLatest is the current bucket's value, or unknown when the
// metering has not attributed any of its hours yet.
func costNamespacesLatest(series client.NamespaceCostSeries, currency string) string {
	if len(series.CostCents) == 0 || series.CostCents[len(series.CostCents)-1] == nil {
		return "unknown"
	}
	return formatCostCents(*series.CostCents[len(series.CostCents)-1], currency)
}

// costNamespacesPeak is the highest metered bucket; with none metered it is
// unknown.
func costNamespacesPeak(series client.NamespaceCostSeries, currency string) string {
	var peak *int64
	for _, value := range series.CostCents {
		if value != nil && (peak == nil || *value > *peak) {
			peak = value
		}
	}
	if peak == nil {
		return "unknown"
	}
	return formatCostCents(*peak, currency)
}

// costNamespacesTrend draws one mark per bucket, scaled to the series' own
// peak: '·' for an unmetered bucket, '_' for a metered zero.
func costNamespacesTrend(values []*int64) string {
	var peak int64
	for _, value := range values {
		if value != nil && *value > peak {
			peak = *value
		}
	}
	var trend strings.Builder
	for _, value := range values {
		switch {
		case value == nil:
			trend.WriteRune(costNamespacesUnknownMark)
		case *value <= 0 || peak == 0:
			trend.WriteRune(costNamespacesZeroMark)
		default:
			level := int((*value*int64(len(costNamespacesTrendMarks)) - 1) / peak)
			if level >= len(costNamespacesTrendMarks) {
				level = len(costNamespacesTrendMarks) - 1
			}
			trend.WriteRune(costNamespacesTrendMarks[level])
		}
	}
	return trend.String()
}

func init() {
	costNamespacesCmd.Flags().Int("days", 0, "How many days to cover, ending now (1-35, at most 7 by the hour; the platform's default when omitted)")
	costNamespacesCmd.Flags().String("granularity", "", "The bucket size: day (the default) or hour")
	registerStructuredOutputFlags(costNamespacesCmd)
	costCmd.AddCommand(costNamespacesCmd)
}
