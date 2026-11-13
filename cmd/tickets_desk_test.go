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
	deskTestClusterID          = "7d2c1f0e-3a55-4d0b-9a57-0d5f3c1a2b3c"
	ticketDeskSettingsTestPath = "/api/v1/org/ai-tickets/settings"
	ticketDeskSettingsTestBody = `{"slack_channel_id":"C0123ABCD","teams_conversation_id":null,"reviewer_agent_task_id":null,
		"require_agent_review":true,"auto_file_insight_severities":["critical"],"notify_mode":"filtered","notify_min_priority":"high",
		"notify_source_kinds":[],"notify_event_scope":"important","notify_cluster_environments":["production"],"updated_at":null}`
)

func TestCostWasteListReadsTheScopeAndSaysWhatTheTotalLeavesOut(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{"GET /api/v1/org/cloud-cost/waste": {http.StatusOK, `{
		"has_data":true,"scanned_at":"2026-11-13T06:00:00Z","currency":"EUR","total_monthly_cost_cents":1250,
		"unpriced_finding_count":1,"shared_not_counted_monthly_cost_cents":400,"shared_not_counted_finding_count":1,
		"unattributed_finding_count":0,"unattributed_monthly_cost_cents":0,"consent":{},"scope":{},
		"credential_errors":[{"provider":"hetzner","credential_name":"hetzner-eu","error":"401 Unauthorized","scanned_at":null,"credential_outside_projects":false}],
		"findings":[
			{"id":"f-1","kind":"unattached_volume","resource_name":"pvc-data","provider":"hetzner","region":"fsn1","cluster_name":"prod-eu","monthly_cost_cents":1250,"cleanup_refusal":null},
			{"id":"f-2","kind":"stopped_server","resource_name":"","provider":"","region":"","cluster_name":null,"monthly_cost_cents":400,"cleanup_refusal":{"code":"shared_with_other_organisations","message":"Shared."}}]}`}})
	stdout, _, runError := runDocumentCommand(t, server, newCostWasteCommand(), "", "list", "--cluster", deskTestClusterID, "--all-projects")
	if runError != nil {
		t.Fatalf("waste list: %v", runError)
	}
	requireDocumentCalls(t, calls, "GET /api/v1/org/cloud-cost/waste")
	if query := (*calls)[0].query; !strings.Contains(query, "cluster_id="+deskTestClusterID) || !strings.Contains(query, "project_id=all") {
		t.Fatalf("waste list query = %q, want the cluster and the whole organisation", query)
	}
	assertContainsAll(t, stdout, []string{"f-1", "pvc-data", "refused: shared_with_other_organisations", "2 findings open",
		"1 finding could not be priced", "1 shared finding Ankra will not clean up", "the hetzner scan of hetzner-eu failed: 401 Unauthorized"})
}

