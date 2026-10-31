package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

// The hours of the month a month to date holds (ankra-bn86k4.4.8.2.2), and
// the measurement statuses unmeasured_confounded and unmeasured_within_noise
// (ankra-bn86k4.7.2.1).

// shortClusterMonth is one cluster's month two hours short, with three
// copied hours.
func shortClusterMonth() *client.MonthHours {
	return &client.MonthHours{HoursElapsed: 720, HoursMetered: 718, HoursEstimated: 3, IsShort: true, ClusterCount: 1, ShortClusterCount: 1}
}

func TestCostMonthHoursWordsTheCountsAndSaysNothingWhenWholeOrUncounted(t *testing.T) {
	for name, scenario := range map[string]struct {
		hours     *client.MonthHours
		wantLine  string
		wantShort bool
	}{
		"one cluster short": {
			hours:     &client.MonthHours{HoursElapsed: 720, HoursMetered: 718, IsShort: true, ClusterCount: 1, ShortClusterCount: 1},
			wantLine:  "Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor.",
			wantShort: true,
		},
		"several clusters, cluster hours": {
			hours:     &client.MonthHours{HoursElapsed: 1440, HoursMetered: 1439, IsShort: true, ClusterCount: 2, ShortClusterCount: 1},
			wantLine:  "Month so far covers 1,439 of 1,440 cluster hours; 1 hour was not metered across 1 cluster, so the figure is a floor.",
			wantShort: true,
		},
		"short with copied hours": {
			hours: shortClusterMonth(),
			wantLine: "Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor. " +
				"3 of the covered hours are estimates: no metering ran then, so the next reading was copied.",
			wantShort: true,
		},
		"whole with one copied hour": {
			hours:    &client.MonthHours{HoursElapsed: 720, HoursMetered: 720, HoursEstimated: 1, ClusterCount: 1},
			wantLine: "Month so far covers all 720 hours. 1 of the covered hours is an estimate: no metering ran then, so the next reading was copied.",
		},
		"whole":     {hours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 720, ClusterCount: 1}},
		"uncounted": {hours: nil},
		"nothing elapsed": {
			hours: &client.MonthHours{HoursElapsed: 0, HoursMetered: 0, ClusterCount: 1},
		},
		// The counts decide: a flag that contradicts them never names "0
		// hours" missing.
		"a short flag over whole counts": {
			hours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 720, IsShort: true, ClusterCount: 1, ShortClusterCount: 1},
		},
	} {
		line, short := costMonthHours(scenario.hours)
		if line != scenario.wantLine || short != scenario.wantShort {
			t.Fatalf("%s: got (%q, %v), want (%q, %v)", name, line, short, scenario.wantLine, scenario.wantShort)
		}
	}
}

func TestCostCountGroupsThousands(t *testing.T) {
	for count, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 21600: "21,600", 1234567: "1,234,567", -1440: "-1,440"} {
		if got := costCount(count); got != want {
			t.Fatalf("costCount(%d) = %q, want %q", count, got, want)
		}
	}
}

func clusterCostWithMonth(hours *client.MonthHours) *client.ClusterCost {
	return &client.ClusterCost{
		HasData: true,
		Summary: &client.ClusterCostSummary{
			Provider: "hetzner", Currency: "eur", TotalNodeCount: 4, PricedNodeCount: 4,
			HourlyCostCents: 115, MonthlyCostEstimateCents: 84200, MonthToDateCents: 41000, ProjectedMonthEndCents: 86000,
			ConfidenceLevel: "high", SnapshotAt: "2026-10-30T14:00:00", MonthHours: hours,
		},
		Trend:      []client.CostTrendPoint{},
		Namespaces: []client.NamespaceCost{},
	}
}

func TestCostClusterSaysAShortMonthIsAFloor(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{clusterCost: clusterCostWithMonth(shortClusterMonth())}, "cost", "cluster", costClusterID)
	if executeError != nil {
		t.Fatalf("cost cluster failed: %v", executeError)
	}
	for _, expected := range []string{
		"at least €860.00 projected month end · at least €410.00 month to date · €842.00/mo run rate",
		"  Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor. 3 of the covered hours are estimates",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostClusterSaysNothingOfHoursWhenWholeOrUncounted(t *testing.T) {
	for name, hours := range map[string]*client.MonthHours{
		"uncounted": nil,
		"whole":     {HoursElapsed: 720, HoursMetered: 720, ClusterCount: 1},
	} {
		output, executeError := runCostCommand(t, &costMock{clusterCost: clusterCostWithMonth(hours)}, "cost", "cluster", costClusterID)
		if executeError != nil {
			t.Fatalf("%s: cost cluster failed: %v", name, executeError)
		}
		if strings.Contains(output, "at least") || strings.Contains(output, "Month so far") {
			t.Fatalf("%s: a month that is whole or was not counted gets no hours line:\n%s", name, output)
		}
		if !strings.Contains(output, "€860.00 projected month end · €410.00 month to date") {
			t.Fatalf("%s: the figures read as before:\n%s", name, output)
		}
	}
}

func TestCostClusterStructuredOutputCarriesMonthHoursOnlyWhenSent(t *testing.T) {
	for name, scenario := range map[string]struct {
		hours *client.MonthHours
		want  bool
	}{
		"counted":   {hours: shortClusterMonth(), want: true},
		"uncounted": {hours: nil, want: false},
	} {
		output, executeError := runCostCommand(t, &costMock{clusterCost: clusterCostWithMonth(scenario.hours)}, "cost", "cluster", costClusterID, "-o", "json")
		if executeError != nil {
			t.Fatalf("%s: cost cluster -o json failed: %v", name, executeError)
		}
		var decoded struct {
			Summary map[string]any `json:"summary"`
		}
		if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
			t.Fatalf("%s: output is not JSON: %v\n%s", name, unmarshalError, output)
		}
		hours, present := decoded.Summary["month_hours"].(map[string]any)
		if present != scenario.want {
			t.Fatalf("%s: month_hours present %v, want %v:\n%s", name, present, scenario.want, output)
		}
		if present && (hours["hours_elapsed"] != float64(720) || hours["hours_metered"] != float64(718) || hours["is_short"] != true) {
			t.Fatalf("%s: month_hours lost its counts: %+v", name, hours)
		}
	}
}

