package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	listCalls   int
	// listResults, when set, is served one entry per call in order and takes
	// precedence over listResult, so a test can stage a run that appears
	// between polls. The last entry answers every call after it.
	listResults []client.PipelineRunList
	// listErrorsOnCall fails the numbered ListPipelineRuns calls (counting
	// from one) with the given error.
	listErrorsOnCall map[int]error

	createRequest client.CreatePipelineRunRequest
	createResult  *client.CreatePipelineRunResult
	createError   error
	createCalls   int

	getRunID  string
	getResult *client.PipelineRunDetail
	getError  error
	getCalls  int
	// getResults, when set, is served one entry per call in order and takes
	// precedence over getResult, so a test can stage a run whose steps move
	// between polls. The last entry answers every call after it, the way a
	// run that has settled keeps answering the same detail.
	getResults []client.PipelineRunDetail
	// getErrorsOnCall fails the numbered GetPipelineRun calls (counting from
	// one) with the given error, so a test can stage a read that fails
	// partway through a wait and then recovers.
	getErrorsOnCall map[int]error

	cancelRunID  string
	cancelResult *client.PipelineRun
	cancelError  error

	// applicationsListing, when set, answers ListApplicationsRaw, so a test
	// can bind an application to the repository a checkout points at.
	applicationsListing string
	// applicationsListingCalls counts ListApplicationsRaw calls, so a test
	// can pin that a dispatch walks the listing exactly as often as it must
	// (ankra-4dq9l: once, never twice).
	applicationsListingCalls int
	// applicationsListingErrorsOnCall fails the numbered ListApplicationsRaw
	// calls (counting from one) with the given error, so a test can stage a
	// listing that answers the first walk and fails a second one.
	applicationsListingErrorsOnCall map[int]error

	rerunRunID      string
	rerunFailedOnly bool
	rerunResult     *client.CreatePipelineRunResult
	rerunError      error

	artifactsRunID  string
	artifactsResult *client.PipelineArtifactList
	artifactsError  error
	// artifactsPages, when set, is served one page per call in order and
	// takes precedence over artifactsResult, so a test can exercise a
	// listing the caller has to follow NextCursor through.
	artifactsPages   []client.PipelineArtifactList
	artifactsOptions []client.ListPipelineArtifactsOptions

	streamStepID  string
	streamOptions []client.StepLogStreamOptions
	streamEvents  []client.PipelineLogEvent
	streamError   error
	// streamNeverEnds serves streamEvents and then holds the channel open
	// until the caller cancels, the way a platform that ignores follow=false
	// keeps a concluded step's connection alive on keepalives.
	streamNeverEnds bool

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

	listRepositoriesOptions client.ListPipelineRepositoriesOptions
	listRepositoriesResult  *client.PipelineRepositoryList
	listRepositoriesError   error

	getRepositoryID     string
	getRepositoryResult *client.PipelineRepository
	getRepositoryError  error

	connectRepositoryRequest client.ConnectPipelineRepositoryRequest
	connectRepositoryResult  *client.ConnectPipelineRepositoryResult
	connectRepositoryError   error
	connectRepositoryCalls   int

	disconnectRepositoryID    string
	disconnectRepositoryError error
	disconnectRepositoryCalls int
}

func (mock *pipelineLaneMock) ListPipelineRuns(ctx context.Context, selector client.PipelineSelector, options client.ListPipelineRunsOptions) (*client.PipelineRunList, error) {
	mock.lastSelector = selector
	mock.listOptions = options
	mock.listCalls++
	if mock.listError != nil {
		return nil, mock.listError
	}
	if failure, isFailing := mock.listErrorsOnCall[mock.listCalls]; isFailing {
		return nil, failure
	}
	if mock.listResults != nil {
		index := mock.listCalls - 1
		if index >= len(mock.listResults) {
			index = len(mock.listResults) - 1
		}
		page := mock.listResults[index]
		return &page, nil
	}
	return mock.listResult, nil
}

