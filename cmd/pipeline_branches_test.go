package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

// pipelineBranchesMock adds ListPipelineBranches to pipelineLaneMock without
// touching its shared definition in pipeline_test.go: Go resolves the method
// on the outer type before the embedded one, so this is the whole override.
type pipelineBranchesMock struct {
	pipelineLaneMock

	branchesOptions client.ListPipelineBranchesOptions
	branchesResult  *client.PipelineBranchList
	branchesError   error
	branchesCalls   int
}

func (mock *pipelineBranchesMock) ListPipelineBranches(ctx context.Context,
	selector client.PipelineSelector,
	options client.ListPipelineBranchesOptions) (*client.PipelineBranchList, error) {
	mock.lastSelector = selector
	mock.branchesOptions = options
	mock.branchesCalls++
	if mock.branchesError != nil {
		return nil, mock.branchesError
	}
	return mock.branchesResult, nil
}

func branchOutcome(value string) *string { return &value }

// branchPageCursor is a cursor in the shape the platform mints: the page
// position base64url-encoded, so a ref carrying a character a URL reads
// specially survives the round trip. The CLI treats it as opaque.
const branchPageCursor = "MjAyNi0wOS0xNVQwOTowMDowMFoscmVmcy9oZWFkcy9tYWlu"

// testPipelineRepositoryID is a UUID, because resolvePipelineSelector
// refuses a --repository that is not one.
const testPipelineRepositoryID = "3f6b2c91-4e07-4a5d-8b31-9c0d7e2a4f18"

// threeBranchPage is the fixture every table assertion reads: the default
// branch, a stale tag, and a pull request whose number belongs in its label.
func threeBranchPage() *client.PipelineBranchList {
	pullRequestNumber := int64(42)
	return &client.PipelineBranchList{Branches: []client.PipelineBranch{
		{
			Ref: "refs/heads/main", Name: "main", Kind: client.PipelineBranchKindBranch,
			IsDefault: true,
			LatestRun: client.PipelineRun{
				RunNumber: 12, Status: "concluded", Outcome: branchOutcome("success"),
			},
			PreviousOutcome: branchOutcome("failure"),
			RunCount:        12, SupersededCount: 3,
			LastActivityAt: "2026-09-15T09:00:00Z",
		},
		{
			Ref: "refs/pull/42/head", Name: "refs/pull/42/head",
			Kind: client.PipelineBranchKindPullRequest, PullRequestNumber: &pullRequestNumber,
			LatestRun: client.PipelineRun{
				RunNumber: 11, Status: "running",
			},
			RunCount:       2,
			LastActivityAt: "2026-09-15T08:00:00Z",
		},
		{
			Ref: "refs/tags/v1.0.0", Name: "v1.0.0", Kind: client.PipelineBranchKindTag,
			LatestRun: client.PipelineRun{
				RunNumber: 4, Status: "concluded", Outcome: branchOutcome("success"),
			},
			RunCount:       1,
			LastActivityAt: "2026-07-01T09:00:00Z",
			Stale:          true,
		},
	}}
}

func TestPipelineBranchesRegistered(t *testing.T) {
	pipelineCommand := newPipelineCommand()
	if findSubcommandOrNil(pipelineCommand, "branches") == nil {
		t.Fatal("pipeline subcommand \"branches\" is not registered")
	}
}

func TestPipelineBranchesTableCarriesEveryColumn(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	for _, expected := range []string{
		"BRANCH", "KIND", "LATEST RUN #", "STATUS", "OUTCOME", "PREVIOUS", "RUNS",
		"SUPERSEDED", "LAST ACTIVITY",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing the %s column: %q", expected, output)
		}
	}
	if !strings.Contains(output, "main (default)") {
		t.Errorf("the default branch must be marked in the BRANCH column: %q", output)
	}
	if !strings.Contains(output, "(#42)") {
		t.Errorf("a pull request row must spell its number out: %q", output)
	}
	if !strings.Contains(output, "failure") {
		t.Errorf("the PREVIOUS column must carry the previous outcome: %q", output)
	}
}

