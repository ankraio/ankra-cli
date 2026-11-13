package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	ticketOperationTestID  = "5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c"
	operationTestTicket    = `{"id":"` + ticketOperationTestID + `","ticket_number":42,"status":"open","plan_status":"drafted","assignee_agent_name":"sre","workspace_id":null}`
	ticketSyncTestPath     = "/api/v1/org/ai-tickets/sync/connections"
	ticketSyncTestDocument = `{"id":"` + ticketOperationTestID + `","name":"jira","provider":"jira","config":{"base_url":"https://example.atlassian.net","project_key":"OPS"},
		"enabled":true,"credential_configured":true,"sync_kinds":[],"sync_min_priority":"low","sync_event_scope":"all","last_synced_at":null,"last_error":null}`
)

func TestTicketSyncCreateSendsTheSecretInTheBodyAndNowhereElse(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{"POST " + ticketSyncTestPath: {http.StatusCreated, ticketSyncTestDocument}})
	stdout, stderr, runError := runDocumentCommand(t, server, newTicketsSyncCommand(), organisationParitySecret+"\n",
		"connections", "create", "--name", "jira", "--provider", "jira", "--base-url", "https://example.atlassian.net",
		"--project-key", "OPS", "--email", "ops@example.com", "--kind", "incident", "--secret-stdin")
	if runError != nil {
		t.Fatalf("create: %v", runError)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
	requireDocumentCalls(t, calls, "POST "+ticketSyncTestPath)
	call := (*calls)[0]
	body := decodeDocumentCallBody(t, call)
	credential, _ := body["credential"].(map[string]any)
	config, _ := body["config"].(map[string]any)
	if credential["api_token"] != organisationParitySecret || credential["email"] != "ops@example.com" ||
		config["project_key"] != "OPS" || body["provider"] != "jira" || strings.Contains(call.query, organisationParitySecret) {
		t.Fatalf("create sent %s ?%s", call.body, call.query)
	}
	if strings.Count(call.body, organisationParitySecret) != 1 || !strings.Contains(stdout, "held by the platform (never shown)") {
		t.Errorf("body %s, stdout %q", call.body, stdout)
	}
}