func (mock *pipelineLaneMock) ListApplicationsRaw(ctx context.Context, page int, pageSize int, search string) (json.RawMessage, error) {
	mock.applicationsListingCalls++
	if failure, isFailing := mock.applicationsListingErrorsOnCall[mock.applicationsListingCalls]; isFailing {
		return nil, failure
	}
	if mock.applicationsListing == "" {
		return mock.baseMock.ListApplicationsRaw(ctx, page, pageSize, search)
	}
	return json.RawMessage(mock.applicationsListing), nil
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
	mock.getCalls++
	if mock.getError != nil {
		return nil, mock.getError
	}
	if failure, isFailing := mock.getErrorsOnCall[mock.getCalls]; isFailing {
		return nil, failure
	}
	if mock.getResults != nil {
		index := mock.getCalls - 1
		if index >= len(mock.getResults) {
			index = len(mock.getResults) - 1
		}
		detail := mock.getResults[index]
		return &detail, nil
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

func (mock *pipelineLaneMock) StreamPipelineStepLogs(ctx context.Context, selector client.PipelineSelector, runID string, stepID string, options client.StepLogStreamOptions) (<-chan client.PipelineLogEvent, error) {
	mock.lastSelector = selector
	mock.streamStepID = stepID
	mock.streamOptions = append(mock.streamOptions, options)
	if mock.streamError != nil {
		return nil, mock.streamError
	}
	events := make(chan client.PipelineLogEvent, len(mock.streamEvents))
	for _, event := range mock.streamEvents {
		events <- event
	}
	if !mock.streamNeverEnds {
		close(events)
		return events, nil
	}
	go func() {
		<-ctx.Done()
		close(events)
	}()
	return events, nil
}

func (mock *pipelineLaneMock) ListPipelineArtifacts(ctx context.Context, selector client.PipelineSelector, runID string, options client.ListPipelineArtifactsOptions) (*client.PipelineArtifactList, error) {
	mock.lastSelector = selector
	mock.artifactsRunID = runID
	mock.artifactsOptions = append(mock.artifactsOptions, options)
	if mock.artifactsError != nil {
		return nil, mock.artifactsError
	}
	if mock.artifactsPages != nil {
		index := len(mock.artifactsOptions) - 1
		if index >= len(mock.artifactsPages) {
			index = len(mock.artifactsPages) - 1
		}
		page := mock.artifactsPages[index]
		return &page, nil
	}
	return mock.artifactsResult, nil
}

func (mock *pipelineLaneMock) DownloadPipelineArtifact(ctx context.Context, selector client.PipelineSelector, artifactID string, destination io.Writer) error {
	mock.lastSelector = selector
	mock.downloadArtifactID = artifactID
	// downloadPayload is written before downloadError is answered, so a test
	// can stage a download that failed partway through one.
	if _, writeError := destination.Write([]byte(mock.downloadPayload)); writeError != nil {
		return writeError
	}
	return mock.downloadError
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

func (mock *pipelineLaneMock) ListPipelineRepositories(ctx context.Context, options client.ListPipelineRepositoriesOptions) (*client.PipelineRepositoryList, error) {
	mock.listRepositoriesOptions = options
	if mock.listRepositoriesError != nil {
		return nil, mock.listRepositoriesError
	}
	return mock.listRepositoriesResult, nil
}

func (mock *pipelineLaneMock) GetPipelineRepository(ctx context.Context, repositoryID string) (*client.PipelineRepository, error) {
	mock.getRepositoryID = repositoryID
	if mock.getRepositoryError != nil {
		return nil, mock.getRepositoryError
	}
	return mock.getRepositoryResult, nil
}

func (mock *pipelineLaneMock) ConnectPipelineRepository(ctx context.Context, request client.ConnectPipelineRepositoryRequest) (*client.ConnectPipelineRepositoryResult, error) {
	mock.connectRepositoryRequest = request
	mock.connectRepositoryCalls++
	if mock.connectRepositoryError != nil {
		return nil, mock.connectRepositoryError
	}
	return mock.connectRepositoryResult, nil
}

func (mock *pipelineLaneMock) DisconnectPipelineRepository(ctx context.Context, repositoryID string) error {
	mock.disconnectRepositoryID = repositoryID
	mock.disconnectRepositoryCalls++
	return mock.disconnectRepositoryError
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
		"run", "list", "get", "cancel", "rerun", "logs", "artifacts", "validate", "definition", "definitions", "schedules", "repositories",
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
	repositoriesCommand := findSubcommand(t, pipelineCommand, "repositories")
	for _, expected := range []string{"list", "get", "connect", "disconnect"} {
		if findSubcommandOrNil(repositoriesCommand, expected) == nil {
			t.Errorf("pipeline repositories subcommand %q is not registered", expected)
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

func TestPipelineArtifactsPagingFlagsOnBothSurfaces(t *testing.T) {
	// A run with more artifacts than one page must be walkable from either
	// address, so both surfaces carry the same paging flags.
	surfaces := map[string]*cobra.Command{
		"pipeline artifacts":             findSubcommand(t, newPipelineCommand(), "artifacts"),
		"application pipeline artifacts": findSubcommand(t, findSubcommand(t, newApplicationCommand(), "pipeline"), "artifacts"),
	}
	for name, command := range surfaces {
		for _, flag := range []string{"cursor", "limit"} {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%q does not register --%s", name, flag)
			}
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

// seedPipelineCheckout creates a checkout on main with one commit whose origin
// is remoteURL, and makes it the working directory.
func seedPipelineCheckout(t *testing.T, remoteURL string) {
	t.Helper()
	repositoryPath := createTestGitRepository(t, "main", remoteURL)
	if writeError := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("service\n"), 0o600); writeError != nil {
		t.Fatalf("seeding the checkout: %v", writeError)
	}
	runTestGit(t, repositoryPath, "add", "service.txt")
	runTestGit(t, repositoryPath, "-c", "user.email=test@example.com", "-c", "user.name=Test",
		"commit", "-m", "Add the service")
	t.Chdir(repositoryPath)
}

// applicationBoundTo is an applications listing with testApplicationID bound to
// owner/name.
func applicationBoundTo(owner string, name string) string {
	return `{"result":[{"id":"` + testApplicationID + `","name":"` + name + `","app_repo_owner":"` + owner +
		`","app_repo_name":"` + name + `"}],"pagination":{"total_pages":1}}`
}

func queuedRunMock() *pipelineLaneMock {
	return &pipelineLaneMock{createResult: &client.CreatePipelineRunResult{
		RunID: "umbrella-1", PipelineRunID: "run-1", RunNumber: 1,
	}}
}

// TestPipelineRunOutsideACheckoutRunsTheTipOfTheRef pins that with no --sha and
// no checkout to read, the dispatch goes out without a commit and the platform
// reads the tip of the ref from the repository's host (cluster
// verifyRunProvenance, cluster#2612). That is a live read at dispatch, not a
// commit the platform stored earlier, so refusing it only made the user look
// the sha up by hand (PLA-863).
func TestPipelineRunOutsideACheckoutRunsTheTipOfTheRef(t *testing.T) {
	t.Chdir(t.TempDir())
	mockClient := queuedRunMock()
	output, executeError := runPipelineCommand(t, mockClient, "run", "--application", testApplicationID, "--ref", "main")
	if executeError != nil {
		t.Fatalf("dispatch with a ref and no sha = %v, want it sent for the platform to resolve", executeError)
	}
	if mockClient.createCalls != 1 {
		t.Fatalf("CreatePipelineRun calls = %d, want 1", mockClient.createCalls)
	}
	if mockClient.createRequest.HeadSHA != "" || mockClient.createRequest.Ref != "main" {
		t.Errorf("dispatched sha %q ref %q, want no sha and ref main", mockClient.createRequest.HeadSHA, mockClient.createRequest.Ref)
	}
	if !strings.Contains(output, "tip of main") {
		t.Errorf("output = %q, want it to say the tip of main runs", output)
	}
}

// TestPipelineRunReadsTheWorkingDirectoryHead pins the simplification: inside
// a checkout of the pipeline's own repository the dispatch runs the commit
// under the user's cursor, and says so, instead of making them paste
// `git rev-parse HEAD` back (ankra-ctsmd).
func TestPipelineRunReadsTheWorkingDirectoryHead(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	mockClient := queuedRunMock()
	mockClient.applicationsListing = applicationBoundTo("acme", "payments")
	if _, executeError := runPipelineCommand(t, mockClient, "run",
		"--application", testApplicationID); executeError != nil {
		t.Fatalf("dispatch inside a checkout = %v, want the HEAD commit used", executeError)
	}
	if mockClient.createCalls != 1 {
		t.Fatalf("CreatePipelineRun calls = %d, want 1", mockClient.createCalls)
	}
	if len(mockClient.createRequest.HeadSHA) != 40 {
		t.Errorf("dispatched sha = %q, want the checkout's full HEAD sha", mockClient.createRequest.HeadSHA)
	}
	if mockClient.createRequest.Ref != "main" {
		t.Errorf("dispatched ref = %q, want the checked-out branch", mockClient.createRequest.Ref)
	}
	if mockClient.applicationsListingCalls != 1 {
		t.Errorf("applications listing read %d times, want exactly once: the checkout match for a named --application is the only walk",
			mockClient.applicationsListingCalls)
	}
}

// TestPipelineRunIgnoresTheHeadOfAnotherRepositorysCheckout is the PLA-863
// case: 'pipeline run --application smartinsight' typed in a checkout of
// commerce must not dispatch smartinsight with commerce's commit and branch.
func TestPipelineRunIgnoresTheHeadOfAnotherRepositorysCheckout(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/commerce.git")
	mockClient := queuedRunMock()
	mockClient.applicationsListing = applicationBoundTo("acme", "smartinsight")
	output, executeError := runPipelineCommand(t, mockClient, "run", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("dispatch = %v", executeError)
	}
	if mockClient.createRequest.HeadSHA != "" || mockClient.createRequest.Ref != "" {
		t.Errorf("dispatched sha %q ref %q from another repository's checkout, want neither",
			mockClient.createRequest.HeadSHA, mockClient.createRequest.Ref)
	}
	if strings.Contains(output, "working directory's HEAD") || !strings.Contains(output, "default branch") {
		t.Errorf("output = %q, want the default branch named and no local HEAD", output)
	}
	if mockClient.applicationsListingCalls != 1 {
		t.Errorf("applications listing read %d times, want exactly once", mockClient.applicationsListingCalls)
	}
}

// TestPipelineRunDoesNotTrustACheckoutItCannotMatch pins the fail-closed arms:
// a listing that cannot be read, and a --repository id there is no
// owner/name lookup for, both leave the commit to the platform rather than
// sending a HEAD that may belong to a different repository. The repository
// arm never reads the listing at all: there is nothing in it to match an id
// against, so a walk would be cost with no answer.
func TestPipelineRunDoesNotTrustACheckoutItCannotMatch(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	for name, testCase := range map[string]struct {
		arguments    []string
		listingReads int
	}{
		"unreadable listing": {
			arguments:    []string{"run", "--application", testApplicationID},
			listingReads: 1,
		},
		"repository id": {
			arguments:    []string{"run", "--repository", "7d0c5a4e-7f55-4f0e-9d8a-2f5a3c1b9e61"},
			listingReads: 0,
		},
	} {
		mockClient := queuedRunMock()
		if _, executeError := runPipelineCommand(t, mockClient, testCase.arguments...); executeError != nil {
			t.Fatalf("%s: dispatch = %v", name, executeError)
		}
		if mockClient.createRequest.HeadSHA != "" {
			t.Errorf("%s: dispatched sha = %q, want none", name, mockClient.createRequest.HeadSHA)
		}
		if mockClient.applicationsListingCalls != testCase.listingReads {
			t.Errorf("%s: applications listing read %d times, want %d",
				name, mockClient.applicationsListingCalls, testCase.listingReads)
		}
	}
}

// TestPipelineRunInferredFromTheCheckoutWalksTheListingOnce is the
// ankra-4dq9l regression. Plain 'ankra pipeline run' infers --application by
// walking the applications listing and matching the checkout's origin; the
// dispatch then used to walk the whole listing AGAIN to decide whether that
// same checkout's HEAD could be the commit. When any page of the second walk
// failed, the HEAD was dropped and a user on a feature branch built the
// default branch instead - after being told which application their checkout
// had just resolved to. The listing here answers once and fails every call
// after it: the inference must be enough for the HEAD to be used.
func TestPipelineRunInferredFromTheCheckoutWalksTheListingOnce(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	mockClient := queuedRunMock()
	mockClient.applicationsListing = applicationBoundTo("acme", "payments")
	mockClient.applicationsListingErrorsOnCall = map[int]error{2: errors.New("second walk must not happen")}
	output, executeError := runPipelineCommand(t, mockClient, "run")
	if executeError != nil {
		t.Fatalf("plain dispatch inside a checkout = %v, want the inferred application run at HEAD", executeError)
	}
	if mockClient.applicationsListingCalls != 1 {
		t.Errorf("applications listing read %d times, want exactly once: the inference is the only walk",
			mockClient.applicationsListingCalls)
	}
	if mockClient.createCalls != 1 {
		t.Fatalf("CreatePipelineRun calls = %d, want 1", mockClient.createCalls)
	}
	if mockClient.lastSelector.ApplicationID != testApplicationID || mockClient.lastSelector.RepositoryID != "" {
		t.Errorf("dispatched selector = %+v, want the application the checkout resolved to", mockClient.lastSelector)
	}
	if len(mockClient.createRequest.HeadSHA) != 40 {
		t.Errorf("dispatched sha = %q, want the checkout's full HEAD sha", mockClient.createRequest.HeadSHA)
	}
	if mockClient.createRequest.Ref != "main" {
		t.Errorf("dispatched ref = %q, want the checked-out branch", mockClient.createRequest.Ref)
	}
	if !strings.Contains(output, "Using the application bound to acme/payments") {
		t.Errorf("output = %q, want the inferred application named", output)
	}
	if !strings.Contains(output, "working directory's HEAD") || strings.Contains(output, "default branch") {
		t.Errorf("output = %q, want the local HEAD announced and no default-branch fallback", output)
	}
}

// TestPipelineRunInferredFromTheCheckoutStillHonoursANamedRef pins that
// knowing the checkout is the selected repository does not loosen the --ref
// rule: an inferred application run at "release-2.0" sends the ref alone,
// never the HEAD of whatever branch the checkout sits on.
func TestPipelineRunInferredFromTheCheckoutStillHonoursANamedRef(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	mockClient := queuedRunMock()
	mockClient.applicationsListing = applicationBoundTo("acme", "payments")
	if _, executeError := runPipelineCommand(t, mockClient, "run", "--ref", "release-2.0"); executeError != nil {
		t.Fatalf("dispatch = %v", executeError)
	}
	if mockClient.lastSelector.ApplicationID != testApplicationID {
		t.Errorf("dispatched selector = %+v, want the inferred application", mockClient.lastSelector)
	}
	if mockClient.createRequest.HeadSHA != "" || mockClient.createRequest.Ref != "release-2.0" {
		t.Errorf("dispatched sha %q ref %q, want no sha and ref release-2.0",
			mockClient.createRequest.HeadSHA, mockClient.createRequest.Ref)
	}
	if mockClient.applicationsListingCalls != 1 {
		t.Errorf("applications listing read %d times, want exactly once", mockClient.applicationsListingCalls)
	}
}

// TestPipelineRunDoesNotPairANamedRefWithTheLocalHead pins that a ref the
// user named is never dispatched against whatever the checkout happens to
// have: running "release-2.0" from a checkout sitting on main sends the ref
// alone, never main's commit under the release ref (ankra-ctsmd).
func TestPipelineRunDoesNotPairANamedRefWithTheLocalHead(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	mockClient := queuedRunMock()
	mockClient.applicationsListing = applicationBoundTo("acme", "payments")
	if _, executeError := runPipelineCommand(t, mockClient, "run",
		"--application", testApplicationID, "--ref", "release-2.0"); executeError != nil {
		t.Fatalf("dispatch = %v", executeError)
	}
	if mockClient.createRequest.HeadSHA != "" || mockClient.createRequest.Ref != "release-2.0" {
		t.Errorf("dispatched sha %q ref %q, want no sha and ref release-2.0",
			mockClient.createRequest.HeadSHA, mockClient.createRequest.Ref)
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

func TestPipelineListingsRefuseANegativeLimit(t *testing.T) {
	// A negative --limit used to be dropped on the way to the query, so the
	// caller silently got the server's default page instead of being told
	// the flag was ignored.
	for name, arguments := range map[string][]string{
		"artifacts": {"artifacts", "run-1", "--application", testApplicationID, "--limit", "-5"},
		"list":      {"list", "--application", testApplicationID, "--limit", "-5"},
	} {
		mockClient := &pipelineLaneMock{}
		_, executeError := runPipelineCommand(t, mockClient, arguments...)
		if executeError == nil || !strings.Contains(executeError.Error(), "--limit must be a positive number") {
			t.Errorf("%s error = %v, want a usage refusal", name, executeError)
		}
		if len(mockClient.artifactsOptions) != 0 || mockClient.listOptions.Limit != 0 {
			t.Errorf("%s must refuse before asking the server anything", name)
		}
	}
}

func TestPipelineArtifactsListSaysWhenAnotherPageExists(t *testing.T) {
	nextCursor := "cursor-2"
	stepID := "step-1"
	mockClient := &pipelineLaneMock{artifactsResult: &client.PipelineArtifactList{
		Artifacts: []client.PipelineArtifact{
			{ID: "artifact-1", StepID: &stepID, Kind: client.PipelineArtifactKindStepLog,
				Status: client.PipelineArtifactStatusUploaded},
		},
		NextCursor: &nextCursor,
	}}
	output, executeError := runPipelineCommand(t, mockClient, "artifacts", "run-1",
		"--application", testApplicationID, "--cursor", "cursor-1", "--limit", "100")
	if executeError != nil {
		t.Fatalf("artifacts error = %v", executeError)
	}
	if len(mockClient.artifactsOptions) != 1 ||
		mockClient.artifactsOptions[0].Cursor != "cursor-1" || mockClient.artifactsOptions[0].Limit != 100 {
		t.Fatalf("paging asked for = %+v, want the flags passed through", mockClient.artifactsOptions)
	}
	if !strings.Contains(output, "--cursor cursor-2") {
		t.Errorf("output = %q, want it to name the next page's cursor", output)
	}
}

func TestPipelineArtifactsListEmptyPageWithMoreToRead(t *testing.T) {
	// An empty page that still offers a cursor is "more to read", not "the
	// run stored nothing".
	nextCursor := "cursor-2"
	mockClient := &pipelineLaneMock{artifactsResult: &client.PipelineArtifactList{
		Artifacts: []client.PipelineArtifact{}, NextCursor: &nextCursor,
	}}
	output, executeError := runPipelineCommand(t, mockClient, "artifacts", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("artifacts error = %v", executeError)
	}
	if strings.Contains(output, "No artifacts stored") {
		t.Errorf("output = %q, must not claim the run stored nothing", output)
	}
	if !strings.Contains(output, "--cursor cursor-2") {
		t.Errorf("output = %q, want it to name the next page's cursor", output)
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

// validationWithNetworkTiers is a dry run of a definition naming no network
// tier of its own, planned by an Ankra that resolves one per step: the build
// takes the egress its base image and registry push need, and the test stage
// keeps the run kind's own default of none.
func validationWithNetworkTiers() *client.PipelineValidation {
	return &client.PipelineValidation{
		Severity: "ok",
		Events: []client.PipelineEventPlan{{
			Event: "push",
			Run:   true,
			Steps: []client.PipelinePlannedStep{
				{StepKey: "checkout", Stage: "checkout", Kind: "checkout", Network: "egress-https"},
				{StepKey: "test", Stage: "test", Kind: "run", Network: "none"},
				{StepKey: "build", Stage: "build", Kind: "build", Network: "egress-https"},
			},
		}},
	}
}

// writePipelineFixture writes a definition for `validate` to read. Naming a
// file keeps the test off the default path, which resolves against the
// process's working directory and, today, refuses rather than falling back to
// the stored definition when it is missing.
func writePipelineFixture(t *testing.T) string {
	t.Helper()
	fixturePath := t.TempDir() + "/pipeline.yaml"
	if writeError := os.WriteFile(fixturePath, []byte("apiVersion: ankra.io/v1\nkind: Pipeline\n"), 0o600); writeError != nil {
		t.Fatalf("writing fixture: %v", writeError)
	}
	return fixturePath
}

func TestPipelineValidateNamesEachPlannedStepsNetworkTier(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: validationWithNetworkTiers()}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("validate error = %v", executeError)
	}
	for _, wanted := range []string{
		"checkout (checkout, checkout, egress-https)",
		"test (test, run, none)",
		"build (build, build, egress-https)",
	} {
		if !strings.Contains(output, wanted) {
			t.Errorf("output = %q, want it to name the step line %q", output, wanted)
		}
	}
}

// TestPipelineValidateOmitsANetworkTierAnOlderPlatformDoesNotSend keeps the
// tier a report of what the platform resolved rather than an assertion the
// CLI invents: an Ankra older than the field sends no tier, and printing an
// empty one would read as "this step runs with no egress" - the opposite of
// the truth for a checkout or a build.
func TestPipelineValidateOmitsANetworkTierAnOlderPlatformDoesNotSend(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{
		Severity: "ok",
		Events: []client.PipelineEventPlan{{
			Event: "push",
			Run:   true,
			Steps: []client.PipelinePlannedStep{{StepKey: "build", Stage: "build", Kind: "build"}},
		}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("validate error = %v", executeError)
	}
	if !strings.Contains(output, "build (build, build)") {
		t.Errorf("output = %q, want the step line without a tier", output)
	}
	if strings.Contains(output, "build, build, )") {
		t.Errorf("output = %q, must not print an empty tier", output)
	}
}

func TestPipelineValidateJSONCarriesTheNetworkTier(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: validationWithNetworkTiers()}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("validate error = %v", executeError)
	}
	var decoded client.PipelineValidation
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("decoding %q: %v", output, decodeError)
	}
	if len(decoded.Events) != 1 || len(decoded.Events[0].Steps) != 3 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Events[0].Steps[2].Network != "egress-https" {
		t.Errorf("build step network = %q, want the tier to survive the re-encode",
			decoded.Events[0].Steps[2].Network)
	}
	if !strings.Contains(output, `"network"`) {
		t.Errorf("output = %q, want the wire field name kept for scripts", output)
	}
}

// TestPipelineValidateJSONOmitsANetworkTierAnOlderPlatformDoesNotSend is the
// scripted half of the older-platform case: the key is left out rather than
// emitted empty, so `.network` is absent for "this Ankra does not resolve
// tiers" and only ever a tier the platform really resolved otherwise. An
// empty string would be indistinguishable from a resolved value to jq.
func TestPipelineValidateJSONOmitsANetworkTierAnOlderPlatformDoesNotSend(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{
		Severity: "ok",
		Events: []client.PipelineEventPlan{{
			Event: "push",
			Run:   true,
			Steps: []client.PipelinePlannedStep{{StepKey: "build", Stage: "build", Kind: "build"}},
		}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("validate error = %v", executeError)
	}
	if strings.Contains(output, `"network"`) {
		t.Errorf("output = %q, want no network key at all", output)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("decoding %q: %v", output, decodeError)
	}
	step := decoded["events"].([]any)[0].(map[string]any)["steps"].([]any)[0].(map[string]any)
	if _, isPresent := step["network"]; isPresent {
		t.Errorf("step = %+v, want the key absent rather than empty", step)
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

// TestPipelineRunDetailPrintsTheRecordedErrorClass pins PLA-851's ask: a
// failed run must name its error class where a caller reads the run, not only
// under -o json. The push case is the reported one - Smartoptics read
// `ankra pipeline get` on five failed builds and nothing on the run said the
// failure was their registry's.
func TestPipelineRunDetailPrintsTheRecordedErrorClass(t *testing.T) {
	pushFailed := "registry_push_failed"
	pushMessage := "The build ran, and its image could not be pushed to the image registry it publishes to; " +
		"its builder reported \"push_failed\". BuildKit stopped with: failed to solve: " +
		"failed to push artifact.example.dev/smart-hub/backend:sha-abc1234: 401 Unauthorized"
	outcome := "infra_error"
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			RunNumber: 98, ID: "run-98", Status: "concluded", Outcome: &outcome,
			ErrorClass: &pushFailed, ErrorMessage: &pushMessage,
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	if !strings.Contains(rendered, "Class:     registry_push_failed") {
		t.Fatalf("the run's error class must be printed, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "401 Unauthorized") || !strings.Contains(rendered, "artifact.example.dev") {
		t.Fatalf("the message keeps the registry host and its answer, got:\n%s", rendered)
	}
	if strings.Index(rendered, "Class:") > strings.Index(rendered, "Error:") {
		t.Fatalf("the class is read before the message it classifies, got:\n%s", rendered)
	}
}

// TestPipelineRunDetailPrintsNoClassLineWhenNoneWasRecorded holds the other
// half: a class the server never recorded is not a class called "", so the
// line is absent rather than empty, and a run with only a message still
// prints it the way it always did.
func TestPipelineRunDetailPrintsNoClassLineWhenNoneWasRecorded(t *testing.T) {
	outcome := "success"
	var succeeded bytes.Buffer
	printPipelineRunDetail(&succeeded, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 99, ID: "run-99", Status: "concluded", Outcome: &outcome},
	}, client.PipelineSelector{})
	if strings.Contains(succeeded.String(), "Class:") {
		t.Fatalf("a run with no recorded class prints no class line, got:\n%s", succeeded.String())
	}

	blank := "   "
	message := "The gate blocked this run."
	failure := "failure"
	var unclassified bytes.Buffer
	printPipelineRunDetail(&unclassified, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			RunNumber: 100, ID: "run-100", Status: "concluded", Outcome: &failure,
			ErrorClass: &blank, ErrorMessage: &message,
		},
	}, client.PipelineSelector{})
	rendered := unclassified.String()
	if strings.Contains(rendered, "Class:") {
		t.Fatalf("a whitespace-only class is not a class, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Error:     The gate blocked this run.") {
		t.Fatalf("the message is printed as it always was, got:\n%s", rendered)
	}
}

// TestPipelineRunConclusionErrorNamesTheErrorClass covers the same fact on the
// --wait and --exit-code path, where the conclusion reaches a script as an
// error string rather than a rendered block. Outcome and class are different
// vocabularies - infra_error is the pipeline's, registry_push_failed is the
// step's - and a caller needs both.
func TestPipelineRunConclusionErrorNamesTheErrorClass(t *testing.T) {
	outcome := "infra_error"
	errorClass := "registry_push_failed"
	message := "The build ran, and its image could not be pushed to the image registry it publishes to."
	classified := pipelineRunConclusionError(client.PipelineRun{
		RunNumber: 98, Outcome: &outcome, ErrorClass: &errorClass, ErrorMessage: &message,
	})
	if classified == nil {
		t.Fatal("a non-success conclusion is an error")
	}
	if !strings.Contains(classified.Error(), "concluded infra_error (registry_push_failed): ") {
		t.Fatalf("the outcome and the class are both named, got %v", classified)
	}
	unclassified := pipelineRunConclusionError(client.PipelineRun{
		RunNumber: 99, Outcome: &outcome, ErrorMessage: &message,
	})
	if unclassified == nil || strings.Contains(unclassified.Error(), "()") {
		t.Fatalf("an unrecorded class adds nothing to the sentence, got %v", unclassified)
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

// TestPipelineDefinitionsGetGuardsAgainstANilResultWithNoError pins that a
// broken APIClient answering (nil, nil) - unreachable through the real
// client, but not through the interface's own contract - is reported as a
// clear error rather than panicking on a nil dereference
// (ankra-platform[bot] review on #231).
func TestPipelineDefinitionsGetGuardsAgainstANilResultWithNoError(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "definitions", "get", "def-1")
	if executeError == nil {
		t.Fatal("expected an error rather than a panic")
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

// TestPipelineDefinitionsApproveGuardsAgainstANilResultWithNoError is
// TestPipelineDefinitionsGetGuardsAgainstANilResultWithNoError's twin for
// approve.
func TestPipelineDefinitionsApproveGuardsAgainstANilResultWithNoError(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "definitions", "approve", "def-1", "--yes")
	if executeError == nil {
		t.Fatal("expected an error rather than a panic")
	}
}

// TestPipelineGetRendersAuthorityWhenRecorded pins that 'pipeline get' shows
// a run's recorded authority state and, for a state other than "approved"
// with no approvable definition reported, says so without naming any id.
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
	if !strings.Contains(output, "trusted authority taken from definition def-1 (already trusted - not the definition to approve)") {
		t.Errorf("output = %q, want the authority's source definition named as context, and not as the lead", output)
	}
	if !strings.Contains(output, "no definition to approve was reported for this run") {
		t.Errorf("output = %q, want the note that no approvable definition was reported", output)
	}
	if strings.Contains(output, "definitions approve") {
		t.Errorf("output = %q, must not print an approve command the server did not name - "+
			"the trusted-authority definition is not the one that needs approving", output)
	}
}

// TestPipelineGetPrintsTheApproveCommandForTheReportedDefinition is PLA-855:
// the reporter followed the Authority block, approved the only id it showed
// (authority_definition_id) and got "Only the repository's current
// default-branch definition can be approved". The approvable id is the run
// detail's approve_definition_id, and it is the only one an approve command
// may name (ankra-erdtu).
func TestPipelineGetPrintsTheApproveCommandForTheReportedDefinition(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			ID: "run-40", RunNumber: 40, Status: "concluded", Outcome: strPipelinePtr("success"),
			Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("b", 40),
			QueuedAt:              "2026-09-14T02:00:00Z",
			AuthorityState:        strPipelinePtr("unapproved"),
			AuthorityDefinitionID: strPipelinePtr("43aa76e5-trusted"),
		},
		ApproveDefinitionID: strPipelinePtr("4a5d3e86-current"),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-40", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "ankra pipeline definitions approve 4a5d3e86-current") {
		t.Errorf("output = %q, want the approve command for the reported definition", output)
	}
	if strings.Contains(output, "definitions approve 43aa76e5-trusted") {
		t.Errorf("output = %q, must never name the trusted-authority definition in an approve command", output)
	}
	if !strings.Contains(output, "for runs planned after it") {
		t.Errorf("output = %q, want the note that an approval does not change this run", output)
	}
	if strings.Contains(output, "no definition to approve was reported") {
		t.Errorf("output = %q, a reported definition must not also print the none-reported note", output)
	}
}

// TestPipelineGetJSONCarriesTheDefinitionToApprove pins that -o json passes
// approve_definition_id through alongside authority_definition_id, so a
// script can tell the two apart.
func TestPipelineGetJSONCarriesTheDefinitionToApprove(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			ID: "run-40", RunNumber: 40, Status: "queued", Trigger: "push",
			TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("b", 40), QueuedAt: "2026-09-14T02:00:00Z",
			AuthorityState:        strPipelinePtr("unapproved"),
			AuthorityDefinitionID: strPipelinePtr("43aa76e5-trusted"),
		},
		ApproveDefinitionID: strPipelinePtr("4a5d3e86-current"),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-40", "--application", testApplicationID,
		"-o", "json")
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	for _, want := range []string{`"approve_definition_id": "4a5d3e86-current"`, `"authority_definition_id": "43aa76e5-trusted"`} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want %s", output, want)
		}
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
	if strings.Contains(output, "definition to approve") || strings.Contains(output, "definitions approve") {
		t.Errorf("output = %q, an approved run must not carry an approval note", output)
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

func int64PipelinePtr(value int64) *int64 { return &value }

// supersededPipelineRun is a run a newer run took the concurrency group from,
// as the platform reports it: cancelled, with the class that says nobody
// pressed cancel and the run that replaced it.
func supersededPipelineRun() client.PipelineRun {
	return client.PipelineRun{
		ID: "run-17", RunNumber: 17, Status: "concluded",
		Outcome:      strPipelinePtr("cancelled"),
		ErrorClass:   strPipelinePtr("superseded"),
		ErrorMessage: strPipelinePtr("A newer run took this run's concurrency group."),
		Trigger:      "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("c", 40),
		QueuedAt:              "2026-09-15T00:00:00Z",
		SupersededByRunID:     strPipelinePtr("run-18"),
		SupersededByRunNumber: int64PipelinePtr(18),
	}
}

// TestPipelineGetNamesTheRunThatSupersededIt pins ankra-n8q38.1's CLI half:
// a run a newer run replaced says so under its status and names the run to
// look at instead. It read "cancelled" with no run named before, which sent
// the author of the superseded run looking for whoever had cancelled it.
func TestPipelineGetNamesTheRunThatSupersededIt(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{
		PipelineRun: supersededPipelineRun(),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "Superseded: by run #18") {
		t.Errorf("output = %q, want the run that took this run's place", output)
	}
	if !strings.Contains(output, "⊘ superseded") {
		t.Errorf("output = %q, want the status line to say superseded", output)
	}
	if strings.Contains(output, "⊘ cancelled") {
		t.Errorf("output = %q, want no state cell reading cancelled for a superseded run", output)
	}
	// The supersession is said once (ankra-ohzw6): the class is the word the
	// Status line carries and the platform's message is the Superseded line
	// without the number, so neither gets a line of its own.
	if strings.Contains(output, "Class:") {
		t.Errorf("output = %q, want no Class line repeating the word superseded", output)
	}
	if strings.Contains(output, "Error:") {
		t.Errorf("output = %q, want no Error line repeating the supersession", output)
	}
	if strings.Count(output, "uperseded") != 2 {
		t.Errorf("output = %q, want the supersession said exactly twice: the status word and the run it names", output)
	}
}

// TestPipelineGetSaysSupersededWithoutNamingARunTheServerDidNotReport pins the
// absent case: the superseding run has been removed by retention, so there is
// no number to quote. The run is still superseded, and saying nothing at all
// would read as a run somebody cancelled.
func TestPipelineGetSaysSupersededWithoutNamingARunTheServerDidNotReport(t *testing.T) {
	run := supersededPipelineRun()
	run.SupersededByRunNumber = nil
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: run}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "Superseded: by a newer run the platform no longer reports") {
		t.Errorf("output = %q, want the supersession named without a run number", output)
	}
	if strings.Contains(output, "#0") {
		t.Errorf("output = %q, must never print a run number nothing reported", output)
	}
}

// TestPipelineGetOfACancelledRunNamesNoSupersession pins the contrast: a run a
// person stopped carries no class, so it stays "cancelled" and names nothing.
func TestPipelineGetOfACancelledRunNamesNoSupersession(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-17", RunNumber: 17, Status: "concluded", Outcome: strPipelinePtr("cancelled"),
		Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("c", 40),
		QueuedAt: "2026-09-15T00:00:00Z",
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if strings.Contains(output, "Superseded") {
		t.Errorf("output = %q, a person's cancel names no supersession", output)
	}
	if !strings.Contains(output, "cancelled") {
		t.Errorf("output = %q, a person's cancel keeps its own word", output)
	}
}

// TestPipelineGetNamesWhoCancelledTheRunAndWhy pins ankra-57z1w's CLI half
// (PLA-866 ask j): a cancelled run says who stopped it and why, under a Status
// line that used to be the whole answer. Smartoptics had two cancelled runs
// and no way to tell a person's press from the concurrency policy.
func TestPipelineGetNamesWhoCancelledTheRunAndWhy(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-17", RunNumber: 17, Status: "concluded", Outcome: strPipelinePtr("cancelled"),
		Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("c", 40),
		QueuedAt:     "2026-09-15T00:00:00Z",
		CancelledBy:  strPipelinePtr("github:octocat"),
		CancelReason: strPipelinePtr("source_control"),
		CancelledAt:  strPipelinePtr("2026-09-15T00:06:00Z"),
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, "Cancelled: by github:octocat (source_control)") {
		t.Errorf("output = %q, want the actor and the reason under the status line", output)
	}
}

// TestPipelineGetOfACancelledRunSaysNothingTheServerDidNotRecord pins the
// absent case, which is the whole estate of runs cancelled before the platform
// recorded any of this, plus every run read from an older server. Printing a
// Cancelled line with nobody in it would read as the platform having done it.
func TestPipelineGetOfACancelledRunSaysNothingTheServerDidNotRecord(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-17", RunNumber: 17, Status: "concluded", Outcome: strPipelinePtr("cancelled"),
		Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("c", 40),
		QueuedAt: "2026-09-15T00:00:00Z",
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if strings.Contains(output, "Cancelled:") {
		t.Errorf("output = %q, want no Cancelled line for a run the server attributed to nobody", output)
	}
}

// TestPipelineGetOfASupersededRunSaysTheSupersessionOnce pins that the
// supersession keeps its own line and does not also print as a cancellation:
// a supersession records concurrency:<run id> as its actor, and the Superseded
// line already says that in the words the run's author needs.
func TestPipelineGetOfASupersededRunSaysTheSupersessionOnce(t *testing.T) {
	run := supersededPipelineRun()
	run.CancelledBy = strPipelinePtr("concurrency:run-18")
	run.CancelReason = strPipelinePtr("superseded")
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{PipelineRun: run}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if strings.Contains(output, "Cancelled:") {
		t.Errorf("output = %q, want the supersession said once, on the Superseded line", output)
	}
	if !strings.Contains(output, "Superseded: by run #18") {
		t.Errorf("output = %q, want the Superseded line kept", output)
	}
}

// TestPipelineListShowsSupersededInTheStatusCell pins the listing half: the
// STATUS column reads superseded rather than cancelled, so a page of runs
// says which of its stopped runs were simply replaced.
func TestPipelineListShowsSupersededInTheStatusCell(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{
		Runs: []client.PipelineRun{supersededPipelineRun()},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "list", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, "superseded") {
		t.Errorf("output = %q, want the STATUS cell to read superseded", output)
	}
}

// TestPipelineGetJSONCarriesTheSupersession pins that -o json passes both
// fields through, so a script can follow the run that took the place of this
// one without reading the human output.
func TestPipelineGetJSONCarriesTheSupersession(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{
		PipelineRun: supersededPipelineRun(),
	}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-17",
		"--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	for _, want := range []string{`"superseded_by_run_id": "run-18"`, `"superseded_by_run_number": 18`} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want %s", output, want)
		}
	}
}

