package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"
)

type fakePodTerminal struct {
	frames      chan client.PodTerminalFrame
	closeError  error
	inputLock   sync.Mutex
	inputs      []string
	stdinClosed bool
}

func newFakePodTerminal(closeError error, frames ...client.PodTerminalFrame) *fakePodTerminal {
	channel := make(chan client.PodTerminalFrame, len(frames))
	for _, frame := range frames {
		channel <- frame
	}
	close(channel)
	return &fakePodTerminal{frames: channel, closeError: closeError}
}

func (f *fakePodTerminal) Frames() <-chan client.PodTerminalFrame { return f.frames }
func (f *fakePodTerminal) SendInput(data []byte) error {
	f.inputLock.Lock()
	defer f.inputLock.Unlock()
	f.inputs = append(f.inputs, string(data))
	return nil
}
func (f *fakePodTerminal) CloseStdin() error {
	f.inputLock.Lock()
	defer f.inputLock.Unlock()
	f.stdinClosed = true
	return nil
}
func (f *fakePodTerminal) isStdinClosed() bool {
	f.inputLock.Lock()
	defer f.inputLock.Unlock()
	return f.stdinClosed
}
func (f *fakePodTerminal) Resize(int, int) error { return nil }
func (f *fakePodTerminal) Ping() error           { return nil }
func (f *fakePodTerminal) Close() error          { return nil }
func (f *fakePodTerminal) Err() error            { return f.closeError }
func (f *fakePodTerminal) typed() string {
	f.inputLock.Lock()
	defer f.inputLock.Unlock()
	return strings.Join(f.inputs, "")
}

type terminalMock struct {
	baseMock
	podItems        []any
	terminal        *fakePodTerminal
	openError       error
	openRequest     *client.PodTerminalRequest
	createResponse  *client.DebugPodResponse
	session         *client.TerminalSession
	transcriptCalls int
}

func (m *terminalMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{
		{Status: "success", Kind: "Pod", Items: m.podItems},
	}}, nil
}

func (m *terminalMock) OpenPodTerminal(ctx context.Context, clusterID string, request client.PodTerminalRequest) (client.PodTerminal, error) {
	m.openRequest = &request
	if m.openError != nil {
		return nil, m.openError
	}
	return m.terminal, nil
}

func (m *terminalMock) CreateDebugPod(clusterID string, request client.CreateDebugPodRequest) (*client.DebugPodResponse, error) {
	return m.createResponse, nil
}

func (m *terminalMock) GetTerminalSession(sessionID string) (*client.TerminalSession, error) {
	return m.session, nil
}

func (m *terminalMock) GetTerminalTranscript(sessionID string, afterSequence int, limit int) (*client.TerminalTranscriptPage, error) {
	m.transcriptCalls++
	return &client.TerminalTranscriptPage{SessionID: sessionID}, nil
}

func TestOrgTerminalSessionSaysTheTranscriptWasPruned(t *testing.T) {
	prunedAt := "2026-11-27T06:00:00Z"
	mock := &terminalMock{session: &client.TerminalSession{
		ID: "11111111-2222-4333-8444-555555555555", UserEmail: "ops@example.com",
		Namespace: "payments", PodName: "api-6d8f9c7b5-x2kq9", ContainerName: "api", Shell: "/bin/sh",
		StartedAt: "2026-08-28T22:00:00Z", RecordedBytes: 4096, TranscriptPrunedAt: &prunedAt,
	}}
	setMockClient(t, mock)
	t.Cleanup(func() {
		_ = orgTerminalSessionCmd.Flags().Set("transcript", "false")
		_ = orgTerminalSessionCmd.Flags().Set("show-input", "false")
	})

	output, err := executeCommand("org", "terminal-session", "11111111-2222-4333-8444-555555555555", "--transcript", "--show-input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "transcript pruned "+prunedAt) {
		t.Errorf("the facts should flag the pruned transcript:\n%s", output)
	}
	if !strings.Contains(output, "pruned on "+prunedAt+" under the retention policy") {
		t.Errorf("the pruned notice is missing:\n%s", output)
	}
	if strings.Contains(output, "--- recorded output ---") || mock.transcriptCalls != 0 {
		t.Errorf("a pruned transcript must not be fetched or replayed (calls=%d):\n%s", mock.transcriptCalls, output)
	}
}

func stdoutFrame(text string) client.PodTerminalFrame {
	return client.PodTerminalFrame{Type: "stdout", Data: base64.StdEncoding.EncodeToString([]byte(text))}
}

