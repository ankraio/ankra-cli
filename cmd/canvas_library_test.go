package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const testCanvasID = "3f0a2f7e-0000-4000-8000-000000000001"

const canvasDocumentBody = `{"id":"` + testCanvasID + `","kind":"page","title":"Q3 report","visibility":"private",
	"owner_user_id":"user-1","is_owner":true,"is_pinned":false,"language":"html","draft_revision_id":"revision-2",
	"published_revision_id":null,"created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-09T10:00:00Z",
	"draft":{"id":"revision-2","number":2,"parent_revision_id":"revision-1","language":"html","schema_version":null,
	 "size_bytes":18,"author_kind":"user","created_at":"2026-11-09T10:00:00Z","content":"<h1>Q3 report</h1>"}}`

type canvasLibraryMock struct {
	baseMock
	answer          string
	failure         error
	listOptions     *client.CanvasListOptions
	created         *client.CreateCanvasRequest
	updated         *client.UpdateCanvasRequest
	deleted         []string
	comment         *client.CreateCanvasCommentRequest
	decisions       []string
	includeResolved bool
}

func (mock *canvasLibraryMock) reply() (json.RawMessage, error) {
	return json.RawMessage(mock.answer), mock.failure
}

func (mock *canvasLibraryMock) ListCanvases(_ context.Context, options client.CanvasListOptions) (json.RawMessage, error) {
	mock.listOptions = &options
	return mock.reply()
}

func (mock *canvasLibraryMock) GetCanvas(context.Context, string) (json.RawMessage, error) {
	return mock.reply()
}

func (mock *canvasLibraryMock) CreateCanvas(_ context.Context, request client.CreateCanvasRequest) (json.RawMessage, error) {
	mock.created = &request
	return mock.reply()
}

func (mock *canvasLibraryMock) UpdateCanvas(_ context.Context, _ string, request client.UpdateCanvasRequest) (json.RawMessage, error) {
	mock.updated = &request
	return mock.reply()
}

func (mock *canvasLibraryMock) DeleteCanvas(_ context.Context, canvasID string) error {
	mock.deleted = append(mock.deleted, canvasID)
	return mock.failure
}

func (mock *canvasLibraryMock) ListCanvasRevisions(context.Context, string, string, int) (json.RawMessage, error) {
	return mock.reply()
}

func (mock *canvasLibraryMock) GetCanvasRevision(context.Context, string, string) (json.RawMessage, error) {
	return mock.reply()
}

func (mock *canvasLibraryMock) ListCanvasComments(_ context.Context, _ string, _ string, _ int, includeResolved bool) (json.RawMessage, error) {
	mock.includeResolved = includeResolved
	return mock.reply()
}

func (mock *canvasLibraryMock) CreateCanvasComment(_ context.Context, _ string, request client.CreateCanvasCommentRequest) (json.RawMessage, error) {
	mock.comment = &request
	return mock.reply()
}

func (mock *canvasLibraryMock) ResolveCanvasComment(_ context.Context, _ string, commentID string) (json.RawMessage, error) {
	mock.decisions = append(mock.decisions, "resolve "+commentID)
	return mock.reply()
}

func (mock *canvasLibraryMock) ReopenCanvasComment(_ context.Context, _ string, commentID string) (json.RawMessage, error) {
	mock.decisions = append(mock.decisions, "reopen "+commentID)
	return mock.reply()
}

func runCanvasLibrary(t *testing.T, mock *canvasLibraryMock, input string, arguments ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "canvas"}
	root.AddCommand(newCanvasLibraryCommands()...)
	return runFreshCommand(t, root, mock, input, arguments...)
}

