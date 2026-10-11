package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestPipelineRepositoriesConnectRequiresProviderOwnerName(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect")
	if executeError == nil {
		t.Fatal("expected --provider/--owner/--name to be required")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.connectRepositoryCalls != 0 {
		t.Errorf("ConnectPipelineRepository calls = %d, want 0", mockClient.connectRepositoryCalls)
	}
}

func TestPipelineRepositoriesConnectHappyPathPrintsRepositoryAndDefinition(t *testing.T) {
	mockClient := &pipelineLaneMock{connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
		PipelineRepository: client.PipelineRepository{
			ID: "repo-1", Provider: "github", Owner: "acme", Name: "webapp", DefaultBranch: "main",
		},
		Definition: client.PipelineRepositoryDefinitionOutcome{
			Status: "recorded", Detail: "Recorded the committed .ankra/pipeline.yaml as the pipeline definition of record.",
		},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "webapp", "--credential", "github-app")
	if executeError != nil {
		t.Fatalf("connect error = %v", executeError)
	}
	if mockClient.connectRepositoryRequest.Provider != "github" ||
		mockClient.connectRepositoryRequest.Owner != "acme" ||
		mockClient.connectRepositoryRequest.Name != "webapp" ||
		mockClient.connectRepositoryRequest.CredentialName != "github-app" {
		t.Errorf("request = %+v", mockClient.connectRepositoryRequest)
	}
	if !strings.Contains(output, "repo-1") || !strings.Contains(output, "acme/webapp") {
		t.Errorf("output = %q, want it to name the repository and its id", output)
	}
	if !strings.Contains(output, "recorded") {
		t.Errorf("output = %q, want the definition status", output)
	}
}

// TestPipelineRepositoriesConnectNormalisesBitbucketAlias pins that
// --provider bitbucket - the name a person types - reaches the wire as
// "bitbucket_cloud" (enginekit/pipelinerun.ProviderBitbucketCloud), the only
// value the server's provider vocabulary accepts for Bitbucket.
func TestPipelineRepositoriesConnectNormalisesBitbucketAlias(t *testing.T) {
	mockClient := &pipelineLaneMock{connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
		PipelineRepository: client.PipelineRepository{ID: "repo-1", Provider: "bitbucket_cloud", Owner: "acme", Name: "webapp"},
	}}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "bitbucket", "--owner", "acme", "--name", "webapp")
	if executeError != nil {
		t.Fatalf("connect error = %v", executeError)
	}
	if mockClient.connectRepositoryRequest.Provider != "bitbucket_cloud" {
		t.Errorf("provider = %q, want the wire value bitbucket_cloud", mockClient.connectRepositoryRequest.Provider)
	}
}

// TestPipelineRepositoriesConnectAlreadyConnectedNamesTheExistingID pins that
// a duplicate connect is reported as an error carrying the server's own
// sentence (which already names the existing id) rather than a rewritten
// one, plus a hint that points at the existing repository.
func TestPipelineRepositoriesConnectAlreadyConnectedNamesTheExistingID(t *testing.T) {
	mockClient := &pipelineLaneMock{connectRepositoryError: &client.PipelineRepositoryAlreadyConnectedError{
		Detail:       "This repository is already connected to pipelines (repository repo-existing)",
		RepositoryID: "repo-existing",
	}}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "webapp")
	if executeError == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(executeError.Error(), "already connected to pipelines") {
		t.Errorf("error = %q, want the server's sentence verbatim", executeError.Error())
	}
	if !strings.Contains(executeError.Error(), "repo-existing") {
		t.Errorf("error = %q, want it to name the existing repository id", executeError.Error())
	}
}

