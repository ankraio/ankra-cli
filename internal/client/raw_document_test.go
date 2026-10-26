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

// Every cost and decision read and write keeps the platform's document, so
// no command's -o json falls back to the declared subset because its method
// decodes on a lane of its own (ankra-bn86k4.8.5).
func TestEveryCostResponseKeepsTheResponseDocument(t *testing.T) {
	const document = `{"zz_added_by_a_newer_platform":true}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	apiClient := New("test-token", server.URL)
	const id = "11111111-1111-4111-8111-111111111111"

	type carrier interface{ ResponseDocument() RawDocument }
	reads := map[string]func() (carrier, error){
		"GetFleetCloudCost":          func() (carrier, error) { return apiClient.GetFleetCloudCost() },
		"GetClusterCost":             func() (carrier, error) { return apiClient.GetClusterCost(id) },
		"GetCostSettings":            func() (carrier, error) { return apiClient.GetCostSettings() },
		"UpdateCostSettings":         func() (carrier, error) { return apiClient.UpdateCostSettings(CostSettings{Currency: "eur"}) },
		"GetCloudSavings":            func() (carrier, error) { return apiClient.GetCloudSavings() },
		"GetCloudLedger":             func() (carrier, error) { return apiClient.GetCloudLedger() },
		"GetCostAutopilot":           func() (carrier, error) { return apiClient.GetCostAutopilot() },
		"UpdateCostAutopilot":        func() (carrier, error) { return apiClient.UpdateCostAutopilot(CostAutopilotPolicyUpdate{}) },
		"SetCostAutopilotOverride":   func() (carrier, error) { return apiClient.SetCostAutopilotOverride(id, CostAutopilotOverrideRequest{}) },
		"ClearCostAutopilotOverride": func() (carrier, error) { return apiClient.ClearCostAutopilotOverride(id) },
		"ListCostBudgets":            func() (carrier, error) { return apiClient.ListCostBudgets() },
		"CreateCostBudget":           func() (carrier, error) { return apiClient.CreateCostBudget(CostBudgetWrite{}) },
		"UpdateCostBudget":           func() (carrier, error) { return apiClient.UpdateCostBudget(id, CostBudgetWrite{}) },
		"GetNamespaceCostHistory":    func() (carrier, error) { return apiClient.GetNamespaceCostHistory(id, 0, "") },
		"GetObjectCost":              func() (carrier, error) { return apiClient.GetObjectCost("cluster", id) },
		"GetCostReconciliation":      func() (carrier, error) { return apiClient.GetCostReconciliation("") },
		"GetFleetCostTrend":          func() (carrier, error) { return apiClient.GetFleetCostTrend(0) },
		"GetCostEvents":              func() (carrier, error) { return apiClient.GetCostEvents(0) },
		"ListDecisions":              func() (carrier, error) { return apiClient.ListDecisions(DecisionListFilter{}) },
		"GetDecision":                func() (carrier, error) { return apiClient.GetDecision(id) },
		"GetDecisionActivity":        func() (carrier, error) { return apiClient.GetDecisionActivity(id) },
		"ApproveDecision":            func() (carrier, error) { return apiClient.ApproveDecision(id, nil) },
		"HoldDecision":               func() (carrier, error) { return apiClient.HoldDecision(id, nil) },
		"ReleaseDecision":            func() (carrier, error) { return apiClient.ReleaseDecision(id, nil) },
		"SetAsideDecision":           func() (carrier, error) { return apiClient.SetAsideDecision(id, nil) },
		"ExecuteDecision":            func() (carrier, error) { return apiClient.ExecuteDecision(id, DecisionExecuteOptions{}) },
	}
	for name, read := range reads {
		value, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := string(value.ResponseDocument().RawJSON()); got != document {
			t.Fatalf("%s keeps no response document (got %q): it decodes outside the shared helpers", name, got)
		}
	}
}
