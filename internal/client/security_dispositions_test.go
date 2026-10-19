package client

import (
	"net/http"
	"testing"
)

func bareDispositionPolicy() map[string]any {
	return map[string]any{
		"id":                 "8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d",
		"disposition":        "accepted_risk",
		"reason":             "not reachable",
		"status":             "active",
		"active_match_count": 3,
		"selector":           map[string]any{"cve_id": "CVE-2025-24813", "package_name": "tomcat-embed-core"},
	}
}

func TestUpdateSecurityDispositionDecodesTheBarePolicy(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPatch || request.URL.Path != "/api/v1/org/security/vulnerability-dispositions/8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, bareDispositionPolicy())
	})

	reason := "not reachable"
	policy, updateError := apiClient.UpdateSecurityDisposition("8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d", SecurityDispositionUpdateRequest{Reason: &reason})
	if updateError != nil {
		t.Fatalf("update: %v", updateError)
	}
	if policy.ID != "8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d" || policy.Disposition != "accepted_risk" ||
		policy.ActiveMatchCount != 3 || policy.Selector.CVEID != "CVE-2025-24813" {
		t.Errorf("policy = %+v, want the decoded bare policy", policy)
	}
}

func TestRevokeSecurityDispositionDecodesTheBarePolicy(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/security/vulnerability-dispositions/8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d/revoke" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		revoked := bareDispositionPolicy()
		revoked["status"] = "revoked"
		jsonResponse(t, writer, http.StatusOK, revoked)
	})

	policy, revokeError := apiClient.RevokeSecurityDisposition("8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d", SecurityDispositionRevokeRequest{Reason: "fixed"})
	if revokeError != nil {
		t.Fatalf("revoke: %v", revokeError)
	}
	if policy.ID != "8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d" || policy.Status != "revoked" {
		t.Errorf("policy = %+v, want the decoded revoked policy", policy)
	}
}

func TestRevokeSecurityDispositionReturnsAPIErrors(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Policy not found"})
	})

	if _, revokeError := apiClient.RevokeSecurityDisposition("missing", SecurityDispositionRevokeRequest{Reason: "fixed"}); revokeError == nil {
		t.Fatal("expected an error for a 404")
	}
}
