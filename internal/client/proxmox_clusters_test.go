package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateProxmoxCluster_Success(t *testing.T) {
	expectedResponse := CreateProxmoxClusterResponse{ClusterID: "pve-cluster-123", Name: "pve-test"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/proxmox" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	})
	request := CreateProxmoxClusterRequest{
		Name: "pve-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Node: "pve1", Bridge: "vmbr0", BastionInstanceType: "px-small",
		ControlPlaneCount: 1, ControlPlaneInstanceType: "px-medium",
		WorkerCount: 2, WorkerInstanceType: "px-medium", Distribution: "k3s",
		IncludeNetworking: true,
	}
	result, createError := testClient.CreateProxmoxCluster(request)
	if createError != nil {
		t.Fatalf("CreateProxmoxCluster: %v", createError)
	}
	if result.ClusterID != expectedResponse.ClusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, expectedResponse.ClusterID)
	}
}

func TestCreateProxmoxCluster_SendsDecoderFieldsAndOmitsOptionals(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if decodeError := json.NewDecoder(r.Body).Decode(&receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateProxmoxClusterResponse{ClusterID: "pve-cluster-123", Name: "pve-test"})
	})
	request := CreateProxmoxClusterRequest{
		Name: "pve-test", CredentialID: "cred-1", SSHKeyCredentialID: "ssh-1",
		Node: "pve1", PlacementNodes: []string{"pve1", "pve2"}, Bridge: "vmbr0",
		BastionInstanceType: "px-small", ControlPlaneCount: 1, ControlPlaneInstanceType: "px-medium",
		WorkerCount: 2, WorkerInstanceType: "px-medium", Distribution: "k3s",
		IncludeNetworking: true,
	}
	if _, createError := testClient.CreateProxmoxCluster(request); createError != nil {
		t.Fatalf("CreateProxmoxCluster: %v", createError)
	}
	if got, _ := receivedBody["node"].(string); got != "pve1" {
		t.Errorf("node = %q, want pve1", got)
	}
	if got, _ := receivedBody["bridge"].(string); got != "vmbr0" {
		t.Errorf("bridge = %q, want vmbr0", got)
	}
	if got, ok := receivedBody["include_networking"].(bool); !ok || !got {
		t.Errorf("include_networking = %v, want true", receivedBody["include_networking"])
	}
	if placementNodes, _ := receivedBody["placement_nodes"].([]any); len(placementNodes) != 2 {
		t.Errorf("placement_nodes = %v, want two entries", receivedBody["placement_nodes"])
	}
	if _, present := receivedBody["external_cloud_provider"]; present {
		t.Error("external_cloud_provider must never be sent for proxmox (the backend refuses it)")
	}
	for _, omitted := range []string{"storage", "template", "kubernetes_version", "description", "cni"} {
		if _, present := receivedBody[omitted]; present {
			t.Errorf("%s should be omitted when unset", omitted)
		}
	}
}

func TestCreateProxmoxCluster_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	})
	_, createError := testClient.CreateProxmoxCluster(CreateProxmoxClusterRequest{Name: "pve-test"})
	if createError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeprovisionProxmoxCluster_Success(t *testing.T) {
	expectedResponse := ProviderDeprovisionClusterResponse{Success: true, ClusterID: "pve-cluster-123"}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/proxmox/pve-cluster-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, deprovisionError := testClient.DeprovisionProxmoxCluster("pve-cluster-123")
	if deprovisionError != nil {
		t.Fatalf("DeprovisionProxmoxCluster: %v", deprovisionError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestScaleProxmoxWorkers_Success(t *testing.T) {
	expectedResponse := ScaleWorkersResult{PreviousCount: 2, NewCount: 5}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/proxmox/cluster-123/scale-workers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	})
	result, scaleError := testClient.ScaleProxmoxWorkers("cluster-123", 5)
	if scaleError != nil {
		t.Fatalf("ScaleProxmoxWorkers: %v", scaleError)
	}
	if result.NewCount != 5 {
		t.Errorf("NewCount = %d, want 5", result.NewCount)
	}
}

func TestListProxmoxNodes_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/proxmox/nodes" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("credential_id"); got != "cred-1" {
			t.Errorf("credential_id = %q, want cred-1", got)
		}
		jsonResponse(t, w, http.StatusOK, []ProxmoxNode{
			{Node: "pve1", Status: "online", CPUCount: 16, MemoryBytes: 68719476736},
		})
	})
	result, listError := testClient.ListProxmoxNodes("cred-1")
	if listError != nil {
		t.Fatalf("ListProxmoxNodes: %v", listError)
	}
	if len(result) != 1 || result[0].Node != "pve1" || result[0].CPUCount != 16 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestListProxmoxStorages_SendsNodeQuery(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/proxmox/storages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("node"); got != "pve1" {
			t.Errorf("node = %q, want pve1", got)
		}
		jsonResponse(t, w, http.StatusOK, []ProxmoxStorage{
			{Storage: "local-lvm", Type: "lvmthin", Active: true, AvailableBytes: 100, TotalBytes: 200},
		})
	})
	result, listError := testClient.ListProxmoxStorages("cred-1", "pve1")
	if listError != nil {
		t.Fatalf("ListProxmoxStorages: %v", listError)
	}
	if len(result) != 1 || result[0].Storage != "local-lvm" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestListProxmoxSizes_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/proxmox/sizes" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []ProxmoxSize{
			{Slug: "px-small", VCPUs: 2, MemoryMB: 2048, DiskGB: 20, Available: true},
		})
	})
	result, listError := testClient.ListProxmoxSizes()
	if listError != nil {
		t.Fatalf("ListProxmoxSizes: %v", listError)
	}
	if len(result) != 1 || result[0].Slug != "px-small" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestStopProxmoxCluster_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/proxmox/cluster-123/stop" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ProviderStopClusterResponse{Success: true, ClusterID: "cluster-123"})
	})
	result, stopError := testClient.StopProxmoxCluster("cluster-123")
	if stopError != nil {
		t.Fatalf("StopProxmoxCluster: %v", stopError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestStartProxmoxCluster_SendsScope(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/proxmox/cluster-123/start" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("scope"); got != "control_plane" {
			t.Errorf("scope = %q, want control_plane", got)
		}
		jsonResponse(t, w, http.StatusOK, ProviderStartClusterResult{Scope: "control_plane", CreatedOperations: 1})
	})
	result, startError := testClient.StartProxmoxCluster("cluster-123", "control_plane")
	if startError != nil {
		t.Fatalf("StartProxmoxCluster: %v", startError)
	}
	if result.Scope != "control_plane" {
		t.Errorf("Scope = %q, want control_plane", result.Scope)
	}
}
