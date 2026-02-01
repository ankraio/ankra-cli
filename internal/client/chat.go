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

// ChatMessage represents a message in a chat conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the request for sending a chat message
type ChatRequest struct {
	Query               string        `json:"query"`
	ConversationID      *string       `json:"conversation_id,omitempty"`
	ConversationHistory []ChatMessage `json:"conversation_history,omitempty"`
}

// ChatConversation represents a saved chat conversation
type ChatConversation struct {
	ID        string        `json:"id"`
	Title     *string       `json:"title,omitempty"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
	Messages  []ChatMessage `json:"messages,omitempty"`
	ClusterID *string       `json:"cluster_id,omitempty"`
}

// ListConversationsResponse is the response from listing conversations
type ListConversationsResponse struct {
	Conversations []ChatConversation `json:"conversations"`
	TotalCount    int                `json:"total_count"`
}

// DeleteConversationResponse is the response from deleting a conversation
type DeleteConversationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ClusterHealth represents cluster health analysis
type ClusterHealth struct {
	OverallHealth   string   `json:"overall_health"`
	Score           int      `json:"score"`
	Issues          []string `json:"issues,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
	LastUpdated     string   `json:"last_updated"`
}

// ChatStreamEvent represents a streaming event from chat
type ChatStreamEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	Content string `json:"content,omitempty"` // Fallback for content field
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

// StreamChat sends a chat message and returns a channel of streaming events
func StreamChat(token, baseURL string, clusterID *string, req ChatRequest) (<-chan ChatStreamEvent, error) {
	var url string
	if clusterID != nil && *clusterID != "" {
		url = fmt.Sprintf("%s/api/v1/org/clusters/%s/kubernetes/chat", strings.TrimRight(baseURL, "/"), *clusterID)
	} else {
		url = strings.TrimRight(baseURL, "/") + "/api/v1/chat/general"
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
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
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					events <- ChatStreamEvent{Type: "error", Error: err.Error()}
				}
				return
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Parse SSE format
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					events <- ChatStreamEvent{Type: "done", Done: true}
					return
				}

				var event ChatStreamEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					// If it's not JSON, treat it as plain content
					events <- ChatStreamEvent{Type: "content", Content: data}
				} else {
					events <- event
				}
			}
		}
	}()

	return events, nil
}

// ListChatHistory returns chat conversations
func ListChatHistory(token, baseURL string, clusterID *string, limit, offset int) (*ListConversationsResponse, error) {
	var url string
	if clusterID != nil && *clusterID != "" {
		url = fmt.Sprintf("%s/api/v1/org/clusters/%s/kubernetes/chat/history?limit=%d&offset=%d",
			strings.TrimRight(baseURL, "/"), *clusterID, limit, offset)
	} else {
		url = fmt.Sprintf("%s/api/v1/chat/general/history?limit=%d&offset=%d",
			strings.TrimRight(baseURL, "/"), limit, offset)
	}

	var resp ListConversationsResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to list chat history: %w", err)
	}
	return &resp, nil
}

// GetChatConversation returns a specific conversation
func GetChatConversation(token, baseURL, conversationID string) (*ChatConversation, error) {
	url := fmt.Sprintf("%s/api/v1/chat/general/history/%s",
		strings.TrimRight(baseURL, "/"), conversationID)
	var conv ChatConversation
	if err := getJSON(url, token, &conv); err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return &conv, nil
}

// DeleteChatConversation deletes a conversation
func DeleteChatConversation(token, baseURL, conversationID string) (*DeleteConversationResponse, error) {
	url := fmt.Sprintf("%s/api/v1/chat/general/history/%s",
		strings.TrimRight(baseURL, "/"), conversationID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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

// GetClusterHealth returns AI-analyzed cluster health
func GetClusterHealth(token, baseURL, clusterID string, includeAI bool) (*ClusterHealth, error) {
	url := fmt.Sprintf("%s/api/v1/chat/clusters/%s/health?include_ai_analysis=%t",
		strings.TrimRight(baseURL, "/"), clusterID, includeAI)
	var health ClusterHealth
	if err := getJSON(url, token, &health); err != nil {
		return nil, fmt.Errorf("failed to get cluster health: %w", err)
	}
	return &health, nil
}
