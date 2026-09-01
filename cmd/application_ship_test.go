package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const (
	shipTestOrganisationID = "7d5c9a10-1111-4222-8333-444455556666"
	shipTestClusterID      = "9e8d7c6b-2222-4333-8444-555566667777"
	shipTestInstallationID = "1a2b3c4d-3333-4444-8555-666677778888"
)

// applicationShipMock plays the whole ship journey: each read hands out one
// scripted answer per poll (repeating the last), so a test walks the state
// machine exactly the way the platform would answer it.
type applicationShipMock struct {
	baseMock

	clusterOrganisationID string

	listApplicationsPayloads [][]byte
	listApplicationsCalls    int

	applicationDetailPayloads [][]byte
	applicationDetailCalls    int

	credentials         []client.Credential
	applicationResponse *client.CreateApplicationResponse
	createRequest       client.CreateApplicationRequest
	createCalls         int

	workflowRunsPayloads [][]byte
	workflowRunsCalls    int

	branchesPayload json.RawMessage

	startBuildPayload    json.RawMessage
	startBuildError      error
	startBuildRequest    client.StartApplicationPlatformBuildRequest
	startBuildCalls      int
	buildRequestPayloads [][]byte
	buildRequestCalls    int
	buildPayloads        [][]byte
	buildCalls           int

	deployPayload json.RawMessage
	deployRequest client.DeployApplicationRequest
	deployCalls   int

	installationsPayloads [][]byte
	installationsCalls    int

	deploymentsPayload json.RawMessage

	ingressItems           []interface{}
	eventItems             []interface{}
	resourceKindsRequested []string
	podsResponse           *client.ListPodsResponse
}

func (mock *applicationShipMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	organisationName := "Acme Org"
	return []client.OrganisationSummary{{
		OrganisationID: shipTestOrganisationID,
		Name:           &organisationName,
		UserCurrent:    true,
	}}, nil
}

func (mock *applicationShipMock) GetCluster(name string) (client.ClusterListItem, error) {
	clusterOrganisationID := mock.clusterOrganisationID
	if clusterOrganisationID == "" {
		clusterOrganisationID = shipTestOrganisationID
	}
	return client.ClusterListItem{ID: shipTestClusterID, Name: name, OrganisationID: clusterOrganisationID}, nil
}

func (mock *applicationShipMock) ListApplicationsRaw(requestContext context.Context,
	page int, pageSize int, search string) (json.RawMessage, error) {
	mock.listApplicationsCalls++
	return json.RawMessage(scriptedPayload(mock.listApplicationsPayloads, mock.listApplicationsCalls)), nil
}

func (mock *applicationShipMock) GetApplicationRaw(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	mock.applicationDetailCalls++
	return json.RawMessage(scriptedPayload(mock.applicationDetailPayloads, mock.applicationDetailCalls)), nil
}

func (mock *applicationShipMock) ListCredentials(provider *string) ([]client.Credential, error) {
	return mock.credentials, nil
}

func (mock *applicationShipMock) CreateApplication(requestContext context.Context,
	applicationRequest client.CreateApplicationRequest) (*client.CreateApplicationResponse, error) {
	mock.createCalls++
	mock.createRequest = applicationRequest
	return mock.applicationResponse, nil
}

func (mock *applicationShipMock) GetApplicationWorkflowRuns(requestContext context.Context,
	applicationID string, status string, page int, pageSize int) (json.RawMessage, error) {
	mock.workflowRunsCalls++
	return json.RawMessage(scriptedPayload(mock.workflowRunsPayloads, mock.workflowRunsCalls)), nil
}

func (mock *applicationShipMock) GetApplicationBranches(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	return mock.branchesPayload, nil
}

func (mock *applicationShipMock) StartApplicationPlatformBuild(requestContext context.Context,
	applicationID string, request client.StartApplicationPlatformBuildRequest) (json.RawMessage, error) {
	mock.startBuildCalls++
	mock.startBuildRequest = request
	return mock.startBuildPayload, mock.startBuildError
}

func (mock *applicationShipMock) GetApplicationPlatformBuildRequest(requestContext context.Context,
	applicationID string, buildRequestID string) (json.RawMessage, error) {
	mock.buildRequestCalls++
	return json.RawMessage(scriptedPayload(mock.buildRequestPayloads, mock.buildRequestCalls)), nil
}

func (mock *applicationShipMock) GetApplicationPlatformBuild(requestContext context.Context,
	applicationID string, buildID string) (json.RawMessage, error) {
	mock.buildCalls++
	return json.RawMessage(scriptedPayload(mock.buildPayloads, mock.buildCalls)), nil
}

