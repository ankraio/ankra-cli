package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type stepsResultsMock struct {
	baseMock
	resultCalls    int
	gotExecutionID string
	resultError    error
}

func (mock *stepsResultsMock) GetExecution(executionID string) (client.ExecutionDetail, error) {
	name := "upcloud_delete_network"
	return client.ExecutionDetail{
		Execution: client.ExecutionSummary{ID: executionID, DisplayName: "Reconcile 2 resources", Status: "success"},
		Steps: []client.ExecutionStep{
			{ID: "step-1", Name: name, Status: "success"},
			{ID: "step-2", Name: "upcloud_delete_router", Status: "pending"},
		},
	}, nil
}

func (mock *stepsResultsMock) EnrichExecutionDetailWithDrift(_ *client.ExecutionDetail) error { return nil }

func (mock *stepsResultsMock) GetExecutionResult(executionID string) (client.ExecutionResultResponse, error) {
	mock.resultCalls++
	mock.gotExecutionID = executionID
	if mock.resultError != nil {
		return client.ExecutionResultResponse{}, mock.resultError
	}
	return client.ExecutionResultResponse{
		ExecutionID: executionID,
		Results: []client.StepResult{{
			StepID: "step-1",
			Result: map[string]any{
				"status":                        "success",
				"force_cleanup_storage_deleted": []any{"uuid-v1"},
			},
		}},
	}, nil
}

// TestOperationsSteps_ResultsFlagFetchesAndMergesStepResults pins the new
// --results lane: the result endpoint is called once, and the -o json shape
// carries each finished step's result under step_results in step order,
// while a step without a result row is simply absent from that list.
func TestOperationsSteps_ResultsFlagFetchesAndMergesStepResults(t *testing.T) {
	resetConfirmFlag(t, clusterOperationsStepsCmd)
	mock := &stepsResultsMock{}
	var runError error
	output := captureStdout(t, func() {
		_, runError = runWithInput(t, mock, "", "cluster", "operations", "steps", "exec-1", "--results", "-o", "json")
	})
	if runError != nil {
		t.Fatalf("execute failed: %v", runError)
	}
	if mock.resultCalls != 1 || mock.gotExecutionID != "exec-1" {
		t.Fatalf("expected one result fetch for exec-1, got calls=%d id=%q", mock.resultCalls, mock.gotExecutionID)
	}
	var decoded struct {
		Steps       []map[string]any `json:"steps"`
		StepResults []struct {
			StepID string         `json:"step_id"`
			Result map[string]any `json:"result"`
		} `json:"step_results"`
	}
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output must be JSON, got %v: %s", unmarshalError, output)
	}
	if len(decoded.Steps) != 2 {
		t.Fatalf("expected the two steps in the payload, got %d", len(decoded.Steps))
	}
	if len(decoded.StepResults) != 1 || decoded.StepResults[0].StepID != "step-1" {
		t.Fatalf("expected exactly the finished step's result, got %+v", decoded.StepResults)
	}
	deleted, _ := decoded.StepResults[0].Result["force_cleanup_storage_deleted"].([]any)
	if len(deleted) != 1 || deleted[0] != "uuid-v1" {
		t.Fatalf("result payload drifted: %+v", decoded.StepResults[0].Result)
	}
}

// TestOperationsSteps_WithoutResultsFlagSkipsResultFetch pins the default:
// plain `steps` never calls the result endpoint and its JSON keeps the
// existing shape (no step_results member).
func TestOperationsSteps_WithoutResultsFlagSkipsResultFetch(t *testing.T) {
	resetConfirmFlag(t, clusterOperationsStepsCmd)
	mock := &stepsResultsMock{}
	var runError error
	output := captureStdout(t, func() {
		_, runError = runWithInput(t, mock, "", "cluster", "operations", "steps", "exec-1", "-o", "json")
	})
	if runError != nil {
		t.Fatalf("execute failed: %v", runError)
	}
	if mock.resultCalls != 0 {
		t.Fatalf("plain steps must not fetch results, got %d calls", mock.resultCalls)
	}
	if strings.Contains(output, "step_results") {
		t.Fatalf("plain steps JSON must keep its shape, got: %s", output)
	}
}

// TestOperationsSteps_ResultsFetchFailureIsAnError pins that a failed result
// fetch surfaces as the command's error instead of a silently partial view.
func TestOperationsSteps_ResultsFetchFailureIsAnError(t *testing.T) {
	resetConfirmFlag(t, clusterOperationsStepsCmd)
	mock := &stepsResultsMock{resultError: errors.New("boom")}
	var runError error
	captureStdout(t, func() {
		_, runError = runWithInput(t, mock, "", "cluster", "operations", "steps", "exec-1", "--results")
	})
	if runError == nil || !strings.Contains(runError.Error(), "fetching execution results") {
		t.Fatalf("expected the result fetch failure to surface, got %v", runError)
	}
}
