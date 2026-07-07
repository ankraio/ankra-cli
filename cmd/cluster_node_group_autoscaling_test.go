package cmd

// Tests for `ankra cluster node-group autoscaling get|set`: provider
// detection through GetClusterByID, the request payload, and both the
// enabled and disabled rendering paths.

import (
	"context"
	"strings"
	"testing"

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
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "get", "test-cluster-id", "default")
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
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", "test-cluster-id", "default",
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
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", "test-cluster-id", "default",
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

	_, commandError := executeCommand("cluster", "node-group", "autoscaling", "get", "test-cluster-id", "default")
	if commandError == nil || !strings.Contains(commandError.Error(), "does not support node groups") {
		t.Fatalf("expected the unsupported-kind refusal, got %v", commandError)
	}
}
