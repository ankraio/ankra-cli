package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

type costTrendMock struct {
	baseMock
	trend       *client.FleetCostTrend
	trendError  error
	events      *client.CostEvents
	eventsError error
	trendDays   []int
	eventsDays  []int
}

func (m *costTrendMock) GetFleetCostTrend(days int) (*client.FleetCostTrend, error) {
	m.trendDays = append(m.trendDays, days)
	if m.trendError != nil {
		return nil, m.trendError
	}
	return m.trend, nil
}

func (m *costTrendMock) GetCostEvents(days int) (*client.CostEvents, error) {
	m.eventsDays = append(m.eventsDays, days)
	if m.eventsError != nil {
		return nil, m.eventsError
	}
	return m.events, nil
}

func runCostTrendCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, costTrendCmd, costEventsCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func trendCents(cents int64) *int64 {
	return &cents
}

func trendString(value string) *string {
	return &value
}

// fleetCostTrendFixture is the trend as the platform sends it: a day no
// cluster was priced, a floor day, a coverage change on the current day and a
// deleted cluster whose first priced hour is only the earliest known.
func fleetCostTrendFixture() *client.FleetCostTrend {
	return &client.FleetCostTrend{
		Currency:    "eur",
		GeneratedAt: "2026-09-24T06:00:00Z",
		Days:        4,
		Points: []client.FleetCostTrendPoint{
			{Day: "2026-09-21", PricedClusterCount: 0, FullyPricedClusterCount: 0},
			{Day: "2026-09-22", MonthlyCostEstimateCents: trendCents(70000), PricedClusterCount: 2, FullyPricedClusterCount: 1},
			{Day: "2026-09-23", MonthlyCostEstimateCents: trendCents(69000), PricedClusterCount: 2, FullyPricedClusterCount: 2},
			{Day: "2026-09-24", MonthlyCostEstimateCents: trendCents(84200), PricedClusterCount: 3, FullyPricedClusterCount: 3, Current: true},
		},
		CoverageChanges: []client.CostCoverageChange{
			{Day: "2026-09-22", PricedClusterCountBefore: 0, PricedClusterCountAfter: 2,
				Entered: []client.CostCoverageChangeCluster{
					{ClusterID: "11111111-1111-4111-8111-111111111111", ClusterName: "prod-eu", MonthlyCostEstimateCents: 60000},
					{ClusterID: "33333333-3333-4333-8333-333333333333", ClusterName: "old-dev", MonthlyCostEstimateCents: 10000},
				},
				Left: []client.CostCoverageChangeCluster{}, CoverageDeltaMonthlyCents: 70000},
			{Day: "2026-09-24", PricedClusterCountBefore: 2, PricedClusterCountAfter: 3,
				Entered: []client.CostCoverageChangeCluster{
					{ClusterID: "22222222-2222-4222-8222-222222222222", ClusterName: "data-platform", MonthlyCostEstimateCents: 25000},
				},
				Left: []client.CostCoverageChangeCluster{
					{ClusterID: "33333333-3333-4333-8333-333333333333", ClusterName: "old-dev", MonthlyCostEstimateCents: 9800},
				},
				CoverageDeltaMonthlyCents: 15200},
		},
		Clusters: []client.FleetCostTrendCluster{
			{ClusterID: "11111111-1111-4111-8111-111111111111", ClusterName: "prod-eu", FirstPricedAt: trendString("2026-08-01T10:00:00Z"),
				FirstPricedExact: true, FirstDay: "2026-09-22", LastDay: "2026-09-24", PricedDays: 3},
			{ClusterID: "33333333-3333-4333-8333-333333333333", ClusterName: "old-dev", Deleted: true,
				FirstPricedAt: trendString("2026-08-27T00:00:00Z"), FirstDay: "2026-09-22", LastDay: "2026-09-23", PricedDays: 2},
			{ClusterID: "22222222-2222-4222-8222-222222222222", ClusterName: "data-platform",
				FirstDay: "2026-09-24", LastDay: "2026-09-24", PricedDays: 1},
		},
	}
}

// tableLineContaining returns the rendered table line whose first cell starts
// with marker.
func tableLineContaining(t *testing.T, output string, marker string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "│ "+marker) {
			return line
		}
	}
	t.Fatalf("no table row for %s:\n%s", marker, output)
	return ""
}

