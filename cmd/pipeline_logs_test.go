package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// concludedStep is one planned, concluded step - the fixture every archive-
// log test below resolves through GetPipelineRun. It carries no execution,
// so it has no log stream either and the archive is its only copy.
func concludedStep() client.PipelineStep {
	return client.PipelineStep{ID: "step-1", StepKey: "checkout", Status: pipelineStepStatusConcluded}
}

// concludedStepThatRan is a concluded step that reached an execution, so the
// platform's retained log stream can still answer for it when the run has no
// archived log.
func concludedStepThatRan() client.PipelineStep {
	executionID, executionStepID := "execution-1", "execution-step-1"
	step := concludedStep()
	step.ExecutionID = &executionID
	step.ExecutionStepID = &executionStepID
	return step
}

// runningStepThatStarted is one planned step still producing output, the
// fixture the live-relay tests resolve through GetPipelineRun.
func runningStepThatStarted() client.PipelineStep {
	executionID, executionStepID := "execution-1", "execution-step-1"
	return client.PipelineStep{ID: "step-1", StepKey: "checkout", Status: "running",
		ExecutionID: &executionID, ExecutionStepID: &executionStepID}
}

// blockedStep is a planned step still waiting on its dependencies: the state
// a run is in the moment it is dispatched, and so the one a person asking for
// a live tail most often meets.
func blockedStep() client.PipelineStep {
	return client.PipelineStep{ID: "step-1", StepKey: "build", Status: pipelineStepStatusBlocked,
		DependsOn: []string{"checkout"}}
}

// pendingStep is the same step once its dependencies are satisfied and the
// claim scan has not taken it yet - still no execution, so still no stream.
func pendingStep() client.PipelineStep {
	step := blockedStep()
	step.Status = pipelineStepStatusPending
	return step
}

// startedBuildStep is that step once an agent claimed it, which is what gives
// it a subject on the log relay.
func startedBuildStep() client.PipelineStep {
	executionID, executionStepID := "execution-1", "execution-step-1"
	step := blockedStep()
	step.Status = pipelineStepStatusRunning
	step.ExecutionID = &executionID
	step.ExecutionStepID = &executionStepID
	return step
}

// concludedBuildStep is that step once it finished. A --follow test needs it:
// the tail's own status poll is what ends the command, and a step that never
// concludes would be reconnected to forever.
func concludedBuildStep() client.PipelineStep {
	step := startedBuildStep()
	step.Status = pipelineStepStatusConcluded
	return step
}

// runDetailWithStep wraps one step as the run detail GetPipelineRun answers,
// carrying the run's own status so the wait can tell a step that is still
// coming from a run that finished without it.
func runDetailWithStep(runStatus string, step client.PipelineStep) client.PipelineRunDetail {
	detail := client.PipelineRunDetail{Steps: []client.PipelineStep{step}}
	detail.Status = runStatus
	return detail
}

// shortenPipelineLogReplayIdleTimeout keeps the idle guard's behaviour
// testable without holding a test open for the production wait.
//
// It writes a package-level var and restores it on cleanup, so a test that
// calls it must not call t.Parallel: two parallel tests would race on the
// same global. Nothing in this package is parallel today.
func shortenPipelineLogReplayIdleTimeout(t *testing.T) {
	t.Helper()
	previous := pipelineLogReplayIdleTimeout
	pipelineLogReplayIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { pipelineLogReplayIdleTimeout = previous })
}

