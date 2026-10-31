package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// decodeFleetWire decodes a fleet summary document the way the client does,
// so the tests read the wire shape rather than a hand-built struct.
func decodeFleetWire(t *testing.T, document string) *client.FleetCloudCost {
	t.Helper()
	summary := &client.FleetCloudCost{}
	if decodeError := json.Unmarshal([]byte(document), summary); decodeError != nil {
		t.Fatalf("decoding the fleet document: %v", decodeError)
	}
	return summary
}

// fleetWithUnmeteredSpend is the live shape the cost review found
// (ankra-bn86k4.8.9): the month to date carries what deleted and stalled
// clusters spent, and the provider row does not.
const fleetWithUnmeteredSpend = `{
  "currency": "eur", "cluster_count": 2,
  "monthly_cost_estimate_cents": 600000, "month_to_date_cents": 578592, "projected_month_end_cents": 586823,
  "by_provider": [{"provider": "hetzner", "cluster_count": 2, "monthly_cost_estimate_cents": 600000,
                   "month_to_date_cents": 544976, "projected_month_end_cents": 553207}],
  "top_clusters": [],
  "unmetered_clusters": [
    {"cluster_id": "a1", "cluster_name": "gone-a", "provider": "hetzner", "month_to_date_cents": 20000,
     "is_deleted": true, "is_moved_out": false, "last_snapshot_at": "2026-10-20T09:00:00Z"},
    {"cluster_id": "b2", "cluster_name": "stalled-b", "provider": "ovh", "month_to_date_cents": 10000,
     "is_deleted": false, "is_moved_out": false, "last_snapshot_at": "2026-10-27T11:00:00Z"},
    {"cluster_id": "c3", "cluster_name": "gone-c", "provider": null, "month_to_date_cents": 3616,
     "is_deleted": true, "is_moved_out": false, "last_snapshot_at": null}
  ],
  "unmetered_month_to_date_cents": 33616,
  "generated_at": "2026-10-31T13:00:00Z"
}`

// TestCostSummaryNamesTheSpendOfDeletedAndStalledClusters pins the gap
// line: the provider rows add up to less than the month to date, and the
// summary now says by how much, from how many clusters and why, says the
// month end is a floor while a stopped cluster is listed, dates the figures,
// and lists the clusters.
func TestCostSummaryNamesTheSpendOfDeletedAndStalledClusters(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{fleet: decodeFleetWire(t, fleetWithUnmeteredSpend)}, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cloud cost (EUR): €5868.23 projected month end · €5785.92 month to date · €6000.00/mo run rate",
		"2 clusters priced across 1 provider(s) · as of 2026-10-31T13:00:00Z",
		"€336.16 of the month to date is from 3 clusters that were deleted or stopped reporting this month (2 deleted, 1 stopped reporting); no provider row includes it.",
		"What the 1 cluster that stopped reporting cost since it was last metered is not in these figures, so the projected month end is a floor.",
		"Deleted or stopped reporting this month",
		"gone-a", "stalled-b", "stopped reporting", "OVHcloud", "€200.00", "2026-10-27T11:00:00Z",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "No priced clusters yet") {
		t.Fatalf("a rollup with metered clusters is never 'nothing priced':\n%s", output)
	}
}

// TestCostSummaryOfAScopeWhoseOnlyClusterWasDeletedShowsWhatItSpent pins
// the other half of ankra-bn86k4.8.9: no cluster is metered, but the scope
// spent money this month, so it is not "No priced clusters yet."
func TestCostSummaryOfAScopeWhoseOnlyClusterWasDeletedShowsWhatItSpent(t *testing.T) {
	summary := decodeFleetWire(t, `{
  "currency": "usd", "cluster_count": 0, "monthly_cost_estimate_cents": 0,
  "month_to_date_cents": 33616, "projected_month_end_cents": 33616,
  "by_provider": [], "top_clusters": [],
  "unmetered_clusters": [{"cluster_id": "d4", "cluster_name": "only-one", "provider": "hetzner",
    "month_to_date_cents": 33616, "is_deleted": true, "is_moved_out": false, "last_snapshot_at": "2026-10-03T08:00:00Z"}],
  "unmetered_month_to_date_cents": 33616, "generated_at": null,
  "scope": {"project_id": "p1", "project_name": "Ended", "all_projects": false, "partial": false}
}`)
	for name, render := range map[string]func(*bytes.Buffer){
		"summary":              func(out *bytes.Buffer) { renderFleetCloudCost(out, summary) },
		"summary --by project": func(out *bytes.Buffer) { renderFleetCloudCostBreakdown(out, summary, fleetCostByProject) },
	} {
		var out bytes.Buffer
		render(&out)
		output := out.String()
		if strings.Contains(output, "No priced clusters yet") || !strings.Contains(output, "$336.16 month to date") ||
			!strings.Contains(output, "No cluster is metered now") ||
			!strings.Contains(output, "$336.16 of the month to date is from 1 cluster that was deleted this month; no provider row includes it.") ||
			!strings.Contains(output, "Scope: project Ended") || strings.Contains(output, "as of") {
			t.Fatalf("%s: a scope that spent this month must show what it spent:\n%s", name, output)
		}
	}
}

