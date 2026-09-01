package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCreateChatSession_PostsTheConversationAndDecodesTheSession(t *testing.T) {
	var captured map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat/sessions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"sess-1","conversation_id":"conv-1","cluster_id":null,"mode":"ask","status":"pending","latest_sequence_number":0,"error":null}`)
	}
	testClient := newTestClient(t, handler)
	session, err := testClient.CreateChatSession(CreateChatSessionRequest{
		ConversationID: "conv-1", Mode: "ask", IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}
	if session.ID != "sess-1" || session.ConversationID != "conv-1" || session.Mode != "ask" {
		t.Fatalf("session = %+v", session)
	}
	if captured["conversation_id"] != "conv-1" || captured["mode"] != "ask" || captured["idempotency_key"] != "key-1" {
		t.Fatalf("request body = %v", captured)
	}
	if _, hasCluster := captured["cluster_id"]; hasCluster {
		t.Fatalf("a nil cluster must be omitted, body = %v", captured)
	}
}

func TestCreateChatSession_ClassifiesTheTwo404s(t *testing.T) {
	routeMissing := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})
	_, err := routeMissing.CreateChatSession(CreateChatSessionRequest{ConversationID: "conv-1"})
	if !errors.Is(err, ErrChatSessionsUnavailable) {
		t.Fatalf("route 404 = %v, want ErrChatSessionsUnavailable", err)
	}
	clusterMissing := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Cluster not found"}`)
	})
	clusterID := "gone"
	_, err = clusterMissing.CreateChatSession(CreateChatSessionRequest{ConversationID: "conv-1", ClusterID: &clusterID})
	if !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("cluster 404 = %v, want ErrClusterNotFound", err)
	}
	if errors.Is(err, ErrChatSessionsUnavailable) {
		t.Fatal("a cluster 404 must not read as a missing lane")
	}
}

func TestSubmitChatTurn_WrapsTheRequestAndSurfacesBackendDetail(t *testing.T) {
	var captured map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/sessions/sess-1/turns" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"session_id":"sess-1","last_seq":1}`)
	}
	testClient := newTestClient(t, handler)
	conversationID := "conv-1"
	submitted, err := testClient.SubmitChatTurn("sess-1", ChatRequest{
		Query: "hello", ConversationID: &conversationID, InteractionMode: "ask",
	})
	if err != nil {
		t.Fatalf("SubmitChatTurn: %v", err)
	}
	if submitted.LastSeq != 1 || submitted.SessionID != "sess-1" {
		t.Fatalf("submitted = %+v", submitted)
	}
	request, _ := captured["request"].(map[string]any)
	if request["query"] != "hello" || request["conversation_id"] != "conv-1" || request["interaction_mode"] != "ask" {
		t.Fatalf("wrapped request = %v", captured)
	}

	exhausted := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = fmt.Fprint(w, `{"error_code":"AI_ALLOWANCE_EXHAUSTED","detail":"Your monthly free AI allowance is used up."}`)
	})
	_, err = exhausted.SubmitChatTurn("sess-1", ChatRequest{Query: "hello"})
	if err == nil || !strings.Contains(err.Error(), "monthly free AI allowance") {
		t.Fatalf("402 error = %v, want the backend detail", err)
	}
}

func TestStreamChatSessionEvents_ParsesDurableFramesAndSkipsLiveMirrors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/sessions/sess-1/events" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("stream") != "true" || r.URL.Query().Get("since") != "1" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		frames := []string{
			": heartbeat\n\n",
			"id: 2\nevent: status\ndata: {\"intent\": \"Processing...\", \"mechanism\": null}\n\n",
			"event: content_delta\ndata: {\"kind\":\"content_delta\",\"offset\":0,\"text\":\"Hel\"}\n\n",
			"id: 3\nevent: content\ndata: \"Hello\"\n\n",
			"id: 4\nevent: content\ndata: \" world\"\n\n",
			"id: 5\nevent: complete\ndata: {\"response\": \"Hello world\"}\n\n",
			"id: 6\nevent: session_complete\ndata: {\"status\": \"completed\"}\n\n",
			"event: end\ndata: {\"status\": \"completed\", \"was_resumed\": false}\n\n",
		}
		for _, frame := range frames {
			_, _ = fmt.Fprint(w, frame)
			flusher.Flush()
		}
	}
	testClient := newTestClient(t, handler)
	events, err := testClient.StreamChatSessionEvents("sess-1", 1)
	if err != nil {
		t.Fatalf("StreamChatSessionEvents: %v", err)
	}
	var received []ChatStreamEvent
	for event := range events {
		received = append(received, event)
	}
	types := make([]string, 0, len(received))
	for _, event := range received {
		types = append(types, event.Type)
	}
	want := []string{"status", "content", "content", "complete", "session_complete", "end"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	if received[0].Sequence != 2 || received[1].Sequence != 3 || received[5].Sequence != 0 {
		t.Fatalf("sequences = %d/%d/%d, want 2/3/0", received[0].Sequence, received[1].Sequence, received[5].Sequence)
	}
	if received[1].Content != "Hello" || received[2].Content != " world" {
		t.Fatalf("content = %q / %q", received[1].Content, received[2].Content)
	}
	if status := chatStatus(received[0]); status != "Processing..." {
		t.Fatalf("status intent = %q", status)
	}
	if !received[5].Done {
		t.Fatal("the end frame must be marked done")
	}
}

func chatStatus(event ChatStreamEvent) string {
	data, _ := event.Data.(map[string]any)
	intent, _ := data["intent"].(string)
	return intent
}

func TestStreamChatSessionEvents_DropWithoutEndClosesWithoutAnError(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: 2\nevent: content\ndata: \"partial\"\n\n")
	}
	testClient := newTestClient(t, handler)
	events, err := testClient.StreamChatSessionEvents("sess-1", 1)
	if err != nil {
		t.Fatalf("StreamChatSessionEvents: %v", err)
	}
	var received []ChatStreamEvent
	for event := range events {
		received = append(received, event)
	}
	if len(received) != 1 || received[0].Type != "content" {
		t.Fatalf("events = %+v, want just the partial content (a clean EOF is the caller's resume cue)", received)
	}
}

func TestStreamChatSessionEvents_IdleTimeoutSurfacesALocalError(t *testing.T) {
	previous := chatStreamIdleTimeout
	chatStreamIdleTimeout = 150 * time.Millisecond
	t.Cleanup(func() { chatStreamIdleTimeout = previous })
	release := make(chan struct{})
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-release
	}
	testClient := newTestClient(t, handler)
	t.Cleanup(func() { close(release) })
	events, err := testClient.StreamChatSessionEvents("sess-1", 0)
	if err != nil {
		t.Fatalf("StreamChatSessionEvents: %v", err)
	}
	var received []ChatStreamEvent
	for event := range events {
		received = append(received, event)
	}
	if len(received) != 1 || received[0].Type != "error" || received[0].Data != nil ||
		!strings.Contains(received[0].Error, "idle timeout") {
		t.Fatalf("events = %+v, want one local idle-timeout error", received)
	}
}

func TestStreamChatSessionEvents_NotFoundClassifies(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})
	_, err := testClient.StreamChatSessionEvents("sess-1", 0)
	if !errors.Is(err, ErrChatSessionsUnavailable) {
		t.Fatalf("err = %v, want ErrChatSessionsUnavailable", err)
	}
}
