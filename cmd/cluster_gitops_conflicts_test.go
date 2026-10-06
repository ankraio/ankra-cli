package cmd

// `ankra cluster gitops conflicts list|resolve` against a scripted platform:
// the real client talks to an httptest server that answers the conflict
// routes, so the method, path and body each verb sends are pinned on the
// request itself, and a refused or declined resolve is proven to send no
// write at all.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type gitopsConflictsRequest struct {
	method        string
	path          string
	authorization string
	body          map[string]any
}

// gitopsConflictsServer is a platform holding one cluster's open conflicts.
// A resolve records the side on the rows it names, as the real routes do.
type gitopsConflictsServer struct {
	mutex     sync.Mutex
	conflicts []client.GitopsConflict
	requests  []gitopsConflictsRequest
	// refusalStatus, when set, answers every conflict request whose method
	// is refusalMethod (any method when empty) with it and refusalBody.
	refusalStatus int
	refusalMethod string
	refusalBody   string
	// extraResolvedByAll is added to the resolve-all count, as a platform
	// that found more open conflicts than the CLI listed would answer.
	extraResolvedByAll int
}

func gitopsConflictFixtures() []client.GitopsConflict {
	detectedAt := "2026-07-02T08:00:00Z"
	decided := client.GitopsConflictKeepCluster
	return []client.GitopsConflict{
		{ResourceKey: "stack:db", ResourceKind: "stack", GitChangeType: "added", DBChangeType: "removed",
			DetectedAt: &detectedAt},
		{ResourceKey: "stack:web/addon:nginx", ResourceKind: "addon", GitChangeType: "modified",
			DBChangeType: "modified", DetectedAt: &detectedAt, ResolutionChoice: &decided},
	}
}

