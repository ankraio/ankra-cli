package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestMoveCluster(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		wantError   bool
		wantRefused bool
		wantDenied  bool
	}{
		{
			name: "success decodes the result",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/clusters/c-1/move" || r.Method != http.MethodPost {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var body MoveClusterRequest
				if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil || body.DestinationOrganisationID != "org-acme" {
					t.Errorf("unexpected body %+v (%v)", body, decodeError)
				}
				jsonResponse(t, w, http.StatusOK, map[string]any{
					"cluster_id": "c-1", "cluster_name": "edge-01", "source_organisation_id": "org-current",
					"destination_organisation_id": "org-acme", "destination_organisation_name": "Acme",
					"detached": map[string]any{"kube_tokens": 1}, "secrets_relocated": 2, "warnings": []string{},
				})
			},
		},
		{
			name: "conflict surfaces the refusal",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": "Operations are still running", "code": "operations_in_flight", "conflicts": []string{"upgrade"}})
			},
			wantError: true, wantRefused: true,
		},
		{
			name: "forbidden surfaces the permission denial",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusForbidden, map[string]any{"detail": "permission_denied", "permission": "organisation.manage", "scope_type": "organisation"})
			},
			wantError: true, wantDenied: true,
		},
		{
			name: "not found surfaces the detail",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
			},
			wantError: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			apiClient := newTestClient(t, testCase.handler)
			result, moveError := apiClient.MoveCluster(context.Background(), "c-1", "org-acme")
			if testCase.wantError {
				if moveError == nil {
					t.Fatalf("expected an error")
				}
				var refused *MoveClusterRefusedError
				if errors.As(moveError, &refused) != testCase.wantRefused {
					t.Fatalf("refused = %v, want %v (%v)", !testCase.wantRefused, testCase.wantRefused, moveError)
				}
				if testCase.wantRefused && (refused.Code != "operations_in_flight" || len(refused.Conflicts) != 1) {
					t.Fatalf("refusal = %+v", refused)
				}
				var denied *PermissionDeniedError
				if errors.As(moveError, &denied) != testCase.wantDenied {
					t.Fatalf("denied = %v, want %v (%v)", !testCase.wantDenied, testCase.wantDenied, moveError)
				}
				return
			}
			if moveError != nil {
				t.Fatalf("unexpected error: %v", moveError)
			}
			if result.ClusterName != "edge-01" || result.DestinationOrganisationName != "Acme" || result.Detached.KubeTokens != 1 || result.SecretsRelocated != 2 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}
