package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// AgentRun mirrors one dispatched AI agent run from
// GET /api/v1/org/ai-agent-runs (the org-wide run surface).
type AgentRun struct {
	ID             string                 `json:"id"`
	TaskID         string                 `json:"task_id"`
	TaskName       string                 `json:"task_name"`
	AiSessionID    *string                `json:"ai_session_id"`
	Status         string                 `json:"status"`
	TriggerContext map[string]interface{} `json:"trigger_context"`
	OutcomeSummary *string                `json:"outcome_summary"`
	TurnsUsed      int                    `json:"turns_used"`
	GoalStatus     *string                `json:"goal_status"`
	StartedAt      string                 `json:"started_at"`
	FinishedAt     *string                `json:"finished_at"`
}

// AgentRunListResponse is the GET /api/v1/org/ai-agent-runs body.
type AgentRunListResponse struct {
	Runs []AgentRun `json:"runs"`
}

// AgentRunTranscriptEvent is one session event in a run transcript.
type AgentRunTranscriptEvent struct {
	ID             string                 `json:"id"`
	SessionID      string                 `json:"session_id"`
	SequenceNumber int64                  `json:"sequence_number"`
	EventType      string                 `json:"event_type"`
	Payload        map[string]interface{} `json:"payload"`
	CreatedAt      string                 `json:"created_at"`
}

// AgentRunTranscript is the GET /api/v1/org/ai-agent-runs/{id}/transcript
// body: the ascending page of session events after `since`.
type AgentRunTranscript struct {
	SessionID          string                    `json:"session_id"`
	LastSequenceNumber int64                     `json:"last_sequence_number"`
	Events             []AgentRunTranscriptEvent `json:"events"`
}

// CancelAgentRunResponse is the POST /api/v1/org/ai-agent-runs/{id}/cancel
// body on success.
type CancelAgentRunResponse struct {
	Detail             string `json:"detail"`
	Status             string `json:"status"`
	CancelledSessionID string `json:"cancelled_session_id,omitempty"`
}

// ListAgentRuns lists the organisation's dispatched AI agent runs, newest
// first, optionally filtered by task id and statuses.
func (c *Client) ListAgentRuns(taskID string, statuses []string, limit int) (*AgentRunListResponse, error) {
	query := url.Values{}
	if taskID != "" {
		query.Set("task_id", taskID)
	}
	for _, status := range statuses {
		query.Add("status", status)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	requestURL := c.BaseURL + "/api/v1/org/ai-agent-runs"
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var response AgentRunListResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, fmt.Errorf("listing agent runs: %w", err)
	}
	return &response, nil
}

// GetAgentRun fetches one dispatched run with its owning agent's name.
func (c *Client) GetAgentRun(runID string) (*AgentRun, error) {
	var run AgentRun
	if err := c.getJSON(c.BaseURL+"/api/v1/org/ai-agent-runs/"+url.PathEscape(runID), &run); err != nil {
		return nil, fmt.Errorf("getting agent run: %w", err)
	}
	return &run, nil
}

// GetAgentRunTranscript reads the run's linked session transcript: the
// ascending page of session events strictly after `since`.
func (c *Client) GetAgentRunTranscript(runID string, since int64, limit int) (*AgentRunTranscript, error) {
	query := url.Values{}
	if since > 0 {
		query.Set("since", strconv.FormatInt(since, 10))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	requestURL := c.BaseURL + "/api/v1/org/ai-agent-runs/" + url.PathEscape(runID) + "/transcript"
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var transcript AgentRunTranscript
	if err := c.getJSON(requestURL, &transcript); err != nil {
		return nil, fmt.Errorf("getting agent run transcript: %w", err)
	}
	return &transcript, nil
}

// CancelAgentRun cancels one live run; the platform interrupts the
// in-flight turn within seconds. Already-finished runs answer 409 with
// their final status in the detail.
func (c *Client) CancelAgentRun(runID string) (*CancelAgentRunResponse, error) {
	requestURL := c.BaseURL + "/api/v1/org/ai-agent-runs/" + url.PathEscape(runID) + "/cancel"
	request, requestError := http.NewRequest(http.MethodPost, requestURL, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("cancelling agent run: %w", sendError)
	}
	defer closeBody(response)
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	body, _ := readResponseBody(response)
	if response.StatusCode != http.StatusOK {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		message := detailFromBody(body)
		if message == "" {
			message = fmt.Sprintf("unexpected status: %s", response.Status)
		}
		return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, message)
	}
	var result CancelAgentRunResponse
	if err := parseJSON(body, &result); err != nil {
		return nil, fmt.Errorf("parsing cancel response: %w", err)
	}
	return &result, nil
}
