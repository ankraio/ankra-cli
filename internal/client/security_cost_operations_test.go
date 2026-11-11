package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// operationRequest is what the stand-in platform received for one call.
type operationRequest struct {
	line          string
	body          string
	authorization string
	headers       string
}

// operationClient answers every request with status and answer and records
// the last one.
func operationClient(t *testing.T, status int, answer string) (*Client, *operationRequest) {
	t.Helper()
	received := &operationRequest{}
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		headers := &strings.Builder{}
		_ = r.Header.Write(headers)
		*received = operationRequest{line: r.Method + " " + r.RequestURI, body: string(body),
			authorization: r.Header.Get("Authorization"), headers: headers.String()}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	})
	return apiClient, received
}

// operationCase is one document route: the call, and the request line and
// body it has to send.
type operationCase struct {
	name     string
	call     func(c *Client) (json.RawMessage, error)
	wantLine string
	wantBody string
}

// requireOperationContract makes each call against a stand-in platform and
// fails unless it sent its contract and returned the answer as it was sent.
func requireOperationContract(t *testing.T, cases []operationCase) {
	t.Helper()
	const answer = `{"id":"1","field_from_a_newer_platform":true}`
	for _, testCase := range cases {
		apiClient, received := operationClient(t, http.StatusOK, answer)
		document, callError := testCase.call(apiClient)
		if callError != nil || string(document) != answer {
			t.Errorf("%s: answer = %s, %v; want the body as answered", testCase.name, document, callError)
		}
		if received.line != testCase.wantLine || received.body != testCase.wantBody {
			t.Errorf("%s: sent %s %s, want %s %s", testCase.name, received.line, received.body, testCase.wantLine, testCase.wantBody)
		}
		if received.authorization != "Bearer "+testToken {
			t.Errorf("%s: authorization = %q", testCase.name, received.authorization)
		}
	}
}

func TestSecurityAndCostOperationRoutesSendTheContract(t *testing.T) {
	const security = "/api/v1/org/security"
	ctx := context.Background()
	days := 45
	requireOperationContract(t, []operationCase{
		{"remediations list", func(c *Client) (json.RawMessage, error) {
			return c.ListSecurityRemediations(ctx, SecurityRemediationsOptions{FindingID: "f-1", ApplicationID: "a-1", Limit: 5})
		}, "GET " + security + "/remediations?application_id=a-1&finding_id=f-1&limit=5", ""},
		{"remediation get", func(c *Client) (json.RawMessage, error) { return c.GetSecurityRemediation(ctx, "run/1") },
			"GET " + security + "/remediations/run%2F1", ""},
		{"remediation start", func(c *Client) (json.RawMessage, error) { return c.StartSecurityRemediations(ctx, "f-1") },
			"POST " + security + "/findings/f-1/remediations", ""},
		{"remediation cancel", func(c *Client) (json.RawMessage, error) { return c.CancelSecurityRemediation(ctx, "run-1") },
			"POST " + security + "/remediations/run-1/cancel", ""},
		{"rebuild plan", func(c *Client) (json.RawMessage, error) { return c.GetSecurityFindingRebuildPlan(ctx, "f-1") },
			"GET " + security + "/findings/f-1/rebuild-plan", ""},
		{"rebuild", func(c *Client) (json.RawMessage, error) { return c.RebuildSecurityFinding(ctx, "f-1") },
			"POST " + security + "/findings/f-1/rebuild", ""},
		{"code dispositions list", func(c *Client) (json.RawMessage, error) {
			return c.ListSecurityCodeDispositions(ctx, SecurityCodeDispositionsOptions{RepositoryID: "r-1", IncludeRevoked: true})
		}, "GET " + security + "/code-dispositions?include_revoked=true&repository_id=r-1", ""},
		{"code disposition create", func(c *Client) (json.RawMessage, error) {
			return c.CreateSecurityCodeDisposition(ctx, SecurityCodeDispositionRequest{ApplicationID: "a-1", Tool: "semgrep",
				RuleID: "rule", Path: "main.go", Disposition: "false_positive", Reason: "test fixture"})
		}, "POST " + security + "/code-dispositions",
			`{"application_id":"a-1","tool":"semgrep","rule_id":"rule","path":"main.go","disposition":"false_positive","reason":"test fixture"}`},
		{"code disposition preview", func(c *Client) (json.RawMessage, error) {
			return c.PreviewSecurityCodeDisposition(ctx, SecurityCodeDispositionRequest{RepositoryID: "r-1", Tool: "checkov",
				RuleID: "CKV_1", Path: "main.tf", Disposition: "accepted_risk"})
		}, "POST " + security + "/code-dispositions/preview",
			`{"repository_id":"r-1","tool":"checkov","rule_id":"CKV_1","path":"main.tf","disposition":"accepted_risk"}`},
		{"code disposition revoke", func(c *Client) (json.RawMessage, error) {
			return c.RevokeSecurityCodeDisposition(ctx, "d-1", "fixed upstream")
		}, "POST " + security + "/code-dispositions/d-1/revoke", `{"reason":"fixed upstream"}`},

		{"rate cards list", func(c *Client) (json.RawMessage, error) { return c.ListCostRateCards(ctx) },
			"GET /api/v1/org/cloud-cost/rate-cards", ""},
		{"rate card set", func(c *Client) (json.RawMessage, error) {
			return c.SetCostRateCard(ctx, CostRateCardRequest{CredentialID: CostRateCardOrganisation, Currency: "EUR", VCPUMonthlyAmount: "4.50"})
		}, "PUT /api/v1/org/cloud-cost/rate-cards", `{"credential_id":"organisation","currency":"EUR","vcpu_monthly_amount":"4.50"}`},
		{"waste resolve", func(c *Client) (json.RawMessage, error) {
			return c.ResolveCloudWasteFinding(ctx, "w-1", CloudWasteResolveRequest{FindingIDs: []string{"ignored"},
				Consent: "delete_unattached_volume_acknowledged", MinUnattachedDays: &days})
		}, "POST /api/v1/org/cloud-cost/waste/w-1/resolve", `{"consent":"delete_unattached_volume_acknowledged","min_unattached_days":45}`},
		{"waste resolve batch", func(c *Client) (json.RawMessage, error) {
			return c.ResolveCloudWasteBatch(ctx, CloudWasteResolveRequest{FindingIDs: []string{"w-1", "w-2"}})
		}, "POST /api/v1/org/cloud-cost/waste/resolve-batch", `{"finding_ids":["w-1","w-2"]}`},
		{"waste resolved", func(c *Client) (json.RawMessage, error) { return c.GetResolvedCloudWaste(ctx, 45) },
			"GET /api/v1/org/cloud-cost/waste/resolved?days=45", ""},
		{"cluster estimate read", func(c *Client) (json.RawMessage, error) { return c.GetClusterCostEstimate(ctx, "c-1") },
			"GET /api/v1/org/clusters/c-1/cost/estimate", ""},
		{"cluster estimate draft", func(c *Client) (json.RawMessage, error) {
			return c.EstimateClusterCost(ctx, "c-1", map[string]any{"node_groups": []any{}})
		}, "POST /api/v1/org/clusters/c-1/cost/estimate", `{"node_groups":[]}`},
	})
}

