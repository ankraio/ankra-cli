package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateMorpheusCluster_Success(t *testing.T) {
	expectedResponse := CreateMorpheusClusterResponse{ClusterID: "morpheus-cluster-123", Name: "morpheus-test"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/morpheus" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	request := CreateMorpheusClusterRequest{
		Name: "morpheus-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		GroupID: 1, CloudID: 2, NetworkID: 3, LayoutID: 4, BastionPlanID: 5,
		ControlPlaneCount: 1, ControlPlanePlanID: 6, WorkerCount: 2, WorkerPlanID: 7,
		Distribution: "k3s", IncludeNetworking: true,
	}
	result, createError := testClient.CreateMorpheusCluster(request)
	if createError != nil {
		t.Fatalf("CreateMorpheusCluster: %v", createError)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
}

func TestCreateMorpheusCluster_SendsNumericIDsAndOmitsOptionals(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if decodeError := json.NewDecoder(r.Body).Decode(&receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateMorpheusClusterResponse{ClusterID: "morpheus-cluster-123", Name: "morpheus-test"})
	})
	request := CreateMorpheusClusterRequest{
		Name: "morpheus-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		GroupID: 1, CloudID: 2, NetworkID: 3, LayoutID: 4, BastionPlanID: 5,
		ControlPlaneCount: 1, ControlPlanePlanID: 6, WorkerCount: 2, WorkerPlanID: 7,
		Distribution: "k3s", IncludeNetworking: true,
	}
	if _, createError := testClient.CreateMorpheusCluster(request); createError != nil {
		t.Fatalf("CreateMorpheusCluster: %v", createError)
	}
	for fieldName, want := range map[string]float64{
		"group_id": 1, "cloud_id": 2, "network_id": 3, "layout_id": 4,
		"bastion_plan_id": 5, "control_plane_plan_id": 6, "worker_plan_id": 7,
	} {
		if got, _ := receivedBody[fieldName].(float64); got != want {
			t.Errorf("%s = %v, want %v", fieldName, receivedBody[fieldName], want)
		}
	}
	if got, ok := receivedBody["include_networking"].(bool); !ok || !got {
		t.Errorf("include_networking = %v, want true", receivedBody["include_networking"])
	}
	if _, present := receivedBody["external_cloud_provider"]; present {
		t.Error("external_cloud_provider must never be sent for morpheus (the backend refuses it)")
	}
	for _, omitted := range []string{"virtual_image_id", "etcd_plan_id", "kubernetes_version", "description", "cni"} {
		if _, present := receivedBody[omitted]; present {
			t.Errorf("%s should be omitted when unset", omitted)
		}
	}
}

func TestCreateMorpheusCluster_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	_, createError := testClient.CreateMorpheusCluster(CreateMorpheusClusterRequest{Name: "morpheus-test"})
	if createError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionMorpheusCluster_Success(t *testing.T) {
	expectedResponse := ProviderDeprovisionClusterResponse{Success: true, ClusterID: "morpheus-cluster-123"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/morpheus/morpheus-cluster-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, deprovisionError := testClient.DeprovisionMorpheusCluster("morpheus-cluster-123")
	if deprovisionError != nil {
		t.Fatalf("DeprovisionMorpheusCluster: %v", deprovisionError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestScaleMorpheusWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{PreviousCount: 2, NewCount: 4}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/morpheus/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, scaleError := testClient.ScaleMorpheusWorkers("cluster-123", 4)
	if scaleError != nil {
		t.Fatalf("ScaleMorpheusWorkers: %v", scaleError)
	}
	if result.NewCount != 4 {
		t.Errorf("NewCount = %d, want 4", result.NewCount)
	}
}

func TestListMorpheusGroups_Success(t *testing.T) {
	location := "eu-west"
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/morpheus/groups" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("credential_id"); got != "cred-1" {
			t.Errorf("credential_id = %q, want cred-1", got)
		}
		jsonResponse(t, w, http.StatusOK, []MorpheusGroup{{ID: 1, Name: "default", Location: &location}})
	})
	result, listError := testClient.ListMorpheusGroups("cred-1")
	if listError != nil {
		t.Fatalf("ListMorpheusGroups: %v", listError)
	}
	if len(result) != 1 || result[0].ID != 1 || result[0].Name != "default" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestListMorpheusPlans_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/morpheus/plans" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []MorpheusPlan{{ID: 5, Name: "small"}})
	})
	result, listError := testClient.ListMorpheusPlans("cred-1")
	if listError != nil {
		t.Fatalf("ListMorpheusPlans: %v", listError)
	}
	if len(result) != 1 || result[0].ID != 5 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestStopMorpheusCluster_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/morpheus/cluster-123/stop" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ProviderStopClusterResponse{Success: true, ClusterID: "cluster-123"})
	})
	result, stopError := testClient.StopMorpheusCluster("cluster-123")
	if stopError != nil {
		t.Fatalf("StopMorpheusCluster: %v", stopError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestStartMorpheusCluster_SendsScope(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/morpheus/cluster-123/start" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("scope"); got != "all" {
			t.Errorf("scope = %q, want all", got)
		}
		jsonResponse(t, w, http.StatusOK, ProviderStartClusterResult{Scope: "all", CreatedOperations: 3})
	})
	result, startError := testClient.StartMorpheusCluster("cluster-123", "all")
	if startError != nil {
		t.Fatalf("StartMorpheusCluster: %v", startError)
	}
	if result.CreatedOperations != 3 {
		t.Errorf("CreatedOperations = %d, want 3", result.CreatedOperations)
	}
}
