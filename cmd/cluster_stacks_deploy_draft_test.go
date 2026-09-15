package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func stackDocument(t *testing.T, body string) client.ClusterStackDocument {
	t.Helper()
	var document client.ClusterStackDocument
	if unmarshalError := json.Unmarshal([]byte(body), &document); unmarshalError != nil {
		t.Fatalf("decoding the stack fixture: %v", unmarshalError)
	}
	return document
}

func deployDraftFixtures(t *testing.T) []client.ClusterStackDocument {
	t.Helper()
	return []client.ClusterStackDocument{
		stackDocument(t, `{"name":"notes","draft_id":"9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f","is_draft_only":true,"lifecycle":"draft_only"}`),
		stackDocument(t, `{"name":"platform","draft_id":null,"is_draft_only":false,"lifecycle":"deployed_clean"}`),
		stackDocument(t, `{"name":"edited","draft_id":"7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a","is_draft_only":false,"lifecycle":"deployed_dirty"}`),
	}
}

func TestSelectDeployableDraftFindsTheDraftOnlyStack(t *testing.T) {
	document, selectError := selectDeployableDraft(deployDraftFixtures(t), "notes")
	if selectError != nil {
		t.Fatalf("selectDeployableDraft: %v", selectError)
	}
	if document.Name() != "notes" || document.DraftID() == "" {
		t.Fatalf("selected %+v", document)
	}
}

func TestSelectDeployableDraftRefusesAStackWithNothingToDeploy(t *testing.T) {
	_, selectError := selectDeployableDraft(deployDraftFixtures(t), "platform")
	if selectError == nil {
		t.Fatal("a deployed, clean stack has no draft and must be refused")
	}
	if !strings.Contains(selectError.Error(), "no draft to deploy") {
		t.Fatalf("the refusal must say why: %v", selectError)
	}
	if exitCodeFor(selectError) != exitUsage {
		t.Fatalf("exit code = %d, want %d", exitCodeFor(selectError), exitUsage)
	}
}

func TestSelectDeployableDraftSendsADirtyDeployedStackToTheBuilder(t *testing.T) {
	// Promoting a draft over a live stack is the update write, not the
	// create write this verb runs; the create write's own refusal names an
	// API function ("use update_cluster_stack") no CLI user has.
	_, selectError := selectDeployableDraft(deployDraftFixtures(t), "edited")
	if selectError == nil {
		t.Fatal("a deployed stack holding a draft of edits must be refused here")
	}
	if !strings.Contains(selectError.Error(), "stack builder") {
		t.Fatalf("the refusal must point somewhere that works: %v", selectError)
	}
}

func TestSelectDeployableDraftNamesTheStacksThatDoHaveDrafts(t *testing.T) {
	_, selectError := selectDeployableDraft(deployDraftFixtures(t), "missing")
	if selectError == nil {
		t.Fatal("an unknown stack must be refused")
	}
	if !strings.Contains(selectError.Error(), "notes") || !strings.Contains(selectError.Error(), "edited") {
		t.Fatalf("the refusal must name the stacks that do have drafts: %v", selectError)
	}
	if exitCodeFor(selectError) != exitNotFound {
		t.Fatalf("exit code = %d, want %d", exitCodeFor(selectError), exitNotFound)
	}

	_, emptyError := selectDeployableDraft(nil, "missing")
	if emptyError == nil || !strings.Contains(emptyError.Error(), "no stack on it has a draft") {
		t.Fatalf("a cluster with no drafts at all must say so: %v", emptyError)
	}
}

func TestPrintStackDraftDeployResultReportsTheAcceptedDeploy(t *testing.T) {
	operationID := "22222222-2222-2222-2222-222222222222"
	output := &bytes.Buffer{}
	printError := printStackDraftDeployResult(output, &client.StackWriteResult{
		StackName: "notes", JobCount: 4, OperationID: &operationID,
		Warnings: []string{"Manifest 'api' carries the source cluster's generated hostname."},
	})
	if printError != nil {
		t.Fatalf("an accepted deploy must not error: %v", printError)
	}
	for _, fragment := range []string{"Draft deployed.", "notes", "4", operationID,
		"carries the source cluster's generated hostname", "ankra cluster operations get"} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output must carry %q, got:\n%s", fragment, output.String())
		}
	}
}

func TestPrintStackDraftDeployResultKeepsTheDraftOnARefusal(t *testing.T) {
	output := &bytes.Buffer{}
	printError := printStackDraftDeployResult(output, &client.StackWriteResult{
		StackName: "notes",
		Errors: []client.StackResourceError{{
			Name: "api", Kind: "manifest",
			Errors: []client.StackResourceErrorItem{{Key: "name", Message: "A manifest named 'api' already exists."}},
		}},
	})
	if printError == nil {
		t.Fatal("a refused write must exit non-zero")
	}
	if exitCodeFor(printError) != exitError {
		t.Fatalf("exit code = %d, want %d", exitCodeFor(printError), exitError)
	}
	for _, fragment := range []string{"kept as a draft", "manifest api", "already exists"} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output must carry %q, got:\n%s", fragment, output.String())
		}
	}
}
