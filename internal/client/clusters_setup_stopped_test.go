package client

import (
	"net/http"
	"strings"
	"testing"
)

// The cluster list decodes the platform's setup_stopped (ankra-2v9ewh): the
// operation a cluster's setup stopped at, while its stored state still reads
// creating. A row without the field, including every row from a platform
// that predates it, decodes to nil.
func TestListClustersDecodesTheStoppedSetup(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": [
			{"id": "stopped", "name": "provisioning-test", "state": "creating",
			 "setup_stopped": {"execution_id": "bc80d0cf-1a2b-4c3d-8e9f-0a1b2c3d4e5f", "status": "failed",
			  "error_excerpt": "Hetzner API 403 resource_limit_exceeded", "stopped_at": "2026-11-03T21:48:26Z"}},
			{"id": "cancelled", "name": "cancelled-create", "state": "provider_access",
			 "setup_stopped": {"execution_id": "0a1b2c3d-1a2b-4c3d-8e9f-0a1b2c3d4e5f", "status": "cancelled",
			  "error_excerpt": null, "stopped_at": "2026-11-03T22:00:00Z"}},
			{"id": "building", "name": "building", "state": "creating"}
		], "pagination": {"page": 1, "page_size": 25, "total_pages": 1, "total_count": 3}}`))
	})

	response, listError := testClient.ListClusters(1, 25)
	if listError != nil {
		t.Fatalf("ListClusters: %v", listError)
	}
	if len(response.Result) != 3 {
		t.Fatalf("decoded %d clusters, want 3", len(response.Result))
	}
	stopped := response.Result[0].SetupStopped
	if stopped == nil || stopped.ExecutionID != "bc80d0cf-1a2b-4c3d-8e9f-0a1b2c3d4e5f" || stopped.Status != "failed" ||
		stopped.ErrorExcerpt == nil || !strings.Contains(*stopped.ErrorExcerpt, "resource_limit_exceeded") ||
		stopped.StoppedAt == nil || *stopped.StoppedAt != "2026-11-03T21:48:26Z" {
		t.Errorf("setup_stopped = %+v, want the platform's operation, status, error and end", stopped)
	}
	if cancelled := response.Result[1].SetupStopped; cancelled == nil || cancelled.ErrorExcerpt != nil ||
		cancelled.Status != "cancelled" {
		t.Errorf("a cancelled setup with no error excerpt = %+v, want status cancelled and a nil excerpt", cancelled)
	}
	if building := response.Result[2].SetupStopped; building != nil {
		t.Errorf("a row without setup_stopped must decode to nil, got %+v", building)
	}
}
