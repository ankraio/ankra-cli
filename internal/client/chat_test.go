package client

import (
	"fmt"
	"net/http"
	"testing"
)

func TestStreamChat_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/general" {
			t.Errorf("path = %s, want /api/v1/chat/general", r.URL.Path)
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %s, want text/event-stream", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support Flush")
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"content\":\"Hello\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"content\":\" world\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
	testClient := newTestClient(t, handler)
	req := ChatRequest{Query: "test"}
	events, err := testClient.StreamChat(nil, req)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	var receivedEvents []ChatStreamEvent
	for ev := range events {
		receivedEvents = append(receivedEvents, ev)
	}
	if len(receivedEvents) != 3 {
		t.Fatalf("received %d events, want 3: %v", len(receivedEvents), receivedEvents)
	}
	if receivedEvents[0].Type != "content" || receivedEvents[0].Content != "Hello" {
		t.Errorf("event 0: type=%s content=%s, want type=content content=Hello",
			receivedEvents[0].Type, receivedEvents[0].Content)
	}
	if receivedEvents[1].Type != "content" || receivedEvents[1].Content != " world" {
		t.Errorf("event 1: type=%s content=%s, want type=content content=\" world\"",
			receivedEvents[1].Type, receivedEvents[1].Content)
	}
	if !receivedEvents[2].Done || receivedEvents[2].Type != "done" {
		t.Errorf("event 2: Done=%v Type=%s, want Done=true Type=done",
			receivedEvents[2].Done, receivedEvents[2].Type)
	}
}

func TestStreamChat_ClusterScoped(t *testing.T) {
	clusterID := "cluster-123"
	handler := func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/api/v1/org/clusters/cluster-123/kubernetes/chat"
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support Flush")
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"content\":\"cluster response\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
	testClient := newTestClient(t, handler)
	req := ChatRequest{Query: "cluster query"}
	events, err := testClient.StreamChat(&clusterID, req)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	var count int
	for range events {
		count++
	}
	if count != 2 {
		t.Errorf("received %d events, want 2", count)
	}
}

func TestStreamChat_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "bad request"})
	}
	testClient := newTestClient(t, handler)
	req := ChatRequest{Query: "test"}
	_, err := testClient.StreamChat(nil, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListChatHistory_Success(t *testing.T) {
	expectedResponse := ListConversationsResponse{
		Conversations: []ChatConversation{
			{ID: "conv-1", Title: strPtr("First chat"), CreatedAt: "2024-01-01T00:00:00Z", UpdatedAt: "2024-01-01T00:00:00Z"},
		},
		TotalCount: 1,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/general/history" {
			t.Errorf("path = %s, want /api/v1/chat/general/history", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "0" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListChatHistory(nil, 10, 0)
	if err != nil {
		t.Fatalf("ListChatHistory: %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
	if len(result.Conversations) != 1 {
		t.Fatalf("Conversations len = %d, want 1", len(result.Conversations))
	}
	if result.Conversations[0].ID != "conv-1" {
		t.Errorf("Conversations[0].ID = %s, want conv-1", result.Conversations[0].ID)
	}
}

func TestListChatHistory_ClusterScoped(t *testing.T) {
	clusterID := "cluster-123"
	expectedResponse := ListConversationsResponse{
		Conversations: []ChatConversation{},
		TotalCount:    0,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/api/v1/org/clusters/cluster-123/kubernetes/chat/history"
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListChatHistory(&clusterID, 25, 5)
	if err != nil {
		t.Fatalf("ListChatHistory: %v", err)
	}
	if result.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", result.TotalCount)
	}
}

func TestGetChatConversation_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/general/history/conv-456" {
			t.Errorf("path = %s, want /api/v1/chat/general/history/conv-456", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, ChatConversation{
			ID:        "conv-456",
			Title:     strPtr("Test Conversation"),
			CreatedAt: "2025-01-01T00:00:00Z",
			UpdatedAt: "2025-01-02T00:00:00Z",
			Messages: []ChatMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi there"},
			},
		})
	}
	testClient := newTestClient(t, handler)
	got, err := testClient.GetChatConversation("conv-456")
	if err != nil {
		t.Fatalf("GetChatConversation() error = %v", err)
	}
	if got.ID != "conv-456" {
		t.Errorf("GetChatConversation() got.ID = %s, want conv-456", got.ID)
	}
	if len(got.Messages) != 2 {
		t.Errorf("GetChatConversation() got %d messages, want 2", len(got.Messages))
	}
}

func TestGetChatConversation_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetChatConversation("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetClusterHealth_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Query().Get("include_ai_analysis") != "true" {
			t.Errorf("include_ai_analysis = %s, want true", r.URL.Query().Get("include_ai_analysis"))
		}
		jsonResponse(t, w, http.StatusOK, ClusterHealth{
			OverallHealth: "healthy",
			Score:         95,
			LastUpdated:   "2025-06-01T00:00:00Z",
		})
	}
	testClient := newTestClient(t, handler)
	got, err := testClient.GetClusterHealth("cluster-123", true)
	if err != nil {
		t.Fatalf("GetClusterHealth() error = %v", err)
	}
	if got.OverallHealth != "healthy" || got.Score != 95 {
		t.Errorf("GetClusterHealth() got = %v", got)
	}
}

func TestGetClusterHealth_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetClusterHealth("cluster-123", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeleteChatConversation_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/general/history/conv-123" {
			t.Errorf("path = %s, want /api/v1/chat/general/history/conv-123", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, DeleteConversationResponse{Success: true, Message: "deleted"})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DeleteChatConversation("conv-123")
	if err != nil {
		t.Fatalf("DeleteChatConversation: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestDeleteChatConversation_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.DeleteChatConversation("conv-123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