// TestPipelineRunDetailPrintsWhyAQueuedRunIsWaiting pins ankra-a0yh3's ask:
// a queued run has to say what it is waiting for where a person reads the run.
// The reported case is Smartoptics watching four repositories' runs sit behind
// one pull request's for up to thirty-eight minutes, with "Queued: 14 minutes
// ago" as the only answer any surface gave.
func TestPipelineRunDetailPrintsWhyAQueuedRunIsWaiting(t *testing.T) {
	var waiting bytes.Buffer
	printPipelineRunDetail(&waiting, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			RunNumber: 71, ID: "run-71", Status: "queued",
			QueuedAt: time.Now().Add(-14 * time.Minute).UTC().Format(time.RFC3339),
		},
		QueueReason: "waiting_on_ci_workers",
		QueueReasonMessage: "The agent on cluster \"build-01\" runs no pipeline-step workers " +
			"(ci_worker_count is 0), so nothing can claim this run's steps.",
	}, client.PipelineSelector{})
	rendered := waiting.String()
	if !strings.Contains(rendered, "Waiting:   The agent on cluster \"build-01\" runs no pipeline-step workers") {
		t.Fatalf("a queued run prints what it is waiting for, got:\n%s", rendered)
	}
	if strings.Index(rendered, "Queued:") > strings.Index(rendered, "Waiting:") {
		t.Fatalf("the wait is read under the time it has been queued for, got:\n%s", rendered)
	}
}

