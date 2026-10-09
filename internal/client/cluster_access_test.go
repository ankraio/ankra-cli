package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestElevateClusterAccessPostsToTheElevateRouteWithoutAMember(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.Method + " " + request.URL.Path
		_ = json.NewDecoder(request.Body).Decode(&seenBody)
		_, _ = writer.Write([]byte(`{"grant":{"id":"g-1","role":"edit","scope":"cluster","expires_at":"2026-10-09T20:00:00Z","reason":"incident"}}`))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}

	expiresIn := "4h"
	created, err := apiClient.ElevateClusterAccess(context.Background(), "c-1",
		ElevateClusterAccessRequest{Scope: "cluster", Role: "edit", ExpiresIn: &expiresIn, Reason: "incident"})
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "POST /api/v1/clusters/c-1/access/elevate" {
		t.Fatalf("request went to %q", seenPath)
	}
	if _, named := seenBody["user_email"]; named {
		t.Fatalf("an elevation must not name a member: %v", seenBody)
	}
	if created.Grant.ExpiresAt == nil || created.Grant.Reason == nil {
		t.Fatalf("grant lost its expiry or reason: %+v", created.Grant)
	}
}

func TestAccessRefusalsSayWhatToChange(t *testing.T) {
	for _, testCase := range []struct {
		status int
		body   string
		want   string
	}{
		{403, `{"detail":"cluster_access_policy_violation","max_grant_role":"edit"}`, `the most access anyone can be given is "edit"`},
		{403, `{"detail":"cluster_access_grant_expiry_required","max_lifetime_seconds":14400}`, "must expire within 4h0m0s; pass --expires"},
		{403, `{"detail":"cluster_access_grant_lifetime_exceeded","max_lifetime_seconds":3600}`, "at most 1h0m0s; pass a shorter --expires"},
		{403, `{"detail":"cluster_access_grant_reason_required","require_reason_from_role":"view"}`, "needs a reason; pass --reason"},
		{403, `{"detail":"Break-glass elevation can only grant access to yourself"}`, "Break-glass elevation can only grant access to yourself"},
		{403, `{"detail":"permission_denied","permission":"kube_access.elevate","scope_type":"organisation"}`, "this needs kube_access.elevate"},
		{502, `<html>bad gateway</html>`, "status 502"},
	} {
		refusal := accessResponseError("elevate cluster access failed", testCase.status, []byte(testCase.body))
		if !strings.Contains(refusal.Error(), testCase.want) {
			t.Fatalf("%s: got %q, want it to say %q", testCase.body, refusal.Error(), testCase.want)
		}
		var unexpected *UnexpectedResponseError
		if !errors.As(refusal, &unexpected) || unexpected.StatusCode != testCase.status {
			t.Fatalf("%s: the status code was lost: %v", testCase.body, refusal)
		}
	}
}

func TestGetClusterAccessPolicyReadsThePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/cluster-access-policy" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"policy":{"creator_grant_role":"view","max_grant_role":"edit","elevated_max_ttl_seconds":14400,"require_reason_from_role":null,"is_configured":true}}`))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}
	policy, err := apiClient.GetClusterAccessPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if policy.MaxGrantRole != "edit" || policy.ElevatedMaxTTLSeconds == nil || *policy.ElevatedMaxTTLSeconds != 14400 ||
		policy.RequireReasonFromRole != nil || !policy.IsConfigured {
		t.Fatalf("policy = %+v", policy)
	}
}
