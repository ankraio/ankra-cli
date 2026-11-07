package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Query the cluster's Prometheus metrics source",
	Long: `Query the Prometheus metrics source configured for a cluster.

The query is proxied through the Ankra agent to the in-cluster Prometheus
endpoint configured in Cluster Settings > Metrics.`,
}

var clusterMetricsQueryCmd = &cobra.Command{
	Use:   "query [promql]",
	Short: "Run an instant PromQL query",
	Long: `Run an instant PromQL query against the cluster's Prometheus source.

Examples:
  ankra cluster metrics query 'up'
  ankra cluster metrics query 'sum(rate(container_cpu_usage_seconds_total[5m])) by (pod)'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveMetricsClusterID(cmd)
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetInt("timeout")
		outputFormat, _ := cmd.Flags().GetString("output")

		result, err := apiClient.QueryPrometheusInstant(clusterID, args[0], timeout)
		if err != nil {
			return err
		}
		return renderPrometheusResult(result, outputFormat, false)
	},
}

var clusterMetricsQueryRangeCmd = &cobra.Command{
	Use:   "query-range [promql]",
	Short: "Run a range PromQL query",
	Long: `Run a range PromQL query against the cluster's Prometheus source.

Provide either --range (a duration relative to now) or both --start and --end
(Unix seconds). When --step is omitted a sensible step is chosen automatically.

A series the platform returned fewer points of than the query produced is
called out: the Points column reads "50 of 385", and a note on stderr says how
far apart the returned points really are. Read such a series as a sketch, since
a spike between two points is not in it.

Examples:
  ankra cluster metrics query-range 'up' --range 1h
  ankra cluster metrics query-range 'rate(node_cpu_seconds_total[5m])' --range 6h --step 5m
  ankra cluster metrics query-range 'up' --start 1717000000 --end 1717003600 --step 1m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveMetricsClusterID(cmd)
		if err != nil {
			return err
		}
		rangeDuration, _ := cmd.Flags().GetString("range")
		start, _ := cmd.Flags().GetString("start")
		end, _ := cmd.Flags().GetString("end")
		step, _ := cmd.Flags().GetString("step")
		timeout, _ := cmd.Flags().GetInt("timeout")
		outputFormat, _ := cmd.Flags().GetString("output")

		result, err := apiClient.QueryPrometheusRange(clusterID, client.PrometheusRangeOptions{
			Query:          args[0],
			Range:          rangeDuration,
			Start:          start,
			End:            end,
			Step:           step,
			TimeoutSeconds: timeout,
		})
		if err != nil {
			return err
		}
		return renderPrometheusResult(result, outputFormat, true)
	},
}

func resolveMetricsClusterID(cmd *cobra.Command) (string, error) {
	clusterName, _ := cmd.Flags().GetString("cluster")
	if clusterName != "" {
		cluster, err := apiClient.GetCluster(clusterName)
		if err != nil {
			return "", fmt.Errorf("finding cluster %s: %w", clusterName, err)
		}
		return cluster.ID, nil
	}
	selected, err := loadSelectedCluster()
	if err != nil {
		return "", fmt.Errorf("no active cluster selected. Run 'ankra cluster select <name>' or pass --cluster")
	}
	return selected.ID, nil
}

func renderPrometheusResult(result *client.PrometheusQueryResult, outputFormat string, isRange bool) error {
	if isRange {
		// stderr in every format: a script's stdout stays parseable, and a
		// script still gets told.
		printRangeDownsamplingNotice(os.Stderr, result)
	}

	switch outputFormat {
	case "json":
		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling to JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
	case "yaml":
		yamlData, err := yaml.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshalling to YAML: %w", err)
		}
		fmt.Print(string(yamlData))
		return nil
	}

	if len(result.Result) == 0 {
		fmt.Println("No series returned.")
		return nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	if isRange {
		t.AppendHeader(table.Row{"Metric", "Points", "Latest Value"})
	} else {
		t.AppendHeader(table.Row{"Metric", "Value"})
	}

	for _, series := range result.Result {
		obj, ok := series.(map[string]interface{})
		if !ok {
			continue
		}
		metricLabel := formatPrometheusMetric(obj)
		if isRange {
			latestValue, pointCount := prometheusRangeSummary(obj)
			t.AppendRow(table.Row{metricLabel, formatRangePointCount(obj, pointCount), latestValue})
		} else {
			t.AppendRow(table.Row{metricLabel, prometheusInstantValue(obj)})
		}
	}
	t.Render()

	// result.Truncated alone does not say series are missing: it is also set
	// when every series came back with fewer points.
	if result.Truncated && result.TotalSeries > len(result.Result) {
		fmt.Fprintf(os.Stderr, "\nShowing %d of %d series (truncated). Narrow the query to see the rest.\n",
			len(result.Result), result.TotalSeries)
	}
	return nil
}

// originalPointCount is how many points the metrics source produced for a
// series. The second result is false when the platform did not say.
func originalPointCount(series map[string]interface{}) (int, bool) {
	count, isNumber := series["original_point_count"].(float64)
	if !isNumber {
		return 0, false
	}
	return int(count), true
}

