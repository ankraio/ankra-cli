package cmd

// Tests for ship's build-lane selection (ankra-q9j68): an application built
// on Ankra Pipelines must be followed through its pipeline run, not through
// GitHub Actions workflow runs it never has.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const shipTestHeadSHA = "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c"

// shipPipelineMock adds the pipeline-run reads and the dispatch to the ship
// journey. Each ListPipelineRuns call hands out the next scripted page
// (repeating the last); run details are looked up by id, each id with its own
// script.
type shipPipelineMock struct {
	*applicationShipMock

	runPages [][]client.PipelineRun
	// cursorPages, when set, answers by cursor instead of by call: the page
	// for "" first, then the page each NextCursor names.
	cursorPages map[string]client.PipelineRunList
	// runsByHead, when set, answers by the listing's head_sha filter.
	runsByHead map[string][]client.PipelineRun
	// branchesPayloads, when set, scripts the branch reads in order
	// (repeating the last) instead of the base mock's single payload.
	branchesPayloads [][]byte
	branchesCalls    int
	listCalls     int
	listOptions   []client.ListPipelineRunsOptions
	runDetails    map[string][]*client.PipelineRunDetail
	detailCalls   map[string]int
	dispatches    []client.CreatePipelineRunRequest
	dispatchedRun *client.CreatePipelineRunResult
}

func (mock *shipPipelineMock) ListPipelineRuns(_ context.Context, selector client.PipelineSelector,
	options client.ListPipelineRunsOptions) (*client.PipelineRunList, error) {
	if selector.ApplicationID != testApplicationID {
		return nil, client.NewUnexpectedResponseError(404, "wrong selector")
	}
	mock.listCalls++
	mock.listOptions = append(mock.listOptions, options)
	if mock.cursorPages != nil {
		page := mock.cursorPages[options.Cursor]
		return &page, nil
	}
	if mock.runsByHead != nil {
		return &client.PipelineRunList{Runs: mock.runsByHead[options.HeadSHA]}, nil
	}
	if len(mock.runPages) == 0 {
		return &client.PipelineRunList{}, nil
	}
	index := mock.listCalls - 1
	if index >= len(mock.runPages) {
		index = len(mock.runPages) - 1
	}
	return &client.PipelineRunList{Runs: mock.runPages[index]}, nil
}

func (mock *shipPipelineMock) GetApplicationBranches(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	if len(mock.branchesPayloads) == 0 {
		return mock.applicationShipMock.GetApplicationBranches(requestContext, applicationID)
	}
	mock.branchesCalls++
	return json.RawMessage(scriptedPayload(mock.branchesPayloads, mock.branchesCalls)), nil
}

func (mock *shipPipelineMock) GetPipelineRun(_ context.Context, _ client.PipelineSelector,
	runID string) (*client.PipelineRunDetail, error) {
	script := mock.runDetails[runID]
	if len(script) == 0 {
		return nil, client.NewUnexpectedResponseError(404, "no such run")
	}
	mock.detailCalls[runID]++
	index := mock.detailCalls[runID] - 1
	if index >= len(script) {
		index = len(script) - 1
	}
	return script[index], nil
}

func (mock *shipPipelineMock) CreatePipelineRun(_ context.Context, _ client.PipelineSelector,
	request client.CreatePipelineRunRequest) (*client.CreatePipelineRunResult, error) {
	mock.dispatches = append(mock.dispatches, request)
	return mock.dispatchedRun, nil
}