func TestCostTrendRendersTheLineCoverageAndClusters(t *testing.T) {
	mock := &costTrendMock{trend: fleetCostTrendFixture()}
	output, executeError := runCostTrendCommand(t, mock, "cost", "trend")
	if executeError != nil {
		t.Fatalf("cost trend failed: %v", executeError)
	}
	for _, expected := range []string{
		"Fleet run rate (EUR), last 4 days · generated 2026-09-24T06:00:00Z",
		"€700.00/mo on 2026-09-22 (2 clusters priced) -> €842.00/mo now (3 clusters priced)",
		"2 coverage changes moved the line by +€852.00/mo in total: clusters entering or leaving pricing, not spend.",
		"A day with fewer fully priced clusters than priced ones is a floor",
		"Run rate per day (UTC):",
		"Coverage changes (clusters entering or leaving pricing; they move the line, not spend):",
		"data-platform (€250.00/mo)", "old-dev (€98.00/mo)", "+€152.00/mo",
		"Clusters priced in the window:",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if line := tableLineContaining(t, output, "2026-09-21"); !strings.Contains(line, "not priced") || strings.Contains(line, "0.00") {
		t.Fatalf("a day no cluster was priced reads as not priced, never zero: %s", line)
	}
	if line := tableLineContaining(t, output, "2026-09-24 (now)"); !strings.Contains(line, "€842.00") {
		t.Fatalf("the current point is marked as now: %s", line)
	}
	if line := tableLineContaining(t, output, "old-dev (deleted)"); !strings.Contains(line, "by 2026-08-27T00:00:00Z") ||
		!strings.Contains(line, "2026-09-22 -> 2026-09-23") {
		t.Fatalf("a first priced hour that is only the earliest known reads as 'by': %s", line)
	}
	if line := tableLineContaining(t, output, "prod-eu "); !strings.Contains(line, "2026-08-01T10:00:00Z") || strings.Contains(line, "by 2026") {
		t.Fatalf("an exact first priced hour is printed as is: %s", line)
	}
	if line := tableLineContaining(t, output, "data-platform "); !strings.Contains(line, "unknown") {
		t.Fatalf("an unreadable first priced hour is unknown: %s", line)
	}
	if strings.Contains(output, "€0.00") {
		t.Fatalf("no figure in this trend is zero, so none may print as €0.00:\n%s", output)
	}
	if len(mock.trendDays) != 1 || mock.trendDays[0] != 0 {
		t.Fatalf("an omitted --days leaves the window to the platform, sent %v", mock.trendDays)
	}
}

func TestCostTrendPassesDaysThrough(t *testing.T) {
	mock := &costTrendMock{trend: fleetCostTrendFixture()}
	if _, executeError := runCostTrendCommand(t, mock, "cost", "trend", "--days", "7"); executeError != nil {
		t.Fatalf("cost trend --days 7 failed: %v", executeError)
	}
	if len(mock.trendDays) != 1 || mock.trendDays[0] != 7 {
		t.Fatalf("--days 7 was not sent, sent %v", mock.trendDays)
	}
	_, executeError := runCostTrendCommand(t, &costTrendMock{trend: fleetCostTrendFixture()}, "cost", "trend", "--days", "0")
	if executeError == nil || exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "--days must be at least 1") {
		t.Fatalf("--days 0 is a usage error, got %v", executeError)
	}
}

func TestCostTrendWithNothingPricedSaysSoInsteadOfZero(t *testing.T) {
	empty := &client.FleetCostTrend{Currency: "usd", Days: 30, Points: []client.FleetCostTrendPoint{
		{Day: "2026-09-23"}, {Day: "2026-09-24", Current: true},
	}, CoverageChanges: []client.CostCoverageChange{}, Clusters: []client.FleetCostTrendCluster{}}
	output, executeError := runCostTrendCommand(t, &costTrendMock{trend: empty}, "cost", "trend")
	if executeError != nil {
		t.Fatalf("cost trend failed: %v", executeError)
	}
	if !strings.HasPrefix(output, "No cluster was priced on any day of the last 30 days, so there is no run rate to follow.\n") {
		t.Fatalf("a trend with nothing priced must lead with one clear sentence:\n%s", output)
	}
	if strings.Contains(output, "$0.00") || strings.Contains(output, "╭") {
		t.Fatalf("a trend with nothing priced must not render zeros or an empty table:\n%s", output)
	}
}

