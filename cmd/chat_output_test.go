package cmd

// Tests for `ankra chat -o json|yaml "<question>"` (ankra-gtpc1): a CI job
// parses the one document on stdout, so everything else the turn says has
// to stay on stderr, and every member has to come from the turn itself.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"ankra/internal/client"
)

// chatResolvedModeMock answers a session created without a mode the way the
// platform does: it resolves the server default and echoes it back.
type chatResolvedModeMock struct {
	chatSessionMock
	resolvedMode string
}

func (m *chatResolvedModeMock) CreateChatSession(request client.CreateChatSessionRequest) (*client.ChatSession, error) {
	session, err := m.chatSessionMock.CreateChatSession(request)
	if err != nil {
		return nil, err
	}
	if session.Mode == "" {
		session.Mode = m.resolvedMode
	}
	return session, nil
}

// chatClusterMock resolves --cluster by name.
type chatClusterMock struct {
	chatSessionMock
	cluster client.ClusterListItem
}

func (m *chatClusterMock) GetCluster(string) (client.ClusterListItem, error) {
	return m.cluster, nil
}

// runChatStructured runs the command with stdout and stderr kept apart, and
// also captures anything written to os.Stdout directly, which a structured
// run must not do.
func runChatStructured(t *testing.T, mock APIClient, args ...string) (string, string, error) {
	t.Helper()
	resetChatFlags(t)
	// No persisted selection leaks in from the machine running the tests.
	t.Setenv("HOME", t.TempDir())
	setMockClient(t, mock)
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	var runErr error
	direct := captureStdout(t, func() {
		captureStderr(t, func() { runErr = rootCmd.Execute() })
	})
	if strings.TrimSpace(direct) != "" {
		t.Fatalf("a structured run wrote to os.Stdout directly: %q", direct)
	}
	return stdout.String(), stderr.String(), runErr
}

func decodeChatResult(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\nstdout: %q", err, stdout)
	}
	return document
}

func TestChatOneShotJSON_CarriesAnswerConversationModeToolsAndActions(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "status", Data: map[string]any{"intent": "Listing pods", "mechanism": nil}, Sequence: 2},
		{Type: "tool_start", Data: map[string]any{"tool_name": "get_pods", "tool_call_id": "toolu_1", "status": "preparing"}, Sequence: 3},
		{Type: "tool_result", Data: map[string]any{"tool_name": "get_pods", "tool_call_id": "toolu_1", "success": true,
			"data": map[string]any{"pods": []any{"a", "b"}}, "execution_time_ms": float64(42)}, Sequence: 4},
		{Type: "tool_result", Data: map[string]any{"tool_name": "get_events", "tool_call_id": "toolu_2", "success": false,
			"error": "namespace not found", "error_class": "not_found", "error_code": "NOT_FOUND"}, Sequence: 5},
		contentFrame(6, "Two pods are "),
		contentFrame(7, "crash-looping."),
		{Type: "action_proposal", Data: map[string]any{"action_id": "act-1", "tool_name": "restart_deployment",
			"description": "Restart payments", "risk_level": "medium", "reversible": true}, Sequence: 8},
		endFrame(),
	}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--mode", "ask", "-o", "json", "why are pods failing?")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	document := decodeChatResult(t, stdout)

	if document["answer"] != "Two pods are crash-looping." {
		t.Errorf("answer = %v, want the streamed text joined", document["answer"])
	}
	if document["conversation_id"] != mock.created[0].ConversationID || document["continuable"] != true {
		t.Errorf("conversation = %v (continuable %v), want the session's conversation %s, continuable",
			document["conversation_id"], document["continuable"], mock.created[0].ConversationID)
	}
	if sessionID, _ := document["session_id"].(string); !strings.HasPrefix(sessionID, "sess-") {
		t.Errorf("session_id = %v, want the session the platform opened", document["session_id"])
	}
	if document["mode"] != "ask" {
		t.Errorf("mode = %v, want ask", document["mode"])
	}
	if clusterID, present := document["cluster_id"]; !present || clusterID != nil {
		t.Errorf("cluster_id = %v (present %v), want an explicit null for an organisation-wide turn", clusterID, present)
	}

	toolCalls, _ := document["tool_calls"].([]any)
	if len(toolCalls) != 2 {
		t.Fatalf("tool_calls = %v, want two calls", document["tool_calls"])
	}
	first, _ := toolCalls[0].(map[string]any)
	if first["tool_name"] != "get_pods" || first["tool_call_id"] != "toolu_1" || first["success"] != true ||
		first["execution_time_ms"] != float64(42) {
		t.Errorf("first tool call = %v, want get_pods succeeded in 42 ms", first)
	}
	if _, leaked := first["data"]; leaked {
		t.Error("the tool's output must not be copied into the document")
	}
	second, _ := toolCalls[1].(map[string]any)
	if second["tool_name"] != "get_events" || second["success"] != false || second["error"] != "namespace not found" ||
		second["error_class"] != "not_found" || second["error_code"] != "NOT_FOUND" {
		t.Errorf("second tool call = %v, want the failure with its error, class and code", second)
	}

	actions, _ := document["pending_actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("pending_actions = %v, want the proposed write", document["pending_actions"])
	}
	if action, _ := actions[0].(map[string]any); action["action_id"] != "act-1" || action["tool_name"] != "restart_deployment" {
		t.Errorf("pending action = %v", actions[0])
	}
	if _, hasError := document["error"]; hasError {
		t.Errorf("error = %v on a clean turn, want it omitted", document["error"])
	}
	if strings.Contains(stdout, "Listing pods") || strings.Contains(stdout, "awaiting confirmation") {
		t.Errorf("stdout carries the human transcript: %q", stdout)
	}
	if len(mock.titleCalls) != 1 {
		t.Errorf("title calls = %v, want the new conversation named like a text run", mock.titleCalls)
	}
}

