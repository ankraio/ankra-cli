package cmd

// What `pipeline logs` says about a step Ankra retried. The fixtures these
// use - concludedBuildStep, lostAttemptOf, retryOf, runDetailWithSteps - live
// in pipeline_logs_test.go alongside the rest of the lane's tests.

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

// TestPipelineLogsByKeyNamesTheEarlierAttempts pins half of PLA-871:
// `logs --step <key>` answers the newest attempt, which is right, and used to
// say nothing at all about the attempt Ankra had retried away - so a run whose
// build failed once and passed on the retry read as a clean build with no
// failure in it. The note goes to stderr, leaving stdout the log and nothing
// else.
func TestPipelineLogsByKeyNamesTheEarlierAttempts(t *testing.T) {
	newest := retryOf(concludedBuildStep())
	lost := lostAttemptOf(concludedBuildStep())
	detail := runDetailWithSteps("concluded", lost, newest)
	detail.ID = "run-1"
	mockClient := &pipelineLaneMock{
		getResult: &detail,
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-2", StepID: &newest.ID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "built and pushed\n",
	}

	standardOutput, standardError, executeError := runPipelineCommandSeparately(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if standardOutput != "built and pushed\n" {
		t.Errorf("stdout = %q, want the resolved attempt's log and nothing else", standardOutput)
	}
	if !strings.Contains(standardError, `Showing attempt 2 of step "build"`) {
		t.Errorf("stderr must say which attempt was shown, got %q", standardError)
	}
	// The selector is part of the command, not decoration: `pipeline logs`
	// resolves no run without one (resolvePipelineTarget ends in a usage
	// error when neither flag is given and the working directory answers for
	// neither), so a hint that printed only --step was one this very caller
	// could not have pasted.
	if !strings.Contains(standardError,
		"attempt 1: ankra pipeline logs run-1 --application "+testApplicationID+" --step step-1-attempt-1") {
		t.Errorf("stderr must give a runnable command for the earlier attempt, got %q", standardError)
	}
}

// TestPipelineLogsSaysNothingAboutAttemptsForAStepThatWasNotRetried is the
// negative: the note exists for a retried step, and a step with one attempt
// must not grow a line about attempts it never had.
func TestPipelineLogsSaysNothingAboutAttemptsForAStepThatWasNotRetried(t *testing.T) {
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "checked out\n",
	}

	_, standardError, executeError := runPipelineCommandSeparately(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if strings.Contains(standardError, "Showing attempt") {
		t.Errorf("a step with one attempt explains no retry, got %q", standardError)
	}
}

// TestPipelineLogsByAttemptIDReadsThatAttemptsOwnLog pins the path the note
// above points at: an id names one attempt row exactly, so the attempt Ankra
// retried away is readable even though its key resolves to the newer one.
func TestPipelineLogsByAttemptIDReadsThatAttemptsOwnLog(t *testing.T) {
	newest := retryOf(concludedBuildStep())
	lost := lostAttemptOf(concludedBuildStep())
	detail := runDetailWithSteps("concluded", lost, newest)
	detail.ID = "run-1"
	mockClient := &pipelineLaneMock{
		getResult: &detail,
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &lost.ID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
			{ID: "artifact-2", StepID: &newest.ID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "rootlesskit: permission denied\n",
	}

	standardOutput, _, executeError := runPipelineCommandSeparately(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", lost.ID)
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if standardOutput != "rootlesskit: permission denied\n" {
		t.Errorf("stdout = %q, want the named attempt's own log", standardOutput)
	}
	if mockClient.downloadArtifactID != "artifact-1" {
		t.Errorf("downloaded artifact id = %q, want the lost attempt's own step_log", mockClient.downloadArtifactID)
	}
}
