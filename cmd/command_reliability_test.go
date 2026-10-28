package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// failingRetryExecutionMock returns an error from RetryExecution to exercise
// the non-zero-exit path of `cluster operations retry`.
type failingRetryExecutionMock struct {
	baseMock
}

func (m *failingRetryExecutionMock) RetryExecution(_ context.Context, _ string) (*client.ExecutionSummary, error) {
	return nil, errors.New("simulated retry failure")
}

// listClustersErrorMock returns an error from ListClusters to exercise
// `cluster list` failure handling.
type listClustersErrorMock struct {
	baseMock
}

func (m *listClustersErrorMock) ListClusters(_, _ int) (*client.ClusterListResponse, error) {
	return nil, errors.New("backend unreachable")
}

func TestClusterListReturnsErrorOnAPIFailure(t *testing.T) {
	setMockClient(t, &listClustersErrorMock{})
	output, err := executeCommand("cluster", "list")
	if err == nil {
		t.Fatalf("expected error exit, got output=%s", output)
	}
	if !strings.Contains(err.Error(), "backend unreachable") {
		t.Errorf("expected upstream error to be wrapped, got %v", err)
	}
}

func TestClusterOperationsListReturnsErrorWhenNoSelection(t *testing.T) {
	setMockClient(t, &baseMock{})
	withTempHome(t)
	_, err := executeCommand("cluster", "operations", "list")
	if err == nil {
		t.Fatalf("expected error when no cluster is selected")
	}
	if !strings.Contains(err.Error(), "no cluster specified") {
		t.Errorf("expected no-cluster-selected error, got %v", err)
	}
}

func TestClusterOperationsRetryReturnsErrorOnAPIFailure(t *testing.T) {
	setMockClient(t, &failingRetryExecutionMock{})
	_, err := executeCommand("cluster", "operations", "retry", "exec-id-123")
	if err == nil {
		t.Fatalf("expected error exit")
	}
	if !strings.Contains(err.Error(), "simulated retry failure") {
		t.Errorf("expected upstream error wrapped, got %v", err)
	}
}
