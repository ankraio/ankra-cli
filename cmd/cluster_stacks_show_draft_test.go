package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

func runShowDraftCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	return executeCommand(append([]string{"cluster", "stacks", "show-draft"}, arguments...)...)
}

// clonedDraftRead is a clone's draft: the rewritten Cluster with what it waits
// for, and the ObjectStore over the source's archive the recovery reads.
func clonedDraftRead(t *testing.T) client.ClusterStackDocument {
	t.Helper()
	objectStore := base64.StdEncoding.EncodeToString([]byte("kind: ObjectStore\nmetadata:\n  name: orders-source\n"))
	return stackDocument(t, `{"name":"edited","draft_id":"`+editedDraftID+`","lifecycle":"draft_only",
		"manifests":[{"name":"orders-source","manifest_base64":"`+objectStore+`"}],
		"applications":[{"name":"orders","namespace":"shop","manifest_base64":null,
			"parents":[{"name":"orders-source","kind":"manifest"}]}]}`)
}

func TestShowDraftPrintsEachMemberDecodedWithWhatItWaitsFor(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draft:    clonedDraftRead(t),
	}

	output, runError := runShowDraftCommand(t, mock, "edited")
	if runError != nil {
		t.Fatalf("show-draft: %v\n%s", runError, output)
	}
	if len(mock.readDraftIDs) != 1 || mock.readDraftIDs[0] != editedDraftID {
		t.Fatalf("the listing elides bodies, so the draft must be read in full, read %v", mock.readDraftIDs)
	}
	for _, wanted := range []string{
		"kind: ObjectStore",
		"after: manifest orders-source",
		"application orders in namespace shop",
		"this read carried no content",
	} {
		if !strings.Contains(stripANSICodes(output), wanted) {
			t.Fatalf("expected %q in the review, got:\n%s", wanted, output)
		}
	}
	if len(mock.promoted) != 0 || mock.createDeployHit {
		t.Fatal("reading a draft must deploy nothing")
	}
}

func TestShowDraftStructuredOutputCarriesTheDecodedContent(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draft:    clonedDraftRead(t),
	}

	output, runError := runShowDraftCommand(t, mock, "edited", "-o", "json")
	if runError != nil {
		t.Fatalf("show-draft: %v\n%s", runError, output)
	}
	var review client.StackDraftReview
	if decodeError := json.Unmarshal([]byte(output), &review); decodeError != nil {
		t.Fatalf("the output must stay parseable: %v\n%s", decodeError, output)
	}
	if len(review.Members) != 2 || !strings.Contains(review.Members[0].Content, "kind: ObjectStore") {
		t.Fatalf("the members must carry their decoded content, got %+v", review.Members)
	}
	if review.Members[1].HasContent {
		t.Fatalf("a body the read did not carry is not an empty one: %+v", review.Members[1])
	}
}

func TestShowDraftSaysWhenTheDraftEndedBeforeItWasRead(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draftError: &client.StackDraftReadError{StatusCode: http.StatusNotFound,
			Code: "DRAFT_TOMBSTONED"},
	}

	_, runError := runShowDraftCommand(t, mock, "edited")
	if runError == nil || !strings.Contains(runError.Error(), "the draft has ended") {
		t.Fatalf("a draft that ended must be reported, got %v", runError)
	}
}

func TestShowDraftRefusesAStackWithNoDraft(t *testing.T) {
	mock := &deployedDraftMock{listings: [][]client.ClusterStackDocument{editedListingAfterDeploy(t)}}

	_, runError := runShowDraftCommand(t, mock, "edited")
	if runError == nil || !strings.Contains(runError.Error(), "has no draft") {
		t.Fatalf("a stack with no draft must say so, got %v", runError)
	}
	if len(mock.readDraftIDs) != 0 {
		t.Fatalf("there was nothing to read, read %v", mock.readDraftIDs)
	}
}

func TestShowDraftRefusesAReadOfAnotherDraft(t *testing.T) {
	mock := &deployedDraftMock{
		listings: [][]client.ClusterStackDocument{editedListing(t)},
		draft:    stackDocument(t, `{"name":"edited","draft_id":"8b8b8b8b-8b8b-8b8b-8b8b-8b8b8b8b8b8b"}`),
	}

	if _, runError := runShowDraftCommand(t, mock, "edited"); runError == nil {
		t.Fatal("a read answering another draft must not be shown as this one")
	}
}
