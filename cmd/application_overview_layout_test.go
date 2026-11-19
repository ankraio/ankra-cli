package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

// These drive `ankra application overview-layout` through the real client
// against a stub platform, so what is asserted is the request that leaves
// and what the command prints from the answer.

const (
	overviewTestVersionID     = "0b7c4d0a-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	overviewTestOlderID       = "0b7c4d0b-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	overviewTestNewerID       = "0b7c4d0c-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	overviewTestLayoutPath    = "/api/v1/org/applications/" + testApplicationID + "/overview-layout"
	overviewTestVersionsPath  = overviewTestLayoutPath + "/versions"
	overviewTestUseAnkraPath  = overviewTestLayoutPath + "/use-ankra"
	overviewTestPutBackPath   = overviewTestVersionsPath + "/" + overviewTestOlderID + "/put-back"
	overviewTestStaleSentence = "The Overview layout changed since base_revision_id; reload it and apply the change again."
)

// overviewTestContent is a layout of two summary cards and a chart, with an
// element the root does not reach, which the page does not draw.
const overviewTestContent = `{"schema_version": 2, "root": "page", "elements": {
  "page": {"type": "Grid", "children": ["health", "errors", "releases"]},
  "releases": {"type": "ApplicationCard", "props": {"card": "releases"}},
  "health": {"type": "ApplicationCard", "props": {"card": "health"}},
  "errors": {"type": "LineChart", "props": {"title": "Error rate", "binding": {"dataset": "application_error_rate", "params": {"application": "` + testApplicationID + `"}}}},
  "orphan": {"type": "ApplicationCard", "props": {"card": "cost"}}
}}`

func overviewTestTeamLayout(versionID string, number int, author string) string {
	encodedContent, _ := json.Marshal(overviewTestContent)
	authorName := "null"
	if author != "" {
		authorName = `"` + author + `"`
	}
	return `{"on_page": "team", "revision": {"id": "` + versionID + `", "number": ` + itoaForTest(number) +
		`, "content": ` + string(encodedContent) + `, "author_user_id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", "author_name": ` +
		authorName + `, "created_at": "2026-11-18T09:30:00Z"}, "versions": ` + itoaForTest(number) + `}`
}

func itoaForTest(number int) string {
	encoded, _ := json.Marshal(number)
	return string(encoded)
}

const overviewTestAnkraLayout = `{"on_page": "ankra", "revision": null, "versions": 3}`

type overviewStubAnswer struct {
	status int
	body   string
}

type overviewStubRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// overviewStub is a fake platform: one answer per "METHOD path", and every
// request it received, in order.
type overviewStub struct {
	t        *testing.T
	answers  map[string]overviewStubAnswer
	mutex    sync.Mutex
	requests []overviewStubRequest
}

func newOverviewStub(t *testing.T, answers map[string]overviewStubAnswer) *overviewStub {
	return &overviewStub{t: t, answers: answers}
}

func (stub *overviewStub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	recorded := overviewStubRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery}
	if payload, _ := io.ReadAll(request.Body); len(payload) > 0 {
		if decodeError := json.Unmarshal(payload, &recorded.body); decodeError != nil {
			stub.t.Errorf("request body is not a JSON object: %v: %s", decodeError, payload)
		}
	}
	stub.mutex.Lock()
	stub.requests = append(stub.requests, recorded)
	stub.mutex.Unlock()
	answer, isKnown := stub.answers[request.Method+" "+request.URL.Path]
	if !isKnown {
		stub.t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		answer = overviewStubAnswer{http.StatusNotFound, `{"detail": "Not Found"}`}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(answer.status)
	_, _ = writer.Write([]byte(answer.body))
}

// sent returns the requests made with method to path.
func (stub *overviewStub) sent(method string, path string) []overviewStubRequest {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	matched := []overviewStubRequest{}
	for _, request := range stub.requests {
		if request.method == method && request.path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

func runOverviewLayout(t *testing.T, stub *overviewStub, input string, arguments ...string) (string, error) {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	return runApplicationCommandWithInput(t, client.New("test-token", server.URL), input,
		append([]string{"overview-layout"}, arguments...)...)
}

func writeOverviewTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layout.json")
	if writeError := os.WriteFile(path, []byte(content), 0o600); writeError != nil {
		t.Fatalf("writing the layout file: %v", writeError)
	}
	return path
}

func TestOverviewLayoutShowSaysWhichVersionIsOnThePageAndListsItsCardsInPageOrder(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestLayoutPath: {http.StatusOK, overviewTestTeamLayout(overviewTestVersionID, 3, "Ada")},
	})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID)

	if executeError != nil {
		t.Fatalf("show error = %v", executeError)
	}
	expected := strings.Join([]string{
		"The Overview of " + testApplicationID + " shows version 3 (" + overviewTestVersionID + "), saved by Ada on 2026-11-18 09:30 UTC.",
		"3 saved versions: 'ankra application overview-layout versions " + testApplicationID + "' lists them.",
		"",
		"#  ELEMENT   CARD",
		"1  health    summary card: health",
		"2  errors    LineChart of dataset application_error_rate (Error rate)",
		"3  releases  summary card: releases",
		"",
	}, "\n")
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
}