func TestCanvasLibraryListsCanvasesWithItsFilters(t *testing.T) {
	mock := &canvasLibraryMock{answer: `{"items":[` + strings.Replace(canvasDocumentBody, `"draft":`, `"ignored":`, 1) + `],"next_cursor":"next-1"}`}
	output, notes, executeError := runCanvasLibrary(t, mock, "", "library", "--query", "report", "--kind", "page", "--pinned", "--limit", "10")
	if executeError != nil {
		t.Fatalf("library failed: %v", executeError)
	}
	if got := *mock.listOptions; got != (client.CanvasListOptions{Limit: 10, Query: "report", Kind: "page", IsPinnedOnly: true}) {
		t.Errorf("options = %+v", got)
	}
	if !strings.Contains(output, testCanvasID) || !strings.Contains(output, "Q3 report") || !strings.Contains(notes, "--cursor next-1") {
		t.Errorf("output:\n%s\nstderr: %s", output, notes)
	}

	empty := &canvasLibraryMock{answer: `{"items":[],"next_cursor":null}`}
	output, _, executeError = runCanvasLibrary(t, empty, "", "library")
	if executeError != nil || !strings.Contains(output, "No canvases found") {
		t.Fatalf("error = %v, output: %s", executeError, output)
	}
	_, _, kindError := runCanvasLibrary(t, empty, "", "library", "--kind", "poster")
	if exitCodeFor(kindError) != exitUsage {
		t.Fatalf("error = %v, want a usage error for an unknown kind", kindError)
	}
	for _, limit := range []string{"0", "101"} {
		if _, _, limitError := runCanvasLibrary(t, empty, "", "library", "--limit", limit); exitCodeFor(limitError) != exitUsage {
			t.Fatalf("--limit %s: error = %v, want a usage error", limit, limitError)
		}
	}
}

func TestCanvasGetPrintsTheCanvasItsContentOrThePlatformDocument(t *testing.T) {
	mock := &canvasLibraryMock{answer: canvasDocumentBody}
	output, _, executeError := runCanvasLibrary(t, mock, "", "get", testCanvasID)
	if executeError != nil || !strings.Contains(output, "Q3 report") || !strings.Contains(output, "revision 2") {
		t.Fatalf("error = %v, output:\n%s", executeError, output)
	}
	output, _, executeError = runCanvasLibrary(t, mock, "", "get", testCanvasID, "--content")
	if executeError != nil || output != "<h1>Q3 report</h1>" {
		t.Fatalf("error = %v, content = %q", executeError, output)
	}
	output, _, executeError = runCanvasLibrary(t, mock, "", "get", testCanvasID, "-o", "json")
	if executeError != nil || !strings.Contains(output, `"owner_user_id"`) {
		t.Fatalf("error = %v, -o json must print the platform's document: %s", executeError, output)
	}
}

func TestCanvasLibraryRefusalsCarryTheirExitCodes(t *testing.T) {
	for name, testCase := range map[string]struct {
		refusal  error
		exitCode int
	}{
		"not found":        {&client.CanvasPagesError{StatusCode: http.StatusNotFound, Code: "canvas_not_found"}, exitNotFound},
		"another member's": {&client.CanvasPagesError{StatusCode: http.StatusForbidden, Code: "canvas_owned_by_another_member"}, exitForbidden},
		"rejected":         {&client.CanvasPagesError{StatusCode: http.StatusBadRequest, Code: "invalid_request"}, exitUsage},
		"unauthorised":     {client.ErrUnauthorized, exitAuth},
		"no permission":    {&client.PermissionDeniedError{}, exitForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &canvasLibraryMock{failure: testCase.refusal}
			_, _, executeError := runCanvasLibrary(t, mock, "", "get", testCanvasID)
			if exitCodeFor(executeError) != testCase.exitCode {
				t.Fatalf("error = %v, exit %d, want %d", executeError, exitCodeFor(executeError), testCase.exitCode)
			}
		})
	}
}

