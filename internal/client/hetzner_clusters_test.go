package client

import (
	"net/http"
	"strings"
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

func TestGetHetznerWorkerCount(t *testing.T) {
	clusterID := "cluster-123"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "worker-count"}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := WorkerCountResult{
			WorkerCount: 3,
			Min:         1,
			Max:         10,
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.GetHetznerWorkerCount(clusterID)
		if err != nil {
			t.Fatalf("GetHetznerWorkerCount: %v", err)
		}
		if result.WorkerCount != 3 {
			t.Errorf("WorkerCount = %d, want 3", result.WorkerCount)
		}
		if result.Min != 1 {
			t.Errorf("Min = %d, want 1", result.Min)
		}
		if result.Max != 10 {
			t.Errorf("Max = %d, want 10", result.Max)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.GetHetznerWorkerCount(clusterID)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestGetHetznerK8sVersion(t *testing.T) {
	clusterID := "cluster-123"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "k8s-version"}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := K8sVersionInfo{
			CurrentVersion: strPtr("1.29.0"),
			Distribution:   "k3s",
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.GetHetznerK8sVersion(clusterID)
		if err != nil {
			t.Fatalf("GetHetznerK8sVersion: %v", err)
		}
		if result.Distribution != "k3s" {
			t.Errorf("Distribution = %s, want k3s", result.Distribution)
		}
		if result.CurrentVersion == nil || *result.CurrentVersion != "1.29.0" {
			t.Errorf("CurrentVersion = %v, want 1.29.0", result.CurrentVersion)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.GetHetznerK8sVersion(clusterID)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestUpgradeHetznerK8sVersion(t *testing.T) {
	clusterID := "cluster-123"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "upgrade-k8s-version"}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := UpgradeK8sVersionResult{
			PreviousVersion: strPtr("1.28.0"),
			NewVersion:      "1.29.0",
			NodesAffected:   5,
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.UpgradeHetznerK8sVersion(clusterID, "1.29.0")
		if err != nil {
			t.Fatalf("UpgradeHetznerK8sVersion: %v", err)
		}
		if result.NewVersion != "1.29.0" {
			t.Errorf("NewVersion = %s, want 1.29.0", result.NewVersion)
		}
		if result.NodesAffected != 5 {
			t.Errorf("NodesAffected = %d, want 5", result.NodesAffected)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid version"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.UpgradeHetznerK8sVersion(clusterID, "9.99.0")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestScaleHetznerNodeGroup(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "node-groups", groupName, "scale"}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := ScaleNodeGroupResult{
			GroupName:     "workers",
			PreviousCount: 2,
			NewCount:      4,
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %s, want PUT", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.ScaleHetznerNodeGroup(clusterID, groupName, 4)
		if err != nil {
			t.Fatalf("ScaleHetznerNodeGroup: %v", err)
		}
		if result.GroupName != "workers" {
			t.Errorf("GroupName = %s, want workers", result.GroupName)
		}
		if result.NewCount != 4 {
			t.Errorf("NewCount = %d, want 4", result.NewCount)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusConflict, map[string]string{"error": "scale in progress"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.ScaleHetznerNodeGroup(clusterID, groupName, 4)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestUpdateHetznerNodeGroupInstanceType(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "node-groups", groupName, "instance-type"}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := UpdateNodeGroupResult{
			GroupName: "workers",
			Updated:   3,
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %s, want PUT", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.UpdateHetznerNodeGroupInstanceType(clusterID, groupName, "cx31")
		if err != nil {
			t.Fatalf("UpdateHetznerNodeGroupInstanceType: %v", err)
		}
		if result.GroupName != "workers" {
			t.Errorf("GroupName = %s, want workers", result.GroupName)
		}
		if result.Updated != 3 {
			t.Errorf("Updated = %d, want 3", result.Updated)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid type"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.UpdateHetznerNodeGroupInstanceType(clusterID, groupName, "cx99")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestDeleteHetznerNodeGroup(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	wantPath := strings.Join([]string{"", "api", "v1", "clusters", "hetzner", clusterID, "node-groups", groupName}, "/")
	t.Run("success", func(t *testing.T) {
		expectedResponse := DeleteNodeGroupResult{
			GroupName: "workers",
			Deleted:   2,
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("method = %s, want DELETE", r.Method)
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			}
			jsonResponse(t, w, http.StatusOK, expectedResponse)
		}
		testClient := newTestClient(t, handler)
		result, err := testClient.DeleteHetznerNodeGroup(clusterID, groupName)
		if err != nil {
			t.Fatalf("DeleteHetznerNodeGroup: %v", err)
		}
		if result.GroupName != "workers" {
			t.Errorf("GroupName = %s, want workers", result.GroupName)
		}
		if result.Deleted != 2 {
			t.Errorf("Deleted = %d, want 2", result.Deleted)
		}
	})
	t.Run("error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "group not found"})
		}
		testClient := newTestClient(t, handler)
		_, err := testClient.DeleteHetznerNodeGroup(clusterID, groupName)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
