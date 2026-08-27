package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type chatDeleteMock struct {
	baseMock
	called            bool
	gotConversationID string
}

func (m *chatDeleteMock) DeleteChatConversation(conversationID string) (*client.DeleteConversationResponse, error) {
	m.called = true
	m.gotConversationID = conversationID
	return &client.DeleteConversationResponse{Success: true}, nil
}

func TestChatDelete_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &chatDeleteMock{}
	resetConfirmFlag(t, chatDeleteCmd)
	_, err := runWithInput(t, mock, "n\n", "chat", "delete", "conv-1")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no delete call when declined")
	}
}

func TestChatDelete_YesProceeds(t *testing.T) {
	mock := &chatDeleteMock{}
	resetConfirmFlag(t, chatDeleteCmd)
	out, err := runWithInput(t, mock, "", "chat", "delete", "conv-1", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected delete call with --yes")
	}
	if mock.gotConversationID != "conv-1" {
		t.Errorf("conversation id = %q, want conv-1", mock.gotConversationID)
	}
}

func TestChatStatusText(t *testing.T) {
	testCases := []struct {
		name string
		data any
		want string
	}{
		{"legacy string status", "Analyzing cluster", "Analyzing cluster"},
		{"object with intent and mechanism", map[string]any{"intent": "Processing...", "mechanism": "tool_use"}, "Processing... · tool_use"},
		{"object with intent only", map[string]any{"intent": "Processing...", "mechanism": nil}, "Processing..."},
		{"object with nothing renderable", map[string]any{"elapsed_ms": float64(12)}, ""},
		{"nil data", nil, ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := chatStatusText(testCase.data); got != testCase.want {
				t.Errorf("chatStatusText(%v) = %q, want %q", testCase.data, got, testCase.want)
			}
		})
	}
}

type chatStreamMock struct {
	baseMock
	events []client.ChatStreamEvent
}

func (m *chatStreamMock) StreamChat(clusterID *string, chatReq client.ChatRequest) (<-chan client.ChatStreamEvent, error) {
	stream := make(chan client.ChatStreamEvent, len(m.events))
	for _, event := range m.events {
		stream <- event
	}
	close(stream)
	return stream, nil
}

func TestChat_BackendErrorFrameFailsWithMessage(t *testing.T) {
	mock := &chatStreamMock{events: []client.ChatStreamEvent{
		{Type: "error", Data: map[string]any{
			"message":         "You've sent too many messages this hour. Please wait a while and try again.",
			"is_rate_limited": true,
		}},
	}}
	_, err := runWithInput(t, mock, "", "chat", "hello")
	if err == nil {
		t.Fatal("expected the command to fail on a backend error frame, got nil (exit 0)")
	}
	if !strings.Contains(err.Error(), "too many messages this hour") {
		t.Errorf("error %q does not carry the backend message", err.Error())
	}
}

func TestChat_ErrorFrameWithoutDetailsStillFails(t *testing.T) {
	mock := &chatStreamMock{events: []client.ChatStreamEvent{
		{Type: "error"},
	}}
	_, err := runWithInput(t, mock, "", "chat", "hello")
	if err == nil {
		t.Fatal("expected the command to fail, got nil")
	}
	if !strings.Contains(err.Error(), "error without details") {
		t.Errorf("error %q should carry the fallback message", err.Error())
	}
}

func TestChat_CleanStreamExitsZero(t *testing.T) {
	mock := &chatStreamMock{events: []client.ChatStreamEvent{
		{Type: "status", Data: map[string]any{"intent": "Processing...", "mechanism": nil}},
		{Type: "content", Content: "All good."},
		{Type: "complete"},
	}}
	_, err := runWithInput(t, mock, "", "chat", "hello")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
