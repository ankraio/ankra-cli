package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func intPtr(value int) *int    { return &value }
func boolPtr(value bool) *bool { return &value }

func TestCreateManagedCluster_SendsKapsuleAndAutoscaling(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/managed/kapsule" {
			t.Errorf("path = %s, want /api/v1/org/clusters/managed/kapsule", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusOK, CreateManagedClusterResponse{ClusterID: "cluster-1", Name: "demo"})
	}
	testClient := newTestClient(t, handler)

	request := CreateManagedClusterRequest{
		Name:         "demo",
		CredentialID: "cred-1",
		Location:     "fr-par",
		NodePools: []ManagedClusterNodePoolRequest{
			{
				Name:        "workers",
				Size:        "DEV1-M",
				Count:       2,
				Autoscaling: &ManagedNodePoolAutoscaling{Enabled: true, MinCount: 1, MaxCount: 5},
			},
		},
		Kapsule: &KapsuleClusterOptions{PrivateNetworkID: "pn-123"},
	}
	result, err := testClient.CreateManagedCluster(ManagedK8sProviderKapsule, request)
	if err != nil {
		t.Fatalf("CreateManagedCluster: %v", err)
	}
	if result.ClusterID != "cluster-1" {
		t.Errorf("ClusterID = %s, want cluster-1", result.ClusterID)
	}

	kapsule, _ := receivedBody["kapsule"].(map[string]any)
	if kapsule == nil || kapsule["private_network_id"] != "pn-123" {
		t.Errorf("kapsule body = %v, want private_network_id pn-123", receivedBody["kapsule"])
	}
	pools, _ := receivedBody["node_pools"].([]any)
	if len(pools) != 1 {
		t.Fatalf("node_pools = %v, want one pool", receivedBody["node_pools"])
	}
	pool, _ := pools[0].(map[string]any)
	autoscaling, _ := pool["autoscaling"].(map[string]any)
	if autoscaling == nil {
		t.Fatalf("pool body = %v, want autoscaling member", pool)
	}
	if enabled, _ := autoscaling["enabled"].(bool); !enabled {
		t.Errorf("autoscaling.enabled = %v, want true", autoscaling["enabled"])
	}
	if minCount, _ := autoscaling["min_count"].(float64); minCount != 1 {
		t.Errorf("autoscaling.min_count = %v, want 1", autoscaling["min_count"])
	}
	if maxCount, _ := autoscaling["max_count"].(float64); maxCount != 5 {
		t.Errorf("autoscaling.max_count = %v, want 5", autoscaling["max_count"])
	}
}

func TestAddManagedNodePool_OmitsAutoscalingWhenUnset(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusOK, AddManagedNodePoolResponse{ClusterID: "cluster-1", NodePoolName: "workers", Count: 2})
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.AddManagedNodePool(ManagedK8sProviderGke, "cluster-1",
		AddManagedNodePoolRequest{Name: "workers", Size: "e2-medium", Count: 2}); err != nil {
		t.Fatalf("AddManagedNodePool: %v", err)
	}
	if _, present := receivedBody["autoscaling"]; present {
		t.Errorf("autoscaling should be omitted when unset, body = %v", receivedBody)
	}
}

func TestUpdateManagedNodePool_SendsPatchWithChangedFieldsOnly(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		wantPath := "/api/v1/org/clusters/managed/kapsule/cluster-1/node-pools/workers"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("content type = %s, want application/json", contentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusOK, UpdateManagedNodePoolResponse{
			ClusterID:    "cluster-1",
			NodePoolName: "workers",
			Count:        intPtr(4),
		})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.UpdateManagedNodePool(ManagedK8sProviderKapsule, "cluster-1", "workers",
		UpdateManagedNodePoolRequest{Count: intPtr(4)})
	if err != nil {
		t.Fatalf("UpdateManagedNodePool: %v", err)
	}
	if result.NodePoolName != "workers" || result.Count == nil || *result.Count != 4 {
		t.Errorf("result = %+v, want workers with count 4", result)
	}
	if count, _ := receivedBody["count"].(float64); count != 4 {
		t.Errorf("count = %v, want 4", receivedBody["count"])
	}
	for _, field := range []string{"autoscaling_enabled", "autoscaling_min", "autoscaling_max"} {
		if _, present := receivedBody[field]; present {
			t.Errorf("%s should be omitted when unset, body = %v", field, receivedBody)
		}
	}
}