func TestChatOneShotJSON_ReportsTheModeTheServerResolved(t *testing.T) {
	mock := &chatResolvedModeMock{resolvedMode: "agent",
		chatSessionMock: chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "ok"), endFrame()}}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	document := decodeChatResult(t, stdout)
	if document["mode"] != "agent" {
		t.Errorf("mode = %v, want the server default the session resolved", document["mode"])
	}
	if calls, _ := document["tool_calls"].([]any); calls == nil {
		t.Errorf("tool_calls = %v, want [] rather than null", document["tool_calls"])
	}
	if actions, _ := document["pending_actions"].([]any); actions == nil {
		t.Errorf("pending_actions = %v, want [] rather than null", document["pending_actions"])
	}
}

func TestChatOneShotJSON_NamesTheCluster(t *testing.T) {
	mock := &chatClusterMock{cluster: client.ClusterListItem{ID: "6f1d7c1e-3a8b-4d0e-9c2f-1b2a3c4d5e6f", Name: "prod"},
		chatSessionMock: chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "ok"), endFrame()}}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "--cluster", "prod", "-o", "json", "hello")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	document := decodeChatResult(t, stdout)
	if document["cluster_id"] != "6f1d7c1e-3a8b-4d0e-9c2f-1b2a3c4d5e6f" || document["cluster_name"] != "prod" {
		t.Errorf("cluster = %v / %v, want prod's id and name", document["cluster_id"], document["cluster_name"])
	}
	if mock.created[0].ClusterID == nil || *mock.created[0].ClusterID != "6f1d7c1e-3a8b-4d0e-9c2f-1b2a3c4d5e6f" {
		t.Errorf("session cluster = %v, want the turn scoped to prod", mock.created[0].ClusterID)
	}
}

