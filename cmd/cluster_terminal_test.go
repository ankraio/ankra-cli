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
	frames     chan client.PodTerminalFrame
	closeError error
	inputLock  sync.Mutex
	inputs     []string
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
	podItems       []any
	terminal       *fakePodTerminal
	openError      error
	openRequest    *client.PodTerminalRequest
	createResponse *client.DebugPodResponse
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
