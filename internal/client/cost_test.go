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
