package client

import (
	"encoding/json"
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

func TestRestartScalewayClusterNode_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/scaleway/cluster-1/nodes/node-1/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, RestartNodeResult{OperationID: "op-1", NodeID: "node-1", JobName: "scaleway_restart_server"})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.RestartScalewayClusterNode("cluster-1", "node-1")
	if err != nil {
		t.Fatalf("RestartScalewayClusterNode: %v", err)
	}
	if result.JobName != "scaleway_restart_server" {
		t.Errorf("JobName = %s, want scaleway_restart_server", result.JobName)
	}
}

func TestListScalewayClusterNodes_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/scaleway/cluster-1/nodes" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, NodeListResult{Nodes: []NodeSummary{{ID: "node-1", Name: "worker-1"}}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListScalewayClusterNodes("cluster-1")
	if err != nil {
		t.Fatalf("ListScalewayClusterNodes: %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Name != "worker-1" {
		t.Errorf("Nodes = %+v, want one node named worker-1", result.Nodes)
	}
}

func TestGetScalewayClusterNode_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/scaleway/cluster-1/nodes/node-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, NodeDetail{ID: "node-1", Name: "worker-1", State: "up"})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetScalewayClusterNode("cluster-1", "node-1")
	if err != nil {
		t.Fatalf("GetScalewayClusterNode: %v", err)
	}
	if result.Name != "worker-1" {
		t.Errorf("Name = %s, want worker-1", result.Name)
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

// ankra-lk328: the nodes list carries what the platform serves for each
// machine, the zone included, and a platform that does not serve a field
// yet leaves it nil rather than failing the read.
func TestListOvhClusterNodes_DecodesZonePublicIPAndKubernetesVersion(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes": [
			{"id": "node-1", "name": "bastion", "state": "up", "public_ip": "57.130.20.200",
			 "availability_zone": "eu-west-par-a", "availability_zone_source": "provider",
			 "kubernetes_version": null, "provider_status": "ACTIVE", "provider_power_state": null},
			{"id": "node-2", "name": "cp-0", "state": "up", "kubernetes_version": "v1.33.4"}
		]}`))
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListOvhClusterNodes("cluster-1")
	if err != nil {
		t.Fatalf("ListOvhClusterNodes: %v", err)
	}
	bastion, controlPlane := result.Nodes[0], result.Nodes[1]
	if bastion.AvailabilityZone == nil || *bastion.AvailabilityZone != "eu-west-par-a" ||
		bastion.AvailabilityZoneSource == nil || *bastion.AvailabilityZoneSource != "provider" {
		t.Errorf("bastion zone = %v from %v, want eu-west-par-a from provider", bastion.AvailabilityZone, bastion.AvailabilityZoneSource)
	}
	if bastion.PublicIP == nil || *bastion.PublicIP != "57.130.20.200" {
		t.Errorf("bastion PublicIP = %v, want 57.130.20.200", bastion.PublicIP)
	}
	if controlPlane.KubernetesVersion == nil || *controlPlane.KubernetesVersion != "v1.33.4" {
		t.Errorf("cp-0 KubernetesVersion = %v, want v1.33.4", controlPlane.KubernetesVersion)
	}
	if controlPlane.AvailabilityZone != nil || controlPlane.AvailabilityZoneSource != nil {
		t.Errorf("a platform that serves no zone leaves it nil, got %v from %v",
			controlPlane.AvailabilityZone, controlPlane.AvailabilityZoneSource)
	}
}

// -o json prints the struct, so a field the platform did not report prints
// as null instead of vanishing: every node carries the same keys.
func TestNodeSummary_EncodesUnreportedFieldsAsNull(t *testing.T) {
	encoded, err := json.Marshal(NodeSummary{ID: "node-1", Name: "cp-0"})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	for _, key := range []string{"availability_zone", "availability_zone_source", "public_ip", "kubernetes_version",
		"provider_status", "provider_power_state"} {
		value, present := decoded[key]
		if !present || value != nil {
			t.Errorf("%s = %v (present %v), want null", key, value, present)
		}
	}
}
