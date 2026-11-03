package cmd

// `ankra cluster gitops preserved list|show|resolve` against a scripted
// platform: the real client talks to an httptest server that answers the
// preserved-path routes the way the platform does (digests compared, a
// different decision refused, SOPS-encrypted content sealed against
// "cluster"), so the body each verb sends is pinned on the request itself,
// and a refused, declined or locally answered resolve is proven to send no
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

const (
	preservedIndexPath   = "clusters/test/test.yaml"
	preservedSealedPath  = "clusters/test/stacks/sealed.yaml"
	preservedDecidedPath = "clusters/test/decided.yaml"
)

type gitopsPreservedRequest struct {
	method        string
	path          string
	authorization string
	body          map[string]any
}

// gitopsPreservedServer is a platform holding one cluster's preserved paths.
type gitopsPreservedServer struct {
	mutex      sync.Mutex
	entries    []client.GitopsPreservedPath
	diffValues string
	requests   []gitopsPreservedRequest
	// refusalStatus, when set, answers every preserved-path request whose
	// method is refusalMethod (any method when empty) with it and refusalBody.
	refusalStatus int
	refusalMethod string
	refusalBody   string
	// changeAfterList moves every digest once a listing has been served, as
	// a push landing between the CLI's read and its decision would.
	changeAfterList bool
	// undecidedStatusCount, when not nil, is what the GitOps status route
	// reports as preserved_path_undecided_count.
	undecidedStatusCount *int
}

func preservedStringPointer(value string) *string { return &value }

// gitopsPreservedFixtures are three files: a withheld cluster index with a
// structural difference, a SOPS-encrypted file, and a file already decided.
func gitopsPreservedFixtures() []client.GitopsPreservedPath {
	detectedAt := "2026-11-03T20:22:19.887235Z"
	return []client.GitopsPreservedPath{
		{
			Path: preservedIndexPath, HeadContentHash: "head-digest-1", RenderContentHash: "render-digest-1",
			ContentRedacted: true, ContentRedactedReason: preservedStringPointer("sensitive_value"),
			HeadCommitSHA: preservedStringPointer("abc123"), DetectedAt: &detectedAt, LastSeenAt: &detectedAt,
			StructuralDiff: client.GitopsPreservedPathDiff{Status: "computed", Total: 7, Changes: []client.GitopsPreservedPathChange{
				{Path: "spec.stacks[name=security].addons[name=trivy].chart_version", Change: "changed",
					GitValue: preservedStringPointer("0.29.0"), AnkraValue: preservedStringPointer("0.30.0")},
				{Path: "spec.stacks[name=demo].name", Change: "added", ValueMasked: true},
				{Path: "spec.stacks[name=demo].addons[name=seaweedfs].chart_name", Change: "added",
					AnkraValue: preservedStringPointer("seaweedfs")},
				{Path: "spec.stacks[name=demo].addons[name=seaweedfs].name", Change: "added", ValueMasked: true},
				{Path: "spec.stacks[name=dns].manifests[name=external-dns].encrypted_paths[0]", Change: "added",
					AnkraValue: preservedStringPointer("data.token")},
				{Path: "spec.stacks[name=legacy].name", Change: "removed", ValueMasked: true},
				{Path: "spec.stacks[name=legacy].addons[name=old].chart_version", Change: "removed",
					GitValue: preservedStringPointer("1.0.0")},
			}},
		},
		{
			Path: preservedSealedPath, HeadContentHash: "head-digest-2", RenderContentHash: "render-digest-2",
			ContentRedacted: true, ContentRedactedReason: preservedStringPointer("sops_encrypted"), DetectedAt: &detectedAt,
			StructuralDiff: client.GitopsPreservedPathDiff{Status: "computed", Total: 1,
				ValuesWithheldReason: preservedStringPointer("sops_encrypted"),
				Changes:              []client.GitopsPreservedPathChange{{Path: "data.value", Change: "changed", ValueMasked: true}}},
		},
		{
			Path: preservedDecidedPath, HeadContentHash: "head-digest-3", RenderContentHash: "render-digest-3",
			DetectedAt: &detectedAt, ResolutionChoice: preservedStringPointer("git"),
			ResolvedAt:     preservedStringPointer("2026-11-03T21:00:00Z"),
			StructuralDiff: client.GitopsPreservedPathDiff{Status: "formatting_only", Changes: []client.GitopsPreservedPathChange{}},
		},
	}
}