func TestOverviewLayoutShowSaysAnkrasOwnLayoutIsNotAnEmptyLayout(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestLayoutPath: {http.StatusOK, overviewTestAnkraLayout},
	})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID)

	if executeError != nil {
		t.Fatalf("show error = %v", executeError)
	}
	expected := "The Overview of " + testApplicationID + " shows Ankra's own layout. Ankra draws it on the page and keeps no " +
		"version of it, so there are no cards to list.\n" +
		"3 saved versions: 'ankra application overview-layout versions " + testApplicationID + "' lists them.\n"
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
}

func TestOverviewLayoutShowNamesAVersionThatRecordsNoAuthor(t *testing.T) {
	answer := strings.Replace(overviewTestTeamLayout(overviewTestVersionID, 1, ""),
		`"author_user_id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"`, `"author_user_id": null`, 1)
	stub := newOverviewStub(t, map[string]overviewStubAnswer{"GET " + overviewTestLayoutPath: {http.StatusOK, answer}})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID)

	if executeError != nil {
		t.Fatalf("show error = %v", executeError)
	}
	if !strings.Contains(output, "shows version 1 ("+overviewTestVersionID+"), saved on 2026-11-18 09:30 UTC; it records no author.") ||
		!strings.Contains(output, "1 saved version:") {
		t.Errorf("output:\n%s", output)
	}
}

func TestOverviewLayoutShowJSONPrintsThePlatformsDocument(t *testing.T) {
	answer := overviewTestTeamLayout(overviewTestVersionID, 3, "Ada")
	stub := newOverviewStub(t, map[string]overviewStubAnswer{"GET " + overviewTestLayoutPath: {http.StatusOK, answer}})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID, "-o", "json")

	if executeError != nil {
		t.Fatalf("show -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(answer), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
}

func TestOverviewLayoutShowContentPrintsOnlyTheLayout(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestLayoutPath: {http.StatusOK, overviewTestTeamLayout(overviewTestVersionID, 3, "Ada")},
	})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID, "--content")

	if executeError != nil {
		t.Fatalf("show --content error = %v", executeError)
	}
	if output != overviewTestContent {
		t.Errorf("output = %q, want the layout", output)
	}
}

func TestOverviewLayoutShowContentOfAnkrasLayoutExitsNotFound(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{"GET " + overviewTestLayoutPath: {http.StatusOK, overviewTestAnkraLayout}})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID, "--content")

	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitNotFound, executeError)
	}
	if executeError == nil || !strings.Contains(executeError.Error(), "shows Ankra's own layout, which the platform keeps no content of") {
		t.Errorf("error = %v", executeError)
	}
	if strings.Contains(output, "{") {
		t.Errorf("printed a layout:\n%s", output)
	}
}

func TestOverviewLayoutShowSaysWhenTheCardsCannotBeRead(t *testing.T) {
	answer := `{"on_page": "team", "revision": {"id": "` + overviewTestVersionID + `", "number": 2, "content": "not json",
	  "author_user_id": null, "author_name": "Ada", "created_at": "2026-11-18T09:30:00Z"}, "versions": 2}`
	stub := newOverviewStub(t, map[string]overviewStubAnswer{"GET " + overviewTestLayoutPath: {http.StatusOK, answer}})

	output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID)

	if executeError != nil {
		t.Fatalf("show error = %v", executeError)
	}
	if !strings.Contains(output, "The cards of version 2 could not be read from its layout") || strings.Contains(output, "places no cards") {
		t.Errorf("output:\n%s", output)
	}
}