// TestPipelineRunDetailPrintsNoWaitingLineWhenNothingIsWaiting holds the other
// half. A run that is not queued has nothing to wait for, so there is no line;
// but a server that could not derive the reason says so, because no line at
// all reads as "nothing is blocking this run".
func TestPipelineRunDetailPrintsNoWaitingLineWhenNothingIsWaiting(t *testing.T) {
	outcome := "success"
	var concluded bytes.Buffer
	printPipelineRunDetail(&concluded, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 72, ID: "run-72", Status: "concluded", Outcome: &outcome},
	}, client.PipelineSelector{})
	if strings.Contains(concluded.String(), "Waiting:") {
		t.Fatalf("a concluded run is not waiting for anything, got:\n%s", concluded.String())
	}

	var unreadable bytes.Buffer
	printPipelineRunDetail(&unreadable, client.PipelineRunDetail{
		PipelineRun:            client.PipelineRun{RunNumber: 73, ID: "run-73", Status: "queued"},
		QueueReasonUnavailable: "Why this run is still queued could not be read; read the run again.",
	}, client.PipelineSelector{})
	if !strings.Contains(unreadable.String(), "Waiting:   Why this run is still queued could not be read") {
		t.Fatalf("a derivation the server could not complete is reported, not hidden, got:\n%s",
			unreadable.String())
	}
}