// TestCostSummaryFromAPlatformWithoutUnmeteredFieldsSaysNothingOfThem pins
// the older platform: a document with no unmetered_clusters or generated_at
// renders as before, with no gap line, no as-of and no list, rather than a
// claim that nothing was deleted.
func TestCostSummaryFromAPlatformWithoutUnmeteredFieldsSaysNothingOfThem(t *testing.T) {
	summary := decodeFleetWire(t, `{"currency": "eur", "cluster_count": 1, "monthly_cost_estimate_cents": 7300,
  "month_to_date_cents": 4000, "projected_month_end_cents": 7300,
  "by_provider": [{"provider": "hetzner", "cluster_count": 1, "monthly_cost_estimate_cents": 7300,
                   "month_to_date_cents": 4000, "projected_month_end_cents": 7300}], "top_clusters": []}`)
	if summary.UnmeteredClusters != nil || summary.UnmeteredMonthToDateCents != nil || summary.GeneratedAt != nil {
		t.Fatalf("absent fields must decode as absent, got %+v", summary)
	}
	var out bytes.Buffer
	renderFleetCloudCost(&out, summary)
	output := out.String()
	if !strings.Contains(output, "1 cluster priced across 1 provider(s)\n") || strings.Contains(output, "of the month to date is from") ||
		strings.Contains(output, "as of") || strings.Contains(output, "Deleted or stopped reporting") || strings.Contains(output, "floor") {
		t.Fatalf("an older platform's summary must render as before:\n%s", output)
	}
}

// TestCostSummaryListsTheCostliestUnmeteredClustersAndSaysHowManyMore pins
// the list's cap: ten rows, and the rest counted rather than dropped.
func TestCostSummaryListsTheCostliestUnmeteredClustersAndSaysHowManyMore(t *testing.T) {
	summary := &client.FleetCloudCost{Currency: "eur", ClusterCount: 1, MonthToDateCents: 1_200}
	for index := 0; index < fleetCostUnmeteredRows+2; index++ {
		summary.UnmeteredClusters = append(summary.UnmeteredClusters, client.FleetUnmeteredCluster{
			ClusterID: fmt.Sprintf("id-%02d", index), ClusterName: fmt.Sprintf("gone-%02d", index), MonthToDateCents: 100, IsDeleted: true})
	}
	var out bytes.Buffer
	renderFleetCloudCost(&out, summary)
	output := out.String()
	if !strings.Contains(output, "gone-09") || strings.Contains(output, "gone-10") || !strings.Contains(output, "… and 2 more (-o json lists every one)") ||
		!strings.Contains(output, "€12.00 of the month to date is from 12 clusters that were deleted this month; no provider row includes it.") {
		t.Fatalf("a long list is capped and says how many more:\n%s", output)
	}
}

// TestCostSummaryNamesAMovedOutClusterWithoutCallingTheMonthAFloor pins a
// project read: a cluster that moved to another project after spending here
// is named, and its spend is final, so it alone does not make the month end
// a floor.
func TestCostSummaryNamesAMovedOutClusterWithoutCallingTheMonthAFloor(t *testing.T) {
	spent := int64(500)
	summary := &client.FleetCloudCost{Currency: "eur", ClusterCount: 1, MonthToDateCents: 1_500,
		UnmeteredClusters: []client.FleetUnmeteredCluster{
			{ClusterID: "e5", ClusterName: "moved-away", MonthToDateCents: 500, IsMovedOut: true},
		},
		UnmeteredMonthToDateCents: &spent}
	lines := strings.Join(fleetCostUnmeteredLines(summary), "\n")
	if lines != "€5.00 of the month to date is from 1 cluster that moved to another project this month; no provider row includes it." {
		t.Fatalf("lines = %q", lines)
	}
}

// TestCostSummaryListsTheCostliestUnmeteredClustersWhateverThePlatformOrder
// pins the CLI's own ranking (review finding 4432161782): an older platform
// makes no ordering promise, so clusters that arrive cheapest first are still
// listed costliest first, and the row cap hides the cheapest, never the
// biggest spend.
func TestCostSummaryListsTheCostliestUnmeteredClustersWhateverThePlatformOrder(t *testing.T) {
	summary := &client.FleetCloudCost{Currency: "eur", ClusterCount: 1}
	for index := 0; index < fleetCostUnmeteredRows+2; index++ {
		summary.UnmeteredClusters = append(summary.UnmeteredClusters, client.FleetUnmeteredCluster{
			ClusterID: fmt.Sprintf("id-%02d", index), ClusterName: fmt.Sprintf("gone-%02d", index),
			MonthToDateCents: int64(index+1) * 100, IsDeleted: true})
		summary.MonthToDateCents += int64(index+1) * 100
	}
	var out bytes.Buffer
	renderFleetCostUnmetered(&out, summary)
	output := out.String()
	costliest, runnerUp := strings.Index(output, "gone-11"), strings.Index(output, "gone-10")
	if costliest < 0 || runnerUp < 0 || costliest > runnerUp || strings.Contains(output, "gone-00") || strings.Contains(output, "gone-01") ||
		!strings.Contains(output, "… and 2 more (-o json lists every one)") {
		t.Fatalf("the costliest are listed first and the cheapest are the ones hidden:\n%s", output)
	}
}