func TestOverviewLayoutShowRefusalPrintsThePlatformsReason(t *testing.T) {
	cases := map[string]struct {
		answer           overviewStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"not found":         {overviewStubAnswer{http.StatusNotFound, `{"detail": "Application not found"}`}, exitNotFound, "Application not found"},
		"permission denied": {overviewStubAnswer{http.StatusForbidden, `{"detail": "permission_denied", "permission": "applications.read"}`}, exitForbidden, `"applications.read"`},
		"server error":      {overviewStubAnswer{http.StatusInternalServerError, `Internal Server Error`}, exitError, "reading the Overview layout of application"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newOverviewStub(t, map[string]overviewStubAnswer{"GET " + overviewTestLayoutPath: testCase.answer})

			output, executeError := runOverviewLayout(t, stub, "", "show", testApplicationID)

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if strings.Contains(output, "Ankra's own layout") || strings.Contains(output, "CARD") {
				t.Errorf("a refused read printed a layout:\n%s", output)
			}
		})
	}
}

func TestOverviewLayoutVersionsListsThePageAndSaysWhenMoreAreKept(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestVersionsPath: {http.StatusOK, `{"items": [
		  {"id": "` + overviewTestNewerID + `", "number": 3, "author_user_id": "u-1", "author_name": "Ada", "created_at": "2026-11-18T09:30:00Z", "size_bytes": 812, "on_page": true},
		  {"id": "` + overviewTestOlderID + `", "number": 2, "author_user_id": "u-2", "author_name": null, "created_at": "2026-11-17T08:00:00Z", "size_bytes": 640, "on_page": false}
		], "next_cursor": "cursor-2"}`},
	})

	output, executeError := runOverviewLayout(t, stub, "", "versions", testApplicationID, "--limit", "2")

	if executeError != nil {
		t.Fatalf("versions error = %v", executeError)
	}
	expected := strings.Join([]string{
		"Showing 2 versions. More are kept: re-run with --cursor cursor-2",
		"VERSION  SAVED BY    SAVED AT              BYTES  ON PAGE  VERSION ID",
		"3        Ada         2026-11-18 09:30 UTC  812    yes      " + overviewTestNewerID,
		"2        member u-2  2026-11-17 08:00 UTC  640    -        " + overviewTestOlderID,
		"",
	}, "\n")
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
	if requests := stub.sent(http.MethodGet, overviewTestVersionsPath); len(requests) != 1 || requests[0].query != "limit=2" {
		t.Errorf("versions requests = %+v, want one with limit=2", requests)
	}
}

func TestOverviewLayoutVersionsWithNoneSavedSaysSo(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestVersionsPath: {http.StatusOK, `{"items": [], "next_cursor": null}`},
	})

	output, executeError := runOverviewLayout(t, stub, "", "versions", testApplicationID)

	if executeError != nil {
		t.Fatalf("versions error = %v", executeError)
	}
	if output != "No version of this Overview has been saved. It shows Ankra's own layout.\n" {
		t.Errorf("output = %q", output)
	}
}

func TestOverviewLayoutVersionsRefusedCursorIsAUsageError(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"GET " + overviewTestVersionsPath: {http.StatusBadRequest, `{"detail": "The cursor is not one this list handed out.", "error_code": "invalid_cursor"}`},
	})

	output, executeError := runOverviewLayout(t, stub, "", "versions", testApplicationID, "--cursor", "stale")

	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if executeError == nil || !strings.Contains(executeError.Error(), "The cursor is not one this list handed out. Leave --cursor out") {
		t.Errorf("error = %v", executeError)
	}
	if strings.Contains(output, "No version") {
		t.Errorf("a refused read printed an empty list:\n%s", output)
	}
}

func TestOverviewLayoutVersionsRefusesALimitOutOfRange(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{})

	_, executeError := runOverviewLayout(t, stub, "", "versions", testApplicationID, "--limit", "101")

	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if len(stub.requests) != 0 {
		t.Errorf("requests were sent: %+v", stub.requests)
	}
}