func newGitopsPreservedServer(t *testing.T, entries []client.GitopsPreservedPath) *gitopsPreservedServer {
	t.Helper()
	recorder := &gitopsPreservedServer{entries: entries, diffValues: client.GitopsPreservedDiffValuesRevealed}
	preservedPath := "/api/v1/org/clusters/" + testClusterID + "/gitops/preserved-paths"
	statusPath := "/api/v1/clusters/" + testClusterID + "/gitops/status"
	conflictsPath := "/api/v1/org/clusters/" + testClusterID + "/gitops/conflicts"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		switch {
		case request.Method == http.MethodGet && request.URL.Path == statusPath && recorder.undecidedStatusCount != nil:
			_ = json.NewEncoder(writer).Encode(client.ClusterGitopsStatus{
				PreservedPathUndecidedCount: recorder.undecidedStatusCount})
			return
		case request.Method == http.MethodGet && request.URL.Path == conflictsPath:
			_, _ = writer.Write([]byte(`{"conflicts":[],"total":0}`))
			return
		case !strings.HasPrefix(request.URL.Path, preservedPath):
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		recorded := gitopsPreservedRequest{method: request.Method, path: request.URL.Path,
			authorization: request.Header.Get("Authorization")}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 && (recorder.refusalMethod == "" || recorder.refusalMethod == request.Method) {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == preservedPath:
			_ = json.NewEncoder(writer).Encode(client.GitopsPreservedPathList{PreservedPaths: recorder.entries,
				Total: len(recorder.entries), StructuralDiffValues: recorder.diffValues})
			if recorder.changeAfterList {
				for index := range recorder.entries {
					recorder.entries[index].HeadContentHash += "-moved"
				}
			}
		case request.Method == http.MethodPost && request.URL.Path == preservedPath+"/resolve":
			recorder.resolve(writer, recorded.body)
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

// resolve answers the decision the way the platform's resolve route does.
func (recorder *gitopsPreservedServer) resolve(writer http.ResponseWriter, body map[string]any) {
	path, _ := body["path"].(string)
	resolution, _ := body["resolution"].(string)
	headDigest, _ := body["head_content_hash"].(string)
	renderDigest, _ := body["render_content_hash"].(string)
	refuse := func(status int, detail string) {
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(map[string]string{"detail": detail})
	}
	for index := range recorder.entries {
		entry := &recorder.entries[index]
		if entry.Path != path {
			continue
		}
		switch {
		case resolution == client.GitopsPreservedResolutionNone:
			entry.ResolutionChoice = nil
		case entry.HeadContentHash != headDigest || entry.RenderContentHash != renderDigest:
			refuse(http.StatusConflict, "The file changed in Git or in Ankra since it was listed; list the preserved "+
				"paths again and decide over the current content")
			return
		case resolution == client.GitopsPreservedResolutionCluster && entry.ContentRedactedReason != nil &&
			*entry.ContentRedactedReason == client.GitopsPreservedRedactedSopsEncrypted:
			refuse(http.StatusConflict, "This file holds SOPS-encrypted content, so Ankra will not write its own "+
				"version over it: that would replace ciphertext in Git with plaintext. Take Git's version, or change the file in Git")
			return
		case entry.ResolutionChoice != nil && *entry.ResolutionChoice != resolution:
			refuse(http.StatusConflict, "This preserved path already has a different decision; it runs on the next "+
				"sync. Withdraw it with resolution none to decide again")
			return
		default:
			entry.ResolutionChoice = &resolution
		}
		_ = json.NewEncoder(writer).Encode(client.GitopsPreservedPathResolution{Path: path, Resolution: resolution,
			SyncTriggered: resolution != client.GitopsPreservedResolutionNone, Message: "Recorded " + resolution + " on " + path})
		return
	}
	refuse(http.StatusNotFound, "Preserved path not found")
}

func (recorder *gitopsPreservedServer) seen() []gitopsPreservedRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]gitopsPreservedRequest{}, recorder.requests...)
}

