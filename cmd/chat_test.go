package cmd

import (
	"errors"
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