func TestTicketDeskSettingsSetSendsOnlyWhatChangesAndOnlyOnceConfirmed(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + ticketDeskSettingsTestPath: {http.StatusOK, ticketDeskSettingsTestBody},
		"PUT " + ticketDeskSettingsTestPath: {http.StatusOK, ticketDeskSettingsTestBody},
	})
	stdout, _, runError := runDocumentCommand(t, server, newTicketsDeskSettingsGetCommand(), "")
	if runError != nil || !strings.Contains(stdout, "C0123ABCD") || !strings.Contains(stdout, "never saved (the defaults)") {
		t.Fatalf("settings get: stdout %q, error %v", stdout, runError)
	}

	*calls = nil
	for _, arguments := range [][]string{
		{"--slack-channel", "C0999"},
		{},
		{"--slack-channel", " ", "--yes"},
	} {
		if _, _, usageError := runDocumentCommand(t, server, newTicketsDeskSettingsSetCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage {
			t.Errorf("%v: error = %v, want a usage error", arguments, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("an unconfirmed or empty change still sent %v", *calls)
	}

	stdout, _, runError = runDocumentCommand(t, server, newTicketsDeskSettingsSetCommand(), "",
		"--slack-channel", "C0999", "--clear-reviewer", "--require-agent-review=false", "--insight-severities", "", "--notify-mode", "none", "--yes")
	if runError != nil || !strings.Contains(stdout, "Ticket desk settings updated.") {
		t.Fatalf("settings set: stdout %q, error %v", stdout, runError)
	}
	requireDocumentCalls(t, calls, "PUT "+ticketDeskSettingsTestPath)
	body := decodeDocumentCallBody(t, (*calls)[0])
	severities, isList := body["auto_file_insight_severities"].([]any)
	reviewer, reviewerSent := body["reviewer_agent_task_id"]
	if body["slack_channel_id"] != "C0999" || !reviewerSent || reviewer != nil || body["require_agent_review"] != false ||
		!isList || len(severities) != 0 || body["notify_mode"] != "none" || len(body) != 5 {
		t.Fatalf("settings set sent %s, want exactly the five changes", (*calls)[0].body)
	}
}

func TestTicketSummaryChangesAndUpdateSendWhatWasAsked(t *testing.T) {
	const ticketPath = "/api/v1/org/ai-tickets/" + ticketOperationTestID
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-tickets/summary": {http.StatusOK, `{"status_counts":{"open":3,"in_progress":1},"needs_human":2,"in_flight":1,
			"done_last_7_days":4,"open_by_kind":{"incident":2},"open_by_priority":{"high":3},"open_assigned_to_viewer":1,"open_by_agent":{},
			"autonomous_resolutions_last_7_days":3,"fixed_resolutions_last_7_days":1,"human_minutes_per_resolution_last_7_days":2.5,
			"human_gate_waiting":1,"human_gate_median_age_seconds":60,"human_gate_p90_age_seconds":120}`},
		"GET " + ticketPath: {http.StatusOK, operationTestTicket},
		"GET " + ticketPath + "/changes": {http.StatusOK, `{"changes":[{"action_id":"a-1","tool_name":"restart_deployment","status":"completed",
			"risk_level":"low","cluster_name":"prod-eu","stack_name":"web","approved_by":"ops@example.com","proposed_at":"2026-11-13T06:00:00Z"}]}`},
		"PATCH " + ticketPath: {http.StatusOK, operationTestTicket},
	})
	stdout, _, runError := runDocumentCommand(t, server, newTicketsSummaryCommand(), "",
		"--kind", "incident", "--priority", "urgent,high", "--cluster", deskTestClusterID, "--needs-human")
	if runError != nil || !strings.Contains(stdout, "in_progress 1, open 3") {
		t.Fatalf("summary: stdout %q, error %v", stdout, runError)
	}
	if query := (*calls)[0].query; !strings.Contains(query, "kind=incident") || !strings.Contains(query, "priority=urgent%2Chigh") ||
		!strings.Contains(query, "cluster_id="+deskTestClusterID) || !strings.Contains(query, "needs_human=true") {
		t.Fatalf("summary query = %q", query)
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newTicketsChangesCommand(), "", ticketOperationTestID)
	if runError != nil || !strings.Contains(stdout, "restart_deployment") || !strings.Contains(stdout, "prod-eu / web") {
		t.Fatalf("changes: stdout %q, error %v", stdout, runError)
	}
	requireDocumentCalls(t, calls, "GET "+ticketPath, "GET "+ticketPath+"/changes")

	*calls = nil
	for _, arguments := range [][]string{
		{ticketOperationTestID, "--priority", "urgent"},
		{ticketOperationTestID, "--yes"},
		{ticketOperationTestID, "--title", " ", "--yes"},
		{ticketOperationTestID, "--title", "\u200b\u200b", "--yes"},
		{ticketOperationTestID, "--due", "2026-11-20", "--yes"},
	} {
		if _, _, usageError := runDocumentCommand(t, server, newTicketsUpdateCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage {
			t.Errorf("%v: error = %v, want a usage error", arguments, usageError)
		}
	}
	for _, call := range *calls {
		if call.method == http.MethodPatch {
			t.Fatalf("a refused update still sent %s", call.body)
		}
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newTicketsUpdateCommand(), "",
		ticketOperationTestID, "--priority", "urgent", "--label", "", "--clear-cluster", "--due", "2026-11-20T17:00:00Z", "--yes")
	if runError != nil || !strings.Contains(stdout, "T-42 is updated") {
		t.Fatalf("update: stdout %q, error %v", stdout, runError)
	}
	requireDocumentCalls(t, calls, "GET "+ticketPath, "PATCH "+ticketPath)
	body := decodeDocumentCallBody(t, (*calls)[1])
	labels, isList := body["labels"].([]any)
	clusterID, clusterSent := body["cluster_id"]
	if body["priority"] != "urgent" || !isList || len(labels) != 0 || !clusterSent || clusterID != nil ||
		body["due_at"] != "2026-11-20T17:00:00Z" || len(body) != 4 {
		t.Fatalf("update sent %s, want exactly the four changes", (*calls)[1].body)
	}
}

func (m baseMock) ListCloudWaste(ctx context.Context, clusterID string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetTicketDeskSettings(ctx context.Context) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateTicketDeskSettings(ctx context.Context, changes map[string]any) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetTicketBoardSummary(ctx context.Context, filter client.TicketSummaryFilter) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListTicketChanges(ctx context.Context, ticketID string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateTicket(ctx context.Context, ticketID string, changes map[string]any) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}
