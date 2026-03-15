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
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Query               string        `json:"query"`
	ConversationID      *string       `json:"conversation_id,omitempty"`
	ConversationHistory []ChatMessage `json:"conversation_history,omitempty"`
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

type ClusterHealth struct {
	OverallHealth   string   `json:"overall_health"`
	Score           int      `json:"score"`
	Issues          []string `json:"issues,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
	LastUpdated     string   `json:"last_updated"`
}

type ChatStreamEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

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

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("chat failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	events := make(chan ChatStreamEvent, 100)

	go func() {
		defer resp.Body.Close()
		defer close(events)

		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				if readErr != io.EOF {
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
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
