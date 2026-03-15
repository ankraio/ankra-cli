package client

import (
	"net/http"
	"testing"
)

func TestCreateOvhCluster_Success(t *testing.T) {
	expectedResponse := CreateOvhClusterResponse{ClusterID: "ovh-cluster-123", Name: "ovh-test"}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/ovh" {
			t.Errorf("path = %s, want /api/v1/clusters/ovh", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := CreateOvhClusterRequest{
		Name: "ovh-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "GRA9", NetworkVlanID: 1, SubnetCIDR: "10.0.0.0/24",
		DHCPStart: "10.0.0.10", DHCPEnd: "10.0.0.250", GatewayFlavorID: "b2-7",
		ControlPlaneCount: 1, ControlPlaneFlavorID: "b2-7",
		WorkerCount: 2, WorkerFlavorID: "b2-7", Distribution: "k3s",
	}
	result, err := testClient.CreateOvhCluster(req)
	if err != nil {
		t.Fatalf("CreateOvhCluster: %v", err)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
}

func TestCreateOvhCluster_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	}
	testClient := newTestClient(t, handler)
	req := CreateOvhClusterRequest{
		Name: "ovh-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "GRA9", NetworkVlanID: 1, SubnetCIDR: "10.0.0.0/24",
		DHCPStart: "10.0.0.10", DHCPEnd: "10.0.0.250", GatewayFlavorID: "b2-7",
		ControlPlaneCount: 1, ControlPlaneFlavorID: "b2-7",
		WorkerCount: 2, WorkerFlavorID: "b2-7", Distribution: "k3s",
	}
	_, err := testClient.CreateOvhCluster(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionOvhCluster_Success(t *testing.T) {
	expectedResponse := DeprovisionOvhClusterResponse{Success: true, ClusterID: "ovh-cluster-123"}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/ovh/ovh-cluster-123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DeprovisionOvhCluster("ovh-cluster-123")
	if err != nil {
		t.Fatalf("DeprovisionOvhCluster: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestScaleOvhWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{PreviousCount: 2, NewCount: 5}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ScaleOvhWorkers("cluster-123", 5)
	if err != nil {
		t.Fatalf("ScaleOvhWorkers: %v", err)
	}
	if result.NewCount != 5 {
		t.Errorf("NewCount = %d, want 5", result.NewCount)
	}
}

func TestListOvhNodeGroups_Success(t *testing.T) {
	expectedResponse := NodeGroupListResult{
		NodeGroups: []NodeGroupInfo{{Name: "pool1", InstanceType: "b2-7", Count: 2, Min: 1, Max: 10}},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-123/node-groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListOvhNodeGroups("cluster-123")
	if err != nil {
		t.Fatalf("ListOvhNodeGroups: %v", err)
	}
	if len(result.NodeGroups) != 1 {
		t.Fatalf("NodeGroups len = %d, want 1", len(result.NodeGroups))
	}
}
