package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

func resetCanvasFlags(t *testing.T) {
	t.Helper()
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		canvasPublishCmd, canvasListCmd, canvasUnpublishCmd,
	} {
		flagged.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
}

type canvasMock struct {
	baseMock

	publishRequests []client.CanvasPublicationRequest
	publishResult   *client.CanvasPublicationResult
	publishError    error
	listResult      *client.CanvasPublicationsListResult
	listCursor      string
	listLimit       int
	unpublished     []string
	unpublishError  error
}

func (m *canvasMock) PublishCanvasPublication(ctx context.Context, request client.CanvasPublicationRequest) (*client.CanvasPublicationResult, error) {
	m.publishRequests = append(m.publishRequests, request)
	if m.publishError != nil {
		return nil, m.publishError
	}
	return m.publishResult, nil
}

func (m *canvasMock) ListCanvasPublications(ctx context.Context, cursor string, limit int) (*client.CanvasPublicationsListResult, error) {
	m.listCursor, m.listLimit = cursor, limit
	return m.listResult, nil
}

func (m *canvasMock) UnpublishCanvasPublication(ctx context.Context, publicationID string) error {
	m.unpublished = append(m.unpublished, publicationID)
	return m.unpublishError
}

func newCanvasPublished(created bool) *client.CanvasPublicationResult {
	return &client.CanvasPublicationResult{
		CanvasPublication: client.CanvasPublication{
			ID: "11111111-1111-4111-8111-111111111111", Title: "Report", Language: "html", SizeBytes: 12,
			URL: "https://pages.example/org/slug", ExpiresAt: "2026-10-19T00:00:00Z",
		},
		Created: created,
	}
}

func runCanvas(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetCanvasFlags(t)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetArgs(append([]string{"canvas"}, args...))
	err := rootCmd.Execute()
	return out.String(), err
}

func writeCanvasFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCanvasPublish_PrintsURLLastAndStableIdentity(t *testing.T) {
	path := writeCanvasFile(t, "report.html", "<h1>hi</h1>")
	mock := &canvasMock{publishResult: newCanvasPublished(true)}
	output, err := runCanvas(t, mock, "publish", path)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if lines[len(lines)-1] != "https://pages.example/org/slug" {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
	if !strings.HasPrefix(lines[0], "Published") {
		t.Errorf("first line = %q", lines[0])
	}
	request := mock.publishRequests[0]
	if request.Language != "html" || request.Title != "report" || request.Content != "<h1>hi</h1>" || request.TTLHours != nil {
		t.Errorf("request = %+v", request)
	}

	again := &canvasMock{publishResult: newCanvasPublished(false)}
	output, err = runCanvas(t, again, "publish", path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(output, "Updated") {
		t.Errorf("output = %q", output)
	}
	second := again.publishRequests[0]
	if second.ConversationID != request.ConversationID || second.ArtifactID != request.ArtifactID {
		t.Errorf("identity changed between publishes: %+v vs %+v", second, request)
	}
	if request.ConversationID == request.ArtifactID || len(request.ArtifactID) != 36 {
		t.Errorf("identity = %q / %q", request.ConversationID, request.ArtifactID)
	}

	other := writeCanvasFile(t, "report.html", "<h1>hi</h1>")
	otherMock := &canvasMock{publishResult: newCanvasPublished(true)}
	if _, err := runCanvas(t, otherMock, "publish", other); err != nil {
		t.Fatal(err)
	}
	if otherMock.publishRequests[0].ArtifactID == request.ArtifactID {
		t.Errorf("different paths share an identity")
	}
}

func TestCanvasPublish_ExplicitFlags(t *testing.T) {
	path := writeCanvasFile(t, "notes.txt", "# Notes")
	mock := &canvasMock{publishResult: newCanvasPublished(true)}
	_, err := runCanvas(t, mock, "publish", path, "--language", "MARKDOWN", "--title", "Notes",
		"--conversation-id", "c1", "--artifact-id", "a1", "--ttl-hours", "12")
	if err != nil {
		t.Fatal(err)
	}
	request := mock.publishRequests[0]
	if request.Language != "markdown" || request.Title != "Notes" || request.ConversationID != "c1" ||
		request.ArtifactID != "a1" || request.TTLHours == nil || *request.TTLHours != 12 {
		t.Errorf("request = %+v", request)
	}
}

func TestCanvasPublish_JSONOutput(t *testing.T) {
	path := writeCanvasFile(t, "diagram.svg", "<svg/>")
	mock := &canvasMock{publishResult: newCanvasPublished(true)}
	output, err := runCanvas(t, mock, "publish", path, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"url": "https://pages.example/org/slug"`) || strings.Contains(output, "Published") {
		t.Errorf("output = %s", output)
	}
	if mock.publishRequests[0].Language != "svg" {
		t.Errorf("language = %s", mock.publishRequests[0].Language)
	}
}

func TestCanvasPublish_ClientSideRefusals(t *testing.T) {
	large := writeCanvasFile(t, "large.html", strings.Repeat("a", canvasMaximumCharacters+1))
	exact := writeCanvasFile(t, "exact.html", strings.Repeat("é", canvasMaximumCharacters))
	hugeBytes := writeCanvasFile(t, "huge.md", strings.Repeat("a", canvasMaximumBytes+1))
	script := writeCanvasFile(t, "app.js", "alert(1)")
	noExtension := writeCanvasFile(t, "page", "x")
	empty := writeCanvasFile(t, "empty.html", "  \n")
	directory := t.TempDir()
	htmlDirectory := filepath.Join(directory, "folder.html")
	if err := os.Mkdir(htmlDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{"over limit", []string{"publish", large}, "200000 character limit"},
		{"over limit in bytes", []string{"publish", hugeBytes}, "200000 character limit"},
		{"unsupported extension", []string{"publish", script}, "only html, svg, markdown"},
		{"no extension", []string{"publish", noExtension}, "without a file extension"},
		{"unsupported language flag", []string{"publish", exact, "--language", "javascript"}, "cannot be published"},
		{"missing file", []string{"publish", filepath.Join(directory, "missing.html")}, "does not exist"},
		{"directory", []string{"publish", htmlDirectory}, "directory"},
		{"empty", []string{"publish", empty}, "empty"},
		{"half identity", []string{"publish", exact, "--artifact-id", "a1"}, "must be given together"},
		{"bad ttl", []string{"publish", exact, "--ttl-hours", "0"}, "at least 1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &canvasMock{publishResult: newCanvasPublished(true)}
			_, err := runCanvas(t, mock, testCase.args...)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want %q", err, testCase.want)
			}
			if exitCodeFor(err) != exitUsage {
				t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitUsage)
			}
			if len(mock.publishRequests) != 0 {
				t.Errorf("request reached the platform")
			}
		})
	}

	mock := &canvasMock{publishResult: newCanvasPublished(true)}
	if _, err := runCanvas(t, mock, "publish", exact); err != nil {
		t.Errorf("a file exactly at the limit must publish: %v", err)
	}
}

func TestCanvasPublish_PlatformErrors(t *testing.T) {
	path := writeCanvasFile(t, "report.html", "<p>x</p>")
	for _, testCase := range []struct {
		name     string
		err      error
		want     string
		wantExit int
	}{
		{"disabled", &client.CanvasPagesError{StatusCode: 503, Code: "canvas_pages_disabled"}, "not enabled on this Ankra platform", exitError},
		{"limit", &client.CanvasPagesError{StatusCode: 409, Code: "publication_limit_reached"}, "ankra canvas unpublish", exitError},
		{"language", &client.CanvasPagesError{StatusCode: 400, Code: "unsupported_language"}, "only html, svg, markdown", exitUsage},
		{"too large", &client.CanvasPagesError{StatusCode: 400, Code: "content_too_large"}, "character limit", exitUsage},
		{"ttl", &client.CanvasPagesError{StatusCode: 400, Code: "invalid_ttl"}, "--ttl-hours", exitUsage},
		{"other unavailable", &client.CanvasPagesError{StatusCode: 503, Code: "later"}, "unavailable", exitError},
		{"taken down", &client.CanvasPagesError{StatusCode: 403, Code: "publication_taken_down"}, "taken down; it cannot be republished", exitError},
		{"other owner", &client.CanvasPagesError{StatusCode: 403, Code: "publication_owned_by_another_member"}, "another member", exitError},
		{"unauthorized", client.ErrUnauthorized, "unauthorized", exitAuth},
		{"denied", &client.PermissionDeniedError{}, "permission denied", exitForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runCanvas(t, &canvasMock{publishError: testCase.err}, "publish", path)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want %q", err, testCase.want)
			}
			if exitCodeFor(err) != testCase.wantExit {
				t.Errorf("exit code = %d, want %d", exitCodeFor(err), testCase.wantExit)
			}
		})
	}
}

func TestCanvasList(t *testing.T) {
	mock := &canvasMock{listResult: &client.CanvasPublicationsListResult{
		Items:      []client.CanvasPublication{{ID: "id-1", Title: "Report", Language: "html", Status: "live", URL: "https://pages.example/o/s"}},
		NextCursor: "more",
	}}
	output, err := runCanvas(t, mock, "list", "--limit", "5", "--cursor", "abc")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id-1", "Report", "https://pages.example/o/s", "--cursor more"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q: %s", want, output)
		}
	}
	if mock.listCursor != "abc" || mock.listLimit != 5 {
		t.Errorf("cursor/limit = %q/%d", mock.listCursor, mock.listLimit)
	}

	empty := &canvasMock{listResult: &client.CanvasPublicationsListResult{}}
	output, err = runCanvas(t, empty, "list")
	if err != nil || !strings.Contains(output, "No canvas pages published") {
		t.Errorf("output = %q, err = %v", output, err)
	}

	output, err = runCanvas(t, mock, "list", "-o", "json")
	if err != nil || !strings.Contains(output, `"next_cursor": "more"`) {
		t.Errorf("output = %q, err = %v", output, err)
	}

	if _, err := runCanvas(t, mock, "list", "--limit", "-1"); err == nil || exitCodeFor(err) != exitUsage {
		t.Errorf("negative limit err = %v", err)
	}
	failing := &canvasMock{listResult: nil}
	_ = failing
}

func TestCanvasUnpublish(t *testing.T) {
	mock := &canvasMock{}
	output, err := runCanvas(t, mock, "unpublish", "id-1", "--yes")
	if err != nil || len(mock.unpublished) != 1 || !strings.Contains(output, "unpublished") {
		t.Fatalf("output = %q, err = %v, calls = %v", output, err, mock.unpublished)
	}

	missing := &canvasMock{unpublishError: client.ErrCanvasPublicationNotFound}
	_, err = runCanvas(t, missing, "unpublish", "id-2", "--yes")
	if err == nil || exitCodeFor(err) != exitNotFound || !strings.Contains(err.Error(), "ankra canvas list") {
		t.Errorf("err = %v", err)
	}

	broken := &canvasMock{unpublishError: errors.New("boom")}
	if _, err = runCanvas(t, broken, "unpublish", "id-3", "--yes"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}

	declined := &canvasMock{}
	setMockClient(t, declined)
	resetCanvasFlags(t)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader("n\n"))
	rootCmd.SetArgs([]string{"canvas", "unpublish", "id-4"})
	err = rootCmd.Execute()
	rootCmd.SetIn(nil)
	if exitCodeFor(err) != exitCancelled || len(declined.unpublished) != 0 {
		t.Errorf("err = %v, calls = %v", err, declined.unpublished)
	}
}

func TestCanvasDerivedUUIDShape(t *testing.T) {
	identifier := canvasDerivedUUID("seed")
	if len(identifier) != 36 || identifier[14] != '5' || !looksLikeUUID(identifier) {
		t.Errorf("identifier = %s", identifier)
	}
}