// newShipPipelineMock is the ship journey for an ankra_pipeline application
// whose setup pull request is still open - nothing on this lane waits for it.
func newShipPipelineMock(source string) *shipPipelineMock {
	base := newApplicationShipMock()
	base.applicationDetailPayloads = [][]byte{
		[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main","pull_request_url":"https://github.com/acme/shop/pull/1","pull_request_merged_at":null,"pipeline_source":"` + source + `"}`),
	}
	base.branchesPayload = []byte(`{"branches":[{"name":"main","head_sha":"` + shipTestHeadSHA + `"}],"configured_branch":"main"}`)
	return &shipPipelineMock{
		applicationShipMock: base,
		runDetails:          map[string][]*client.PipelineRunDetail{},
		detailCalls:         map[string]int{},
	}
}

func shipPipelineRun(id string, number int64, trigger string) client.PipelineRun {
	return client.PipelineRun{ID: id, RunNumber: number, Trigger: trigger, HeadSHA: shipTestHeadSHA, Status: "queued"}
}

func shipRunDetail(run client.PipelineRun, status string, outcome string, steps ...client.PipelineStep) *client.PipelineRunDetail {
	run.Status = status
	if outcome != "" {
		run.Outcome = &outcome
	}
	return &client.PipelineRunDetail{PipelineRun: run, Steps: steps}
}

func shipStep(kind string, attempt int16, outcome string) client.PipelineStep {
	step := client.PipelineStep{Stage: kind, StepKey: kind, Kind: kind, Attempt: attempt, Status: "running"}
	if outcome != "" {
		step.Outcome = &outcome
		step.Status = "concluded"
	}
	return step
}

func shortPipelineGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	previous := shipPipelineRunAppearGrace
	shipPipelineRunAppearGrace = grace
	t.Cleanup(func() { shipPipelineRunAppearGrace = previous })
}

func TestApplicationShipFollowsThePipelineRunOfTheBranchHead(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, time.Hour)
	mockClient := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 4, "push")
	mockClient.runPages = [][]client.PipelineRun{{}, {push}}
	mockClient.runDetails["run-push"] = []*client.PipelineRunDetail{
		shipRunDetail(push, "running", "", shipStep("build", 1, ""), shipStep("publish", 1, "")),
		// The image exists once publish succeeded; ship does not wait for
		// the verify step that follows it.
		shipRunDetail(push, "running", "", shipStep("build", 1, "success"), shipStep("publish", 1, "success"),
			shipStep("verify", 1, "")),
	}

	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production", "-o", "json")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.workflowRunsCalls != 0 {
		t.Errorf("an Ankra Pipelines application must not wait on workflow runs; reads = %d", mockClient.workflowRunsCalls)
	}
	if strings.Contains(progress, "Waiting for the setup pull request") {
		t.Errorf("the pipeline lane must not wait on the setup pull request merge:\n%s", progress)
	}
	if len(mockClient.dispatches) != 0 {
		t.Errorf("a run the trigger started must not be dispatched again: %+v", mockClient.dispatches)
	}
	if mockClient.listOptions[0].HeadSHA != shipTestHeadSHA {
		t.Errorf("runs were listed with %+v, want the tracked branch's head commit", mockClient.listOptions[0])
	}
	if mockClient.deployCalls != 1 {
		t.Errorf("deploy calls = %d, want 1 once the image is published", mockClient.deployCalls)
	}
	if !strings.Contains(output, `"build_ref": "run-push"`) {
		t.Errorf("build_ref must name the pipeline run: %s", output)
	}
	if !strings.Contains(progress, "Ankra Pipelines built the image: run #4") {
		t.Errorf("progress must say which run built the image:\n%s", progress)
	}
}

func TestApplicationShipDispatchesARunWhenNothingStartsOne(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, 0)
	mockClient := newShipPipelineMock("ankra_pipeline")
	manual := shipPipelineRun("run-manual", 1, "api")
	mockClient.dispatchedRun = &client.CreatePipelineRunResult{PipelineRunID: "run-manual", RunNumber: 1}
	mockClient.runPages = [][]client.PipelineRun{{}, {manual}}
	mockClient.runDetails["run-manual"] = []*client.PipelineRunDetail{
		shipRunDetail(manual, "concluded", "success"),
	}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if len(mockClient.dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want exactly one run for the head commit", mockClient.dispatches)
	}
	if mockClient.dispatches[0].HeadSHA != shipTestHeadSHA || mockClient.dispatches[0].Ref != "main" {
		t.Errorf("dispatch = %+v, want the tracked branch's head commit", mockClient.dispatches[0])
	}
	if !strings.Contains(progress, "ship dispatched run #1") {
		t.Errorf("progress must say ship dispatched the run:\n%s", progress)
	}
	if mockClient.deployCalls != 1 {
		t.Errorf("deploy calls = %d, want 1", mockClient.deployCalls)
	}
}

