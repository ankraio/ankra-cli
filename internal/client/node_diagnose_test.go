package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDiagnoseClusterNode_PostsToTheProvidersNodeRoute(t *testing.T) {
	expected := NodeDiagnoseResult{
		OperationID: "22222222-2222-2222-2222-222222222222",
		StepID:      "33333333-3333-3333-3333-333333333333",
		NodeID:      "44444444-4444-4444-4444-444444444444",
		JobName:     "hetzner_node_diagnose",
		Status:      "success",
		Completed:   true,
		Report: map[string]any{"action": "diagnose",
			"diagnosis": map[string]any{"final_outcome": "diagnose_only", "symptoms_detected": []any{"kubelet_not_running"}}},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/nodes/node-456/diagnose" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, expected)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DiagnoseClusterNode(context.Background(), "hetzner", "cluster-123", "node-456")
	if err != nil {
		t.Fatalf("DiagnoseClusterNode: %v", err)
	}
	if result.JobName != expected.JobName || !result.Completed || result.NodeID != expected.NodeID {
		t.Fatalf("result = %+v", result)
	}
	diagnosis, _ := result.Report["diagnosis"].(map[string]any)
	if diagnosis["final_outcome"] != "diagnose_only" {
		t.Errorf("report diverged: %+v", result.Report)
	}
}

func TestDiagnoseClusterNode_UsesTheProviderSegment(t *testing.T) {
	for _, provider := range []string{"ovh", "upcloud", "digitalocean", "scaleway", "ankracloud", "aws"} {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if want := "/api/v1/clusters/" + provider + "/c/nodes/n/diagnose"; r.URL.Path != want {
				t.Errorf("path = %s, want %s", r.URL.Path, want)
			}
			jsonResponse(t, w, http.StatusOK, NodeDiagnoseResult{JobName: provider + "_node_diagnose"})
		}
		if _, err := newTestClient(t, handler).DiagnoseClusterNode(context.Background(), provider, "c", "n"); err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
	}
}

// A missing node is a 404 and an unsupported provider or node state a 409,
// both with the platform's wording, which has to reach the user verbatim with
// its status (the 404 maps onto the not-found exit code).
func TestDiagnoseClusterNode_RefusalSurfacesDetail(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		const refusal = "Node remediation over SSH is not available for this cluster's provider."
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, status, map[string]string{"detail": refusal})
		}
		_, err := newTestClient(t, handler).DiagnoseClusterNode(context.Background(), "proxmox", "c", "n")
		if err == nil || err.Error() != refusal {
			t.Fatalf("status %d: error = %v, want the platform's refusal wording", status, err)
		}
		var unexpected *UnexpectedResponseError
		if !errors.As(err, &unexpected) || unexpected.StatusCode != status {
			t.Errorf("status %d: expected an UnexpectedResponseError carrying it, got %#v", status, err)
		}
	}
}

// The endpoint blocks for up to two minutes, so a caller that gave up has to
// surface the cancelled context, which the command maps onto the
// wait-timeout exit code.
func TestDiagnoseClusterNode_HonoursTheContextDeadline(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newTestClient(t, handler).DiagnoseClusterNode(ctx, "hetzner", "c", "n")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}
