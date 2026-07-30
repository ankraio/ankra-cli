package cmd

// Tests for the agent-mode approval surface: an `action_proposal` frame must
// never be dropped (the write is halted behind it), and confirm/reject must
// reach the API with the right body and translate a drift conflict into the
// --force hint.

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type chatActionMock struct {
	baseMock

	confirmRequests []client.ConfirmChatActionRequest
	confirmResult   *client.ConfirmChatActionResult
	confirmError    error

	pendingResult *client.PendingChatActionsResult
}

func (m *chatActionMock) ConfirmChatAction(request client.ConfirmChatActionRequest) (*client.ConfirmChatActionResult, error) {
	m.confirmRequests = append(m.confirmRequests, request)
	if m.confirmError != nil {
		return nil, m.confirmError
	}
	return m.confirmResult, nil
}

func (m *chatActionMock) ListPendingChatActions(conversationID string) (*client.PendingChatActionsResult, error) {
	return m.pendingResult, nil
}

func TestChatActionsConfirmSendsConfirmedTrue(t *testing.T) {
	mock := &chatActionMock{confirmResult: &client.ConfirmChatActionResult{Success: true, Status: "executing"}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		if _, commandError := executeCommand("chat", "actions", "confirm", "action-1"); commandError != nil {
			t.Fatalf("confirm: %v", commandError)
		}
	})

	if len(mock.confirmRequests) != 1 {
		t.Fatalf("expected one confirm call, got %d", len(mock.confirmRequests))
	}
	request := mock.confirmRequests[0]
	if request.ActionID != "action-1" || !request.Confirmed {
		t.Errorf("request = %+v, want action-1 confirmed=true", request)
	}
	if request.Force != nil {
		t.Errorf("Force = %v, want nil when --force is not passed", *request.Force)
	}
	if !strings.Contains(stdoutOutput, "confirmed") {
		t.Errorf("expected a confirmation line, got: %s", stdoutOutput)
	}
}

func TestChatActionsRejectSendsConfirmedFalse(t *testing.T) {
	mock := &chatActionMock{confirmResult: &client.ConfirmChatActionResult{Success: true}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		if _, commandError := executeCommand("chat", "actions", "reject", "action-2", "--reason", "not now"); commandError != nil {
			t.Fatalf("reject: %v", commandError)
		}
	})

	request := mock.confirmRequests[0]
	if request.Confirmed {
		t.Error("Confirmed = true, want false for reject")
	}
	if request.Reason == nil || *request.Reason != "not now" {
		t.Errorf("Reason = %v, want \"not now\"", request.Reason)
	}
	if !strings.Contains(stdoutOutput, "rejected") {
		t.Errorf("expected a rejection line, got: %s", stdoutOutput)
	}
}

func TestChatActionsConfirmForwardsForce(t *testing.T) {
	mock := &chatActionMock{confirmResult: &client.ConfirmChatActionResult{Success: true}}
	setMockClient(t, mock)

	captureStdout(t, func() {
		_, _ = executeCommand("chat", "actions", "confirm", "action-3",
			"--force", "--force-reason", "unrelated label drift")
	})

	request := mock.confirmRequests[0]
	if request.Force == nil || !*request.Force {
		t.Error("expected Force=true to be forwarded")
	}
	if request.ForceReason == nil || *request.ForceReason != "unrelated label drift" {
		t.Errorf("ForceReason = %v, want the supplied reason", request.ForceReason)
	}
}

func TestChatActionsConfirmDriftSuggestsForce(t *testing.T) {
	mock := &chatActionMock{confirmError: &client.ActionConflictError{
		ErrorCode: client.ActionErrorCodeDrifted,
		Detail:    "Cluster state changed since this action was proposed",
	}}
	setMockClient(t, mock)

	_, commandError := executeCommand("chat", "actions", "confirm", "action-4")
	if commandError == nil {
		t.Fatal("expected the drift conflict to surface as an error")
	}
	if !strings.Contains(commandError.Error(), "--force") {
		t.Errorf("expected the --force hint, got: %v", commandError)
	}
	if !strings.Contains(commandError.Error(), "action-4") {
		t.Errorf("expected the ready-to-run command with the action id, got: %v", commandError)
	}
}

func TestChatActionsConfirmSupersededDoesNotSuggestForce(t *testing.T) {
	mock := &chatActionMock{confirmError: &client.ActionConflictError{
		ErrorCode: client.ActionErrorCodeSuperseded,
		Detail:    "A newer action replaced this one",
	}}
	setMockClient(t, mock)

	_, commandError := executeCommand("chat", "actions", "confirm", "action-5")
	if commandError == nil {
		t.Fatal("expected the supersede conflict to surface as an error")
	}
	if strings.Contains(commandError.Error(), "--force") {
		t.Errorf("a superseded action is not forceable, but the error offered --force: %v", commandError)
	}
}

func TestDecodeActionProposalReadsTheSSEPayload(t *testing.T) {
	var payload any
	if unmarshalError := json.Unmarshal([]byte(`{
		"action_id": "6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77",
		"tool_name": "restart_node",
		"description": "Restart worker-1",
		"parameters": {"node_id": "node-1"},
		"risk_level": "medium",
		"reversible": true,
		"expires_in_seconds": 300
	}`), &payload); unmarshalError != nil {
		t.Fatalf("seed payload: %v", unmarshalError)
	}

	proposal, decodeError := decodeActionProposal(payload)
	if decodeError != nil {
		t.Fatalf("decodeActionProposal: %v", decodeError)
	}
	if proposal.ActionID != "6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77" {
		t.Errorf("ActionID = %s", proposal.ActionID)
	}
	if proposal.ToolName != "restart_node" || !proposal.Reversible || proposal.RiskLevel != "medium" {
		t.Errorf("proposal = %+v", proposal)
	}
	if proposal.ExpiresInSeconds != 300 {
		t.Errorf("ExpiresInSeconds = %d, want 300", proposal.ExpiresInSeconds)
	}
}

func TestDecodeActionProposalRejectsPayloadWithoutActionID(t *testing.T) {
	if _, decodeError := decodeActionProposal(map[string]any{"tool_name": "restart_node"}); decodeError == nil {
		t.Fatal("expected an error when action_id is absent - it is the only handle for confirming")
	}
}

func TestRenderActionProposalShowsIrreversibilityAndActionID(t *testing.T) {
	stdoutOutput := captureStdout(t, func() {
		renderActionProposal(&client.ChatActionProposal{
			ActionID:    "action-9",
			ToolName:    "delete_cluster",
			Description: "Delete production",
			RiskLevel:   "high",
			Reversible:  false,
		})
	})

	if !strings.Contains(stdoutOutput, "NOT reversible") {
		t.Errorf("an irreversible action must say so, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "action-9") {
		t.Errorf("the action id is needed to confirm, got: %s", stdoutOutput)
	}
}

func TestIsAffirmativeDefaultsToRejecting(t *testing.T) {
	for _, answer := range []string{"y", "Y", "yes", "YES", " yes \n"} {
		if !isAffirmative(answer) {
			t.Errorf("isAffirmative(%q) = false, want true", answer)
		}
	}
	for _, answer := range []string{"", "\n", "n", "no", "sure", "later"} {
		if isAffirmative(answer) {
			t.Errorf("isAffirmative(%q) = true; anything but yes must reject a mutation", answer)
		}
	}
}
