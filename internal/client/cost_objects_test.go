package client

import (
	"errors"
	"net/http"
	"testing"
)

const objectCostTestClusterID = "1834920e-3001-4157-8938-33c447031033"

// TestGetObjectCost_ReadsEachKindsRoute pins the route of every kind and
// that the namespace and stack segments are path-escaped, so a name with a
// space or a slash stays one segment instead of reaching another route.
func TestGetObjectCost_ReadsEachKindsRoute(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		segments []string
		wantPath string
	}{
		{"cluster", ObjectCostKindCluster, []string{objectCostTestClusterID},
			"/api/v1/org/cloud-cost/objects/cluster/" + objectCostTestClusterID},
		{"namespace", ObjectCostKindNamespace, []string{objectCostTestClusterID, "kube-system"},
			"/api/v1/org/cloud-cost/objects/namespace/" + objectCostTestClusterID + "/kube-system"},
		{"namespace escaped", ObjectCostKindNamespace, []string{objectCostTestClusterID, "a/b"},
			"/api/v1/org/cloud-cost/objects/namespace/" + objectCostTestClusterID + "/a%2Fb"},
		{"stack escaped", ObjectCostKindStack, []string{objectCostTestClusterID, "web app/v2?x#y"},
			"/api/v1/org/cloud-cost/objects/stack/" + objectCostTestClusterID + "/web%20app%2Fv2%3Fx%23y"},
		{"application", ObjectCostKindApplication, []string{"2b8c7c1e-7d6a-4f0e-9d3c-0c5a1b2c3d4e"},
			"/api/v1/org/cloud-cost/objects/application/2b8c7c1e-7d6a-4f0e-9d3c-0c5a1b2c3d4e"},
		{"credential", ObjectCostKindCredential, []string{"9f1d2e3c-4b5a-4968-8776-655443322110"},
			"/api/v1/org/cloud-cost/objects/credential/9f1d2e3c-4b5a-4968-8776-655443322110"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotPath, gotMethod, gotQuery string
			handler := func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod, gotQuery = r.URL.EscapedPath(), r.Method, r.URL.RawQuery
				jsonResponse(t, w, http.StatusOK, map[string]any{
					"kind": testCase.kind, "object": map[string]any{"id": "x", "name": "x", "cluster_id": nil, "cluster_name": nil},
					"currency": "usd", "priced": false, "unpriced_reason": "nothing behind it",
					"open_waste": map[string]any{"available": false, "reason": "not attributed", "count": 0,
						"monthly_cents": nil, "unpriced_count": 0},
					"snapshot_stale_after_hours": 26,
				})
			}
			result, err := newTestClient(t, handler).GetObjectCost(testCase.kind, testCase.segments...)
			if err != nil {
				t.Fatalf("GetObjectCost: %v", err)
			}
			if gotMethod != http.MethodGet || gotPath != testCase.wantPath || gotQuery != "" {
				t.Fatalf("request = %s %s?%s, want GET %s", gotMethod, gotPath, gotQuery, testCase.wantPath)
			}
			if result.Trend30d == nil || result.Clusters == nil || result.Namespaces == nil {
				t.Fatalf("absent lists must decode as empty, not nil: %+v", result)
			}
			if result.MonthlyCents != nil || result.UnpricedReason == nil || *result.UnpricedReason != "nothing behind it" {
				t.Fatalf("decoded = %+v", result)
			}
		})
	}
}

// TestGetObjectCost_KeepsThePlatformsRefusal pins that a refusal reaches the
// command with its status and the platform's detail, which is what the
// command's error mapping keys on; a 422 validation list carries no detail
// string.
func TestGetObjectCost_KeepsThePlatformsRefusal(t *testing.T) {
	for _, testCase := range []struct {
		status     int
		body       any
		wantDetail string
	}{
		{http.StatusNotFound, map[string]any{"detail": "Object not found"}, "Object not found"},
		{http.StatusNotFound, map[string]any{"detail": "Cluster not found"}, "Cluster not found"},
		{http.StatusBadRequest, map[string]any{"detail": "namespace must be a Kubernetes namespace name"},
			"namespace must be a Kubernetes namespace name"},
		{http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{"type": "uuid_parsing",
			"loc": []any{"path", "application_id"}, "msg": "Input should be a valid UUID"}}}, ""},
	} {
		handler := func(w http.ResponseWriter, r *http.Request) { jsonResponse(t, w, testCase.status, testCase.body) }
		_, err := newTestClient(t, handler).GetObjectCost(ObjectCostKindApplication, "not-a-uuid")
		var unexpected *UnexpectedResponseError
		if !errors.As(err, &unexpected) || unexpected.StatusCode != testCase.status || unexpected.Detail != testCase.wantDetail {
			t.Fatalf("status %d: err = %#v, want status %d detail %q", testCase.status, err, testCase.status, testCase.wantDetail)
		}
	}
}