func TestCostTrendWithoutCoverageChangesSaysTheSetHeld(t *testing.T) {
	trend := fleetCostTrendFixture()
	trend.Points = trend.Points[2:]
	trend.CoverageChanges = []client.CostCoverageChange{}
	output, executeError := runCostTrendCommand(t, &costTrendMock{trend: trend}, "cost", "trend")
	if executeError != nil {
		t.Fatalf("cost trend failed: %v", executeError)
	}
	if !strings.Contains(output, "No coverage change: the same clusters were priced on every day of the window.") ||
		strings.Contains(output, "Coverage changes (") || strings.Contains(output, "is a floor") {
		t.Fatalf("a window without coverage changes or floors says so and lists none:\n%s", output)
	}
}

func TestCostTrendStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostTrendCommand(t, &costTrendMock{trend: fleetCostTrendFixture()}, "cost", "trend", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost trend -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["currency"] != "eur" || decoded["days"] != float64(4) || decoded["generated_at"] != "2026-09-24T06:00:00Z" {
		t.Fatalf("structured document lacks the wire fields: %+v", decoded)
	}
	points, _ := decoded["points"].([]any)
	if len(points) != 4 {
		t.Fatalf("points missing: %+v", decoded["points"])
	}
	unpriced := points[0].(map[string]any)
	if value, present := unpriced["monthly_cost_estimate_cents"]; !present || value != nil {
		t.Fatalf("a day no cluster was priced must stay null on the wire, not vanish or become 0: %+v", unpriced)
	}
	if points[3].(map[string]any)["current"] != true {
		t.Fatalf("the current flag must pass through: %+v", points[3])
	}
	changes, _ := decoded["coverage_changes"].([]any)
	if len(changes) != 2 || changes[1].(map[string]any)["coverage_delta_monthly_cents"] != float64(15200) {
		t.Fatalf("coverage changes must pass through: %+v", decoded["coverage_changes"])
	}
	clusters, _ := decoded["clusters"].([]any)
	if value, present := clusters[2].(map[string]any)["first_priced_at"]; !present || value != nil {
		t.Fatalf("an unreadable first priced hour must stay null: %+v", clusters[2])
	}
}

// costEventsFixture is the events document as the platform sends it: every
// delta status, an account-level finding, an event with an actor, and one
// source that could not be read.
func costEventsFixture() *client.CostEvents {
	return &client.CostEvents{
		Currency:    "usd",
		GeneratedAt: "2026-09-24T06:00:00Z",
		Days:        30,
		Events: []client.CostEvent{
			{At: "2026-09-23T14:05:00Z", Day: "2026-09-23", Kind: "application_release",
				ClusterID: trendString("11111111-1111-4111-8111-111111111111"), ClusterName: trendString("prod-eu"),
				Subject: "Commerce 2.13 → 2.14", DeltaMonthlyCents: trendCents(4000), WindowDeltaMonthlyCents: trendCents(4000),
				DeltaStatus: "isolated"},
			{At: "2026-09-22T19:00:00Z", Day: "2026-09-22", Kind: "power_schedule",
				ClusterID: trendString("33333333-3333-4333-8333-333333333333"), ClusterName: trendString("staging-1"),
				Subject: "Scheduled stop", WindowDeltaMonthlyCents: trendCents(-23100), DeltaStatus: "shared",
				Note: "Moved together with 1 other event(s) between the same two priced hours; the window's move is theirs together."},
			{At: "2026-09-21T08:30:00Z", Day: "2026-09-21", Kind: "power_manual",
				ClusterID: trendString("44444444-4444-4444-8444-444444444444"), Subject: "Manual start",
				Actor: trendString("alice@example.com"), DeltaStatus: "no_snapshots",
				Note: "The cluster has no priced hour on one side of this event, so its move cannot be measured."},
			{At: "2026-09-20T00:00:00Z", Day: "2026-09-20", Kind: "coverage",
				ClusterID: trendString("22222222-2222-4222-8222-222222222222"), ClusterName: trendString("data-platform"),
				Subject: "Entered pricing", DeltaMonthlyCents: trendCents(15000), DeltaStatus: "coverage"},
			{At: "2026-09-19T11:00:00Z", Day: "2026-09-19", Kind: "waste_resolved", Subject: "Deleted unattached volume vol-1",
				Actor: trendString("bob@example.com"), DeltaMonthlyCents: trendCents(-1200), DeltaStatus: "own_cost"},
			{At: "2026-09-18T09:00:00Z", Day: "2026-09-18", Kind: "waste_resolved", Subject: "Released address 203.0.113.9",
				DeltaStatus: "unpriced"},
			{At: "2026-09-17T09:00:00Z", Day: "2026-09-17", Kind: "decision",
				ClusterID: trendString("55555555-5555-4555-8555-555555555555"), ClusterName: trendString("batch-eu"),
				Subject: "Resize workers from cx42 to cx32", DeltaStatus: "not_applied"},
		},
		Sources: []client.CostEventSource{
			{Kind: "coverage", Available: true}, {Kind: "node_count", Available: true},
			{Kind: "application_release", Available: false}, {Kind: "decision", Available: true},
		},
	}
}

