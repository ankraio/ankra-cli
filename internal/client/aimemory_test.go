package client

import (
	"errors"
	"net/http"
	"testing"
)

func TestListAIMemories_ReadsTheCallersMemories(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/ai-memory/memories" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("request = %s %s, want GET /api/v1/org/ai-memory/memories?limit=200", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"memories": []map[string]any{
			{"id": "0b8f6c2e-0000-4000-8000-000000000001", "content": "prefers cx33 workers", "cluster_id": nil, "source_conversation_id": nil, "created_at": "2026-10-16T22:28:58Z"},
		}})
	}
	memories, listError := newTestClient(t, handler).ListAIMemories(AIMemoryListCap)
	if listError != nil {
		t.Fatalf("ListAIMemories: %v", listError)
	}
	if len(memories) != 1 || memories[0].Content != "prefers cx33 workers" || memories[0].ClusterID != nil {
		t.Fatalf("memories = %+v", memories)
	}
}

// An answer with no list is not an empty store: the caller must not read it as "no memories".
func TestListAIMemories_RefusesAnAnswerWithoutAList(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"items": []any{}})
	}
	if _, listError := newTestClient(t, handler).ListAIMemories(10); listError == nil {
		t.Fatal("an answer without memories must be an error, not an empty list")
	}
}

func TestClearAIMemories_DeletesAndReportsTheCount(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/org/ai-memory/memories" {
			t.Errorf("request = %s %s, want DELETE /api/v1/org/ai-memory/memories", r.Method, r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"deleted": 37})
	}
	deleted, clearError := newTestClient(t, handler).ClearAIMemories()
	if clearError != nil || deleted != 37 {
		t.Fatalf("ClearAIMemories = %d, %v; want 37, nil", deleted, clearError)
	}
}

func TestClearAIMemories_SurfacesAPlatformWithoutTheRoute(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	_, clearError := newTestClient(t, handler).ClearAIMemories()
	var unexpected *UnexpectedResponseError
	if !errors.As(clearError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("clear error = %v, want an UnexpectedResponseError 404", clearError)
	}
}