func TestCostRateCardDeleteSendsNoBodyAndAcceptsAnEmptyAnswer(t *testing.T) {
	apiClient, received := operationClient(t, http.StatusNoContent, "")
	deleteError := apiClient.DeleteCostRateCard(context.Background(), CostRateCardOrganisation)
	if deleteError != nil || received.line != "DELETE /api/v1/org/cloud-cost/rate-cards/organisation" || received.body != "" {
		t.Errorf("sent %s %q, error %v", received.line, received.body, deleteError)
	}
}

func TestSecurityOperationReadsCarryTheProjectScopeAndWritesDoNot(t *testing.T) {
	ctx := context.Background()
	apiClient, received := operationClient(t, http.StatusOK, `{}`)
	apiClient.SetSecurityProjectScope(CostProjectScopeAll)
	apiClient.SetCostProjectScope(CostProjectScopeAll)
	_, _ = apiClient.ListSecurityRemediations(ctx, SecurityRemediationsOptions{Limit: 5})
	if received.line != "GET /api/v1/org/security/remediations?limit=5&project_id=all" {
		t.Errorf("remediations list sent %s", received.line)
	}
	_, _ = apiClient.ListSecurityCodeDispositions(ctx, SecurityCodeDispositionsOptions{})
	if received.line != "GET /api/v1/org/security/code-dispositions?project_id=all" {
		t.Errorf("code dispositions list sent %s", received.line)
	}
	_, _ = apiClient.GetResolvedCloudWaste(ctx, 0)
	if received.line != "GET /api/v1/org/cloud-cost/waste/resolved?project_id=all" {
		t.Errorf("resolved waste sent %s", received.line)
	}
	_, _ = apiClient.StartSecurityRemediations(ctx, "f-1")
	if strings.Contains(received.line, "project_id") {
		t.Errorf("a write carried the project scope: %s", received.line)
	}
}
