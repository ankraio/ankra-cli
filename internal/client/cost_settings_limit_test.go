package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestGetCostSettings_ReadsTheLimitInEffectAndKnowsWhenItIsAbsent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"effective_discount_pct":12.5,"currency":"eur","include_network_egress_estimate":false,` +
			`"analysed_cluster_limit":20,"display_currency":"eur","fx_rates":{"usd":1.0,"eur":0.92}}`))
	}
	settings, err := newTestClient(t, handler).GetCostSettings()
	if err != nil {
		t.Fatalf("GetCostSettings: %v", err)
	}
	if settings.AnalysedClusterLimit == nil || *settings.AnalysedClusterLimit != 20 || settings.AnalysedClusterLimitChange != nil {
		t.Fatalf("the limit in effect did not decode: %+v", settings)
	}

	handler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"effective_discount_pct":0,"currency":"usd","include_network_egress_estimate":true}`))
	}
	older, err := newTestClient(t, handler).GetCostSettings()
	if err != nil {
		t.Fatalf("GetCostSettings: %v", err)
	}
	if older.AnalysedClusterLimit != nil {
		t.Fatalf("a platform that predates the limit must read as unknown (nil), not 8: %+v", older)
	}
	encoded, _ := json.Marshal(older)
	if string(encoded) != `{"effective_discount_pct":0,"currency":"usd","include_network_egress_estimate":true}` {
		t.Fatalf("structured output must not invent the field: %s", encoded)
	}
}

// capturePutBody serves one PUT /api/v1/org/cloud-cost/settings and records
// its raw body.
func capturePutBody(t *testing.T) (*Client, *map[string]json.RawMessage) {
	t.Helper()
	body := map[string]json.RawMessage{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/org/cloud-cost/settings" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if decodeError := json.Unmarshal(raw, &body); decodeError != nil {
			t.Fatalf("body is not a JSON object: %v: %s", decodeError, raw)
		}
		_, _ = w.Write([]byte(`{"effective_discount_pct":5,"currency":"usd","include_network_egress_estimate":true,"analysed_cluster_limit":8}`))
	}
	return newTestClient(t, handler), &body
}

// analysed_cluster_limit is three-state on the route: omitted keeps the stored
// limit, null returns it to the default, a number sets it. The read-only limit
// a settings read carried must never be sent back.
func TestUpdateCostSettings_SendsTheLimitOnlyForAChange(t *testing.T) {
	limitInEffect := 8
	testClient, body := capturePutBody(t)
	saved, err := testClient.UpdateCostSettings(CostSettings{Currency: "usd", EffectiveDiscountPct: 5,
		IncludeNetworkEgressEstimate: true, AnalysedClusterLimit: &limitInEffect})
	if err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if _, present := (*body)["analysed_cluster_limit"]; present || len(*body) != 3 {
		t.Fatalf("without a change the limit must not be sent, even when the settings carry the one in effect: %v", *body)
	}
	if saved.AnalysedClusterLimit == nil || *saved.AnalysedClusterLimit != 8 {
		t.Fatalf("the saved limit did not decode: %+v", saved)
	}

	testClient, body = capturePutBody(t)
	if _, err := testClient.UpdateCostSettings(CostSettings{Currency: "usd", EffectiveDiscountPct: 5,
		AnalysedClusterLimitChange: &AnalysedClusterLimitChange{Limit: 20}}); err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if string((*body)["analysed_cluster_limit"]) != "20" || len(*body) != 4 {
		t.Fatalf("a set limit is sent as the number: %v", *body)
	}

	testClient, body = capturePutBody(t)
	if _, err := testClient.UpdateCostSettings(CostSettings{Currency: "usd", EffectiveDiscountPct: 5,
		AnalysedClusterLimit: &limitInEffect, AnalysedClusterLimitChange: &AnalysedClusterLimitChange{Default: true}}); err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if raw, present := (*body)["analysed_cluster_limit"]; !present || string(raw) != "null" {
		t.Fatalf("returning to the default is sent as null: %v", *body)
	}
}

func TestGetCloudSavings_DecodesTheBudgetAndStalenessWhenPresent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"currency":"eur","analysed_cluster_count":6,"unanalysed_cluster_count":4,"analysis_budget_exhausted":true,` +
			`"priced_cluster_count":10,"waste":{"available":true},"thresholds":{"minimum_savings_cents":500,"minimum_off_hours_monthly_cents":1000,` +
			`"unallocated_share_threshold":0.25,"off_hours_share":0.64,"analysed_cluster_limit":20,"analysis_budget_seconds":20,"snapshot_stale_after_hours":26}}`))
	}
	savings, err := newTestClient(t, handler).GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if savings.AnalysisBudgetExhausted == nil || !*savings.AnalysisBudgetExhausted || savings.Thresholds.AnalysedClusterLimit != 20 ||
		savings.Thresholds.AnalysisBudgetSeconds == nil || *savings.Thresholds.AnalysisBudgetSeconds != 20 ||
		savings.Thresholds.SnapshotStaleAfterHours == nil || *savings.Thresholds.SnapshotStaleAfterHours != 26 {
		t.Fatalf("the budget and staleness did not decode: %+v", savings)
	}

	handler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"currency":"eur","waste":{"available":true},"thresholds":{"analysed_cluster_limit":8}}`))
	}
	older, err := newTestClient(t, handler).GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if older.AnalysisBudgetExhausted != nil || older.Thresholds.AnalysisBudgetSeconds != nil || older.Thresholds.SnapshotStaleAfterHours != nil {
		t.Fatalf("a platform that predates them leaves them nil: %+v", older)
	}
}
