package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

// discardDraftMock lists stacks and answers the discard write.
type discardDraftMock struct {
	listing       []client.ClusterStackDocument
	result        *client.StackDraftDiscardResult
	discardError  error
	discardedIDs  []string
	forcedDiscard []bool
}

func (mock *discardDraftMock) ListClusterStackDocuments(string) ([]client.ClusterStackDocument, error) {
	return mock.listing, nil
}

func (mock *discardDraftMock) DiscardClusterStackDraft(_ context.Context, _ string, draftID string,
	force bool) (*client.StackDraftDiscardResult, error) {
	mock.discardedIDs = append(mock.discardedIDs, draftID)
	mock.forcedDiscard = append(mock.forcedDiscard, force)
	if mock.discardError != nil {
		return nil, mock.discardError
	}
	return mock.result, nil
}

// runDiscardDraftCommand runs discard-draft with input on stdin and returns
// stdout and stderr apart, so structured output can be checked for being
// clean. A prompt is only answered when a test opts in with isInteractive.
func runDiscardDraftCommand(t *testing.T, mock any, input string, isInteractive bool,
	arguments ...string) (string, string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return isInteractive }
	t.Cleanup(func() { promptIsInteractive = previous })

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "stacks", "discard-draft"}, arguments...))
	resetTreeFlags(t, clusterStacksDiscardDraftCmd)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, clusterStacksDiscardDraftCmd)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func discardResult(alreadyDiscarded bool, reason string) *client.StackDraftDiscardResult {
	stackName := "edited"
	result := &client.StackDraftDiscardResult{Success: true, StackName: &stackName, AlreadyDiscarded: alreadyDiscarded}
	if reason != "" {
		result.Reason = &reason
	}
	return result
}

func TestDiscardDraftDiscardsTheListedDraftOnceConfirmed(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t), result: discardResult(false, "USER_DISCARD")}

	stdout, stderr, runError := runDiscardDraftCommand(t, mock, "", false, "EDITED", "--yes")
	if runError != nil {
		t.Fatalf("discard-draft: %v\n%s%s", runError, stdout, stderr)
	}
	if len(mock.discardedIDs) != 1 || mock.discardedIDs[0] != editedDraftID || mock.forcedDiscard[0] {
		t.Fatalf("the listed draft must be discarded once, unforced, got %v %v", mock.discardedIDs, mock.forcedDiscard)
	}
	if !strings.Contains(stderr, "Discarding draft "+editedDraftID+" of stack 'edited'") ||
		!strings.Contains(stderr, "keeps running as it is") {
		t.Fatalf("the draft must be named before it is discarded, got stderr %q", stderr)
	}
	if !strings.Contains(stdout, "Discarded draft "+editedDraftID) {
		t.Fatalf("the discard must be reported, got %q", stdout)
	}
}

func TestDiscardDraftAsksAndDeclinedDiscardsNothing(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t), result: discardResult(false, "USER_DISCARD")}

	_, _, runError := runDiscardDraftCommand(t, mock, "n\n", true, "edited")
	if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined prompt exits 4, got %v (%d)", runError, exitCodeFor(runError))
	}
	if len(mock.discardedIDs) != 0 {
		t.Fatalf("a declined prompt must discard nothing, discarded %v", mock.discardedIDs)
	}

	_, stderr, runError := runDiscardDraftCommand(t, mock, "y\n", true, "edited")
	if runError != nil || len(mock.discardedIDs) != 1 || !strings.Contains(stderr, "Discard this draft? [y/N]") {
		t.Fatalf("a confirmed prompt discards, got %v %v %q", runError, mock.discardedIDs, stderr)
	}
}

func TestDiscardDraftWithoutATerminalNeedsYes(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t), result: discardResult(false, "USER_DISCARD")}

	_, _, runError := runDiscardDraftCommand(t, mock, "", false, "edited")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--yes") {
		t.Fatalf("a run with no terminal and no --yes is refused with exit 2, got %v", runError)
	}
	if len(mock.discardedIDs) != 0 {
		t.Fatalf("nothing may be discarded unconfirmed, discarded %v", mock.discardedIDs)
	}
}

func TestDiscardDraftSaysWhenAnAIConversationKeptTheDraft(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t),
		result: discardResult(true, client.StackDraftDiscardReasonProtected)}

	stdout, _, runError := runDiscardDraftCommand(t, mock, "", false, "edited", "--yes")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("a kept draft is a block that changed nothing (exit 8), got %v (%d)", runError, exitCodeFor(runError))
	}
	if !strings.Contains(stdout, "was kept") || !strings.Contains(stdout, "--force") {
		t.Fatalf("a kept draft must say so and name --force, got %q", stdout)
	}

	forced := &discardDraftMock{listing: editedListing(t), result: discardResult(false, "USER_DISCARD")}
	_, forcedStderr, forceError := runDiscardDraftCommand(t, forced, "", false, "edited", "--yes", "--force")
	if forceError != nil || len(forced.forcedDiscard) != 1 || !forced.forcedDiscard[0] {
		t.Fatalf("--force must reach the write, got %v %v", forceError, forced.forcedDiscard)
	}
	if !strings.Contains(forcedStderr, "--force: it is discarded even if an AI conversation is working on it.") {
		t.Fatalf("a forced discard must say before it runs that it overrides an AI conversation's hold, got %q", forcedStderr)
	}
}

func TestDiscardDraftStructuredOutputIsTheAnswerAndKeepsTheExit(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t),
		result: discardResult(true, client.StackDraftDiscardReasonProtected)}

	stdout, _, runError := runDiscardDraftCommand(t, mock, "", false, "edited", "--yes", "-o", "json")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("structured output keeps the exit of a kept draft, got %v", runError)
	}
	var answer client.StackDraftDiscardResult
	if decodeError := json.Unmarshal([]byte(stdout), &answer); decodeError != nil {
		t.Fatalf("stdout must stay parseable: %v\n%s", decodeError, stdout)
	}
	if !answer.AlreadyDiscarded || answer.Reason == nil || *answer.Reason != "protected" {
		t.Fatalf("the answer must be the platform's, got %+v", answer)
	}
}

func TestDiscardDraftReportsADraftThatAlreadyEnded(t *testing.T) {
	mock := &discardDraftMock{listing: editedListing(t), result: discardResult(true, "DEPLOYED")}

	stdout, _, runError := runDiscardDraftCommand(t, mock, "", false, "edited", "--yes")
	if runError != nil || !strings.Contains(stdout, "had already ended") || !strings.Contains(stdout, "DEPLOYED") {
		t.Fatalf("a draft that ended is reported and exits 0, got %v %q", runError, stdout)
	}

	gone := &discardDraftMock{listing: editedListing(t), discardError: &client.StackDraftDiscardError{
		StatusCode: http.StatusNotFound, Code: "DRAFT_UNKNOWN"}}
	if _, _, goneError := runDiscardDraftCommand(t, gone, "", false, "edited", "--yes"); exitCodeFor(goneError) != exitNotFound {
		t.Fatalf("a draft the platform does not hold exits 3, got %v", goneError)
	}
}

func TestDiscardDraftRefusesAStackWithNoDraft(t *testing.T) {
	mock := &discardDraftMock{listing: editedListingAfterDeploy(t)}

	_, _, runError := runDiscardDraftCommand(t, mock, "", false, "edited", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "has no draft to discard") {
		t.Fatalf("a stack with no draft must say so in discard's words, got %v", runError)
	}
	if len(mock.discardedIDs) != 0 {
		t.Fatalf("there was nothing to discard, discarded %v", mock.discardedIDs)
	}
}
