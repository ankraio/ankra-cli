package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListExecutionsBuildsQueryString(t *testing.T) {
	var capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/org/executions") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		capturedQuery = r.URL.RawQuery
		jsonResponse(t, w, http.StatusOK, ExecutionListResponse{
			Result: []ExecutionSummary{
				{
					ID:          "exec-1",
					Name:        "deploy_addon",
					DisplayName: "Deploy redis",
					Status:      "failed",
					Type:        "write",
					StepSummary: StepSummary{Total: 2, Failed: 1, Succeeded: 1},
				},
			},
			Pagination: Pagination{TotalCount: 1, TotalPages: 1, Page: 1, PageSize: 25},
		})
	})

	resp, err := testClient.ListExecutions(ListExecutionsOptions{
		ClusterID:  "cluster-uuid",
		StatusList: []string{"failed", "critical"},
		Page:       2,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("ListExecutions error = %v", err)
	}
	if len(resp.Result) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(resp.Result))
	}
	if resp.Result[0].ID != "exec-1" {
		t.Errorf("unexpected execution id: %s", resp.Result[0].ID)
	}
	if !strings.Contains(capturedQuery, "cluster_id=cluster-uuid") {
		t.Errorf("expected cluster_id in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "status=failed") || !strings.Contains(capturedQuery, "status=critical") {
		t.Errorf("expected status filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "page=2") || !strings.Contains(capturedQuery, "page_size=10") {
		t.Errorf("expected pagination in query, got: %s", capturedQuery)
	}
}

func TestGetExecutionReturnsDetail(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/exec-1") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ExecutionDetail{
			Execution: ExecutionSummary{
				ID:          "exec-1",
				Name:        "deploy_addon",
				DisplayName: "Deploy redis",
				Status:      "failed",
				Type:        "write",
			},
			Steps: []ExecutionStep{
				{ID: "step-1", Name: "install", Status: "failed", ErrorExcerpt: strPtr("ImagePullBackOff")},
			},
			StepSummary: StepSummary{Total: 1, Failed: 1},
		})
	})

	detail, err := testClient.GetExecution("exec-1")
	if err != nil {
		t.Fatalf("GetExecution error = %v", err)
	}
	if detail.Execution.ID != "exec-1" {
		t.Errorf("unexpected execution id: %s", detail.Execution.ID)
	}
	if len(detail.Steps) != 1 || detail.Steps[0].Status != "failed" {
		t.Errorf("unexpected steps: %+v", detail.Steps)
	}
}

func TestCancelExecution(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/cancel") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		jsonResponse(t, w, http.StatusOK, CancelExecutionResponse{
			ExecutionID: "exec-1",
			Status:      "cancelling",
		})
	})

	resp, err := testClient.CancelExecution(context.Background(), "exec-1")
	if err != nil {
		t.Fatalf("CancelExecution error = %v", err)
	}
	if resp.Status != "cancelling" {
		t.Errorf("expected cancelling status, got %s", resp.Status)
	}
}

func TestBatchCancelExecutionsSendsPayload(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload BatchCancelExecutionsRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal payload: %v", err)
		}
		if len(payload.ExecutionIDs) != 2 {
			t.Fatalf("expected 2 execution ids, got %d", len(payload.ExecutionIDs))
		}
		jsonResponse(t, w, http.StatusOK, BatchCancelExecutionsResponse{
			Cancelled: payload.ExecutionIDs,
		})
	})

	resp, err := testClient.BatchCancelExecutions(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("BatchCancelExecutions error = %v", err)
	}
	if len(resp.Cancelled) != 2 {
		t.Errorf("expected 2 cancelled, got %d", len(resp.Cancelled))
	}
}

func TestRetryExecution(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/retry") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		jsonResponse(t, w, http.StatusOK, ExecutionSummary{
			ID:          "new-exec-1",
			DisplayName: "Deploy redis (retry)",
			Status:      "running",
			Type:        "write",
		})
	})

	resp, err := testClient.RetryExecution(context.Background(), "exec-1")
	if err != nil {
		t.Fatalf("RetryExecution error = %v", err)
	}
	if resp.ID != "new-exec-1" {
		t.Errorf("expected new execution id, got %s", resp.ID)
	}
}

func TestCancelExecutionStep(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/steps/") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		jsonResponse(t, w, http.StatusOK, CancelStepResponse{
			ExecutionID: "exec-1",
			StepID:      "step-1",
			Status:      "cancelled",
		})
	})

	resp, err := testClient.CancelExecutionStep(context.Background(), "exec-1", "step-1")
	if err != nil {
		t.Fatalf("CancelExecutionStep error = %v", err)
	}
	if resp.Status != "cancelled" {
		t.Errorf("expected cancelled status, got %s", resp.Status)
	}
}