func (recorder *gitopsPreservedServer) writes() []gitopsPreservedRequest {
	var writes []gitopsPreservedRequest
	for _, request := range recorder.seen() {
		if request.method != http.MethodGet {
			writes = append(writes, request)
		}
	}
	return writes
}

func (recorder *gitopsPreservedServer) refuse(method string, status int, body string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.refusalMethod, recorder.refusalStatus, recorder.refusalBody = method, status, body
}

func gitopsPreservedCommands() []*cobra.Command {
	return []*cobra.Command{clusterGitopsPreservedCmd, clusterGitopsPreservedListCmd, clusterGitopsPreservedShowCmd,
		clusterGitopsPreservedResolveCmd}
}

// runGitopsPreserved executes the command with input on stdin and returns
// stdout and stderr apart, so structured output can be checked for being
// clean.
func runGitopsPreserved(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "gitops", "preserved"}, arguments...))
	resetTreeFlags(t, gitopsPreservedCommands()...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, gitopsPreservedCommands()...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestClusterGitopsPreservedListPrintsDecisionsContentAndDifferences(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

	stdout, _, runError := runGitopsPreserved(t, "", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/org/clusters/"+testClusterID+"/gitops/preserved-paths" {
		t.Fatalf("expected one GET of the selected cluster's preserved paths, got %+v", requests)
	}
	if requests[0].authorization != "Bearer test-token" {
		t.Fatalf("the list must use the bearer route, got Authorization %q", requests[0].authorization)
	}
	assertContainsAll(t, stdout, []string{
		"Preserved GitOps paths on cluster 'test-cluster': 3, 2 of those listed awaiting a decision.",
		preservedIndexPath, "withheld: sensitive value", "7: 4 added, 2 removed, 1 changed",
		preservedSealedPath, "withheld: sops encrypted",
		preservedDecidedPath, "keep Git (next sync)", "formatting only", "listed",
		"Nothing is paused.",
		"ankra cluster gitops preserved show <path>",
		"ankra cluster gitops preserved resolve <path> --keep git|ankra",
	})
}

func TestClusterGitopsPreservedListWithNothingPreserved(t *testing.T) {
	newGitopsPreservedServer(t, []client.GitopsPreservedPath{})

	stdout, _, runError := runGitopsPreserved(t, "", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if !strings.HasPrefix(stdout, "No preserved GitOps paths on cluster 'test-cluster'") {
		t.Fatalf("output = %q", stdout)
	}
}

func TestClusterGitopsPreservedListJSONIsTheResponseBody(t *testing.T) {
	newGitopsPreservedServer(t, gitopsPreservedFixtures())

	stdout, stderr, runError := runGitopsPreserved(t, "", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("list -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the JSON answer: %v\n%s", decodeError, stdout)
	}
	paths, _ := decoded["preserved_paths"].([]any)
	if decoded["total"] != float64(3) || decoded["structural_diff_values"] != "revealed" || len(paths) != 3 {
		t.Fatalf("decoded = %v", decoded)
	}
	first, _ := paths[0].(map[string]any)
	if first["head_content_hash"] != "head-digest-1" || first["structural_diff"] == nil {
		t.Fatalf("first entry = %v", first)
	}
	if strings.Contains(stdout+stderr, "Nothing is paused") {
		t.Fatalf("-o json must not print the human hints")
	}
}

func TestClusterGitopsPreservedShowPrintsEveryDifferenceWithMaskedValues(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
	recorder.diffValues = client.GitopsPreservedDiffValuesPermissionRequired

	stdout, _, runError := runGitopsPreserved(t, "", "show", preservedIndexPath)
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{
		"Preserved path " + preservedIndexPath + " on cluster 'test-cluster'",
		"Git commit: abc123", "Decision:   awaiting a decision",
		"withheld, because it holds a value under a key that reads as a credential",
		"Differences, read from Git's version to Ankra's: 7",
		"spec.stacks[name=security].addons[name=trivy].chart_version", "0.29.0", "0.30.0",
		"spec.stacks[name=demo].name", "(masked)",
		"needs permission to reveal secrets on the cluster (kubernetes.secrets_reveal)",
		"ankra cluster gitops preserved resolve " + preservedIndexPath + " --keep git|ankra",
	})
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("show wrote: %+v", writes)
	}
}