func TestPipelineRepositoriesListEmpty(t *testing.T) {
	mockClient := &pipelineLaneMock{listRepositoriesResult: &client.PipelineRepositoryList{Repositories: []client.PipelineRepository{}}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "list")
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, "No pipeline repositories connected") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineRepositoriesListRendersTableAndPassesFilters(t *testing.T) {
	mockClient := &pipelineLaneMock{listRepositoriesResult: &client.PipelineRepositoryList{Repositories: []client.PipelineRepository{
		{ID: "repo-1", Provider: "github", Owner: "acme", Name: "webapp", DefaultBranch: "main", CreatedAt: "2026-09-01T00:00:00Z"},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "list", "--provider", "github", "--limit", "5")
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, "repo-1") || !strings.Contains(output, "acme/webapp") {
		t.Errorf("output = %q", output)
	}
	if mockClient.listRepositoriesOptions.Provider != "github" || mockClient.listRepositoriesOptions.Limit != 5 {
		t.Errorf("list options = %+v", mockClient.listRepositoriesOptions)
	}
}

func TestPipelineRepositoriesListNormalisesBitbucketFilter(t *testing.T) {
	mockClient := &pipelineLaneMock{listRepositoriesResult: &client.PipelineRepositoryList{Repositories: []client.PipelineRepository{}}}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "list", "--provider", "bitbucket")
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if mockClient.listRepositoriesOptions.Provider != "bitbucket_cloud" {
		t.Errorf("provider filter = %q, want the wire value bitbucket_cloud", mockClient.listRepositoriesOptions.Provider)
	}
}

func TestPipelineRepositoriesGetRequiresAUUID(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "get", "acme/webapp")
	if executeError == nil {
		t.Fatal("expected a non-uuid argument to be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if !strings.Contains(executeError.Error(), "there is no lookup by owner/name") {
		t.Errorf("error = %q, want it to explain the gap", executeError.Error())
	}
	if mockClient.getRepositoryID != "" {
		t.Errorf("GetPipelineRepository was called with %q despite the refusal", mockClient.getRepositoryID)
	}
}

