package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"
)

// chatSessionMock scripts the sessions lane: every created session gets the
// next scripted tail, and every request is recorded for assertions.
type chatSessionMock struct {
	baseMock
	mutex sync.Mutex

	createErrors []error // consumed in order; nil means success
	created      []client.CreateChatSessionRequest
	submitted    []client.ChatRequest
	tails        [][]client.ChatStreamEvent // one per StreamChatSessionEvents call
	tailSince    []int64
	legacyEvents []client.ChatStreamEvent
	legacyCalls  int
}

func (m *chatSessionMock) CreateChatSession(request client.CreateChatSessionRequest) (*client.ChatSession, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.created = append(m.created, request)
	if len(m.createErrors) > 0 {
		err := m.createErrors[0]
		m.createErrors = m.createErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &client.ChatSession{ID: "sess-" + request.IdempotencyKey, ConversationID: request.ConversationID,
		ClusterID: request.ClusterID, Mode: request.Mode, Status: "pending"}, nil
}

func (m *chatSessionMock) SubmitChatTurn(sessionID string, request client.ChatRequest) (*client.SubmitChatTurnResponse, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.submitted = append(m.submitted, request)
	return &client.SubmitChatTurnResponse{SessionID: sessionID, LastSeq: 1}, nil
}

func (m *chatSessionMock) StreamChatSessionEvents(_ string, since int64) (<-chan client.ChatStreamEvent, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.tailSince = append(m.tailSince, since)
	if len(m.tails) == 0 {
		return nil, errors.New("no tail scripted")
	}
	tail := m.tails[0]
	m.tails = m.tails[1:]
	stream := make(chan client.ChatStreamEvent, len(tail))
	for _, event := range tail {
		stream <- event
	}
	close(stream)
	return stream, nil
}

func (m *chatSessionMock) StreamChat(_ *string, _ client.ChatRequest) (<-chan client.ChatStreamEvent, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.legacyCalls++
	stream := make(chan client.ChatStreamEvent, len(m.legacyEvents))
	for _, event := range m.legacyEvents {
		stream <- event
	}
	close(stream)
	return stream, nil
}


// resetChatFlags clears the chat command's flag values after a test: cobra
// keeps the last parsed value on the shared root command, so a
// --conversation or --mode from one test would leak into the next.
func resetChatFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range []string{"conversation", "mode", "cluster"} {
			_ = chatCmd.Flags().Set(name, "")
		}
	})
}

func endFrame() client.ChatStreamEvent {
	return client.ChatStreamEvent{Type: "end", Data: map[string]any{"status": "completed"}, Done: true}
}

func contentFrame(sequence int64, text string) client.ChatStreamEvent {
	return client.ChatStreamEvent{Type: "content", Data: text, Content: text, Sequence: sequence}
}

func TestChatOneShot_RunsOnSessionsAndNamesTheConversation(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "status", Data: map[string]any{"intent": "Processing...", "mechanism": nil}, Sequence: 2},
		contentFrame(3, "Hello"),
		contentFrame(4, " world"),
		{Type: "complete", Data: map[string]any{"response": "Hello world"}, Sequence: 5},
		endFrame(),
	}}}
	var runErr error
	var stderrOutput string
	stdoutOutput := captureStdout(t, func() {
		stderrOutput = captureStderr(t, func() {
			_, runErr = runWithInput(t, mock, "", "chat", "--mode", "ask", "hello")
		})
	})
	if runErr != nil {
		t.Fatalf("chat failed: %v", runErr)
	}
	if !strings.Contains(stdoutOutput, "Hello world") {
		t.Fatalf("stdout = %q, want the streamed answer", stdoutOutput)
	}
	if len(mock.created) != 1 || mock.created[0].Mode != "ask" || mock.created[0].ConversationID == "" {
		t.Fatalf("created sessions = %+v, want one ask-mode session on a fresh conversation", mock.created)
	}
	if len(mock.submitted) != 1 || mock.submitted[0].ConversationID == nil ||
		*mock.submitted[0].ConversationID != mock.created[0].ConversationID {
		t.Fatalf("submitted = %+v, want the turn bound to the session's conversation", mock.submitted)
	}
	if mock.submitted[0].ConversationHistory != nil {
		t.Fatal("the sessions lane must not resend history")
	}
	if len(mock.tailSince) != 1 || mock.tailSince[0] != 1 {
		t.Fatalf("tail since = %v, want [1] (after the persisted user turn)", mock.tailSince)
	}
	if !strings.Contains(stderrOutput, "conversation "+mock.created[0].ConversationID) ||
		!strings.Contains(stderrOutput, "--conversation "+mock.created[0].ConversationID) {
		t.Fatalf("stderr = %q, want the conversation id and how to continue it", stderrOutput)
	}
	if strings.Contains(stdoutOutput, "conversation "+mock.created[0].ConversationID) {
		t.Fatal("the conversation id must stay off stdout so piped answers stay clean")
	}
	if mock.legacyCalls != 0 {
		t.Fatal("the deprecated stream must not be used when sessions work")
	}
}