func TestClusterGitopsPreservedShowAnUnlistedPathIsNotFound(t *testing.T) {
	newGitopsPreservedServer(t, gitopsPreservedFixtures())

	_, _, runError := runGitopsPreserved(t, "", "show", "clusters/test/ghost.yaml")
	if code := exitCodeFor(runError); code != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d (not found)", code, runError, exitNotFound)
	}
	if !strings.Contains(runError.Error(), "no preserved path 'clusters/test/ghost.yaml'") {
		t.Errorf("error = %v", runError)
	}
}

// --keep maps the CLI's words onto the platform's: ankra and cluster are the
// same side, and nothing else reaches the platform as a decision.
func TestGitopsPreservedKeepResolution(t *testing.T) {
	cases := []struct {
		keep  string
		want  string
		known bool
	}{
		{"git", client.GitopsPreservedResolutionGit, true},
		{"ankra", client.GitopsPreservedResolutionCluster, true},
		{"cluster", client.GitopsPreservedResolutionCluster, true},
		{" Ankra ", client.GitopsPreservedResolutionCluster, true},
		{"GIT", client.GitopsPreservedResolutionGit, true},
		{"", "", false},
		{"none", "", false},
		{"both", "", false},
	}
	for _, testCase := range cases {
		got, known := gitopsPreservedKeepResolution(testCase.keep)
		if got != testCase.want || known != testCase.known {
			t.Errorf("--keep %q = (%q, %v), want (%q, %v)", testCase.keep, got, known, testCase.want, testCase.known)
		}
	}
}

