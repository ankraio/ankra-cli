package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationResourceMock struct {
	baseMock
	payload json.RawMessage
	fail    error

	deployRequest     client.DeployApplicationRequest
	deployCalls       int
	demoRequest       client.DeployApplicationDemoRequest
	demoCalls         int
	filesRequest      client.UpdateApplicationFilesRequest
	filesCalls        int
	deploymentsAppID  string
	workflowRunID     int64
	visibilityRequest client.SetApplicationPackageVisibilityRequest
	deleteCalls       int
	makePublicCalls   int
}

func (mock *applicationResourceMock) GetApplicationDeployments(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.deploymentsAppID = applicationID
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) DeployApplication(requestContext context.Context, applicationID string, deployRequest client.DeployApplicationRequest) (json.RawMessage, error) {
	mock.deployCalls++
	mock.deployRequest = deployRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) DeployApplicationDemo(requestContext context.Context, applicationID string, demoRequest client.DeployApplicationDemoRequest) (json.RawMessage, error) {
	mock.demoCalls++
	mock.demoRequest = demoRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) UpdateApplicationFiles(requestContext context.Context, applicationID string, filesRequest client.UpdateApplicationFilesRequest) (json.RawMessage, error) {
	mock.filesCalls++
	mock.filesRequest = filesRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) GetApplicationWorkflowRunJobs(requestContext context.Context, applicationID string, runID int64) (json.RawMessage, error) {
	mock.workflowRunID = runID
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) SetApplicationPackageVisibility(requestContext context.Context, applicationID string, visibilityRequest client.SetApplicationPackageVisibilityRequest) (json.RawMessage, error) {
	mock.visibilityRequest = visibilityRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) DeleteApplication(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.deleteCalls++
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationResourceMock) MakeApplicationPackagesPublic(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.makePublicCalls++
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func runApplicationCommand(t *testing.T, mockClient APIClient, arguments ...string) (string, error) {
	t.Helper()
	return runApplicationCommandWithInput(t, mockClient, "", arguments...)
}

func runApplicationCommandWithInput(t *testing.T, mockClient APIClient, input string, arguments ...string) (string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	applicationCommand := newApplicationCommand()
	var output bytes.Buffer
	applicationCommand.SetOut(&output)
	applicationCommand.SetErr(&output)
	applicationCommand.SetIn(strings.NewReader(input))
	applicationCommand.SetArgs(arguments)
	executeError := applicationCommand.Execute()
	return output.String(), executeError
}

func TestApplicationResourceCommandsRegistered(t *testing.T) {
	applicationCommand := newApplicationCommand()
	registered := map[string]bool{}
	for _, subcommand := range applicationCommand.Commands() {
		registered[subcommand.Name()] = true
	}
	for _, expected := range []string{
		"add", "get", "list", "jobs", "retry", "reconcile", "delete", "deploy",
		"deployments", "installations", "chart-versions", "platform",
		"workflow-runs", "workflow-run-jobs", "rerun-workflow",
		"pull-request-reviews", "upgrade-workflow", "branches", "branch-files",
		"files", "publish-readiness", "container-security", "code-security",
		"package-visibility", "set-package-visibility", "make-public", "demo",
	} {
		if !registered[expected] {
			t.Errorf("subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationSubresourceRendersJSON(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"deployments":[{"cluster_id":"c1"}]}`)}
	output, executeError := runApplicationCommand(t, mockClient, "deployments", "app-1")
	if executeError != nil {
		t.Fatalf("deployments error = %v", executeError)
	}
	if mockClient.deploymentsAppID != "app-1" {
		t.Errorf("application id = %q, want app-1", mockClient.deploymentsAppID)
	}
	if !strings.Contains(output, "\"cluster_id\": \"c1\"") {
		t.Errorf("output is not indented JSON: %q", output)
	}
}

func TestApplicationSubresourceRendersYAML(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"ready":true}`)}
	output, executeError := runApplicationCommand(t, mockClient, "deployments", "app-1", "-o", "yaml")
	if executeError != nil {
		t.Fatalf("deployments error = %v", executeError)
	}
	if !strings.Contains(output, "ready: true") {
		t.Errorf("output is not YAML: %q", output)
	}
}

func TestApplicationDeployRequiresCluster(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient, "deploy", "app-1")
	if executeError == nil {
		t.Fatal("expected missing --cluster to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("DeployApplication calls = %d, want 0", mockClient.deployCalls)
	}
}

func TestApplicationDeployMapsRequest(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"status":"queued"}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"deploy", "app-1",
		"--cluster", "cluster-1",
		"--namespace", "prod",
		"--mode", "high_availability",
		"--set", "replicas=3",
		"--set", "image.tag=v2",
	)
	if executeError != nil {
		t.Fatalf("deploy error = %v", executeError)
	}
	if mockClient.deployCalls != 1 {
		t.Fatalf("DeployApplication calls = %d, want 1", mockClient.deployCalls)
	}
	request := mockClient.deployRequest
	if request.ClusterID != "cluster-1" || request.Namespace != "prod" || request.DeployMode != "high_availability" {
		t.Errorf("deploy request = %+v", request)
	}
	if request.Inputs["replicas"] != "3" || request.Inputs["image.tag"] != "v2" {
		t.Errorf("deploy inputs = %+v", request.Inputs)
	}
}

func TestApplicationDeployRejectsInvalidMode(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient, "deploy", "app-1", "--cluster", "c1", "--mode", "turbo")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("DeployApplication calls = %d, want 0", mockClient.deployCalls)
	}
}

func TestApplicationDemoDeployOnlySendsSetFlags(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"workspace_id":"w1"}`)}
	_, executeError := runApplicationCommand(t, mockClient, "demo", "deploy", "app-1", "--branch", "feature/x")
	if executeError != nil {
		t.Fatalf("demo deploy error = %v", executeError)
	}
	if mockClient.demoCalls != 1 {
		t.Fatalf("DeployApplicationDemo calls = %d, want 1", mockClient.demoCalls)
	}
	request := mockClient.demoRequest
	if request.Branch == nil || *request.Branch != "feature/x" {
		t.Errorf("branch = %v, want feature/x", request.Branch)
	}
	if request.PRNumber != nil || request.ImageTag != nil || request.TTLHours != nil || request.ContainerPort != nil {
		t.Errorf("unset flags were sent: %+v", request)
	}
}

func TestApplicationFilesReadsLocalFile(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "Dockerfile")
	if writeError := os.WriteFile(localPath, []byte("FROM scratch\n"), 0o600); writeError != nil {
		t.Fatalf("write fixture: %v", writeError)
	}
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"committed":true}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"files", "app-1",
		"--file", "Dockerfile="+localPath,
		"--delete", "old/path.yaml",
		"--message", "Update image",
	)
	if executeError != nil {
		t.Fatalf("files error = %v", executeError)
	}
	if mockClient.filesCalls != 1 {
		t.Fatalf("UpdateApplicationFiles calls = %d, want 1", mockClient.filesCalls)
	}
	request := mockClient.filesRequest
	if len(request.Files) != 1 || request.Files[0].Path != "Dockerfile" || request.Files[0].Content != "FROM scratch\n" {
		t.Errorf("files = %+v", request.Files)
	}
	if len(request.DeletedPaths) != 1 || request.DeletedPaths[0] != "old/path.yaml" {
		t.Errorf("deleted paths = %+v", request.DeletedPaths)
	}
	if request.CommitMessage != "Update image" {
		t.Errorf("commit message = %q", request.CommitMessage)
	}
}