// shortenPipelineStepStartWait makes the not-started wait's poll interval and
// its bound testable: the production values are five seconds and thirty
// minutes, and a test must exercise several polls without spending either.
//
// Like the helper above it writes package-level vars, so a test that calls it
// must not call t.Parallel.
func shortenPipelineStepStartWait(t *testing.T, bound time.Duration) {
	t.Helper()
	previousInterval, previousBound := pipelineStepStartPollInterval, pipelineStepStartWaitBound
	pipelineStepStartPollInterval = time.Millisecond
	pipelineStepStartWaitBound = bound
	t.Cleanup(func() {
		pipelineStepStartPollInterval = previousInterval
		pipelineStepStartWaitBound = previousBound
	})
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

func TestPipelineLogsConcludedStepPrefersTheLiveLogOverASupersededOne(t *testing.T) {
	// Re-dispatching a step supersedes the step_log it had already minted -
	// that row is marked failed and a fresh one written under the same step
	// id - and the listing is oldest-first, so the first match is the
	// superseded row. Reporting its failure would state that the step's log
	// was not archived when the live one is sitting further down the run.
	stepID := "step-1"
	secondCursor := "cursor-2"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsPages: []client.PipelineArtifactList{
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-superseded", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
					Status:       client.PipelineArtifactStatusFailed,
					ErrorMessage: "a new attempt of the step was dispatched before this upload was settled"},
			}, NextCursor: &secondCursor},
			{Artifacts: []client.PipelineArtifact{
				{ID: "artifact-live", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
					Status: client.PipelineArtifactStatusUploaded},
			}},
		},
		downloadPayload: "the log the step actually wrote\n",
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if output != "the log the step actually wrote\n" {
		t.Errorf("output = %q, want the live log rather than the superseded row's failure", output)
	}
	if mockClient.downloadArtifactID != "artifact-live" {
		t.Errorf("downloaded artifact id = %q, want the newest step_log for the step", mockClient.downloadArtifactID)
	}
	if len(mockClient.artifactsOptions) != 2 {
		t.Errorf("artifact list calls = %d, want the walk to keep looking past a superseded row",
			len(mockClient.artifactsOptions))
	}
}

func TestPipelineLogsConcludedStepReportsTheNewestFailureWhenEveryLogFailed(t *testing.T) {
	// When every step_log for the step failed, the one worth reporting is
	// the newest - the superseded row's reason describes the dispatch that
	// replaced it, not why this step has no log.
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-superseded", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status:       client.PipelineArtifactStatusFailed,
				ErrorMessage: "a new attempt of the step was dispatched before this upload was settled"},
			{ID: "artifact-live", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusFailed, ErrorMessage: "the vault rejected the upload"},
		}},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError == nil {
		t.Fatalf("logs error = nil, want the archived log's own failure")
	}
	if !strings.Contains(executeError.Error(), "the vault rejected the upload") {
		t.Errorf("error = %v, want the newest step_log row's reason", executeError)
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
	if len(mockClient.streamOptions) != 1 {
		t.Fatalf("stream calls = %d, want one live connection", len(mockClient.streamOptions))
	}
	if mockClient.streamOptions[0] != (client.StepLogStreamOptions{}) {
		t.Errorf("stream options = %+v, want none sent so the platform decides from the step's status",
			mockClient.streamOptions[0])
	}
}

func TestPipelineLogsRunningStepReplayAsksForTheOutputAlreadyProduced(t *testing.T) {
	// --replay is the only way a live connection sees what the step printed
	// before it was opened, and it must reach the wire as replay=true rather
	// than change anything the CLI does locally.
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{
			runningStepThatStarted(),
		}},
		streamEvents: []client.PipelineLogEvent{
			{Type: "line", Stream: "stdout", Line: "already printed", Seq: 1},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout", "--replay")
	if executeError != nil {
		t.Fatalf("logs --replay error = %v", executeError)
	}
	if !strings.Contains(output, "[stdout] already printed") {
		t.Errorf("output = %q, want the replayed line", output)
	}
	if len(mockClient.streamOptions) != 1 || mockClient.streamOptions[0].IsReplaying == nil ||
		!*mockClient.streamOptions[0].IsReplaying {
		t.Fatalf("stream options = %+v, want replay=true on the wire", mockClient.streamOptions)
	}
	if mockClient.streamOptions[0].IsFollowing != nil {
		t.Errorf("follow = %v, want it unsent: --replay must not change when the connection ends",
			*mockClient.streamOptions[0].IsFollowing)
	}
}

