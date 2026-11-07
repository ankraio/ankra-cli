package cmd

// Tests for `ankra cluster node-group autoscaling get|set`: provider
// detection through GetClusterByID, the request payload, and both the
// enabled and disabled rendering paths.

import (
	"context"
	"encoding/json"
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

// The command used to send its own flag defaults (min 1, max 5) for bounds
// the user never set: a plain enable capped the group at 5 nodes against the
// platform's default of 100, and --enabled=false overwrote the recorded
// bounds with 1 and 5 (ankra-v3qdto.6). Only the flags the user set travel
// now.
func TestClusterNodeGroupAutoscalingSetSendsOnlyTheBoundsTheUserSet(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		args     []string
		wantBody string
	}{
		{name: "plain enable", args: []string{"--enabled"}, wantBody: `{"enabled":true}`},
		{name: "plain disable", args: []string{"--enabled=false"}, wantBody: `{"enabled":false}`},
		{name: "max only", args: []string{"--enabled", "--max", "8"}, wantBody: `{"enabled":true,"max_count":8}`},
		{name: "both bounds", args: []string{"--enabled", "--min", "2", "--max", "6"}, wantBody: `{"enabled":true,"max_count":6,"min_count":2}`},
		{name: "scale to zero", args: []string{"--enabled", "--min", "0", "--max", "4"}, wantBody: `{"enabled":true,"max_count":4,"min_count":0}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
			writeSelectedClusterJSON(t)
			mock := &autoscalingMock{clusterKind: "hetzner"}
			setMockClient(t, mock)

			captureStdout(t, func() {
				arguments := append([]string{"cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--wait"}, testCase.args...)
				if _, runError := executeCommand(arguments...); runError != nil {
					t.Fatalf("set: %v", runError)
				}
			})
			if len(mock.setCalls) != 1 {
				t.Fatalf("autoscaling update calls = %d, want 1", len(mock.setCalls))
			}
			body, marshalError := json.Marshal(mock.setCalls[0])
			if marshalError != nil {
				t.Fatalf("marshal: %v", marshalError)
			}
			if string(body) != testCase.wantBody {
				t.Errorf("body = %s, want %s", body, testCase.wantBody)
			}
		})
	}
}

// An omitted bound still carries the platform default in the request, so
// code that echoes the request (the confirmation line) reads 1 and 100, not
// 0.
func TestClusterNodeGroupAutoscalingSetEchoesThePlatformDefaults(t *testing.T) {
	resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner"}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--enabled", "--wait")
	})
	if !strings.Contains(stdoutOutput, "autoscaling enabled (min 1, max 100)") {
		t.Errorf("expected the platform defaults in the confirmation, got: %s", stdoutOutput)
	}
}
