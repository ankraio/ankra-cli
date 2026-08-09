package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Stable error codes the confirm endpoint answers 409 with. The CLI keys on
// the code, not the message, so the wording can change server-side without
// breaking the retry hint.
const (
	ActionErrorCodeDrifted    = "ACTION_DRIFTED"
	ActionErrorCodeSuperseded = "ACTION_SUPERSEDED"
)

// ChatActionProposal is the payload of an `action_proposal` SSE frame: the
// write the assistant wants to run, halted until it is confirmed. Emitted
// only in agent mode - ask mode refuses mutating tools before proposing.
type ChatActionProposal struct {
	ActionID         string          `json:"action_id"`
	ToolName         string          `json:"tool_name"`
	Description      string          `json:"description"`
	Parameters       json.RawMessage `json:"parameters,omitempty"`
	RiskLevel        string          `json:"risk_level"`
	Reversible       bool            `json:"reversible"`
	CreatedAt        string          `json:"created_at"`
	ExpiresInSeconds int             `json:"expires_in_seconds"`
	PlanID           *string         `json:"plan_id,omitempty"`
}

// ActionConflictError is a 409 from the confirm endpoint: the live state no
// longer matches the fingerprint taken when the action was proposed
// (ACTION_DRIFTED), or a newer action replaced this one
// (ACTION_SUPERSEDED). Drift is recoverable by confirming with force.
type ActionConflictError struct {
	ErrorCode string
	Detail    string
	Payload   json.RawMessage
}

func (conflictError *ActionConflictError) Error() string {
	if conflictError.Detail != "" {
		return conflictError.Detail
	}
	return conflictError.ErrorCode
}

// IsDrift reports whether the conflict is recoverable by re-confirming with
// force, as opposed to a supersede, which never is.
func (conflictError *ActionConflictError) IsDrift() bool {
	return conflictError.ErrorCode == ActionErrorCodeDrifted
}

// ConfirmChatActionRequest is the confirm endpoint's body. Confirmed=false
// rejects the action instead of running it.
type ConfirmChatActionRequest struct {
	ActionID    string  `json:"action_id"`
	Confirmed   bool    `json:"confirmed"`
	Reason      *string `json:"reason,omitempty"`
	Force       *bool   `json:"force,omitempty"`
	ForceReason *string `json:"force_reason,omitempty"`
}

// ConfirmChatActionResult is the confirm endpoint's 200 body. The envelope
// varies by tool, so the decoded document is kept whole for `-o json` and
// the well-known fields are lifted for the human rendering.
type ConfirmChatActionResult struct {
	Success  bool           `json:"success"`
	Status   string         `json:"status,omitempty"`
	Message  string         `json:"message,omitempty"`
	ActionID string         `json:"action_id,omitempty"`
	ToolName string         `json:"tool_name,omitempty"`
	Document map[string]any `json:"-"`
}

// UnmarshalJSON keeps the whole document alongside the lifted fields.
func (result *ConfirmChatActionResult) UnmarshalJSON(data []byte) error {
	type plainResult ConfirmChatActionResult
	var lifted plainResult
	if unmarshalError := json.Unmarshal(data, &lifted); unmarshalError != nil {
		return unmarshalError
	}
	*result = ConfirmChatActionResult(lifted)
	return json.Unmarshal(data, &result.Document)
}

// ConfirmChatAction confirms or rejects a pending AI action.
//
// The endpoint is CSRF-protected (double-submit), so the request carries a
// matching `X-Ankra-CSRF` header and `ankra_csrf` cookie the same way the
// account MFA writes do.
func (c *Client) ConfirmChatAction(request ConfirmChatActionRequest) (*ConfirmChatActionResult, error) {
	requestURL := c.BaseURL + "/api/v1/chat/actions/confirm"
	payload, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	httpRequest, requestError := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(httpRequest)

	response, doError := c.HTTP.Do(httpRequest)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}

	if response.StatusCode == http.StatusConflict {
		return nil, actionConflictFromBody(body)
	}
	if response.StatusCode != http.StatusOK {
		detail := detailFromBody(body)
		if detail != "" {
			return nil, fmt.Errorf("%s", detail)
		}
		return nil, newUnexpectedResponseError("confirm action failed",
			response.StatusCode, redactedBodyForError(body, 500))
	}

	var result ConfirmChatActionResult
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return nil, fmt.Errorf("decode response: %w", unmarshalError)
	}
	return &result, nil
}

// actionConflictFromBody decodes the 409 envelope
// ({error_code, detail, payload}) into the typed conflict.
func actionConflictFromBody(body []byte) error {
	var envelope struct {
		ErrorCode string          `json:"error_code"`
		Detail    string          `json:"detail"`
		Payload   json.RawMessage `json:"payload,omitempty"`
	}
	if unmarshalError := json.Unmarshal(body, &envelope); unmarshalError != nil {
		return fmt.Errorf("%s", detailFromBody(body))
	}
	return &ActionConflictError{
		ErrorCode: envelope.ErrorCode,
		Detail:    envelope.Detail,
		Payload:   envelope.Payload,
	}
}

// PendingChatActionsResult is the pending-actions listing for one
// conversation.
type PendingChatActionsResult struct {
	Actions []map[string]any `json:"actions"`
	Count   int              `json:"count"`
}

// ListPendingChatActions lists the actions still awaiting confirmation in a
// conversation. conversationID is required by the endpoint.
func (c *Client) ListPendingChatActions(conversationID string) (*PendingChatActionsResult, error) {
	requestURL := fmt.Sprintf("%s/api/v1/chat/actions/pending?conversation_id=%s",
		c.BaseURL, conversationID)
	var result PendingChatActionsResult
	if getError := c.getJSON(requestURL, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}