func newGitopsConflictsServer(t *testing.T, conflicts []client.GitopsConflict) *gitopsConflictsServer {
	t.Helper()
	recorder := &gitopsConflictsServer{conflicts: conflicts}
	conflictsPath := "/api/v1/org/clusters/" + testClusterID + "/gitops/conflicts"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(request.URL.Path, conflictsPath) {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		recorded := gitopsConflictsRequest{method: request.Method, path: request.URL.Path,
			authorization: request.Header.Get("Authorization")}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}

		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 && (recorder.refusalMethod == "" || recorder.refusalMethod == request.Method) {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		side, _ := recorded.body["resolution"].(string)
		switch {
		case request.Method == http.MethodGet && request.URL.Path == conflictsPath:
			_ = json.NewEncoder(writer).Encode(client.GitopsConflictList{
				Conflicts: recorder.conflicts, Total: len(recorder.conflicts)})
		case request.Method == http.MethodPost && request.URL.Path == conflictsPath+"/resolve-resource":
			resourceKey, _ := recorded.body["resource_key"].(string)
			for index := range recorder.conflicts {
				if recorder.conflicts[index].ResourceKey == resourceKey {
					recorder.conflicts[index].ResolutionChoice = &side
					_ = json.NewEncoder(writer).Encode(client.GitopsConflictResolution{ClearedCount: 1, SyncTriggered: true,
						Message: "Marked '" + resourceKey + "' to keep the " + gitopsConflictSideLabel(side) +
							" version; converging on the next sync."})
					return
				}
			}
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"No open GitOps conflict for the given resource"}`))
		case request.Method == http.MethodPost && request.URL.Path == conflictsPath+"/resolve":
			for index := range recorder.conflicts {
				recorder.conflicts[index].ResolutionChoice = &side
			}
			_ = json.NewEncoder(writer).Encode(client.GitopsConflictResolution{
				ClearedCount: len(recorder.conflicts) + recorder.extraResolvedByAll, SyncTriggered: true,
				Message: "Marked the conflicting resource(s) to keep the " + gitopsConflictSideLabel(side) +
					" version; converging on the next sync."})
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	// Every test answers on a pipe, never a terminal, unless it opts in.
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return false }
	t.Cleanup(func() { promptIsInteractive = previous })
	return recorder
}

func (recorder *gitopsConflictsServer) seen() []gitopsConflictsRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]gitopsConflictsRequest{}, recorder.requests...)
}

func (recorder *gitopsConflictsServer) writes() []gitopsConflictsRequest {
	var writes []gitopsConflictsRequest
	for _, request := range recorder.seen() {
		if request.method != http.MethodGet {
			writes = append(writes, request)
		}
	}
	return writes
}

func (recorder *gitopsConflictsServer) refuse(method string, status int, body string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.refusalMethod, recorder.refusalStatus, recorder.refusalBody = method, status, body
}

func gitopsConflictsCommands() []*cobra.Command {
	return []*cobra.Command{clusterGitopsConflictsCmd, clusterGitopsConflictsListCmd, clusterGitopsConflictsResolveCmd}
}

// runGitopsConflicts executes the command with input on stdin and returns
// stdout and stderr apart, so structured output can be checked for being
// clean.
func runGitopsConflicts(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "gitops", "conflicts"}, arguments...))
	resetTreeFlags(t, gitopsConflictsCommands()...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, gitopsConflictsCommands()...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestClusterGitopsConflictsListPrintsKeysSidesAndTheWayOut(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

	stdout, _, runError := runGitopsConflicts(t, "", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/org/clusters/"+testClusterID+"/gitops/conflicts" {
		t.Fatalf("expected one GET of the selected cluster's conflicts, got %+v", requests)
	}
	if requests[0].authorization != "Bearer test-token" {
		t.Fatalf("the list must use the bearer route, got Authorization %q", requests[0].authorization)
	}
	assertContainsAll(t, stdout, []string{
		"GitOps conflicts on cluster 'test-cluster': 2 open, 1 undecided.",
		"stack:db", "added", "removed", "undecided",
		"stack:web/addon:nginx", "modified", "keep cluster (applies on the next sync)",
		"GitOps sync is paused",
		"ankra cluster gitops conflicts resolve <resource-key> --keep git|cluster",
		"ankra cluster gitops conflicts resolve --all --keep git|cluster",
	})
}

func TestClusterGitopsConflictsListWithNothingOpen(t *testing.T) {
	newGitopsConflictsServer(t, []client.GitopsConflict{})

	stdout, _, runError := runGitopsConflicts(t, "", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if strings.TrimSpace(stdout) != "No open GitOps conflicts on cluster 'test-cluster'." {
		t.Fatalf("output = %q", stdout)
	}
}

func TestClusterGitopsConflictsListWhenEveryConflictIsDecided(t *testing.T) {
	conflicts := gitopsConflictFixtures()
	decided := client.GitopsConflictKeepGit
	conflicts[0].ResolutionChoice = &decided
	newGitopsConflictsServer(t, conflicts)

	stdout, _, runError := runGitopsConflicts(t, "", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"2 open, 0 undecided", "the next sync applies them"})
	if strings.Contains(stdout, "GitOps sync is paused") {
		t.Fatalf("a fully decided list must not claim the sync is paused:\n%s", stdout)
	}
}

func TestClusterGitopsConflictsListJSONIsTheResponseBody(t *testing.T) {
	newGitopsConflictsServer(t, gitopsConflictFixtures())

	stdout, stderr, runError := runGitopsConflicts(t, "", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("list -o json failed: %v", runError)
	}
	var decoded client.GitopsConflictList
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the JSON answer: %v\n%s", decodeError, stdout)
	}
	if decoded.Total != 2 || len(decoded.Conflicts) != 2 || decoded.Conflicts[1].ResourceKey != "stack:web/addon:nginx" ||
		decoded.Conflicts[1].DBChangeType != "modified" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if strings.Contains(stderr, "paused") {
		t.Fatalf("-o json must not print the human hints:\n%s", stderr)
	}
}

func TestClusterGitopsConflictsResolveRefusesAnIncompleteRequest(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		wantText  string
	}{
		{"no --keep", []string{"resolve", "stack:db"}, "--keep is required"},
		{"an unknown side", []string{"resolve", "stack:db", "--keep", "both"}, "--keep is required"},
		{"neither a key nor --all", []string{"resolve", "--keep", "git"}, "or pass --all"},
		{"a key and --all", []string{"resolve", "stack:db", "--all", "--keep", "git"}, "not both"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

			_, _, runError := runGitopsConflicts(t, "", append(testCase.arguments, "--yes")...)
			if code := exitCodeFor(runError); code != exitUsage {
				t.Fatalf("exit code = %d (%v), want %d (usage)", code, runError, exitUsage)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Errorf("error = %v, want it to mention %q", runError, testCase.wantText)
			}
			if requests := recorder.seen(); len(requests) != 0 {
				t.Fatalf("an incomplete request still reached the platform: %+v", requests)
			}
		})
	}
}

func TestClusterGitopsConflictsResolveWithYesRecordsTheSide(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

	stdout, stderr, runError := runGitopsConflicts(t, "", "resolve", "stack:db", "--keep", "git", "--yes")
	if runError != nil {
		t.Fatalf("resolve failed: %v", runError)
	}
	writes := recorder.writes()
	if len(writes) != 1 || writes[0].path != "/api/v1/org/clusters/"+testClusterID+"/gitops/conflicts/resolve-resource" ||
		writes[0].body["resource_key"] != "stack:db" || writes[0].body["resolution"] != "git" {
		t.Fatalf("expected one resolve-resource of stack:db keeping git, got %+v", writes)
	}
	if writes[0].authorization != "Bearer test-token" {
		t.Fatalf("the resolve must use the bearer route, got Authorization %q", writes[0].authorization)
	}
	// What is discarded is said even when --yes skips the question.
	assertContainsAll(t, stderr, []string{
		"Resolving 1 GitOps conflict on cluster 'test-cluster' by keeping the Git version",
		"stack:db  (Git: added, cluster: removed)",
		"Keeping Git discards the platform's version of this resource",
	})
	assertContainsAll(t, stdout, []string{
		"Marked 'stack:db' to keep the Git version; converging on the next sync.",
		"A sync was triggered", "ankra cluster gitops status",
	})
}

func TestClusterGitopsConflictsResolveAllKeepsTheClusterSide(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

	_, stderr, runError := runGitopsConflicts(t, "", "resolve", "--all", "--keep", "cluster", "--yes")
	if runError != nil {
		t.Fatalf("resolve --all failed: %v", runError)
	}
	writes := recorder.writes()
	if len(writes) != 1 || writes[0].path != "/api/v1/org/clusters/"+testClusterID+"/gitops/conflicts/resolve" ||
		len(writes[0].body) != 1 || writes[0].body["resolution"] != "cluster" {
		t.Fatalf("expected one resolve-all keeping cluster, got %+v", writes)
	}
	assertContainsAll(t, stderr, []string{
		"Resolving 2 GitOps conflicts on cluster 'test-cluster' by keeping the cluster version",
		"stack:db  (Git: added, cluster: removed)",
		"stack:web/addon:nginx  (Git: modified, cluster: modified), replacing the recorded choice to keep the cluster version",
		"Keeping the cluster's version discards the change made in Git to these resources",
		"including one detected after this list was read",
	})
	if strings.Contains(stderr, "Note:") {
		t.Fatalf("a count that matches the list must not print a note:\n%s", stderr)
	}
}

func TestClusterGitopsConflictsResolveAllSaysWhenMoreWereResolvedThanListed(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())
	recorder.extraResolvedByAll = 1

	_, stderr, runError := runGitopsConflicts(t, "", "resolve", "--all", "--keep", "git", "--yes")
	if runError != nil {
		t.Fatalf("resolve --all failed: %v", runError)
	}
	assertContainsAll(t, stderr, []string{"Note: 2 conflicts were listed and 3 were resolved"})
}

func TestClusterGitopsConflictsResolveAllWithNothingOpenSendsNoWrite(t *testing.T) {
	recorder := newGitopsConflictsServer(t, []client.GitopsConflict{})

	stdout, _, runError := runGitopsConflicts(t, "", "resolve", "--all", "--keep", "git", "--yes")
	if runError != nil {
		t.Fatalf("resolve --all with nothing open failed: %v", runError)
	}
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("nothing was open, yet a resolve was sent: %+v", writes)
	}
	assertContainsAll(t, stdout, []string{"No open GitOps conflicts on cluster 'test-cluster'; nothing to resolve."})
}

func TestClusterGitopsConflictsResolveAnUnlistedKeyIsNotFound(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

	_, _, runError := runGitopsConflicts(t, "", "resolve", "stack:ghost", "--keep", "git", "--yes")
	if code := exitCodeFor(runError); code != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d (not found)", code, runError, exitNotFound)
	}
	if !strings.Contains(runError.Error(), "no open GitOps conflict 'stack:ghost'") {
		t.Errorf("error = %v", runError)
	}
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("an unlisted key was still resolved: %+v", writes)
	}
}

func TestClusterGitopsConflictsResolveWithoutYesIsRefusedWithoutATerminal(t *testing.T) {
	recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())

	_, stderr, runError := runGitopsConflicts(t, "y\n", "resolve", "stack:db", "--keep", "cluster")
	if code := exitCodeFor(runError); code != exitUsage {
		t.Fatalf("exit code = %d (%v), want %d (usage)", code, runError, exitUsage)
	}
	if !strings.Contains(runError.Error(), "discards the Git version") ||
		!strings.Contains(runError.Error(), "re-run with --yes to confirm") {
		t.Errorf("the refusal must say what is discarded and how to confirm: %v", runError)
	}
	assertContainsAll(t, stderr, []string{"Keeping the cluster's version discards the change made in Git"})
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("a refused resolve still wrote: %+v", writes)
	}
}

func TestClusterGitopsConflictsResolveAsksOnATerminal(t *testing.T) {
	t.Run("yes resolves", func(t *testing.T) {
		recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())
		answerPromptInteractively(t)

		_, stderr, runError := runGitopsConflicts(t, "y\n", "resolve", "stack:db", "--keep", "git")
		if runError != nil {
			t.Fatalf("a confirmed resolve failed: %v", runError)
		}
		assertContainsAll(t, stderr, []string{"Keep the Git version? [y/N]"})
		if writes := recorder.writes(); len(writes) != 1 {
			t.Fatalf("a confirmed resolve must write once, got %+v", writes)
		}
	})

	t.Run("anything else cancels", func(t *testing.T) {
		recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())
		answerPromptInteractively(t)

		stdout, _, runError := runGitopsConflicts(t, "n\n", "resolve", "--all", "--keep", "git")
		if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
			t.Fatalf("a declined resolve must return errCancelled (exit 4), got %v", runError)
		}
		if writes := recorder.writes(); len(writes) != 0 {
			t.Fatalf("a declined resolve wrote: %+v", writes)
		}
		if strings.Contains(stdout, "Marked") {
			t.Fatalf("a declined resolve printed success:\n%s", stdout)
		}
	})
}

func TestClusterGitopsConflictsResolveJSONStaysParseable(t *testing.T) {
	newGitopsConflictsServer(t, gitopsConflictFixtures())

	stdout, stderr, runError := runGitopsConflicts(t, "", "resolve", "--all", "--keep", "git", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("resolve -o json failed: %v", runError)
	}
	var decoded client.GitopsConflictResolution
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the JSON answer: %v\n%s", decodeError, stdout)
	}
	if decoded.ClearedCount != 2 || !decoded.SyncTriggered {
		t.Fatalf("decoded = %+v", decoded)
	}
	// The confirmation text went to stderr, not into the JSON.
	assertContainsAll(t, stderr, []string{"Resolving 2 GitOps conflicts"})
}

func TestClusterGitopsConflictsRefusals(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		method    string
		status    int
		body      string
		wantExit  int
		wantText  string
	}{
		{"a resolve without clusters.write", []string{"resolve", "stack:db", "--keep", "git", "--yes"},
			http.MethodPost, http.StatusForbidden,
			`{"detail":"permission_denied","permission":"clusters.write","scope_type":"cluster"}`,
			exitForbidden, "(clusters.write) to resolve its GitOps conflicts"},
		{"a resolve refused by the viewer write gate", []string{"resolve", "--all", "--keep", "git", "--yes"},
			http.MethodPost, http.StatusForbidden, `{"detail":"permission_denied"}`,
			exitForbidden, "(clusters.write) to resolve its GitOps conflicts"},
		{"a cluster outside the organisation", []string{"list"},
			http.MethodGet, http.StatusNotFound, `{"detail":"Cluster not found"}`,
			exitNotFound, "not found in this organisation"},
		{"a platform without the routes", []string{"list"},
			http.MethodGet, http.StatusNotFound, `{"detail":"Not Found"}`,
			exitError, "does not offer GitOps conflicts to the CLI yet"},
		{"a 404 that says nothing", []string{"list"},
			http.MethodGet, http.StatusNotFound, ``,
			exitError, "without saying why"},
		{"a conflict resolved in between", []string{"resolve", "stack:db", "--keep", "git", "--yes"},
			http.MethodPost, http.StatusNotFound, `{"detail":"No open GitOps conflict for the given resource"}`,
			exitNotFound, "no open GitOps conflict 'stack:db'"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGitopsConflictsServer(t, gitopsConflictFixtures())
			recorder.refuse(testCase.method, testCase.status, testCase.body)

			_, _, runError := runGitopsConflicts(t, "", testCase.arguments...)
			if code := exitCodeFor(runError); code != testCase.wantExit {
				t.Fatalf("exit code = %d (%v), want %d", code, runError, testCase.wantExit)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Errorf("error = %v, want it to mention %q", runError, testCase.wantText)
			}
		})
	}
}
