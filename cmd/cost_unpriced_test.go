package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// fleetPricedOneCluster is a summary with one priced cluster; each test
// appends the unpriced count it needs (or none) before the closing brace.
const fleetPricedOneCluster = `{"currency": "eur", "cluster_count": 1, "monthly_cost_estimate_cents": 7300,
  "month_to_date_cents": 4000, "projected_month_end_cents": 7300,
  "by_provider": [{"provider": "hetzner", "cluster_count": 1, "monthly_cost_estimate_cents": 7300,
                   "month_to_date_cents": 4000, "projected_month_end_cents": 7300}], "top_clusters": []`

// renderFleetSummary renders `cost summary` for one fleet document.
func renderFleetSummary(t *testing.T, document string) string {
	t.Helper()
	var out bytes.Buffer
	renderFleetCloudCost(&out, decodeFleetWire(t, document))
	return out.String()
}

// TestCostSummaryNamesTheUnpricedClustersAndCallsTheFiguresFloors pins
// ankra-bn86k4.8.18: the platform counts the clusters in scope it has no
// price for, and the summary says how many and that the run rate and the
// projected month end leave them out, so the totals never read as the whole
// fleet's.
func TestCostSummaryNamesTheUnpricedClustersAndCallsTheFiguresFloors(t *testing.T) {
	output := renderFleetSummary(t, fleetPricedOneCluster+`, "unpriced_cluster_count": 2}`)
	want := "  2 clusters in scope have no price from the last day, so what they cost is unknown, not zero: " +
		"the run rate and the projected month end leave them out and are floors.\n"
	if !strings.Contains(output, "1 cluster priced across 1 provider(s)") || !strings.Contains(output, want) {
		t.Fatalf("output lacks the unpriced line %q:\n%s", want, output)
	}
}

// TestCostSummaryWithNothingPricedStillNamesTheUnpricedClusters pins the
// empty answer: no cluster is priced and nothing was spent, but a cluster
// in scope has no price. It is still "No priced clusters yet.", and the
// cluster is named as unknown rather than left to read as free. No figure
// is shown, so the line does not speak of floors.
func TestCostSummaryWithNothingPricedStillNamesTheUnpricedClusters(t *testing.T) {
	output := renderFleetSummary(t, `{"currency": "eur", "cluster_count": 0, "monthly_cost_estimate_cents": 0,
  "month_to_date_cents": 0, "projected_month_end_cents": 0, "by_provider": [], "top_clusters": [],
  "unmetered_clusters": [], "unmetered_month_to_date_cents": 0, "unpriced_cluster_count": 1}`)
	if !strings.Contains(output, "No priced clusters yet.") ||
		!strings.Contains(output, "1 cluster in scope has no price from the last day, so what it costs is unknown, not zero.\n") ||
		strings.Contains(output, "floor") {
		t.Fatalf("an unpriced cluster with nothing priced must be named as unknown:\n%s", output)
	}
}

// TestCostSummarySaysNothingOfUnpricedClustersItWasNotTold pins the
// tolerance: a platform that does not send the count (absent) and one that
// counts none (0) both print no line, never "0 clusters" and never a claim
// that every cluster is priced.
func TestCostSummarySaysNothingOfUnpricedClustersItWasNotTold(t *testing.T) {
	for name, document := range map[string]string{
		"absent": fleetPricedOneCluster + `}`,
		"zero":   fleetPricedOneCluster + `, "unpriced_cluster_count": 0}`,
	} {
		summary := decodeFleetWire(t, document)
		if name == "absent" && summary.UnpricedClusterCount != nil {
			t.Fatalf("an absent count must decode as absent, got %d", *summary.UnpricedClusterCount)
		}
		output := renderFleetSummary(t, document)
		if strings.Contains(output, "no price") || strings.Contains(output, "unpriced") || strings.Contains(output, "0 clusters") ||
			strings.Contains(output, "floor") {
			t.Fatalf("%s: the summary must say nothing of unpriced clusters:\n%s", name, output)
		}
	}
}
