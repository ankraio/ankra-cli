package client

import (
	"net/http"
	"testing"
)

// TestGetNamespaceCostHistory_SendsTheWindowAndNormalisesLists pins the
// request (path, days, granularity, and nothing when omitted) and that
// absent lists decode as empty, never nil.
func TestGetNamespaceCostHistory_SendsTheWindowAndNormalisesLists(t *testing.T) {
	var queries []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/clusters/cluster-123/cost/namespaces/history" {
			t.Errorf("path = %s", r.URL.Path)
		}
		queries = append(queries, r.URL.RawQuery)
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"cluster_id": "cluster-123", "currency": "usd", "granularity": "day", "days": 30,
			"from": "2026-08-17T00:00:00Z", "to": "2026-09-15T12:00:00Z",
			"namespaces": []any{map[string]any{"namespace": "shop", "stack_id": nil, "total_cents": 0}},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetNamespaceCostHistory("cluster-123", 7, "hour")
	if err != nil {
		t.Fatalf("GetNamespaceCostHistory: %v", err)
	}
	if _, err := testClient.GetNamespaceCostHistory("cluster-123", 0, ""); err != nil {
		t.Fatalf("GetNamespaceCostHistory: %v", err)
	}
	if len(queries) != 2 || queries[0] != "days=7&granularity=hour" || queries[1] != "" {
		t.Fatalf("queries = %q", queries)
	}
	if result.Buckets == nil || len(result.Namespaces) != 1 || result.Namespaces[0].CostCents == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", result)
	}
}
