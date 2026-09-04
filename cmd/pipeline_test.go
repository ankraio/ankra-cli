package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// pipelineLaneMock stubs the pipeline surface of APIClient with call
// tracking, so a test can assert both what was returned and what the CLI
// asked for.
type pipelineLaneMock struct {
	baseMock

	lastSelector client.PipelineSelector

	listOptions client.ListPipelineRunsOptions
	listResult  *client.PipelineRunList
	listError   error

	createRequest client.CreatePipelineRunRequest
	createResult  *client.CreatePipelineRunResult
	createError   error
	createCalls   int

	getRunID  string
	getResult *client.PipelineRunDetail
	getError  error

	cancelRunID  string
	cancelResult *client.PipelineRun
	cancelError  error

	rerunRunID      string
	rerunFailedOnly bool
	rerunResult     *client.CreatePipelineRunResult
	rerunError      error

	artifactsRunID  string
	artifactsResult *client.PipelineArtifactList
	artifactsError  error

	downloadArtifactID string
	downloadError      error
	downloadPayload    string

	definitionResult *client.PipelineDefinition
	definitionError  error
	putSpecYAML      string

	validateSpecYAML string
	validateResult   *client.PipelineValidation
	validateError    error

	getApprovalDefinitionID string
	getApprovalResult       *client.PipelineDefinitionApproval
	getApprovalError        error

	approveDefinitionID string
	approveResult       *client.PipelineDefinitionApproval
	approveError        error
	approveCalls        int

	schedulesResult *client.PipelineScheduleList
	schedulesError  error

	createScheduleRequest client.CreatePipelineScheduleRequest
	createScheduleResult  *client.PipelineSchedule
	createScheduleError   error

	updateScheduleID      string
	updateScheduleRequest client.UpdatePipelineScheduleRequest
	updateScheduleResult  *client.PipelineSchedule
	updateScheduleError   error

	deleteScheduleID    string
	deleteScheduleError error
}

func (mock *pipelineLaneMock) ListPipelineRuns(ctx context.Context, selector client.PipelineSelector, options client.ListPipelineRunsOptions) (*client.PipelineRunList, error) {
	mock.lastSelector = selector
	mock.listOptions = options
	if mock.listError != nil {
		return nil, mock.listError
	}
	return mock.listResult, nil
}

func (mock *pipelineLaneMock) CreatePipelineRun(ctx context.Context, selector client.PipelineSelector, request client.CreatePipelineRunRequest) (*client.CreatePipelineRunResult, error) {
	mock.lastSelector = selector
	mock.createRequest = request
	mock.createCalls++
	if mock.createError != nil {
		return nil, mock.createError
	}
	return mock.createResult, nil
}

func (mock *pipelineLaneMock) GetPipelineRun(ctx context.Context, selector client.PipelineSelector, runID string) (*client.PipelineRunDetail, error) {
	mock.lastSelector = selector
	mock.getRunID = runID
	if mock.getError != nil {
		return nil, mock.getError
	}
	return mock.getResult, nil
}

func (mock *pipelineLaneMock) RerunPipelineRun(ctx context.Context, selector client.PipelineSelector, runID string, failedOnly bool) (*client.CreatePipelineRunResult, error) {
	mock.lastSelector = selector
	mock.rerunRunID = runID
	mock.rerunFailedOnly = failedOnly
	if mock.rerunError != nil {
		return nil, mock.rerunError
	}
	return mock.rerunResult, nil
}

func (mock *pipelineLaneMock) CancelPipelineRun(ctx context.Context, selector client.PipelineSelector, runID string) (*client.PipelineRun, error) {
	mock.lastSelector = selector
	mock.cancelRunID = runID
	if mock.cancelError != nil {
		return nil, mock.cancelError
	}
	return mock.cancelResult, nil
}

func (mock *pipelineLaneMock) StreamPipelineStepLogs(ctx context.Context, selector client.PipelineSelector, runID string, stepID string, fromSequence int64) (<-chan client.PipelineLogEvent, error) {
	events := make(chan client.PipelineLogEvent)
	close(events)
	return events, nil
}

func (mock *pipelineLaneMock) ListPipelineArtifacts(ctx context.Context, selector client.PipelineSelector, runID string) (*client.PipelineArtifactList, error) {
	mock.lastSelector = selector
	mock.artifactsRunID = runID
	if mock.artifactsError != nil {
		return nil, mock.artifactsError
	}
	return mock.artifactsResult, nil
}

