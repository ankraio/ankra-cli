package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestOvhNodeConsoleLog_PostsToTheOVHConsoleRoute(t *testing.T) {
	expected := NodeConsoleLogResult{
		OperationID: "22222222-2222-2222-2222-222222222222",
		NodeID:      "44444444-4444-4444-4444-444444444444",
		JobName:     "ovh_node_console_log",
		Status:      "success",
		Completed:   true,
		Report: map[string]any{"action": "console_log", "console_output": "kernel: BUG: soft lockup\n",
			"line_count": 1, "line_limit": 1000, "line_limit_reached": false, "truncated": false},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-123/nodes/node-456/console-log" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, expected)
	}
	result, err := newTestClient(t, handler).OvhNodeConsoleLog(context.Background(), "cluster-123", "node-456")
	if err != nil {
		t.Fatalf("OvhNodeConsoleLog: %v", err)
	}
	if result.JobName != expected.JobName || !result.Completed || result.Report["console_output"] != "kernel: BUG: soft lockup\n" {
		t.Fatalf("result = %+v", result)
	}
}

func TestOvhNodeConsoleLog_SurfacesThePlatformsRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": "A remediation job is already in progress for this node"})
	}
	_, err := newTestClient(t, handler).OvhNodeConsoleLog(context.Background(), "c", "n")
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("error = %v, want the platform's detail", err)
	}
}

func TestGetNodeKubeletConfig_ReadsTheNodesRouteAndKeepsTheReason(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/clusters/cluster-123/kubernetes/nodes/worker-1/kubelet-config":
			jsonResponse(t, w, http.StatusOK, NodeKubeletConfig{NodeName: "worker-1",
				KubeletConfig: map[string]any{"maxPods": 110, "evictionHard": map[string]any{"memory.available": "100Mi"}}})
		default:
			jsonResponse(t, w, http.StatusBadGateway, map[string]any{"detail": "the apiserver could not reach the kubelet of worker-2"})
		}
	}
	testClient := newTestClient(t, handler)
	config, err := testClient.GetNodeKubeletConfig("cluster-123", "worker-1")
	if err != nil {
		t.Fatalf("GetNodeKubeletConfig: %v", err)
	}
	if config.NodeName != "worker-1" || config.KubeletConfig["maxPods"] != float64(110) {
		t.Fatalf("config = %+v", config)
	}
	if _, unreachableError := testClient.GetNodeKubeletConfig("cluster-123", "worker-2"); unreachableError == nil ||
		!strings.Contains(unreachableError.Error(), "could not reach the kubelet") {
		t.Fatalf("error = %v, want the platform's reason", unreachableError)
	}
}