func TestClusterGitopsPreservedResolveRefusesAnIncompleteRequest(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		wantText  string
	}{
		{"no --keep", []string{"resolve", preservedIndexPath}, "--keep is required"},
		{"an unknown side", []string{"resolve", preservedIndexPath, "--keep", "both"}, "--keep is required"},
		{"the platform's none", []string{"resolve", preservedIndexPath, "--keep", "none"}, "--keep is required"},
		{"no path", []string{"resolve", "--keep", "git"}, "name the preserved path"},
		{"--keep and --withdraw", []string{"resolve", preservedIndexPath, "--keep", "git", "--withdraw"}, "not both"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

			_, _, runError := runGitopsPreserved(t, "", append(testCase.arguments, "--yes")...)
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

// The decision carries the digests the listing returned for that path, never
// typed ones, and each --keep word sends the platform's resolution.
func TestClusterGitopsPreservedResolveSendsTheListedDigests(t *testing.T) {
	cases := []struct {
		keep           string
		wantResolution string
	}{
		{"ankra", "cluster"},
		{"cluster", "cluster"},
		{"git", "git"},
	}
	for _, testCase := range cases {
		t.Run(testCase.keep, func(t *testing.T) {
			recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

			stdout, _, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", testCase.keep, "--yes")
			if runError != nil {
				t.Fatalf("resolve failed: %v", runError)
			}
			writes := recorder.writes()
			if len(writes) != 1 || writes[0].path != "/api/v1/org/clusters/"+testClusterID+"/gitops/preserved-paths/resolve" {
				t.Fatalf("expected one resolve, got %+v", writes)
			}
			want := map[string]any{"path": preservedIndexPath, "resolution": testCase.wantResolution,
				"head_content_hash": "head-digest-1", "render_content_hash": "render-digest-1"}
			for key, value := range want {
				if writes[0].body[key] != value {
					t.Errorf("body[%s] = %v, want %v (body %v)", key, writes[0].body[key], value, writes[0].body)
				}
			}
			if len(writes[0].body) != len(want) {
				t.Errorf("body = %v, want exactly %v", writes[0].body, want)
			}
			if writes[0].authorization != "Bearer test-token" {
				t.Fatalf("the resolve must use the bearer route, got Authorization %q", writes[0].authorization)
			}
			assertContainsAll(t, stdout, []string{"Recorded " + testCase.wantResolution, "A sync was triggered"})
		})
	}
}

func TestClusterGitopsPreservedResolveWhenTheFileChangedAfterTheListing(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
	recorder.changeAfterList = true

	_, _, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", "ankra", "--yes")
	if code := exitCodeFor(runError); code != exitError {
		t.Fatalf("exit code = %d (%v), want %d", code, runError, exitError)
	}
	if !strings.Contains(runError.Error(), "Run the same command again") {
		t.Errorf("the refusal must say how to decide over the current content: %v", runError)
	}
	if writes := recorder.writes(); len(writes) != 1 || writes[0].body["head_content_hash"] != "head-digest-1" {
		t.Fatalf("expected one write carrying the digest that was listed, got %+v", writes)
	}
}

func TestClusterGitopsPreservedResolveWithoutYesIsRefusedWithoutATerminal(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

	_, stderr, runError := runGitopsPreserved(t, "y\n", "resolve", preservedIndexPath, "--keep", "git")
	if code := exitCodeFor(runError); code != exitUsage {
		t.Fatalf("exit code = %d (%v), want %d (usage)", code, runError, exitUsage)
	}
	if !strings.Contains(runError.Error(), "discards Ankra's version") ||
		!strings.Contains(runError.Error(), "re-run with --yes to confirm") {
		t.Errorf("the refusal must say what is discarded and how to confirm: %v", runError)
	}
	assertContainsAll(t, stderr, []string{"Only in Ankra's version, so REMOVED from the cluster:"})
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("a refused resolve still wrote: %+v", writes)
	}
}

func TestClusterGitopsPreservedResolveAsksOnATerminal(t *testing.T) {
	t.Run("yes decides", func(t *testing.T) {
		recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
		answerPromptInteractively(t)

		_, stderr, runError := runGitopsPreserved(t, "y\n", "resolve", preservedIndexPath, "--keep", "ankra")
		if runError != nil {
			t.Fatalf("a confirmed resolve failed: %v", runError)
		}
		assertContainsAll(t, stderr, []string{"Keep Ankra's version of " + preservedIndexPath + "? [y/N]"})
		if writes := recorder.writes(); len(writes) != 1 {
			t.Fatalf("a confirmed resolve must write once, got %+v", writes)
		}
	})

	t.Run("anything else cancels", func(t *testing.T) {
		recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
		answerPromptInteractively(t)

		stdout, _, runError := runGitopsPreserved(t, "n\n", "resolve", preservedIndexPath, "--keep", "git")
		if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
			t.Fatalf("a declined resolve must return errCancelled (exit 4), got %v", runError)
		}
		if writes := recorder.writes(); len(writes) != 0 {
			t.Fatalf("a declined resolve wrote: %+v", writes)
		}
		if strings.Contains(stdout, "Recorded") {
			t.Fatalf("a declined resolve printed success:\n%s", stdout)
		}
	})
}

func TestClusterGitopsPreservedResolveJSONIsTheResponseBody(t *testing.T) {
	newGitopsPreservedServer(t, gitopsPreservedFixtures())

	stdout, stderr, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", "ankra", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("resolve -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the JSON answer: %v\n%s", decodeError, stdout)
	}
	if decoded["path"] != preservedIndexPath || decoded["resolution"] != "cluster" || decoded["sync_triggered"] != true {
		t.Fatalf("decoded = %v", decoded)
	}
	// The preview went to stderr, not into the JSON.
	assertContainsAll(t, stderr, []string{"Deciding " + preservedIndexPath})
}