func TestApplicationShipFailsOnAFailedPipelineRun(t *testing.T) {
	fastShipPolling(t)
	mockClient := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 4, "push")
	mockClient.runPages = [][]client.PipelineRun{{push}}
	mockClient.runDetails["run-push"] = []*client.PipelineRunDetail{
		shipRunDetail(push, "concluded", "failure", shipStep("build", 1, "failure"), shipStep("publish", 1, "skipped")),
	}
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a failed pipeline run must fail ship\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "run #4 concluded") ||
		!strings.Contains(executeError.Error(), "ankra application pipeline logs "+testApplicationID+" run-push") {
		t.Errorf("the error must name the run and where to look: %v", executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("nothing may deploy without an image; calls = %d", mockClient.deployCalls)
	}
}

func TestApplicationShipFollowsTheRunThatSupersededItsRun(t *testing.T) {
	fastShipPolling(t)
	mockClient := newShipPipelineMock("ankra_pipeline")
	first := shipPipelineRun("run-first", 4, "push")
	second := shipPipelineRun("run-second", 5, "api")
	superseded := shipRunDetail(first, "concluded", "cancelled")
	errorClass := pipelineErrorClassSuperseded
	superseded.ErrorClass = &errorClass
	mockClient.runPages = [][]client.PipelineRun{{first}, {second, first}}
	mockClient.runDetails["run-first"] = []*client.PipelineRunDetail{superseded}
	mockClient.runDetails["run-second"] = []*client.PipelineRunDetail{shipRunDetail(second, "concluded", "success")}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("a superseded run must be followed by its replacement, got %v\nprogress: %s", executeError, progress)
	}
	if mockClient.detailCalls["run-second"] == 0 {
		t.Error("the replacing run was never read")
	}
}

func TestApplicationShipIgnoresPullRequestRunsOfTheHeadCommit(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, time.Hour)
	mockClient := newShipPipelineMock("ankra_pipeline")
	pullRequest := shipPipelineRun("run-pr", 3, "pull_request")
	push := shipPipelineRun("run-push", 4, "push")
	mockClient.runPages = [][]client.PipelineRun{{pullRequest}, {push, pullRequest}}
	mockClient.runDetails["run-pr"] = []*client.PipelineRunDetail{shipRunDetail(pullRequest, "concluded", "failure")}
	mockClient.runDetails["run-push"] = []*client.PipelineRunDetail{shipRunDetail(push, "concluded", "success")}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if mockClient.detailCalls["run-pr"] != 0 {
		t.Error("a pull request run of the head commit is not the build ship waits for")
	}
}

func TestShipPipelineImagePublishedCountsTheLatestAttempt(t *testing.T) {
	run := shipPipelineRun("run", 1, "push")
	retried := shipRunDetail(run, "running", "", shipStep("publish", 1, "failure"), shipStep("publish", 2, "success"))
	if !shipPipelineImagePublished(retried) {
		t.Error("a publish that succeeded on retry has published the image")
	}
	pending := shipRunDetail(run, "running", "", shipStep("publish", 1, "failure"), shipStep("publish", 2, ""))
	if shipPipelineImagePublished(pending) {
		t.Error("a publish still retrying has not published the image")
	}
	noPublish := shipRunDetail(run, "running", "", shipStep("build", 1, "success"))
	if shipPipelineImagePublished(noPublish) {
		t.Error("a run without publish steps is ready only once it concludes successfully")
	}
	skipped := shipRunDetail(run, "concluded", "success", shipStep("build", 1, "success"), shipStep("publish", 1, "skipped"))
	if shipPipelineImagePublished(skipped) {
		t.Error("a run whose publish was skipped concludes successfully but has pushed no image")
	}
}