func (mock *pipelineLaneMock) DownloadPipelineArtifact(ctx context.Context, selector client.PipelineSelector, artifactID string, destination io.Writer) error {
	mock.lastSelector = selector
	mock.downloadArtifactID = artifactID
	if mock.downloadError != nil {
		return mock.downloadError
	}
	_, writeError := destination.Write([]byte(mock.downloadPayload))
	return writeError
}

func (mock *pipelineLaneMock) GetPipelineDefinition(ctx context.Context, selector client.PipelineSelector) (*client.PipelineDefinition, error) {
	mock.lastSelector = selector
	if mock.definitionError != nil {
		return nil, mock.definitionError
	}
	return mock.definitionResult, nil
}

func (mock *pipelineLaneMock) PutPipelineDefinition(ctx context.Context, selector client.PipelineSelector, specYAML string) (*client.PipelineDefinition, error) {
	mock.lastSelector = selector
	mock.putSpecYAML = specYAML
	if mock.definitionError != nil {
		return nil, mock.definitionError
	}
	return mock.definitionResult, nil
}

func (mock *pipelineLaneMock) GetPipelineDefinitionApproval(ctx context.Context, definitionID string) (*client.PipelineDefinitionApproval, error) {
	mock.getApprovalDefinitionID = definitionID
	if mock.getApprovalError != nil {
		return nil, mock.getApprovalError
	}
	return mock.getApprovalResult, nil
}

func (mock *pipelineLaneMock) ApprovePipelineDefinition(ctx context.Context, definitionID string) (*client.PipelineDefinitionApproval, error) {
	mock.approveDefinitionID = definitionID
	mock.approveCalls++
	if mock.approveError != nil {
		return nil, mock.approveError
	}
	return mock.approveResult, nil
}

func (mock *pipelineLaneMock) ValidatePipelineDefinition(ctx context.Context, selector client.PipelineSelector, specYAML string) (*client.PipelineValidation, error) {
	mock.lastSelector = selector
	mock.validateSpecYAML = specYAML
	if mock.validateError != nil {
		return nil, mock.validateError
	}
	return mock.validateResult, nil
}

func (mock *pipelineLaneMock) ListPipelineSchedules(ctx context.Context, selector client.PipelineSelector) (*client.PipelineScheduleList, error) {
	mock.lastSelector = selector
	if mock.schedulesError != nil {
		return nil, mock.schedulesError
	}
	return mock.schedulesResult, nil
}

func (mock *pipelineLaneMock) CreatePipelineSchedule(ctx context.Context, selector client.PipelineSelector, request client.CreatePipelineScheduleRequest) (*client.PipelineSchedule, error) {
	mock.lastSelector = selector
	mock.createScheduleRequest = request
	if mock.createScheduleError != nil {
		return nil, mock.createScheduleError
	}
	return mock.createScheduleResult, nil
}

func (mock *pipelineLaneMock) UpdatePipelineSchedule(ctx context.Context, selector client.PipelineSelector, scheduleID string, request client.UpdatePipelineScheduleRequest) (*client.PipelineSchedule, error) {
	mock.lastSelector = selector
	mock.updateScheduleID = scheduleID
	mock.updateScheduleRequest = request
	if mock.updateScheduleError != nil {
		return nil, mock.updateScheduleError
	}
	return mock.updateScheduleResult, nil
}

func (mock *pipelineLaneMock) DeletePipelineSchedule(ctx context.Context, selector client.PipelineSelector, scheduleID string) error {
	mock.lastSelector = selector
	mock.deleteScheduleID = scheduleID
	return mock.deleteScheduleError
}

func runPipelineCommand(t *testing.T, mockClient APIClient, arguments ...string) (string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	var output bytes.Buffer
	pipelineCommand.SetOut(&output)
	pipelineCommand.SetErr(&output)
	pipelineCommand.SetArgs(arguments)
	executeError := pipelineCommand.Execute()
	return output.String(), executeError
}