func (mock *applicationShipMock) DeployApplication(requestContext context.Context,
	applicationID string, deployRequest client.DeployApplicationRequest) (json.RawMessage, error) {
	mock.deployCalls++
	mock.deployRequest = deployRequest
	return mock.deployPayload, nil
}

func (mock *applicationShipMock) GetApplicationInstallations(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	mock.installationsCalls++
	return json.RawMessage(scriptedPayload(mock.installationsPayloads, mock.installationsCalls)), nil
}

func (mock *applicationShipMock) GetApplicationDeployments(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	return mock.deploymentsPayload, nil
}

func (mock *applicationShipMock) GetResources(clusterID string,
	request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	items := mock.ingressItems
	if len(request.ResourceRequests) == 1 {
		mock.resourceKindsRequested = append(mock.resourceKindsRequested, request.ResourceRequests[0].Kind)
		if request.ResourceRequests[0].Kind == "Event" {
			items = mock.eventItems
		}
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{{Items: items}}}, nil
}

func (mock *applicationShipMock) ListPods(clusterID string,
	options *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	if mock.podsResponse != nil {
		return mock.podsResponse, nil
	}
	return &client.ListPodsResponse{TotalPages: 1}, nil
}

// newApplicationShipMock scripts the whole happy path for a repository that
// is not registered yet: registration, setup with a merged PR, a green
// workflow run, a deploy that settles healthy, and a published ingress.
func newApplicationShipMock() *applicationShipMock {
	applicationID := testApplicationID
	acmeLogin := "acme"
	namespace := "shop"
	return &applicationShipMock{
		listApplicationsPayloads: [][]byte{
			[]byte(`{"result":[],"pagination":{"total_pages":1}}`),
		},
		credentials: []client.Credential{{
			ID: "credential-id", Name: "github-acme", Provider: "github",
			Available: true, AccountLogin: &acmeLogin,
		}},
		applicationResponse: &client.CreateApplicationResponse{
			ID: &applicationID, Errors: []client.ApplicationResourceError{},
		},
		applicationDetailPayloads: [][]byte{
			[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"creating","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main"}`),
			[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main","pull_request_url":"https://github.com/acme/shop/pull/1","pull_request_merged_at":"2026-09-01T10:00:00Z"}`),
		},
		workflowRunsPayloads: [][]byte{
			[]byte(`{"runs":[{"id":7,"status":"in_progress","conclusion":null,"branch":"main","html_url":"https://github.com/acme/shop/actions/runs/7"}]}`),
			[]byte(`{"runs":[{"id":7,"status":"completed","conclusion":"success","branch":"main","html_url":"https://github.com/acme/shop/actions/runs/7"}]}`),
		},
		deployPayload: []byte(`{"installation_id":"` + shipTestInstallationID + `","status":"pending","message":"Deploy started. Track progress under Deployments."}`),
		installationsPayloads: [][]byte{
			[]byte(`{"installations":[]}`),
			[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"` + namespace + `","status":"deploying"}]}`),
			[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"` + namespace + `","status":"healthy"}]}`),
		},
		deploymentsPayload: []byte(`{"deployments":[]}`),
		ingressItems: []interface{}{
			map[string]interface{}{
				"spec": map[string]interface{}{
					"rules": []interface{}{
						map[string]interface{}{"host": "shop.acme.ankra.cc"},
					},
				},
			},
		},
	}
}

// fastShipPolling shrinks every ship poll interval so the loop tests run in
// milliseconds instead of the production cadence.
func fastShipPolling(t *testing.T) {
	t.Helper()
	previousPoll := shipPollInterval
	previousMerge := shipMergePollInterval
	previousBuild := buildPollInterval
	shipPollInterval = time.Millisecond
	shipMergePollInterval = time.Millisecond
	buildPollInterval = time.Millisecond
	t.Cleanup(func() {
		shipPollInterval = previousPoll
		shipMergePollInterval = previousMerge
		buildPollInterval = previousBuild
	})
}

// runApplicationShipCommand executes ship against a temp checkout of
// acme/shop with stdout and stderr captured separately, because ship's
// contract splits them: progress on stderr, the result on stdout.
func runApplicationShipCommand(t *testing.T, mockClient APIClient,
	arguments ...string) (string, string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANKRA_ORG", "")
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/shop.git")
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	applicationCommand := newApplicationCommand()
	var output bytes.Buffer
	var progress bytes.Buffer
	applicationCommand.SetOut(&output)
	applicationCommand.SetErr(&progress)
	applicationCommand.SetArgs(append([]string{"ship", repositoryPath}, arguments...))
	executeError := applicationCommand.Execute()
	return output.String(), progress.String(), executeError
}

