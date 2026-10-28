package client

import (
	"errors"
	"net/http"
	"testing"
)

func TestGetAIRemediationPolicy_ReadsTheSavedPolicy(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/ai-remediation/policy" {
			t.Errorf("path = %s, want /api/v1/org/ai-remediation/policy", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"enabled":                  true,
			"autonomy_level":           "auto",
			"tier_overrides":           map[string]string{"apply_stack_changes": "auto"},
			"slack_webhook_id":         "0b4f1b9e-0000-4000-8000-000000000001",
			"approver_user_ids":        []string{"11111111-0000-4000-8000-000000000001"},
			"max_actions_per_incident": 7,
			"cooldown_minutes":         15,
			"cluster_allow_list":       []string{"22222222-0000-4000-8000-000000000002"},
			"updated_at":               "2026-09-01T10:30:00Z",
		})
	}
	testClient := newTestClient(t, handler)

	policy, readError := testClient.GetAIRemediationPolicy()
	if readError != nil {
		t.Fatalf("GetAIRemediationPolicy: %v", readError)
	}
	if !policy.IsConfigured() {
		t.Fatal("a policy carrying updated_at is configured")
	}
	if !policy.Enabled || policy.AutonomyLevel != "auto" ||
		policy.TierOverrides["apply_stack_changes"] != "auto" ||
		policy.MaxActionsPerIncident != 7 || policy.CooldownMinutes != 15 ||
		len(policy.ClusterAllowList) != 1 || len(policy.ApproverUserIDs) != 1 ||
		policy.SlackWebhookID == nil {
		t.Fatalf("policy = %+v", policy)
	}
}

// The platform answers a default DOCUMENT with HTTP 200 for an organisation
// that never saved a policy, so the absence has to survive decoding as a null
// updated_at rather than as a status code.
func TestGetAIRemediationPolicy_KeepsTheNoRowDefaultDistinguishable(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"enabled":                  false,
			"autonomy_level":           "propose",
			"tier_overrides":           map[string]string{},
			"slack_webhook_id":         nil,
			"approver_user_ids":        []string{},
			"max_actions_per_incident": 5,
			"cooldown_minutes":         30,
			"cluster_allow_list":       nil,
			"updated_at":               nil,
		})
	}
	testClient := newTestClient(t, handler)

	policy, readError := testClient.GetAIRemediationPolicy()
	if readError != nil {
		t.Fatalf("GetAIRemediationPolicy: %v", readError)
	}
	if policy.IsConfigured() {
		t.Fatal("a null updated_at is the platform default, not a saved policy")
	}
	if policy.ClusterAllowList != nil {
		t.Fatalf("an absent allow list stays nil, got %#v", policy.ClusterAllowList)
	}
}

// null and [] are different scopes on the platform side, so the decode must
// not fold one into the other.
func TestGetAIRemediationPolicy_KeepsAnEmptyAllowListApartFromNoAllowList(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"enabled": true, "autonomy_level": "propose",
			"cluster_allow_list": []string{},
			"updated_at":         "2026-09-01T10:30:00Z",
		})
	}
	testClient := newTestClient(t, handler)

	policy, readError := testClient.GetAIRemediationPolicy()
	if readError != nil {
		t.Fatalf("GetAIRemediationPolicy: %v", readError)
	}
	if policy.ClusterAllowList == nil {
		t.Fatal("an empty allow list is not the same as no allow list")
	}
	if len(policy.ClusterAllowList) != 0 {
		t.Fatalf("allow list = %#v", policy.ClusterAllowList)
	}
}

func TestGetAIRemediationPolicy_SurfacesTheStatusCode(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	testClient := newTestClient(t, handler)

	_, readError := testClient.GetAIRemediationPolicy()
	if readError == nil {
		t.Fatal("a 404 is an error")
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v", readError)
	}
}
