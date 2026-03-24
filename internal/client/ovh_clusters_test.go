package client

import (
	"net/http"
	"strings"
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

func TestGetOvhWorkerCount_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedResponse := WorkerCountResult{WorkerCount: 3, Min: 1, Max: 10}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "worker-count"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetOvhWorkerCount(clusterID)
	if err != nil {
		t.Fatalf("GetOvhWorkerCount: %v", err)
	}
	if result.WorkerCount != expectedResponse.WorkerCount {
		t.Errorf("WorkerCount = %d, want %d", result.WorkerCount, expectedResponse.WorkerCount)
	}
}

func TestGetOvhWorkerCount_Error(t *testing.T) {
	clusterID := "cluster-123"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetOvhWorkerCount(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetOvhK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedResponse := K8sVersionInfo{
		CurrentVersion: strPtr("1.28.0"),
		Distribution:   "k3s",
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "k8s-version"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetOvhK8sVersion(clusterID)
	if err != nil {
		t.Fatalf("GetOvhK8sVersion: %v", err)
	}
	if result.Distribution != expectedResponse.Distribution {
		t.Errorf("Distribution = %s, want %s", result.Distribution, expectedResponse.Distribution)
	}
}

func TestGetOvhK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetOvhK8sVersion(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgradeOvhK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	targetVersion := "1.29.0"
	expectedResponse := UpgradeK8sVersionResult{
		NewVersion:    targetVersion,
		NodesAffected: 4,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "upgrade-k8s-version"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.UpgradeOvhK8sVersion(clusterID, targetVersion)
	if err != nil {
		t.Fatalf("UpgradeOvhK8sVersion: %v", err)
	}
	if result.NewVersion != targetVersion {
		t.Errorf("NewVersion = %s, want %s", result.NewVersion, targetVersion)
	}
}

func TestUpgradeOvhK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{"error": "upgrade in progress"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.UpgradeOvhK8sVersion(clusterID, "1.29.0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAddOvhNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedResponse := AddNodeGroupResult{GroupName: "gpu-pool", Count: 2}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "node-groups"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := AddNodeGroupRequest{Name: "gpu-pool", InstanceType: "b2-7", Count: 2}
	result, err := testClient.AddOvhNodeGroup(clusterID, req)
	if err != nil {
		t.Fatalf("AddOvhNodeGroup: %v", err)
	}
	if result.GroupName != expectedResponse.GroupName {
		t.Errorf("GroupName = %s, want %s", result.GroupName, expectedResponse.GroupName)
	}
}

func TestAddOvhNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	}
	testClient := newTestClient(t, handler)
	req := AddNodeGroupRequest{Name: "gpu-pool", InstanceType: "b2-7", Count: 2}
	_, err := testClient.AddOvhNodeGroup(clusterID, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestScaleOvhNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedResponse := ScaleNodeGroupResult{
		GroupName:     groupName,
		PreviousCount: 2,
		NewCount:      5,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "node-groups", groupName, "scale"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ScaleOvhNodeGroup(clusterID, groupName, 5)
	if err != nil {
		t.Fatalf("ScaleOvhNodeGroup: %v", err)
	}
	if result.NewCount != 5 {
		t.Errorf("NewCount = %d, want 5", result.NewCount)
	}
}

func TestScaleOvhNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]string{"error": "cannot scale"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.ScaleOvhNodeGroup(clusterID, groupName, 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpdateOvhNodeGroupInstanceType_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedResponse := UpdateNodeGroupResult{GroupName: groupName, Updated: 3}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "node-groups", groupName, "instance-type"}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.UpdateOvhNodeGroupInstanceType(clusterID, groupName, "b2-15")
	if err != nil {
		t.Fatalf("UpdateOvhNodeGroupInstanceType: %v", err)
	}
	if result.Updated != 3 {
		t.Errorf("Updated = %d, want 3", result.Updated)
	}
}

func TestUpdateOvhNodeGroupInstanceType_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid type"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.UpdateOvhNodeGroupInstanceType(clusterID, groupName, "unknown")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeleteOvhNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "old-pool"
	expectedResponse := DeleteNodeGroupResult{GroupName: groupName, Deleted: 2}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "ovh", clusterID, "node-groups", groupName}, "/")
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DeleteOvhNodeGroup(clusterID, groupName)
	if err != nil {
		t.Fatalf("DeleteOvhNodeGroup: %v", err)
	}
	if result.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2", result.Deleted)
	}
}

func TestDeleteOvhNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "old-pool"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.DeleteOvhNodeGroup(clusterID, groupName)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
