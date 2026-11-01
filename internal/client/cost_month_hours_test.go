package client

import (
	"net/http"
	"testing"
)

// The hours of the month a month to date holds (ankra-bn86k4.4.8.2.2). The
// cluster read, the fleet summary (in total and per costliest cluster) and
// each budget's projection carry month_hours from a platform that counts
// them. A platform that predates the field, or sends null, decodes as nil:
// not counted, never a whole month.

const costMonthHoursJSON = `{"hours_elapsed": 1440, "hours_metered": 1436, "hours_estimated": 3, "is_short": true,
	"cluster_count": 2, "short_cluster_count": 1, "note": "Month so far covers 1436 of 1440 cluster hours."}`

func serveCostJSON(t *testing.T, body string) *Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func assertCostMonthHours(t *testing.T, name string, hours *MonthHours) {
	t.Helper()
	if hours == nil {
		t.Fatalf("%s: month_hours did not decode", name)
	}
	want := MonthHours{HoursElapsed: 1440, HoursMetered: 1436, HoursEstimated: 3, IsShort: true, ClusterCount: 2,
		ShortClusterCount: 1, Note: "Month so far covers 1436 of 1440 cluster hours."}
	if *hours != want {
		t.Fatalf("%s: month_hours = %+v, want %+v", name, *hours, want)
	}
}

func TestGetClusterCost_DecodesMonthHoursAndKeepsAnAbsentOneNil(t *testing.T) {
	result, err := serveCostJSON(t, `{"has_data": true, "summary": {"currency": "eur", "month_to_date_cents": 41000,
		"month_hours": `+costMonthHoursJSON+`}}`).GetClusterCost("c1")
	if err != nil {
		t.Fatalf("GetClusterCost: %v", err)
	}
	assertCostMonthHours(t, "cluster", result.Summary.MonthHours)

	for name, body := range map[string]string{
		"absent": `{"has_data": true, "summary": {"currency": "eur"}}`,
		"null":   `{"has_data": true, "summary": {"currency": "eur", "month_hours": null}}`,
	} {
		result, err := serveCostJSON(t, body).GetClusterCost("c1")
		if err != nil {
			t.Fatalf("%s: GetClusterCost: %v", name, err)
		}
		if result.Summary.MonthHours != nil {
			t.Fatalf("%s: an uncounted month must decode as nil, got %+v", name, *result.Summary.MonthHours)
		}
	}
}

func TestGetFleetCloudCost_DecodesMonthHoursOnTheTotalAndEachCluster(t *testing.T) {
	result, err := serveCostJSON(t, `{"currency": "eur", "cluster_count": 2, "by_provider": [],
		"top_clusters": [{"cluster_id": "a", "cluster_name": "prod", "month_hours": `+costMonthHoursJSON+`},
			{"cluster_id": "b", "cluster_name": "dev"}],
		"month_hours": `+costMonthHoursJSON+`}`).GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost: %v", err)
	}
	assertCostMonthHours(t, "fleet", result.MonthHours)
	assertCostMonthHours(t, "top cluster", result.TopClusters[0].MonthHours)
	if result.TopClusters[1].MonthHours != nil {
		t.Fatalf("a cluster sent without month_hours must decode as nil, got %+v", *result.TopClusters[1].MonthHours)
	}

	older, err := serveCostJSON(t, `{"currency": "eur", "cluster_count": 1, "by_provider": [], "top_clusters": []}`).GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost (older platform): %v", err)
	}
	if older.MonthHours != nil {
		t.Fatalf("an older platform's summary must decode with nil month_hours, got %+v", *older.MonthHours)
	}
}

func TestListCostBudgets_DecodesTheProjectionMonthHours(t *testing.T) {
	result, err := serveCostJSON(t, `{"budgets": [
		{"id": "a", "projection": {"status": "under", "month_hours": `+costMonthHoursJSON+`}},
		{"id": "b", "projection": {"status": "under"}},
		{"id": "c", "projection": {"status": "unknown", "month_hours": null}}]}`).ListCostBudgets()
	if err != nil {
		t.Fatalf("ListCostBudgets: %v", err)
	}
	assertCostMonthHours(t, "budget", result.Budgets[0].Projection.MonthHours)
	for _, budget := range result.Budgets[1:] {
		if budget.Projection.MonthHours != nil {
			t.Fatalf("budget %s: an uncounted month must decode as nil, got %+v", budget.ID, *budget.Projection.MonthHours)
		}
	}
}
