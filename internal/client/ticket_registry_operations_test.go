package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestTicketRegistryAndProfileOperationRoutesSendTheContract(t *testing.T) {
	const tickets = "/api/v1/org/ai-tickets"
	ctx := context.Background()
	agentTaskID, enabled, noKinds := "task-1", false, []string{}
	requireOperationContract(t, []operationCase{
		{"ticket assign", func(c *Client) (json.RawMessage, error) {
			return c.AssignTicket(ctx, "t-1", TicketAssignRequest{AssigneeAgentTaskID: &agentTaskID})
		}, "POST " + tickets + "/t-1/assign", `{"assignee_agent_task_id":"task-1","assignee_user_id":null}`},
		{"ticket unassign", func(c *Client) (json.RawMessage, error) { return c.AssignTicket(ctx, "t-1", TicketAssignRequest{}) },
			"POST " + tickets + "/t-1/assign", `{"assignee_agent_task_id":null,"assignee_user_id":null}`},
		{"ticket plan", func(c *Client) (json.RawMessage, error) { return c.DraftTicketPlan(ctx, "t-1", "1. Do it") },
			"POST " + tickets + "/t-1/plan", `{"plan_document":"1. Do it"}`},
		{"ticket plan review", func(c *Client) (json.RawMessage, error) {
			return c.ReviewTicketPlan(ctx, "t-1", TicketPlanReviewRequest{Verdict: "approve"})
		}, "POST " + tickets + "/t-1/plan/review", `{"verdict":"approve"}`},
		{"ticket workspace attach", func(c *Client) (json.RawMessage, error) { return c.SetTicketWorkspace(ctx, "t-1", &agentTaskID) },
			"POST " + tickets + "/t-1/workspace", `{"workspace_id":"task-1"}`},
		{"ticket workspace detach", func(c *Client) (json.RawMessage, error) { return c.SetTicketWorkspace(ctx, "t-1", nil) },
			"POST " + tickets + "/t-1/workspace", `{"workspace_id":null}`},
		{"sync connections list", func(c *Client) (json.RawMessage, error) { return c.ListTicketSyncConnections(ctx) },
			"GET " + tickets + "/sync/connections", ""},
		{"sync connection get", func(c *Client) (json.RawMessage, error) { return c.GetTicketSyncConnection(ctx, "s-1") },
			"GET " + tickets + "/sync/connections/s-1", ""},
		{"sync connection update", func(c *Client) (json.RawMessage, error) {
			return c.UpdateTicketSyncConnection(ctx, "s-1", TicketSyncConnectionRequest{Enabled: &enabled, SyncKinds: &noKinds})
		}, "PUT " + tickets + "/sync/connections/s-1", `{"enabled":false,"sync_kinds":[]}`},
		{"sync destinations of a saved connection", func(c *Client) (json.RawMessage, error) {
			return c.ListTicketSyncDestinations(ctx, TicketSyncDestinationsRequest{Provider: "linear", ConnectionID: "s-1"})
		}, "POST " + tickets + "/sync/destinations", `{"provider":"linear","connection_id":"s-1"}`},

		{"registry discover", func(c *Client) (json.RawMessage, error) {
			return c.DiscoverRegistryConnection(ctx, "oci://harbor.example.com", "harbor-admin")
		}, "POST /api/v1/org/registry-connections/discover", `{"admin_credential_name":"harbor-admin","url":"oci://harbor.example.com"}`},
		{"registry verify", func(c *Client) (json.RawMessage, error) {
			return c.VerifyRegistryConnection(ctx, "harbor", "harbor-admin")
		}, "POST /api/v1/org/registry-connections/verify", `{"admin_credential_name":"harbor-admin","registry":"harbor"}`},
		{"credential propagation", func(c *Client) (json.RawMessage, error) { return c.GetCredentialPropagation(ctx, "cred-1") },
			"GET /api/v1/org/credentials/cred-1/propagation", ""},
		{"memory profile get", func(c *Client) (json.RawMessage, error) { return c.GetAIMemoryProfile(ctx) },
			"GET /api/v1/org/ai-memory/profile", ""},
		{"memory profile update", func(c *Client) (json.RawMessage, error) {
			return c.UpdateAIMemoryProfile(ctx, AIMemoryProfileUpdate{LearningEnabled: &enabled})
		}, "PUT /api/v1/org/ai-memory/profile", `{"learning_enabled":false}`},
		{"autonomous actions", func(c *Client) (json.RawMessage, error) { return c.ListApplicationAutonomousActions(ctx) },
			"GET /api/v1/org/applications/autonomous-actions", ""},
	})
}