// The generated pipeline publishes only on push, so a run ship dispatched
// itself (trigger manual) skips publish and still concludes successfully.
// Ship must not take that as a built image and deploy.
func TestApplicationShipRefusesARunThatSkippedPublish(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, 0)
	mockClient := newShipPipelineMock("ankra_pipeline")
	manual := shipPipelineRun("run-manual", 1, "manual")
	mockClient.dispatchedRun = &client.CreatePipelineRunResult{PipelineRunID: "run-manual", RunNumber: 1}
	mockClient.runPages = [][]client.PipelineRun{{}, {manual}}
	skippedPublish := shipStep("publish", 1, "skipped")
	reason := `Stage "publish" runs only on push, and this run was triggered by manual.`
	skippedPublish.ErrorMessage = &reason
	mockClient.runDetails["run-manual"] = []*client.PipelineRunDetail{
		shipRunDetail(manual, "concluded", "success", shipStep("build", 1, "success"), skippedPublish),
	}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a run that skipped publish must fail ship\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), "does not publish the image") ||
		!strings.Contains(executeError.Error(), "runs only on push") {
		t.Errorf("the error must say the run publishes nothing, and why: %v", executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("nothing may deploy without an image; calls = %d", mockClient.deployCalls)
	}
}

// A push during the wait cancels the older commit's run as superseded, and
// the replacing run belongs to the new head. Ship must follow the branch to
// it instead of polling the superseded run until --timeout.
func TestApplicationShipFollowsTheBranchWhenAPushSupersedesItsRun(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, time.Hour)
	const newHead = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	mockClient := newShipPipelineMock("ankra_pipeline")
	mockClient.branchesPayloads = [][]byte{
		[]byte(`{"branches":[{"name":"main","head_sha":"` + shipTestHeadSHA + `"}],"configured_branch":"main"}`),
		[]byte(`{"branches":[{"name":"main","head_sha":"` + newHead + `"}],"configured_branch":"main"}`),
	}
	first := shipPipelineRun("run-first", 4, "push")
	second := shipPipelineRun("run-second", 5, "push")
	second.HeadSHA = newHead
	superseded := shipRunDetail(first, "concluded", "cancelled")
	errorClass := pipelineErrorClassSuperseded
	superseded.ErrorClass = &errorClass
	mockClient.runsByHead = map[string][]client.PipelineRun{
		shipTestHeadSHA: {first},
		newHead:         {second},
	}
	mockClient.runDetails["run-first"] = []*client.PipelineRunDetail{superseded}
	mockClient.runDetails["run-second"] = []*client.PipelineRunDetail{
		shipRunDetail(second, "concluded", "success", shipStep("publish", 1, "success")),
	}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship must follow the new head's run, got %v\nprogress: %s", executeError, progress)
	}
	if mockClient.detailCalls["run-second"] == 0 {
		t.Error("the new head's run was never read")
	}
	if !strings.Contains(progress, "moved to commit a1b2c3d") {
		t.Errorf("progress must say ship moved to the new head:\n%s", progress)
	}
	if mockClient.deployCalls != 1 {
		t.Errorf("deploy calls = %d, want 1", mockClient.deployCalls)
	}
}

// own_ci applications are built by the repository's own workflow, which does
// not live in the setup pull request: ship waits on its runs, not the merge.
func TestApplicationShipOwnCISkipsTheMergeGate(t *testing.T) {
	fastShipPolling(t)
	mockClient := newShipPipelineMock("own_ci")
	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if strings.Contains(progress, "Waiting for the setup pull request") {
		t.Errorf("own_ci must not wait on the setup pull request:\n%s", progress)
	}
	if mockClient.workflowRunsCalls == 0 || mockClient.listCalls != 0 {
		t.Errorf("own_ci waits on workflow runs (reads %d), not pipeline runs (reads %d)",
			mockClient.workflowRunsCalls, mockClient.listCalls)
	}
}

