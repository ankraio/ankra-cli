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

const testPlanID = "6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77"

const chatPlanBody = `{"plan_id":"` + testPlanID + `","title":"Scale the shop","summary":"Two steps.",
	"risk_aggregate":"high","status":"proposed","conversation_id":"conversation-1","expires_in_seconds":600,
	"steps":[{"step_id":"s-1","step_order":1,"tool_name":"scale_deployment","description":"Scale web to 3",
	  "risk_level":"low","reversible":true,"status":"pending"},
	 {"step_id":"s-2","step_order":2,"tool_name":"delete_pvc","description":"Delete the old volume",
	  "risk_level":"high","reversible":false,"status":"pending"}]}`

type chatPlansMock struct {
	baseMock
	answer       string
	failure      error
	conversation string
	rejected     [][2]string
	acknowledged []int
	approvals    int
	events       []client.ChatStreamEvent
}

func (mock *chatPlansMock) ListChatPlans(_ context.Context, conversationID string) (json.RawMessage, error) {
	mock.conversation = conversationID
	return json.RawMessage(mock.answer), mock.failure
}

func (mock *chatPlansMock) GetChatPlan(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(mock.answer), mock.failure
}

func (mock *chatPlansMock) RejectChatPlan(_ context.Context, planID string, reason string) (json.RawMessage, error) {
	mock.rejected = append(mock.rejected, [2]string{planID, reason})
	return json.RawMessage(mock.answer), mock.failure
}

func (mock *chatPlansMock) ApproveChatPlan(_ context.Context, _ string, acknowledgedStepOrders []int, onEvent func(client.ChatStreamEvent)) error {
	mock.approvals++
	mock.acknowledged = acknowledgedStepOrders
	for _, event := range mock.events {
		onEvent(event)
	}
	return mock.failure
}

func runChatPlans(t *testing.T, mock *chatPlansMock, arguments ...string) (string, error) {
	t.Helper()
	output, _, executeError := runFreshCommand(t, newChatPlansCommand(), mock, "", arguments...)
	return output, executeError
}

func planEvent(eventType string, data map[string]any) client.ChatStreamEvent {
	return client.ChatStreamEvent{Type: eventType, Data: data}
}

func TestChatPlansListAndGet(t *testing.T) {
	list := &chatPlansMock{answer: `{"plans":[` + chatPlanBody + `],"count":1}`}
	output, executeError := runChatPlans(t, list, "list", "conversation-1")
	if executeError != nil || list.conversation != "conversation-1" {
		t.Fatalf("error = %v, conversation = %q", executeError, list.conversation)
	}
	if !strings.Contains(output, testPlanID) || !strings.Contains(output, "2 step(s)") || !strings.Contains(output, "Scale the shop") {
		t.Errorf("output:\n%s", output)
	}
	empty := &chatPlansMock{answer: `{"plans":[],"count":0}`}
	if output, executeError = runChatPlans(t, empty, "list", "conversation-1"); executeError != nil ||
		!strings.Contains(output, "No plans awaiting approval") {
		t.Fatalf("error = %v, output: %s", executeError, output)
	}

	get := &chatPlansMock{answer: chatPlanBody}
	output, executeError = runChatPlans(t, get, "get", testPlanID)
	if executeError != nil {
		t.Fatalf("get failed: %v", executeError)
	}
	for _, want := range []string{"Scale the shop", "1. [pending] scale_deployment", "delete_pvc", "NOT reversible", "in 600s"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
	output, executeError = runChatPlans(t, get, "get", testPlanID, "-o", "json")
	if executeError != nil || !strings.Contains(output, `"conversation_id"`) {
		t.Fatalf("error = %v, -o json must print the platform's document: %s", executeError, output)
	}

	missing := &chatPlansMock{failure: client.NewUnexpectedResponseError(http.StatusNotFound, "Plan not found")}
	if _, missingError := runChatPlans(t, missing, "get", testPlanID); exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("error = %v, want the not-found exit", missingError)
	}
}

func TestChatPlansRejectSendsTheReason(t *testing.T) {
	mock := &chatPlansMock{answer: `{"plan_id":"` + testPlanID + `","status":"rejected"}`}
	output, executeError := runChatPlans(t, mock, "reject", testPlanID, "--reason", "not now")
	if executeError != nil || !strings.Contains(output, "rejected") {
		t.Fatalf("error = %v, output: %s", executeError, output)
	}
	if len(mock.rejected) != 1 || mock.rejected[0] != [2]string{testPlanID, "not now"} {
		t.Errorf("rejected = %v", mock.rejected)
	}
	gone := &chatPlansMock{failure: client.NewUnexpectedResponseError(http.StatusNotFound, "Plan not found or not in a rejectable state")}
	if _, goneError := runChatPlans(t, gone, "reject", testPlanID); exitCodeFor(goneError) != exitNotFound {
		t.Fatalf("error = %v, want the not-found exit", goneError)
	}
}

