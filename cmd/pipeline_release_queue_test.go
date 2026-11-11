package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

const testPipelineRunID = "9c2e7a14-5b3d-4e8f-a1c6-2d7f9b4e6a31"

type pipelineReadsMock struct {
	baseMock
	queue       *client.PipelineReleaseQueue
	suggestions *client.PipelineResourceSuggestions
	failure     error
	selector    client.PipelineSelector
	runID       string
	window      int
}

func (mock *pipelineReadsMock) GetPipelineReleaseQueue(_ context.Context, selector client.PipelineSelector) (*client.PipelineReleaseQueue, error) {
	mock.selector = selector
	return mock.queue, mock.failure
}

func (mock *pipelineReadsMock) GetPipelineRunResourceSuggestions(_ context.Context, selector client.PipelineSelector,
	runID string, window int) (*client.PipelineResourceSuggestions, error) {
	mock.selector, mock.runID, mock.window = selector, runID, window
	return mock.suggestions, mock.failure
}

func runPipelineRead(t *testing.T, mock *pipelineReadsMock, arguments ...string) (string, error) {
	t.Helper()
	output, _, executeError := runFreshCommand(t, newPipelineCommand(), mock, "", arguments...)
	return output, executeError
}

func textPointer(value string) *string {
	return &value
}

func TestPipelineReleaseQueuePrintsTheCountsAndTheOldestRun(t *testing.T) {
	mock := &pipelineReadsMock{queue: &client.PipelineReleaseQueue{RepositoryID: testPipelineRepositoryID, Branch: "main",
		Queued: 2, Running: 1, OldestRunID: testPipelineRunID, CheckedAt: "2026-11-10T09:00:00Z"}}
	output, executeError := runPipelineRead(t, mock, "release-queue", "--repository", testPipelineRepositoryID)
	if executeError != nil {
		t.Fatalf("release-queue failed: %v", executeError)
	}
	if mock.selector.RepositoryID != testPipelineRepositoryID {
		t.Errorf("selector = %+v", mock.selector)
	}
	for _, want := range []string{"Branch:  main", "Queued:  2", "Running: 1",
		"ankra pipeline get " + testPipelineRunID + " --repository " + testPipelineRepositoryID} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}

	output, executeError = runPipelineRead(t, mock, "release-queue", "--application", testApplicationID, "-o", "json")
	if executeError != nil || mock.selector.ApplicationID != testApplicationID {
		t.Fatalf("error = %v, selector = %+v", executeError, mock.selector)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(output), &document); decodeError != nil || document["queued"] != float64(2) {
		t.Fatalf("-o json = %s (%v)", output, decodeError)
	}
}

func TestPipelineReleaseQueueFailures(t *testing.T) {
	missing := &pipelineReadsMock{failure: client.ErrPipelineReleaseQueueUnavailable}
	if _, missingError := runPipelineRead(t, missing, "release-queue", "--repository", testPipelineRepositoryID); exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("error = %v, want the not-found exit", missingError)
	}
	denied := &pipelineReadsMock{failure: &client.PermissionDeniedError{Permission: "pipelines.read"}}
	if _, deniedError := runPipelineRead(t, denied, "release-queue", "--repository", testPipelineRepositoryID); exitCodeFor(deniedError) != exitForbidden {
		t.Fatalf("error = %v, want the permission exit", deniedError)
	}
	if _, bothError := runPipelineRead(t, missing, "release-queue", "--repository", testPipelineRepositoryID,
		"--application", testApplicationID); exitCodeFor(bothError) != exitUsage {
		t.Fatalf("error = %v, want a usage error for both selectors", bothError)
	}
}

func TestPipelineResourceSuggestionsPrintsWhatToSetPerStage(t *testing.T) {
	mock := &pipelineReadsMock{suggestions: &client.PipelineResourceSuggestions{
		RunID: testPipelineRunID, Window: 50,
		Method: client.PipelineResourceSuggestionMethod{Percentile: 95, HeadroomPercent: 25},
		Stages: []client.PipelineStageResourceSuggestion{
			{Stage: "build", SampleCount: 12,
				Memory: client.PipelineMemoryResourceSuggestion{Status: "oversized", Suggested: textPointer("2Gi")},
				CPU:    client.PipelineCPUResourceSuggestion{Status: "right_sized"}},
			{Stage: "test", SampleCount: 3,
				Memory: client.PipelineMemoryResourceSuggestion{Status: "at_limit"},
				CPU:    client.PipelineCPUResourceSuggestion{Status: "insufficient_samples"}},
		}}}
	output, executeError := runPipelineRead(t, mock, "resource-suggestions", testPipelineRunID,
		"--repository", testPipelineRepositoryID, "--window", "50")
	if executeError != nil {
		t.Fatalf("resource-suggestions failed: %v", executeError)
	}
	if mock.runID != testPipelineRunID || mock.window != 50 {
		t.Errorf("run = %q, window = %d", mock.runID, mock.window)
	}
	for _, want := range []string{"build", "oversized", "set 2Gi", "keep", "at limit", "raise the limit", "not enough samples yet",
		"p95 of each stage's latest 50 successful attempts, plus 25% headroom"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}

	empty := &pipelineReadsMock{suggestions: &client.PipelineResourceSuggestions{RunID: testPipelineRunID,
		Stages: []client.PipelineStageResourceSuggestion{}}}
	output, executeError = runPipelineRead(t, empty, "resource-suggestions", testPipelineRunID, "--repository", testPipelineRepositoryID)
	if executeError != nil || empty.window != 0 || !strings.Contains(output, "usage report yet") {
		t.Fatalf("error = %v, window = %d, output: %s", executeError, empty.window, output)
	}
}

func TestPipelineResourceSuggestionsFailures(t *testing.T) {
	missing := &pipelineReadsMock{failure: client.ErrPipelineResourceSuggestionsUnavailable}
	_, missingError := runPipelineRead(t, missing, "resource-suggestions", testPipelineRunID, "--repository", testPipelineRepositoryID)
	if exitCodeFor(missingError) != exitNotFound || !strings.Contains(missingError.Error(), testPipelineRunID) {
		t.Fatalf("error = %v, want the not-found exit naming the run", missingError)
	}
	untouched := &pipelineReadsMock{}
	_, windowError := runPipelineRead(t, untouched, "resource-suggestions", testPipelineRunID,
		"--repository", testPipelineRepositoryID, "--window", "101")
	if exitCodeFor(windowError) != exitUsage || untouched.runID != "" {
		t.Fatalf("error = %v, run = %q, want a usage error before any request", windowError, untouched.runID)
	}
}