func TestCostSummarySaysAShortMonthIsAFloorAndMarksShortClusters(t *testing.T) {
	fleet := fleetCostFixture()
	fleet.MonthHours = &client.MonthHours{HoursElapsed: 2160, HoursMetered: 2156, IsShort: true, ClusterCount: 3, ShortClusterCount: 2}
	fleet.TopClusters[0].MonthHours = shortClusterMonth()
	fleet.TopClusters = append(fleet.TopClusters, client.FleetClusterCost{ClusterID: "22222222-2222-4222-8222-222222222222",
		ClusterName: "dev-eu", Provider: "hetzner", MonthToDateCents: 9000, ProjectedMonthEndCents: 18000, ConfidenceLevel: "high",
		MonthHours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 720, ClusterCount: 1}})
	output, executeError := runCostCommand(t, &costMock{fleet: fleet}, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cloud cost (EUR): at least €860.00 projected month end · at least €410.00 month to date · €842.00/mo run rate",
		"  Month so far covers 2,156 of 2,160 cluster hours; 4 hours were not metered across 2 clusters, so the figure is a floor.",
		"≥ €430.00", "≥ €200.00",
		"≥ marks a cluster whose month so far is short of metered hours, so its figures are floors",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	// The whole cluster's figures carry no mark.
	if strings.Contains(output, "≥ €180.00") || strings.Contains(output, "≥ €90.00") {
		t.Fatalf("a cluster whose month is whole must not be marked:\n%s", output)
	}
}

func TestCostSummaryFromAnOlderPlatformSaysNothingOfHours(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{fleet: fleetCostFixture()}, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary failed: %v", executeError)
	}
	for _, absent := range []string{"at least", "Month so far", "≥"} {
		if strings.Contains(output, absent) {
			t.Fatalf("a platform that did not count the hours gets no %q:\n%s", absent, output)
		}
	}
}

func TestCostBudgetsListSaysAShortMonthIsAFloor(t *testing.T) {
	budgets := costBudgetsFixture()
	budgets.Budgets[0].Projection.MonthHours = shortClusterMonth()
	// A month whose status is unknown has no figure to qualify.
	budgets.Budgets[2].Projection.MonthHours = &client.MonthHours{HoursElapsed: 1440, HoursMetered: 1000, IsShort: true, ClusterCount: 2, ShortClusterCount: 2}
	output, executeError := runCostBudgetsCommand(t, &costBudgetsMock{budgets: budgets}, "", "cost", "budgets", "list")
	if executeError != nil {
		t.Fatalf("cost budgets list failed: %v", executeError)
	}
	line := budgetRowLine(t, output, "prod-eu ")
	for _, cell := range []string{"≥ €1200.00", "≥ €1620.00 (108%)"} {
		if !strings.Contains(line, cell) {
			t.Fatalf("row prod-eu lacks %q: %s", cell, line)
		}
	}
	flat := flattenBudgetNotes(output)
	if !strings.Contains(flat, "↳ Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor.") {
		t.Fatalf("the short budget lacks its hours note:\n%s", output)
	}
	if strings.Count(output, "Month so far") != 1 || strings.Contains(flat, "1,000 of 1,440") {
		t.Fatalf("only the short, known budget gets an hours note:\n%s", output)
	}
	if fleetLine := budgetRowLine(t, output, "Fleet "); strings.Contains(fleetLine, "≥") {
		t.Fatalf("a budget sent without month_hours must not be marked: %s", fleetLine)
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if width := text.StringWidthWithoutEscSequences(line); width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
	}
}

func TestCostLedgerAndDecisionsNameEveryUnmeasuredStatus(t *testing.T) {
	for status, want := range map[string]string{
		"unmeasured_confounded":   "unmeasured · another change overlapped",
		"unmeasured_within_noise": "unmeasured · within normal day-to-day swings",
	} {
		if got := costLedgerStatus(client.CloudLedgerRow{Status: "succeeded", MeasurementStatus: status}); got != want {
			t.Fatalf("ledger %s = %q, want %q", status, got, want)
		}
	}
	ledger := cloudLedgerFixture()
	ledger.Rows[1].MeasurementStatus = "unmeasured_within_noise"
	ledger.Rows[2].MeasurementStatus = "unmeasured_confounded"
	output, executeError := runCostCommand(t, &costMock{ledger: ledger}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	flat := flattenBudgetNotes(output)
	for _, expected := range []string{"unmeasured · within normal", "day-to-day swings", "unmeasured · another change", "overlapped"} {
		if !strings.Contains(flat, expected) {
			t.Fatalf("the ledger lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "unmeasured within noise") || strings.Contains(output, "unmeasured confounded") {
		t.Fatalf("a known status must never print as its raw words:\n%s", output)
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if width := text.StringWidthWithoutEscSequences(line); width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
	}

	for status, want := range map[string]string{
		"unmeasured_confounded":   "unmeasured: another cost change ran on the same cluster in the same window, so the drop is not this one's alone",
		"unmeasured_within_noise": "unmeasured: the change in the run rate was no larger than its normal day-to-day swings",
	} {
		if got := costDecisionMeasurement(status); got != want {
			t.Fatalf("decision %s = %q, want %q", status, got, want)
		}
	}
}