func TestPipelineRepositoriesGetHappyPath(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{getRepositoryResult: &client.PipelineRepository{
		ID: repositoryID, Provider: "github", Owner: "acme", Name: "webapp", DefaultBranch: "main",
	}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "get", repositoryID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if mockClient.getRepositoryID != repositoryID {
		t.Errorf("repository id = %q", mockClient.getRepositoryID)
	}
	if !strings.Contains(output, "acme/webapp") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineRepositoriesGetNotFound(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{getRepositoryError: errors.New("Pipeline repository not found")}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "get", repositoryID)
	if executeError == nil || executeError.Error() != "Pipeline repository not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineRepositoriesGetShowsApplicationAndCICluster(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	applicationID := "app-1"
	clusterID := "cluster-1"
	mockClient := &pipelineLaneMock{getRepositoryResult: &client.PipelineRepository{
		ID: repositoryID, Provider: "github", Owner: "acme", Name: "webapp", DefaultBranch: "main",
		ApplicationID: &applicationID, ClusterID: &clusterID,
	}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "get", repositoryID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if !strings.Contains(output, applicationID) || !strings.Contains(output, clusterID) {
		t.Errorf("output = %q, want it to show the application and CI cluster", output)
	}
}

func TestPipelineRepositoriesListShowsCICluster(t *testing.T) {
	clusterID := "cluster-1"
	mockClient := &pipelineLaneMock{listRepositoriesResult: &client.PipelineRepositoryList{Repositories: []client.PipelineRepository{
		{ID: "repo-1", Provider: "github", Owner: "acme", Name: "webapp", DefaultBranch: "main", ClusterID: &clusterID},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "list")
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	if !strings.Contains(output, clusterID) {
		t.Errorf("output = %q, want the CI cluster column populated", output)
	}
}

// TestPipelineRepositoriesConnectPassesApplicationAndCluster pins that
// --application/--cluster (cluster PR #2509) reach the request unmodified.
func TestPipelineRepositoriesConnectPassesApplicationAndCluster(t *testing.T) {
	applicationID := "app-1"
	clusterID := "cluster-1"
	mockClient := &pipelineLaneMock{connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
		PipelineRepository: client.PipelineRepository{
			ID: "repo-1", Provider: "github", Owner: "acme", Name: "webapp",
			ApplicationID: &applicationID, ClusterID: &clusterID,
		},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "webapp",
		"--application", applicationID, "--cluster", clusterID)
	if executeError != nil {
		t.Fatalf("connect error = %v", executeError)
	}
	if mockClient.connectRepositoryRequest.ApplicationID != applicationID ||
		mockClient.connectRepositoryRequest.ClusterID != clusterID {
		t.Errorf("request = %+v", mockClient.connectRepositoryRequest)
	}
	if !strings.Contains(output, applicationID) || !strings.Contains(output, clusterID) {
		t.Errorf("output = %q, want it to show the linked application and CI cluster", output)
	}
}

func TestPipelineRepositoriesConnectApplicationNotFound(t *testing.T) {
	mockClient := &pipelineLaneMock{connectRepositoryError: errors.New("Application not found in this organisation")}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "webapp", "--application", "00000000-0000-0000-0000-000000000000")
	if executeError == nil || executeError.Error() != "Application not found in this organisation" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineRepositoriesConnectClusterCannotRunPipelines(t *testing.T) {
	mockClient := &pipelineLaneMock{connectRepositoryError: errors.New("This cluster's agent cannot run pipeline steps")}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "webapp", "--cluster", "00000000-0000-0000-0000-000000000000")
	if executeError == nil || executeError.Error() != "This cluster's agent cannot run pipeline steps" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineRepositoriesDisconnectRequiresAUUID(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "disconnect", "acme/webapp", "--yes")
	if executeError == nil {
		t.Fatal("expected a non-uuid argument to be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.disconnectRepositoryCalls != 0 {
		t.Errorf("DisconnectPipelineRepository calls = %d, want 0", mockClient.disconnectRepositoryCalls)
	}
}

func TestPipelineRepositoriesDisconnectHappyPathWithYes(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "disconnect", repositoryID, "--yes")
	if executeError != nil {
		t.Fatalf("disconnect error = %v", executeError)
	}
	if mockClient.disconnectRepositoryID != repositoryID || mockClient.disconnectRepositoryCalls != 1 {
		t.Errorf("disconnect id = %q, calls = %d", mockClient.disconnectRepositoryID, mockClient.disconnectRepositoryCalls)
	}
	if !strings.Contains(output, repositoryID) || !strings.Contains(output, "disconnected") {
		t.Errorf("output = %q", output)
	}
}

// TestPipelineRepositoriesDisconnectDeclinedConfirmationDoesNotCall pins that
// disconnect is a destructive command that confirms first
// (CLAUDE.md "Destructive commands confirm first"), matching
// TestPipelineSchedulesDeleteConfirms's pattern for the sibling delete.
func TestPipelineRepositoriesDisconnectDeclinedConfirmationDoesNotCall(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	var output bytes.Buffer
	pipelineCommand.SetOut(&output)
	pipelineCommand.SetErr(&output)
	pipelineCommand.SetIn(strings.NewReader("n\n"))
	pipelineCommand.SetArgs([]string{"repositories", "disconnect", repositoryID})
	executeError := pipelineCommand.Execute()
	if executeError == nil || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("declined disconnect error = %v", executeError)
	}
	if mockClient.disconnectRepositoryCalls != 0 {
		t.Errorf("DisconnectPipelineRepository was called despite the decline")
	}
}

// TestPipelineRepositoriesDisconnectHasLiveRun pins that the 409
// "queued or running" refusal (cluster PR #2509) is reported verbatim with a
// non-zero exit, not swallowed as a soft success.
func TestPipelineRepositoriesDisconnectHasLiveRun(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{
		disconnectRepositoryError: errors.New("This repository has a pipeline run that is queued or running"),
	}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "disconnect", repositoryID, "--yes")
	if executeError == nil || executeError.Error() != "This repository has a pipeline run that is queued or running" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineRepositoriesDisconnectNotFound(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{disconnectRepositoryError: errors.New("Pipeline repository not found")}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "disconnect", repositoryID, "--yes")
	if executeError == nil || executeError.Error() != "Pipeline repository not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

// TestPipelineRepositoriesDisconnectStructuredOutputStaysCleanOfThePrompt
// pins the Ankra AI review finding on this PR (cmd/pipeline_repositories.go,
// commit cb02d17464eb): confirmPrompt must write to stderr, not stdout, so
// that '--format json' without '--yes' still leaves stdout as one parseable
// document instead of the prompt text followed by the JSON.
func TestPipelineRepositoriesDisconnectStructuredOutputStaysCleanOfThePrompt(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineLaneMock{}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	var stdout, stderr bytes.Buffer
	pipelineCommand.SetOut(&stdout)
	pipelineCommand.SetErr(&stderr)
	pipelineCommand.SetIn(strings.NewReader("y\n"))
	pipelineCommand.SetArgs([]string{"repositories", "disconnect", repositoryID, "--output", "json"})
	if executeError := pipelineCommand.Execute(); executeError != nil {
		t.Fatalf("disconnect error = %v", executeError)
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout = %q, want it to be exactly one valid JSON document with no prompt text mixed in", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("stderr = %q, want the confirmation prompt", stderr.String())
	}
	if mockClient.disconnectRepositoryCalls != 1 {
		t.Errorf("DisconnectPipelineRepository calls = %d, want 1", mockClient.disconnectRepositoryCalls)
	}
}

func TestPipelineRepositoriesSetClusterResolvesTheNameAndSendsIt(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	clusterID := "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e"
	mockClient := &pipelineSetClusterMock{
		clusters: []client.ClusterListItem{{ID: clusterID, Name: "ci-hel1-b"}},
		result: &client.PipelineRepository{ID: repositoryID, Provider: "github", Owner: "acme", Name: "webapp",
			DefaultBranch: "main", ClusterID: &clusterID},
	}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "set-cluster", repositoryID,
		"--cluster", "ci-hel1-b")
	if executeError != nil {
		t.Fatalf("set-cluster error = %v", executeError)
	}
	if mockClient.repositoryID != repositoryID || mockClient.clusterID != clusterID {
		t.Errorf("sent repository %q cluster %q", mockClient.repositoryID, mockClient.clusterID)
	}
	if !strings.Contains(output, clusterID) {
		t.Errorf("output = %q, want the repository's new CI cluster", output)
	}
}

// An empty --cluster clears the override: the client is asked for no cluster,
// which it sends as an explicit null.
func TestPipelineRepositoriesSetClusterWithAnEmptyValueClearsIt(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	mockClient := &pipelineSetClusterMock{result: &client.PipelineRepository{ID: repositoryID}}
	if _, executeError := runPipelineCommand(t, mockClient, "repositories", "set-cluster", repositoryID,
		"--cluster", ""); executeError != nil {
		t.Fatalf("set-cluster error = %v", executeError)
	}
	if !mockClient.called || mockClient.clusterID != "" {
		t.Errorf("expected a clearing call, got called=%v cluster=%q", mockClient.called, mockClient.clusterID)
	}
}

func TestPipelineRepositoriesSetClusterRequiresTheFlagAndAUUID(t *testing.T) {
	mockClient := &pipelineSetClusterMock{}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "set-cluster",
		"3fa85f64-5717-4562-b3fc-2c963f66afa6")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error without --cluster, got %v", executeError)
	}
	_, executeError = runPipelineCommand(t, mockClient, "repositories", "set-cluster", "acme/webapp",
		"--cluster", "x")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error for a non-id repository, got %v", executeError)
	}
	if mockClient.called {
		t.Error("nothing is sent for a refused invocation")
	}
}

