package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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