func TestApplicationFilesMissingLocalFile(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient,
		"files", "app-1",
		"--file", "Dockerfile="+filepath.Join(t.TempDir(), "missing"),
	)
	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitNotFound, executeError)
	}
	if mockClient.filesCalls != 0 {
		t.Errorf("UpdateApplicationFiles calls = %d, want 0", mockClient.filesCalls)
	}
}

func TestApplicationFilesRequiresChange(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient, "files", "app-1")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
}

func TestApplicationWorkflowRunJobsParsesRunID(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"jobs":[]}`)}
	_, executeError := runApplicationCommand(t, mockClient, "workflow-run-jobs", "app-1", "12345")
	if executeError != nil {
		t.Fatalf("workflow-run-jobs error = %v", executeError)
	}
	if mockClient.workflowRunID != 12345 {
		t.Errorf("run id = %d, want 12345", mockClient.workflowRunID)
	}
}

func TestApplicationWorkflowRunJobsRejectsNonNumericRunID(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient, "workflow-run-jobs", "app-1", "not-a-number")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
}

func TestApplicationSetPackageVisibilityRequiresFlags(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient, "set-package-visibility", "app-1", "--kind", "image")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
}

func TestApplicationRejectsInvalidOutputBeforeRequest(t *testing.T) {
	mockClient := &applicationResourceMock{fail: errors.New("should not be called")}
	_, executeError := runApplicationCommand(t, mockClient, "deployments", "app-1", "-o", "xml")
	if executeError == nil {
		t.Fatal("expected invalid output format to fail")
	}
	if mockClient.deploymentsAppID != "" {
		t.Errorf("client was called before format validation: %q", mockClient.deploymentsAppID)
	}
}

func TestApplicationDelete_DeclineDoesNotDelete(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n", "delete", "app-1")
	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", executeError)
	}
	if got := exitCodeFor(executeError); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mockClient.deleteCalls != 0 {
		t.Errorf("expected no delete call on decline, got %d", mockClient.deleteCalls)
	}
}

func TestApplicationDelete_YesFlagProceeds(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "", "delete", "app-1", "--yes")
	if executeError != nil {
		t.Fatalf("expected success with --yes, got %v", executeError)
	}
	if mockClient.deleteCalls != 1 {
		t.Errorf("expected one delete call with --yes, got %d", mockClient.deleteCalls)
	}
}

func TestApplicationDelete_PipedYesProceeds(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "y\n", "delete", "app-1")
	if executeError != nil {
		t.Fatalf("expected success with piped y, got %v", executeError)
	}
	if mockClient.deleteCalls != 1 {
		t.Errorf("expected one delete call with piped y, got %d", mockClient.deleteCalls)
	}
}

func TestApplicationMakePublic_DeclineDoesNotCall(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n", "make-public", "app-1")
	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", executeError)
	}
	if got := exitCodeFor(executeError); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mockClient.makePublicCalls != 0 {
		t.Errorf("expected no make-public call on decline, got %d", mockClient.makePublicCalls)
	}
}

func TestApplicationMakePublic_YesFlagProceeds(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "", "make-public", "app-1", "--yes")
	if executeError != nil {
		t.Fatalf("expected success with --yes, got %v", executeError)
	}
	if mockClient.makePublicCalls != 1 {
		t.Errorf("expected one make-public call with --yes, got %d", mockClient.makePublicCalls)
	}
}

func TestApplicationMakePublic_PipedYesProceeds(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"success":true}`)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "y\n", "make-public", "app-1")
	if executeError != nil {
		t.Fatalf("expected success with piped y, got %v", executeError)
	}
	if mockClient.makePublicCalls != 1 {
		t.Errorf("expected one make-public call with piped y, got %d", mockClient.makePublicCalls)
	}
}

func TestParseKeyValueFlags(t *testing.T) {
	values, parseError := parseKeyValueFlags([]string{"a=1", "b=x=y"})
	if parseError != nil {
		t.Fatalf("parseKeyValueFlags error = %v", parseError)
	}
	if values["a"] != "1" || values["b"] != "x=y" {
		t.Errorf("values = %+v", values)
	}
	if _, missingError := parseKeyValueFlags([]string{"noequals"}); missingError == nil {
		t.Error("expected error for entry without =")
	}
	if _, emptyKeyError := parseKeyValueFlags([]string{"=value"}); emptyKeyError == nil {
		t.Error("expected error for empty key")
	}
}