// TestPipelineBranchesLatestRunReadsSupersededLikeTheRunListing pins the two
// tables to one vocabulary: a latest run a newer run replaced reads
// "superseded" here exactly as it does in 'ankra pipeline list', never
// "cancelled" in one and "superseded" in the other for the same run.
func TestPipelineBranchesLatestRunReadsSupersededLikeTheRunListing(t *testing.T) {
	page := threeBranchPage()
	page.Branches[0].LatestRun = supersededPipelineRun()
	mockClient := &pipelineBranchesMock{branchesResult: page}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if !strings.Contains(output, "⊘ superseded") {
		t.Errorf("the OUTCOME cell of a superseded latest run must read superseded: %q", output)
	}
	if strings.Contains(output, "⊘ cancelled") {
		t.Errorf("a superseded run must not read cancelled anywhere in the table: %q", output)
	}
}

func TestPipelineBranchesHidesStaleRowsAndSaysSo(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if strings.Contains(output, "v1.0.0") {
		t.Errorf("a stale ref must be hidden by default: %q", output)
	}
	if !strings.Contains(output, "1 stale branch hidden, --all shows them") {
		t.Errorf("the footer must say what was hidden and how to see it: %q", output)
	}
}

func TestPipelineBranchesAllShowsTheStaleRows(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID, "--all")
	if executeError != nil {
		t.Fatalf("branches --all error = %v", executeError)
	}
	if !strings.Contains(output, "v1.0.0") {
		t.Errorf("--all must show the stale ref: %q", output)
	}
	if strings.Contains(output, "stale branch hidden") {
		t.Errorf("--all hides nothing, so it must not claim it did: %q", output)
	}
}

// The structured output is the platform's page untouched: a script that asked
// for JSON filters it itself, and dropping rows from it would make --all a
// flag the script has to know about to be told the truth.
func TestPipelineBranchesStructuredOutputKeepsStaleRows(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("branches -o json error = %v", executeError)
	}
	if strings.Contains(output, "BRANCH") {
		t.Errorf("-o json must skip the table: %q", output)
	}
	var page client.PipelineBranchList
	if unmarshalError := json.Unmarshal([]byte(output), &page); unmarshalError != nil {
		t.Fatalf("decoding -o json output: %v\n%s", unmarshalError, output)
	}
	if len(page.Branches) != 3 {
		t.Fatalf("-o json carries every row the platform answered, got %d", len(page.Branches))
	}
	if !page.Branches[2].Stale {
		t.Error("the stale row must keep its stale flag in the structured output")
	}
}

func TestPipelineBranchesEmpty(t *testing.T) {
	mockClient := &pipelineBranchesMock{
		branchesResult: &client.PipelineBranchList{Branches: []client.PipelineBranch{}},
	}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if !strings.Contains(output, "No pipeline branches found.") {
		t.Errorf("output = %q", output)
	}
}

// A page whose every row is stale must not read as a pipeline with no
// branches: those are different facts, and only one of them is fixed by
// passing --all.
func TestPipelineBranchesAllStaleSaysWhatWasHidden(t *testing.T) {
	page := threeBranchPage()
	for index := range page.Branches {
		page.Branches[index].Stale = true
	}
	mockClient := &pipelineBranchesMock{branchesResult: page}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if strings.Contains(output, "No pipeline branches found.") {
		t.Errorf("every row being stale is not the same as having none: %q", output)
	}
	if !strings.Contains(output, "3 stale branches hidden, --all shows them") {
		t.Errorf("output = %q", output)
	}
}

// TestPipelineBranchesAllStalePageStillNamesTheNextPage pins the cursor hint
// to the page, not to the rows shown: a page whose every row was hidden as
// stale still has pages after it, and --all only unhides this one.
func TestPipelineBranchesAllStalePageStillNamesTheNextPage(t *testing.T) {
	page := threeBranchPage()
	for index := range page.Branches {
		page.Branches[index].Stale = true
	}
	cursor := "eyJvZmZzZXQiOjIwfQ"
	page.NextCursor = &cursor
	mockClient := &pipelineBranchesMock{branchesResult: page}
	output, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if !strings.Contains(output, "No branches with a run in the last 14 days.") {
		t.Errorf("an all-stale page must say so: %q", output)
	}
	if !strings.Contains(output, "--cursor "+cursor) {
		t.Errorf("an all-stale page must still name the next page: %q", output)
	}
}

