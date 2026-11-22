package client

// AI plans (ankra-hq41xb.22): the multi-step proposals a chat conversation
// halts on. A plan is reviewed as a whole; approving it runs its steps and
// rejecting it discards them.
//
//	GET  /api/v1/chat/plans?conversation_id=…   the caller's open plans
//	GET  /api/v1/chat/plans/{plan_id}           one plan with its steps
//	POST /api/v1/chat/plans/{plan_id}/approve   run it (server-sent events)
//	POST /api/v1/chat/plans/{plan_id}/reject    discard it

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// PlanErrorCodeAcknowledgementRequired is the approve route's 400 for a plan
// whose high-risk or irreversible steps were not all named.
const PlanErrorCodeAcknowledgementRequired = "PLAN_ACKNOWLEDGEMENT_REQUIRED"

// The frames of the approval stream.
const (
	ChatPlanEventStepResult = "plan_step_result"
	ChatPlanEventComplete   = "plan_complete"
	ChatPlanEventError      = "plan_error"
)

// ChatPlanUnacknowledgedStep is one step an approval has to name before the
// platform runs the plan.
type ChatPlanUnacknowledgedStep struct {
	StepOrder   int    `json:"step_order"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	RiskLevel   string `json:"risk_level"`
	Reversible  bool   `json:"reversible"`
	Reason      string `json:"reason"`
}

// ChatPlanAcknowledgementRequiredError is the approve route refusing a plan
// before anything ran: Steps are the high-risk and irreversible steps the
// approval did not name.
type ChatPlanAcknowledgementRequiredError struct {
	Detail string
	Steps  []ChatPlanUnacknowledgedStep
}

func (acknowledgementError *ChatPlanAcknowledgementRequiredError) Error() string {
	if acknowledgementError.Detail != "" {
		return acknowledgementError.Detail
	}
	return "the plan has steps that must be acknowledged before it is approved"
}

// ListChatPlans reads the caller's open plans in a conversation.
func (c *Client) ListChatPlans(ctx context.Context, conversationID string) (json.RawMessage, error) {
	return c.applicationResourceRequest(ctx, http.MethodGet, "/api/v1/chat/plans",
		neturl.Values{"conversation_id": []string{conversationID}}, nil)
}

// GetChatPlan reads one plan with its steps.
func (c *Client) GetChatPlan(ctx context.Context, planID string) (json.RawMessage, error) {
	return c.applicationResourceRequest(ctx, http.MethodGet, "/api/v1/chat/plans/"+neturl.PathEscape(planID), nil, nil)
}

// RejectChatPlan rejects a proposed plan so none of its steps run. An empty
// reason is not sent.
func (c *Client) RejectChatPlan(ctx context.Context, planID string, reason string) (json.RawMessage, error) {
	body := map[string]string{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.applicationResourceRequest(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/chat/plans/%s/reject", neturl.PathEscape(planID)), nil, body)
}

// ApproveChatPlan approves a plan and follows the stream of its execution,
// handing every frame to onEvent until the stream ends. acknowledgedStepOrders
// names the high-risk and irreversible steps the caller accepts; a plan with
// any left unnamed answers ChatPlanAcknowledgementRequiredError and nothing
// runs. A refusal the stream itself carries arrives as a plan_error frame,
// not as an error.
func (c *Client) ApproveChatPlan(ctx context.Context, planID string, acknowledgedStepOrders []int,
	onEvent func(ChatStreamEvent)) error {
	body := map[string]any{}
	if len(acknowledgedStepOrders) > 0 {
		body["acknowledged_step_orders"] = acknowledgedStepOrders
	}
	payload, marshalError := json.Marshal(body)
	if marshalError != nil {
		return fmt.Errorf("marshal request: %w", marshalError)
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/chat/plans/%s/approve", c.BaseURL, neturl.PathEscape(planID)), bytes.NewReader(payload))
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "text/event-stream")

	response, doError := c.StreamingHTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	if response.StatusCode != http.StatusOK {
		responseBody, readError := readResponseBody(response)
		if readError != nil {
			return fmt.Errorf("read response: %w", readError)
		}
		return chatPlanApprovalRefusal(response.StatusCode, responseBody)
	}

	reader := bufio.NewReader(response.Body)
	var frame sseFrame
	for {
		line, readError := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			frame.addLine(line)
		}
		if line == "" || readError != nil {
			if event, ok := chatPlanEventFromFrame(frame); ok {
				onEvent(event)
			}
			frame = sseFrame{}
		}
		if readError != nil {
			if errors.Is(readError, io.EOF) {
				return nil
			}
			return fmt.Errorf("read the approval stream: %w", readError)
		}
	}
}

// chatPlanEventFromFrame decodes one approval frame. The stream writes
// {"type": …, "schema_version": …, "data": …} on the data line with no event
// name, so the type is taken from the envelope.
func chatPlanEventFromFrame(frame sseFrame) (ChatStreamEvent, bool) {
	if !frame.hasData {
		return ChatStreamEvent{}, false
	}
	var event ChatStreamEvent
	if unmarshalError := json.Unmarshal([]byte(strings.Join(frame.data, "\n")), &event); unmarshalError != nil {
		return ChatStreamEvent{}, false
	}
	if event.Type == "" {
		event.Type = frame.eventName
	}
	return event, event.Type != ""
}

func chatPlanApprovalRefusal(statusCode int, body []byte) error {
	if statusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if denied := PermissionDeniedFromResponse(statusCode, body); denied != nil {
		return denied
	}
	var envelope struct {
		ErrorCode string `json:"error_code"`
		Detail    string `json:"detail"`
		Payload   struct {
			UnacknowledgedSteps []ChatPlanUnacknowledgedStep `json:"unacknowledged_steps"`
		} `json:"payload"`
	}
	if unmarshalError := json.Unmarshal(body, &envelope); unmarshalError == nil &&
		envelope.ErrorCode == PlanErrorCodeAcknowledgementRequired {
		return &ChatPlanAcknowledgementRequiredError{Detail: envelope.Detail, Steps: envelope.Payload.UnacknowledgedSteps}
	}
	message := detailFromBody(body)
	if message == "" {
		message = "plan approval failed"
	}
	return newUnexpectedResponseError(message, statusCode, redactedBodyForError(body, 500))
}
