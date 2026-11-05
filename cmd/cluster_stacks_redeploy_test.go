package cmd

// `ankra cluster stacks redeploy` against a scripted platform: the real client
// talks to an httptest server, so the path and the query each call sends are
// pinned on the request itself.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const stackRedeployTestPath = "/api/v1/org/clusters/imported/" + testClusterID + "/stacks/web/redeploy"

type stackRedeployServer struct {
	status     int
	body       string
	calls      int
	lastPath   string
	lastQuery  string
	lastMethod string
}

func newStackRedeployServer(t *testing.T, status int, body string) *stackRedeployServer {
	t.Helper()
	recorder := &stackRedeployServer{status: status, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.calls++
		recorder.lastPath = request.URL.Path
		recorder.lastQuery = request.URL.RawQuery
		recorder.lastMethod = request.Method
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(recorder.status)
		_, _ = writer.Write([]byte(recorder.body))
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

// runStackRedeploy runs the verb with no terminal to ask on, the way a script
// or an agent runs it.
func runStackRedeploy(t *testing.T, arguments ...string) (string, string, error) {
	t.Helper()
	return runStackRedeployAnswering(t, "", false, arguments...)
}

// runStackRedeployAnswering runs the verb with input on stdin; a prompt is
// only answered when isInteractive says a terminal is there to ask on.
func runStackRedeployAnswering(t *testing.T, input string, isInteractive bool, arguments ...string) (string, string, error) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return isInteractive }
	t.Cleanup(func() { promptIsInteractive = previous })

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "stacks", "redeploy"}, arguments...))
	commands := []*cobra.Command{clusterStacksRedeployCmd}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, commands...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

const stackRedeployStartedBody = `{"stack_name":"web","operation_id":"operation-1","job_count":2,` +
	`"operations":[{"operation_id":"operation-1","job_count":2,"resource_ids":["manifest-1"]}],` +
	`"skipped_resource_ids":["manifest-2","manifest-3"],` +
	`"skipped_resources":[{"resource_id":"manifest-2","reason":"overlap"},{"resource_id":"manifest-3","reason":"a_new_reason"}],` +
	`"force_conflicts":%FORCE%}`

func stackRedeployBody(force string) string {
	body := strings.ReplaceAll(stackRedeployStartedBody, "%FORCE%", force)
	if force == "true" {
		return strings.TrimSuffix(body, "}") + `,"forced_resource_ids":["manifest-1"]}`
	}
	return body
}

func TestStackRedeployPlainSendsNoForceAndPrintsTheOutcome(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("false"))
	stdout, stderr, runError := runStackRedeploy(t, "web")
	if runError != nil {
		t.Fatalf("redeploy failed: %v", runError)
	}
	if recorder.lastMethod != http.MethodPost || recorder.lastPath != stackRedeployTestPath || recorder.lastQuery != "" {
		t.Fatalf("request = %s %s?%s", recorder.lastMethod, recorder.lastPath, recorder.lastQuery)
	}
	for _, want := range []string{
		"Redeploy started.", "Stack:  web", "Jobs:   2", "operation-1 (2 jobs)",
		"manifest-2: an operation already in flight covers it",
		"manifest-3: a_new_reason",
		"Follow it with 'ankra cluster operations list operation-1'",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not say %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "orce") {
		t.Errorf("a plain redeploy must not read as forced:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Redeploying stack 'web'") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestStackRedeployPlainNeverAsksEvenOnATerminal(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("false"))
	_, stderr, runError := runStackRedeployAnswering(t, "", true, "web")
	if runError != nil {
		t.Fatalf("a plain redeploy asked or failed: %v", runError)
	}
	if recorder.calls != 1 || strings.Contains(stderr, "[y/N]") || strings.Contains(stderr, "cannot be undone") {
		t.Fatalf("a plain redeploy must not ask: calls %d, stderr %q", recorder.calls, stderr)
	}
}

func TestStackRedeployForceWithoutATerminalNeedsYesAndSendsNothing(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("true"))
	_, stderr, runError := runStackRedeploy(t, "web", "--force")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "re-run with --yes") {
		t.Fatalf("error = %v (exit %d), want the usage exit naming --yes", runError, exitCodeFor(runError))
	}
	if recorder.calls != 0 {
		t.Fatalf("an unconfirmed force reached the platform %d times", recorder.calls)
	}
	if !strings.Contains(stderr, "cannot be undone") {
		t.Errorf("the consequence was not stated before refusing: %q", stderr)
	}
}