// Decisions the listing already shows cannot land are answered locally,
// without a write.
func TestClusterGitopsPreservedResolveAnswersLocallyWhatTheListingShows(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		wantError string
		wantOut   string
	}{
		{"keeping Ankra's over SOPS-encrypted content", []string{"resolve", preservedSealedPath, "--keep", "ankra"},
			"holds SOPS-encrypted content", ""},
		{"the other side of a recorded decision", []string{"resolve", preservedDecidedPath, "--keep", "ankra"},
			"--withdraw", ""},
		{"the decision already recorded", []string{"resolve", preservedDecidedPath, "--keep", "git"},
			"", "already has this decision (keep Git's version)"},
		{"withdrawing where nothing is decided", []string{"resolve", preservedIndexPath, "--withdraw"},
			"", "has no decision to withdraw"},
		{"a path that is not preserved", []string{"resolve", "clusters/test/ghost.yaml", "--keep", "git"},
			"no preserved path 'clusters/test/ghost.yaml'", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

			stdout, _, runError := runGitopsPreserved(t, "", append(testCase.arguments, "--yes")...)
			switch {
			case testCase.wantError != "":
				if runError == nil || !strings.Contains(runError.Error(), testCase.wantError) {
					t.Fatalf("error = %v, want it to mention %q", runError, testCase.wantError)
				}
			case runError != nil:
				t.Fatalf("unexpected error: %v", runError)
			default:
				assertContainsAll(t, stdout, []string{testCase.wantOut})
			}
			if writes := recorder.writes(); len(writes) != 0 {
				t.Fatalf("a decision the listing answers still wrote: %+v", writes)
			}
		})
	}
}

// --withdraw sends resolution none with the listed digests, and needs no
// confirmation: it changes nothing in Git or on the cluster.
func TestClusterGitopsPreservedResolveWithdrawsADecision(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())

	_, stderr, runError := runGitopsPreserved(t, "", "resolve", preservedDecidedPath, "--withdraw")
	if runError != nil {
		t.Fatalf("withdraw failed: %v", runError)
	}
	writes := recorder.writes()
	if len(writes) != 1 || writes[0].body["resolution"] != "none" || writes[0].body["path"] != preservedDecidedPath ||
		writes[0].body["head_content_hash"] != "head-digest-3" || writes[0].body["render_content_hash"] != "render-digest-3" {
		t.Fatalf("expected one withdrawal of %s with its digests, got %+v", preservedDecidedPath, writes)
	}
	assertContainsAll(t, stderr, []string{"Withdrawing the decision to keep Git's version", "Nothing changes in Git or on the cluster"})
}

func TestClusterGitopsPreservedRefusals(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		method    string
		status    int
		body      string
		wantExit  int
		wantText  string
	}{
		{"a decision without clusters.write", []string{"resolve", preservedIndexPath, "--keep", "git", "--yes"},
			http.MethodPost, http.StatusForbidden,
			`{"detail":"permission_denied","permission":"clusters.write","scope_type":"cluster"}`,
			exitForbidden, "(clusters.write) to decide its preserved GitOps paths"},
		{"a path settled in between", []string{"resolve", preservedIndexPath, "--keep", "git", "--yes"},
			http.MethodPost, http.StatusNotFound, `{"detail":"Preserved path not found"}`,
			exitNotFound, "a push may have settled it"},
		{"digests that moved", []string{"resolve", preservedIndexPath, "--keep", "git", "--yes"},
			http.MethodPost, http.StatusConflict,
			`{"detail":"The file changed in Git or in Ankra since it was listed; list the preserved paths again"}`,
			exitError, "Run the same command again"},
		{"a decision recorded in between", []string{"resolve", preservedIndexPath, "--keep", "git", "--yes"},
			http.MethodPost, http.StatusConflict,
			`{"detail":"This preserved path already has a different decision; it runs on the next sync."}`,
			exitError, "resolve " + preservedIndexPath + " --withdraw"},
		{"content sealed in between", []string{"resolve", preservedIndexPath, "--keep", "ankra", "--yes"},
			http.MethodPost, http.StatusConflict,
			`{"detail":"This file holds SOPS-encrypted content, so Ankra will not write its own version over it"}`,
			exitError, "Keep Git's version with --keep git, or change the file in Git"},
		{"a cluster outside the organisation", []string{"list"},
			http.MethodGet, http.StatusNotFound, `{"detail":"Cluster not found"}`,
			exitNotFound, "not found in this organisation"},
		{"a platform without the routes", []string{"list"},
			http.MethodGet, http.StatusNotFound, `{"detail":"Not Found"}`,
			exitError, "does not offer preserved GitOps paths to the CLI yet"},
		{"a 404 that says nothing", []string{"list"},
			http.MethodGet, http.StatusNotFound, ``,
			exitError, "without saying why"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
			recorder.refuse(testCase.method, testCase.status, testCase.body)

			_, _, runError := runGitopsPreserved(t, "", testCase.arguments...)
			if code := exitCodeFor(runError); code != testCase.wantExit {
				t.Fatalf("exit code = %d (%v), want %d", code, runError, testCase.wantExit)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Errorf("error = %v, want it to mention %q", runError, testCase.wantText)
			}
		})
	}
}

