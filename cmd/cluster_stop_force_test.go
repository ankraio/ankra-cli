package cmd

import (
	"testing"

	"ankra/internal/client"
)

// The force stop contract (backend cluster#1832): stop --force cancels
// every in-flight operation and blocks new operations for 60 seconds, so
// the flag must reach the client exactly as typed - and default to false.

type proxmoxStopMock struct {
	baseMock
	stopClusterID string
	gotForce      bool
}

func (mock *proxmoxStopMock) StopProxmoxCluster(clusterID string, force bool) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	mock.gotForce = force
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

type morpheusStopMock struct {
	baseMock
	stopClusterID string
	gotForce      bool
}

func (mock *morpheusStopMock) StopMorpheusCluster(clusterID string, force bool) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	mock.gotForce = force
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func TestProxmoxStopForceReachesTheClient(t *testing.T) {
	mock := &proxmoxStopMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = proxmoxStopCmd.Flags().Set("force", "false") })

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "proxmox", "stop", testClusterID, "--force")
	})
	if mock.stopClusterID != testClusterID {
		t.Fatalf("cluster id = %q, want %q", mock.stopClusterID, testClusterID)
	}
	if !mock.gotForce {
		t.Fatal("expected --force to reach the API")
	}
}

func TestProxmoxStopDefaultsToUnforced(t *testing.T) {
	mock := &proxmoxStopMock{}
	setMockClient(t, mock)

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "proxmox", "stop", testClusterID)
	})
	if mock.gotForce {
		t.Fatal("a plain stop must not send force")
	}
}

func TestMorpheusStopForceReachesTheClient(t *testing.T) {
	mock := &morpheusStopMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = morpheusStopCmd.Flags().Set("force", "false") })

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "morpheus", "stop", testClusterID, "--force")
	})
	if mock.stopClusterID != testClusterID {
		t.Fatalf("cluster id = %q, want %q", mock.stopClusterID, testClusterID)
	}
	if !mock.gotForce {
		t.Fatal("expected --force to reach the API")
	}
}