// formatRangePointCount is the Points cell: the count, or "50 of 385" for a
// series that lost points on the way here.
func formatRangePointCount(series map[string]interface{}, returned int) string {
	if original, known := originalPointCount(series); known && original > returned {
		return fmt.Sprintf("%d of %d", returned, original)
	}
	return fmt.Sprintf("%d", returned)
}

// sampleTimestamp is the time of one [timestamp, value] sample.
func sampleTimestamp(sample interface{}) (float64, bool) {
	pair, isPair := sample.([]interface{})
	if !isPair || len(pair) < 2 {
		return 0, false
	}
	timestamp, isNumber := pair[0].(float64)
	return timestamp, isNumber
}

// averagePointSpacing is the mean time between a series' returned points, or
// 0 when it has too few to measure.
func averagePointSpacing(values []interface{}) float64 {
	if len(values) < 2 {
		return 0
	}
	first, firstKnown := sampleTimestamp(values[0])
	last, lastKnown := sampleTimestamp(values[len(values)-1])
	if !firstKnown || !lastKnown {
		return 0
	}
	return (last - first) / float64(len(values)-1)
}

// printRangeDownsamplingNotice says when a series came back with fewer points
// than the metrics source produced. Nothing else does: a platform that
// predates the effective step reports truncated: false for it, and the table
// and the JSON look exactly like a complete answer at the requested --step.
// That once passed for a real baseline in an incident review (PLA-928).
func printRangeDownsamplingNotice(out io.Writer, result *client.PrometheusQueryResult) {
	downsampled := 0
	returnedPoints, producedPoints := 0, 0
	spacingSeconds := float64(result.EffectiveStepSeconds)
	for _, rawSeries := range result.Result {
		series, isSeries := rawSeries.(map[string]interface{})
		if !isSeries {
			continue
		}
		values, _ := series["values"].([]interface{})
		original, known := originalPointCount(series)
		if !known || original <= len(values) {
			continue
		}
		downsampled++
		if original > producedPoints {
			returnedPoints, producedPoints = len(values), original
			if result.EffectiveStepSeconds == 0 {
				spacingSeconds = averagePointSpacing(values)
			}
		}
	}
	if downsampled == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nDownsampled: %d of %d series came back with fewer points than the query produced (%d of %d).",
		downsampled, len(result.Result), returnedPoints, producedPoints)
	if spacingSeconds > 0 {
		spacing := time.Duration(spacingSeconds * float64(time.Second)).Round(time.Second)
		_, _ = fmt.Fprintf(out, " The returned points are about %s apart, whatever --step asked for.", spacing)
	}
	_, _ = fmt.Fprintln(out, " A spike between two points is not in this answer: query a shorter window to see them all.")
}

func formatPrometheusMetric(series map[string]interface{}) string {
	metric, ok := series["metric"].(map[string]interface{})
	if !ok {
		return ""
	}
	name := ""
	labels := make([]string, 0, len(metric))
	for key, value := range metric {
		if key == "__name__" {
			name = fmt.Sprintf("%v", value)
			continue
		}
		labels = append(labels, fmt.Sprintf("%s=%q", key, fmt.Sprintf("%v", value)))
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		return name
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(labels, ", "))
}

func prometheusInstantValue(series map[string]interface{}) string {
	value, ok := series["value"].([]interface{})
	if !ok || len(value) < 2 {
		return ""
	}
	return fmt.Sprintf("%v", value[1])
}

func prometheusRangeSummary(series map[string]interface{}) (string, int) {
	values, ok := series["values"].([]interface{})
	if !ok || len(values) == 0 {
		return "", 0
	}
	latestValue := ""
	if last, ok := values[len(values)-1].([]interface{}); ok && len(last) >= 2 {
		latestValue = fmt.Sprintf("%v", last[1])
	}
	return latestValue, len(values)
}

func init() {
	clusterMetricsQueryCmd.Flags().Int("timeout", 0, "Query timeout in seconds (default server-side)")
	clusterMetricsQueryCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	clusterMetricsQueryRangeCmd.Flags().String("range", "", "Duration relative to now (e.g. 15m, 1h, 6h, 24h, 7d)")
	clusterMetricsQueryRangeCmd.Flags().String("start", "", "Range start as Unix seconds (use with --end)")
	clusterMetricsQueryRangeCmd.Flags().String("end", "", "Range end as Unix seconds (use with --start)")
	clusterMetricsQueryRangeCmd.Flags().String("step", "", "Resolution step (e.g. 30s, 1m, 5m)")
	clusterMetricsQueryRangeCmd.Flags().Int("timeout", 0, "Query timeout in seconds (default server-side)")
	clusterMetricsQueryRangeCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	clusterMetricsCmd.PersistentFlags().String("cluster", "", "Cluster name (defaults to the selected cluster)")
	clusterMetricsCmd.AddCommand(clusterMetricsQueryCmd)
	clusterMetricsCmd.AddCommand(clusterMetricsQueryRangeCmd)
	clusterCmd.AddCommand(clusterMetricsCmd)
}