func TestApplicationShipRefusesAnApplicationNothingBuilds(t *testing.T) {
	fastShipPolling(t)
	mockClient := newShipPipelineMock("none")
	_, _, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil || !strings.Contains(executeError.Error(), "--ankra-build") {
		t.Fatalf("error = %v, want the refusal naming --ankra-build", executeError)
	}
	if mockClient.workflowRunsCalls != 0 || mockClient.deployCalls != 0 {
		t.Error("nothing may be waited on or deployed for an application nothing builds")
	}
}

func TestApplicationShipStopsOnAClosedSetupPullRequest(t *testing.T) {
	fastShipPolling(t)
	mockClient := newShipPipelineMock("generated_workflow")
	mockClient.applicationDetailPayloads = [][]byte{
		[]byte(`{"id":"` + testApplicationID + `","name":"shop","state":"up","app_repo_owner":"acme","app_repo_name":"shop","app_repo_branch":"main","pull_request_url":"https://github.com/acme/shop/pull/1","pull_request_merged_at":null,"pull_request_closed_at":"2026-09-02T10:00:00Z","pipeline_source":"generated_workflow"}`),
	}
	_, _, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil || !strings.Contains(executeError.Error(), "closed without merging") {
		t.Fatalf("error = %v, want the closed setup pull request named instead of a wait", executeError)
	}
	if mockClient.workflowRunsCalls != 0 {
		t.Errorf("workflow reads = %d, want none", mockClient.workflowRunsCalls)
	}
}

// Several pull request runs of the head commit must not hide the run that
// publishes, nor make ship dispatch a second one (review of #402).
func TestApplicationShipPagesPastPullRequestRuns(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, 0)
	mockClient := newShipPipelineMock("ankra_pipeline")
	pullRequests := []client.PipelineRun{}
	for number := int64(10); number < 30; number++ {
		pullRequests = append(pullRequests, shipPipelineRun("run-pr", number, "pull_request"))
	}
	push := shipPipelineRun("run-push", 4, "push")
	next := "cursor-2"
	mockClient.cursorPages = map[string]client.PipelineRunList{
		"":         {Runs: pullRequests, NextCursor: &next},
		"cursor-2": {Runs: []client.PipelineRun{push}},
	}
	mockClient.runDetails["run-push"] = []*client.PipelineRunDetail{shipRunDetail(push, "concluded", "success")}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship error = %v\nprogress: %s", executeError, progress)
	}
	if len(mockClient.dispatches) != 0 {
		t.Errorf("a run exists past the pull request runs; dispatches = %+v", mockClient.dispatches)
	}
	if mockClient.detailCalls["run-push"] == 0 {
		t.Error("the push run on the second page was never followed")
	}
}

func TestApplicationShipDoesNotDispatchWhenTheListingWasNotExhausted(t *testing.T) {
	fastShipPolling(t)
	shortPipelineGrace(t, 0)
	mockClient := newShipPipelineMock("ankra_pipeline")
	next := "again"
	// Every page is pull request runs with another page behind it: the walk
	// stops at its bound without having proved there is no run.
	mockClient.cursorPages = map[string]client.PipelineRunList{
		"":      {Runs: []client.PipelineRun{shipPipelineRun("run-pr", 3, "pull_request")}, NextCursor: &next},
		"again": {Runs: []client.PipelineRun{shipPipelineRun("run-pr", 3, "pull_request")}, NextCursor: &next},
	}
	_, _, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production", "--timeout", "50ms")
	if executeError == nil {
		t.Fatal("ship must keep waiting (and time out here) rather than succeed")
	}
	if len(mockClient.dispatches) != 0 {
		t.Errorf("an unexhausted listing is not proof of no run; dispatches = %+v", mockClient.dispatches)
	}
}