func TestTicketSyncWritesRedactTheSecretFromARefusalAndNeverTakeItAsAnArgument(t *testing.T) {
	organisationPromptsNotInteractive(t)
	refusal := documentRoute{http.StatusBadGateway, `{"detail":"The tracker rejected ` + organisationParitySecret + `."}`}
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + ticketSyncTestPath + "/" + ticketOperationTestID: {http.StatusOK, ticketSyncTestDocument},
		"PUT " + ticketSyncTestPath + "/" + ticketOperationTestID: refusal,
		"POST /api/v1/org/ai-tickets/sync/destinations":           refusal,
	})
	stdout, stderr, runError := runDocumentCommand(t, server, newTicketsSyncCommand(), organisationParitySecret,
		"connections", "update", ticketOperationTestID, "--issue-type", "Bug", "--rotate-secret", "--email", "ops@example.com", "--secret-stdin")
	if runError == nil || !strings.Contains(runError.Error(), "[redacted]") {
		t.Fatalf("update error = %v, want the platform's refusal with the secret redacted", runError)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
	body := decodeDocumentCallBody(t, (*calls)[1])
	config, _ := body["config"].(map[string]any)
	credential, _ := body["credential"].(map[string]any)
	if config["issue_type"] != "Bug" || config["project_key"] != "OPS" || credential["api_token"] != organisationParitySecret {
		t.Fatalf("update sent %v, want the stored config with the change laid over it and the new credential", body)
	}

	stdout, stderr, runError = runDocumentCommand(t, server, newTicketsSyncCommand(), organisationParitySecret,
		"destinations", "--provider", "linear", "--secret-stdin")
	if runError == nil {
		t.Fatal("destinations: want the platform's refusal")
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)

	*calls = nil
	for _, arguments := range [][]string{
		{"connections", "create", "--name", "linear", "--provider", "linear", "--team-id", "team-1"},
		{"connections", "create", "--name", "linear", "--provider", "linear", "--url", "https://example.com", "--secret-stdin"},
		{"connections", "create", "--name", "jira", "--provider", "jira", "--unsigned"},
		{"connections", "delete", ticketOperationTestID},
		{"destinations", "--provider", "github", "--secret-stdin"},
	} {
		if _, _, usageError := runDocumentCommand(t, server, newTicketsSyncCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage {
			t.Errorf("%v: error = %v, want a usage error", arguments, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("refused commands still sent %v", *calls)
	}
}

func TestTicketAssignPlanAndWorkspaceSendWhatWasAsked(t *testing.T) {
	const ticketPath = "/api/v1/org/ai-tickets/" + ticketOperationTestID
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + ticketPath:                   {http.StatusOK, operationTestTicket},
		"POST " + ticketPath + "/assign":      {http.StatusOK, operationTestTicket},
		"POST " + ticketPath + "/plan":        {http.StatusOK, operationTestTicket},
		"POST " + ticketPath + "/plan/review": {http.StatusConflict, `{"detail":"The ticket changed since it was read."}`},
		"POST " + ticketPath + "/workspace":   {http.StatusOK, operationTestTicket},
	})
	stdout, _, runError := runDocumentCommand(t, server, newTicketsAssignCommand(), "", ticketOperationTestID, "--agent", "task-1")
	if runError != nil || !strings.Contains(stdout, "T-42 is assigned to sre (agent).") {
		t.Fatalf("assign: stdout %q, error %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[1]); body["assignee_agent_task_id"] != "task-1" || body["assignee_user_id"] != nil {
		t.Fatalf("assign sent %v", body)
	}
	if _, _, usageError := runDocumentCommand(t, server, newTicketsAssignCommand(), "", ticketOperationTestID, "--agent", "a", "--unassign"); exitCodeFor(usageError) != exitUsage {
		t.Errorf("assign with two targets: error = %v, want a usage error", usageError)
	}

	*calls = nil
	if _, _, runError = runDocumentCommand(t, server, newTicketsPlanCommand(), "1. Renew it\n", "draft", ticketOperationTestID, "--file", "-"); runError != nil {
		t.Fatalf("plan draft: %v", runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[1]); body["plan_document"] != "1. Renew it\n" {
		t.Fatalf("plan draft sent %v", body)
	}
	_, _, reviewError := runDocumentCommand(t, server, newTicketsPlanCommand(), "", "review", ticketOperationTestID, "--verdict", "approve")
	if exitCodeFor(reviewError) != exitError || !strings.Contains(reviewError.Error(), "changed since it was read") {
		t.Errorf("plan review refusal = %v (exit %d)", reviewError, exitCodeFor(reviewError))
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newTicketsWorkspaceCommand(), "", ticketOperationTestID, "--detach")
	if runError != nil || !strings.Contains(stdout, "T-42 has no workspace attached.") {
		t.Fatalf("workspace detach: stdout %q, error %v", stdout, runError)
	}
	if workspaceID, isSent := decodeDocumentCallBody(t, (*calls)[1])["workspace_id"]; !isSent || workspaceID != nil {
		t.Fatalf("workspace detach sent %s, want workspace_id null", (*calls)[1].body)
	}
}

func TestRegistryConnectionCheckThatFailsPrintsWhyAndExitsOne(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST /api/v1/org/registry-connections/verify":   {http.StatusOK, `{"ok":false,"detail":"The registry refused the credential.","host":"harbor.example.com","provider":"harbor","project_count":null}`},
		"POST /api/v1/org/registry-connections/discover": {http.StatusOK, `{"ok":true,"detail":"Reached Harbor.","host":"harbor.example.com","provider":"harbor","projects":[{"name":"platform","public":false,"repository_count":4}],"truncated":false}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newRegistryConnectionsCommand(), "", "verify", "--registry", "harbor", "--admin-credential", "harbor-admin")
	if exitCodeFor(runError) != exitError || !strings.Contains(stdout, "The registry refused the credential.") {
		t.Fatalf("verify: stdout %q, error %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["registry"] != "harbor" || body["admin_credential_name"] != "harbor-admin" || len(body) != 2 {
		t.Fatalf("verify sent %v, want the registry and the credential's name only", body)
	}
	stdout, _, runError = runDocumentCommand(t, server, newRegistryConnectionsCommand(), "", "discover", "--url", "oci://harbor.example.com", "--admin-credential", "harbor-admin")
	if runError != nil || !strings.Contains(stdout, "platform") {
		t.Fatalf("discover: stdout %q, error %v", stdout, runError)
	}
	if _, _, usageError := runDocumentCommand(t, server, newRegistryConnectionsCommand(), "", "discover", "--url", "oci://harbor.example.com"); exitCodeFor(usageError) != exitUsage {
		t.Errorf("discover without a credential: error %v, want a usage error", usageError)
	}
}

func TestPropagationMemoryProfileAndAutonomousActionsRender(t *testing.T) {
	organisationPromptsNotInteractive(t)
	const profile = `{"profile":{"communication_style":"terse","preferred_tools":["ankra","jq"]},"learning_enabled":false,"turns_observed":12,"last_extracted_at":null}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/credentials/" + ticketOperationTestID + "/propagation": {http.StatusOK, `{"rotation_id":"r-1","revision":3,"rotated_at":"2026-11-10T00:00:00Z",
			"safe_to_revoke":false,"blocking_clusters":[{"cluster_name":"prod"}],
			"clusters":[{"cluster_name":"prod","coverage":"not_covered","state":"not_covered","reason":"Nothing Ankra renders carries it.","remedy":"ankra cluster apply -f prod.yaml"}]}`},
		"PUT /api/v1/org/ai-memory/profile":    {http.StatusOK, profile},
		"DELETE /api/v1/org/ai-memory/profile": {http.StatusNoContent, ""},
		"GET /api/v1/org/applications/autonomous-actions": {http.StatusOK, `{"actions":[{"id":"a-1","kind":"auto_remediation","status":"succeeded",
			"occurred_at":"2026-11-10T00:00:00Z","rule":"auto_remediation","undo":{"available":false,"reason":"Not yet."},"applications":[{"id":"1","name":"shop"}]}],
			"capped":true,"capped_sources":["build_fixes"],"unavailable_sources":[]}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newCredentialsPropagationCommand(), "", ticketOperationTestID)
	if runError != nil || !strings.Contains(stdout, "NOT safe to revoke yet: 1 cluster still blocks it") || !strings.Contains(stdout, "prod: ankra cluster apply -f prod.yaml") {
		t.Fatalf("propagation: stdout %q, error %v", stdout, runError)
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newAIMemoriesProfileCommand(), "", "set", "--learning", "off")
	if runError != nil || !strings.Contains(stdout, "Learning: off (12 turns observed") || !strings.Contains(stdout, "preferred tools: ankra; jq") {
		t.Fatalf("profile set: stdout %q, error %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["learning_enabled"] != false || len(body) != 1 {
		t.Fatalf("profile set sent %v", body)
	}
	if _, _, usageError := runDocumentCommand(t, server, newAIMemoriesProfileCommand(), "", "set"); exitCodeFor(usageError) != exitUsage {
		t.Errorf("profile set with nothing to change: error %v, want a usage error", usageError)
	}
	if _, _, refusedError := runDocumentCommand(t, server, newAIMemoriesProfileCommand(), "", "reset"); exitCodeFor(refusedError) != exitUsage {
		t.Errorf("profile reset without --yes: error %v, want a usage error", refusedError)
	}
	if stdout, _, runError = runDocumentCommand(t, server, newAIMemoriesProfileCommand(), "", "reset", "--yes"); runError != nil || !strings.Contains(stdout, "Learned profile reset.") {
		t.Fatalf("profile reset: stdout %q, error %v", stdout, runError)
	}

	stdout, _, runError = runDocumentCommand(t, server, newApplicationAutonomyActionsCommand(), "")
	if runError != nil || !strings.Contains(stdout, "shop") || !strings.Contains(stdout, "More actions exist than are listed for: build_fixes.") {
		t.Fatalf("autonomy actions: stdout %q, error %v", stdout, runError)
	}
}

func (m baseMock) AssignTicket(ctx context.Context, ticketID string, request client.TicketAssignRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DraftTicketPlan(ctx context.Context, ticketID string, planDocument string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ReviewTicketPlan(ctx context.Context, ticketID string, request client.TicketPlanReviewRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) SetTicketWorkspace(ctx context.Context, ticketID string, workspaceID *string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListTicketSyncConnections(ctx context.Context) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetTicketSyncConnection(ctx context.Context, connectionID string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateTicketSyncConnection(ctx context.Context, request client.TicketSyncConnectionRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateTicketSyncConnection(ctx context.Context, connectionID string, request client.TicketSyncConnectionRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteTicketSyncConnection(ctx context.Context, connectionID string) error {
	return errors.New("not implemented")
}

func (m baseMock) ListTicketSyncDestinations(ctx context.Context, request client.TicketSyncDestinationsRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetAIMemoryProfile(ctx context.Context) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateAIMemoryProfile(ctx context.Context, update client.AIMemoryProfileUpdate) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ResetAIMemoryProfile(ctx context.Context) error {
	return errors.New("not implemented")
}

func (m baseMock) DiscoverRegistryConnection(ctx context.Context, registryURL string, adminCredentialName string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) VerifyRegistryConnection(ctx context.Context, registry string, adminCredentialName string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetCredentialPropagation(ctx context.Context, credentialID string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListApplicationAutonomousActions(ctx context.Context) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}
