package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCreateDigitaloceanCluster_Success(t *testing.T) {
	expectedResponse := CreateDigitaloceanClusterResponse{ClusterID: "digitalocean-cluster-123", Name: "digitalocean-test"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/digitalocean" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	req := CreateDigitaloceanClusterRequest{
		Name: "digitalocean-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "fi-hel1", NetworkIPRange: "10.0.0.0/16", BastionSize: "1xCPU-1GB",
		ControlPlaneCount: 1, ControlPlaneSize: "2xCPU-4GB",
		WorkerCount: 2, WorkerSize: "2xCPU-4GB", Distribution: "k3s",
	}
	result, err := testClient.CreateDigitaloceanCluster(req)
	if err != nil {
		t.Fatalf("CreateDigitaloceanCluster: %v", err)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
}

func TestCreateDigitaloceanCluster_SendsCloudProviderNetworkingAndGitopsFields(t *testing.T) {
	expectedResponse := CreateDigitaloceanClusterResponse{ClusterID: "digitalocean-cluster-123", Name: "digitalocean-test"}
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	branch := "main"
	req := CreateDigitaloceanClusterRequest{
		Name: "digitalocean-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "fi-hel1", NetworkIPRange: "10.0.0.0/16", BastionSize: "1xCPU-1GB",
		ControlPlaneCount: 1, ControlPlaneSize: "2xCPU-4GB",
		WorkerCount: 2, WorkerSize: "2xCPU-4GB", Distribution: "k3s",
		ExternalCloudProvider: true,
		IncludeNetworking:     true,
		GitopsCredentialName:  strPtr("github-cred"),
		GitopsRepository:      strPtr("acme/infra"),
		GitopsBranch:          &branch,
	}
	if _, err := testClient.CreateDigitaloceanCluster(req); err != nil {
		t.Fatalf("CreateDigitaloceanCluster: %v", err)
	}
	if got, ok := receivedBody["external_cloud_provider"].(bool); !ok || !got {
		t.Errorf("external_cloud_provider = %v, want true", receivedBody["external_cloud_provider"])
	}
	if got, ok := receivedBody["include_networking"].(bool); !ok || !got {
		t.Errorf("include_networking = %v, want true", receivedBody["include_networking"])
	}
	if got, _ := receivedBody["gitops_credential_name"].(string); got != "github-cred" {
		t.Errorf("gitops_credential_name = %q, want github-cred", got)
	}
	if got, _ := receivedBody["gitops_repository"].(string); got != "acme/infra" {
		t.Errorf("gitops_repository = %q, want acme/infra", got)
	}
	if got, _ := receivedBody["gitops_branch"].(string); got != "main" {
		t.Errorf("gitops_branch = %q, want main", got)
	}
}

func TestCreateDigitaloceanCluster_OmitsGitopsWhenUnset(t *testing.T) {
	expectedResponse := CreateDigitaloceanClusterResponse{ClusterID: "digitalocean-cluster-123", Name: "digitalocean-test"}
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	req := CreateDigitaloceanClusterRequest{
		Name: "digitalocean-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "fi-hel1", Distribution: "k3s",
		ExternalCloudProvider: false,
		IncludeNetworking:     false,
	}
	if _, err := testClient.CreateDigitaloceanCluster(req); err != nil {
		t.Fatalf("CreateDigitaloceanCluster: %v", err)
	}
	if got, ok := receivedBody["external_cloud_provider"].(bool); !ok || got {
		t.Errorf("external_cloud_provider = %v, want false", receivedBody["external_cloud_provider"])
	}
	if got, ok := receivedBody["include_networking"].(bool); !ok || got {
		t.Errorf("include_networking = %v, want false", receivedBody["include_networking"])
	}
	if _, present := receivedBody["gitops_credential_name"]; present {
		t.Errorf("gitops_credential_name should be omitted when unset")
	}
	if _, present := receivedBody["gitops_repository"]; present {
		t.Errorf("gitops_repository should be omitted when unset")
	}
}

func TestCreateDigitaloceanCluster_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	req := CreateDigitaloceanClusterRequest{
		Name: "digitalocean-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Region: "fi-hel1", NetworkIPRange: "10.0.0.0/16", BastionSize: "1xCPU-1GB",
		ControlPlaneCount: 1, ControlPlaneSize: "2xCPU-4GB",
		WorkerCount: 2, WorkerSize: "2xCPU-4GB", Distribution: "k3s",
	}
	_, err := testClient.CreateDigitaloceanCluster(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionDigitaloceanCluster_Success(t *testing.T) {
	expectedResponse := DeprovisionDigitaloceanClusterResponse{Success: true, ClusterID: "digitalocean-cluster-123"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/digitalocean/digitalocean-cluster-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.DeprovisionDigitaloceanCluster("digitalocean-cluster-123")
	if err != nil {
		t.Fatalf("DeprovisionDigitaloceanCluster: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestScaleDigitaloceanWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{PreviousCount: 2, NewCount: 6}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.ScaleDigitaloceanWorkers("cluster-123", 6)
	if err != nil {
		t.Fatalf("ScaleDigitaloceanWorkers: %v", err)
	}
	if result.NewCount != 6 {
		t.Errorf("NewCount = %d, want 6", result.NewCount)
	}
}

func TestListDigitaloceanNodeGroups_Success(t *testing.T) {
	expectedResponse := NodeGroupListResult{
		NodeGroups: []NodeGroupInfo{{Name: "workers", InstanceType: "2xCPU-4GB", Count: 2, Min: 1, Max: 10}},
	}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-123/node-groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, err := testClient.ListDigitaloceanNodeGroups("cluster-123")
	if err != nil {
		t.Fatalf("ListDigitaloceanNodeGroups: %v", err)
	}
	if len(result.NodeGroups) != 1 {
		t.Fatalf("NodeGroups len = %d, want 1", len(result.NodeGroups))
	}
}

func TestGetDigitaloceanWorkerCount_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "worker-count"}, "/")
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
	result, err := testClient.GetDigitaloceanWorkerCount(clusterID)
	if err != nil {
		t.Fatalf("GetDigitaloceanWorkerCount: %v", err)
	}
	if result.WorkerCount != expectedResponse.WorkerCount {
		t.Errorf("WorkerCount = %d, want %d", result.WorkerCount, expectedResponse.WorkerCount)
	}
}

func TestGetDigitaloceanWorkerCount_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "worker-count"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	_, err := testClient.GetDigitaloceanWorkerCount(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetDigitaloceanK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "k8s-version"}, "/")
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
	result, err := testClient.GetDigitaloceanK8sVersion(clusterID)
	if err != nil {
		t.Fatalf("GetDigitaloceanK8sVersion: %v", err)
	}
	if result.Distribution != expectedResponse.Distribution {
		t.Errorf("Distribution = %s, want %s", result.Distribution, expectedResponse.Distribution)
	}
}

