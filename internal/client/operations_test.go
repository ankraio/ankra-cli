package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListClusterOperations(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/operations") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, []OperationResponseListItem{
			{ID: "op1", Name: "operation1", Status: "running", CreatedAt: "2025-01-01", UpdatedAt: "2025-01-02"},
		})
	})
	got, err := testClient.ListClusterOperations("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterOperations() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "operation1" {
		t.Errorf("ListClusterOperations() got = %v", got)
	}
}

func TestCancelOperation(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/cancel") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.CancelOperation(context.Background(), "op-id")
	if err != nil {
		t.Fatalf("CancelOperation() error = %v", err)
	}
	if !got.Success {
		t.Errorf("CancelOperation() got.Success = %v, want true", got.Success)
	}
}

func TestCancelJob(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/jobs/") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.CancelJob(context.Background(), "op-id", "job-id")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if !got.Success {
		t.Errorf("CancelJob() got.Success = %v, want true", got.Success)
	}
}
