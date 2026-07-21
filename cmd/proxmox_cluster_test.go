package cmd

import (
	"testing"

	"ankra/internal/client"
)

type proxmoxCreateMock struct {
	baseMock
	called     bool
	gotRequest client.CreateProxmoxClusterRequest
}

func (m *proxmoxCreateMock) CreateProxmoxCluster(request client.CreateProxmoxClusterRequest) (*client.CreateProxmoxClusterResponse, error) {
	m.called = true
	m.gotRequest = request
	return &client.CreateProxmoxClusterResponse{ClusterID: "pve-cluster-123", Name: request.Name}, nil
}

type proxmoxSizesMock struct {
	baseMock
	called bool
}

func (m *proxmoxSizesMock) ListProxmoxSizes() ([]client.ProxmoxSize, error) {
	m.called = true
	return []client.ProxmoxSize{
		{Slug: "px-small", VCPUs: 2, MemoryMB: 2048, DiskGB: 20, Available: true},
		{Slug: "px-medium", VCPUs: 4, MemoryMB: 4096, DiskGB: 40, Available: true},
	}, nil
}

type proxmoxHostsMock struct {
	baseMock
	called          bool
	gotCredentialID string
}

func (m *proxmoxHostsMock) ListProxmoxNodes(credentialID string) ([]client.ProxmoxNode, error) {
	m.called = true
	m.gotCredentialID = credentialID
	return []client.ProxmoxNode{{Node: "pve1", Status: "online", CPUCount: 16, MemoryBytes: 64 << 30}}, nil
}

func TestProxmoxCreate_MapsFlagsToRequest(t *testing.T) {
	mock := &proxmoxCreateMock{}
	out, runError := runWithInput(t, mock, "",
		"cluster", "proxmox", "create",
		"--name", "pve-test",
		"--credential-id", "cred-1",
		"--ssh-key-credential-id", "ssh-1",
		"--node", "pve1",
		"--bridge", "vmbr0",
		"--worker-count", "2",
		"--worker-instance-type", "px-large",
	)
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected CreateProxmoxCluster call")
	}
	request := mock.gotRequest
	if request.Name != "pve-test" || request.CredentialID != "cred-1" || request.SSHKeyCredentialID != "ssh-1" {
		t.Errorf("identity fields = %q/%q/%q, want pve-test/cred-1/ssh-1",
			request.Name, request.CredentialID, request.SSHKeyCredentialID)
	}
	if request.Node != "pve1" || request.Bridge != "vmbr0" {
		t.Errorf("placement fields = node %q, bridge %q, want pve1/vmbr0", request.Node, request.Bridge)
	}
	if request.WorkerCount != 2 || request.WorkerInstanceType != "px-large" {
		t.Errorf("worker fields = %d/%q, want 2/px-large", request.WorkerCount, request.WorkerInstanceType)
	}
	if request.ControlPlaneCount != 1 || request.ControlPlaneInstanceType != "px-medium" {
		t.Errorf("control plane defaults = %d/%q, want 1/px-medium",
			request.ControlPlaneCount, request.ControlPlaneInstanceType)
	}
	if request.BastionInstanceType != "px-small" {
		t.Errorf("bastion instance type = %q, want px-small", request.BastionInstanceType)
	}
	if request.Distribution != "k3s" {
		t.Errorf("distribution = %q, want k3s", request.Distribution)
	}
	if request.Storage != "" {
		t.Errorf("storage = %q, want empty (backend default local-lvm applies)", request.Storage)
	}
	if !request.IncludeNetworking {
		t.Error("include_networking should default to true")
	}
	if request.Description != nil || request.KubernetesVersion != nil {
		t.Errorf("optional fields should be omitted when unset, got description=%v kubernetes_version=%v",
			request.Description, request.KubernetesVersion)
	}
}

func TestProxmoxCreate_IncludeNetworkingCanBeDisabled(t *testing.T) {
	mock := &proxmoxCreateMock{}
	out, runError := runWithInput(t, mock, "",
		"cluster", "proxmox", "create",
		"--name", "pve-test",
		"--credential-id", "cred-1",
		"--ssh-key-credential-id", "ssh-1",
		"--node", "pve1",
		"--bridge", "vmbr0",
		"--include-networking=false",
	)
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if mock.gotRequest.IncludeNetworking {
		t.Error("include_networking should be false when disabled")
	}
}

func TestProxmoxCreate_HasNoExternalCloudProviderFlag(t *testing.T) {
	if proxmoxCreateCmd.Flags().Lookup("external-cloud-provider") != nil {
		t.Error("proxmox create must not expose --external-cloud-provider (the backend refuses it)")
	}
}

func TestProxmoxSizes_ListsPresets(t *testing.T) {
	mock := &proxmoxSizesMock{}
	out, runError := runWithInput(t, mock, "", "cluster", "proxmox", "sizes")
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected ListProxmoxSizes call")
	}
}

func TestProxmoxHosts_PassesCredentialID(t *testing.T) {
	mock := &proxmoxHostsMock{}
	out, runError := runWithInput(t, mock, "", "cluster", "proxmox", "hosts", "--credential-id", "cred-1")
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected ListProxmoxNodes call")
	}
	if mock.gotCredentialID != "cred-1" {
		t.Errorf("credential id = %q, want cred-1", mock.gotCredentialID)
	}
}