// pipelineSetClusterMock records the repository cluster change.
type pipelineSetClusterMock struct {
	baseMock

	clusters     []client.ClusterListItem
	result       *client.PipelineRepository
	called       bool
	repositoryID string
	clusterID    string
}

func (m *pipelineSetClusterMock) SetPipelineRepositoryCluster(ctx context.Context, repositoryID string,
	clusterID string) (*client.PipelineRepository, error) {
	m.called = true
	m.repositoryID = repositoryID
	m.clusterID = clusterID
	return m.result, nil
}

func (m *pipelineSetClusterMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	response := &client.ClusterListResponse{Result: m.clusters}
	response.Pagination.TotalPages = 1
	return response, nil
}

// --workspaces-only reaches the wire, and the result says the repository is
// connected for Workspaces alone.
func TestPipelineRepositoriesConnectWorkspacesOnlySendsTheModeAndSaysSo(t *testing.T) {
	isWorkspacesOnly := true
	mockClient := &pipelineLaneMock{connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
		PipelineRepository: client.PipelineRepository{
			ID: "repo-1", Provider: "github", Owner: "acme", Name: "agent", DefaultBranch: "main",
			WorkspacesOnly: &isWorkspacesOnly,
		},
		Definition: client.PipelineRepositoryDefinitionOutcome{Status: "workspaces_only", Detail: "not read"},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "agent", "--credential", "github-app", "--workspaces-only")
	if executeError != nil {
		t.Fatalf("connect error = %v", executeError)
	}
	if !mockClient.connectRepositoryRequest.WorkspacesOnly {
		t.Errorf("request = %+v, want workspaces_only", mockClient.connectRepositoryRequest)
	}
	if !strings.Contains(output, "workspaces only") || mockClient.disconnectRepositoryCalls != 0 {
		t.Errorf("output = %q after %d disconnects, want the mode and no disconnect", output,
			mockClient.disconnectRepositoryCalls)
	}
}