func TestUpdateManagedNodePool_SendsAutoscalingFields(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusOK, UpdateManagedNodePoolResponse{
			ClusterID:          "cluster-1",
			NodePoolName:       "workers",
			AutoscalingEnabled: boolPtr(true),
			AutoscalingMin:     intPtr(1),
			AutoscalingMax:     intPtr(9),
		})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.UpdateManagedNodePool(ManagedK8sProviderAks, "cluster-1", "workers",
		UpdateManagedNodePoolRequest{
			AutoscalingEnabled: boolPtr(true),
			AutoscalingMin:     intPtr(1),
			AutoscalingMax:     intPtr(9),
		})
	if err != nil {
		t.Fatalf("UpdateManagedNodePool: %v", err)
	}
	if enabled, _ := receivedBody["autoscaling_enabled"].(bool); !enabled {
		t.Errorf("autoscaling_enabled = %v, want true", receivedBody["autoscaling_enabled"])
	}
	if minCount, _ := receivedBody["autoscaling_min"].(float64); minCount != 1 {
		t.Errorf("autoscaling_min = %v, want 1", receivedBody["autoscaling_min"])
	}
	if maxCount, _ := receivedBody["autoscaling_max"].(float64); maxCount != 9 {
		t.Errorf("autoscaling_max = %v, want 9", receivedBody["autoscaling_max"])
	}
	if _, present := receivedBody["count"]; present {
		t.Errorf("count should be omitted when unset, body = %v", receivedBody)
	}
	if result.AutoscalingMax == nil || *result.AutoscalingMax != 9 {
		t.Errorf("result = %+v, want autoscaling max 9", result)
	}
}

func TestUpdateManagedNodePool_ErrorSurfacesDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"detail": "at least one field is required"})
	}
	testClient := newTestClient(t, handler)

	_, err := testClient.UpdateManagedNodePool(ManagedK8sProviderDoks, "cluster-1", "workers",
		UpdateManagedNodePoolRequest{})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "at least one field is required") {
		t.Errorf("expected backend detail in error, got: %v", err)
	}
}

func TestStopManagedCluster_PostsEmptyBody(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/api/v1/org/clusters/managed/aks/cluster-1/stop"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusOK, ManagedClusterLifecycleResponse{ClusterID: "cluster-1", Status: "stopping"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.StopManagedCluster(ManagedK8sProviderAks, "cluster-1")
	if err != nil {
		t.Fatalf("StopManagedCluster: %v", err)
	}
	if result.ClusterID != "cluster-1" || result.Status != "stopping" {
		t.Errorf("result = %+v, want cluster-1 stopping", result)
	}
	if len(receivedBody) != 0 {
		t.Errorf("expected empty JSON body, got %v", receivedBody)
	}
}

func TestStartManagedCluster_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/api/v1/org/clusters/managed/aks/cluster-1/start"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		jsonResponse(t, w, http.StatusOK, ManagedClusterLifecycleResponse{ClusterID: "cluster-1", Status: "starting"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.StartManagedCluster(ManagedK8sProviderAks, "cluster-1")
	if err != nil {
		t.Fatalf("StartManagedCluster: %v", err)
	}
	if result.Status != "starting" {
		t.Errorf("status = %s, want starting", result.Status)
	}
}

func TestStopManagedCluster_RefusalSurfacesDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{"detail": "provider gke does not support cluster stop"})
	}
	testClient := newTestClient(t, handler)

	_, err := testClient.StopManagedCluster(ManagedK8sProviderGke, "cluster-1")
	if err == nil {
		t.Fatal("expected error for refusal response")
	}
	if !strings.Contains(err.Error(), "does not support cluster stop") {
		t.Errorf("expected refusal detail in error, got: %v", err)
	}
}