func TestPipelineLogsConcludedStepWithoutArchiveReplaysTheRetainedStream(t *testing.T) {
	// The case this fallback exists for: an organisation with no ready
	// backup vault never gets a step_log artifact, so the run's artifacts
	// are read to the end and hold none. The step's output is still on the
	// platform's retained stream, and follow=false replays it and ends.
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
		streamEvents: []client.PipelineLogEvent{
			{Type: "line", Stream: "stdout", Line: "cloning commit abc123", Seq: 1},
			{Type: "line", Stream: "stderr", Line: "warning: detached HEAD", Seq: 2},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "[stdout] cloning commit abc123") ||
		!strings.Contains(output, "[stderr] warning: detached HEAD") {
		t.Errorf("output = %q, want the replayed frames printed like a live tail", output)
	}
	if !strings.Contains(output, "replaying the platform's retained log stream") {
		t.Errorf("output = %q, want it to say where the log came from", output)
	}
	if len(mockClient.streamOptions) != 1 {
		t.Fatalf("stream calls = %d, want exactly one replay", len(mockClient.streamOptions))
	}
	if mockClient.streamOptions[0].IsFollowing == nil || *mockClient.streamOptions[0].IsFollowing {
		t.Errorf("stream options = %+v, want follow=false so the replay ends when it is drained",
			mockClient.streamOptions[0])
	}
	if mockClient.streamStepID != "step-1" {
		t.Errorf("streamed step id = %q, want the resolved step's own id", mockClient.streamStepID)
	}
}

func TestPipelineLogsRetainedStreamWithNoOutputSaysSo(t *testing.T) {
	// A replay the platform ended with nothing in it is an answer, not a
	// silent exit - and it is stated as the stream's answer, because inside
	// the retention window "nothing retained" and "the step printed nothing"
	// are indistinguishable from the client.
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "held no output for step \"checkout\"") {
		t.Errorf("output = %q, want the drained replay reported as an empty stream", output)
	}
}

func TestPipelineLogsRetainedStreamFaultIsNotReportedAsEmpty(t *testing.T) {
	// A replay that ended on the relay's own error frame observed nothing
	// about the step's output, so it must not also be stated as an empty
	// stream - the fault is the answer.
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
		streamEvents: []client.PipelineLogEvent{
			{Type: "error", Error: "stream closed"},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "Log stream fault: stream closed") {
		t.Errorf("output = %q, want the relay's fault reported", output)
	}
	if strings.Contains(output, "held no output") {
		t.Errorf("output = %q, want a faulted read never stated as an observed empty one", output)
	}
}

func TestPipelineLogsConcludedStepWithoutArchiveNeedsAnExecution(t *testing.T) {
	// A step that never reached an execution has no subject on the log
	// stream either, so there is nothing to fall back to and the honest
	// answer is still that no log was recorded.
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStep()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "No archived log was recorded") {
		t.Errorf("output = %q", output)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none for a step that never ran", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsCappedArtifactReadDoesNotReplay(t *testing.T) {
	// A capped walk never observed the archive's absence, so it must not be
	// treated as one: the archive is still the better copy if it is sitting
	// on a page this command declined to fetch.
	endlessCursor := "cursor-next"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsPages: []client.PipelineArtifactList{
			{Artifacts: []client.PipelineArtifact{}, NextCursor: &endlessCursor},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "Stopped after") {
		t.Errorf("output = %q, want it to say the read was capped", output)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none after a capped read", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsRetainedStreamExpiredReadsAsNotFound(t *testing.T) {
	// The relay's 410: the frames aged out of the retention window. The
	// platform's own sentence is the whole message, and a log that is gone
	// exits like any other missing resource.
	detail := "This step's live output is no longer retained; its archived log needs a ready backup vault."
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
		streamError: &client.PipelineLogNoLongerRetainedError{
			Detail: detail, ErrorCode: "LOG_NO_LONGER_RETAINED",
		},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError == nil {
		t.Fatal("logs error = nil, want the platform's retention refusal")
	}
	if executeError.Error() != detail {
		t.Errorf("error = %q, want the platform's detail sentence verbatim", executeError.Error())
	}
	if exitCode := exitCodeFor(executeError); exitCode != exitNotFound {
		t.Errorf("exit code = %d, want %d", exitCode, exitNotFound)
	}
}

func TestPipelineLogsRetainedStreamIdleGuardStopsAnEndlessReplay(t *testing.T) {
	// A platform older than the replay contract ignores follow=false and
	// holds the connection open on keepalives forever. The command must
	// print what it did get, say the replay never ended, and return -
	// never hang.
	shortenPipelineLogReplayIdleTimeout(t)
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
		streamEvents: []client.PipelineLogEvent{
			{Type: "line", Stream: "stdout", Line: "the one line that arrived", Seq: 1},
		},
		streamNeverEnds: true,
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "[stdout] the one line that arrived") {
		t.Errorf("output = %q, want the frames that did arrive", output)
	}
	if !strings.Contains(output, "did not end step \"checkout\"'s log replay") {
		t.Errorf("output = %q, want one line saying the platform never ended the replay", output)
	}
}