func TestChatOneShotJSON_FailedTurnStillPrintsTheDocument(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		contentFrame(2, "Partial"),
		{Type: "error", Data: map[string]any{"message": "You've sent too many messages this hour."}, Sequence: 3},
		endFrame(),
	}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err == nil || !strings.Contains(err.Error(), "too many messages") {
		t.Fatalf("err = %v, want the turn's failure (a non-zero exit)", err)
	}
	document := decodeChatResult(t, stdout)
	if document["error"] != "You've sent too many messages this hour." || document["answer"] != "Partial" {
		t.Errorf("document = %v, want the error and the partial answer", document)
	}
	if !strings.Contains(stderr, "too many messages") {
		t.Errorf("stderr = %q, want the error there for a human reading the job log", stderr)
	}
	if len(mock.titleCalls) != 0 {
		t.Errorf("title calls = %v, want none for a failed turn", mock.titleCalls)
	}
}

func TestChatOneShotJSON_DeprecatedStreamIsNotContinuable(t *testing.T) {
	mock := &chatSessionMock{
		createErrors: []error{client.ErrChatSessionsUnavailable},
		legacyEvents: []client.ChatStreamEvent{{Type: "content", Content: "Legacy answer."}, {Type: "complete"}},
	}
	stdout, _, err := runChatStructured(t, mock, "chat", "--mode", "agent", "-o", "json", "hello")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	document := decodeChatResult(t, stdout)
	if document["answer"] != "Legacy answer." || document["continuable"] != false || document["mode"] != "agent" {
		t.Errorf("document = %v, want the answer, not continuable, the requested mode", document)
	}
	if _, hasSession := document["session_id"]; hasSession {
		t.Errorf("session_id = %v, want it omitted on the deprecated stream", document["session_id"])
	}
}

func TestChatOneShotYAML(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "All good."), endFrame()}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "yaml", "hello")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if document["answer"] != "All good." || document["conversation_id"] != mock.created[0].ConversationID {
		t.Errorf("document = %v", document)
	}
}

func TestChat_OutputNeedsAOneShotQuestion(t *testing.T) {
	mock := &chatSessionMock{}
	_, _, err := runChatStructured(t, mock, "chat", "-o", "json")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("err = %v (exit %d), want a usage error", err, exitCodeFor(err))
	}
	if len(mock.created) != 0 {
		t.Error("no session may be opened for a rejected invocation")
	}
}

func TestChat_RejectsAnUnknownOutputFormat(t *testing.T) {
	mock := &chatSessionMock{}
	_, _, err := runChatStructured(t, mock, "chat", "-o", "table", "hello")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if len(mock.created) != 0 {
		t.Error("no session may be opened for a rejected invocation")
	}
}

func TestChatOneShotJSON_FoldsAnIdlessToolStartAndResult(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "tool_start", Data: map[string]any{"tool_name": "get_nodes"}, Sequence: 2},
		{Type: "tool_result", Data: map[string]any{"tool_name": "get_nodes", "success": true}, Sequence: 3},
		{Type: "tool_start", Data: map[string]any{"tool_name": "get_nodes"}, Sequence: 4},
		{Type: "tool_result", Data: map[string]any{"tool_name": "get_nodes", "success": false, "error": "timed out"}, Sequence: 5},
		contentFrame(6, "ok"),
		endFrame(),
	}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	toolCalls, _ := decodeChatResult(t, stdout)["tool_calls"].([]any)
	if len(toolCalls) != 2 {
		t.Fatalf("tool_calls = %v, want the two calls, each start folded with its result", toolCalls)
	}
	first, _ := toolCalls[0].(map[string]any)
	second, _ := toolCalls[1].(map[string]any)
	if first["success"] != true || second["success"] != false || second["error"] != "timed out" {
		t.Errorf("tool_calls = %v, want the first succeeded and the second failed", toolCalls)
	}
}

