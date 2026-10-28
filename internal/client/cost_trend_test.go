package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fleetCostTrendGolden is a GET /api/v1/org/cloud-cost/trend body in the
// shape the cluster's openapi.json declares (FleetCostTrendResponse): a day no
// cluster was priced (null run rate), a floor day, the current point, a
// coverage change with a cluster entering, and a deleted cluster whose first
// priced hour is only the earliest still known.
const fleetCostTrendGolden = `{"currency":"eur","generated_at":"2026-09-24T06:00:00Z","days":3,` +
	`"points":[` +
	`{"day":"2026-09-22","monthly_cost_estimate_cents":null,"priced_cluster_count":0,"fully_priced_cluster_count":0,"current":false},` +
	`{"day":"2026-09-23","monthly_cost_estimate_cents":70000,"priced_cluster_count":2,"fully_priced_cluster_count":1,"current":false},` +
	`{"day":"2026-09-24","monthly_cost_estimate_cents":84200,"priced_cluster_count":3,"fully_priced_cluster_count":3,"current":true}],` +
	`"coverage_changes":[{"day":"2026-09-24","priced_cluster_count_before":2,"priced_cluster_count_after":3,` +
	`"entered":[{"cluster_id":"22222222-2222-4222-8222-222222222222","cluster_name":"data-platform","monthly_cost_estimate_cents":15000}],` +
	`"left":[],"coverage_delta_monthly_cents":15000}],` +
	`"clusters":[{"cluster_id":"11111111-1111-4111-8111-111111111111","cluster_name":"prod-eu","deleted":false,` +
	`"first_priced_at":"2026-08-01T10:00:00Z","first_priced_exact":true,"first_day":"2026-09-23","last_day":"2026-09-24","priced_days":2},` +
	`{"cluster_id":"33333333-3333-4333-8333-333333333333","cluster_name":"old-dev","deleted":true,` +
	`"first_priced_at":null,"first_priced_exact":false,"first_day":"2026-09-23","last_day":"2026-09-23","priced_days":1}]}`

func TestGetFleetCostTrend_DecodesNullDaysCoverageAndClusters(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/trend" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("days"); got != "3" {
			t.Errorf("days = %q, want 3", got)
		}
		if header := r.Header.Get("X-Ankra-CSRF"); header != "" {
			t.Errorf("a bearer read must not send a CSRF header, got %q", header)
		}
		_, _ = w.Write([]byte(fleetCostTrendGolden))
	}
	testClient := newTestClient(t, handler)
	trend, err := testClient.GetFleetCostTrend(3)
	if err != nil {
		t.Fatalf("GetFleetCostTrend: %v", err)
	}
	if trend.Currency != "eur" || trend.Days != 3 || trend.GeneratedAt != "2026-09-24T06:00:00Z" || len(trend.Points) != 3 {
		t.Fatalf("trend did not decode: %+v", trend)
	}
	unpriced := trend.Points[0]
	if unpriced.MonthlyCostEstimateCents != nil || unpriced.PricedClusterCount != 0 || unpriced.Current {
		t.Fatalf("a day no cluster was priced must decode as a nil run rate, not zero: %+v", unpriced)
	}
	floor := trend.Points[1]
	if floor.MonthlyCostEstimateCents == nil || *floor.MonthlyCostEstimateCents != 70000 ||
		floor.PricedClusterCount != 2 || floor.FullyPricedClusterCount != 1 {
		t.Fatalf("floor day did not decode: %+v", floor)
	}
	if !trend.Points[2].Current {
		t.Fatalf("the last point is the current run rate: %+v", trend.Points[2])
	}
	if len(trend.CoverageChanges) != 1 {
		t.Fatalf("coverage changes did not decode: %+v", trend.CoverageChanges)
	}
	change := trend.CoverageChanges[0]
	if change.PricedClusterCountBefore != 2 || change.PricedClusterCountAfter != 3 || change.CoverageDeltaMonthlyCents != 15000 ||
		len(change.Entered) != 1 || change.Entered[0].ClusterName != "data-platform" || change.Entered[0].MonthlyCostEstimateCents != 15000 ||
		change.Left == nil || len(change.Left) != 0 {
		t.Fatalf("coverage change did not decode: %+v", change)
	}
	if len(trend.Clusters) != 2 {
		t.Fatalf("clusters did not decode: %+v", trend.Clusters)
	}
	exact, deleted := trend.Clusters[0], trend.Clusters[1]
	if exact.FirstPricedAt == nil || *exact.FirstPricedAt != "2026-08-01T10:00:00Z" || !exact.FirstPricedExact || exact.PricedDays != 2 {
		t.Fatalf("cluster span did not decode: %+v", exact)
	}
	if !deleted.Deleted || deleted.FirstPricedAt != nil || deleted.FirstPricedExact {
		t.Fatalf("an unreadable first priced hour must decode as nil: %+v", deleted)
	}
}