func TestChatOneShot_ContinuesTheGivenConversation(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "Sure."), endFrame()}}}
	stderrOutput := captureStderr(t, func() {
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "chat", "--conversation",
				"a75c06a2-5100-41f2-bdde-778c5a74200c", "and then?"); err != nil {
				t.Fatalf("chat failed: %v", err)
			}
		})
	})
	if mock.created[0].ConversationID != "a75c06a2-5100-41f2-bdde-778c5a74200c" {
		t.Fatalf("conversation = %q, want the --conversation value", mock.created[0].ConversationID)
	}
	if strings.Contains(stderrOutput, "continue with") {
		t.Fatalf("stderr = %q; a continued conversation needs no how-to-continue hint", stderrOutput)
	}
	if _, err := runWithInput(t, mock, "", "chat", "--conversation", "has space", "x"); err == nil ||
		!strings.Contains(err.Error(), "invalid --conversation") {
		t.Fatalf("err = %v, want the --conversation validation", err)
	}
}

func TestChatOneShot_ErrorFrameFailsWithTheCode(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "error", Data: map[string]any{
			"message":    "The AI service is temporarily unavailable. Please try again shortly.",
			"error_code": "ai_platform_billing",
			"detail":     "messages API returned HTTP 400: credit balance is too low",
		}, Sequence: 2},
		endFrame(),
	}}}
	t.Setenv("ANKRA_DEBUG", "")
	var runErr error
	captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "temporarily unavailable") ||
		!strings.Contains(runErr.Error(), "ai_platform_billing") {
		t.Fatalf("err = %v, want the message with its stable code", runErr)
	}
	if strings.Contains(runErr.Error(), "credit balance") {
		t.Fatalf("err = %v; the operator detail must stay behind ANKRA_DEBUG", runErr)
	}
	t.Setenv("ANKRA_DEBUG", "1")
	mock.tails = [][]client.ChatStreamEvent{{
		{Type: "error", Data: map[string]any{"message": "unavailable", "detail": "credit balance is too low"}},
		endFrame(),
	}}
	captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "credit balance") {
		t.Fatalf("err = %v, want the detail under ANKRA_DEBUG", runErr)
	}
}

func TestChatOneShot_ResumesADroppedTailFromItsLastSequence(t *testing.T) {
	resetChatFlags(t)
	previous := chatTailReconnectDelay
	chatTailReconnectDelay = time.Millisecond
	t.Cleanup(func() { chatTailReconnectDelay = previous })
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{
		{contentFrame(2, "Hello"), {Type: "error", Error: "read: connection reset"}},
		{contentFrame(3, " world"), endFrame()},
	}}
	var runErr error
	stdoutOutput := captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr != nil {
		t.Fatalf("chat failed: %v", runErr)
	}
	if !strings.Contains(stdoutOutput, "Hello world") {
		t.Fatalf("stdout = %q, want the answer stitched across the reconnect", stdoutOutput)
	}
	if len(mock.tailSince) != 2 || mock.tailSince[1] != 2 {
		t.Fatalf("tail since = %v, want the resume from the last durable sequence", mock.tailSince)
	}
}

func TestChatOneShot_GivesUpAfterTooManyDrops(t *testing.T) {
	resetChatFlags(t)
	previous := chatTailReconnectDelay
	chatTailReconnectDelay = time.Millisecond
	t.Cleanup(func() { chatTailReconnectDelay = previous })
	drop := []client.ChatStreamEvent{{Type: "error", Error: "read: connection reset"}}
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{drop, drop, drop, drop, drop}}
	var runErr error
	captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "could not be resumed") {
		t.Fatalf("err = %v, want the resume give-up", runErr)
	}
	if len(mock.tailSince) != chatTailMaxReconnects+1 {
		t.Fatalf("tail opens = %d, want the initial open plus %d reconnects", len(mock.tailSince), chatTailMaxReconnects)
	}
}

func TestChatOneShot_FallsBackToTheDeprecatedStreamWithoutSessions(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{
		createErrors: []error{client.ErrChatSessionsUnavailable},
		legacyEvents: []client.ChatStreamEvent{{Type: "content", Content: "Legacy answer."}, {Type: "complete"}},
	}
	var runErr error
	stdoutOutput := captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr != nil {
		t.Fatalf("chat failed: %v", runErr)
	}
	if mock.legacyCalls != 1 || !strings.Contains(stdoutOutput, "Legacy answer.") {
		t.Fatalf("legacy calls = %d, stdout = %q", mock.legacyCalls, stdoutOutput)
	}
}

