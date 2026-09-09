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

func (mock *scalewayLifecycleMock) StopScalewayCluster(clusterID string, force bool) (*client.ProviderStopClusterResponse, error) {
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
		_, _ = executeCommand("cluster", "scaleway", "stop", testClusterID)
	})

	if mock.stopClusterID != testClusterID {
		t.Fatalf("cluster id = %q, want %q", mock.stopClusterID, testClusterID)
	}
	if !strings.Contains(output, "Scaleway cluster stop initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestScalewayStartCommandWithScope(t *testing.T) {
	mock := &scalewayLifecycleMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "start", testClusterID, "--scope", "control_plane")
	})

	if mock.startClusterID != testClusterID {
		t.Fatalf("cluster id = %q, want %q", mock.startClusterID, testClusterID)
	}
	if mock.startScope != "control_plane" {
		t.Fatalf("scope = %q, want control_plane", mock.startScope)
	}
	if !strings.Contains(output, "Scaleway cluster start initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}