func TestApplicationShipCommandRegistered(t *testing.T) {
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() == "ship" {
			return
		}
	}
	t.Fatal("ship is not registered under application")
}

// Registering through ship must carry the identical contract as
// `application add`: every add flag exists on ship too.
func TestApplicationShipCarriesTheAddFlagContract(t *testing.T) {
	shipCommand := newApplicationShipCommand()
	newApplicationAddCommand().Flags().VisitAll(func(flag *pflag.Flag) {
		if shipCommand.Flags().Lookup(flag.Name) == nil {
			t.Errorf("ship is missing the add flag --%s", flag.Name)
		}
	})
}

func TestApplicationShipRegistersDeploysAndPrintsTheLiveURL(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.createCalls != 1 {
		t.Errorf("create calls = %d, want 1", mockClient.createCalls)
	}
	if mockClient.createRequest.RepositoryOwner != "acme" || mockClient.createRequest.RepositoryName != "shop" {
		t.Errorf("created repository = %s/%s, want acme/shop",
			mockClient.createRequest.RepositoryOwner, mockClient.createRequest.RepositoryName)
	}
	if mockClient.deployCalls != 1 {
		t.Errorf("deploy calls = %d, want 1", mockClient.deployCalls)
	}
	if mockClient.deployRequest.ClusterID != shipTestClusterID || mockClient.deployRequest.Namespace != "shop" {
		t.Errorf("deploy request = %+v, want cluster %s namespace shop",
			mockClient.deployRequest, shipTestClusterID)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout must end on the live URL: %q", output)
	}
	// The org-drift defence: the resolved organisation and cluster are named
	// before anything acts.
	if !strings.Contains(progress, "Acme Org") || !strings.Contains(progress, "production") {
		t.Errorf("progress must echo the organisation and cluster: %s", progress)
	}
	if !strings.Contains(progress, "Registered application") {
		t.Errorf("progress must say the application was registered: %s", progress)
	}
}