func TestStackRedeployForceDeclinedOnATerminalSendsNothing(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("true"))
	_, stderr, runError := runStackRedeployAnswering(t, "n\n", true, "web", "--force")
	if runError == nil || exitCodeFor(runError) != exitCodeFor(errCancelled) {
		t.Fatalf("error = %v (exit %d), want the declined exit %d", runError, exitCodeFor(runError), exitCodeFor(errCancelled))
	}
	if recorder.calls != 0 {
		t.Fatalf("a declined force reached the platform %d times", recorder.calls)
	}
	for _, want := range []string{"stack 'web'", "on the live objects", "cannot be undone", "add-ons are not forced", "[y/N]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the prompt does not say %q: %q", want, stderr)
		}
	}
}

func TestStackRedeployForceConfirmedOnATerminalSendsTheQuery(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("true"))
	_, _, runError := runStackRedeployAnswering(t, "y\n", true, "web", "--force")
	if runError != nil {
		t.Fatalf("a confirmed force failed: %v", runError)
	}
	if recorder.calls != 1 || recorder.lastQuery != "force=true" {
		t.Fatalf("calls %d, query %q, want one call with force=true", recorder.calls, recorder.lastQuery)
	}
}

func TestStackRedeployForceSendsTheQueryAndSaysSo(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("true"))
	stdout, stderr, runError := runStackRedeploy(t, "web", "--force", "--yes")
	if runError != nil {
		t.Fatalf("forced redeploy failed: %v", runError)
	}
	if recorder.lastQuery != "force=true" {
		t.Fatalf("query = %q, want force=true", recorder.lastQuery)
	}
	for _, want := range []string{"Forced redeploy started.", "for this redeploy only", "Forced:\n    - manifest-1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not say %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "Force redeploying stack 'web'") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestStackRedeployForcedThatStartedNothingSaysNothingWasForced(t *testing.T) {
	for name, testCase := range map[string]struct {
		body string
		want string
	}{
		"nothing picked": {
			body: `{"stack_name":"web","operation_id":null,"job_count":0,"operations":[],"skipped_resource_ids":[],"skipped_resources":[],"force_conflicts":true}`,
			want: "this redeploy picked no members",
		},
		"everything skipped": {
			body: `{"stack_name":"web","operation_id":null,"job_count":0,"operations":[],"skipped_resource_ids":["manifest-1"],"skipped_resources":[{"resource_id":"manifest-1","reason":"overlap"}],"force_conflicts":true}`,
			want: "every member this redeploy picked was skipped",
		},
	} {
		t.Run(name, func(t *testing.T) {
			newStackRedeployServer(t, http.StatusOK, testCase.body)
			stdout, _, runError := runStackRedeploy(t, "web", "--force", "--yes")
			if runError != nil {
				t.Fatalf("a redeploy that started nothing is not a failure: %v", runError)
			}
			if !strings.Contains(stdout, testCase.want) || !strings.Contains(stdout, "Nothing was forced.") {
				t.Errorf("stdout = %s", stdout)
			}
			if strings.Contains(stdout, "Forced redeploy started") || strings.Contains(stdout, "Follow it with") {
				t.Errorf("a redeploy that started nothing reads as one that ran:\n%s", stdout)
			}
		})
	}
}

// TestStackRedeployForcedThatReachedNoManifestIsNotReportedAsForced is the case
// the echoed force_conflicts would misreport: operations started, but none of
// them applies a manifest, so the manifest that failed was not touched.
func TestStackRedeployForcedThatReachedNoManifestIsNotReportedAsForced(t *testing.T) {
	for name, testCase := range map[string]struct {
		forcedMember string
		want         string
	}{
		"the platform says none":    {forcedMember: `,"forced_resource_ids":[]`, want: "Nothing was forced: what it re-runs applies no manifest."},
		"the platform does not say": {forcedMember: ``, want: "The platform did not say which manifests were forced"},
	} {
		t.Run(name, func(t *testing.T) {
			body := strings.TrimSuffix(strings.ReplaceAll(stackRedeployStartedBody, "%FORCE%", "true"), "}") + testCase.forcedMember + "}"
			newStackRedeployServer(t, http.StatusOK, body)
			stdout, _, runError := runStackRedeploy(t, "web", "--force", "--yes")
			if runError != nil {
				t.Fatalf("redeploy failed: %v", runError)
			}
			if !strings.Contains(stdout, testCase.want) || !strings.Contains(stdout, "Redeploy started.") {
				t.Errorf("stdout does not say %q:\n%s", testCase.want, stdout)
			}
			if strings.Contains(stdout, "Forced redeploy started") || strings.Contains(stdout, "Forced:") {
				t.Errorf("a redeploy that reached no manifest reads as forced:\n%s", stdout)
			}
		})
	}
}

