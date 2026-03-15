package client

import (
	"net/http"
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
