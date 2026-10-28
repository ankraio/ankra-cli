package cmd

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

const editedDraftID = "7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a"

// deployedDraftMock answers the three calls the deployed-stack deploy makes:
// the listing (before and after), the draft read, and the promote.
type deployedDraftMock struct {
	baseMock

	listings        [][]client.ClusterStackDocument
	listCalls       int
	listError       error
	draft           client.ClusterStackDocument
	draftError      error
	promoteResult   *client.PatchStackResult
	promoteError    error
	readDraftIDs    []string
	promoted        []client.ClusterStackDocument
	createDeployHit bool
}

func (mock *deployedDraftMock) ListClusterStackDocuments(clusterID string) ([]client.ClusterStackDocument, error) {
	index := mock.listCalls
	mock.listCalls++
	if index > 0 && mock.listError != nil {
		return nil, mock.listError
	}
	if index >= len(mock.listings) {
		index = len(mock.listings) - 1
	}
	return mock.listings[index], nil
}

func (mock *deployedDraftMock) GetClusterStackDraft(_ context.Context, clusterID string, draftID string) (client.ClusterStackDocument, error) {
	mock.readDraftIDs = append(mock.readDraftIDs, draftID)
	if mock.draftError != nil {
		return nil, mock.draftError
	}
	return mock.draft, nil
}

func (mock *deployedDraftMock) PromoteClusterStackDraft(_ context.Context, clusterID string, document client.ClusterStackDocument) (*client.PatchStackResult, error) {
	mock.promoted = append(mock.promoted, document)
	if mock.promoteError != nil {
		return nil, mock.promoteError
	}
	return mock.promoteResult, nil
}

func (mock *deployedDraftMock) DeployClusterStackDraft(_ context.Context, clusterID string, document client.ClusterStackDocument) (*client.StackWriteResult, error) {
	mock.createDeployHit = true
	return nil, errors.New("the create write must not run for a deployed stack")
}

func editedListing(t *testing.T) []client.ClusterStackDocument {
	t.Helper()
	return []client.ClusterStackDocument{
		stackDocument(t, `{"name":"edited","draft_id":"`+editedDraftID+`","is_draft_only":false,"lifecycle":"deployed_dirty"}`),
	}
}

func editedListingAfterDeploy(t *testing.T) []client.ClusterStackDocument {
	t.Helper()
	return []client.ClusterStackDocument{
		stackDocument(t, `{"name":"edited","draft_id":null,"is_draft_only":false,"lifecycle":"deployed_clean"}`),
	}
}

func editedDraftRead(t *testing.T) client.ClusterStackDocument {
	t.Helper()
	return stackDocument(t, `{"name":"edited","draft_id":"`+editedDraftID+`","state":"creating",
		"manifests":[{"name":"api","manifest_base64":"a2luZDogQ29uZmlnTWFw","state":"creating"}]}`)
}

func runDeployDraftCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	return executeCommand(append([]string{"cluster", "stacks", "deploy-draft"}, arguments...)...)
}

func TestDeployDraftPromotesTheDraftOfADeployedStack(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t), editedListingAfterDeploy(t)},
		draft:    editedDraftRead(t),
		promoteResult: &client.PatchStackResult{
			StackName: "edited", JobCount: 2, OperationID: "33333333-3333-3333-3333-333333333333",
		},
	}

	output, runError := runDeployDraftCommand(t, mock, "edited")
	if runError != nil {
		t.Fatalf("deploy-draft: %v\n%s", runError, output)
	}
	if mock.createDeployHit {
		t.Fatal("a deployed stack must be promoted through the update write, not the create write")
	}
	if len(mock.readDraftIDs) != 1 || mock.readDraftIDs[0] != editedDraftID {
		t.Fatalf("the listed draft must be read in full first, read %v", mock.readDraftIDs)
	}
	if len(mock.promoted) != 1 || mock.promoted[0].DraftID() != editedDraftID {
		t.Fatalf("the draft read must be what is promoted, got %+v", mock.promoted)
	}
	for _, fragment := range []string{"Draft deployed.", "edited", "33333333-3333-3333-3333-333333333333"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output must carry %q, got:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, "still holds a draft") {
		t.Fatalf("a closed draft must not be reported as left over:\n%s", output)
	}
}

