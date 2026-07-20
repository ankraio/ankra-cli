package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type scalewayLifecycleMock struct {
	baseMock
	startClusterID string
	startScope     string
	stopClusterID  string
}

func (mock *scalewayLifecycleMock) StopScalewayCluster(clusterID string) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (mock *scalewayLifecycleMock) StartScalewayCluster(clusterID, scope string) (*client.ProviderStartClusterResult, error) {
	mock.startClusterID = clusterID
	mock.startScope = scope
	return &client.ProviderStartClusterResult{Scope: scope, CreatedOperations: 2}, nil
}

func TestScalewayStopCommand(t *testing.T) {
	mock := &scalewayLifecycleMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "stop", "cluster-123")
	})

	if mock.stopClusterID != "cluster-123" {
		t.Fatalf("cluster id = %q, want cluster-123", mock.stopClusterID)
	}
	if !strings.Contains(output, "Scaleway cluster stop initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestScalewayStartCommandWithScope(t *testing.T) {
	mock := &scalewayLifecycleMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "start", "cluster-123", "--scope", "control_plane")
	})

	if mock.startClusterID != "cluster-123" {
		t.Fatalf("cluster id = %q, want cluster-123", mock.startClusterID)
	}
	if mock.startScope != "control_plane" {
		t.Fatalf("scope = %q, want control_plane", mock.startScope)
	}
	if !strings.Contains(output, "Scaleway cluster start initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}
