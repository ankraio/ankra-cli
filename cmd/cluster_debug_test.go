package cmd

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type debugPodMock struct {
	baseMock
	createRequest  *client.CreateDebugPodRequest
	createResponse *client.DebugPodResponse
	createError    error
	listed         []client.DebugPodSummary
	deleted        []string
	session        *client.TerminalSession
	transcript     []client.TerminalTranscriptChunk
}

func (m *debugPodMock) ListDebugPodImages(clusterID string) (*client.DebugPodImagesResponse, error) {
	return &client.DebugPodImagesResponse{Images: []client.DebugPodImage{
		{Image: "docker.io/nicolaka/netshoot:v0.13", Name: "netshoot", Description: "Network toolkit.", IsDefault: true},
		{Image: "docker.io/library/busybox:1.37", Name: "busybox", Description: "Minimal shell."},
	}}, nil
}

func (m *debugPodMock) CreateDebugPod(clusterID string, request client.CreateDebugPodRequest) (*client.DebugPodResponse, error) {
	m.createRequest = &request
	if m.createError != nil {
		return nil, m.createError
	}
	return m.createResponse, nil
}

func (m *debugPodMock) ListDebugPods(clusterID string, namespace string) (*client.ListDebugPodsResponse, error) {
	return &client.ListDebugPodsResponse{DebugPods: m.listed}, nil
}

func (m *debugPodMock) DeleteDebugPod(clusterID string, namespace string, podName string) (*client.DeleteDebugPodResponse, error) {
	m.deleted = append(m.deleted, namespace+"/"+podName)
	return &client.DeleteDebugPodResponse{Status: "success"}, nil
}

func (m *debugPodMock) GetTerminalSession(sessionID string) (*client.TerminalSession, error) {
	return m.session, nil
}

func (m *debugPodMock) GetTerminalTranscript(sessionID string, afterSequence int, limit int) (*client.TerminalTranscriptPage, error) {
	page := &client.TerminalTranscriptPage{SessionID: sessionID}
	for _, chunk := range m.transcript {
		if chunk.Sequence > afterSequence && len(page.Chunks) < 2 {
			page.Chunks = append(page.Chunks, chunk)
			page.NextAfter = chunk.Sequence
		}
	}
	page.HasMore = len(page.Chunks) == 2 && page.NextAfter < m.transcript[len(m.transcript)-1].Sequence
	return page, nil
}

func debugStringPointer(value string) *string { return &value }

// Cobra flags are package-level state, so a value set by one invocation
// would leak into the next; every test resets the debug flags on cleanup,
// and a test that invokes the command more than once resets in between.
func resetDebugFlagsNow() {
	for name, value := range map[string]string{
		"namespace": "", "from-pod": "", "container": "", "image": "",
		"no-mounts": "false", "no-env": "false", "ttl": debugPodDefaultTTL.String(),
	} {
		_ = clusterDebugCreateCmd.Flags().Set(name, value)
	}
	_ = clusterDebugListCmd.Flags().Set("namespace", "")
	_ = clusterDebugDeleteCmd.Flags().Set("namespace", "")
	_ = clusterDebugDeleteCmd.Flags().Set("yes", "false")
}

func resetDebugCreateFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(resetDebugFlagsNow)
}

func createdDebugPod() *client.DebugPodResponse {
	return &client.DebugPodResponse{
		PodName:                      "debug-api-7f3a",
		Namespace:                    "payments",
		ContainerName:                "debug",
		Image:                        "docker.io/nicolaka/netshoot:v0.13",
		NodeName:                     "worker-2",
		Phase:                        "Running",
		Ready:                        true,
		ExpiresAt:                    "2026-08-28T23:00:00Z",
		TargetPodName:                debugStringPointer("api-6d8f9c7b5-x2kq9"),
		TargetContainerName:          debugStringPointer("api"),
		MirroredVolumes:              []string{"data"},
		MirroredVolumeMounts:         []string{"/var/lib/api"},
		MirroredEnvironmentVariables: 2,
		MirroredEnvironmentSources:   1,
		Warnings:                     []string{"volume scratch is an emptyDir: the debug pod gets a fresh, empty one, not the target's contents"},
		RequestedBy:                  "ops@example.com",
	}
}

func TestClusterDebugCreateImpersonatesAPod(t *testing.T) {
	mock := &debugPodMock{createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "debug", "create", "-n", "payments",
		"--from-pod", "api-6d8f9c7b5-x2kq9", "--container", "api", "--no-env", "--ttl", "30m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.createRequest == nil {
		t.Fatal("the API was never called")
	}
	request := *mock.createRequest
	if request.Namespace != "payments" || request.TargetPodName != "api-6d8f9c7b5-x2kq9" || request.TargetContainerName != "api" {
		t.Errorf("target not carried: %+v", request)
	}
	if !request.MirrorVolumeMounts || request.MirrorEnvironment {
		t.Errorf("--no-env turns environment mirroring off and leaves mounts on: %+v", request)
	}
	if request.TTLSeconds != 1800 {
		t.Errorf("--ttl 30m is 1800 seconds: %d", request.TTLSeconds)
	}
	plain := stripANSICodes(output)
	for _, expected := range []string{"debug-api-7f3a", "running", "Mirrors:   api-6d8f9c7b5-x2kq9 (container api)", "1 volume mount(s), 2 env var(s), 1 envFrom source(s)", "emptyDir", "activeTab=terminal"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("output lacks %q:\n%s", expected, plain)
		}
	}
}