func TestCanvasCreateReadsTheFileAndInfersTitleAndLanguage(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "quarterly.md")
	if writeError := os.WriteFile(path, []byte("# Quarterly\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &canvasLibraryMock{answer: canvasDocumentBody}
	output, _, executeError := runCanvasLibrary(t, mock, "", "create", path)
	if executeError != nil {
		t.Fatalf("create failed: %v", executeError)
	}
	want := client.CreateCanvasRequest{Kind: "page", Title: "quarterly", Language: "markdown", Content: "# Quarterly\n"}
	if mock.created == nil || *mock.created != want {
		t.Errorf("request = %+v, want %+v", mock.created, want)
	}
	if !strings.Contains(output, testCanvasID) {
		t.Errorf("output must name the new canvas: %s", output)
	}

	for name, arguments := range map[string][]string{
		"missing file":      {"create", filepath.Join(directory, "absent.html")},
		"unknown extension": {"create", filepath.Join(directory, "notes.txt")},
		"unknown kind":      {"create", path, "--kind", "poster"},
	} {
		t.Run(name, func(t *testing.T) {
			refused := &canvasLibraryMock{answer: canvasDocumentBody}
			_, _, refusedError := runCanvasLibrary(t, refused, "", arguments...)
			if exitCodeFor(refusedError) != exitUsage || refused.created != nil {
				t.Fatalf("error = %v, created = %+v, want a usage error and no request", refusedError, refused.created)
			}
		})
	}
}

func TestCanvasUpdateSendsOnlyWhatChanged(t *testing.T) {
	mock := &canvasLibraryMock{answer: canvasDocumentBody}
	if _, _, executeError := runCanvasLibrary(t, mock, "", "update", testCanvasID, "--visibility", "organisation"); executeError != nil {
		t.Fatalf("update failed: %v", executeError)
	}
	if mock.updated == nil || mock.updated.Title != nil || mock.updated.Visibility == nil || *mock.updated.Visibility != "organisation" {
		t.Errorf("request = %+v", mock.updated)
	}
	for name, arguments := range map[string][]string{
		"nothing to change":  {"update", testCanvasID},
		"unknown visibility": {"update", testCanvasID, "--visibility", "public"},
	} {
		t.Run(name, func(t *testing.T) {
			refused := &canvasLibraryMock{answer: canvasDocumentBody}
			_, _, refusedError := runCanvasLibrary(t, refused, "", arguments...)
			if exitCodeFor(refusedError) != exitUsage || refused.updated != nil {
				t.Fatalf("error = %v, want a usage error and no request", refusedError)
			}
		})
	}
}

func TestCanvasDeleteAsksFirst(t *testing.T) {
	declined := &canvasLibraryMock{}
	_, _, declinedError := runCanvasLibrary(t, declined, "n\n", "delete", testCanvasID)
	if exitCodeFor(declinedError) != exitCancelled || len(declined.deleted) != 0 {
		t.Fatalf("error = %v, deleted = %v, want the declined exit and no delete", declinedError, declined.deleted)
	}
	confirmed := &canvasLibraryMock{}
	output, _, confirmedError := runCanvasLibrary(t, confirmed, "", "delete", testCanvasID, "--yes")
	if confirmedError != nil || len(confirmed.deleted) != 1 || !strings.Contains(output, "deleted") {
		t.Fatalf("error = %v, deleted = %v, output: %s", confirmedError, confirmed.deleted, output)
	}
	missing := &canvasLibraryMock{failure: &client.CanvasPagesError{StatusCode: http.StatusNotFound, Code: "canvas_not_found"}}
	_, _, missingError := runCanvasLibrary(t, missing, "", "delete", testCanvasID, "--yes")
	if exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("error = %v, want the not-found exit", missingError)
	}
}