func writeStaleSelection(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ankra"), 0o700); err != nil {
		t.Fatal(err)
	}
	selection, _ := json.Marshal(client.ClusterListItem{ID: "b765f99a-ec5c-48ab-98a9-9b4f1857eff2", Name: "tael-ops"})
	if err := os.WriteFile(filepath.Join(home, ".ankra", "selected.json"), selection, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChatOneShot_StaleSelectionDegradesToGlobalWithANotice(t *testing.T) {
	resetChatFlags(t)
	writeStaleSelection(t)
	mock := &chatSessionMock{
		createErrors: []error{client.ErrClusterNotFound},
		tails:        [][]client.ChatStreamEvent{{contentFrame(2, "Global answer."), endFrame()}},
	}
	var runErr error
	var stderrOutput string
	stdoutOutput := captureStdout(t, func() {
		stderrOutput = captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr != nil {
		t.Fatalf("chat failed: %v", runErr)
	}
	if !strings.Contains(stdoutOutput, "Global answer.") {
		t.Fatalf("stdout = %q", stdoutOutput)
	}
	if !strings.Contains(stderrOutput, `selected cluster "tael-ops" no longer exists`) ||
		!strings.Contains(stderrOutput, "ankra cluster clear") {
		t.Fatalf("stderr = %q, want the stale-selection notice with the fix", stderrOutput)
	}
	if len(mock.created) != 2 || mock.created[0].ClusterID == nil || mock.created[1].ClusterID != nil {
		t.Fatalf("created = %+v, want the selected cluster first, then the global lane", mock.created)
	}
}

func TestChatOneShot_StaleSelectionDegradesOnlyOnce(t *testing.T) {
	resetChatFlags(t)
	writeStaleSelection(t)
	// The degrade is a single step: a not-found on the global retry is a
	// real failure and surfaces.
	mock := &chatSessionMock{createErrors: []error{client.ErrClusterNotFound, client.ErrClusterNotFound}}
	var runErr error
	captureStdout(t, func() {
		captureStderr(t, func() { _, runErr = runWithInput(t, mock, "", "chat", "hello") })
	})
	if runErr == nil || !errors.Is(runErr, client.ErrClusterNotFound) {
		t.Fatalf("err = %v, want the cluster-not-found surfaced after one degrade", runErr)
	}
}

func TestChatInteractive_KeepsOneConversationAcrossTurns(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{
		{contentFrame(2, "pong"), endFrame()},
		{contentFrame(2, "ping"), endFrame()},
	}}
	var runErr error
	stdoutOutput := captureStdout(t, func() {
		captureStderr(t, func() {
			_, runErr = runWithInput(t, mock, "say pong\nsay ping\nexit\n", "chat")
		})
	})
	if runErr != nil {
		t.Fatalf("chat failed: %v", runErr)
	}
	if len(mock.created) != 2 || mock.created[0].ConversationID != mock.created[1].ConversationID {
		t.Fatalf("created = %+v, want two sessions on one conversation", mock.created)
	}
	for index, request := range mock.submitted {
		if request.ConversationHistory != nil {
			t.Fatalf("turn %d resent history; the server owns the transcript", index)
		}
	}
	if !strings.Contains(stdoutOutput, "Conversation: "+mock.created[0].ConversationID) {
		t.Fatalf("stdout = %q, want the conversation id in the header", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "pong") || !strings.Contains(stdoutOutput, "ping") {
		t.Fatalf("stdout = %q, want both answers", stdoutOutput)
	}
}

func TestChatInteractive_ClearStartsANewConversation(t *testing.T) {
	resetChatFlags(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{
		{contentFrame(2, "one"), endFrame()},
		{contentFrame(2, "two"), endFrame()},
	}}
	captureStdout(t, func() {
		captureStderr(t, func() {
			if _, err := runWithInput(t, mock, "first\nclear\nsecond\nexit\n", "chat"); err != nil {
				t.Fatalf("chat failed: %v", err)
			}
		})
	})
	if len(mock.created) != 2 || mock.created[0].ConversationID == mock.created[1].ConversationID {
		t.Fatalf("created = %+v, want 'clear' to start a fresh conversation", mock.created)
	}
}

func TestChatShow_RefusesANonConversationID(t *testing.T) {
	resetChatFlags(t)
	_, err := runWithInput(t, &baseMock{}, "", "chat", "show", "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "invalid conversation id") {
		t.Fatalf("err = %v, want the id validation", err)
	}
	_, err = runWithInput(t, &baseMock{}, "", "chat", "delete", "--yes", "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "invalid conversation id") {
		t.Fatalf("delete err = %v, want the id validation", err)
	}
}

func TestSessionModeForInteraction(t *testing.T) {
	cases := map[string]string{"": "", "ask": "ask", "agentic": "agent", "plan": "plan", "other": ""}
	for interaction, want := range cases {
		if got := sessionModeForInteraction(interaction); got != want {
			t.Errorf("sessionModeForInteraction(%q) = %q, want %q", interaction, got, want)
		}
	}
}

func TestNewChatUUIDIsV4(t *testing.T) {
	id, err := newChatUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !looksLikeUUID(id) || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Fatalf("id = %q, want a v4 UUID", id)
	}
	other, _ := newChatUUID()
	if other == id {
		t.Fatal("two ids must differ")
	}
}