// A connect without the flag never names the member, so the body is what it
// always was.
func TestConnectPipelineRepositoryRequestLeavesWorkspacesOnlyOutWhenFalse(t *testing.T) {
	body, marshalError := json.Marshal(client.ConnectPipelineRepositoryRequest{Provider: "github", Owner: "a", Name: "b"})
	if marshalError != nil || strings.Contains(string(body), "workspaces_only") {
		t.Fatalf("body = %s (%v), want no workspaces_only member", body, marshalError)
	}
}

// A platform that predates the mode ignores the member and connects an
// ordinary pipeline repository; the CLI disconnects it again and fails rather
// than leave a repository whose pushes start runs.
func TestPipelineRepositoriesConnectWorkspacesOnlyUndoesAConnectThePlatformDidNotHonour(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		workspacesOnly *bool
	}{
		{name: "the platform predates the mode", workspacesOnly: nil},
		{name: "the platform answered false", workspacesOnly: new(bool)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mockClient := &pipelineLaneMock{connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
				PipelineRepository: client.PipelineRepository{ID: "repo-1", Provider: "github", Owner: "acme",
					Name: "agent", WorkspacesOnly: testCase.workspacesOnly},
			}}
			_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
				"--provider", "github", "--owner", "acme", "--name", "agent", "--workspaces-only")
			if executeError == nil || !strings.Contains(executeError.Error(), "disconnected again") {
				t.Errorf("error = %v, want the connect refused as disconnected again", executeError)
			}
			if mockClient.disconnectRepositoryCalls != 1 || mockClient.disconnectRepositoryID != "repo-1" {
				t.Errorf("disconnects = %d of %q, want one of repo-1", mockClient.disconnectRepositoryCalls,
					mockClient.disconnectRepositoryID)
			}
		})
	}
}

