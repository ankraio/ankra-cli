package cmd

import (
	"context"
	"strings"
	"testing"

	"ankra/internal/client"
)

type ovhStopMock struct {
	baseMock
	gotClusterID string
}

func (m *ovhStopMock) StopOvhCluster(clusterID string) (*client.StopOvhClusterResponse, error) {
	m.gotClusterID = clusterID
	return &client.StopOvhClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func TestOvhStopCommand(t *testing.T) {
	mock := &ovhStopMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "stop", "ovh-123")
	})

	if mock.gotClusterID != "ovh-123" {
		t.Errorf("cluster id = %q, want ovh-123", mock.gotClusterID)
	}
	if !strings.Contains(output, "stop initiated") {
		t.Errorf("expected 'stop initiated', got: %s", output)
	}
	if !strings.Contains(output, "ovh-123") {
		t.Errorf("expected cluster id in output, got: %s", output)
	}
}

type ovhStartMock struct {
	baseMock
	gotClusterID string
	gotScope     string
}

func (m *ovhStartMock) StartOvhCluster(clusterID, scope string) (*client.StartOvhClusterResult, error) {
	m.gotClusterID = clusterID
	m.gotScope = scope
	return &client.StartOvhClusterResult{MarkedToStartAt: "2026-01-01T00:00:00Z", Scope: scope, CreatedOperations: 1}, nil
}

func TestOvhStartCommandWithScope(t *testing.T) {
	mock := &ovhStartMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "start", "ovh-123", "--scope", "control_plane")
	})

	if mock.gotScope != "control_plane" {
		t.Errorf("scope = %q, want control_plane", mock.gotScope)
	}
	if !strings.Contains(output, "start initiated") {
		t.Errorf("expected 'start initiated', got: %s", output)
	}
	if !strings.Contains(output, "control_plane") {
		t.Errorf("expected scope in output, got: %s", output)
	}
}

type ovhAccessInfoMock struct {
	baseMock
}

func (m *ovhAccessInfoMock) GetOvhAccessInfo(clusterID string) (*client.ClusterAccessInfo, error) {
	bastion := "203.0.113.10"
	controlPlane := "10.0.1.10"
	name := "ovh-test"
	return &client.ClusterAccessInfo{
		BastionIP:       &bastion,
		ControlPlaneIP:  &controlPlane,
		ControlPlaneIPs: []string{"10.0.1.10"},
		ClusterName:     &name,
	}, nil
}

func TestOvhAccessInfoCommand(t *testing.T) {
	setMockClient(t, &ovhAccessInfoMock{})

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "access-info", "ovh-123")
	})

	if !strings.Contains(output, "203.0.113.10") {
		t.Errorf("expected gateway IP in output, got: %s", output)
	}
	if !strings.Contains(output, "ssh -J ubuntu@203.0.113.10 ubuntu@10.0.1.10") {
		t.Errorf("expected SSH jump command in output, got: %s", output)
	}
	if !strings.Contains(output, "ssh -L 6443:10.0.1.10:6443") {
		t.Errorf("expected port-forward command in output, got: %s", output)
	}
}

type ovhSSHKeysMock struct {
	baseMock
	gotSetIDs []string
}

func (m *ovhSSHKeysMock) GetOvhClusterSSHKeys(clusterID string) (*client.ClusterSSHKeysResult, error) {
	return &client.ClusterSSHKeysResult{
		SSHKeyCredentialIDs: []string{"ssh-1"},
		AvailableSSHKeys:    []client.ClusterSSHKeyEntry{{CredentialID: "ssh-1", Name: "primary"}},
	}, nil
}

func (m *ovhSSHKeysMock) UpdateOvhClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*client.UpdateClusterSSHKeysResult, error) {
	m.gotSetIDs = sshKeyCredentialIDs
	return &client.UpdateClusterSSHKeysResult{SSHKeyCredentialIDs: sshKeyCredentialIDs}, nil
}

func TestOvhSSHKeysGetCommand(t *testing.T) {
	setMockClient(t, &ovhSSHKeysMock{})

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "ssh-keys", "get", "ovh-123")
	})

	if !strings.Contains(output, "ssh-1") {
		t.Errorf("expected ssh-1 in output, got: %s", output)
	}
	if !strings.Contains(output, "primary") {
		t.Errorf("expected available key name in output, got: %s", output)
	}
}

func TestOvhSSHKeysSetCommand(t *testing.T) {
	mock := &ovhSSHKeysMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "ssh-keys", "set", "ovh-123", "--ssh-key-credential-ids", "ssh-1,ssh-2")
	})

	if len(mock.gotSetIDs) != 2 || mock.gotSetIDs[0] != "ssh-1" || mock.gotSetIDs[1] != "ssh-2" {
		t.Errorf("set ids = %v, want [ssh-1 ssh-2]", mock.gotSetIDs)
	}
	if !strings.Contains(output, "SSH keys updated") {
		t.Errorf("expected confirmation in output, got: %s", output)
	}
}

type ovhNodeGroupAddMock struct {
	baseMock
	gotRequest client.AddNodeGroupRequest
}

func (m *ovhNodeGroupAddMock) AddOvhNodeGroup(ctx context.Context, clusterID string, req client.AddNodeGroupRequest, wait bool) (*client.AddNodeGroupResult, bool, error) {
	m.gotRequest = req
	return &client.AddNodeGroupResult{GroupName: req.Name, Count: req.Count}, false, nil
}

func TestOvhNodeGroupAddWithLabelsAndTaints(t *testing.T) {
	mock := &ovhNodeGroupAddMock{}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand(
			"cluster", "ovh", "node-group", "add", "ovh-123",
			"--name", "gpu", "--instance-type", "b2-30", "--count", "2",
			"--labels", "tier=gold,team=ml",
			"--taints", "dedicated=gpu:NoSchedule",
			"--wait",
		)
	})

	if mock.gotRequest.Name != "gpu" {
		t.Errorf("name = %q, want gpu", mock.gotRequest.Name)
	}
	if mock.gotRequest.Labels["tier"] != "gold" || mock.gotRequest.Labels["team"] != "ml" {
		t.Errorf("labels = %v, want tier=gold team=ml", mock.gotRequest.Labels)
	}
	if len(mock.gotRequest.Taints) != 1 {
		t.Fatalf("taints len = %d, want 1", len(mock.gotRequest.Taints))
	}
	taint := mock.gotRequest.Taints[0]
	if taint.Key != "dedicated" || taint.Value != "gpu" || taint.Effect != "NoSchedule" {
		t.Errorf("taint = %+v, want dedicated=gpu:NoSchedule", taint)
	}
	if !strings.Contains(output, "gpu") {
		t.Errorf("expected group name in output, got: %s", output)
	}
}