func TestCostEventsRendersEveryDeltaStatusAndTheUnavailableSource(t *testing.T) {
	mock := &costTrendMock{events: costEventsFixture()}
	output, executeError := runCostTrendCommand(t, mock, "cost", "events")
	if executeError != nil {
		t.Fatalf("cost events failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cost events (USD), last 30 days: 7 events · generated 2026-09-24T06:00:00Z",
		"Could not be read: application releases. Those events are missing below, not quiet.",
		"WHEN (UTC)", "CLUSTER", "WHAT HAPPENED", "MOVE/MO",
		"↳ Moved together with 1 other event(s) between the same two priced hours",
		"↳ The cluster has no priced hour on one side of this event",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	for marker, cells := range map[string][]string{
		"2026-09-23 14:05": {"prod-eu", "Release: Commerce 2.13 → 2.14", "+$40.00"},
		"2026-09-22 19:00": {"staging-1", "Power schedule: Scheduled stop", "-$231.00 (shared)"},
		"2026-09-21 08:30": {"cluster 44444444", "Manual power: Manual start", "unknown"},
		"2026-09-20 00:00": {"data-platform", "Coverage: Entered pricing", "+$150.00 (coverage)"},
		"2026-09-19 11:00": {"(account)", "Waste resolved: Deleted", "-$12.00 (own cost)"},
		"2026-09-18 09:00": {"(account)", "unpriced"},
		"2026-09-17 09:00": {"batch-eu", "Cost decision: Resize", "not applied"},
	} {
		line := tableLineContaining(t, output, marker)
		for _, cell := range cells {
			if !strings.Contains(line, cell) {
				t.Fatalf("row %s lacks %q: %s", marker, cell, line)
			}
		}
	}
	if !strings.Contains(output, "Manual start (by") || !strings.Contains(output, "alice@example.com)") {
		t.Fatalf("an event a person did names them:\n%s", output)
	}
	if strings.Contains(output, "$0.00") {
		t.Fatalf("an unknown move must never print as $0.00:\n%s", output)
	}
	if strings.Contains(output, "(the platform serves the newest") {
		t.Fatalf("an untruncated list must not say it is truncated:\n%s", output)
	}
	if len(mock.eventsDays) != 1 || mock.eventsDays[0] != 0 {
		t.Fatalf("an omitted --days leaves the window to the platform, sent %v", mock.eventsDays)
	}
}

func TestCostEventsFitsA100ColumnTerminal(t *testing.T) {
	output, executeError := runCostTrendCommand(t, &costTrendMock{events: costEventsFixture()}, "cost", "events")
	if executeError != nil {
		t.Fatalf("cost events failed: %v", executeError)
	}
	tableWidth := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		width := text.StringWidthWithoutEscSequences(line)
		if strings.HasPrefix(line, "│") || strings.HasPrefix(line, "╭") || strings.HasPrefix(line, "╰") || strings.HasPrefix(line, "├") {
			if width > 100 {
				t.Fatalf("table line is %d columns wide, over 100: %q\n%s", width, line, output)
			}
			if tableWidth == 0 {
				tableWidth = width
			}
			if width != tableWidth {
				t.Fatalf("every table line must be as wide as the table (%d), got %d: %q\n%s", tableWidth, width, line, output)
			}
		}
	}
}

func TestCostEventsSaysWhenTruncatedAndWhenEmpty(t *testing.T) {
	truncated := costEventsFixture()
	truncated.Truncated = true
	output, executeError := runCostTrendCommand(t, &costTrendMock{events: truncated}, "cost", "events", "--days", "14")
	if executeError != nil {
		t.Fatalf("cost events failed: %v", executeError)
	}
	if !strings.Contains(output, "(the platform serves the newest 7 events; older ones in the window are not shown)") {
		t.Fatalf("a truncated list must say so:\n%s", output)
	}

	quiet := &client.CostEvents{Currency: "eur", Days: 7, Events: []client.CostEvent{},
		Sources: []client.CostEventSource{{Kind: "coverage", Available: true}}}
	output, executeError = runCostTrendCommand(t, &costTrendMock{events: quiet}, "cost", "events")
	if executeError != nil {
		t.Fatalf("cost events failed: %v", executeError)
	}
	if !strings.Contains(output, "No event moved the cost line in the last 7 days.") || strings.Contains(output, "╭") {
		t.Fatalf("an empty window with every source read says so in one sentence:\n%s", output)
	}

	blind := &client.CostEvents{Currency: "eur", Days: 7, Events: []client.CostEvent{},
		Sources: []client.CostEventSource{{Kind: "coverage", Available: true}, {Kind: "decision", Available: false}}}
	output, executeError = runCostTrendCommand(t, &costTrendMock{events: blind}, "cost", "events")
	if executeError != nil {
		t.Fatalf("cost events failed: %v", executeError)
	}
	if strings.Contains(output, "No event moved the cost line") ||
		!strings.Contains(output, "Could not be read: cost decisions.") ||
		!strings.Contains(output, "No event from the sources that could be read.") {
		t.Fatalf("an empty window with a source unread must not claim nothing moved:\n%s", output)
	}
}

func TestCostEventsStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostTrendCommand(t, &costTrendMock{events: costEventsFixture()}, "cost", "events", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost events -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["currency"] != "usd" || decoded["days"] != float64(30) || decoded["truncated"] != false {
		t.Fatalf("structured document lacks the wire fields: %+v", decoded)
	}
	events, _ := decoded["events"].([]any)
	if len(events) != 7 {
		t.Fatalf("events missing: %+v", decoded["events"])
	}
	shared := events[1].(map[string]any)
	if value, present := shared["delta_monthly_cents"]; !present || value != nil || shared["window_delta_monthly_cents"] != float64(-23100) {
		t.Fatalf("a shared event keeps a null own move and its window's move: %+v", shared)
	}
	account := events[4].(map[string]any)
	if value, present := account["cluster_id"]; !present || value != nil {
		t.Fatalf("an account-level event keeps a null cluster: %+v", account)
	}
	sources, _ := decoded["sources"].([]any)
	if len(sources) != 4 || sources[2].(map[string]any)["available"] != false {
		t.Fatalf("sources must pass through: %+v", decoded["sources"])
	}
}

// Both routes are served to API tokens on every platform that has them, so a
// 404 can only mean an older platform, and it must not read as "not found".
func TestCostTrendAndEventsReportAMissingRouteAsSuch(t *testing.T) {
	for _, readError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found."},
	} {
		_, trendError := runCostTrendCommand(t, &costTrendMock{trendError: readError}, "cost", "trend")
		if trendError == nil || !strings.Contains(trendError.Error(), "this platform does not serve the fleet cost trend") ||
			!strings.Contains(trendError.Error(), "GET /api/v1/org/cloud-cost/trend is not registered") {
			t.Fatalf("trend error = %v", trendError)
		}
		if got := exitCodeFor(trendError); got != exitError {
			t.Fatalf("exit code = %d, want %d (not the not-found code)", got, exitError)
		}
		_, eventsError := runCostTrendCommand(t, &costTrendMock{eventsError: readError}, "cost", "events")
		if eventsError == nil || !strings.Contains(eventsError.Error(), "this platform does not serve the cost events") ||
			!strings.Contains(eventsError.Error(), "GET /api/v1/org/cloud-cost/events is not registered") {
			t.Fatalf("events error = %v", eventsError)
		}
		if got := exitCodeFor(eventsError); got != exitError {
			t.Fatalf("exit code = %d, want %d", got, exitError)
		}
	}
}

func TestCostTrendAndEventsRelayTheBackendDetail(t *testing.T) {
	detail := client.NewUnexpectedResponseError(403, "This token may not use this surface.")
	_, trendError := runCostTrendCommand(t, &costTrendMock{trendError: detail}, "cost", "trend")
	if trendError == nil || !strings.Contains(trendError.Error(), "reading the fleet cost trend") ||
		!strings.Contains(trendError.Error(), detail.Error()) {
		t.Fatalf("trend error = %v", trendError)
	}
	_, eventsError := runCostTrendCommand(t, &costTrendMock{eventsError: &client.PermissionDeniedError{Permission: "billing.read"}}, "cost", "events")
	if eventsError == nil || !strings.Contains(eventsError.Error(), "reading the cost events") ||
		!strings.Contains(eventsError.Error(), `"billing.read"`) {
		t.Fatalf("events error = %v", eventsError)
	}
	if got := exitCodeFor(eventsError); got != exitForbidden {
		t.Fatalf("exit code = %d, want %d", got, exitForbidden)
	}
}