// When the undo itself fails, the error names the repository to disconnect.
func TestPipelineRepositoriesConnectWorkspacesOnlyNamesTheRepositoryWhenTheUndoFails(t *testing.T) {
	mockClient := &pipelineLaneMock{
		connectRepositoryResult: &client.ConnectPipelineRepositoryResult{
			PipelineRepository: client.PipelineRepository{ID: "repo-1", Owner: "acme", Name: "agent"},
		},
		disconnectRepositoryError: errors.New("boom"),
	}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "connect",
		"--provider", "github", "--owner", "acme", "--name", "agent", "--workspaces-only")
	if executeError == nil || !strings.Contains(executeError.Error(), "ankra pipeline repositories disconnect repo-1") {
		t.Errorf("error = %v, want the disconnect command for repo-1", executeError)
	}
}

// pipelineSetModeMock records the repository mode change.
type pipelineSetModeMock struct {
	baseMock

	called           bool
	repositoryID     string
	isWorkspacesOnly bool
	answered         *bool
	err              error
}

func (m *pipelineSetModeMock) SetPipelineRepositoryWorkspacesOnly(ctx context.Context, repositoryID string,
	isWorkspacesOnly bool) (*client.PipelineRepository, error) {
	m.called, m.repositoryID, m.isWorkspacesOnly = true, repositoryID, isWorkspacesOnly
	if m.err != nil {
		return nil, m.err
	}
	answered := m.answered
	if answered == nil {
		answered = &isWorkspacesOnly
	}
	return &client.PipelineRepository{ID: repositoryID, Owner: "acme", Name: "agent", WorkspacesOnly: answered}, nil
}

func TestPipelineRepositoriesSetModeSendsEachMode(t *testing.T) {
	repositoryID := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	for _, testCase := range []struct {
		mode       string
		want       bool
		wantOutput string
	}{
		{mode: "workspaces-only", want: true, wantOutput: "workspaces only"},
		{mode: "pipelines", want: false, wantOutput: "pipelines"},
	} {
		mockClient := &pipelineSetModeMock{}
		output, executeError := runPipelineCommand(t, mockClient, "repositories", "set-mode", repositoryID, testCase.mode)
		if executeError != nil {
			t.Fatalf("set-mode %s error = %v", testCase.mode, executeError)
		}
		if !mockClient.called || mockClient.repositoryID != repositoryID || mockClient.isWorkspacesOnly != testCase.want {
			t.Errorf("set-mode %s sent %+v", testCase.mode, mockClient)
		}
		if !strings.Contains(output, "Mode:           "+testCase.wantOutput) {
			t.Errorf("set-mode %s output = %q", testCase.mode, output)
		}
	}
}

func TestPipelineRepositoriesSetModeRefusesAnUnknownModeOrAnID(t *testing.T) {
	mockClient := &pipelineSetModeMock{}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "set-mode",
		"3fa85f64-5717-4562-b3fc-2c963f66afa6", "sometimes")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("unknown mode: error = %v, want a usage error", executeError)
	}
	_, executeError = runPipelineCommand(t, mockClient, "repositories", "set-mode", "acme/agent", "pipelines")
	if exitCodeFor(executeError) != exitUsage || mockClient.called {
		t.Errorf("owner/name: error = %v called=%v, want a usage error and no call", executeError, mockClient.called)
	}
}

// A platform that answers without the mode applied (one that predates it
// would refuse the body; this guards a silent no-op) fails the command.
func TestPipelineRepositoriesSetModeFailsWhenTheModeWasNotApplied(t *testing.T) {
	mockClient := &pipelineSetModeMock{answered: new(bool)}
	_, executeError := runPipelineCommand(t, mockClient, "repositories", "set-mode",
		"3fa85f64-5717-4562-b3fc-2c963f66afa6", "workspaces-only")
	if executeError == nil || !strings.Contains(executeError.Error(), "without applying the mode") {
		t.Errorf("error = %v, want the unapplied mode reported", executeError)
	}
}