// TestPipelineRunDetailNamesASupersededAttempt pins PLA-871's ask. A retried
// step is several rows under one key and the detail carries all of them, so
// the table printed two build rows that differed in nothing a reader could
// see - and the attempt Ankra threw away, the only one that says what went
// wrong, was the one with no way to read its log.
func TestPipelineRunDetailNamesASupersededAttempt(t *testing.T) {
	infraError, confined := "infra_error", "build_runtime_confined"
	message := "This cluster's node runtime confines the rootless image builder, so the build cannot run in-cluster."
	lost := pipelineStepFixture("step-build-attempt-1", "build-commerce-backend", "concluded", &infraError)
	lost.Stage, lost.Kind = "build", "build"
	lost.ErrorClass, lost.ErrorMessage = &confined, &message
	succeeded := pipelineStepFixture("step-build-attempt-2", "build-commerce-backend", "concluded",
		strPipelinePtr("success"))
	succeeded.Stage, succeeded.Kind, succeeded.Attempt = "build", "build", 2

	var output bytes.Buffer
	printPipelineRunDetail(&output, pipelineRunDetailFixture("concluded", strPipelinePtr("success"), lost, succeeded),
		client.PipelineSelector{ApplicationID: "application-7"})
	rendered := output.String()

	if !strings.Contains(rendered, "ATTEMPT") {
		t.Fatalf("the step table must distinguish attempts of one key, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Earlier attempts (1), superseded by a retry:") {
		t.Fatalf("a superseded attempt must be named, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Class: build_runtime_confined") {
		t.Fatalf("the superseded attempt's own class explains the retry, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "confines the rootless image builder") {
		t.Fatalf("the superseded attempt's own message is printed, got:\n%s", rendered)
	}
	// With the selector, because the printed command is worth printing only
	// if it runs: `pipeline logs` resolves no run without one, so a hint that
	// dropped it failed for every reader not standing in the checkout that
	// the selector can be inferred from.
	if !strings.Contains(rendered,
		"ankra pipeline logs run-44 --application application-7 --step step-build-attempt-1") {
		t.Fatalf("the command that reads the lost attempt's log names its row id and carries "+
			"the selector that resolves the run, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "--step step-build-attempt-2") {
		t.Fatalf("the newest attempt is what --step <key> already answers; it is not listed, got:\n%s", rendered)
	}
}

// TestPipelineRunDetailNamesTheLaneEachStepRanOn pins PLA-868's ask on this
// command. PipelineStep.Executor was on the wire from the start and nothing
// printed it, so the one run detail a person reads could not say that a build
// had left their cluster for Ankra's builders - the fact every other question
// about that step depends on, starting with where its log is.
func TestPipelineRunDetailNamesTheLaneEachStepRanOn(t *testing.T) {
	checkout := pipelineStepFixture("step-checkout", "checkout", "concluded", strPipelinePtr("success"))
	checkout.Stage, checkout.Kind, checkout.Executor = "build", "run", "in_cluster"
	build := pipelineStepFixture("step-build", "build", "running", nil)
	build.Stage, build.Kind, build.Executor = "build", "build", "platform_builders"
	planned := pipelineStepFixture("step-publish", "publish", "blocked", nil)
	planned.Stage, planned.Kind = "publish", "publish"

	var output bytes.Buffer
	printPipelineRunDetail(&output, pipelineRunDetailFixture("running", nil, checkout, build, planned),
		client.PipelineSelector{})
	rendered := output.String()

	if !strings.Contains(rendered, "EXECUTOR") {
		t.Fatalf("the step table must name the lane each step ran on, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "platform_builders") || !strings.Contains(rendered, "in_cluster") {
		t.Fatalf("the lane is printed in the platform's own vocabulary, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "On Ankra's platform builders: build") {
		t.Fatalf("a step on Ankra's builders must be called out under the table, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "no live log stream") {
		t.Fatalf("the note must say why 'pipeline logs' cannot tail it, got:\n%s", rendered)
	}
}

// A run whose steps all ran in its own cluster grows no note: there is
// nothing to explain, and most runs are this one.
func TestPipelineRunDetailSaysNothingAboutPlatformBuildersWhenNoStepUsedThem(t *testing.T) {
	checkout := pipelineStepFixture("step-checkout", "checkout", "concluded", strPipelinePtr("success"))
	checkout.Executor = "in_cluster"

	var output bytes.Buffer
	printPipelineRunDetail(&output, pipelineRunDetailFixture("concluded", strPipelinePtr("success"), checkout),
		client.PipelineSelector{})
	if strings.Contains(output.String(), "On Ankra's platform builders") {
		t.Fatalf("a run that never left its cluster explains no fallback, got:\n%s", output.String())
	}
}

// TestPipelineRunDetailOrdersEarlierAttemptsByAttempt pins the chronology the
// block exists to explain. The rows arrive in whatever order the server's
// query returned them, and this lane already declines to inherit that order
// where it decides which attempt is newest (newestPipelineStepAttempt); a
// section listing attempt 2 above attempt 1 would be the same trust, in the
// one place a reader is reading for sequence.
func TestPipelineRunDetailOrdersEarlierAttemptsByAttempt(t *testing.T) {
	infraError := "infra_error"
	first := pipelineStepFixture("step-build-attempt-1", "build", "concluded", &infraError)
	second := pipelineStepFixture("step-build-attempt-2", "build", "concluded", &infraError)
	second.Attempt = 2
	newest := pipelineStepFixture("step-build-attempt-3", "build", "concluded", strPipelinePtr("success"))
	newest.Attempt = 3

	var output bytes.Buffer
	// Handed over newest-first, which is exactly the order this must not keep.
	printPipelineRunDetail(&output, pipelineRunDetailFixture("concluded", strPipelinePtr("success"),
		newest, second, first), client.PipelineSelector{})
	rendered := output.String()

	if !strings.Contains(rendered, "Earlier attempts (2), superseded by a retry:") {
		t.Fatalf("both superseded attempts must be named, got:\n%s", rendered)
	}
	if strings.Index(rendered, "build attempt 1:") > strings.Index(rendered, "build attempt 2:") {
		t.Fatalf("earlier attempts are listed oldest first, got:\n%s", rendered)
	}
}

// TestPipelineRunDetailPrintsNoSelectorItWasNotGiven holds the other half of
// the hint: an empty selector prints no flag rather than an empty one. It is
// reachable only through a caller that resolved no selector, and
// "--application " with nothing after it would be worse than no hint at all.
func TestPipelineRunDetailPrintsNoSelectorItWasNotGiven(t *testing.T) {
	infraError := "infra_error"
	lost := pipelineStepFixture("step-build-attempt-1", "build", "concluded", &infraError)
	succeeded := pipelineStepFixture("step-build-attempt-2", "build", "concluded", strPipelinePtr("success"))
	succeeded.Attempt = 2

	var output bytes.Buffer
	printPipelineRunDetail(&output, pipelineRunDetailFixture("concluded", strPipelinePtr("success"), lost, succeeded),
		client.PipelineSelector{})
	rendered := output.String()

	if !strings.Contains(rendered, "ankra pipeline logs run-44 --step step-build-attempt-1") {
		t.Fatalf("with no selector resolved the command prints none, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "--application ") || strings.Contains(rendered, "--repository ") {
		t.Fatalf("an empty selector prints no flag at all, got:\n%s", rendered)
	}
}

// TestPipelineRunDetailSaysNothingAboutAttemptsWhenNothingWasRetried holds the
// other half: almost every run has one attempt per step, and those must not
// grow a block explaining a retry that never happened.
func TestPipelineRunDetailSaysNothingAboutAttemptsWhenNothingWasRetried(t *testing.T) {
	checkout := pipelineStepFixture("step-checkout", "checkout", "concluded", strPipelinePtr("success"))
	build := pipelineStepFixture("step-build", "build", "concluded", strPipelinePtr("success"))

	var output bytes.Buffer
	printPipelineRunDetail(&output, pipelineRunDetailFixture("concluded", strPipelinePtr("success"), checkout, build),
		client.PipelineSelector{})
	rendered := output.String()

	if strings.Contains(rendered, "Earlier attempts") {
		t.Fatalf("a run with no retried step explains no retry, got:\n%s", rendered)
	}
}

// seedPipelineDefinitionCommit makes a checkout whose committed
// .ankra/pipeline.yaml differs from the one in the working tree, and makes it
// the working directory. The two contents are what tells a `--ref` read apart
// from a working-tree read.
func seedPipelineDefinitionCommit(t *testing.T, committedYAML string, workingTreeYAML string) {
	t.Helper()
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/service.git")
	definitionDirectory := filepath.Join(repositoryPath, ".ankra")
	if makeError := os.MkdirAll(definitionDirectory, 0o750); makeError != nil {
		t.Fatalf("creating .ankra: %v", makeError)
	}
	definitionPath := filepath.Join(definitionDirectory, "pipeline.yaml")
	if writeError := os.WriteFile(definitionPath, []byte(committedYAML), 0o600); writeError != nil {
		t.Fatalf("writing the committed definition: %v", writeError)
	}
	runTestGit(t, repositoryPath, "add", ".ankra/pipeline.yaml")
	runTestGit(t, repositoryPath, "-c", "user.email=test@example.com", "-c", "user.name=Test",
		"commit", "-m", "Add the pipeline definition")
	if writeError := os.WriteFile(definitionPath, []byte(workingTreeYAML), 0o600); writeError != nil {
		t.Fatalf("writing the working-tree definition: %v", writeError)
	}
	t.Chdir(repositoryPath)
}

// TestPipelineValidateReadsTheDefinitionAtAGitRef pins the candidate check
// the customer asked for: until now the only way to find out whether a
// change was valid was to merge it to the default branch and run it
// (PLA-863). The ref is read from the repository, not the working tree.
func TestPipelineValidateReadsTheDefinitionAtAGitRef(t *testing.T) {
	seedPipelineDefinitionCommit(t,
		"apiVersion: ankra.io/v1\nkind: Pipeline\nname: committed\n",
		"apiVersion: ankra.io/v1\nkind: Pipeline\nname: working-tree\n")
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{Severity: "ok", Events: []client.PipelineEventPlan{}}}

	_, executeError := runPipelineCommand(t, mockClient, "validate", "--ref", "main", "--application", testApplicationID)

	if executeError != nil {
		t.Fatalf("validate at a ref = %v", executeError)
	}
	if !strings.Contains(mockClient.validateSpecYAML, "name: committed") {
		t.Errorf("spec yaml = %q, want the definition as committed on the ref", mockClient.validateSpecYAML)
	}
}

// TestPipelineValidateAtARefReportsAMissingDefinition pins that a ref without
// the file is an error, not a quiet fall-back to whatever is stored
// server-side: "ok" would answer a question the caller did not ask.
func TestPipelineValidateAtARefReportsAMissingDefinition(t *testing.T) {
	seedPipelineDefinitionCommit(t,
		"apiVersion: ankra.io/v1\nkind: Pipeline\nname: committed\n",
		"apiVersion: ankra.io/v1\nkind: Pipeline\nname: working-tree\n")
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{Severity: "ok", Events: []client.PipelineEventPlan{}}}

	_, executeError := runPipelineCommand(t, mockClient, "validate", ".ankra/absent.yaml",
		"--ref", "main", "--application", testApplicationID)

	if executeError == nil {
		t.Fatal("validate at a ref without the file = nil, want an error")
	}
	if mockClient.validateSpecYAML != "" {
		t.Errorf("spec yaml = %q, want nothing sent when the file is not on the ref", mockClient.validateSpecYAML)
	}
}

// TestPipelineValidateSpecFileFlagMatchesTheArgument pins that --spec-file is
// the flag spelling of the positional file, so a caller that already writes
// `pipeline run --spec-file` does not have to learn a second shape.
func TestPipelineValidateSpecFileFlagMatchesTheArgument(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{Severity: "ok", Events: []client.PipelineEventPlan{}}}
	fixturePath := writePipelineFixture(t)

	_, executeError := runPipelineCommand(t, mockClient, "validate", "--spec-file", fixturePath,
		"--application", testApplicationID)

	if executeError != nil {
		t.Fatalf("validate --spec-file = %v", executeError)
	}
	if mockClient.validateSpecYAML != "apiVersion: ankra.io/v1\nkind: Pipeline\n" {
		t.Errorf("spec yaml = %q", mockClient.validateSpecYAML)
	}
}

func TestPipelineValidateRefusesTheDefinitionNamedTwice(t *testing.T) {
	mockClient := &pipelineLaneMock{validateResult: &client.PipelineValidation{Severity: "ok", Events: []client.PipelineEventPlan{}}}
	fixturePath := writePipelineFixture(t)

	_, executeError := runPipelineCommand(t, mockClient, "validate", fixturePath, "--spec-file", fixturePath,
		"--application", testApplicationID)

	if executeError == nil {
		t.Fatal("naming the definition twice = nil, want a usage error")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}
