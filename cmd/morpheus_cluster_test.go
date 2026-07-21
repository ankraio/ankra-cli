package cmd

import (
	"testing"

	"ankra/internal/client"
)

type morpheusCreateMock struct {
	baseMock
	called     bool
	gotRequest client.CreateMorpheusClusterRequest
}

func (m *morpheusCreateMock) CreateMorpheusCluster(request client.CreateMorpheusClusterRequest) (*client.CreateMorpheusClusterResponse, error) {
	m.called = true
	m.gotRequest = request
	return &client.CreateMorpheusClusterResponse{ClusterID: "morpheus-cluster-123", Name: request.Name}, nil
}

type morpheusGroupsMock struct {
	baseMock
	called          bool
	gotCredentialID string
}

func (m *morpheusGroupsMock) ListMorpheusGroups(credentialID string) ([]client.MorpheusGroup, error) {
	m.called = true
	m.gotCredentialID = credentialID
	return []client.MorpheusGroup{{ID: 1, Name: "default"}}, nil
}

func TestMorpheusCreate_MapsFlagsToRequest(t *testing.T) {
	mock := &morpheusCreateMock{}
	out, runError := runWithInput(t, mock, "",
		"cluster", "morpheus", "create",
		"--name", "morpheus-test",
		"--credential-id", "cred-1",
		"--ssh-key-credential-id", "ssh-1",
		"--group-id", "1",
		"--cloud-id", "2",
		"--network-id", "3",
		"--layout-id", "4",
		"--bastion-plan-id", "5",
		"--control-plane-plan-id", "6",
		"--worker-plan-id", "7",
		"--worker-count", "2",
	)
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected CreateMorpheusCluster call")
	}
	request := mock.gotRequest
	if request.Name != "morpheus-test" || request.CredentialID != "cred-1" || request.SSHKeyCredentialID != "ssh-1" {
		t.Errorf("identity fields = %q/%q/%q, want morpheus-test/cred-1/ssh-1",
			request.Name, request.CredentialID, request.SSHKeyCredentialID)
	}
	if request.GroupID != 1 || request.CloudID != 2 || request.NetworkID != 3 || request.LayoutID != 4 {
		t.Errorf("catalog ids = %d/%d/%d/%d, want 1/2/3/4",
			request.GroupID, request.CloudID, request.NetworkID, request.LayoutID)
	}
	if request.BastionPlanID != 5 || request.ControlPlanePlanID != 6 || request.WorkerPlanID != 7 {
		t.Errorf("plan ids = %d/%d/%d, want 5/6/7",
			request.BastionPlanID, request.ControlPlanePlanID, request.WorkerPlanID)
	}
	if request.ControlPlaneCount != 1 || request.WorkerCount != 2 {
		t.Errorf("counts = %d/%d, want 1/2", request.ControlPlaneCount, request.WorkerCount)
	}
	if request.Distribution != "k3s" {
		t.Errorf("distribution = %q, want k3s", request.Distribution)
	}
	if !request.IncludeNetworking {
		t.Error("include_networking should default to true")
	}
	if request.VirtualImageID != nil || request.EtcdPlanID != nil {
		t.Errorf("optional ids should be omitted when unset, got virtual_image_id=%v etcd_plan_id=%v",
			request.VirtualImageID, request.EtcdPlanID)
	}
	if request.Description != nil || request.KubernetesVersion != nil {
		t.Errorf("optional fields should be omitted when unset, got description=%v kubernetes_version=%v",
			request.Description, request.KubernetesVersion)
	}
}

func TestMorpheusCreate_SendsOptionalNumericIDs(t *testing.T) {
	mock := &morpheusCreateMock{}
	out, runError := runWithInput(t, mock, "",
		"cluster", "morpheus", "create",
		"--name", "morpheus-test",
		"--credential-id", "cred-1",
		"--ssh-key-credential-id", "ssh-1",
		"--group-id", "1",
		"--cloud-id", "2",
		"--network-id", "3",
		"--layout-id", "4",
		"--bastion-plan-id", "5",
		"--control-plane-plan-id", "6",
		"--worker-plan-id", "7",
		"--virtual-image-id", "42",
		"--etcd-plan-id", "9",
	)
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if mock.gotRequest.VirtualImageID == nil || *mock.gotRequest.VirtualImageID != 42 {
		t.Errorf("virtual_image_id = %v, want 42", mock.gotRequest.VirtualImageID)
	}
	if mock.gotRequest.EtcdPlanID == nil || *mock.gotRequest.EtcdPlanID != 9 {
		t.Errorf("etcd_plan_id = %v, want 9", mock.gotRequest.EtcdPlanID)
	}
}

func TestMorpheusCreate_HasNoExternalCloudProviderFlag(t *testing.T) {
	if morpheusCreateCmd.Flags().Lookup("external-cloud-provider") != nil {
		t.Error("morpheus create must not expose --external-cloud-provider (the backend refuses it)")
	}
}

func TestMorpheusGroups_PassesCredentialID(t *testing.T) {
	mock := &morpheusGroupsMock{}
	out, runError := runWithInput(t, mock, "", "cluster", "morpheus", "groups", "--credential-id", "cred-1")
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected ListMorpheusGroups call")
	}
	if mock.gotCredentialID != "cred-1" {
		t.Errorf("credential id = %q, want cred-1", mock.gotCredentialID)
	}
}
