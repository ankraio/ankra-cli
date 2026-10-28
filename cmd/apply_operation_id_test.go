package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

// applySubmittedMock answers every apply as the platform answers wait=false:
// accepted, with the operation that records the outcome.
type applySubmittedMock struct {
	applyOverrideMock
	operationID string
}

func (mock *applySubmittedMock) ApplyCluster(ctx context.Context, request client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	mock.applied = append(mock.applied, request)
	return &client.ImportResponse{OperationID: mock.operationID}, true, nil
}

func TestApplyClusterReadsTheOperationIDOfAnAcceptedApply(t *testing.T) {
	for name, testCase := range map[string]struct {
		body          map[string]string
		wantOperation string
	}{
		"the platform names the operation": {map[string]string{"status": "accepted", "operation_id": "7f0c3c1e-0000-4000-8000-000000000001"}, "7f0c3c1e-0000-4000-8000-000000000001"},
		"an older platform names none":     {map[string]string{"status": "accepted"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(responseWriter).Encode(testCase.body)
			}))
			defer server.Close()

			response, submitted, applyError := client.New("test-token", server.URL).ApplyCluster(
				context.Background(),
				client.CreateImportClusterRequest{Name: "smoke", Spec: client.CreateResourceSpec{Stacks: []client.Stack{}}},
				false,
			)
			if applyError != nil || !submitted {
				t.Fatalf("submitted=%v error=%v, want an accepted apply", submitted, applyError)
			}
			operationID := ""
			if response != nil {
				operationID = response.OperationID
			}
			if operationID != testCase.wantOperation {
				t.Fatalf("response = %+v, want operation %q", response, testCase.wantOperation)
			}
		})
	}
}

func TestClusterApplySubmittedNamesTheOperationToFollow(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applySubmittedMock{operationID: "7f0c3c1e-0000-4000-8000-000000000002"}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	stdout := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path); executeError != nil {
			t.Errorf("apply failed: %v", executeError)
		}
	})
	if !strings.Contains(stdout, "Follow it with: ankra cluster operations list 7f0c3c1e-0000-4000-8000-000000000002") {
		t.Errorf("a submitted apply must name the operation to follow:\n%s", stdout)
	}

	resetClusterApplyFlags(t)
	structured, executeError := executeCommand("cluster", "apply", "-f", path, "-o", "json")
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	var result asyncSubmittedResult
	if decodeError := json.Unmarshal([]byte(structured), &result); decodeError != nil {
		t.Fatalf("decoding %q: %v", structured, decodeError)
	}
	if !result.Submitted || result.OperationID != "7f0c3c1e-0000-4000-8000-000000000002" ||
		!strings.Contains(result.Hint, "ankra cluster operations list 7f0c3c1e-0000-4000-8000-000000000002") {
		t.Errorf("structured result = %+v, want the operation id and the command that follows it", result)
	}
}

func TestClusterApplySubmittedWithoutAnOperationKeepsTheWaitHint(t *testing.T) {
	resetClusterApplyFlags(t)
	setMockClient(t, &applySubmittedMock{})
	path := writeApplyOverrideDocument(t)

	stdout := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path); executeError != nil {
			t.Errorf("apply failed: %v", executeError)
		}
	})
	if strings.Contains(stdout, "Follow it with") || !strings.Contains(stdout, "Re-run the same command with --wait") {
		t.Errorf("without an operation id the output must keep the --wait guidance and name no operation:\n%s", stdout)
	}
}

// executionByIDMock serves one execution and fails the test if anything
// tries to list a cluster's executions.
type executionByIDMock struct {
	baseMock
	t *testing.T
}

func (mock executionByIDMock) GetExecution(executionID string) (client.ExecutionDetail, error) {
	return client.ExecutionDetail{Execution: client.ExecutionSummary{ID: executionID, DisplayName: "cluster_import_apply", Status: "failed"}}, nil
}

func (mock executionByIDMock) ListExecutions(options client.ListExecutionsOptions) (client.ExecutionListResponse, error) {
	mock.t.Errorf("listing executions (%+v) when one was asked for by id", options)
	return client.ExecutionListResponse{}, nil
}

func TestClusterOperationsListReadsOneOperationWithoutASelectedCluster(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetExecutionsListFlags(t)
	setMockClient(t, executionByIDMock{t: t})

	stdout := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "operations", "list", "7f0c3c1e-0000-4000-8000-000000000003"); executeError != nil {
			t.Errorf("an operation read by id must not need a selected cluster: %v", executeError)
		}
	})
	if !strings.Contains(stdout, "7f0c3c1e-0000-4000-8000-000000000003") || !strings.Contains(stdout, "cluster_import_apply") {
		t.Errorf("output = %q, want the operation's details", stdout)
	}
}
