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

// TestListExecutionsIncludeInternalIsSentOnlyWhenSet pins the wire contract
// for --include-internal: the server hides internal executions by default and
// reads include_internal_executions as an explicit opt-in, so the parameter
// must be present (true) when asked for and absent otherwise - never sent as
// "false", which some servers reject as a validation error.
func TestListExecutionsAttentionStateIsSentOnlyWhenSet(t *testing.T) {
	var capturedQueries []string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQueries = append(capturedQueries, r.URL.RawQuery)
		resolvedBy := "exec-2"
		jsonResponse(t, w, http.StatusOK, ExecutionListResponse{
			Result: []ExecutionSummary{{
				ID: "exec-1", Status: "failed", AttentionState: "resolved", ResolvedByExecutionID: &resolvedBy,
			}},
		})
	})

	resp, err := testClient.ListExecutions(ListExecutionsOptions{ClusterID: "cluster-uuid", AttentionState: "open"})
	if err != nil {
		t.Fatalf("ListExecutions error = %v", err)
	}
	if _, err := testClient.ListExecutions(ListExecutionsOptions{ClusterID: "cluster-uuid"}); err != nil {
		t.Fatalf("ListExecutions error = %v", err)
	}
	if !strings.Contains(capturedQueries[0], "attention_state=open") {
		t.Errorf("expected attention_state in query, got: %s", capturedQueries[0])
	}
	if strings.Contains(capturedQueries[1], "attention_state") {
		t.Errorf("attention_state must be omitted when unset, got: %s", capturedQueries[1])
	}
	if resp.Result[0].AttentionState != "resolved" || resp.Result[0].ResolvedByExecutionID == nil ||
		*resp.Result[0].ResolvedByExecutionID != "exec-2" {
		t.Errorf("attention fields not decoded: %+v", resp.Result[0])
	}
}

func TestListExecutionsIncludeInternalIsSentOnlyWhenSet(t *testing.T) {
	var capturedQueries []string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQueries = append(capturedQueries, r.URL.RawQuery)
		jsonResponse(t, w, http.StatusOK, ExecutionListResponse{})
	})

	if _, err := testClient.ListExecutions(ListExecutionsOptions{ClusterID: "cluster-uuid"}); err != nil {
		t.Fatalf("ListExecutions (default) error = %v", err)
	}
	if _, err := testClient.ListExecutions(ListExecutionsOptions{ClusterID: "cluster-uuid", IncludeInternalExecutions: true}); err != nil {
		t.Fatalf("ListExecutions (include internal) error = %v", err)
	}
	if len(capturedQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(capturedQueries))
	}
	if strings.Contains(capturedQueries[0], "include_internal_executions") {
		t.Errorf("default listing must not send include_internal_executions, got: %s", capturedQueries[0])
	}
	if !strings.Contains(capturedQueries[1], "include_internal_executions=true") {
		t.Errorf("expected include_internal_executions=true in query, got: %s", capturedQueries[1])
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
