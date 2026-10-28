package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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
			t.AppendRow(table.Row{metricLabel, pointCount, latestValue})
		} else {
			t.AppendRow(table.Row{metricLabel, prometheusInstantValue(obj)})
		}
	}
	t.Render()

	if result.Truncated {
		fmt.Fprintf(os.Stderr, "\nShowing %d of %d series (truncated). Narrow the query to see the rest.\n",
			len(result.Result), result.TotalSeries)
	}
	return nil
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
