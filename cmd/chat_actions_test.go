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

// platformConfirmResult decodes a confirm body shaped the way the platform
// writes it (no `success` member; the outcome is `status`) through the
// client's own decoder, so these tests exercise the mapping the CLI ships.
func platformConfirmResult(t *testing.T, status, message string) *client.ConfirmChatActionResult {
	t.Helper()
	body := `{"action_id":"82908385-998d-430d-a5ff-4d1b4ca5e252","status":"` + status +
		`","message":"` + message + `","result":null,"execution_ref":null,"live_status_hint":null,` +
		`"model_visible_summary":"action.apply_stack_changes: ` + status + `","frontend_links":[],"chat_fix_ticket":null}`
	var result client.ConfirmChatActionResult
	if unmarshalError := json.Unmarshal([]byte(body), &result); unmarshalError != nil {
		t.Fatalf("seed result: %v", unmarshalError)
	}
	return &result
}

func TestChatActionsConfirmJSONReportsSuccessForAnExecutedAction(t *testing.T) {
	// ankra-g7307k: -o json printed success:false for actions that ran.
	mock := &chatActionMock{confirmResult: platformConfirmResult(t, "confirmed", "Action executed successfully")}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = chatActionsConfirmCmd.Flags().Set("output", "") })

	output, commandError := executeCommand("chat", "actions", "confirm",
		"82908385-998d-430d-a5ff-4d1b4ca5e252", "-o", "json")
	if commandError != nil {
		t.Fatalf("confirm: %v", commandError)
	}
	var rendered map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &rendered); unmarshalError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", unmarshalError, output)
	}
	if rendered["success"] != true {
		t.Errorf("success = %v for an executed action, want true:\n%s", rendered["success"], output)
	}
	if rendered["status"] != "confirmed" {
		t.Errorf("status = %v, want confirmed", rendered["status"])
	}
}

func TestChatActionsConfirmFailsWhenThePlatformDidNotRunTheAction(t *testing.T) {
	// The platform answers 200 `failed` when the write limit refuses the
	// action (the card stays pending); that is not a confirmation.
	mock := &chatActionMock{confirmResult: platformConfirmResult(t, "failed", "Organisation write limit reached")}
	setMockClient(t, mock)

	var commandError error
	stdoutOutput := captureStdout(t, func() {
		_, commandError = executeCommand("chat", "actions", "confirm", "action-6")
	})
	if commandError == nil {
		t.Fatal("expected an error for an action the platform did not run")
	}
	if exitCodeFor(commandError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(commandError), exitError)
	}
	if !strings.Contains(commandError.Error(), "Organisation write limit reached") {
		t.Errorf("expected the platform's reason, got: %v", commandError)
	}
	if strings.Contains(stdoutOutput, "Action action-6 confirmed") {
		t.Errorf("a refused action must not read as confirmed, got: %s", stdoutOutput)
	}
}

func TestChatActionsConfirmJSONStillPrintsTheDocumentWhenNotRun(t *testing.T) {
	mock := &chatActionMock{confirmResult: platformConfirmResult(t, "failed", "Organisation write limit reached")}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = chatActionsConfirmCmd.Flags().Set("output", "") })

	output, commandError := executeCommand("chat", "actions", "confirm", "action-7", "-o", "json")
	if exitCodeFor(commandError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(commandError), exitError)
	}
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON document in output: %s", output)
	}
	var rendered map[string]any
	if unmarshalError := json.NewDecoder(strings.NewReader(output[jsonStart:])).Decode(&rendered); unmarshalError != nil {
		t.Fatalf("decode: %v\n%s", unmarshalError, output)
	}
	if rendered["success"] != false || rendered["status"] != "failed" {
		t.Errorf("rendered = %v, want success false and status failed", rendered)
	}
}

func TestChatActionsConfirmUnknownStatusIsNotReportedConfirmed(t *testing.T) {
	// Only `confirmed` means the action ran; an outcome this CLI does not
	// recognise must not print "confirmed" and exit 0.
	mock := &chatActionMock{confirmResult: platformConfirmResult(t, "pending", "Queued")}
	setMockClient(t, mock)

	var commandError error
	stdoutOutput := captureStdout(t, func() {
		_, commandError = executeCommand("chat", "actions", "confirm", "action-8")
	})
	if exitCodeFor(commandError) != exitError {
		t.Errorf("exit code = %d, want %d (error %v)", exitCodeFor(commandError), exitError, commandError)
	}
	if strings.Contains(stdoutOutput, "Action action-8 confirmed") {
		t.Errorf("an unrecognised status must not read as confirmed, got: %s", stdoutOutput)
	}
}

func TestChatActionsRejectJSONExitsZero(t *testing.T) {
	// A reject answers `rejected` with success false; that is the reject
	// working, not a failure.
	mock := &chatActionMock{confirmResult: platformConfirmResult(t, "rejected", "Action rejected")}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = chatActionsRejectCmd.Flags().Set("output", "") })

	output, commandError := executeCommand("chat", "actions", "reject", "action-9", "-o", "json")
	if commandError != nil {
		t.Fatalf("reject: %v\n%s", commandError, output)
	}
}