func TestOverviewLayoutSaveSendsTheFileOnItsBaseAndSaysWhatChanged(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestVersionsPath: {http.StatusCreated, overviewTestTeamLayout(overviewTestNewerID, 4, "Ada")},
	})
	path := writeOverviewTestFile(t, overviewTestContent)

	output, executeError := runOverviewLayout(t, stub, "", "save", testApplicationID, "--file", path, "--base", overviewTestVersionID)

	if executeError != nil {
		t.Fatalf("save error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, overviewTestVersionsPath)
	if len(requests) != 1 {
		t.Fatalf("save requests = %d, want 1", len(requests))
	}
	expectedBody := map[string]any{"content": overviewTestContent, "base_revision_id": overviewTestVersionID}
	if !reflect.DeepEqual(requests[0].body, expectedBody) {
		t.Errorf("save body = %v\nwant %v", requests[0].body, expectedBody)
	}
	expected := "Saved version 4 of the Overview of " + testApplicationID + " (" + overviewTestNewerID + "). The Overview shows it now.\n"
	if output != expected {
		t.Errorf("output = %q, want %q", output, expected)
	}
}

func TestOverviewLayoutSaveWithoutBaseSendsNullForAnkrasLayout(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestVersionsPath: {http.StatusCreated, overviewTestTeamLayout(overviewTestNewerID, 1, "Ada")},
	})

	_, executeError := runOverviewLayout(t, stub, overviewTestContent, "save", testApplicationID, "--file", "-")

	if executeError != nil {
		t.Fatalf("save error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, overviewTestVersionsPath)
	if len(requests) != 1 {
		t.Fatalf("save requests = %d, want 1", len(requests))
	}
	base, hasBase := requests[0].body["base_revision_id"]
	if !hasBase || base != nil {
		t.Errorf("base_revision_id = %v (sent %v), want null", base, hasBase)
	}
}

func TestOverviewLayoutSaveOnAStaleBaseSaysWhatTheOverviewShowsNowAndExitsOne(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestVersionsPath: {http.StatusConflict, `{"detail": "` + overviewTestStaleSentence + `",
		  "error_code": "canvas_revision_conflict", "current_revision_id": "` + overviewTestNewerID + `",
		  "layout": ` + overviewTestTeamLayout(overviewTestNewerID, 4, "Grace") + `}`},
	})
	path := writeOverviewTestFile(t, overviewTestContent)

	output, executeError := runOverviewLayout(t, stub, "", "save", testApplicationID, "--file", path, "--base", overviewTestVersionID)

	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitError, executeError)
	}
	expected := "saving the layout: " + overviewTestStaleSentence + " Nothing changed. The Overview shows version 4 (" +
		overviewTestNewerID + "), saved by Grace on 2026-11-18 09:30 UTC now. Make the change on that version " +
		"('ankra application overview-layout show " + testApplicationID + " --content' prints it) and pass --base " + overviewTestNewerID + "."
	if executeError == nil || executeError.Error() != expected {
		t.Errorf("error = %v\nwant %s", executeError, expected)
	}
	if strings.Contains(output, "Saved version") {
		t.Errorf("a refused save was reported done:\n%s", output)
	}
}

func TestOverviewLayoutStaleBaseOnAnkrasLayoutSaysToLeaveBaseOut(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestUseAnkraPath: {http.StatusConflict, `{"detail": "` + overviewTestStaleSentence + `",
		  "error_code": "canvas_revision_conflict", "current_revision_id": null, "layout": ` + overviewTestAnkraLayout + `}`},
	})

	_, executeError := runOverviewLayout(t, stub, "", "use-ankra", testApplicationID, "--base", overviewTestVersionID)

	if executeError == nil || !strings.HasSuffix(executeError.Error(),
		"Nothing changed. The Overview shows Ankra's own layout now. Make the change on that and leave --base out.") {
		t.Errorf("error = %v", executeError)
	}
}

func TestOverviewLayoutSaveRefusedLayoutPrintsThePlatformsSentence(t *testing.T) {
	sentence := "element orphan: props.card must be one of health, environments, services, cost, safety, releases, runs, backups."
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestVersionsPath: {http.StatusBadRequest, `{"detail": "` + sentence + `", "error_code": "invalid_canvas_spec"}`},
	})
	path := writeOverviewTestFile(t, overviewTestContent)

	_, executeError := runOverviewLayout(t, stub, "", "save", testApplicationID, "--file", path)

	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitError, executeError)
	}
	if executeError == nil || executeError.Error() != "saving the layout: "+sentence {
		t.Errorf("error = %v", executeError)
	}
}

func TestOverviewLayoutSaveRefusesBeforeSendingAnything(t *testing.T) {
	cases := map[string]struct {
		fileContent  string
		arguments    []string
		expectedText string
	}{
		"not JSON":           {"<html>", nil, "does not contain valid JSON"},
		"empty file":         {"  \n", nil, "is empty: nothing to save"},
		"base not an id":     {overviewTestContent, []string{"--base", "3"}, `--base "3" is not a version id`},
		"base given, empty":  {overviewTestContent, []string{"--base", " "}, "--base is empty"},
		"no file flag given": {"", []string{"--no-file"}, "required flag(s) \"file\""},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newOverviewStub(t, map[string]overviewStubAnswer{})
			arguments := []string{"save", testApplicationID}
			if len(testCase.arguments) == 1 && testCase.arguments[0] == "--no-file" {
				arguments = append(arguments, "--base", overviewTestVersionID)
			} else {
				arguments = append(arguments, "--file", writeOverviewTestFile(t, testCase.fileContent))
				arguments = append(arguments, testCase.arguments...)
			}

			_, executeError := runOverviewLayout(t, stub, "", arguments...)

			if exitCodeFor(executeError) != exitUsage {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if len(stub.requests) != 0 {
				t.Errorf("requests were sent: %+v", stub.requests)
			}
		})
	}
}

