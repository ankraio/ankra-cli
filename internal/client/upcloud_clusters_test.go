package client

import (
	"net/http"
	"strings"
	"testing"
)

func TestCreateUpcloudCluster_Success(t *testing.T) {
	expectedResponse := CreateUpcloudClusterResponse{ClusterID: "upcloud-cluster-123", Name: "upcloud-test"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/upcloud" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	req := CreateUpcloudClusterRequest{
		Name: "upcloud-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Zone: "fi-hel1", NetworkIPRange: "10.0.0.0/16", BastionPlan: "1xCPU-1GB",
		ControlPlaneCount: 1, ControlPlanePlan: "2xCPU-4GB",
		WorkerCount: 2, WorkerPlan: "2xCPU-4GB", Distribution: "k3s",
	}
	result, err := testClient.CreateUpcloudCluster(req)
	if err != nil {
		t.Fatalf("CreateUpcloudCluster: %v", err)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
}

func TestCreateUpcloudCluster_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	req := CreateUpcloudClusterRequest{
		Name: "upcloud-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Zone: "fi-hel1", NetworkIPRange: "10.0.0.0/16", BastionPlan: "1xCPU-1GB",
		ControlPlaneCount: 1, ControlPlanePlan: "2xCPU-4GB",
		WorkerCount: 2, WorkerPlan: "2xCPU-4GB", Distribution: "k3s",
	}
	_, err := testClient.CreateUpcloudCluster(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionUpcloudCluster_Success(t *testing.T) {
	expectedResponse := DeprovisionUpcloudClusterResponse{Success: true, ClusterID: "upcloud-cluster-123"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/upcloud/upcloud-cluster-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.DeprovisionUpcloudCluster("upcloud-cluster-123")
	if err != nil {
		t.Fatalf("DeprovisionUpcloudCluster: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestScaleUpcloudWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{PreviousCount: 2, NewCount: 6}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.ScaleUpcloudWorkers("cluster-123", 6)
	if err != nil {
		t.Fatalf("ScaleUpcloudWorkers: %v", err)
	}
	if result.NewCount != 6 {
		t.Errorf("NewCount = %d, want 6", result.NewCount)
	}
}

func TestListUpcloudNodeGroups_Success(t *testing.T) {
	expectedResponse := NodeGroupListResult{
		NodeGroups: []NodeGroupInfo{{Name: "workers", InstanceType: "2xCPU-4GB", Count: 2, Min: 1, Max: 10}},
	}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-123/node-groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.ListUpcloudNodeGroups("cluster-123")
	if err != nil {
		t.Fatalf("ListUpcloudNodeGroups: %v", err)
	}
	if len(result.NodeGroups) != 1 {
		t.Fatalf("NodeGroups len = %d, want 1", len(result.NodeGroups))
	}
}

func TestGetUpcloudWorkerCount_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "worker-count"}, "/")
	expectedResponse := WorkerCountResult{WorkerCount: 3, Min: 1, Max: 10}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.GetUpcloudWorkerCount(clusterID)
	if err != nil {
		t.Fatalf("GetUpcloudWorkerCount: %v", err)
	}
	if result.WorkerCount != expectedResponse.WorkerCount {
		t.Errorf("WorkerCount = %d, want %d", result.WorkerCount, expectedResponse.WorkerCount)
	}
}

func TestGetUpcloudWorkerCount_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "worker-count"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	_, err := testClient.GetUpcloudWorkerCount(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetUpcloudK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "k8s-version"}, "/")
	expectedResponse := K8sVersionInfo{
		CurrentVersion: strPtr("1.29.0"),
		Distribution:   "k3s",
	}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.GetUpcloudK8sVersion(clusterID)
	if err != nil {
		t.Fatalf("GetUpcloudK8sVersion: %v", err)
	}
	if result.Distribution != expectedResponse.Distribution {
		t.Errorf("Distribution = %s, want %s", result.Distribution, expectedResponse.Distribution)
	}
}

