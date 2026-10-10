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

// pipelineTrainMock records the merge train calls on top of pipelineLaneMock.
type pipelineTrainMock struct {
	pipelineLaneMock

	train           *client.PipelineMergeTrain
	settingsRequest *client.SetPipelineMergeTrainSettingsRequest
	enqueueRequest  *client.EnqueuePipelineMergeTrainRequest
	dequeuedEntryID string

	// repositoryPages answers ListPipelineRepositories one page per call, in
	// order; repositoryListings records what each call asked for.
	repositoryPages    []client.PipelineRepositoryList
	repositoryError    error
	repositoryListings []client.ListPipelineRepositoriesOptions
}

func (mock *pipelineTrainMock) ListPipelineRepositories(_ context.Context,
	options client.ListPipelineRepositoriesOptions) (*client.PipelineRepositoryList, error) {
	mock.repositoryListings = append(mock.repositoryListings, options)
	if mock.repositoryError != nil {
		return nil, mock.repositoryError
	}
	index := len(mock.repositoryListings) - 1
	if index >= len(mock.repositoryPages) {
		return &client.PipelineRepositoryList{Repositories: []client.PipelineRepository{}}, nil
	}
	page := mock.repositoryPages[index]
	return &page, nil
}

// noApplicationsListing is an applications listing read to its end that binds
// nothing, the shape of the cluster and ankra-cli repositories today.
const noApplicationsListing = `{"result":[],"pagination":{"total_pages":1}}`

func pipelineRepositoryRow(id string, owner string, name string) client.PipelineRepository {
	return client.PipelineRepository{ID: id, Provider: "github", Owner: owner, Name: name}
}

func (mock *pipelineTrainMock) GetPipelineMergeTrain(_ context.Context,
	selector client.PipelineSelector) (*client.PipelineMergeTrain, error) {
	mock.lastSelector = selector
	return mock.train, nil
}

func (mock *pipelineTrainMock) SetPipelineMergeTrainSettings(_ context.Context, selector client.PipelineSelector,
	request client.SetPipelineMergeTrainSettingsRequest) (*client.PipelineMergeTrainSettings, error) {
	mock.lastSelector = selector
	mock.settingsRequest = &request
	maxCars := 1
	if request.MaxCars != nil {
		maxCars = *request.MaxCars
	}
	return &client.PipelineMergeTrainSettings{Enabled: request.Enabled, MaxCars: maxCars}, nil
}

func (mock *pipelineTrainMock) EnqueuePipelineMergeTrain(_ context.Context, selector client.PipelineSelector,
	request client.EnqueuePipelineMergeTrainRequest) (*client.PipelineMergeTrainEntry, error) {
	mock.lastSelector = selector
	mock.enqueueRequest = &request
	return &client.PipelineMergeTrainEntry{ID: "entry-1", PullRequestNumber: request.PullRequestNumber,
		Status: "queued"}, nil
}

func (mock *pipelineTrainMock) DequeuePipelineMergeTrain(_ context.Context, selector client.PipelineSelector,
	entryID string) (*client.PipelineMergeTrainEntry, error) {
	mock.lastSelector = selector
	mock.dequeuedEntryID = entryID
	return &client.PipelineMergeTrainEntry{ID: entryID, PullRequestNumber: 7, Status: "cancelled"}, nil
}

func runPipelineTrainCommand(t *testing.T, mock *pipelineTrainMock, arguments ...string) (string, error) {
	t.Helper()
	previous := apiClient
	apiClient = mock
	t.Cleanup(func() { apiClient = previous })
	command := newPipelineTrainCommand()
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs(arguments)
	runError := command.ExecuteContext(context.Background())
	return output.String(), runError
}

func TestPipelineTrainAddPinsTheHeadAndAcceptsAHashPrefix(t *testing.T) {
	mock := &pipelineTrainMock{}
	sha := strings.Repeat("ab", 20)
	output, runError := runPipelineTrainCommand(t, mock, "add", "#42", "--repository", testPipelineRepositoryID,
		"--head-sha", sha)
	if runError != nil {
		t.Fatalf("add: %v", runError)
	}
	if mock.enqueueRequest == nil || mock.enqueueRequest.PullRequestNumber != 42 || mock.enqueueRequest.HeadSHA != sha {
		t.Fatalf("enqueue request = %+v", mock.enqueueRequest)
	}
	if mock.lastSelector.RepositoryID != testPipelineRepositoryID {
		t.Fatalf("selector = %+v", mock.lastSelector)
	}
	if !strings.Contains(output, "Pull request #42 is in the merge train") {
		t.Fatalf("output = %q", output)
	}
}

func TestPipelineTrainAddRefusesANonNumber(t *testing.T) {
	mock := &pipelineTrainMock{}
	if _, runError := runPipelineTrainCommand(t, mock, "add", "feature-branch", "--repository",
		testPipelineRepositoryID); runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("expected a usage error, got %v", runError)
	}
	if mock.enqueueRequest != nil {
		t.Fatal("nothing is sent for an invalid pull request number")
	}
}

