package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Query               string        `json:"query"`
	ConversationID      *string       `json:"conversation_id,omitempty"`
	ConversationHistory []ChatMessage `json:"conversation_history,omitempty"`
	// InteractionMode selects the chat safety mode: "ask" (read-only plus
	// the curated safe creations) or "agent" (can act). Empty leaves the
	// server default.
	InteractionMode string `json:"interaction_mode,omitempty"`
}

type ChatConversation struct {
	ID        string        `json:"id"`
	Title     *string       `json:"title,omitempty"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
	Messages  []ChatMessage `json:"messages,omitempty"`
	ClusterID *string       `json:"cluster_id,omitempty"`
}

type ListConversationsResponse struct {
	Conversations []ChatConversation `json:"conversations"`
	TotalCount    int                `json:"total_count"`
}

type DeleteConversationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ClusterHealthIssue mirrors the backend's ClusterIssueWire (the members
// the CLI renders).
type ClusterHealthIssue struct {
	Title            string   `json:"title"`
	Severity         string   `json:"severity"`
	Description      string   `json:"description"`
	Namespace        *string  `json:"namespace"`
	SuggestedActions []string `json:"suggested_actions"`
}

// ClusterHealthReport mirrors the backend's ClusterHealthReportWire.
type ClusterHealthReport struct {
	Status      string               `json:"status"`
	Score       int                  `json:"score"`
	Issues      []ClusterHealthIssue `json:"issues"`
	PodStats    map[string]int       `json:"pod_stats"`
	NodeStats   map[string]int       `json:"node_stats"`
	EvaluatedAt string               `json:"evaluated_at"`
}

// ClusterHealthAIInsight mirrors the backend's AIInsightWire (the members
// the CLI renders).
type ClusterHealthAIInsight struct {
	Title             string `json:"title"`
	RootCauseAnalysis string `json:"root_cause_analysis"`
	Severity          string `json:"severity"`
	ImpactAssessment  string `json:"impact_assessment"`
}

// ClusterHealth mirrors the backend's ProactiveInsightWire: the health
// report is nested, not flat.
type ClusterHealth struct {
	ClusterID    string                   `json:"cluster_id"`
	HealthReport ClusterHealthReport      `json:"health_report"`
	AIInsights   []ClusterHealthAIInsight `json:"ai_insights"`
	Summary      string                   `json:"summary"`
	GeneratedAt  string                   `json:"generated_at"`
}

type ChatStreamEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
	// Sequence is the durable event log position of a sessions-lane frame
	// (the SSE id), the cursor a dropped tail resumes from; zero on the
	// legacy stream and on id-less frames.
	Sequence int64 `json:"-"`
}

// ErrorMessage extracts the human message from an error event. Backend
// error frames carry {"type":"error","data":{"message":...}}; the Error
// member is only set by this client for local stream failures. Returns ""
// when the event carries no readable message.
func (event ChatStreamEvent) ErrorMessage() string {
	if event.Error != "" {
		return event.Error
	}
	switch data := event.Data.(type) {
	case string:
		return data
	case map[string]any:
		if message, ok := data["message"].(string); ok {
			return message
		}
	}
	return ""
}

// chatStreamIdleTimeout bounds how long a chat stream read may sit with no
// bytes at all. The backend heartbeats every few seconds while working, so
// a silent stream this long is a dead connection, not a slow answer;
// without the watchdog a stalled stream hung the command forever. A var so
// tests can shorten it.
var chatStreamIdleTimeout = 3 * time.Minute

// deprecatedChatWarningOnce rate-limits the legacy lane's deprecation
// warning to one per process.
var deprecatedChatWarningOnce sync.Once

func (c *Client) StreamChat(clusterID *string, chatReq ChatRequest) (<-chan ChatStreamEvent, error) {
	var url string
	if clusterID != nil && *clusterID != "" {
		url = fmt.Sprintf("%s/api/v1/org/clusters/%s/kubernetes/chat", c.BaseURL, *clusterID)
	} else {
		url = c.BaseURL + "/api/v1/chat/general"
	}

	payload, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.StreamingHTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.Header.Get("Deprecation") == "true" {
		// Once per process: the interactive loop calls this per turn, and a
		// warning re-printed mid-prompt on every turn is noise, not a signal.
		sunsetMessage := resp.Header.Get("Sunset")
		deprecatedChatWarningOnce.Do(func() {
			fmt.Fprintf(os.Stderr,
				"warning: this backend serves chat over a deprecated endpoint (sunset: %s).\n"+
					"         Upgrade the Ankra platform to one that offers /api/v1/chat/sessions.\n",
				sunsetMessage,
			)
		})
	}

	if resp.StatusCode != http.StatusOK {
		body, err := readResponseBody(resp)
		closeBody(resp)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, newUnexpectedResponseError("chat failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	events := make(chan ChatStreamEvent, 100)

	go func() {
		defer closeBody(resp)
		defer close(events)

		// The watchdog closes the body when nothing arrives for the idle
		// window, which unblocks the pending Read; heartbeat comment frames
		// reset it, so only a genuinely dead connection trips it.
		var idleTimedOut atomic.Bool
		watchdog := time.AfterFunc(chatStreamIdleTimeout, func() {
			idleTimedOut.Store(true)
			closeBody(resp)
		})
		defer watchdog.Stop()

		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			watchdog.Reset(chatStreamIdleTimeout)
			if readErr != nil {
				if idleTimedOut.Load() {
					events <- ChatStreamEvent{Type: "error",
						Error: fmt.Sprintf("stream idle timeout: no data received for %s", chatStreamIdleTimeout)}
				} else if readErr != io.EOF {
					events <- ChatStreamEvent{Type: "error", Error: readErr.Error()}
				}
				return
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					events <- ChatStreamEvent{Type: "done", Done: true}
					return
				}

				var event ChatStreamEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					events <- ChatStreamEvent{Type: "content", Content: data}
				} else {
					events <- event
				}
			}
		}
	}()

	return events, nil
}

func (c *Client) ListChatHistory(clusterID *string, limit, offset int) (*ListConversationsResponse, error) {
	var url string
	if clusterID != nil && *clusterID != "" {
		url = fmt.Sprintf("%s/api/v1/org/clusters/%s/kubernetes/chat/history?limit=%d&offset=%d",
			c.BaseURL, *clusterID, limit, offset)
	} else {
		url = fmt.Sprintf("%s/api/v1/chat/general/history?limit=%d&offset=%d",
			c.BaseURL, limit, offset)
	}

	var resp ListConversationsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to list chat history: %w", err)
	}
	return &resp, nil
}

func (c *Client) GetChatConversation(conversationID string) (*ChatConversation, error) {
	url := fmt.Sprintf("%s/api/v1/chat/general/history/%s",
		c.BaseURL, conversationID)
	var conv ChatConversation
	if err := c.getJSON(url, &conv); err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return &conv, nil
}

func (c *Client) DeleteChatConversation(conversationID string) (*DeleteConversationResponse, error) {
	url := fmt.Sprintf("%s/api/v1/chat/general/history/%s",
		c.BaseURL, conversationID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		body, err := readResponseBody(resp)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, newUnexpectedResponseError("delete failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &DeleteConversationResponse{Success: true, Message: "Conversation deleted"}, nil
}

func (c *Client) GetClusterHealth(clusterID string, includeAI bool) (*ClusterHealth, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/%s/kubernetes/health?include_ai_analysis=%t",
		c.BaseURL, clusterID, includeAI)
	var health ClusterHealth
	if err := c.getJSON(url, &health); err != nil {
		return nil, fmt.Errorf("failed to get cluster health: %w", err)
	}
	return &health, nil
}
