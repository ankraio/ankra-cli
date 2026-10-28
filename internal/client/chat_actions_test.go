package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
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

// platformConfirmBody is the confirm endpoint's 200 body as the platform
// writes it (cluster: chatturn.buildActionConfirmationResponse): the outcome
// is `status`, and there is no `success` member at all.
func platformConfirmBody(status, message string) map[string]any {
	return map[string]any{
		"action_id":             "82908385-998d-430d-a5ff-4d1b4ca5e252",
		"status":                status,
		"message":               message,
		"result":                nil,
		"execution_ref":         nil,
		"live_status_hint":      nil,
		"model_visible_summary": "action.apply_stack_changes: " + status,
		"frontend_links":        []any{},
		"chat_fix_ticket":       nil,
	}
}

func TestConfirmChatActionSuccessFollowsThePlatformStatus(t *testing.T) {
	// ankra-g7307k: the platform never sends `success`, so decoding it
	// straight left Success false for every action that ran, and
	// `-o json` printed success:false next to status:confirmed.
	cases := []struct {
		name        string
		status      string
		message     string
		wantSuccess bool
		wantNotRun  bool
	}{
		{"executed", ConfirmStatusConfirmed, "Action executed successfully", true, false},
		{"write limit refused it", ConfirmStatusFailed, "Organisation write limit reached", false, true},
		// A reject is not a confirmation; callers ask NotRun only after
		// confirming, where `rejected` would indeed mean it did not run.
		{"rejected", ConfirmStatusRejected, "Action rejected", false, true},
		// A status this CLI does not know never reads as executed.
		{"unknown status", "pending", "Queued", false, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, platformConfirmBody(testCase.status, testCase.message))
			}
			testClient := newTestClient(t, handler)

			result, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{
				ActionID: "82908385-998d-430d-a5ff-4d1b4ca5e252", Confirmed: true,
			})
			if confirmError != nil {
				t.Fatalf("ConfirmChatAction: %v", confirmError)
			}
			if result.Success != testCase.wantSuccess {
				t.Errorf("Success = %v for status %q, want %v", result.Success, testCase.status, testCase.wantSuccess)
			}
			if result.NotRun() != testCase.wantNotRun {
				t.Errorf("NotRun() = %v for status %q, want %v", result.NotRun(), testCase.status, testCase.wantNotRun)
			}

			// `-o json` encodes the result itself, so the field a script
			// reads must carry the same answer.
			encoded, marshalError := json.Marshal(result)
			if marshalError != nil {
				t.Fatalf("marshal: %v", marshalError)
			}
			var rendered map[string]any
			if unmarshalError := json.Unmarshal(encoded, &rendered); unmarshalError != nil {
				t.Fatalf("unmarshal: %v", unmarshalError)
			}
			if rendered["success"] != testCase.wantSuccess {
				t.Errorf("rendered success = %v, want %v (document %s)", rendered["success"], testCase.wantSuccess, encoded)
			}
			if rendered["status"] != testCase.status {
				t.Errorf("rendered status = %v, want %q", rendered["status"], testCase.status)
			}
		})
	}
}

func TestConfirmChatActionNilResultIsNotRun(t *testing.T) {
	// No answer is not an observation that the action ran.
	var result *ConfirmChatActionResult
	if !result.NotRun() {
		t.Error("NotRun() = false for a nil result, want true")
	}
}

func TestConfirmChatActionHonoursAnExplicitSuccess(t *testing.T) {
	// A body that states `success` is taken at its word, so a platform that
	// starts sending it cannot be overridden by the status fallback.
	handler := func(w http.ResponseWriter, r *http.Request) {
		body := platformConfirmBody(ConfirmStatusConfirmed, "Action executed successfully")
		body["success"] = false
		jsonResponse(t, w, http.StatusOK, body)
	}
	testClient := newTestClient(t, handler)

	result, confirmError := testClient.ConfirmChatAction(ConfirmChatActionRequest{ActionID: "a", Confirmed: true})
	if confirmError != nil {
		t.Fatalf("ConfirmChatAction: %v", confirmError)
	}
	if result.Success {
		t.Error("an explicit success:false must not be overridden by status")
	}
}

func TestConfirmChatActionResultYAMLUsesTheJSONFieldNames(t *testing.T) {
	// `-o yaml` encodes the result with yaml.v3, which ignores json tags:
	// without yaml tags it printed actionid/toolname and the whole raw
	// document under `document`.
	var result ConfirmChatActionResult
	encodedBody, _ := json.Marshal(platformConfirmBody(ConfirmStatusConfirmed, "Action executed successfully"))
	if unmarshalError := json.Unmarshal(encodedBody, &result); unmarshalError != nil {
		t.Fatalf("decode: %v", unmarshalError)
	}
	encoded, marshalError := yaml.Marshal(&result)
	if marshalError != nil {
		t.Fatalf("yaml: %v", marshalError)
	}
	var rendered map[string]any
	if unmarshalError := yaml.Unmarshal(encoded, &rendered); unmarshalError != nil {
		t.Fatalf("yaml decode: %v", unmarshalError)
	}
	if rendered["success"] != true || rendered["action_id"] == nil {
		t.Errorf("yaml = %s, want success: true and action_id", encoded)
	}
	if _, leaked := rendered["document"]; leaked {
		t.Errorf("yaml leaked the raw document: %s", encoded)
	}
}