func TestPipelineLogsRetainedStreamIdleGuardStopsASilentReplay(t *testing.T) {
	// The same guard with nothing at all on the wire: an old platform's
	// keepalives decode to no frames, so the timeout has to run from the
	// connection rather than from a first frame that never comes.
	shortenPipelineLogReplayIdleTimeout(t)
	mockClient := &pipelineLaneMock{
		getResult:       &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
		streamNeverEnds: true,
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "did not end step \"checkout\"'s log replay") {
		t.Errorf("output = %q, want the guard's line", output)
	}
}

func TestPipelineLogsArchivedLogThePlatformCannotFindFallsBackToTheStream(t *testing.T) {
	// The listing promises an uploaded step_log whose object the download
	// cannot find. Nothing was printed, so the retained stream can still
	// answer without showing anything twice.
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadError: &client.PipelineArtifactDownloadError{
			StatusCode: http.StatusNotFound,
			Underlying: errors.New("Pipeline artifact not found"),
		},
		streamEvents: []client.PipelineLogEvent{
			{Type: "line", Stream: "stdout", Line: "recovered from the stream", Seq: 1},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError != nil {
		t.Fatalf("logs error = %v", executeError)
	}
	if !strings.Contains(output, "[stdout] recovered from the stream") {
		t.Errorf("output = %q, want the retained stream's frames", output)
	}
	if len(mockClient.streamOptions) != 1 {
		t.Errorf("stream calls = %d, want one replay after the 404", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsArchivedLogThatFailedPartWayThroughIsNotReplayed(t *testing.T) {
	// A download that printed part of the log and then failed must be
	// reported, not silently topped up from a second source: replaying the
	// stream over it would show those lines twice.
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadPayload: "the first half of the log\n",
		downloadError: &client.PipelineArtifactDownloadError{
			StatusCode: http.StatusNotFound,
			Underlying: errors.New("Pipeline artifact not found"),
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError == nil || !strings.Contains(executeError.Error(), "Pipeline artifact not found") {
		t.Fatalf("error = %v, want the download's own refusal", executeError)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none once part of the log was printed", len(mockClient.streamOptions))
	}
	if !strings.Contains(output, "the first half of the log") {
		t.Errorf("output = %q, want what the download did print", output)
	}
}

func TestPipelineLogsArchivedLogRefusalOtherThanNotFoundIsReported(t *testing.T) {
	// A 409 describes a state the caller has to act on (the vault holding
	// the artifact is gone), so it is reported rather than papered over
	// with a stream read that answers a different question.
	stepID := "step-1"
	mockClient := &pipelineLaneMock{
		getResult: &client.PipelineRunDetail{Steps: []client.PipelineStep{concludedStepThatRan()}},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		}},
		downloadError: &client.PipelineArtifactDownloadError{
			StatusCode: http.StatusConflict,
			Underlying: errors.New("The backup vault holding this artifact is no longer available"),
		},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "checkout")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), "The backup vault holding this artifact is no longer available") {
		t.Fatalf("error = %v, want the 409 reported as it stands", executeError)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none for a refusal that is not a missing artifact",
			len(mockClient.streamOptions))
	}
}