func podWithContainers(names ...string) any {
	containers := make([]any, 0, len(names))
	for _, name := range names {
		containers = append(containers, map[string]any{"name": name})
	}
	return map[string]any{"kind": "Pod", "spec": map[string]any{"containers": containers}}
}

func helloTerminal() *fakePodTerminal {
	return newFakePodTerminal(nil,
		client.PodTerminalFrame{Type: "connecting"},
		client.PodTerminalFrame{Type: "connected"},
		stdoutFrame("hello from the pod\n"),
		client.PodTerminalFrame{Type: "end"})
}

func resetTerminalFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		_ = clusterTerminalCmd.Flags().Set("namespace", "")
		_ = clusterTerminalCmd.Flags().Set("container", "")
		_ = clusterTerminalCmd.Flags().Set("shell", podTerminalDefaultShell)
		rootCmd.SetIn(nil)
	}
	reset()
	t.Cleanup(reset)
}

func waitForTyped(t *testing.T, terminal *fakePodTerminal, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(terminal.typed(), expected) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("typed input never reached the session: %q", terminal.typed())
}

func TestClusterTerminalBridgesInputAndOutput(t *testing.T) {
	terminal := helloTerminal()
	mock := &terminalMock{terminal: terminal}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)
	rootCmd.SetIn(bytes.NewBufferString("id\n"))

	output, err := executeCommand("cluster", "terminal", "web-1", "-n", "default", "-c", "app", "--shell", "/bin/bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.openRequest == nil {
		t.Fatal("no terminal was opened")
	}
	request := *mock.openRequest
	if request.Namespace != "default" || request.PodName != "web-1" || request.ContainerName != "app" || request.Shell != "/bin/bash" {
		t.Errorf("request not carried: %+v", request)
	}
	if request.Cols != podTerminalDefaultCols || request.Rows != podTerminalDefaultRows {
		t.Errorf("a non-terminal output falls back to %dx%d, got %dx%d", podTerminalDefaultCols, podTerminalDefaultRows, request.Cols, request.Rows)
	}
	if !strings.Contains(output, "hello from the pod") {
		t.Errorf("remote output was not written:\n%s", output)
	}
	if !strings.Contains(output, "recorded to the audit log") {
		t.Errorf("the recording notice is missing:\n%s", output)
	}
	waitForTyped(t, terminal, "id\n")
}

func TestClusterTerminalDefaultsToTheOnlyContainer(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal(), podItems: []any{podWithContainers("app")}}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	if _, err := executeCommand("cluster", "terminal", "web-1", "-n", "default"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.openRequest == nil || mock.openRequest.ContainerName != "app" {
		t.Fatalf("the only container was not chosen: %+v", mock.openRequest)
	}
	if mock.openRequest.Shell != podTerminalDefaultShell {
		t.Errorf("shell defaults to %s, got %q", podTerminalDefaultShell, mock.openRequest.Shell)
	}
}

func TestClusterTerminalRefusesToGuessBetweenContainers(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal(), podItems: []any{podWithContainers("app", "sidecar")}}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "terminal", "web-1", "-n", "default")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "app, sidecar") || !strings.Contains(err.Error(), "--container") {
		t.Errorf("the refusal should list the containers: %v", err)
	}
	if mock.openRequest != nil {
		t.Error("a terminal was opened despite the refusal")
	}
}

func TestClusterTerminalReportsAMissingPod(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal()}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "terminal", "ghost", "-n", "default")
	if err == nil || exitCodeFor(err) != exitNotFound {
		t.Fatalf("expected the not-found exit code, got %v", err)
	}
}

func TestClusterTerminalRequiresANamespace(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal()}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "terminal", "web-1")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestClusterTerminalRefusesACommandLineAsTheShell(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal(), podItems: []any{podWithContainers("postgres")}}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "terminal", "cnpg-cluster-1", "-n", "cnpg-database", "-c", "postgres",
		"--shell", `/bin/sh -c 'psql -U postgres -d appdb -Atc "select count(*) from users"'`)
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "one executable") || !strings.Contains(err.Error(), "pipe them into the session") {
		t.Errorf("the refusal should say what --shell takes and how to run commands: %v", err)
	}
	if mock.openRequest != nil {
		t.Error("a terminal was opened (and recorded) despite the refusal")
	}
}