// The core design requirement: a re-run re-resolves state and continues from
// wherever the flow is - an already-registered application with a healthy
// installation must neither be created nor deployed again.
func TestApplicationShipIsIdempotentAcrossARerun(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.listApplicationsPayloads = [][]byte{
		[]byte(`{"result":[{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main"}],"pagination":{"total_pages":1}}`),
	}
	mockClient.applicationDetailPayloads = [][]byte{
		[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main","pull_request_url":"https://github.com/acme/shop/pull/1","pull_request_merged_at":"2026-09-01T10:00:00Z"}`),
	}
	mockClient.workflowRunsPayloads = [][]byte{
		[]byte(`{"runs":[{"id":7,"status":"completed","conclusion":"success","branch":"main","html_url":"https://github.com/acme/shop/actions/runs/7"}]}`),
	}
	mockClient.installationsPayloads = [][]byte{
		[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"shop","status":"healthy"}]}`),
	}
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.createCalls != 0 {
		t.Errorf("a registered application must not be created again; calls = %d", mockClient.createCalls)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("a healthy installation must not be deployed again; calls = %d", mockClient.deployCalls)
	}
	if !strings.Contains(progress, "Using existing application") {
		t.Errorf("progress must say the application was reused: %s", progress)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout = %q", output)
	}
}

// A status this CLI does not know is an in-flight deploy to follow, never a
// gap to deploy into: a platform that grows a new transitional state must
// not turn a re-run into a second deploy racing the first.
func TestApplicationShipFollowsAnUnknownInstallationStatus(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.installationsPayloads = [][]byte{
		[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"shop","status":"converging"}]}`),
		[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"shop","status":"healthy"}]}`),
	}
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("an unknown in-flight status must be followed, not deployed over; calls = %d", mockClient.deployCalls)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout = %q", output)
	}
}

// Registration-shaping flags do nothing against an already-registered
// application; passing one explicitly must be said out loud, not silently
// ignored.
func TestApplicationShipWarnsAboutIgnoredRegistrationFlags(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.listApplicationsPayloads = [][]byte{
		[]byte(`{"result":[{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main"}],"pagination":{"total_pages":1}}`),
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production",
		"--credential", "github-acme", "--registry-url", "oci://registry.example.com/shop")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.createCalls != 0 {
		t.Errorf("a registered application must not be created again; calls = %d", mockClient.createCalls)
	}
	if !strings.Contains(progress, "--credential") || !strings.Contains(progress, "--registry-url") ||
		!strings.Contains(progress, "ignored") {
		t.Errorf("explicitly-passed registration flags must be reported as ignored: %s", progress)
	}
}

func TestApplicationShipFailsWhenSetupFails(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.applicationDetailPayloads = [][]byte{
		[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"error","error_message":"the repository could not be analysed","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main"}`),
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a failed setup must fail the command\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "the repository could not be analysed") {
		t.Errorf("the platform's error must reach the caller: %v", executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("nothing may deploy after a failed setup; calls = %d", mockClient.deployCalls)
	}
}

func TestApplicationShipFailsOnAFailedWorkflowRunWithItsURL(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.workflowRunsPayloads = [][]byte{
		[]byte(`{"runs":[{"id":7,"status":"completed","conclusion":"failure","branch":"main","html_url":"https://github.com/acme/shop/actions/runs/7"}]}`),
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a failed workflow run must fail the command\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "https://github.com/acme/shop/actions/runs/7") {
		t.Errorf("the run URL is where to look and must be named: %v", executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("nothing may deploy without a green image; calls = %d", mockClient.deployCalls)
	}
}

// The merge gate is skipped entirely with --ankra-build: the builder clones
// the commit itself, so an unmerged setup PR must not block the ship.
func TestApplicationShipAnkraBuildSkipsTheMergeGate(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.applicationDetailPayloads = [][]byte{
		[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main","pull_request_url":"https://github.com/acme/shop/pull/1","pull_request_merged_at":null}`),
	}
	mockClient.branchesPayload = []byte(`{"branches":[{"name":"main","head_sha":"9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c"}],"configured_branch":"main"}`)
	mockClient.startBuildPayload = []byte(`{"request_id":"req-1","build_id":"build-9","already_requested":false}`)
	mockClient.buildPayloads = [][]byte{
		[]byte(`{"id":"build-9","status":"succeeded","recipe":"dockerfile","image_ref":"registry/shop:sha-9f4a1c2"}`),
	}
	output, progress, executeError := runApplicationShipCommand(t, mockClient,
		"--cluster", "production", "--ankra-build")
	if executeError != nil {
		t.Fatalf("ship --ankra-build error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.startBuildCalls != 1 {
		t.Fatalf("the platform build must be started; calls = %d", mockClient.startBuildCalls)
	}
	if mockClient.startBuildRequest.HeadSHA != "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c" ||
		mockClient.startBuildRequest.Ref != "main" {
		t.Errorf("build request = %+v, want the tracked branch's head commit", mockClient.startBuildRequest)
	}
	if mockClient.workflowRunsCalls != 0 {
		t.Errorf("--ankra-build must not wait on repository CI; workflow reads = %d", mockClient.workflowRunsCalls)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout = %q", output)
	}
}

// The build routes answer 404 for organisations without the platform_builds
// feature flag; that answer must be translated, not passed through as a bare
// not-found.
func TestApplicationShipAnkraBuildTranslatesTheFeatureFlag404(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.branchesPayload = []byte(`{"branches":[{"name":"main","head_sha":"9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c"}],"configured_branch":"main"}`)
	mockClient.startBuildError = client.NewUnexpectedResponseError(404, "Not Found")
	_, progress, executeError := runApplicationShipCommand(t, mockClient,
		"--cluster", "production", "--ankra-build")
	if executeError == nil {
		t.Fatalf("the 404 must fail the command\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "platform builds are not enabled for this organisation") {
		t.Errorf("the feature-flag 404 must be translated: %v", executeError)
	}
}

func TestApplicationShipFailedDeploySurfacesTheErrorAndEvents(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.installationsPayloads = [][]byte{
		[]byte(`{"installations":[]}`),
		[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"shop","status":"failed","error_message":"the chart could not be rendered"}]}`),
	}
	mockClient.eventItems = []interface{}{
		map[string]interface{}{
			"type": "Warning", "reason": "BackOff", "message": "Back-off pulling image",
			"involvedObject": map[string]interface{}{"kind": "Pod", "name": "shop-0"},
		},
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a failed installation must fail the command\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "the chart could not be rendered") {
		t.Errorf("the platform execution error must reach the caller: %v", executeError)
	}
	if !strings.Contains(progress, "Back-off pulling image") {
		t.Errorf("the namespace's warning events must be surfaced: %s", progress)
	}
}

// --timeout expiry exits with the wait-timeout code and points at the
// designed recovery: re-running ship resumes from the current state.
func TestApplicationShipTimeoutExitsResumable(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.installationsPayloads = [][]byte{
		[]byte(`{"installations":[{"id":"` + shipTestInstallationID + `","cluster_id":"` + shipTestClusterID + `","namespace":"shop","status":"deploying"}]}`),
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient,
		"--cluster", "production", "--timeout", "150ms")
	if executeError == nil {
		t.Fatalf("an expired --timeout must fail the command\nprogress: %s", progress)
	}
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Errorf("exit code = %d, want %d (wait timeout)", exitCodeFor(executeError), exitWaitTimeout)
	}
	if !strings.Contains(executeError.Error(), "re-run 'ankra application ship'") {
		t.Errorf("the expiry must carry the resumable hint: %v", executeError)
	}
}

func TestApplicationShipRefusesAClusterFromAnotherOrganisation(t *testing.T) {
	mockClient := newApplicationShipMock()
	mockClient.clusterOrganisationID = "00000000-9999-4999-8999-999999999999"
	_, _, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatal("a cluster from another organisation must be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(executeError), exitUsage)
	}
}

func TestApplicationShipStructuredOutputStaysParseable(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	output, progress, executeError := runApplicationShipCommand(t, mockClient,
		"--cluster", "production", "-o", "json")
	if executeError != nil {
		t.Fatalf("ship -o json error = %v\nprogress: %s", executeError, progress)
	}
	var result shipResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if result.ApplicationID != testApplicationID || result.URL != "https://shop.acme.ankra.cc" ||
		result.Namespace != "shop" || result.InstallationID != shipTestInstallationID || !result.Registered {
		t.Errorf("structured result = %+v", result)
	}
}

// The deployments surface is the primary source of the published URL: each
// row carries the ingress_host the platform derived, so ship must not need
// to read the namespace's Ingress resources when the row answers.
func TestApplicationShipReadsTheURLFromTheDeploymentsRow(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.deploymentsPayload = []byte(`{"deployments":[{"cluster_id":"` + shipTestClusterID +
		`","namespace":"shop","ingress_host":"shop.wma9ydel20.ankra.cc","ingress_host_publication":"publisher_present"}]}`)
	mockClient.ingressItems = nil
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if !strings.Contains(output, "Live: https://shop.wma9ydel20.ankra.cc") {
		t.Errorf("stdout must carry the deployment row's ingress_host: %q", output)
	}
	for _, kind := range mockClient.resourceKindsRequested {
		if kind == "Ingress" {
			t.Errorf("the namespace Ingress read is the fallback and must not run when the deployments row answers")
		}
	}
}

// A hostname whose publication verdict is publisher_absent will not resolve;
// it is still the deployment's URL, but the caveat must be said out loud.
func TestApplicationShipWarnsWhenNothingPublishesTheHost(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.deploymentsPayload = []byte(`{"deployments":[{"cluster_id":"` + shipTestClusterID +
		`","namespace":"shop","ingress_host":"shop.example.com","ingress_host_publication":"publisher_absent"}]}`)
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if !strings.Contains(output, "Live: https://shop.example.com") {
		t.Errorf("stdout = %q", output)
	}
	if !strings.Contains(progress, "nothing on the cluster publishes DNS") {
		t.Errorf("the publisher_absent verdict must be surfaced: %s", progress)
	}
}

// Deployment rows without an ingress_host (hand-installed add-ons, older
// platforms) fall back to the namespace's Ingress resources.
func TestApplicationShipFallsBackToTheNamespaceIngress(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.deploymentsPayload = []byte(`{"deployments":[{"cluster_id":"` + shipTestClusterID +
		`","namespace":"shop","ingress_host":null,"ingress_host_publication":null}]}`)
	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout must carry the ingress fallback's hostname: %q", output)
	}
}

func TestDefaultShipNamespace(t *testing.T) {
	testCases := []struct {
		applicationName string
		expected        string
	}{
		{"shop", "shop"},
		{"My Shop", "my-shop"},
		{"api_service.v2", "api-service-v2"},
		{"---", "default"},
		{"", "default"},
		{strings.Repeat("a", 70), strings.Repeat("a", 63)},
	}
	for _, testCase := range testCases {
		if derived := defaultShipNamespace(testCase.applicationName); derived != testCase.expected {
			t.Errorf("defaultShipNamespace(%q) = %q, want %q",
				testCase.applicationName, derived, testCase.expected)
		}
	}
}
