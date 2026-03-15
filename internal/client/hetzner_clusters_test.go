package client

import (
	"net/http"
	"testing"
)

func TestCreateHetznerCluster_Success(t *testing.T) {
	expectedResponse := CreateHetznerClusterResponse{
		ClusterID: "cluster-123",
		Name:      "test-cluster",
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner" {
			t.Errorf("path = %s, want /api/v1/clusters/hetzner", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+testToken {
			t.Errorf("Authorization = %s, want Bearer %s", auth, testToken)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := CreateHetznerClusterRequest{
		Name:                   "test-cluster",
		CredentialID:           "cred-1",
		Location:               "fsn1",
		NetworkIPRange:         "10.0.0.0/16",
		SubnetRange:            "10.0.1.0/24",
		BastionServerType:      "cx11",
		ControlPlaneCount:      1,
		ControlPlaneServerType: "cx21",
		WorkerCount:            2,
		WorkerServerType:       "cx21",
		Distribution:           "k3s",
	}
	result, err := testClient.CreateHetznerCluster(req)
	if err != nil {
		t.Fatalf("CreateHetznerCluster: %v", err)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
	if result.Name != expectedResponse.Name {
		t.Errorf("Name = %s, want %s", result.Name, expectedResponse.Name)
	}
}

func TestCreateHetznerCluster_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	testClient := newTestClient(t, handler)
	req := CreateHetznerClusterRequest{
		Name:                   "test",
		CredentialID:           "cred-1",
		Location:               "fsn1",
		NetworkIPRange:         "10.0.0.0/16",
		SubnetRange:            "10.0.1.0/24",
		BastionServerType:      "cx11",
		ControlPlaneCount:      1,
		ControlPlaneServerType: "cx21",
		WorkerCount:            2,
		WorkerServerType:       "cx21",
		Distribution:           "k3s",
	}
	_, err := testClient.CreateHetznerCluster(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionHetznerCluster_Success(t *testing.T) {
	expectedResponse := DeprovisionHetznerClusterResponse{
		Success:   true,
		ClusterID: "cluster-123",
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123" {
			t.Errorf("path = %s, want /api/v1/clusters/hetzner/cluster-123", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DeprovisionHetznerCluster("cluster-123")
	if err != nil {
		t.Fatalf("DeprovisionHetznerCluster: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.ClusterID != "cluster-123" {
		t.Errorf("ClusterID = %s, want cluster-123", result.ClusterID)
	}
}

func TestDeprovisionHetznerCluster_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.DeprovisionHetznerCluster("cluster-123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestScaleHetznerWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{
		PreviousCount: 2,
		NewCount:      4,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ScaleHetznerWorkers("cluster-123", 4)
	if err != nil {
		t.Fatalf("ScaleHetznerWorkers: %v", err)
	}
	if result.NewCount != 4 {
		t.Errorf("NewCount = %d, want 4", result.NewCount)
	}
	if result.PreviousCount != 2 {
		t.Errorf("PreviousCount = %d, want 2", result.PreviousCount)
	}
}

func TestListHetznerNodeGroups_Success(t *testing.T) {
	expectedResponse := NodeGroupListResult{
		NodeGroups: []NodeGroupInfo{
			{Name: "workers", InstanceType: "cx21", Count: 2, Min: 1, Max: 5},
		},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/node-groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListHetznerNodeGroups("cluster-123")
	if err != nil {
		t.Fatalf("ListHetznerNodeGroups: %v", err)
	}
	if len(result.NodeGroups) != 1 {
		t.Fatalf("NodeGroups len = %d, want 1", len(result.NodeGroups))
	}
	if result.NodeGroups[0].Name != "workers" {
		t.Errorf("NodeGroups[0].Name = %s, want workers", result.NodeGroups[0].Name)
	}
}

func TestAddHetznerNodeGroup_Success(t *testing.T) {
	expectedResponse := AddNodeGroupResult{
		GroupName: "gpu-pool",
		Count:     2,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/node-groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := AddNodeGroupRequest{
		Name:         "gpu-pool",
		InstanceType: "cx21",
		Count:        2,
	}
	result, err := testClient.AddHetznerNodeGroup("cluster-123", req)
	if err != nil {
		t.Fatalf("AddHetznerNodeGroup: %v", err)
	}
	if result.GroupName != "gpu-pool" {
		t.Errorf("GroupName = %s, want gpu-pool", result.GroupName)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d, want 2", result.Count)
	}
}