func preservedFixtureListing() *client.GitopsPreservedPathList {
	entries := gitopsPreservedFixtures()
	return &client.GitopsPreservedPathList{PreservedPaths: entries, Total: len(entries),
		StructuralDiffValues: client.GitopsPreservedDiffValuesRevealed}
}

// lineIndex is where a line starting (after indentation) with prefix sits in
// text, or -1.
func lineIndex(text string, prefix string) int {
	for index, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return index
		}
	}
	return -1
}

// Keeping Git's version names, most destructive first, what only Ankra
// declares (removed from the cluster, whole stacks folded into one line),
// what only Git declares, and the values that go back, then warns about the
// encrypted_paths entry Git does not declare and the import waiting on the
// other undecided file.
func TestGitopsPreservedPreviewForKeepingGit(t *testing.T) {
	listing := preservedFixtureListing()
	preview := gitopsPreservedPreview("test-cluster", listing, listing.PreservedPaths[0], client.GitopsPreservedResolutionGit)

	assertContainsAll(t, preview, []string{
		"Deciding " + preservedIndexPath + " on cluster 'test-cluster': keep Git's version.",
		"Ankra imports the file as Git holds it (commit abc123), and the cluster changes to match it.",
		"Where the two versions differ: 7 differences.",
		"  Only in Ankra's version, so REMOVED from the cluster:\n    - the whole stack demo (3 keys)\n" +
			"    - spec.stacks[name=dns].manifests[name=external-dns].encrypted_paths[0]: data.token\n",
		"  Only in Git's version, so ADDED to the cluster:\n    - the whole stack legacy (2 keys)\n",
		"  Values that go back to Git's (Ankra's -> Git's):\n" +
			"    - spec.stacks[name=security].addons[name=trivy].chart_version: 0.30.0 -> 0.29.0\n",
		"Warning: Git's version does not declare 1 encrypted_paths entry that Ankra's does",
		"The import waits until every preserved path on this cluster has a decision: 1 file besides this one still awaits one.",
	})
	if removed, added := lineIndex(preview, "Only in Ankra's version"), lineIndex(preview, "Only in Git's version"); removed < 0 || added < removed {
		t.Errorf("what keeping Git removes must come first:\n%s", preview)
	}
	if strings.Contains(preview, "seaweedfs") {
		t.Errorf("the add-on of a stack removed as a whole must be folded into the stack's line:\n%s", preview)
	}
}

// Keeping Ankra's version words the same difference the other way round and
// carries neither of the keep-Git warnings.
func TestGitopsPreservedPreviewForKeepingAnkra(t *testing.T) {
	listing := preservedFixtureListing()
	preview := gitopsPreservedPreview("test-cluster", listing, listing.PreservedPaths[0], client.GitopsPreservedResolutionCluster)

	assertContainsAll(t, preview, []string{
		"keep Ankra's version.",
		"Ankra writes its own version of the file into Git, over the edit made there",
		"  Only in Git's version, so REMOVED from the file in Git:\n    - the whole stack legacy (2 keys)\n",
		"  Only in Ankra's version, so ADDED to the file in Git:\n    - the whole stack demo (3 keys)\n",
		"  Values in Git replaced by Ankra's (Git's -> Ankra's):\n" +
			"    - spec.stacks[name=security].addons[name=trivy].chart_version: 0.29.0 -> 0.30.0\n",
	})
	if removed, added := lineIndex(preview, "Only in Git's version"), lineIndex(preview, "Only in Ankra's version"); removed < 0 || added < removed {
		t.Errorf("what keeping Ankra's removes from Git must come first:\n%s", preview)
	}
	for _, absent := range []string{"Warning:", "The import waits"} {
		if strings.Contains(preview, absent) {
			t.Errorf("keeping Ankra's version must not print %q:\n%s", absent, preview)
		}
	}
}

