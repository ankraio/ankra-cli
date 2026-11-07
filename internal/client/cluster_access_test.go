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
		{403, `{"detail":"permission_denied"}`, "permission denied: your role does not allow this"},
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

// TestAccessRefusalsAreClassified pins what the exit-code classification
// reads: a policy refusal is a ClusterAccessRefusalError that says so and
// carries no RBAC denial; any other 403 carries a PermissionDeniedError
// (exit 7, not the credential exit 6); other statuses carry neither.
func TestAccessRefusalsAreClassified(t *testing.T) {
	for _, testCase := range []struct {
		status             int
		body               string
		policy, permission bool
	}{
		{403, `{"detail":"cluster_access_policy_violation","max_grant_role":"edit"}`, true, false},
		{403, `{"detail":"cluster_access_grant_expiry_required","max_lifetime_seconds":14400}`, true, false},
		{403, `{"detail":"cluster_access_grant_lifetime_exceeded","max_lifetime_seconds":3600}`, true, false},
		{403, `{"detail":"cluster_access_grant_reason_required"}`, true, false},
		{403, `{"detail":"permission_denied","permission":"kube_access.elevate"}`, false, true},
		{403, `{"detail":"Only organisation admins can manage cluster access"}`, false, true},
		{401, `{"detail":"Not authenticated"}`, false, false},
		{409, `{"detail":"A matching access grant already exists"}`, false, false},
		{502, `<html>bad gateway</html>`, false, false},
	} {
		refusal := accessResponseError("op", testCase.status, []byte(testCase.body))
		var refused *ClusterAccessRefusalError
		isPolicy := errors.As(refusal, &refused) && refused.PolicyRefusal()
		var denied *PermissionDeniedError
		isPermission := errors.As(refusal, &denied)
		if isPolicy != testCase.policy || isPermission != testCase.permission {
			t.Fatalf("%d %s: policy=%v permission=%v, want policy=%v permission=%v",
				testCase.status, testCase.body, isPolicy, isPermission, testCase.policy, testCase.permission)
		}
	}
	denied := &PermissionDeniedError{}
	if !errors.As(accessResponseError("op", 403, []byte(`{"detail":"permission_denied","permission":"kube_access.elevate"}`)), &denied) ||
		denied.Permission != "kube_access.elevate" {
		t.Fatalf("the denied permission was lost: %+v", denied)
	}
}

func TestListClusterAccessGrantsKeepsOverPolicyAndSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"result":[` +
			`{"id":"g-1","role":"admin","scope":"cluster","over_policy":true,"source":"role"},` +
			`{"id":"g-2","role":"view","scope":"cluster","over_policy":null,"source":null}]}`))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}
	grants, err := apiClient.ListClusterAccessGrants(context.Background(), "c-1")
	if err != nil {
		t.Fatal(err)
	}
	first, second := grants.Result[0], grants.Result[1]
	if first.OverPolicy == nil || !*first.OverPolicy || first.Source == nil || *first.Source != ClusterAccessGrantSourceRole {
		t.Fatalf("first grant lost over_policy or source: %+v", first)
	}
	if second.OverPolicy != nil || second.Source != nil {
		t.Fatalf("a null over_policy or source must stay nil, never false or manual: %+v", second)
	}
	encoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"over_policy":null`) || !strings.Contains(string(encoded), `"source":null`) {
		t.Fatalf("null must stay null in JSON output, got %s", encoded)
	}
}

func TestListClusterAccessGrantsRefusalIsAnRBACDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"Only organisation admins can view cluster access"}`))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}
	_, err := apiClient.ListClusterAccessGrants(context.Background(), "c-1")
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) || err.Error() != "Only organisation admins can view cluster access" {
		t.Fatalf("expected the platform sentence as an RBAC denial, got %v", err)
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
	read, err := apiClient.GetClusterAccessPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policy := read.Policy
	if policy.MaxGrantRole != "edit" || policy.ElevatedMaxTTLSeconds == nil || *policy.ElevatedMaxTTLSeconds != 14400 ||
		policy.RequireReasonFromRole != nil || !policy.IsConfigured {
		t.Fatalf("policy = %+v", policy)
	}
	if read.Describe() != "" {
		t.Fatalf("a platform that names no organisation must describe none, got %q", read.Describe())
	}
}

// TestGetClusterAccessPolicyReadsTheOrganisation pins ankra-gsjnfb.5: the
// read carries the organisation the platform read the policy in, so a read
// made in the wrong organisation can be told from the right one.
func TestGetClusterAccessPolicyReadsTheOrganisation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"policy":{"creator_grant_role":"view","max_grant_role":"edit","elevated_max_ttl_seconds":null,"require_reason_from_role":null,"is_configured":true},"organisation_id":"2f853b14-78b0-471a-937c-1ee13a08ddff","organisation_name":"Depict"}`))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}
	read, err := apiClient.GetClusterAccessPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if read.OrganisationID != "2f853b14-78b0-471a-937c-1ee13a08ddff" || read.Describe() != "Depict (2f853b14-78b0-471a-937c-1ee13a08ddff)" ||
		read.Policy.MaxGrantRole != "edit" {
		t.Fatalf("read = %+v (%q)", read, read.Describe())
	}
	unnamed := ClusterAccessPolicyOrganisation{OrganisationID: "o-1"}
	if unnamed.Describe() != "o-1" {
		t.Fatalf("an organisation without a name is described by its id, got %q", unnamed.Describe())
	}
}