func TestClusterDebugCreateRefusesMirrorFlagsWithoutATarget(t *testing.T) {
	mock := &debugPodMock{createResponse: createdDebugPod()}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "debug", "create", "-n", "payments", "--no-env")
	if exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if mock.createRequest != nil {
		t.Fatal("a usage error must not reach the API")
	}
	resetDebugFlagsNow()
	_, err = executeCommand("cluster", "debug", "create", "--from-pod", "api")
	if exitCodeFor(err) != exitUsage {
		t.Fatalf("a missing namespace is a usage error, got %v", err)
	}
	resetDebugFlagsNow()
	_, err = executeCommand("cluster", "debug", "create", "-n", "payments", "--ttl", "9h")
	if exitCodeFor(err) != exitUsage {
		t.Fatalf("a ttl over eight hours is a usage error, got %v", err)
	}
}

func TestClusterDebugCreatePropagatesTheRefusal(t *testing.T) {
	mock := &debugPodMock{createError: &client.ClusterUnavailableError{ErrorCode: "AGENT_TIMEOUT", Detail: "Agent timeout while creating the debug pod"}}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	_, err := executeCommand("cluster", "debug", "create", "-n", "payments")
	var unavailable *client.ClusterUnavailableError
	if !errors.As(err, &unavailable) || unavailable.ErrorCode != "AGENT_TIMEOUT" {
		t.Fatalf("the cluster error must surface typed, with its code: %v", err)
	}
}

func TestClusterDebugListAndImages(t *testing.T) {
	mock := &debugPodMock{listed: []client.DebugPodSummary{{
		PodName: "debug-api-7f3a", Namespace: "payments", Phase: "Running", Image: "busybox:1.37",
		TargetPodName: debugStringPointer("api-6d8f9c7b5-x2kq9"), RequestedBy: "ops@example.com",
		ExpiresAt: "2026-08-28T23:00:00Z", IsExpired: true,
	}}}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "debug", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plain := stripANSICodes(output)
	if !strings.Contains(plain, "debug-api-7f3a") || !strings.Contains(plain, "api-6d8f9c7b5-x2kq9") || !strings.Contains(plain, "(expired)") {
		t.Errorf("listing lacks the debug pod facts:\n%s", plain)
	}

	output, err = executeCommand("cluster", "debug", "images", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, `"is_default": true`) || !strings.Contains(output, "netshoot") {
		t.Errorf("json catalogue lacks the default: %s", output)
	}
}

func TestClusterDebugDeleteConfirmsFirst(t *testing.T) {
	mock := &debugPodMock{}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)
	writeSelectedClusterJSON(t)

	output, err := executeCommand("cluster", "debug", "delete", "debug-api-7f3a", "-n", "payments", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.deleted) != 1 || mock.deleted[0] != "payments/debug-api-7f3a" {
		t.Errorf("delete not issued: %v", mock.deleted)
	}
	if !strings.Contains(stripANSICodes(output), "Deleted debug pod debug-api-7f3a") {
		t.Errorf("output = %s", output)
	}
}

func TestOrgTerminalSessionPrintsTheRecording(t *testing.T) {
	endedAt := "2026-08-28T22:01:30Z"
	endReason := "agent_ended"
	mock := &debugPodMock{
		session: &client.TerminalSession{
			ID: "11111111-2222-4333-8444-555555555555", UserEmail: "ops@example.com",
			Namespace: "payments", PodName: "debug-api-7f3a", ContainerName: "debug", Shell: "/bin/sh",
			StartedAt: "2026-08-28T22:00:00Z", EndedAt: &endedAt, EndReason: &endReason,
			RecordedBytes: 42, IsTruncated: true,
		},
		transcript: []client.TerminalTranscriptChunk{
			{Sequence: 1, Direction: "input", Data: base64.StdEncoding.EncodeToString([]byte("env\x7f\n"))},
			{Sequence: 2, Direction: "output", Data: base64.StdEncoding.EncodeToString([]byte("DATABASE_URL=postgres://x\r\n"))},
			{Sequence: 3, Direction: "input", Data: base64.StdEncoding.EncodeToString([]byte("exit\n"))},
		},
	}
	setMockClient(t, mock)
	resetDebugCreateFlags(t)

	output, err := executeCommand("org", "terminal-session", "11111111-2222-4333-8444-555555555555", "--transcript", "--show-input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{"ops@example.com", "payments/debug-api-7f3a (debug), shell /bin/sh", "(agent_ended)", "42 bytes [truncated]", "DATABASE_URL=postgres://x", "env⌫⏎\nexit⏎"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
}