// A capped difference says how many differences the preview cannot show,
// and masked values say why.
func TestGitopsPreservedPreviewSaysWhatItCannotShow(t *testing.T) {
	listing := preservedFixtureListing()
	listing.StructuralDiffValues = client.GitopsPreservedDiffValuesPermissionRequired
	entry := listing.PreservedPaths[0]
	entry.StructuralDiff.Total = 111
	entry.StructuralDiff.Truncated = true
	preview := gitopsPreservedPreview("test-cluster", listing, entry, client.GitopsPreservedResolutionGit)
	assertContainsAll(t, preview, []string{
		"Where the two versions differ: 111 differences. The platform lists the first 7; the other 104 are not shown here",
		"Values are masked: seeing them needs permission to reveal secrets on the cluster (kubernetes.secrets_reveal).",
	})

	notRecorded := listing.PreservedPaths[0]
	notRecorded.StructuralDiff = client.GitopsPreservedPathDiff{Status: client.GitopsPreservedDiffNotRecorded}
	preview = gitopsPreservedPreview("test-cluster", listing, notRecorded, client.GitopsPreservedResolutionCluster)
	assertContainsAll(t, preview, []string{"The platform has no key-by-key difference for this file",
		"so this cannot say what the choice discards"})
}

func TestGitopsPreservedEntityLabel(t *testing.T) {
	cases := map[string]string{
		"spec.stacks[name=web]":                        "stack web",
		"spec.stacks[name=web].addons[name=nginx]":     "addon nginx in stack web",
		"spec.stacks[name=web].manifests[name=ns-web]": "manifest ns-web in stack web",
		`spec.stacks[name="a.b"]`:                      `spec.stacks[name="a.b"]`,
		"Deployment/web:spec.containers[name=app]":     "Deployment/web:spec.containers[name=app]",
	}
	for entity, want := range cases {
		if got := gitopsPreservedEntityLabel(entity); got != want {
			t.Errorf("label(%q) = %q, want %q", entity, got, want)
		}
	}
}

// With no merge conflicts, `conflicts list` points at undecided preserved
// paths: on ankra-ci-sto1 they were the whole reason Git and the cluster
// disagreed, and the conflict verbs cannot decide them.
func TestClusterGitopsConflictsListPointsAtUndecidedPreservedPaths(t *testing.T) {
	recorder := newGitopsPreservedServer(t, gitopsPreservedFixtures())
	undecided := 1
	recorder.undecidedStatusCount = &undecided

	stdout, _, runError := runGitopsConflicts(t, "", "list")
	if runError != nil {
		t.Fatalf("conflicts list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{
		"No open GitOps conflicts on cluster 'test-cluster'.",
		"1 preserved path awaits a decision: a file edited in Git that Ankra keeps instead of overwriting.",
		"ankra cluster gitops preserved list",
	})

	none := 0
	recorder.undecidedStatusCount = &none
	stdout, _, runError = runGitopsConflicts(t, "", "list")
	if runError != nil {
		t.Fatalf("conflicts list failed: %v", runError)
	}
	if strings.Contains(stdout, "preserved") {
		t.Fatalf("no undecided preserved path, yet the listing mentions them:\n%s", stdout)
	}
}

func TestClusterGitopsConflictsResolveAFilePathPointsAtPreservedPaths(t *testing.T) {
	newGitopsConflictsServer(t, gitopsConflictFixtures())

	_, _, runError := runGitopsConflicts(t, "", "resolve", preservedIndexPath, "--keep", "cluster", "--yes")
	if code := exitCodeFor(runError); code != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d", code, runError, exitNotFound)
	}
	if !strings.Contains(runError.Error(), "ankra cluster gitops preserved resolve "+preservedIndexPath) {
		t.Errorf("a file path must point at the preserved-path verbs: %v", runError)
	}
}