func TestChatOneShotJSON_RefusedTurnPrintsNoDocument(t *testing.T) {
	mock := &chatSessionMock{createErrors: []error{client.ErrUnauthorized}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err == nil || exitCodeFor(err) != exitAuth {
		t.Fatalf("err = %v (exit %d), want the auth failure's exit code", err, exitCodeFor(err))
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing when no turn started", stdout)
	}
}

func TestChatOneShotJSON_HiddenRuneNoticeUsesTheCommandsErrorStream(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{contentFrame(2, "ok"), endFrame()}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "-o", "json", "hel\u200blo")
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	decodeChatResult(t, stdout)
	if !strings.Contains(stderr, "invisible character") {
		t.Errorf("stderr = %q, want the stripped-runes notice on the command's error stream", stderr)
	}
}

// A turn that proposes a write parks the session as awaiting_user: the
// platform sends session_complete with that status and never "end", because
// the session is not over. The one-shot must end the turn there and print
// the document, not wait for an "end" that never comes.
func parkedProposalTail() []client.ChatStreamEvent {
	return []client.ChatStreamEvent{
		contentFrame(2, "I can restart it."),
		{Type: "tool_start", Data: map[string]any{"tool_name": "restart_deployment", "tool_call_id": "toolu_w"}, Sequence: 3},
		{Type: "tool_result", Data: map[string]any{"tool_name": "restart_deployment", "tool_call_id": "toolu_w",
			"success": true, "status": "pending_confirmation"}, Sequence: 4},
		{Type: "action_proposal", Data: map[string]any{"action_id": "act-9", "tool_name": "restart_deployment",
			"description": "Restart payments", "risk_level": "medium", "reversible": true,
			"parameters": map[string]any{"namespace": "shop", "name": "payments"}}, Sequence: 5},
		{Type: "session_complete", Data: map[string]any{"status": "awaiting_user"}, Sequence: 6},
	}
}

func TestChatOneShotJSON_AParkedTurnEndsWithoutAnEndFrame(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{parkedProposalTail()}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--mode", "agent", "-o", "json", "restart payments")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	if len(mock.tailSince) != 1 {
		t.Errorf("tails opened = %v, want one: a parked turn is over and must not be resumed", mock.tailSince)
	}
	document := decodeChatResult(t, stdout)
	toolCalls, _ := document["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %v, want the proposed write", document["tool_calls"])
	}
	call, _ := toolCalls[0].(map[string]any)
	if call["success"] != nil || call["status"] != "pending_confirmation" {
		t.Errorf("tool call = %v, want success null and status pending_confirmation: the write has not run", call)
	}
	actions, _ := document["pending_actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("pending_actions = %v, want the proposal", document["pending_actions"])
	}
	action, _ := actions[0].(map[string]any)
	parameters, _ := action["parameters"].(map[string]any)
	if action["action_id"] != "act-9" || parameters["name"] != "payments" {
		t.Errorf("pending action = %v, want act-9 with its parameters", action)
	}
}

func TestChatOneShotYAML_PendingActionsUseTheSameKeysAsJSON(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{parkedProposalTail()}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "--mode", "agent", "-o", "yaml", "restart payments")
	if err != nil {
		t.Fatalf("chat failed: %v\nstderr: %s", err, stderr)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	actions, _ := document["pending_actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("pending_actions = %v\n%s", document["pending_actions"], stdout)
	}
	action, _ := actions[0].(map[string]any)
	parameters, _ := action["parameters"].(map[string]any)
	if action["action_id"] != "act-9" || action["risk_level"] != "medium" || parameters["namespace"] != "shop" {
		t.Errorf("pending action = %v, want action_id/risk_level keys and parameters as a map\n%s", action, stdout)
	}
}

func TestChatOneShotJSON_ACancelledTurnExitsNonZero(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		contentFrame(2, "Looking at"),
		{Type: "session_complete", Data: map[string]any{"status": "cancelled"}, Sequence: 3},
		{Type: "end", Data: map[string]any{"status": "cancelled"}, Done: true},
	}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want a cancelled turn to exit non-zero", err)
	}
	document := decodeChatResult(t, stdout)
	if document["answer"] != "Looking at" || !strings.Contains(fmt.Sprint(document["error"]), "cancelled") {
		t.Errorf("document = %v, want the partial answer and the cancellation as its error", document)
	}
}