func TestClusterTerminalEmptyShellOpensTheDefaultShell(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal(), podItems: []any{podWithContainers("app")}}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	if _, err := executeCommand("cluster", "terminal", "web-1", "-n", "default", "--shell", ""); err != nil {
		t.Fatalf("an empty --shell should fall back to the default, got %v", err)
	}
	if mock.openRequest == nil || mock.openRequest.Shell != podTerminalDefaultShell {
		t.Fatalf("an empty --shell should open %s, got %+v", podTerminalDefaultShell, mock.openRequest)
	}
}

func TestValidatePodTerminalShell(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/busybox/sh", ""} {
		if err := validatePodTerminalShell(shell); err != nil {
			t.Errorf("%q is one executable and should pass: %v", shell, err)
		}
	}
	for _, shell := range []string{"/bin/bash -l", "/bin/sh\t-c", "bash\n", " /bin/sh"} {
		if err := validatePodTerminalShell(shell); err == nil || exitCodeFor(err) != exitUsage {
			t.Errorf("%q is not one executable and should be a usage error, got %v", shell, err)
		}
	}
}

func TestClusterTerminalSurfacesThePermissionRefusal(t *testing.T) {
	terminal := newFakePodTerminal(&client.PermissionDeniedError{Permission: "kubernetes.exec"},
		client.PodTerminalFrame{Type: "error", Message: "Permission denied"})
	mock := &terminalMock{terminal: terminal}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "terminal", "web-1", "-n", "default", "-c", "app")
	if err == nil || exitCodeFor(err) != exitForbidden {
		t.Fatalf("expected the RBAC exit code, got %v", err)
	}
	if !strings.Contains(output, "Permission denied") {
		t.Errorf("the relay's error frame was not shown:\n%s", output)
	}
}

func TestClusterTerminalRefusedTokenExitsAuth(t *testing.T) {
	terminal := newFakePodTerminal(&client.PodTerminalClosedError{Code: 4001, Message: "Authentication required"},
		client.PodTerminalFrame{Type: "error", Message: "Authentication required"})
	mock := &terminalMock{terminal: terminal}
	setMockClient(t, mock)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "terminal", "web-1", "-n", "default", "-c", "app")
	if err == nil || exitCodeFor(err) != exitAuth {
		t.Fatalf("expected the auth exit code, got %v", err)
	}
}

func TestClusterDebugCreateAttachOpensTheDebugContainer(t *testing.T) {
	terminal := helloTerminal()
	mock := &terminalMock{terminal: terminal, createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--attach", "--shell", "/bin/bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.openRequest == nil {
		t.Fatal("no terminal was opened after the create")
	}
	request := *mock.openRequest
	if request.Namespace != "payments" || request.PodName != "debug-api-7f3a" || request.ContainerName != "debug" || request.Shell != "/bin/bash" {
		t.Errorf("the debug container was not attached: %+v", request)
	}
	plain := stripANSICodes(output)
	for _, expected := range []string{"debug-api-7f3a", "hello from the pod"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("output lacks %q:\n%s", expected, plain)
		}
	}
}

func TestClusterDebugCreateAttachWaitsForARunningPod(t *testing.T) {
	notReady := createdDebugPod()
	notReady.Ready = false
	notReady.Phase = "Pending"
	mock := &terminalMock{terminal: helloTerminal(), createResponse: notReady}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--attach")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.openRequest != nil {
		t.Error("a terminal was opened on a pod that is not running")
	}
	if !strings.Contains(output, "ankra cluster terminal debug-api-7f3a -n payments -c debug") {
		t.Errorf("the hint to attach later is missing:\n%s", output)
	}
}

func TestClusterDebugCreateAttachRefusesACommandLineBeforeCreatingThePod(t *testing.T) {
	mock := &debugPodMock{createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--attach", "--shell", "/bin/bash -l")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if mock.createRequest != nil {
		t.Error("a debug pod was created for a terminal that could never open")
	}
}

func TestClusterDebugCreateWithoutAttachIgnoresTheShell(t *testing.T) {
	mock := &debugPodMock{createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	if _, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--shell", "/bin/bash -l"); err != nil {
		t.Fatalf("--shell only means something with --attach, so it should not refuse the create: %v", err)
	}
	if mock.createRequest == nil {
		t.Error("the debug pod was not created")
	}
}

func TestClusterDebugCreateAttachRefusesStructuredOutput(t *testing.T) {
	mock := &terminalMock{terminal: helloTerminal(), createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	resetTerminalFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--attach", "-o", "json")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if mock.openRequest != nil {
		t.Error("a terminal was opened despite the refusal")
	}
}