func TestCanvasRevisionsListAndGet(t *testing.T) {
	list := &canvasLibraryMock{answer: `{"items":[{"id":"revision-2","number":2,"language":"html","size_bytes":18,
		"author_kind":"assistant","created_at":"2026-11-09T10:00:00Z"}],"next_cursor":null}`}
	output, _, executeError := runCanvasLibrary(t, list, "", "revisions", "list", testCanvasID)
	if executeError != nil || !strings.Contains(output, "revision-2") || !strings.Contains(output, "assistant") {
		t.Fatalf("error = %v, output:\n%s", executeError, output)
	}
	get := &canvasLibraryMock{answer: `{"id":"revision-1","number":1,"language":"html","size_bytes":9,
		"author_kind":"user","created_at":"2026-11-01T10:00:00Z","content":"<p>v1</p>"}`}
	output, _, executeError = runCanvasLibrary(t, get, "", "revisions", "get", testCanvasID, "revision-1", "--content")
	if executeError != nil || output != "<p>v1</p>" {
		t.Fatalf("error = %v, content = %q", executeError, output)
	}
	missing := &canvasLibraryMock{failure: &client.CanvasPagesError{StatusCode: http.StatusNotFound, Code: "canvas_revision_not_found"}}
	_, _, missingError := runCanvasLibrary(t, missing, "", "revisions", "get", testCanvasID, "revision-9")
	if exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("error = %v, want the not-found exit", missingError)
	}
}

func TestCanvasCommentsListAddResolveAndReopen(t *testing.T) {
	const commentBody = `{"id":"comment-1","canvas_id":"` + testCanvasID + `","parent_comment_id":null,"element_id":"chart-1",
		"body":"The Q3 total looks low","author_user_id":"user-2","author_email":"ana@example.com","resolved_at":null,
		"created_at":"2026-11-09T11:00:00Z"}`
	list := &canvasLibraryMock{answer: `{"items":[` + commentBody + `],"next_cursor":null,"can_resolve":true}`}
	output, _, executeError := runCanvasLibrary(t, list, "", "comments", "list", testCanvasID, "--include-resolved")
	if executeError != nil || !list.includeResolved {
		t.Fatalf("error = %v, include_resolved = %v", executeError, list.includeResolved)
	}
	for _, want := range []string{"comment-1", "open", "ana@example.com", "The Q3 total looks low", "chart-1"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}

	add := &canvasLibraryMock{answer: commentBody}
	output, _, executeError = runCanvasLibrary(t, add, "", "comments", "add", testCanvasID, "Fixed", "--reply-to", "comment-1")
	if executeError != nil || !strings.Contains(output, "comment-1 added") {
		t.Fatalf("error = %v, output: %s", executeError, output)
	}
	if add.comment.Body != "Fixed" || add.comment.ParentCommentID == nil || *add.comment.ParentCommentID != "comment-1" ||
		add.comment.ElementID != nil || add.comment.RevisionID != nil {
		t.Errorf("request = %+v", add.comment)
	}
	_, _, blankError := runCanvasLibrary(t, &canvasLibraryMock{}, "", "comments", "add", testCanvasID, "  ")
	if exitCodeFor(blankError) != exitUsage {
		t.Fatalf("error = %v, want a usage error for an empty comment", blankError)
	}

	decide := &canvasLibraryMock{answer: commentBody}
	if _, _, resolveError := runCanvasLibrary(t, decide, "", "comments", "resolve", testCanvasID, "comment-1"); resolveError != nil {
		t.Fatalf("resolve failed: %v", resolveError)
	}
	if _, _, reopenError := runCanvasLibrary(t, decide, "", "comments", "reopen", testCanvasID, "comment-1"); reopenError != nil {
		t.Fatalf("reopen failed: %v", reopenError)
	}
	if strings.Join(decide.decisions, ",") != "resolve comment-1,reopen comment-1" {
		t.Errorf("decisions = %v", decide.decisions)
	}
	forbidden := &canvasLibraryMock{failure: &client.CanvasPagesError{StatusCode: http.StatusForbidden, Code: "comment_forbidden"}}
	_, _, forbiddenError := runCanvasLibrary(t, forbidden, "", "comments", "resolve", testCanvasID, "comment-1")
	if exitCodeFor(forbiddenError) != exitForbidden {
		t.Fatalf("error = %v, want the permission exit", forbiddenError)
	}
}