func TestPipelineBranchesPassesPagingThrough(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	if _, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID, "--limit", "5", "--cursor", branchPageCursor); executeError != nil {
		t.Fatalf("branches error = %v", executeError)
	}
	if mockClient.branchesOptions.Limit != 5 {
		t.Errorf("limit = %d, want 5", mockClient.branchesOptions.Limit)
	}
	if mockClient.branchesOptions.Cursor != branchPageCursor {
		t.Errorf("cursor = %q", mockClient.branchesOptions.Cursor)
	}
}

func TestPipelineListLatestPerBranchPrintsTheBranchTable(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	output, executeError := runPipelineCommand(t, mockClient, "list",
		"--application", testApplicationID, "--latest-per-branch")
	if executeError != nil {
		t.Fatalf("list --latest-per-branch error = %v", executeError)
	}
	if mockClient.branchesCalls != 1 || mockClient.listCalls != 0 {
		t.Fatalf("the alias must read branches, not runs: branches=%d runs=%d",
			mockClient.branchesCalls, mockClient.listCalls)
	}
	if !strings.Contains(output, "SUPERSEDED") || !strings.Contains(output, "main (default)") {
		t.Errorf("the alias must print the branch table: %q", output)
	}
}

func TestPipelineListLatestPerBranchRefusesRunFilters(t *testing.T) {
	for _, filterFlag := range []string{"--status", "--trigger", "--branch", "--head-sha"} {
		mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
		_, executeError := runPipelineCommand(t, mockClient, "list",
			"--application", testApplicationID, "--latest-per-branch", filterFlag, "main")
		if executeError == nil {
			t.Fatalf("%s with --latest-per-branch must be refused", filterFlag)
		}
		if !strings.Contains(executeError.Error(), "--latest-per-branch") {
			t.Errorf("%s: error = %v, want it to name the conflicting flag", filterFlag, executeError)
		}
		if mockClient.branchesCalls != 0 {
			t.Errorf("%s: a refused invocation must not read anything", filterFlag)
		}
	}
}

func TestPipelineListAllWithoutLatestPerBranchIsRefused(t *testing.T) {
	mockClient := &pipelineBranchesMock{branchesResult: threeBranchPage()}
	_, executeError := runPipelineCommand(t, mockClient, "list",
		"--application", testApplicationID, "--all")
	if executeError == nil || !strings.Contains(executeError.Error(), "--latest-per-branch") {
		t.Fatalf("error = %v, want --all to say it needs --latest-per-branch", executeError)
	}
}

func TestPipelineBranchesError(t *testing.T) {
	// The platform sentinel for a repository outside the caller's
	// organisation, spelled the way the route answers it.
	mockClient := &pipelineBranchesMock{
		branchesError: errors.New("Pipeline repository not found"),
	}
	_, executeError := runPipelineCommand(t, mockClient, "branches",
		"--application", testApplicationID)
	if executeError == nil || executeError.Error() != "Pipeline repository not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

// The client is exercised against a real server rather than a mock for the
// one thing a mock cannot check: that the command asks the route the platform
// actually serves, on the by-repository surface, with the paging in the query.
func TestPipelineBranchesRequestsTheRouteWithItsQuery(t *testing.T) {
	requestedPath := ""
	requestedQuery := ""
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			requestedPath = request.URL.Path
			requestedQuery = request.URL.RawQuery
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(threeBranchPage())
		}))
	defer server.Close()

	useTestClient(t, server.URL)

	output, executeError := executeCommand("pipeline", "branches",
		"--repository", testPipelineRepositoryID, "--limit", "7")
	if executeError != nil {
		t.Fatalf("pipeline branches: %v", executeError)
	}
	if requestedPath != "/api/v1/org/pipeline-repositories/"+testPipelineRepositoryID+"/pipeline-branches" {
		t.Errorf("path = %q", requestedPath)
	}
	if requestedQuery != "limit=7" {
		t.Errorf("query = %q, want the limit passed through", requestedQuery)
	}
	if !strings.Contains(output, "main (default)") {
		t.Errorf("output = %q", output)
	}
}