func TestChatPlansApproveFollowsTheStepsToCompletion(t *testing.T) {
	mock := &chatPlansMock{events: []client.ChatStreamEvent{
		planEvent(client.ChatPlanEventStepResult, map[string]any{"step_order": float64(1), "tool_name": "scale_deployment", "status": "completed", "success": true}),
		planEvent(client.ChatPlanEventStepResult, map[string]any{"step_order": float64(2), "tool_name": "delete_pvc", "status": "completed", "success": true}),
		planEvent(client.ChatPlanEventComplete, map[string]any{"plan_id": testPlanID, "status": "completed"}),
	}}
	output, executeError := runChatPlans(t, mock, "approve", testPlanID, "--acknowledge", "2")
	if executeError != nil {
		t.Fatalf("approve failed: %v", executeError)
	}
	if len(mock.acknowledged) != 1 || mock.acknowledged[0] != 2 {
		t.Errorf("acknowledged = %v, want [2]", mock.acknowledged)
	}
	for _, want := range []string{"Step 1 scale_deployment: completed", "Step 2 delete_pvc: completed", "completed."} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
}

func TestChatPlansApproveHandedToTheSessionIsNotAFailure(t *testing.T) {
	mock := &chatPlansMock{events: []client.ChatStreamEvent{
		planEvent(client.ChatPlanEventComplete, map[string]any{"plan_id": testPlanID, "status": "approved", "executor": "session"}),
	}}
	output, executeError := runChatPlans(t, mock, "approve", testPlanID)
	if executeError != nil || !strings.Contains(output, "chat session that proposed it is running its steps") {
		t.Fatalf("error = %v, output: %s", executeError, output)
	}
}

func TestChatPlansApproveExitsNonZeroWhenThePlanDidNotFinish(t *testing.T) {
	for name, testCase := range map[string]struct {
		events   []client.ChatStreamEvent
		failure  error
		exitCode int
		message  string
	}{
		"a step failed": {
			events: []client.ChatStreamEvent{
				planEvent(client.ChatPlanEventStepResult, map[string]any{"step_order": float64(1), "tool_name": "scale_deployment", "status": "failed", "error": "quota exceeded"}),
				planEvent(client.ChatPlanEventComplete, map[string]any{"status": "partially_completed"}),
			},
			exitCode: exitError, message: "partially completed",
		},
		"the plan is not there": {
			events:   []client.ChatStreamEvent{planEvent(client.ChatPlanEventError, map[string]any{"reason": "not_found", "message": "Plan not found or you do not have access."})},
			exitCode: exitNotFound, message: "Plan not found",
		},
		"the plan is not approvable": {
			events:   []client.ChatStreamEvent{planEvent(client.ChatPlanEventError, map[string]any{"reason": "not_approvable", "message": "Plan is not in an approvable state."})},
			exitCode: exitError, message: "not in an approvable state",
		},
		"the stream ended with no outcome": {exitCode: exitError, message: "without an outcome"},
		"the stream was cut": {
			events:   []client.ChatStreamEvent{planEvent(client.ChatPlanEventStepResult, map[string]any{"step_order": float64(1), "status": "completed"})},
			failure:  errors.New("unexpected EOF"),
			exitCode: exitError, message: "was cut before it finished",
		},
		"the role lacks ai.execute": {failure: &client.PermissionDeniedError{Permission: "ai.execute"}, exitCode: exitForbidden, message: ""},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &chatPlansMock{events: testCase.events, failure: testCase.failure}
			_, executeError := runChatPlans(t, mock, "approve", testPlanID)
			if executeError == nil || exitCodeFor(executeError) != testCase.exitCode ||
				!strings.Contains(executeError.Error(), testCase.message) {
				t.Fatalf("error = %v (exit %d), want exit %d mentioning %q", executeError, exitCodeFor(executeError),
					testCase.exitCode, testCase.message)
			}
		})
	}
}

func TestChatPlansApproveNamesTheStepsThatNeedAcknowledging(t *testing.T) {
	mock := &chatPlansMock{failure: &client.ChatPlanAcknowledgementRequiredError{Steps: []client.ChatPlanUnacknowledgedStep{
		{StepOrder: 2, ToolName: "delete_pvc", Description: "Delete the old volume", RiskLevel: "high", Reason: "irreversible"},
	}}}
	_, executeError := runChatPlans(t, mock, "approve", testPlanID)
	if exitCodeFor(executeError) != exitBlocked {
		t.Fatalf("error = %v, want the blocked exit", executeError)
	}
	for _, want := range []string{"nothing ran", "2. delete_pvc: Delete the old volume", "--acknowledge 2"} {
		if !strings.Contains(executeError.Error(), want) {
			t.Errorf("error must contain %q: %v", want, executeError)
		}
	}
}

func TestChatPlansApproveStructuredOutputCarriesTheFrames(t *testing.T) {
	mock := &chatPlansMock{events: []client.ChatStreamEvent{
		planEvent(client.ChatPlanEventStepResult, map[string]any{"step_order": float64(1), "status": "completed"}),
		planEvent(client.ChatPlanEventComplete, map[string]any{"status": "completed"}),
	}}
	output, executeError := runChatPlans(t, mock, "approve", testPlanID, "-o", "json")
	if executeError != nil {
		t.Fatalf("approve failed: %v", executeError)
	}
	var document struct {
		PlanID string           `json:"plan_id"`
		Status string           `json:"status"`
		Events []map[string]any `json:"events"`
	}
	if decodeError := json.Unmarshal([]byte(output), &document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, output)
	}
	if document.PlanID != testPlanID || document.Status != "completed" || len(document.Events) != 2 {
		t.Errorf("document = %+v", document)
	}
}