func TestGetUpcloudK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "k8s-version"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	})
	_, err := testClient.GetUpcloudK8sVersion(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgradeUpcloudK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	targetVersion := "1.30.0"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "upgrade-k8s-version"}, "/")
	expectedResponse := UpgradeK8sVersionResult{
		NewVersion:    "1.30.0",
		NodesAffected: 5,
	}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.UpgradeUpcloudK8sVersion(clusterID, targetVersion)
	if err != nil {
		t.Fatalf("UpgradeUpcloudK8sVersion: %v", err)
	}
	if result.NewVersion != expectedResponse.NewVersion {
		t.Errorf("NewVersion = %s, want %s", result.NewVersion, expectedResponse.NewVersion)
	}
}

func TestUpgradeUpcloudK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "upgrade-k8s-version"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusConflict, map[string]string{"error": "upgrade blocked"})
	})
	_, err := testClient.UpgradeUpcloudK8sVersion(clusterID, "1.30.0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAddUpcloudNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups"}, "/")
	expectedResponse := AddNodeGroupResult{GroupName: "extra", Count: 2}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	req := AddNodeGroupRequest{Name: "extra", InstanceType: "2xCPU-4GB", Count: 2}
	result, err := testClient.AddUpcloudNodeGroup(clusterID, req)
	if err != nil {
		t.Fatalf("AddUpcloudNodeGroup: %v", err)
	}
	if result.GroupName != expectedResponse.GroupName {
		t.Errorf("GroupName = %s, want %s", result.GroupName, expectedResponse.GroupName)
	}
}

func TestAddUpcloudNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]string{"error": "validation"})
	})
	_, err := testClient.AddUpcloudNodeGroup(clusterID, AddNodeGroupRequest{Name: "x", InstanceType: "y", Count: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestScaleUpcloudNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName, "scale"}, "/")
	expectedResponse := ScaleNodeGroupResult{
		GroupName:     groupName,
		PreviousCount: 2,
		NewCount:      4,
	}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.ScaleUpcloudNodeGroup(clusterID, groupName, 4)
	if err != nil {
		t.Fatalf("ScaleUpcloudNodeGroup: %v", err)
	}
	if result.NewCount != 4 {
		t.Errorf("NewCount = %d, want 4", result.NewCount)
	}
}

func TestScaleUpcloudNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName, "scale"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "scale failed"})
	})
	_, err := testClient.ScaleUpcloudNodeGroup(clusterID, groupName, 4)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpdateUpcloudNodeGroupInstanceType_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	instanceType := "4xCPU-8GB"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName, "instance-type"}, "/")
	expectedResponse := UpdateNodeGroupResult{GroupName: groupName, Updated: 2}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.UpdateUpcloudNodeGroupInstanceType(clusterID, groupName, instanceType)
	if err != nil {
		t.Fatalf("UpdateUpcloudNodeGroupInstanceType: %v", err)
	}
	if result.Updated != expectedResponse.Updated {
		t.Errorf("Updated = %d, want %d", result.Updated, expectedResponse.Updated)
	}
}

func TestUpdateUpcloudNodeGroupInstanceType_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName, "instance-type"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	_, err := testClient.UpdateUpcloudNodeGroupInstanceType(clusterID, groupName, "4xCPU-8GB")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeleteUpcloudNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName}, "/")
	expectedResponse := DeleteNodeGroupResult{GroupName: groupName, Deleted: 2}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.DeleteUpcloudNodeGroup(clusterID, groupName)
	if err != nil {
		t.Fatalf("DeleteUpcloudNodeGroup: %v", err)
	}
	if result.Deleted != expectedResponse.Deleted {
		t.Errorf("Deleted = %d, want %d", result.Deleted, expectedResponse.Deleted)
	}
}

func TestDeleteUpcloudNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "upcloud", clusterID, "node-groups", groupName}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	})
	_, err := testClient.DeleteUpcloudNodeGroup(clusterID, groupName)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