func TestGetFleetCostTrend_NoDaysLeavesTheWindowToThePlatform(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("an omitted window must send no query, got %q", r.URL.RawQuery)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"currency": "usd", "days": 30,
			"coverage_changes": []any{map[string]any{"day": "2026-09-01"}}})
	}
	testClient := newTestClient(t, handler)
	trend, err := testClient.GetFleetCostTrend(0)
	if err != nil {
		t.Fatalf("GetFleetCostTrend: %v", err)
	}
	if trend.Points == nil || trend.Clusters == nil || trend.CoverageChanges[0].Entered == nil || trend.CoverageChanges[0].Left == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", trend)
	}
}

// costEventsGolden is a GET /api/v1/org/cloud-cost/events body in the shape
// openapi.json declares (CostEventsResponse): an isolated release, two events
// sharing one window, an event with no snapshot on one side, a coverage
// event, an account-level resolved finding and one source that could not be
// read.
const costEventsGolden = `{"currency":"usd","generated_at":"2026-09-24T06:00:00Z","days":30,"events":[` +
	`{"at":"2026-09-23T14:05:00Z","day":"2026-09-23","kind":"application_release","cluster_id":"11111111-1111-4111-8111-111111111111",` +
	`"cluster_name":"prod-eu","subject":"Commerce 2.13 → 2.14","actor":null,"delta_monthly_cents":4000,` +
	`"window_delta_monthly_cents":4000,"delta_status":"isolated","note":""},` +
	`{"at":"2026-09-22T19:00:00Z","day":"2026-09-22","kind":"power_schedule","cluster_id":"33333333-3333-4333-8333-333333333333",` +
	`"cluster_name":"staging-1","subject":"Scheduled stop","actor":null,"delta_monthly_cents":null,` +
	`"window_delta_monthly_cents":-23100,"delta_status":"shared",` +
	`"note":"Moved together with 1 other event(s) between the same two priced hours; the window's move is theirs together."},` +
	`{"at":"2026-09-22T19:00:00Z","day":"2026-09-22","kind":"node_count","cluster_id":"33333333-3333-4333-8333-333333333333",` +
	`"cluster_name":"staging-1","subject":"3 → 0 nodes","actor":null,"delta_monthly_cents":null,` +
	`"window_delta_monthly_cents":-23100,"delta_status":"shared","note":"Moved together with 1 other event(s) between the same two priced hours; the window's move is theirs together."},` +
	`{"at":"2026-09-21T08:30:00Z","day":"2026-09-21","kind":"power_manual","cluster_id":"44444444-4444-4444-8444-444444444444",` +
	`"cluster_name":null,"subject":"Manual start","actor":"alice@example.com","delta_monthly_cents":null,` +
	`"window_delta_monthly_cents":null,"delta_status":"no_snapshots",` +
	`"note":"The cluster has no priced hour on one side of this event, so its move cannot be measured."},` +
	`{"at":"2026-09-20T00:00:00Z","day":"2026-09-20","kind":"coverage","cluster_id":"22222222-2222-4222-8222-222222222222",` +
	`"cluster_name":"data-platform","subject":"Entered pricing","actor":null,"delta_monthly_cents":15000,` +
	`"window_delta_monthly_cents":null,"delta_status":"coverage","note":""},` +
	`{"at":"2026-09-19T11:00:00Z","day":"2026-09-19","kind":"waste_resolved","cluster_id":null,"cluster_name":null,` +
	`"subject":"Deleted unattached volume vol-1","actor":"bob@example.com","delta_monthly_cents":-1200,` +
	`"window_delta_monthly_cents":null,"delta_status":"own_cost","note":""}],` +
	`"truncated":false,"sources":[{"kind":"coverage","available":true},{"kind":"node_count","available":true},` +
	`{"kind":"application_release","available":true},{"kind":"decision","available":false}]}`