func TestPipelineTrainEnableSendsMaxCarsOnlyWhenGiven(t *testing.T) {
	mock := &pipelineTrainMock{}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	if mock.settingsRequest == nil || !mock.settingsRequest.Enabled || mock.settingsRequest.MaxCars != nil {
		t.Fatalf("an enable without --max-cars keeps the current value, got %+v", mock.settingsRequest)
	}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--max-cars", "3", "--repository",
		testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable --max-cars: %v", runError)
	}
	if mock.settingsRequest.MaxCars == nil || *mock.settingsRequest.MaxCars != 3 {
		t.Fatalf("settings request = %+v", mock.settingsRequest)
	}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--max-cars", "9", "--repository",
		testPipelineRepositoryID); runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("--max-cars 9 is a usage error, got %v", runError)
	}
	if _, runError := runPipelineTrainCommand(t, mock, "disable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("disable: %v", runError)
	}
	if mock.settingsRequest.Enabled {
		t.Fatal("disable switches the train off")
	}
}

func TestPipelineTrainListShowsTheQueueInOrderAndWhatLeft(t *testing.T) {
	commit := strings.Repeat("c", 40)
	runID := "run-1"
	reason := "Merged by the merge train."
	isMatch := true
	mock := &pipelineTrainMock{train: &client.PipelineMergeTrain{
		Settings: client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 2},
		Entries: []client.PipelineMergeTrainEntry{
			{ID: "entry-1", PullRequestNumber: 11, PullRequestTitle: "First", Status: "testing", Attempt: 1,
				CarCommitSHA: &commit, PipelineRunID: &runID},
			{ID: "entry-2", PullRequestNumber: 12, PullRequestTitle: "Second", Status: "queued"},
		},
		Recent: []client.PipelineMergeTrainEntry{
			{ID: "entry-0", PullRequestNumber: 10, Status: "merged", ReasonText: &reason, MergeCommitSHA: &commit,
				MergeTreeMatches: &isMatch},
		},
	}}
	output, runError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	for _, want := range []string{"Merge train: on, 2 car(s)", "#11 First", "testing", "ccccccc", "#12 Second",
		"Recently left the train", "#10", "yes"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Index(output, "#11") > strings.Index(output, "#12") {
		t.Fatalf("the first car is listed first:\n%s", output)
	}

	jsonOutput, jsonError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "json")
	if jsonError != nil {
		t.Fatalf("list -o json: %v", jsonError)
	}
	var decoded client.PipelineMergeTrain
	if unmarshalError := json.Unmarshal([]byte(jsonOutput), &decoded); unmarshalError != nil ||
		len(decoded.Entries) != 2 {
		t.Fatalf("json output = %q (%v)", jsonOutput, unmarshalError)
	}
}

func TestPipelineTrainRemoveNamesTheEntry(t *testing.T) {
	mock := &pipelineTrainMock{}
	output, runError := runPipelineTrainCommand(t, mock, "remove", "entry-9", "--repository", testPipelineRepositoryID)
	if runError != nil || mock.dequeuedEntryID != "entry-9" || !strings.Contains(output, "left the merge train") {
		t.Fatalf("remove = %q %v %q", mock.dequeuedEntryID, runError, output)
	}
}

// TestPipelineTrainFindsTheCheckoutsRepositoryWhenNoApplicationIsBound is
// ankra-q573dh.37: a connected repository with no application bound to it
// (cluster, ankra-cli) used to answer "one of --application or --repository
// is required" from inside its own checkout, so nobody could tell whether it
// had a train. The repository is now looked up by the origin's owner/name.
func TestPipelineTrainFindsTheCheckoutsRepositoryWhenNoApplicationIsBound(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/platform.git")
	mock := &pipelineTrainMock{repositoryPages: []client.PipelineRepositoryList{{
		Repositories: []client.PipelineRepository{pipelineRepositoryRow(testPipelineRepositoryID, "Acme", "Platform")},
	}}}
	mock.applicationsListing = noApplicationsListing
	output, runError := runPipelineTrainCommand(t, mock, "add", "7")
	if runError != nil {
		t.Fatalf("add inside an unbound checkout = %v, want the repository looked up", runError)
	}
	if mock.lastSelector.RepositoryID != testPipelineRepositoryID || mock.lastSelector.ApplicationID != "" {
		t.Fatalf("selector = %+v, want the repository the origin names", mock.lastSelector)
	}
	if len(mock.repositoryListings) != 1 {
		t.Fatalf("repository listing read %d times, want once", len(mock.repositoryListings))
	}
	if asked := mock.repositoryListings[0]; asked.Provider != "github" || asked.Owner != "acme" || asked.Name != "platform" {
		t.Errorf("listing asked for %+v, want github acme/platform", asked)
	}
	if !strings.Contains(output, "Using the pipeline repository Acme/Platform") {
		t.Errorf("output = %q, want the repository it resolved to named", output)
	}
}

