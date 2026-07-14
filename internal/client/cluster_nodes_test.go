package client

import (
	"net/http"
	"testing"
)

func TestRestartHetznerClusterNode_Success(t *testing.T) {
	expectedResponse := RestartNodeResult{
		OperationID: "op-123",
		NodeID:      "node-456",
		JobName:     "hetzner_restart_server",
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/nodes/node-456/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.RestartHetznerClusterNode("cluster-123", "node-456")
	if err != nil {
		t.Fatalf("RestartHetznerClusterNode: %v", err)
	}
	if result.OperationID != "op-123" {
		t.Errorf("OperationID = %s, want op-123", result.OperationID)
	}
	if result.JobName != "hetzner_restart_server" {
		t.Errorf("JobName = %s, want hetzner_restart_server", result.JobName)
	}
}

func TestRestartOvhClusterNode_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-1/nodes/node-1/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, RestartNodeResult{OperationID: "op-1", NodeID: "node-1", JobName: "ovh_restart_server"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.RestartOvhClusterNode("cluster-1", "node-1"); err != nil {
		t.Fatalf("RestartOvhClusterNode: %v", err)
	}
}

func TestRestartUpcloudClusterNode_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-1/nodes/node-1/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, RestartNodeResult{OperationID: "op-1", NodeID: "node-1", JobName: "upcloud_restart_server"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.RestartUpcloudClusterNode("cluster-1", "node-1"); err != nil {
		t.Fatalf("RestartUpcloudClusterNode: %v", err)
	}
}

func TestRestartDigitaloceanClusterNode_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-1/nodes/node-1/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, RestartNodeResult{OperationID: "op-1", NodeID: "node-1", JobName: "digitalocean_restart_server"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.RestartDigitaloceanClusterNode("cluster-1", "node-1"); err != nil {
		t.Fatalf("RestartDigitaloceanClusterNode: %v", err)
	}
}

func TestRestartHetznerClusterNode_ConflictSurfacesDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{
			"detail": "A restart is already in progress for this node",
		})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.RestartHetznerClusterNode("cluster-123", "node-456")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != "A restart is already in progress for this node" {
		t.Errorf("error = %q, want the backend detail message", err.Error())
	}
}

func TestRestartHetznerClusterNode_NotFoundSurfacesDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": "Node not found"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.RestartHetznerClusterNode("cluster-123", "missing-node")
	if err == nil || err.Error() != "Node not found" {
		t.Errorf("error = %v, want \"Node not found\"", err)
	}
}

func TestListHetznerClusterNodes_SurfacesProviderStatus(t *testing.T) {
	activeStatus := "ACTIVE"
	poweredOn := "running"
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, NodeListResult{
			Nodes: []NodeSummary{
				{ID: "node-1", Name: "worker-1", ProviderStatus: &activeStatus, ProviderPowerState: &poweredOn},
				{ID: "node-2", Name: "worker-2"},
			},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListHetznerClusterNodes("cluster-123")
	if err != nil {
		t.Fatalf("ListHetznerClusterNodes: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("Nodes len = %d, want 2", len(result.Nodes))
	}
	if result.Nodes[0].ProviderStatus == nil || *result.Nodes[0].ProviderStatus != "ACTIVE" {
		t.Errorf("Nodes[0].ProviderStatus = %v, want ACTIVE", result.Nodes[0].ProviderStatus)
	}
	if result.Nodes[0].ProviderPowerState == nil || *result.Nodes[0].ProviderPowerState != "running" {
		t.Errorf("Nodes[0].ProviderPowerState = %v, want running", result.Nodes[0].ProviderPowerState)
	}
	if result.Nodes[1].ProviderStatus != nil {
		t.Errorf("Nodes[1].ProviderStatus = %v, want nil", result.Nodes[1].ProviderStatus)
	}
}