func TestPipelineCommandsRegistered(t *testing.T) {
	pipelineCommand := newPipelineCommand()
	registered := map[string]bool{}
	for _, subcommand := range pipelineCommand.Commands() {
		registered[subcommand.Name()] = true
	}
	for _, expected := range []string{
		"run", "list", "get", "cancel", "rerun", "logs", "artifacts", "validate", "definition", "definitions", "schedules",
	} {
		if !registered[expected] {
			t.Errorf("pipeline subcommand %q is not registered", expected)
		}
	}

	artifactsCommand := findSubcommand(t, pipelineCommand, "artifacts")
	if findSubcommandOrNil(artifactsCommand, "download") == nil {
		t.Error("pipeline artifacts subcommand \"download\" is not registered")
	}
	definitionCommand := findSubcommand(t, pipelineCommand, "definition")
	for _, expected := range []string{"get", "put"} {
		if findSubcommandOrNil(definitionCommand, expected) == nil {
			t.Errorf("pipeline definition subcommand %q is not registered", expected)
		}
	}
	definitionsCommand := findSubcommand(t, pipelineCommand, "definitions")
	for _, expected := range []string{"get", "approve"} {
		if findSubcommandOrNil(definitionsCommand, expected) == nil {
			t.Errorf("pipeline definitions subcommand %q is not registered", expected)
		}
	}
	schedulesCommand := findSubcommand(t, pipelineCommand, "schedules")
	for _, expected := range []string{"list", "create", "update", "delete"} {
		if findSubcommandOrNil(schedulesCommand, expected) == nil {
			t.Errorf("pipeline schedules subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationPipelineCommandsRegistered(t *testing.T) {
	applicationPipelineCommand := findSubcommand(t, newApplicationCommand(), "pipeline")
	for _, expected := range []string{
		"run", "list", "get", "cancel", "rerun", "logs", "artifacts", "validate", "definition", "schedules",
	} {
		if findSubcommandOrNil(applicationPipelineCommand, expected) == nil {
			t.Errorf("application pipeline subcommand %q is not registered", expected)
		}
	}
}

func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	found := findSubcommandOrNil(parent, name)
	if found == nil {
		t.Fatalf("subcommand %q is not registered under %q", name, parent.Use)
	}
	return found
}

func findSubcommandOrNil(parent *cobra.Command, name string) *cobra.Command {
	if parent == nil {
		return nil
	}
	for _, subcommand := range parent.Commands() {
		if subcommand.Name() == name {
			return subcommand
		}
	}
	return nil
}

func TestPipelineSelectorRequiresExactlyOne(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "list")
	if executeError == nil {
		t.Fatal("expected an error when neither --application nor --repository is given")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}

	_, executeError = runPipelineCommand(t, mockClient, "list", "--application", testApplicationID, "--repository", "repo-1")
	if executeError == nil {
		t.Fatal("expected an error when both --application and --repository are given")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}

func TestPipelineSelectorRepositoryMustBeAnID(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "list", "--repository", "acme/webapp")
	if executeError == nil {
		t.Fatal("expected --repository owner/name to be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if !strings.Contains(executeError.Error(), "there is no lookup by owner/name") {
		t.Errorf("error = %q, want it to explain the gap", executeError.Error())
	}
}

func TestPipelineListEmpty(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{}}}
	output, executeError := runPipelineCommand(t, mockClient, "list", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, "No pipeline runs found") {
		t.Errorf("output = %q", output)
	}
	if mockClient.lastSelector.ApplicationID != testApplicationID {
		t.Errorf("selector = %+v", mockClient.lastSelector)
	}
}

func TestPipelineListHappyPathRendersTable(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{
		{ID: "run-1", RunNumber: 12, Status: "concluded", Outcome: strPipelinePtr("success"),
			Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("a", 40),
			QueuedAt: "2026-09-01T00:00:00Z"},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "list", "--application", testApplicationID, "--status", "concluded", "--limit", "5")
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, "run-1") || !strings.Contains(output, "12") {
		t.Errorf("output = %q", output)
	}
	if mockClient.listOptions.Status != "concluded" || mockClient.listOptions.Limit != 5 {
		t.Errorf("list options = %+v", mockClient.listOptions)
	}
}

