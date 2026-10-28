package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func TestFormatPrometheusMetric(t *testing.T) {
	tests := []struct {
		name   string
		series map[string]interface{}
		want   string
	}{
		{
			name:   "name with sorted labels",
			series: map[string]interface{}{"metric": map[string]interface{}{"__name__": "up", "job": "api", "instance": "a"}},
			want:   `up{instance="a", job="api"}`,
		},
		{
			name:   "labels only without name",
			series: map[string]interface{}{"metric": map[string]interface{}{"pod": "web"}},
			want:   `{pod="web"}`,
		},
		{
			name:   "name only",
			series: map[string]interface{}{"metric": map[string]interface{}{"__name__": "up"}},
			want:   "up",
		},
		{
			name:   "missing metric",
			series: map[string]interface{}{},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPrometheusMetric(tt.series); got != tt.want {
				t.Errorf("formatPrometheusMetric() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrometheusInstantValue(t *testing.T) {
	series := map[string]interface{}{"value": []interface{}{1234567890.0, "42"}}
	if got := prometheusInstantValue(series); got != "42" {
		t.Errorf("prometheusInstantValue() = %q, want 42", got)
	}
	if got := prometheusInstantValue(map[string]interface{}{}); got != "" {
		t.Errorf("missing value should be empty, got %q", got)
	}
}

func TestPrometheusRangeSummary(t *testing.T) {
	series := map[string]interface{}{
		"values": []interface{}{
			[]interface{}{1.0, "10"},
			[]interface{}{2.0, "20"},
			[]interface{}{3.0, "30"},
		},
	}
	latest, count := prometheusRangeSummary(series)
	if latest != "30" || count != 3 {
		t.Errorf("prometheusRangeSummary() = (%q, %d), want (30, 3)", latest, count)
	}
	latest, count = prometheusRangeSummary(map[string]interface{}{})
	if latest != "" || count != 0 {
		t.Errorf("empty series = (%q, %d), want (\"\", 0)", latest, count)
	}
}

func TestRenderPrometheusResultJSON(t *testing.T) {
	resultType := "vector"
	result := &client.PrometheusQueryResult{
		Status:      "success",
		ResultType:  &resultType,
		Result:      []interface{}{map[string]interface{}{"metric": map[string]interface{}{"__name__": "up"}, "value": []interface{}{0.0, "1"}}},
		TotalSeries: 1,
	}
	output := captureStdout(t, func() {
		if err := renderPrometheusResult(result, "json", false); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	for _, fragment := range []string{`"status": "success"`, `"result_type": "vector"`, `"total_series": 1`} {
		if !strings.Contains(output, fragment) {
			t.Errorf("json output missing %q\n%s", fragment, output)
		}
	}
}

func TestRenderPrometheusResultTable(t *testing.T) {
	result := &client.PrometheusQueryResult{
		Status:      "success",
		Result:      []interface{}{map[string]interface{}{"metric": map[string]interface{}{"__name__": "up", "job": "api"}, "value": []interface{}{0.0, "1"}}},
		TotalSeries: 1,
	}
	output := captureStdout(t, func() {
		if err := renderPrometheusResult(result, "table", false); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	if !strings.Contains(output, "up{job=\"api\"}") {
		t.Errorf("table output missing metric label\n%s", output)
	}
	if !strings.Contains(strings.ToLower(output), "value") {
		t.Errorf("table output missing header\n%s", output)
	}
}

func TestRenderPrometheusResultEmpty(t *testing.T) {
	result := &client.PrometheusQueryResult{Status: "success", Result: []interface{}{}}
	output := captureStdout(t, func() {
		if err := renderPrometheusResult(result, "table", false); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	if !strings.Contains(output, "No series returned.") {
		t.Errorf("expected empty notice, got %q", output)
	}
}

func TestMetricsCommandWiring(t *testing.T) {
	var metricsCmd *cobra.Command
	for _, sub := range clusterCmd.Commands() {
		if sub.Name() == "metrics" {
			metricsCmd = sub
			break
		}
	}
	if metricsCmd == nil {
		t.Fatal("metrics command not registered under cluster")
	}
	names := map[string]bool{}
	for _, sub := range metricsCmd.Commands() {
		names[sub.Name()] = true
	}
	if !names["query"] || !names["query-range"] {
		t.Errorf("expected query and query-range subcommands, got %v", names)
	}
	if metricsCmd.PersistentFlags().Lookup("cluster") == nil {
		t.Error("metrics command should expose a --cluster flag")
	}
}
