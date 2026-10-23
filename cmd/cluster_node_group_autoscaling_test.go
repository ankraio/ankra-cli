package cmd

// Tests for `ankra cluster node-group autoscaling get|set`: provider
// detection through GetClusterByID, the request payload, and both the
// enabled and disabled rendering paths.

import (
	"context"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type autoscalingMock struct {
	baseMock

	clusterKind string
	getResult   *client.NodeGroupAutoscalingResult

	setCalls []client.NodeGroupAutoscalingRequest
}

func (m *autoscalingMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Kind: m.clusterKind}, nil
}

func (m *autoscalingMock) GetHetznerNodeGroupAutoscaling(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error) {
	return m.getResult, nil
}

func (m *autoscalingMock) GetOvhNodeGroupAutoscaling(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error) {
	return m.getResult, nil
}

func (m *autoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
	}, false, nil
}

func TestClusterNodeGroupAutoscalingGetCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{
		clusterKind: "hetzner",
		getResult:   &client.NodeGroupAutoscalingResult{GroupName: "default", Enabled: true, MinCount: 1, MaxCount: 5},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "get", testClusterID, "default")
	})

	if !strings.Contains(stdoutOutput, "autoscaling: enabled") {
		t.Errorf("expected enabled state in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Min: 1") || !strings.Contains(stdoutOutput, "Max: 5") {
		t.Errorf("expected bounds in output, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingSetCommandSendsBounds(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner"}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default",
			"--enabled", "--min", "2", "--max", "6", "--wait")
	})

	if len(mock.setCalls) != 1 {
		t.Fatalf("expected one autoscaling update call, got %d", len(mock.setCalls))
	}
	sentRequest := mock.setCalls[0]
	if !sentRequest.Enabled || sentRequest.MinCount != 2 || sentRequest.MaxCount != 6 {
		t.Fatalf("request drifted: %+v", sentRequest)
	}
	if !strings.Contains(stdoutOutput, "autoscaling enabled (min 2, max 6)") {
		t.Errorf("expected enable confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingSetCommandDisables(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner"}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default",
			"--enabled=false", "--min", "1", "--max", "5", "--wait")
	})

	if len(mock.setCalls) != 1 || mock.setCalls[0].Enabled {
		t.Fatalf("expected a disable call, got %+v", mock.setCalls)
	}
	if !strings.Contains(stdoutOutput, "autoscaling disabled") {
		t.Errorf("expected disable confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingRefusesUnsupportedKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "imported"}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "node-group", "autoscaling", "get", testClusterID, "default")
	if commandError == nil || !strings.Contains(commandError.Error(), "does not support node groups") {
		t.Fatalf("expected the unsupported-kind refusal, got %v", commandError)
	}
}

// scalingAutoscalingMock answers an autoscaling write that moves the group
// to its new bounds, and serves the operation doing it.
type scalingAutoscalingMock struct {
	autoscalingMock

	submitted       bool
	operationStatus string
	operationReads  int
}

func (m *scalingAutoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	nodeCount, targetCount, operationID := 5, 3, "6f1c2d3e-0000-4000-8000-000000000042"
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
		NodeCount: &nodeCount, TargetCount: &targetCount, OperationID: &operationID,
		Message: "Node group 'ci-large' will scale down from 5 to 3 nodes as running pipeline steps finish.",
	}, m.submitted, nil
}

func (m *scalingAutoscalingMock) GetExecution(executionID string) (client.ExecutionDetail, error) {
	m.operationReads++
	status := "running"
	if m.operationReads > 1 {
		status = m.operationStatus
	}
	excerpt := "drain refused by a PodDisruptionBudget"
	return client.ExecutionDetail{Execution: client.ExecutionSummary{ID: executionID, Status: status, ErrorExcerpt: &excerpt}}, nil
}

func fastNodeGroupOperationPolling(t *testing.T) {
	t.Helper()
	previousInterval := nodeGroupOperationPollInterval
	nodeGroupOperationPollInterval = time.Millisecond
	t.Cleanup(func() { nodeGroupOperationPollInterval = previousInterval })
}

// The 2026-10-22 ci-large request: a --max below the group's node count says
// the group will scale down, names the operation, and --wait waits for it.
func TestClusterNodeGroupAutoscalingSetSaysTheGroupScalesDownAndWaitsForTheOperation(t *testing.T) {
	writeSelectedClusterJSON(t)
	fastNodeGroupOperationPolling(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, operationStatus: "success"}
	setMockClient(t, mock)

	var commandError error
	stdoutOutput := captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if commandError != nil {
		t.Fatalf("set failed: %v", commandError)
	}
	for _, expected := range []string{
		"autoscaling enabled (min 1, max 3)",
		"will scale down from 5 to 3 nodes as running pipeline steps finish",
		"Operation: 6f1c2d3e-0000-4000-8000-000000000042",
		"Operation 6f1c2d3e-0000-4000-8000-000000000042 finished: success.",
	} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in output, got: %s", expected, stdoutOutput)
		}
	}
	if mock.operationReads < 2 {
		t.Fatalf("--wait must poll the operation until it ends, read it %d times", mock.operationReads)
	}
}

func TestClusterNodeGroupAutoscalingSetWaitFailsWhenTheOperationFails(t *testing.T) {
	writeSelectedClusterJSON(t)
	fastNodeGroupOperationPolling(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, operationStatus: "failed"}
	setMockClient(t, mock)

	var commandError error
	captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if commandError == nil || !strings.Contains(commandError.Error(), "ended failed: drain refused by a PodDisruptionBudget") {
		t.Fatalf("a failed operation must fail the command with its reason, got %v", commandError)
	}
}

// Without --wait, a platform that finished the write inside its verdict
// window still says what happens, rather than only "submitted".
func TestClusterNodeGroupAutoscalingSetSubmittedSaysWhatHappens(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, submitted: true}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait=false")
	})

	if !strings.Contains(stdoutOutput, "will scale down from 5 to 3 nodes") ||
		!strings.Contains(stdoutOutput, "Operation: 6f1c2d3e-0000-4000-8000-000000000042") {
		t.Errorf("expected the outcome and the operation, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Re-run the same command with --wait") {
		t.Errorf("an answer that says what happens must not read as a bare submission, got: %s", stdoutOutput)
	}
	if mock.operationReads != 0 {
		t.Fatalf("without --wait the operation must not be polled, read it %d times", mock.operationReads)
	}
}

// inBoundsAutoscalingMock answers a write that leaves the node count alone,
// with the platform's message about it.
type inBoundsAutoscalingMock struct {
	autoscalingMock
}

func (m *inBoundsAutoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	nodeCount := 2
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
		NodeCount: &nodeCount, TargetCount: &nodeCount,
		Message: "Node group 'ci-large' autoscaling enabled (min 1, max 3); its node count of 2 is within the bounds.",
	}, false, nil
}

// The platform's message is printed whenever it sends one, not only when the
// node count moves.
func TestClusterNodeGroupAutoscalingSetPrintsThePlatformMessageWhenNothingMoves(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &inBoundsAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if !strings.Contains(stdoutOutput, "its node count of 2 is within the bounds") {
		t.Errorf("expected the platform's message, got: %s", stdoutOutput)
	}
}