func TestPipelineLogsFollowWaitsForABlockedStepThenStreams(t *testing.T) {
	// The moment a live tail is actually asked for: the run was just
	// dispatched, so the step is blocked on its dependencies and the relay
	// would refuse it. --follow waits through blocked and pending, says what
	// it is waiting on, and attaches as soon as the step starts.
	shortenPipelineStepStartWait(t, time.Minute)
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			runDetailWithStep("running", blockedStep()),
			runDetailWithStep("running", pendingStep()),
			runDetailWithStep("running", startedBuildStep()),
			runDetailWithStep("running", concludedBuildStep()),
		},
		streamEvents: []client.PipelineLogEvent{
			{Type: "line", Stream: "stdout", Line: "compiling", Seq: 1},
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError != nil {
		t.Fatalf("logs --follow error = %v", executeError)
	}
	if !strings.Contains(output, `Waiting for step "build" to start (blocked on: checkout).`) {
		t.Errorf("output = %q, want the blocked wait to name the dependency", output)
	}
	if !strings.Contains(output, `Waiting for step "build" to start (pending).`) {
		t.Errorf("output = %q, want the wait re-stated when the status changed", output)
	}
	if !strings.Contains(output, "[stdout] compiling") {
		t.Errorf("output = %q, want the step's output once it started", output)
	}
	if !strings.Contains(output, "Log stream ended: the step has concluded.") {
		t.Errorf("output = %q, want the tail to end on the step's conclusion", output)
	}
	if len(mockClient.streamOptions) != 1 {
		t.Errorf("stream calls = %d, want the relay opened once, after the wait", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsFollowWaitLineIsPrintedOncePerReason(t *testing.T) {
	// A step blocked for twenty minutes is polled hundreds of times; the
	// wait line is a status change, not a heartbeat, so an unchanged status
	// prints nothing. The same test covers the bound: a step that never
	// starts gives up with the refusal a bare 'logs' call gives immediately.
	shortenPipelineStepStartWait(t, 30*time.Millisecond)
	detail := runDetailWithStep("running", pendingStep())
	mockClient := &pipelineLaneMock{getResult: &detail}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), `step "build" has not started, so it has no log stream yet`) {
		t.Fatalf("error = %v, want the bounded wait to give up with the not-started refusal", executeError)
	}
	if exitCode := exitCodeFor(executeError); exitCode != 1 {
		t.Errorf("exit code = %d, want the same 1 a bare 'logs' call exits with", exitCode)
	}
	if count := strings.Count(output, "Waiting for step"); count != 1 {
		t.Errorf("wait lines = %d, want exactly one for an unchanging status", count)
	}
	if mockClient.getCalls < 2 {
		t.Errorf("run reads = %d, want the wait to have polled at least once", mockClient.getCalls)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none for a step that never started", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsWithoutFollowStillRefusesAStepThatHasNotStarted(t *testing.T) {
	// The one-shot read is unchanged: it says the step has not started and
	// returns, rather than silently blocking for half an hour.
	shortenPipelineStepStartWait(t, time.Minute)
	detail := runDetailWithStep("running", blockedStep())
	mockClient := &pipelineLaneMock{getResult: &detail}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build")
	if executeError == nil || executeError.Error() !=
		`step "build" has not started, so it has no log stream yet - check 'ankra pipeline get run-1' for its status` {
		t.Fatalf("error = %v, want today's refusal verbatim", executeError)
	}
	if mockClient.getCalls != 1 {
		t.Errorf("run reads = %d, want the single resolve read and no polling", mockClient.getCalls)
	}
	if strings.Contains(output, "Waiting for step") {
		t.Errorf("output = %q, want no wait announced without --follow", output)
	}
}

func TestPipelineLogsFollowReadsAStepThatConcludedWithoutStarting(t *testing.T) {
	// A dependency failed while the wait was running, so the step is skipped
	// and will never produce a line. The wait says why it ended and then
	// hands the step to the concluded-step path, which reports the log it
	// does (not) have rather than leaving the outcome unexplained.
	shortenPipelineStepStartWait(t, time.Minute)
	outcome := "skipped"
	errorMessage := `Dependency "checkout" did not succeed.`
	skipped := blockedStep()
	skipped.Status = pipelineStepStatusConcluded
	skipped.Outcome = &outcome
	skipped.ErrorMessage = &errorMessage
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			runDetailWithStep("running", pendingStep()),
			runDetailWithStep(pipelineRunStatusConcluded, skipped),
		},
		artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError != nil {
		t.Fatalf("logs --follow error = %v", executeError)
	}
	if !strings.Contains(output, `Step "build" concluded while waiting for it to start: skipped`) {
		t.Errorf("output = %q, want the step's outcome stated", output)
	}
	if !strings.Contains(output, errorMessage) {
		t.Errorf("output = %q, want the platform's own error message", output)
	}
	if !strings.Contains(output, `No archived log was recorded for step "build"`) {
		t.Errorf("output = %q, want the concluded-step path to answer for the log", output)
	}
	if len(mockClient.streamOptions) != 0 {
		t.Errorf("stream calls = %d, want none for a step that never reached an execution",
			len(mockClient.streamOptions))
	}
}

func TestPipelineLogsFollowStopsWhenTheRunConcludesWithoutTheStep(t *testing.T) {
	// A run can conclude leaving a step it never dispatched behind (its
	// stage was cancelled, the run was stopped). Waiting longer cannot
	// produce a log, so the wait ends there and says so.
	shortenPipelineStepStartWait(t, time.Minute)
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			runDetailWithStep("running", pendingStep()),
			runDetailWithStep(pipelineRunStatusConcluded, pendingStep()),
		},
	}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), `run run-1 concluded without starting step "build"`) {
		t.Fatalf("error = %v, want the wait to stop on the run's own conclusion", executeError)
	}
	if exitCode := exitCodeFor(executeError); exitCode != exitNotFound {
		t.Errorf("exit code = %d, want %d", exitCode, exitNotFound)
	}
}