func TestTicketSyncAndProfileDeletesSendNoBodyAndAcceptAnEmptyAnswer(t *testing.T) {
	ctx := context.Background()
	for wantLine, call := range map[string]func(c *Client) error{
		"DELETE /api/v1/org/ai-tickets/sync/connections/s-1": func(c *Client) error { return c.DeleteTicketSyncConnection(ctx, "s-1") },
		"DELETE /api/v1/org/ai-memory/profile":               func(c *Client) error { return c.ResetAIMemoryProfile(ctx) },
	} {
		apiClient, received := operationClient(t, http.StatusNoContent, "")
		if callError := call(apiClient); callError != nil || received.line != wantLine || received.body != "" {
			t.Errorf("sent %s %q, error %v; want %s with no body", received.line, received.body, callError, wantLine)
		}
	}
}

func TestTicketSyncSecretTravelsInTheRequestBodyOnly(t *testing.T) {
	const secret = "lin_api_s3cr3t-value"
	ctx := context.Background()
	apiClient, received := operationClient(t, http.StatusCreated, `{"id":"s-1","credential_configured":true}`)
	document, createError := apiClient.CreateTicketSyncConnection(ctx, TicketSyncConnectionRequest{Name: "linear", Provider: "linear",
		Config: map[string]string{"team_id": "team-1"}, Credential: map[string]string{"api_key": secret}})
	if createError != nil || strings.Contains(string(document), secret) {
		t.Fatalf("create = %s, %v", document, createError)
	}
	if received.line != "POST /api/v1/org/ai-tickets/sync/connections" ||
		received.body != `{"name":"linear","provider":"linear","config":{"team_id":"team-1"},"credential":{"api_key":"`+secret+`"}}` {
		t.Errorf("sent %s %s", received.line, received.body)
	}
	if strings.Contains(received.line, secret) || strings.Contains(received.headers, secret) {
		t.Errorf("the secret left the request body: %s\n%s", received.line, received.headers)
	}

	_, _ = apiClient.ListTicketSyncDestinations(ctx, TicketSyncDestinationsRequest{Provider: "linear",
		Credential: map[string]string{"api_key": secret}})
	if received.body != `{"provider":"linear","credential":{"api_key":"`+secret+`"}}` ||
		strings.Contains(received.line, secret) || strings.Contains(received.headers, secret) {
		t.Errorf("destinations sent %s %s", received.line, received.body)
	}
}

func TestOperationDocumentRefusalsKeepTheirStatusAndNeverEchoTheRequest(t *testing.T) {
	const secret = "jira-token-s3cr3t"
	ctx := context.Background()
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusBadGateway} {
		apiClient, _ := operationClient(t, status, `{"detail":"The jira provider rejected this credential."}`)
		_, updateError := apiClient.UpdateTicketSyncConnection(ctx, "s-1", TicketSyncConnectionRequest{
			Credential: map[string]string{"email": "ops@example.com", "api_token": secret}})
		var unexpected *UnexpectedResponseError
		if !errors.As(updateError, &unexpected) || unexpected.StatusCode != status || strings.Contains(updateError.Error(), secret) {
			t.Errorf("status %d: error = %v", status, updateError)
		}
	}
	apiClient, _ := operationClient(t, http.StatusUnauthorized, ``)
	if _, readError := apiClient.GetCredentialPropagation(ctx, "cred-1"); !errors.Is(readError, ErrUnauthorized) {
		t.Errorf("401 error = %v, want ErrUnauthorized", readError)
	}
	apiClient, _ = operationClient(t, http.StatusForbidden, `{"detail":"permission_denied","permission":"ai.manage"}`)
	var denied *PermissionDeniedError
	if deleteError := apiClient.DeleteTicketSyncConnection(ctx, "s-1"); !errors.As(deleteError, &denied) {
		t.Errorf("403 error = %v, want a PermissionDeniedError", deleteError)
	}
}