func TestDeployDraftWarnsWhenTheDeployedStackStillHoldsTheDraft(t *testing.T) {
	mock := &deployedDraftMock{
		listings:      [][]client.ClusterStackDocument{editedListing(t), editedListing(t)},
		draft:         editedDraftRead(t),
		promoteResult: &client.PatchStackResult{StackName: "edited", JobCount: 1},
	}

	output, runError := runDeployDraftCommand(t, mock, "edited")
	if runError != nil {
		t.Fatalf("deploy-draft: %v", runError)
	}
	if !strings.Contains(output, "still holds a draft") {
		t.Fatalf("a draft left open by the partial promote must be reported:\n%s", output)
	}
}

func TestDeployDraftSaysWhenItCannotConfirmTheDraftClosed(t *testing.T) {
	mock := &deployedDraftMock{
		listings:      [][]client.ClusterStackDocument{editedListing(t)},
		listError:     errors.New("listing timed out"),
		draft:         editedDraftRead(t),
		promoteResult: &client.PatchStackResult{StackName: "edited", JobCount: 1},
	}

	output, runError := runDeployDraftCommand(t, mock, "edited")
	if runError != nil {
		t.Fatalf("deploy-draft: %v", runError)
	}
	if !strings.Contains(output, "Could not confirm") || strings.Contains(output, "still holds a draft") {
		t.Fatalf("an unreadable listing is unconfirmed, not closed and not open:\n%s", output)
	}
}

func TestDeployDraftReportsADraftThatEndedSinceTheListing(t *testing.T) {
	mock := &deployedDraftMock{
		listings:   [][]client.ClusterStackDocument{editedListing(t)},
		draftError: &client.StackDraftReadError{StatusCode: http.StatusGone, Code: "DRAFT_TOMBSTONED"},
	}

	_, runError := runDeployDraftCommand(t, mock, "edited")
	if runError == nil {
		t.Fatal("a draft that ended must be refused")
	}
	if exitCodeFor(runError) != exitNotFound {
		t.Fatalf("exit code = %d, want %d (%v)", exitCodeFor(runError), exitNotFound, runError)
	}
	if len(mock.promoted) != 0 {
		t.Fatal("nothing may be promoted when the draft is gone")
	}
}

func TestDeployDraftRefusesADraftReadForAnotherDraft(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draft:    stackDocument(t, `{"name":"edited","draft_id":"8b8b8b8b-8b8b-8b8b-8b8b-8b8b8b8b8b8b"}`),
	}

	_, runError := runDeployDraftCommand(t, mock, "edited")
	if runError == nil || !strings.Contains(runError.Error(), "nothing was deployed") {
		t.Fatalf("a read answering another draft must be refused: %v", runError)
	}
	if len(mock.promoted) != 0 {
		t.Fatal("a mismatched draft must never be promoted")
	}
}

func TestDeployDraftMapsAPromoteRefusal(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draft:    editedDraftRead(t),
		promoteError: &client.PatchStackError{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       []byte(`{"detail":"The chart version would move backwards."}`),
		},
	}

	_, runError := runDeployDraftCommand(t, mock, "edited")
	if runError == nil || !strings.Contains(runError.Error(), "The chart version would move backwards.") {
		t.Fatalf("the platform's refusal must reach the operator verbatim: %v", runError)
	}
}

func TestDeployDraftRefusesAnUnknownStack(t *testing.T) {
	mock := &deployedDraftMock{listings: [][]client.ClusterStackDocument{editedListing(t)}}

	_, runError := runDeployDraftCommand(t, mock, "missing")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("an unknown stack must exit not-found: %v", runError)
	}
	if !strings.Contains(runError.Error(), "edited") {
		t.Fatalf("the refusal must name the stacks that have a draft: %v", runError)
	}
}

func TestStackWriteResultFromPromoteKeepsRefusalsAndTheDeferredPush(t *testing.T) {
	result := stackWriteResultFromPromote(&client.PatchStackResult{
		StackName: "edited", GitPushDeferred: true, GitPushMessage: "The repository is unreachable.",
		Errors: []client.PatchStackResourceError{{
			Name: "api", Kind: "manifest",
			Errors: []client.PatchStackErrorDetail{{Key: "name", Message: "Manifest cannot be empty."}},
		}},
	})
	if len(result.Errors) != 1 || result.Errors[0].Errors[0].Message != "Manifest cannot be empty." {
		t.Fatalf("errors = %+v", result.Errors)
	}
	if result.OperationID != nil || result.CommitSHA != nil {
		t.Fatalf("absent ids must stay absent: %+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0].Message, "The repository is unreachable.") {
		t.Fatalf("a deferred push must be a warning: %+v", result.Warnings)
	}
}
