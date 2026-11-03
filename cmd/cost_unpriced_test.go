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

// fleetNotToldUnpriced opens the line a summary prints when the platform did
// not send unpriced_cluster_count.
const fleetNotToldUnpriced = "The platform did not say how many clusters in scope it could not price"

// TestCostSummaryTellsAMissingUnpricedCountFromZero pins ankra-bn86k4.8.25:
// a platform that does not send the count (absent) and one that counts none
// (0) are told apart. Absent says the platform did not say, rather than
// printing nothing as if every cluster were priced; 0 prints no line. Neither
// prints a count, "0 clusters", or calls the figures floors.
func TestCostSummaryTellsAMissingUnpricedCountFromZero(t *testing.T) {
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
			t.Fatalf("%s: the summary must not count unpriced clusters it was not told of:\n%s", name, output)
		}
		if told := strings.Contains(output, fleetNotToldUnpriced); told != (name == "absent") {
			t.Fatalf("%s: says the platform did not report the count %v, want %v:\n%s", name, told, name == "absent", output)
		}
	}
}

// TestCostSummarySaysWhichQualifyingReadingsThePlatformDidNotSend pins the
// one line that names what the platform left out (ankra-bn86k4.8.25):
// the unpriced count, the month's hours, both, or neither. A reading that
// was sent is never named as missing, whatever its value.
func TestCostSummarySaysWhichQualifyingReadingsThePlatformDidNotSend(t *testing.T) {
	const wholeMonth = `, "month_hours": {"hours_elapsed": 24, "hours_metered": 24, "cluster_count": 1}`
	const hoursNotTold = "the month so far was metered"
	for name, scenario := range map[string]struct {
		document              string
		unpricedNamed, hours  bool
		wantWholeLineContains string
	}{
		"neither sent": {fleetPricedOneCluster + `}`, true, true,
			"  " + fleetNotToldUnpriced + ", or whether every hour of the month so far was metered, so these figures may be short of what was spent.\n"},
		"only hours sent": {fleetPricedOneCluster + wholeMonth + `}`, true, false,
			"  " + fleetNotToldUnpriced + ", so these figures may leave some out.\n"},
		"only count sent": {fleetPricedOneCluster + `, "unpriced_cluster_count": 0}`, false, true,
			"  The platform did not say whether every hour of the month so far was metered, so the month to date may be short of what was spent.\n"},
		"both sent": {fleetPricedOneCluster + wholeMonth + `, "unpriced_cluster_count": 0}`, false, false, ""},
	} {
		output := renderFleetSummary(t, scenario.document)
		if strings.Contains(output, fleetNotToldUnpriced) != scenario.unpricedNamed || strings.Contains(output, hoursNotTold) != scenario.hours {
			t.Fatalf("%s: named the count %v (want %v) and the hours %v (want %v):\n%s", name,
				strings.Contains(output, fleetNotToldUnpriced), scenario.unpricedNamed, strings.Contains(output, hoursNotTold), scenario.hours, output)
		}
		if scenario.wantWholeLineContains != "" && !strings.Contains(output, scenario.wantWholeLineContains) {
			t.Fatalf("%s: output lacks %q:\n%s", name, scenario.wantWholeLineContains, output)
		}
		if strings.Contains(output, "The platform did not say") && scenario.wantWholeLineContains == "" {
			t.Fatalf("%s: every reading was sent, so nothing is missing:\n%s", name, output)
		}
	}
}

// TestCostSummaryWithNothingPricedSaysTheUnpricedCountWasNotSent pins the
// empty answer from a platform that predates the count: it is still "No
// priced clusters yet.", the count is named as not reported, and with no
// figure shown the month's hours are not spoken of.
func TestCostSummaryWithNothingPricedSaysTheUnpricedCountWasNotSent(t *testing.T) {
	output := renderFleetSummary(t, `{"currency": "eur", "cluster_count": 0, "monthly_cost_estimate_cents": 0,
  "month_to_date_cents": 0, "projected_month_end_cents": 0, "by_provider": [], "top_clusters": [],
  "unmetered_clusters": [], "unmetered_month_to_date_cents": 0}`)
	if !strings.Contains(output, "No priced clusters yet.") || !strings.Contains(output, fleetNotToldUnpriced+".\n") ||
		strings.Contains(output, "hour") || strings.Contains(output, "floor") {
		t.Fatalf("nothing priced from a platform without the count must say only that the count was not sent:\n%s", output)
	}
}
