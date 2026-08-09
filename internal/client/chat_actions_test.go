package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestConfirmChatActionSendsCSRFDoubleSubmit(t *testing.T) {
	// The confirm endpoint enforces double-submit CSRF, so the CLI must send
	// a cookie and a header carrying the same token or it gets a 403.
	var headerToken string
	var cookieToken string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/actions/confirm" {
			t.Errorf("path = %s", r.URL.Path)
		}
		headerToken = r.Header.Get("X-Ankra-CSRF")
		if cookie, cookieError := r.Cookie("ankra_csrf"); cookieError == nil {
			cookieToken = cookie.Value
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"success": true, "status": "executing"})
	}
	testClient := newTestClient(t, handler)

	result, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{
		ActionID: "action-1", Confirmed: true,
	})
	if confirmError != nil {
		t.Fatalf("ConfirmChatAction: %v", confirmError)
	}
	if headerToken == "" {
		t.Error("no X-Ankra-CSRF header sent; the server answers 403 without it")
	}
	if cookieToken == "" {
		t.Error("no ankra_csrf cookie sent; the server answers 403 without it")
	}
	if headerToken != cookieToken {
		t.Errorf("CSRF header %q != cookie %q; double-submit requires them to match", headerToken, cookieToken)
	}
	if !result.Success || result.Status != "executing" {
		t.Errorf("result = %+v", result)
	}
}

func TestConfirmChatActionSendsTheRequestBody(t *testing.T) {
	var received ConfirmChatActionRequest
	handler := func(w http.ResponseWriter, r *http.Request) {
		if decodeError := json.NewDecoder(r.Body).Decode(&received); decodeError != nil {
			t.Errorf("decode body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"success": true})
	}
	testClient := newTestClient(t, handler)

	force := true
	forceReason := "drift is unrelated"
	if _, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{
		ActionID:    "action-2",
		Confirmed:   true,
		Force:       &force,
		ForceReason: &forceReason,
	}); confirmError != nil {
		t.Fatalf("ConfirmChatAction: %v", confirmError)
	}

	if received.ActionID != "action-2" || !received.Confirmed {
		t.Errorf("received = %+v", received)
	}
	if received.Force == nil || !*received.Force {
		t.Error("force was not sent")
	}
	if received.ForceReason == nil || *received.ForceReason != "drift is unrelated" {
		t.Errorf("ForceReason = %v", received.ForceReason)
	}
}

func TestConfirmChatActionDecodesDriftConflict(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"error_code": "ACTION_DRIFTED",
			"detail":     "Cluster state changed since this action was proposed",
			"payload":    map[string]any{"changed": []string{"replicas"}},
		})
	}
	testClient := newTestClient(t, handler)

	_, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{ActionID: "action-3", Confirmed: true})
	if confirmError == nil {
		t.Fatal("expected an error for the 409")
	}
	var conflict *ActionConflictError
	if !errors.As(confirmError, &conflict) {
		t.Fatalf("error = %T, want *ActionConflictError", confirmError)
	}
	if !conflict.IsDrift() {
		t.Errorf("IsDrift() = false for %s", conflict.ErrorCode)
	}
	if conflict.Detail != "Cluster state changed since this action was proposed" {
		t.Errorf("Detail = %q", conflict.Detail)
	}
	if len(conflict.Payload) == 0 {
		t.Error("the drift payload should be preserved for the caller to show")
	}
}

func TestConfirmChatActionSupersededIsNotDrift(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"error_code": "ACTION_SUPERSEDED",
			"detail":     "A newer action replaced this one",
		})
	}
	testClient := newTestClient(t, handler)

	_, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{ActionID: "action-4", Confirmed: true})
	var conflict *ActionConflictError
	if !errors.As(confirmError, &conflict) {
		t.Fatalf("error = %T, want *ActionConflictError", confirmError)
	}
	if conflict.IsDrift() {
		t.Error("a superseded action must not be reported as forceable drift")
	}
}

func TestConfirmChatActionSurfacesNotFoundDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": "Action not found or has expired"})
	}
	testClient := newTestClient(t, handler)

	_, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{ActionID: "gone", Confirmed: true})
	if confirmError == nil || confirmError.Error() != "Action not found or has expired" {
		t.Errorf("error = %v, want the backend detail", confirmError)
	}
}

func TestListPendingChatActionsRequestsTheConversation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/actions/pending" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("conversation_id"); got != "conversation-1" {
			t.Errorf("conversation_id = %q, want conversation-1", got)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"actions": []map[string]any{{"action_id": "action-1", "tool_name": "restart_node"}},
			"count":   1,
		})
	}
	testClient := newTestClient(t, handler)

	result, listError := testClient.ListPendingChatActions("conversation-1")
	if listError != nil {
		t.Fatalf("ListPendingChatActions: %v", listError)
	}
	if result.Count != 1 || len(result.Actions) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Actions[0]["tool_name"] != "restart_node" {
		t.Errorf("actions[0] = %+v", result.Actions[0])
	}
}