func TestOverviewLayoutPutBackNamesTheVersionAndTheBaseAndSaysWhatChanged(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestPutBackPath: {http.StatusCreated, overviewTestTeamLayout(overviewTestNewerID, 5, "Ada")},
	})

	output, executeError := runOverviewLayout(t, stub, "", "put-back", testApplicationID, overviewTestOlderID, "--base", overviewTestVersionID)

	if executeError != nil {
		t.Fatalf("put-back error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, overviewTestPutBackPath)
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body, map[string]any{"base_revision_id": overviewTestVersionID}) {
		t.Errorf("put-back requests = %+v", requests)
	}
	expected := "Put version " + overviewTestOlderID + " back on the Overview of " + testApplicationID + " as version 5 (" +
		overviewTestNewerID + "). The Overview shows it now.\n"
	if output != expected {
		t.Errorf("output = %q, want %q", output, expected)
	}
}

func TestOverviewLayoutPutBackOfAnUnknownVersionExitsNotFoundAndSaysWhereTheyAreListed(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestPutBackPath: {http.StatusNotFound, `{"detail": "Overview layout version not found.", "error_code": "overview_layout_version_not_found"}`},
	})

	_, executeError := runOverviewLayout(t, stub, "", "put-back", testApplicationID, overviewTestOlderID)

	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitNotFound, executeError)
	}
	expected := "putting back version " + overviewTestOlderID + ": Overview layout version not found. " +
		"'ankra application overview-layout versions " + testApplicationID + "' lists them"
	if executeError == nil || executeError.Error() != expected {
		t.Errorf("error = %v\nwant %s", executeError, expected)
	}
}

func TestOverviewLayoutUseAnkraSaysWhatChanged(t *testing.T) {
	cases := map[string]struct {
		arguments    []string
		expectedBase any
		expected     string
	}{
		"from a saved version": {[]string{"--base", overviewTestVersionID}, overviewTestVersionID,
			"The Overview of " + testApplicationID + " shows Ankra's own layout now. Its 3 saved versions stay listed.\n"},
		"already on Ankra's layout": {nil, nil,
			"The Overview of " + testApplicationID + " already showed Ankra's own layout. Nothing changed.\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newOverviewStub(t, map[string]overviewStubAnswer{
				"POST " + overviewTestUseAnkraPath: {http.StatusOK, overviewTestAnkraLayout},
			})

			output, executeError := runOverviewLayout(t, stub, "", append([]string{"use-ankra", testApplicationID}, testCase.arguments...)...)

			if executeError != nil {
				t.Fatalf("use-ankra error = %v", executeError)
			}
			requests := stub.sent(http.MethodPost, overviewTestUseAnkraPath)
			if len(requests) != 1 {
				t.Fatalf("use-ankra requests = %d, want 1", len(requests))
			}
			if base, hasBase := requests[0].body["base_revision_id"]; !hasBase || base != testCase.expectedBase {
				t.Errorf("base_revision_id = %v (sent %v), want %v", base, hasBase, testCase.expectedBase)
			}
			if output != testCase.expected {
				t.Errorf("output = %q, want %q", output, testCase.expected)
			}
		})
	}
}

func TestOverviewLayoutUseAnkraJSONPrintsThePlatformsDocument(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestUseAnkraPath: {http.StatusOK, overviewTestAnkraLayout},
	})

	output, executeError := runOverviewLayout(t, stub, "", "use-ankra", testApplicationID, "--base", overviewTestVersionID, "-o", "json")

	if executeError != nil {
		t.Fatalf("use-ankra -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(overviewTestAnkraLayout), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
}

func TestOverviewLayoutChangeRefusedForPermissionExitsSeven(t *testing.T) {
	stub := newOverviewStub(t, map[string]overviewStubAnswer{
		"POST " + overviewTestUseAnkraPath: {http.StatusForbidden, `{"detail": "permission_denied", "permission": "applications.write"}`},
	})

	_, executeError := runOverviewLayout(t, stub, "", "use-ankra", testApplicationID, "--base", overviewTestVersionID)

	if exitCodeFor(executeError) != exitForbidden {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitForbidden, executeError)
	}
	if executeError == nil || !strings.Contains(executeError.Error(), `"applications.write"`) {
		t.Errorf("error = %v", executeError)
	}
}