func TestPipelineLogsFollowWaitsAgainWhenAStepIsReplannedMidTail(t *testing.T) {
	// A re-dispatch replans a step that was already running, and the relay
	// answers the replanned step the same "has not started" 404 it answers
	// one that never ran. --follow reads the step's status rather than that
	// sentence, waits for it again, and only reports the stream's error once
	// the step is back to having one.
	shortenPipelineStepStartWait(t, time.Minute)
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			runDetailWithStep("running", startedBuildStep()),
			runDetailWithStep("running", pendingStep()),
			runDetailWithStep("running", startedBuildStep()),
		},
		streamError: errors.New("This step has not started, so it has no log stream yet"),
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), "This step has not started") {
		t.Fatalf("error = %v, want the relay's own refusal once the step has a stream again", executeError)
	}
	if !strings.Contains(output, `Waiting for step "build" to start (pending).`) {
		t.Errorf("output = %q, want the replanned step waited for rather than reported as failed", output)
	}
	if len(mockClient.streamOptions) != 2 {
		t.Errorf("stream calls = %d, want the relay retried once after the wait", len(mockClient.streamOptions))
	}
}

func TestPipelineLogsFollowDoesNotWaitOnAStatusItDoesNotKnow(t *testing.T) {
	// The wait enumerates the states the scheduler moves a step out of, so a
	// status added to the platform after this build - as likely to be
	// terminal as pre-dispatch - reads as not started at once rather than
	// costing the whole 30-minute bound.
	shortenPipelineStepStartWait(t, time.Minute)
	unknown := blockedStep()
	unknown.Status = "quarantined"
	detail := runDetailWithStep("running", unknown)
	mockClient := &pipelineLaneMock{getResult: &detail}
	_, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), `step "build" has not started, so it has no log stream yet`) {
		t.Fatalf("error = %v, want the not-started refusal without a wait", executeError)
	}
	if mockClient.getCalls != 1 {
		t.Errorf("run reads = %d, want the single resolve read and no polling", mockClient.getCalls)
	}
}

func TestPipelineLogsFollowStopsWhenAWaitedStepLeavesTheStatesItWaitsIn(t *testing.T) {
	// The same guard inside the wait: a step that was pending and is now in a
	// state this build does not wait in stops the poll rather than running it
	// out to the bound.
	shortenPipelineStepStartWait(t, time.Minute)
	unknown := blockedStep()
	unknown.Status = "quarantined"
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			runDetailWithStep("running", pendingStep()),
			runDetailWithStep("running", unknown),
		},
	}
	output, executeError := runPipelineCommand(t, mockClient, "logs", "run-1",
		"--application", testApplicationID, "--step", "build", "--follow")
	if executeError == nil ||
		!strings.Contains(executeError.Error(), `step "build" has not started, so it has no log stream yet`) {
		t.Fatalf("error = %v, want the wait to stop on a state it does not sit in", executeError)
	}
	if !strings.Contains(output, `Waiting for step "build" to start (pending).`) {
		t.Errorf("output = %q, want the wait to have started before it gave up", output)
	}
	if mockClient.getCalls != 2 {
		t.Errorf("run reads = %d, want the resolve read plus one poll", mockClient.getCalls)
	}
}