func TestGetCostEvents_DecodesStatusesNullsAndSources(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/events" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("days"); got != "30" {
			t.Errorf("days = %q, want 30", got)
		}
		_, _ = w.Write([]byte(costEventsGolden))
	}
	testClient := newTestClient(t, handler)
	events, err := testClient.GetCostEvents(30)
	if err != nil {
		t.Fatalf("GetCostEvents: %v", err)
	}
	if events.Currency != "usd" || events.Days != 30 || events.Truncated || len(events.Events) != 6 || len(events.Sources) != 4 {
		t.Fatalf("events did not decode: %+v", events)
	}
	isolated := events.Events[0]
	if isolated.DeltaStatus != "isolated" || isolated.DeltaMonthlyCents == nil || *isolated.DeltaMonthlyCents != 4000 ||
		isolated.Subject != "Commerce 2.13 → 2.14" || isolated.Actor != nil {
		t.Fatalf("isolated event did not decode: %+v", isolated)
	}
	shared := events.Events[1]
	if shared.DeltaStatus != "shared" || shared.DeltaMonthlyCents != nil || shared.WindowDeltaMonthlyCents == nil ||
		*shared.WindowDeltaMonthlyCents != -23100 || !strings.Contains(shared.Note, "Moved together") {
		t.Fatalf("a shared event carries the window's move and no own move: %+v", shared)
	}
	unknown := events.Events[3]
	if unknown.DeltaMonthlyCents != nil || unknown.WindowDeltaMonthlyCents != nil || unknown.ClusterName != nil ||
		unknown.ClusterID == nil || unknown.Actor == nil || *unknown.Actor != "alice@example.com" {
		t.Fatalf("an unmeasured event must decode with nil moves, not zero: %+v", unknown)
	}
	account := events.Events[5]
	if account.ClusterID != nil || account.DeltaStatus != "own_cost" || *account.DeltaMonthlyCents != -1200 {
		t.Fatalf("an account-level finding did not decode: %+v", account)
	}
	if events.Sources[3] != (CostEventSource{Kind: "decision", Available: false}) {
		t.Fatalf("an unavailable source did not decode: %+v", events.Sources)
	}
}

func TestGetCostEvents_AbsentListsDecodeAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"currency": "eur", "days": 7})
	}
	testClient := newTestClient(t, handler)
	events, err := testClient.GetCostEvents(7)
	if err != nil {
		t.Fatalf("GetCostEvents: %v", err)
	}
	if events.Events == nil || events.Sources == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", events)
	}
}

// A platform that predates the routes answers with a bare 404; the commands
// key their "this platform predates it" message on the status.
func TestCostTrendAndEvents_SurfaceTheStatusCode(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	testClient := newTestClient(t, handler)
	_, trendError := testClient.GetFleetCostTrend(0)
	_, eventsError := testClient.GetCostEvents(0)
	for _, readError := range []error{trendError, eventsError} {
		var unexpected *UnexpectedResponseError
		if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
			t.Fatalf("error = %v", readError)
		}
	}
}

// A window out of range is the platform's 400 with a detail; the detail is
// what the user reads.
func TestCostTrendAndEvents_SurfaceTheBackendDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"detail": "days must be an integer between 1 and 34"})
	}
	testClient := newTestClient(t, handler)
	_, trendError := testClient.GetFleetCostTrend(90)
	_, eventsError := testClient.GetCostEvents(90)
	for _, readError := range []error{trendError, eventsError} {
		var unexpected *UnexpectedResponseError
		if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusBadRequest ||
			unexpected.Error() != "days must be an integer between 1 and 34" {
			t.Fatalf("error = %v", readError)
		}
	}
}
