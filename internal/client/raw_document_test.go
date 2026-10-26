package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The summary document a newer platform answers: every field the CLI struct
// declares, plus fields it does not.
const fleetCostWithFutureFields = `{"currency":"eur","cluster_count":2,"monthly_cost_estimate_cents":100,` +
	`"month_to_date_cents":40,"projected_month_end_cents":90,"by_provider":[],"top_clusters":[],` +
	`"generated_at":"2026-10-25T19:00:00Z","scope":{"kind":"organisation"},"by_project":[{"project":"web"}]}`

func TestSendJSONKeepsTheResponseDocumentBesideTheDecode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(fleetCostWithFutureFields))
	}))
	defer server.Close()

	summary, err := New("test-token", server.URL).GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost: %v", err)
	}
	if summary.Currency != "eur" || summary.ProjectedMonthEndCents != 90 {
		t.Fatalf("the typed decode is unchanged, got %+v", summary)
	}
	if got := string(summary.RawJSON()); got != fleetCostWithFutureFields {
		t.Fatalf("RawJSON = %s, want the body as answered", got)
	}
}

func TestCSRFWriteKeepsTheResponseDocumentBesideTheDecode(t *testing.T) {
	const saved = `{"effective_discount_pct":5,"currency":"usd","include_network_egress_estimate":true,` +
		`"analysed_cluster_limit":8,"fx_rates":{"usd":1}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(saved))
	}))
	defer server.Close()

	settings, err := New("test-token", server.URL).UpdateCostSettings(CostSettings{Currency: "usd"})
	if err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if got := string(settings.RawJSON()); got != saved {
		t.Fatalf("RawJSON = %s, want the body as answered", got)
	}
}

func TestRawDocumentIsEmptyForAValueBuiltInCode(t *testing.T) {
	built := FleetCloudCost{Currency: "eur"}
	if built.RawJSON() != nil {
		t.Fatalf("a value built in code carries no document, got %s", built.RawJSON())
	}
}
