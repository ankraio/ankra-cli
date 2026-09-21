package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGetFleetCloudCost_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/summary" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, FleetCloudCost{
			Currency: "eur", ClusterCount: 2, ProjectedMonthEndCents: 86000,
			ByProvider: []FleetProviderCost{{Provider: "hetzner", ClusterCount: 2, ProjectedMonthEndCents: 86000}},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost: %v", err)
	}
	if result.Currency != "eur" || result.ClusterCount != 2 || len(result.ByProvider) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.TopClusters == nil {
		t.Fatalf("an absent top_clusters list must decode as empty, not nil")
	}
}

func TestGetCloudSavings_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/savings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"currency":"eur","generated_at":"2026-09-18T06:00:00Z","total_monthly_savings_cents":43800,` +
			`"recommendations":[{"id":"right_size_idle:c1","kind":"right_size_idle","cluster_id":"c1","cluster_name":"prod-eu",` +
			`"provider":"hetzner","environment":null,"monthly_savings_cents":43800,"monthly_cost_cents":200000,"share_percent":22,` +
			`"evidence":{"idle_monthly_cents":43800,"unallocated_monthly_cents":10000,"unallocated_share_percent":null,` +
			`"stoppable_monthly_cents":null,"off_hours_share_percent":null,"stop_cron":null,"start_cron":null}}],` +
			`"breakdowns":[],"namespaces":[],"analysed_cluster_count":1,"unanalysed_cluster_count":0,"priced_cluster_count":1,` +
			`"unpriced_cluster_count":0,"unpriced_clusters":[],"stale_cluster_count":0,"stale_clusters":[],"unreadable_clusters":[],` +
			`"waste":{"available":true,"has_data":false,"scanned_at":null,"total_monthly_cost_cents":0,"finding_count":0,"unpriced_finding_count":0},` +
			`"thresholds":{"minimum_savings_cents":500,"minimum_off_hours_monthly_cents":1000,"unallocated_share_threshold":0.25,` +
			`"off_hours_share":0.6428571428571429,"analysed_cluster_limit":8}}`))
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if result.Currency != "eur" || result.TotalMonthlySavingsCents != 43800 || len(result.Recommendations) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	recommendation := result.Recommendations[0]
	if recommendation.Kind != "right_size_idle" || recommendation.Evidence.IdleMonthlyCents == nil ||
		*recommendation.Evidence.IdleMonthlyCents != 43800 || recommendation.Evidence.StopCron != nil || recommendation.Environment != nil {
		t.Fatalf("recommendation did not decode: %+v", recommendation)
	}
	if result.Thresholds.AnalysedClusterLimit != 8 || result.Thresholds.UnallocatedShareThreshold != 0.25 {
		t.Fatalf("thresholds did not decode: %+v", result.Thresholds)
	}
	if !result.Waste.Available || result.Waste.HasData || result.Waste.ScannedAt != nil {
		t.Fatalf("waste did not decode: %+v", result.Waste)
	}
}

func TestGetCloudSavings_AbsentListsDecodeAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"currency": "usd", "waste": map[string]any{"available": false}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if result.Recommendations == nil || result.Breakdowns == nil || result.Namespaces == nil ||
		result.UnpricedClusters == nil || result.StaleClusters == nil || result.UnreadableClusters == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", result)
	}
}

func TestGetClusterCost_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/clusters/cluster-123/cost" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"has_data":   false,
			"summary":    nil,
			"trend":      []any{},
			"namespaces": []any{},
			"readiness":  map[string]any{"state": "no_credential", "provider": "aws"},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetClusterCost("cluster-123")
	if err != nil {
		t.Fatalf("GetClusterCost: %v", err)
	}
	if result.HasData || result.Summary != nil || result.Readiness == nil || result.Readiness.State != "no_credential" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestUpdateCostSettings_SendsPutWithCSRFAndBody(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/settings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		header := r.Header.Get("X-Ankra-CSRF")
		cookie, cookieError := r.Cookie("ankra_csrf")
		if header == "" || cookieError != nil || cookie.Value != header {
			t.Errorf("CSRF double-submit missing: header=%q cookieError=%v", header, cookieError)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode body: %v", decodeError)
		}
		if body["currency"] != "eur" || body["effective_discount_pct"] != 12.5 || body["include_network_egress_estimate"] != true {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, CostSettings{Currency: "eur", EffectiveDiscountPct: 12.5, IncludeNetworkEgressEstimate: true})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.UpdateCostSettings(CostSettings{Currency: "eur", EffectiveDiscountPct: 12.5, IncludeNetworkEgressEstimate: true})
	if err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if result.Currency != "eur" || result.EffectiveDiscountPct != 12.5 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCost_BackendDetailSurfaces(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{
			"detail": "Only organisation admins can change cloud cost settings"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.UpdateCostSettings(CostSettings{Currency: "usd"})
	if err == nil || !strings.Contains(err.Error(), "Only organisation admins") {
		t.Fatalf("expected the backend detail to surface, got %v", err)
	}
	_, err = testClient.GetCostSettings()
	if err == nil || !strings.Contains(err.Error(), "Only organisation admins") {
		t.Fatalf("expected the backend detail to surface on a read too, got %v", err)
	}
}
