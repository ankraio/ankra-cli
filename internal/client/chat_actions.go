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

// Statuses the confirm endpoint's 200 body reports for one action. The
// platform answers 200 for an action it could not run too - a write the
// organisation's hourly write limit refuses comes back `failed` with the card
// left pending - so the status, not the HTTP code, says whether it ran.
const (
	ConfirmStatusConfirmed = "confirmed"
	ConfirmStatusFailed    = "failed"
	ConfirmStatusRejected  = "rejected"
)

// ConfirmChatActionResult is the confirm endpoint's 200 body. The envelope
// varies by tool, so the decoded document is kept whole in Document and the
// well-known fields are lifted for the human rendering and `-o json|yaml`.
type ConfirmChatActionResult struct {
	// Success is true only when the action ran: the platform's body carries
	// no `success` member (its outcome is `status`), so it is derived from
	// the status unless a body states it explicitly.
	Success  bool           `json:"success" yaml:"success"`
	Status   string         `json:"status,omitempty" yaml:"status,omitempty"`
	Message  string         `json:"message,omitempty" yaml:"message,omitempty"`
	ActionID string         `json:"action_id,omitempty" yaml:"action_id,omitempty"`
	ToolName string         `json:"tool_name,omitempty" yaml:"tool_name,omitempty"`
	Document map[string]any `json:"-" yaml:"-"`
}

// UnmarshalJSON keeps the whole document alongside the lifted fields.
//
// The platform's ActionConfirmationResponse is {action_id, status, message,
// result, execution_ref, ...} with no `success` member, so decoding it
// straight into Success left it false for every action that ran, next to
// `status: confirmed` and "Action executed successfully" (ankra-g7307k).
func (result *ConfirmChatActionResult) UnmarshalJSON(data []byte) error {
	type plainResult ConfirmChatActionResult
	var lifted plainResult
	if unmarshalError := json.Unmarshal(data, &lifted); unmarshalError != nil {
		return unmarshalError
	}
	*result = ConfirmChatActionResult(lifted)
	if unmarshalError := json.Unmarshal(data, &result.Document); unmarshalError != nil {
		return unmarshalError
	}
	if _, stated := result.Document["success"]; !stated {
		result.Success = result.Status == ConfirmStatusConfirmed
	}
	return nil
}

// NotRun reports, for a confirmation (confirmed=true), whether the platform
// answered without running the action: anything but success, which covers
// the 200 `failed` status (an execution error, or the write limit refusing it
// before it started) and any status this CLI does not know. A missing answer
// (nil) is not an observation that it ran, so it counts as not run too. A
// reject answers `rejected`, which is not a confirmation, so callers ask this
// only when they confirmed.
func (result *ConfirmChatActionResult) NotRun() bool {
	return result == nil || !result.Success
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
