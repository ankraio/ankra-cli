package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

func fastChatReconnects(t *testing.T) {
	t.Helper()
	previous := chatTailReconnectDelay
	chatTailReconnectDelay = time.Millisecond
	t.Cleanup(func() { chatTailReconnectDelay = previous })
}

func waitingPark(sequence int64) client.ChatStreamEvent {
	return client.ChatStreamEvent{Type: "session_complete", Data: map[string]any{"status": "waiting"}, Sequence: sequence}
}

func TestChatOneShotExitsWaitingWhenTheAgentPauses(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "Checking."), waitingPark(3)}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "-o", "json", "upgrade it")
	if exitCodeFor(err) != exitWaiting {
		t.Fatalf("exit code = %d (%v), want %d\nstderr: %s", exitCodeFor(err), err, exitWaiting, stderr)
	}
	if len(mock.tailSince) != 1 {
		t.Errorf("tails opened = %v, want one without --wait", mock.tailSince)
	}
	document := decodeChatResult(t, stdout)
	if document["state"] != "waiting" || document["error"] != nil {
		t.Errorf("document = %v, want state waiting and no error", document)
	}
}

func TestChatOneShotAwaitingUserStaysExitZero(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{parkedProposalTail()}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--mode", "agent", "-o", "json", "restart payments")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	if document := decodeChatResult(t, stdout); document["state"] != "awaiting_user" {
		t.Errorf("state = %v, want awaiting_user", document["state"])
	}
}

func TestChatOneShotCompletedState(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		contentFrame(2, "All good."),
		{Type: "session_complete", Data: map[string]any{"status": "completed"}, Sequence: 3},
		{Type: "end", Sequence: 4},
	}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "-o", "json", "how is it")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	if document := decodeChatResult(t, stdout); document["state"] != "completed" {
		t.Errorf("state = %v, want completed", document["state"])
	}
}

func TestChatWaitFollowsThroughRepeatedPauses(t *testing.T) {
	fastChatReconnects(t)
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{
		{contentFrame(2, "Checking. "), waitingPark(3)},
		{}, // a quiet tail that closes during the park
		{contentFrame(4, "Still going. "), waitingPark(5)},
		{contentFrame(6, "Done."), {Type: "session_complete", Data: map[string]any{"status": "completed"}, Sequence: 7}, {Type: "end", Sequence: 8}},
	}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--wait", "-o", "json", "upgrade it")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	document := decodeChatResult(t, stdout)
	if document["state"] != "completed" || document["answer"] != "Checking. Still going. Done." {
		t.Errorf("document = %v, want the whole answer, completed", document)
	}
	if got := mock.tailSince[len(mock.tailSince)-1]; got != 5 {
		t.Errorf("last tail opened from %d, want the cursor 5", got)
	}
}

func TestChatWaitSurvivesMoreDropsThanTheBudgetWhileParked(t *testing.T) {
	fastChatReconnects(t)
	quietTails := make([][]client.ChatStreamEvent, 0, chatTailMaxReconnects+4)
	quietTails = append(quietTails, []client.ChatStreamEvent{waitingPark(2)})
	for range chatTailMaxReconnects + 2 {
		quietTails = append(quietTails, []client.ChatStreamEvent{})
	}
	quietTails = append(quietTails, []client.ChatStreamEvent{
		contentFrame(3, "Done."),
		{Type: "end", Sequence: 4},
	})
	mock := &chatSessionMock{tails: quietTails}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--wait", "-o", "json", "upgrade it")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	if document := decodeChatResult(t, stdout); document["answer"] != "Done." {
		t.Errorf("document = %v", document)
	}
}

func TestChatWaitExpiresWithItsOwnExitCode(t *testing.T) {
	fastChatReconnects(t)
	mock := &chatSessionMock{
		tails:      [][]client.ChatStreamEvent{{waitingPark(2)}},
		tailErrors: []error{nil, errors.New("connection reset"), errors.New("connection reset"), errors.New("connection reset")},
	}
	stdout, _, err := runChatStructured(t, mock, "chat", "--wait", "--wait-timeout", "20ms", "-o", "json", "upgrade it")
	if exitCodeFor(err) != exitWaitTimeout {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(err), err, exitWaitTimeout)
	}
	if document := decodeChatResult(t, stdout); document["state"] != "waiting" {
		t.Errorf("state = %v, want waiting", document["state"])
	}
}

func TestChatWaitRefusedWhereItMeansNothing(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "no_question", args: []string{"chat", "--wait"}, want: "one-shot"},
		{name: "zero_timeout", args: []string{"chat", "--wait", "--wait-timeout", "0s", "hello"}, want: "--wait-timeout"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := runChatStructured(t, &chatSessionMock{}, testCase.args...)
			if exitCodeFor(err) != exitUsage || err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v (exit %d), want a usage error naming %q", err, exitCodeFor(err), testCase.want)
			}
		})
	}
}