func TestStackRedeployJSONCarriesTheResultAndNoProgressOnStderr(t *testing.T) {
	newStackRedeployServer(t, http.StatusOK, stackRedeployBody("false"))
	stdout, stderr, runError := runStackRedeploy(t, "web", "-o", "json")
	if runError != nil {
		t.Fatalf("redeploy failed: %v", runError)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	for _, key := range []string{"stack_name", "operation_id", "job_count", "operations",
		"skipped_resource_ids", "skipped_resources", "force_conflicts"} {
		if _, isPresent := document[key]; !isPresent {
			t.Errorf("the document lost %s: %s", key, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("structured output must keep stderr clean, got %q", stderr)
	}
	if _, isPresent := document["forced_resource_ids"]; isPresent {
		t.Errorf("a plain redeploy's document grew forced_resource_ids: %s", stdout)
	}
}

func TestStackRedeployForcedJSONKeepsStdoutParseable(t *testing.T) {
	newStackRedeployServer(t, http.StatusOK, stackRedeployBody("true"))
	stdout, _, runError := runStackRedeploy(t, "web", "--force", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("forced redeploy failed: %v", runError)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil {
		t.Fatalf("the confirmation leaked into stdout: %v\n%s", decodeError, stdout)
	}
	if forced, _ := document["forced_resource_ids"].([]any); len(forced) != 1 || forced[0] != "manifest-1" {
		t.Errorf("forced_resource_ids = %v", document["forced_resource_ids"])
	}
}

func TestStackRedeployForcedJSONKeepsAnEmptyReachApartFromAMissingOne(t *testing.T) {
	body := strings.TrimSuffix(strings.ReplaceAll(stackRedeployStartedBody, "%FORCE%", "true"), "}") + `,"forced_resource_ids":[]}`
	newStackRedeployServer(t, http.StatusOK, body)
	stdout, _, runError := runStackRedeploy(t, "web", "--force", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("forced redeploy failed: %v", runError)
	}
	if !strings.Contains(stdout, `"forced_resource_ids": []`) {
		t.Errorf("a force that reached nothing lost its empty list:\n%s", stdout)
	}
}

func TestStackRedeployRefusalsExitWithTheirOwnCodes(t *testing.T) {
	for name, testCase := range map[string]struct {
		status   int
		body     string
		wantCode int
		want     string
	}{
		"the floor": {
			status:   http.StatusForbidden,
			body:     `{"detail":"permission_denied","permission":"clusters.write","scope_type":"cluster","scope_id":"` + testClusterID + `"}`,
			wantCode: exitForbidden, want: "clusters.write",
		},
		"a missing stack": {
			status: http.StatusNotFound, body: `{"detail":"Stack not found"}`,
			wantCode: exitNotFound, want: `stack "web" not found`,
		},
		"a cluster outside the organisation": {
			status: http.StatusNotFound, body: `{"detail":"Cluster not found"}`,
			wantCode: exitNotFound, want: "not found in this organisation",
		},
		"a platform without the route": {
			status: http.StatusNotFound, body: `{"detail":"Not Found"}`,
			wantCode: exitError, want: "does not offer a stack redeploy to API tokens yet",
		},
		"a 404 that gives no reason": {
			status: http.StatusNotFound, body: `not json`,
			wantCode: exitError, want: "without saying why",
		},
		"a platform failure": {
			status: http.StatusInternalServerError, body: `Internal Server Error`,
			wantCode: exitError, want: "redeploying the stack",
		},
	} {
		t.Run(name, func(t *testing.T) {
			newStackRedeployServer(t, testCase.status, testCase.body)
			_, _, runError := runStackRedeploy(t, "web", "--force", "--yes")
			if runError == nil || exitCodeFor(runError) != testCase.wantCode || !strings.Contains(runError.Error(), testCase.want) {
				t.Fatalf("error = %v (exit %d), want exit %d saying %q", runError, exitCodeFor(runError), testCase.wantCode, testCase.want)
			}
		})
	}
}

func TestStackRedeployEmptyStackNameIsAUsageErrorAndSendsNothing(t *testing.T) {
	recorder := newStackRedeployServer(t, http.StatusOK, stackRedeployBody("false"))
	_, _, runError := runStackRedeploy(t, "  ", "--force", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("error = %v (exit %d), want the usage exit", runError, exitCodeFor(runError))
	}
	if recorder.calls != 0 {
		t.Fatalf("an empty stack name reached the platform %d times", recorder.calls)
	}
}

func TestStackRedeployHelpStatesTheSetAndTheForce(t *testing.T) {
	help := clusterStacksRedeployCmd.Long
	for _, phrase := range []string{
		"members a redeploy of this stack picks", "operations", "skipped", "conflict with", "for this redeploy",
		"nothing is stored", "names the manifests it forced", "add-ons are not forced", "application member's apply",
		"on the live objects", "cannot be undone",
		"before-first-apply", "not another controller", "clusters.write", "stacks.deploy",
		"asks for confirmation", "--yes",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("the help does not say %q:\n%s", phrase, help)
		}
	}
	if strings.ContainsRune(help, '\u2014') {
		t.Errorf("the help carries an em dash:\n%s", help)
	}
}
