package client

import (
	"net/http"
	"testing"
)

func TestStopScalewayCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/scaleway/cluster-123/stop" {
			t.Errorf("path = %s, want /api/v1/clusters/scaleway/cluster-123/stop", request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, ProviderStopClusterResponse{
			Success:   true,
			ClusterID: "cluster-123",
		})
	})

	result, stopError := testClient.StopScalewayCluster("cluster-123")
	if stopError != nil {
		t.Fatalf("StopScalewayCluster: %v", stopError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.ClusterID != "cluster-123" {
		t.Errorf("ClusterID = %q, want cluster-123", result.ClusterID)
	}
}

func TestStartScalewayCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/scaleway/cluster-123/start" {
			t.Errorf("path = %s, want /api/v1/clusters/scaleway/cluster-123/start", request.URL.Path)
		}
		if scope := request.URL.Query().Get("scope"); scope != "control_plane" {
			t.Errorf("scope = %q, want control_plane", scope)
		}
		jsonResponse(t, responseWriter, http.StatusOK, ProviderStartClusterResult{
			MarkedToStartAt:   "2026-07-20T09:00:00Z",
			Scope:             "control_plane",
			CreatedOperations: 2,
		})
	})

	result, startError := testClient.StartScalewayCluster("cluster-123", "control_plane")
	if startError != nil {
		t.Fatalf("StartScalewayCluster: %v", startError)
	}
	if result.Scope != "control_plane" {
		t.Errorf("Scope = %q, want control_plane", result.Scope)
	}
	if result.CreatedOperations != 2 {
		t.Errorf("CreatedOperations = %d, want 2", result.CreatedOperations)
	}
}
