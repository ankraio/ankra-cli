package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type scalewayLifecycleMock struct {
	baseMock
	startClusterID string
	startScope     string
	stopClusterID  string
}

func (mock *scalewayLifecycleMock) StopScalewayCluster(clusterID string, options client.StopClusterOptions) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (mock *scalewayLifecycleMock) StartScalewayCluster(clusterID string, options client.StartClusterOptions) (*client.ProviderStartClusterResult, error) {
	mock.startClusterID = clusterID
	mock.startScope = options.Scope
	return &client.ProviderStartClusterResult{Scope: options.Scope, CreatedOperations: 2}, nil
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

type scalewayDeprovisionForceMock struct {
	baseMock
	force bool
	calls int
}

func (m *scalewayDeprovisionForceMock) DeprovisionScalewayCluster(_ string, options client.DeprovisionOptions) (*client.ProviderDeprovisionClusterResponse, error) {
	m.calls++
	m.force = options.Force
	return &client.ProviderDeprovisionClusterResponse{ClusterID: testClusterID}, nil
}

// The platform honours force on a Scaleway teardown, but this subcommand had
// no --force to send it with (ankra-v3qdto.6).
func TestScalewayDeprovisionSendsForce(t *testing.T) {
	for _, forced := range []bool{true, false} {
		mock := &scalewayDeprovisionForceMock{}
		forceArgument := "--force=false"
		if forced {
			forceArgument = "--force=true"
		}
		if output, runError := runConfirmCommand(t, mock, "", []*cobra.Command{scalewayDeprovisionCmd},
			"cluster", "scaleway", "deprovision", testClusterID, "--yes", forceArgument); runError != nil {
			t.Fatalf("deprovision %s: %v\n%s", forceArgument, runError, output)
		}
		if mock.calls != 1 || mock.force != forced {
			t.Fatalf("deprovision %s: calls %d, force %v", forceArgument, mock.calls, mock.force)
		}
	}
}