// TestPipelineTrainMatchesTheRepositoryOnAPlatformThatIgnoresTheFilter pins
// the backward-compatible arm: a platform older than the owner/name filter
// answers the whole listing, a page at a time, and the CLI must still pick
// the checkout's repository out of it - not the first row it is handed.
func TestPipelineTrainMatchesTheRepositoryOnAPlatformThatIgnoresTheFilter(t *testing.T) {
	seedPipelineCheckout(t, "git@github.com:acme/platform.git")
	nextCursor := "cursor-2"
	mock := &pipelineTrainMock{repositoryPages: []client.PipelineRepositoryList{
		{Repositories: []client.PipelineRepository{
			pipelineRepositoryRow("repo-other", "acme", "platform-docs"),
			pipelineRepositoryRow("repo-elsewhere", "other", "platform"),
		}, NextCursor: &nextCursor},
		{Repositories: []client.PipelineRepository{
			pipelineRepositoryRow(testPipelineRepositoryID, "acme", "platform"),
		}},
	}}
	mock.applicationsListing = noApplicationsListing
	if _, runError := runPipelineTrainCommand(t, mock, "enable"); runError != nil {
		t.Fatalf("enable = %v", runError)
	}
	if mock.lastSelector.RepositoryID != testPipelineRepositoryID {
		t.Fatalf("selector = %+v, want the matching row from the second page", mock.lastSelector)
	}
	if len(mock.repositoryListings) != 2 || mock.repositoryListings[1].Cursor != nextCursor {
		t.Fatalf("listing calls = %+v, want the second page read by its cursor", mock.repositoryListings)
	}
}

// TestPipelineTrainSaysWhenTheCheckoutsRepositoryIsNotConnected pins the
// three answers: a listing read to its end without the repository is "not
// connected" (exitNotFound, which a caller may read as "no train"), and a
// listing that could not be read is an error that claims nothing about it.
func TestPipelineTrainSaysWhenTheCheckoutsRepositoryIsNotConnected(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/platform.git")

	notConnected := &pipelineTrainMock{repositoryPages: []client.PipelineRepositoryList{{
		Repositories: []client.PipelineRepository{pipelineRepositoryRow("repo-other", "acme", "other")},
	}}}
	notConnected.applicationsListing = noApplicationsListing
	_, runError := runPipelineTrainCommand(t, notConnected, "add", "7")
	if runError == nil || exitCodeFor(runError) != exitNotFound ||
		!strings.Contains(runError.Error(), "acme/platform is not connected to Ankra Pipelines") {
		t.Fatalf("an unconnected repository = %v (exit %d), want a not-found naming it", runError, exitCodeFor(runError))
	}
	if notConnected.enqueueRequest != nil {
		t.Fatal("nothing is enqueued when the repository is not connected")
	}

	unreadable := &pipelineTrainMock{repositoryError: errors.New("Internal error")}
	unreadable.applicationsListing = noApplicationsListing
	_, runError = runPipelineTrainCommand(t, unreadable, "add", "7")
	if runError == nil || exitCodeFor(runError) == exitNotFound || !strings.Contains(runError.Error(), "Internal error") {
		t.Fatalf("an unreadable listing = %v (exit %d), want its error, never a not-found",
			runError, exitCodeFor(runError))
	}
}

// TestPipelineTrainStillPrefersTheBoundApplication pins that the repository
// lookup is a fallback: a checkout whose origin has one application bound
// keeps resolving to that application, and the repository listing is never
// read.
func TestPipelineTrainStillPrefersTheBoundApplication(t *testing.T) {
	seedPipelineCheckout(t, "https://github.com/acme/payments.git")
	mock := &pipelineTrainMock{}
	mock.applicationsListing = applicationBoundTo("acme", "payments")
	if _, runError := runPipelineTrainCommand(t, mock, "add", "7"); runError != nil {
		t.Fatalf("add = %v", runError)
	}
	if mock.lastSelector.ApplicationID != testApplicationID || mock.lastSelector.RepositoryID != "" {
		t.Fatalf("selector = %+v, want the bound application", mock.lastSelector)
	}
	if len(mock.repositoryListings) != 0 {
		t.Fatalf("repository listing read %d times, want never", len(mock.repositoryListings))
	}
}

// TestPipelineTrainOutsideACheckoutStillAsksForAFlag pins that nothing is
// guessed without an origin to read.
func TestPipelineTrainOutsideACheckoutStillAsksForAFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	mock := &pipelineTrainMock{}
	_, runError := runPipelineTrainCommand(t, mock, "add", "7")
	if runError == nil || exitCodeFor(runError) != exitUsage ||
		runError.Error() != "one of --application or --repository is required" {
		t.Fatalf("outside a checkout = %v, want the usage error", runError)
	}
	if len(mock.repositoryListings) != 0 {
		t.Fatal("the repository listing must not be read without an origin")
	}
}