func TestGetDigitaloceanK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "k8s-version"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	})
	_, err := testClient.GetDigitaloceanK8sVersion(clusterID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpgradeDigitaloceanK8sVersion_Success(t *testing.T) {
	clusterID := "cluster-123"
	targetVersion := "1.30.0"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "upgrade-k8s-version"}, "/")
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
	result, err := testClient.UpgradeDigitaloceanK8sVersion(clusterID, targetVersion, false)
	if err != nil {
		t.Fatalf("UpgradeDigitaloceanK8sVersion: %v", err)
	}
	if result.NewVersion != expectedResponse.NewVersion {
		t.Errorf("NewVersion = %s, want %s", result.NewVersion, expectedResponse.NewVersion)
	}
}

func TestUpgradeDigitaloceanK8sVersion_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "upgrade-k8s-version"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusConflict, map[string]string{"error": "upgrade blocked"})
	})
	_, err := testClient.UpgradeDigitaloceanK8sVersion(clusterID, "1.30.0", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAddDigitaloceanNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups"}, "/")
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
	result, _, err := testClient.AddDigitaloceanNodeGroup(context.Background(), clusterID, req, true)
	if err != nil {
		t.Fatalf("AddDigitaloceanNodeGroup: %v", err)
	}
	if result.GroupName != expectedResponse.GroupName {
		t.Errorf("GroupName = %s, want %s", result.GroupName, expectedResponse.GroupName)
	}
}

func TestAddDigitaloceanNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]string{"error": "validation"})
	})
	_, _, err := testClient.AddDigitaloceanNodeGroup(context.Background(), clusterID, AddNodeGroupRequest{Name: "x", InstanceType: "y", Count: 1}, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestScaleDigitaloceanNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName, "scale"}, "/")
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
	result, _, err := testClient.ScaleDigitaloceanNodeGroup(context.Background(), clusterID, groupName, 4, true)
	if err != nil {
		t.Fatalf("ScaleDigitaloceanNodeGroup: %v", err)
	}
	if result.NewCount != 4 {
		t.Errorf("NewCount = %d, want 4", result.NewCount)
	}
}

func TestScaleDigitaloceanNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName, "scale"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "scale failed"})
	})
	_, _, err := testClient.ScaleDigitaloceanNodeGroup(context.Background(), clusterID, groupName, 4, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpdateDigitaloceanNodeGroupInstanceType_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	instanceType := "4xCPU-8GB"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName, "instance-type"}, "/")
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
	result, _, err := testClient.UpdateDigitaloceanNodeGroupInstanceType(context.Background(), clusterID, groupName, instanceType, true)
	if err != nil {
		t.Fatalf("UpdateDigitaloceanNodeGroupInstanceType: %v", err)
	}
	if result.Updated != expectedResponse.Updated {
		t.Errorf("Updated = %d, want %d", result.Updated, expectedResponse.Updated)
	}
}

func TestUpdateDigitaloceanNodeGroupInstanceType_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName, "instance-type"}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	_, _, err := testClient.UpdateDigitaloceanNodeGroupInstanceType(context.Background(), clusterID, groupName, "4xCPU-8GB", true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeleteDigitaloceanNodeGroup_Success(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName}, "/")
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
	result, _, err := testClient.DeleteDigitaloceanNodeGroup(context.Background(), clusterID, groupName, true)
	if err != nil {
		t.Fatalf("DeleteDigitaloceanNodeGroup: %v", err)
	}
	if result.Deleted != expectedResponse.Deleted {
		t.Errorf("Deleted = %d, want %d", result.Deleted, expectedResponse.Deleted)
	}
}

func TestDeleteDigitaloceanNodeGroup_Error(t *testing.T) {
	clusterID := "cluster-123"
	groupName := "workers"
	expectedPath := strings.Join([]string{"", "api", "v1", "clusters", "digitalocean", clusterID, "node-groups", groupName}, "/")
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, expectedPath)
		}
		jsonResponse(t, w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	})
	_, _, err := testClient.DeleteDigitaloceanNodeGroup(context.Background(), clusterID, groupName, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
