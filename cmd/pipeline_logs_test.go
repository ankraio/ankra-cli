package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

// concludedStep is one planned, concluded step - the fixture every archive-
// log test below resolves through GetPipelineRun.
func concludedStep() client.PipelineStep {
	return client.PipelineStep{ID: "step-1", StepKey: "checkout", Status: pipelineStepStatusConcluded}
}

func TestPipelineLogsConcludedStepReadsArchivedStepLog(t *testing.T) {
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "cloning commit abc123\nchecked out\n",
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if output != "cloning commit abc123\nchecked out\n" {
		t.Errorf("output = %q", output)
	}
	if mockClient.downloadArtifactID != "artifact-1" {
		t.Errorf("downloaded artifact id = %q, want the step_log artifact's own id", mockClient.downloadArtifactID)
	}
}

func TestPipelineLogsConcludedStepFollowIsANoOp(t *testing.T) {
	// --follow on an already-concluded step must not hang or error: there is
	// nothing left to follow, so it reads the same archived log as a bare
	// 'logs' call.
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "the whole log\n",
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout", "--follow")
	if executeError != nil {
		t.Fatalf("logs --follow error = %v", executeError)
	}
	if output != "the whole log\n" {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineLogsConcludedStepNoArchivedLog(t *testing.T) {
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "No archived log was recorded") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineLogsConcludedStepFollowsArtifactPages(t *testing.T) {
	// A run with more artifacts than one server page must not read as "no
	// archived log": the search follows next_cursor until it finds the
	// step's own step_log row.
	stepID := "step-1"
	otherStepID := "step-0"
	secondCursor, thirdCursor := "cursor-2", "cursor-3"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsPages: []client.PipelineArtifactList{
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-1", StepID: &otherStepID, Kind: client.PipelineArtifactKindStepLog,
					Status: client.PipelineArtifactStatusUploaded},
			}, NextCursor: &secondCursor},
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-2", Kind: client.PipelineArtifactKindArtifact,
					Status: client.PipelineArtifactStatusUploaded},
			}, NextCursor: &thirdCursor},
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-3", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
					Status: client.PipelineArtifactStatusUploaded},
			}},
		},
		downloadPayload: "found on the third page\n",
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if output != "found on the third page\n" {
		t.Errorf("output = %q", output)
	}
	if mockClient.downloadArtifactID != "artifact-3" {
		t.Errorf("downloaded artifact id = %q, want the step_log found on a later page", mockClient.downloadArtifactID)
	}
	if len(mockClient.artifactsOptions) != 3 {
		t.Fatalf("artifact list calls = %d, want one per page until the step_log is found", len(mockClient.artifactsOptions))
	}
	if mockClient.artifactsOptions[0].Cursor != "" ||
		mockClient.artifactsOptions[1].Cursor != secondCursor ||
		mockClient.artifactsOptions[2].Cursor != thirdCursor {
		t.Errorf("cursors asked for = %+v, want each page's own next_cursor", mockClient.artifactsOptions)
	}
}

func TestPipelineLogsConcludedStepStopsPagingOnceFound(t *testing.T) {
	// The walk must stop at the page carrying the step's log rather than
	// read the run's remaining artifacts for nothing.
	stepID := "step-1"
	secondCursor := "cursor-2"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsPages: []client.PipelineArtifactList{
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
					Status: client.PipelineArtifactStatusUploaded},
			}, NextCursor: &secondCursor},
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-2", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
					Status: client.PipelineArtifactStatusUploaded},
			}},
		},
		downloadPayload: "the first match\n",
	}
	if _, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout"); executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if len(mockClient.artifactsOptions) != 1 {
		t.Errorf("artifact list calls = %d, want the walk to stop at the first match", len(mockClient.artifactsOptions))
	}
}

func TestPipelineLogsConcludedStepCappedReadIsNotAbsence(t *testing.T) {
	// A server that keeps handing back a cursor must not make the command
	// walk forever, and giving up must not be reported as "no archived log
	// was recorded" - the absence was never observed.
	endlessCursor := "cursor-next"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsPages: []client.PipelineArtifactList{
			{Artifacts: []client.PipelineArtifact{}, NextCursor: &endlessCursor},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if strings.Contains(output, "No archived log was recorded") {
		t.Errorf("a capped read must not read as an observed absence: %q", output)
	}
	if !strings.Contains(output, "Stopped after") {
		t.Errorf("output = %q, want it to say the read was capped", output)
	}
	if len(mockClient.artifactsOptions) != pipelineArtifactPageBudget {
		t.Errorf("artifact list calls = %d, want the walk bounded at %d pages",
			len(mockClient.artifactsOptions), pipelineArtifactPageBudget)
	}
}

func TestPipelineLogsConcludedStepStillArchiving(t *testing.T) {
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusPending},
		}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "still being archived") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineLogsConcludedStepArchiveFailed(t *testing.T) {
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusFailed, ErrorMessage: "the vault rejected the upload"},
		}},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError == nil || !strings.Contains(executeError.Error(), "the vault rejected the upload") {
		t.Fatalf("error = %v, want it to carry the artifact's own error message", executeError)
	}
}

func TestPipelineLogsRunningStepStillStreamsLive(t *testing.T) {
	// A step that has not concluded must not take the archive-log branch,
	// even when the run also carries an (irrelevant) artifacts result -
	// resolving to the live relay is what --follow documents.
	executionID, executionStepID := "execution-1", "execution-step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{
			{ID: "step-1", StepKey: "checkout", Status: "running",
				ExecutionID: &executionID, ExecutionStepID: &executionStepID},
		}},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1", "--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if mockClient.artifactsRunID != "" {
		t.Errorf("a running step must not read artifacts at all, got artifactsRunID = %q", mockClient.artifactsRunID)
	}
}