func TestPipelineGetNotFound(t *testing.T) {
	mockClient := &pipelineLaneMock{getError: errors.New("Pipeline run not found")}
	_, executeError := runPipelineCommand(t, mockClient, "get", "missing-run", "--application", testApplicationID)
	if executeError == nil || executeError.Error() != "Pipeline run not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineRunRequiresSHA(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "run", "--application", testApplicationID, "--ref", "main")
	if executeError == nil {
		t.Fatal("expected --sha to be required")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.createCalls != 0 {
		t.Errorf("CreatePipelineRun calls = %d, want 0", mockClient.createCalls)
	}
}

func TestPipelineRunPassesInputsAndSelector(t *testing.T) {
	mockClient := &pipelineLaneMock{createResult: &client.CreatePipelineRunResult{
		RunID: "umbrella-1", PipelineRunID: "run-1", RunNumber: 7,
	}}
	sha := strings.Repeat("b", 40)
	output, executeError := runPipelineCommand(t, mockClient, "run",
		"--application", testApplicationID, "--sha", sha, "--input", "env=staging", "--input", "replicas=3")
	if executeError != nil {
		t.Fatalf("run error = %v", executeError)
	}
	if mockClient.createRequest.HeadSHA != sha {
		t.Errorf("head sha = %q", mockClient.createRequest.HeadSHA)
	}
	if mockClient.createRequest.Inputs["env"] != "staging" || mockClient.createRequest.Inputs["replicas"] != "3" {
		t.Errorf("inputs = %+v", mockClient.createRequest.Inputs)
	}
	if !strings.Contains(output, "Run #7 queued") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineRunPlanRefusalCarriesDiagnostics(t *testing.T) {
	mockClient := &pipelineLaneMock{createError: &client.PipelineValidationError{
		Reason:      "This pipeline definition has at least one fatal violation",
		Diagnostics: []string{"pipeline_stages: stage \"build\" has no kind"},
	}}
	sha := strings.Repeat("c", 40)
	_, executeError := runPipelineCommand(t, mockClient, "run", "--application", testApplicationID, "--sha", sha)
	if executeError == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(executeError.Error(), "fatal violation") || !strings.Contains(executeError.Error(), "has no kind") {
		t.Errorf("error = %q, want the reason and the diagnostic both rendered", executeError.Error())
	}
}

func TestPipelineCancelRendersOutcome(t *testing.T) {
	mockClient := &pipelineLaneMock{cancelResult: &client.PipelineRun{ID: "run-1", RunNumber: 4, Status: "concluded"}}
	output, executeError := runPipelineCommand(t, mockClient, "cancel", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("cancel error = %v", executeError)
	}
	if mockClient.cancelRunID != "run-1" {
		t.Errorf("cancel run id = %q", mockClient.cancelRunID)
	}
	if !strings.Contains(output, "Run #4 cancelled") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineCancelAlreadyConcluded(t *testing.T) {
	mockClient := &pipelineLaneMock{cancelError: errors.New("This pipeline run has already concluded")}
	_, executeError := runPipelineCommand(t, mockClient, "cancel", "run-1", "--application", testApplicationID)
	if executeError == nil || executeError.Error() != "This pipeline run has already concluded" {
		t.Fatalf("error = %v", executeError)
	}
}

func TestPipelineRerunPassesFailedOnly(t *testing.T) {
	mockClient := &pipelineLaneMock{rerunResult: &client.CreatePipelineRunResult{PipelineRunID: "run-2", RunNumber: 8}}
	_, executeError := runPipelineCommand(t, mockClient, "rerun", "run-1", "--application", testApplicationID, "--failed-only")
	if executeError != nil {
		t.Fatalf("rerun error = %v", executeError)
	}
	if !mockClient.rerunFailedOnly {
		t.Error("failed-only was not passed through")
	}
	if mockClient.rerunRunID != "run-1" {
		t.Errorf("rerun run id = %q", mockClient.rerunRunID)
	}
}

func TestPipelineArtifactsListEmpty(t *testing.T) {
	mockClient := &pipelineLaneMock{artifactsResult: &client.PipelineArtifactList{Artifacts: []client.PipelineArtifact{}}}
	output, executeError := runPipelineCommand(t, mockClient, "artifacts", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("artifacts error = %v", executeError)
	}
	if !strings.Contains(output, "No artifacts stored") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineArtifactsDownloadWritesFile(t *testing.T) {
	mockClient := &pipelineLaneMock{downloadPayload: "binary-content"}
	outputPath := t.TempDir() + "/artifact.bin"
	_, executeError := runPipelineCommand(t, mockClient, "artifacts", "download", "artifact-1",
		"--application", testApplicationID, "--out", outputPath)
	if executeError != nil {
		t.Fatalf("download error = %v", executeError)
	}
	if mockClient.downloadArtifactID != "artifact-1" {
		t.Errorf("artifact id = %q", mockClient.downloadArtifactID)
	}
}

func TestPipelineValidateFatalExitsNonZero(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{
		Severity:   "fatal",
		Violations: []string{"pipeline_stages: at least one stage is required"},
		Events:     []client.PipelineEventPlan{},
	}}
	_, executeError := runPipelineCommand(t, mockClient, "validate", "--application", testApplicationID)
	if executeError == nil {
		t.Fatal("expected a fatal validation to exit non-zero")
	}
}

func TestPipelineValidateOKPassesThroughFileContent(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{Severity: "ok", Events: []client.PipelineEventPlan{}}}
	fixturePath := t.TempDir() + "/pipeline.yaml"
	if writeError := os.WriteFile(fixturePath, []byte("apiVersion: ankra.io/v1\nkind: Pipeline\n"), 0o600); writeError != nil {
		t.Fatalf("writing fixture: %v", writeError)
	}
	_, executeError := runPipelineCommand(t, mockClient, "validate", fixturePath, "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("validate error = %v", executeError)
	}
	if mockClient.validateSpecYAML != "apiVersion: ankra.io/v1\nkind: Pipeline\n" {
		t.Errorf("spec yaml = %q", mockClient.validateSpecYAML)
	}
}

func TestPipelineSchedulesUpdateRequiresAChange(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "schedules", "update", "sched-1", "--application", testApplicationID)
	if executeError == nil {
		t.Fatal("expected a bare update with no flags to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}

func TestPipelineSchedulesUpdateEnabledDisabledMutuallyExclusive(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "schedules", "update", "sched-1",
		"--application", testApplicationID, "--enabled", "--disabled")
	if executeError == nil {
		t.Fatal("expected --enabled and --disabled together to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}

func TestPipelineSchedulesUpdateOnlyChangedField(t *testing.T) {
	mockClient := &pipelineLaneMock{updateScheduleResult: &client.PipelineSchedule{ID: "sched-1", Enabled: false}}
	_, executeError := runPipelineCommand(t, mockClient, "schedules", "update", "sched-1",
		"--application", testApplicationID, "--disabled")
	if executeError != nil {
		t.Fatalf("update error = %v", executeError)
	}
	if mockClient.updateScheduleRequest.Cron != nil || mockClient.updateScheduleRequest.Ref != nil {
		t.Errorf("request = %+v, only Enabled should be set", mockClient.updateScheduleRequest)
	}
	if mockClient.updateScheduleRequest.Enabled == nil || *mockClient.updateScheduleRequest.Enabled {
		t.Errorf("enabled = %v, want a pointer to false", mockClient.updateScheduleRequest.Enabled)
	}
}

func TestPipelineSchedulesUpdateReadsTheFlagValueNotItsPresence(t *testing.T) {
	for _, testCase := range []struct {
		argument string
		wanted   bool
	}{
		{"--enabled=false", false},
		{"--enabled=true", true},
		{"--disabled=false", true},
		{"--disabled=true", false},
	} {
		mockClient := &pipelineLaneMock{updateScheduleResult: &client.PipelineSchedule{ID: "sched-1"}}
		_, executeError := runPipelineCommand(t, mockClient, "schedules", "update", "sched-1",
			"--application", testApplicationID, testCase.argument)
		if executeError != nil {
			t.Fatalf("%s: update error = %v", testCase.argument, executeError)
		}
		if mockClient.updateScheduleRequest.Enabled == nil ||
			*mockClient.updateScheduleRequest.Enabled != testCase.wanted {
			t.Errorf("%s: enabled = %v, want a pointer to %v", testCase.argument,
				mockClient.updateScheduleRequest.Enabled, testCase.wanted)
		}
	}
}

func TestPipelineArtifactsDownloadRefusesAPathOutsideTheDirectory(t *testing.T) {
	for _, outside := range []string{"../escaped", "../../etc/passwd"} {
		mockClient := &pipelineLaneMock{}
		_, executeError := runPipelineCommand(t, mockClient, "artifacts", "download", "artifact-1",
			"--application", testApplicationID, "--out", outside)
		if executeError == nil {
			t.Fatalf("--out %q was accepted; a download must not write outside the directory", outside)
		}
	}
}

func TestPipelineArtifactsDownloadUsesOnlyTheBaseNameOfAServerChosenID(t *testing.T) {
	for _, hostile := range []string{"sub/dir", "/etc/passwd", "../escaped"} {
		mockClient := &pipelineLaneMock{}
		_, executeError := runPipelineCommand(t, mockClient, "artifacts", "download", hostile,
			"--application", testApplicationID)
		if executeError != nil {
			// A refusal is fine; what must never happen is a write outside
			// the working directory, which the base-name reduction prevents.
			continue
		}
		if _, statError := os.Stat(filepath.Base(filepath.Clean(hostile))); statError != nil {
			t.Fatalf("%q: expected the download to land on its base name, got %v", hostile, statError)
		}
		_ = os.Remove(filepath.Base(filepath.Clean(hostile)))
	}
}

func TestPipelineRunConclusionErrorNamesAnAbsentOutcomeAsAbsent(t *testing.T) {
	absent := pipelineRunConclusionError(client.PipelineRun{RunNumber: 7})
	if absent == nil || !strings.Contains(absent.Error(), "without recording an outcome") {
		t.Fatalf("a run that concluded with no outcome must say so, got %v", absent)
	}
	failed := "failure"
	named := pipelineRunConclusionError(client.PipelineRun{RunNumber: 8, Outcome: &failed})
	if named == nil || !strings.Contains(named.Error(), "concluded failure") {
		t.Fatalf("a named outcome is reported verbatim, got %v", named)
	}
	succeeded := "success"
	if ok := pipelineRunConclusionError(client.PipelineRun{RunNumber: 9, Outcome: &succeeded}); ok != nil {
		t.Fatalf("a successful run is not an error, got %v", ok)
	}
}

func TestPipelineSchedulesDeleteConfirms(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	var output bytes.Buffer
	pipelineCommand.SetOut(&output)
	pipelineCommand.SetErr(&output)
	pipelineCommand.SetIn(strings.NewReader("n\n"))
	pipelineCommand.SetArgs([]string{"schedules", "delete", "sched-1", "--application", testApplicationID})
	executeError := pipelineCommand.Execute()
	if executeError == nil || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("declined delete error = %v", executeError)
	}
	if mockClient.deleteScheduleID != "" {
		t.Errorf("DeletePipelineSchedule was called despite the decline")
	}
}

// TestApplicationPipelineAliasesForceTheApplicationSelector pins that
// `application pipeline <verb> <application-id> ...` calls the exact same
// runPipeline* function as `pipeline <verb> --application <application-id>`,
// by asserting the selector the mock observed.
func TestApplicationPipelineAliasesForceTheApplicationSelector(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{}}}
	_, executeError := runApplicationCommand(t, mockClient, "pipeline", "list", testApplicationID)
	if executeError != nil {
		t.Fatalf("application pipeline list error = %v", executeError)
	}
	if mockClient.lastSelector.ApplicationID != testApplicationID || mockClient.lastSelector.RepositoryID != "" {
		t.Errorf("selector = %+v", mockClient.lastSelector)
	}
}

func TestPipelineDefinitionsGetRendersApprovalState(t *testing.T) {
	mockClient := &pipelineLaneMock{getApprovalResult: &client.PipelineDefinitionApproval{
		DefinitionID:  "def-1",
		ProtectedHash: strings.Repeat("a", 64),
		ApprovedHash:  strings.Repeat("a", 64),
		ApprovedBy:    "user-1",
		ApprovedAt:    strPipelinePtr("2026-09-01T00:00:00Z"),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "def-1")
	if executeError != nil {
		t.Fatalf("definitions get error = %v", executeError)
	}
	if mockClient.getApprovalDefinitionID != "def-1" {
		t.Errorf("definition id = %q", mockClient.getApprovalDefinitionID)
	}
	if !strings.Contains(output, "def-1") || !strings.Contains(output, "user-1") {
		t.Errorf("output = %q", output)
	}
	if !strings.Contains(output, "Approved: this definition's protected sections are trusted authority") {
		t.Errorf("output = %q, want the approved summary line", output)
	}
}

// TestPipelineDefinitionsGetRendersNotApproved pins the "not approved" branch
// - an empty ApprovedHash - and that the hint names this exact id, since a
// person reading it is about to copy the command it prints.
func TestPipelineDefinitionsGetRendersNotApproved(t *testing.T) {
	mockClient := &pipelineLaneMock{getApprovalResult: &client.PipelineDefinitionApproval{
		DefinitionID:  "def-2",
		ProtectedHash: strings.Repeat("b", 64),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "def-2")
	if executeError != nil {
		t.Fatalf("definitions get error = %v", executeError)
	}
	if !strings.Contains(output, "Not approved") || !strings.Contains(output, "ankra pipeline definitions approve def-2") {
		t.Errorf("output = %q, want the not-approved hint naming the id", output)
	}
}

// TestPipelineDefinitionsGetRendersUnassessed pins the third branch - a
// definition recorded before the writer stamped protected hashes
// (ankra-vn0bd.10.8), so ProtectedHash itself is "". That definition can
// never be approved (the server refuses it as a 409), so the output must say
// so rather than printing the ordinary "not approved" hint that implies
// approving it would work.
func TestPipelineDefinitionsGetRendersUnassessed(t *testing.T) {
	mockClient := &pipelineLaneMock{getApprovalResult: &client.PipelineDefinitionApproval{DefinitionID: "def-3"}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "def-3")
	if executeError != nil {
		t.Fatalf("definitions get error = %v", executeError)
	}
	if !strings.Contains(output, "have not been assessed yet") {
		t.Errorf("output = %q, want the unassessed explanation", output)
	}
	if strings.Contains(output, "Not approved: run") {
		t.Errorf("output = %q, must not offer the ordinary approve hint for a definition that cannot be approved", output)
	}
}

func TestPipelineDefinitionsGetJSON(t *testing.T) {
	mockClient := &pipelineLaneMock{getApprovalResult: &client.PipelineDefinitionApproval{
		DefinitionID: "def-1", ProtectedHash: strings.Repeat("a", 64),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "def-1", "-o", "json")
	if executeError != nil {
		t.Fatalf("definitions get -o json error = %v", executeError)
	}
	if !strings.Contains(output, `"definition_id": "def-1"`) {
		t.Errorf("output = %q, want the JSON envelope", output)
	}
}

func TestPipelineDefinitionsGetNotFound(t *testing.T) {
	mockClient := &pipelineLaneMock{getApprovalError: errors.New("Pipeline definition not found")}
	_, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "missing")
	if executeError == nil || executeError.Error() != "Pipeline definition not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineDefinitionsApproveHappyPath(t *testing.T) {
	mockClient := &pipelineLaneMock{approveResult: &client.PipelineDefinitionApproval{
		DefinitionID: "def-1", ProtectedHash: strings.Repeat("a", 64), ApprovedHash: strings.Repeat("a", 64),
		ApprovedBy: "user-1", ApprovedAt: strPipelinePtr("2026-09-01T00:00:00Z"),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "approve", "def-1", "--yes")
	if executeError != nil {
		t.Fatalf("definitions approve error = %v", executeError)
	}
	if mockClient.approveDefinitionID != "def-1" || mockClient.approveCalls != 1 {
		t.Errorf("approve calls = %d, id = %q", mockClient.approveCalls, mockClient.approveDefinitionID)
	}
	if !strings.Contains(output, "Approved pipeline definition def-1") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineDefinitionsApproveJSON(t *testing.T) {
	mockClient := &pipelineLaneMock{approveResult: &client.PipelineDefinitionApproval{DefinitionID: "def-1"}}
	output, executeError := runPipelineCommand(t, mockClient, "definitions", "approve", "def-1", "-o", "json", "--yes")
	if executeError != nil {
		t.Fatalf("definitions approve -o json error = %v", executeError)
	}
	if !strings.Contains(output, `"definition_id": "def-1"`) {
		t.Errorf("output = %q, want the JSON envelope", output)
	}
}

// TestPipelineDefinitionsApproveErrorsAreVerbatimAndExitNonZero pins that the
// 404/409/403 the server can answer reach the caller unrewritten and exit
// non-zero - the command layer must not reinterpret what the client already
// surfaced verbatim (internal/client's own tests pin the wire shape each maps
// from).
func TestPipelineDefinitionsApproveErrorsAreVerbatimAndExitNonZero(t *testing.T) {
	for _, sentinel := range []string{
		"Pipeline definition not found",
		"Only the repository's current default-branch definition can be approved",
		"This pipeline definition is already approved",
		"A pipeline definition's authority can only be approved by a human administrator",
	} {
		t.Run(sentinel, func(t *testing.T) {
			mockClient := &pipelineLaneMock{approveError: errors.New(sentinel)}
			_, executeError := runPipelineCommand(t, mockClient, "definitions", "approve", "def-1", "--yes")
			if executeError == nil || executeError.Error() != sentinel {
				t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
			}
			if exitCodeFor(executeError) == exitOK {
				t.Errorf("exit code = %d, want non-zero", exitCodeFor(executeError))
			}
		})
	}
}

// TestPipelineDefinitionsApproveConfirms pins that approving - which grants
// whatever permissions, credentials and network access the definition's
// protected sections declare - confirms first like every other privileged or
// destructive command in this CLI, and that declining never reaches the API
// (ankra-platform[bot] review on #231).
func TestPipelineDefinitionsApproveConfirms(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	var output bytes.Buffer
	pipelineCommand.SetOut(&output)
	pipelineCommand.SetErr(&output)
	pipelineCommand.SetIn(strings.NewReader("n\n"))
	pipelineCommand.SetArgs([]string{"definitions", "approve", "def-1"})
	executeError := pipelineCommand.Execute()
	if executeError == nil || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("declined approve error = %v", executeError)
	}
	if mockClient.approveCalls != 0 {
		t.Errorf("ApprovePipelineDefinition was called despite the decline, calls = %d", mockClient.approveCalls)
	}
}

// TestPipelineGetRendersAuthorityWhenRecorded pins that 'pipeline get' shows
// a run's recorded authority state and, for a state other than "approved",
// a note that an administrator's approval would change it.
//
// It deliberately does NOT assert an
// "ankra pipeline definitions approve <id>" command naming
// AuthorityDefinitionID: that field is the definition the run's CURRENTLY
// TRUSTED authority came from - already approved, or empty - never the
// definition a non-approved run is waiting on, so turning it into an approve
// command would print a command that 409s (ankra-platform[bot] review on
// #231: the original version of this test pinned exactly that mistake).
func TestPipelineGetRendersAuthorityWhenRecorded(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-1", RunNumber: 9, Status: "concluded", Outcome: strPipelinePtr("success"),
		Trigger: "pull_request", TriggerRef: "refs/heads/feature", HeadSHA: strings.Repeat("a", 40),
		QueuedAt:              "2026-09-01T00:00:00Z",
		AuthorityState:        strPipelinePtr("changed_on_head"),
		AuthorityDefinitionID: strPipelinePtr("def-1"),
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "Authority: changed_on_head") {
		t.Errorf("output = %q, want the authority state line", output)
	}
	if !strings.Contains(output, "trusted authority taken from definition def-1") {
		t.Errorf("output = %q, want the authority's source definition named as context", output)
	}
	if !strings.Contains(output, "an administrator approving the repository's current default-branch definition") {
		t.Errorf("output = %q, want the generic approval note", output)
	}
	if strings.Contains(output, "definitions approve def-1") {
		t.Errorf("output = %q, must not turn the trusted-authority definition into an approve command - "+
			"it is not necessarily the one that needs approving", output)
	}
}

// TestPipelineGetApprovedAuthorityOmitsTheApprovalNote pins that an approved
// run still names its authority's definition (useful context) but carries no
// approval note, since there is nothing left to approve.
func TestPipelineGetApprovedAuthorityOmitsTheApprovalNote(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-1", RunNumber: 9, Status: "concluded", Outcome: strPipelinePtr("success"),
		Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("a", 40),
		QueuedAt:              "2026-09-01T00:00:00Z",
		AuthorityState:        strPipelinePtr("approved"),
		AuthorityDefinitionID: strPipelinePtr("def-1"),
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "Authority: approved") {
		t.Errorf("output = %q, want the authority state line", output)
	}
	if !strings.Contains(output, "trusted authority taken from definition def-1") {
		t.Errorf("output = %q, want the authority's source definition named as context", output)
	}
	if strings.Contains(output, "an administrator approving") {
		t.Errorf("output = %q, an approved run must not carry the approval note", output)
	}
}

// TestPipelineGetOmitsAuthorityWhenNotRecorded pins that a run with no
// recorded authority (planned before ankra-vn0bd.10.8, or not yet planned)
// prints no Authority line at all - null means "not recorded", not "no
// authority", and the two must not read the same.
func TestPipelineGetOmitsAuthorityWhenNotRecorded(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-1", RunNumber: 9, Status: "concluded", Outcome: strPipelinePtr("success"),
		Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("a", 40),
		QueuedAt: "2026-09-01T00:00:00Z",
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if strings.Contains(output, "Authority:") {
		t.Errorf("output = %q, want no authority line for a run that recorded none", output)
	}
}

func strPipelinePtr(value string) *string { return &value }
