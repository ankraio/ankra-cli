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

const proposalBody = `{"id":"p-1","status":"pending",
"proposed":{"creator_grant_role":"view","max_grant_role":"edit","elevated_max_ttl_seconds":14400,"require_reason_from_role":null},
"base":{"creator_grant_role":"cluster-admin","max_grant_role":"cluster-admin","elevated_max_ttl_seconds":null,"require_reason_from_role":null,"is_configured":false},
"current":{"creator_grant_role":"cluster-admin","max_grant_role":"cluster-admin","elevated_max_ttl_seconds":null,"require_reason_from_role":null,"is_configured":false},
"base_matches_current":true,"reason":"tighten","proposed_by":{"user_id":"u-1","principal":"ops@example.com","token_id":"t-1","token_name":"laptop"},
"created_at":"2026-10-19T10:00:00Z","expires_at":"2026-10-26T10:00:00Z","decided_by_user_id":null,"decided_at":null,"decision_note":null,
"apply_url":"https://platform.ankra.app/next/organisation/roles?org=o-1&proposal=p-1"}`

func TestProposeClusterAccessPolicySendsOnlyTheFieldsItSets(t *testing.T) {
	var seen string
	var seenBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = request.Method + " " + request.URL.Path + " " + request.Header.Get("Authorization")
		_ = json.NewDecoder(request.Body).Decode(&seenBody)
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(proposalBody))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}

	maxRole := "edit"
	proposal, err := apiClient.ProposeClusterAccessPolicy(context.Background(), ProposeClusterAccessPolicyRequest{
		MaxGrantRole: &maxRole, ClearRequireReasonFromRole: true, Reason: "tighten",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "POST /api/v1/org/cluster-access-policy/proposals Bearer t" {
		t.Fatalf("request = %q", seen)
	}
	if string(seenBody["max_grant_role"]) != `"edit"` || string(seenBody["reason"]) != `"tighten"` ||
		string(seenBody["require_reason_from_role"]) != "null" {
		t.Fatalf("body = %v", seenBody)
	}
	for _, omitted := range []string{"creator_grant_role", "elevated_max_ttl_seconds"} {
		if _, present := seenBody[omitted]; present {
			t.Fatalf("%s must be omitted so the live value is kept, body = %v", omitted, seenBody)
		}
	}
	if proposal.ID != "p-1" || proposal.Status != ProposalStatusPending || proposal.Proposed.MaxGrantRole != "edit" ||
		proposal.Proposed.ElevatedMaxTTLSeconds == nil || *proposal.Proposed.ElevatedMaxTTLSeconds != 14400 ||
		proposal.Current.MaxGrantRole != "cluster-admin" || !proposal.BaseMatchesCurrent ||
		proposal.ProposedBy.TokenName == nil || *proposal.ProposedBy.TokenName != "laptop" ||
		!strings.Contains(proposal.ApplyURL, "proposal=p-1") || proposal.IsFinal() {
		t.Fatalf("proposal = %+v", proposal)
	}
}

func TestProposeClusterAccessPolicyNamesTheRefusal(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		wantText   string
		isRejected bool
		isDenied   bool
	}{
		{"unchanged", 422, `{"detail":[{"type":"value_error","loc":["body"],"msg":"Value error, the proposed policy is identical to the current policy","input":{}}]}`,
			"the proposed policy is identical to the current policy", true, false},
		{"field", 422, `{"detail":[{"type":"literal_error","loc":["body","max_grant_role"],"msg":"Input should be 'view', 'edit', 'admin' or 'cluster-admin'"}]}`,
			"max_grant_role: Input should be", true, false},
		{"admin only", 403, `{"detail":"Only organisation admins can manage cluster access"}`, "Only organisation admins", false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()
			apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}
			maxRole := "edit"
			_, err := apiClient.ProposeClusterAccessPolicy(context.Background(),
				ProposeClusterAccessPolicyRequest{MaxGrantRole: &maxRole, Reason: "x"})
			if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("err = %v, want %q", err, testCase.wantText)
			}
			var rejected *ClusterAccessPolicyProposalRejectedError
			if errors.As(err, &rejected) != testCase.isRejected {
				t.Fatalf("rejected = %v for %v", !testCase.isRejected, err)
			}
			var denied *PermissionDeniedError
			if errors.As(err, &denied) != testCase.isDenied {
				t.Fatalf("denied = %v for %v", !testCase.isDenied, err)
			}
		})
	}
}

func TestGetAndListClusterAccessPolicyProposals(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.Method+" "+request.URL.RequestURI())
		switch request.URL.Path {
		case "/api/v1/org/cluster-access-policy/proposals/p-1":
			_, _ = writer.Write([]byte(proposalBody))
		case "/api/v1/org/cluster-access-policy/proposals":
			_, _ = writer.Write([]byte(`{"proposals":[` + proposalBody + `],"is_capped":true}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Cluster access policy proposal not found"}`))
		}
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}

	proposal, err := apiClient.GetClusterAccessPolicyProposal(context.Background(), "p-1")
	if err != nil || proposal.ID != "p-1" {
		t.Fatalf("get = %+v, %v", proposal, err)
	}
	list, err := apiClient.ListClusterAccessPolicyProposals(context.Background(), ProposalStatusPending)
	if err != nil || len(list.Proposals) != 1 || !list.IsCapped {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := apiClient.ListClusterAccessPolicyProposals(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	_, err = apiClient.GetClusterAccessPolicyProposal(context.Background(), "p-2")
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown proposal must keep its 404, got %v", err)
	}
	want := []string{
		"GET /api/v1/org/cluster-access-policy/proposals/p-1",
		"GET /api/v1/org/cluster-access-policy/proposals?status=pending",
		"GET /api/v1/org/cluster-access-policy/proposals",
		"GET /api/v1/org/cluster-access-policy/proposals/p-2",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s", strings.Join(seen, "\n"))
	}
}
