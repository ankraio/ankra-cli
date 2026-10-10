package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"
)

// fakeWorkspaceAPI is the platform's Workspaces API, scripted per test: one
// workspace (ws-1), a vault for presigned transfers and a queue of stream
// connections.
type fakeWorkspaceAPI struct {
	t      *testing.T
	server *httptest.Server
	client *client.Client

	mutex           sync.Mutex
	upStatus        string
	getStatuses     []string
	upFailures      int
	bundleRequests  []map[string]any
	uploads         map[string][]byte
	runRequests     []client.WorkspaceRunRequest
	startFailStatus int
	startFailBody   string
	streams         []func(http.ResponseWriter, *http.Request)
	streamQueries   []string
	cancels         int
	exportRequests  []client.WorkspaceRunExportRequest
	exportBodies    map[string][]byte
	runStates       map[string]string
	repositories    []client.PipelineRepository
	listStatus      int
	requests        int
	deleted         []string
	upRequests      []client.WorkspaceUpRequest
	// profilesStatus and profilesBody answer the repository's workspace
	// profiles read; profileReads counts it.
	profilesStatus int
	profilesBody   string
	profileReads   int
}

func newFakeWorkspaceAPI(t *testing.T) *fakeWorkspaceAPI {
	t.Helper()
	api := &fakeWorkspaceAPI{
		t:            t,
		upStatus:     client.WorkspaceStatusReady,
		uploads:      map[string][]byte{},
		exportBodies: map[string][]byte{},
		runStates:    map[string]string{},
		listStatus:   http.StatusOK,
		repositories: []client.PipelineRepository{{ID: "repo-1", Provider: "github", Owner: "acme", Name: "shop"}},
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.server.Close)
	api.client = client.New("test-token", api.server.URL)
	return api
}

func (api *fakeWorkspaceAPI) workspaceJSON(status string) string {
	return fmt.Sprintf(`{"id":"ws-1","organisation_id":"org-1","user_id":"user-1","repository_id":"repo-1",`+
		`"kind":"default","status":%q,"idle_ttl_hours":24,"namespace":"ws-ns","pod_name":"workspace-0",`+
		`"image":"golang:1.26","image_source":"pipeline_defaults","last_error":%s}`,
		status, map[bool]string{true: `"preflight failed: no bash"`, false: "null"}[status == client.WorkspaceStatusFailed])
}

func (api *fakeWorkspaceAPI) serve(writer http.ResponseWriter, request *http.Request) {
	api.mutex.Lock()
	api.requests++
	api.mutex.Unlock()
	path := request.URL.Path
	route := request.Method + " " + path
	writer.Header().Set("Content-Type", "application/json")
	switch {
	case route == "POST /api/v1/org/workspaces":
		var upRequest client.WorkspaceUpRequest
		_ = json.NewDecoder(request.Body).Decode(&upRequest)
		api.mutex.Lock()
		api.upRequests = append(api.upRequests, upRequest)
		failing := api.upFailures > 0
		if failing {
			api.upFailures--
		}
		status := api.upStatus
		api.mutex.Unlock()
		if failing {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"error_code":"AGENT_TIMEOUT","detail":"The agent did not answer.","retry_after":10}`)
			return
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(writer, api.workspaceJSON(status))
	case route == "GET /api/v1/org/workspaces":
		if api.listStatus != http.StatusOK {
			writer.WriteHeader(api.listStatus)
			_, _ = io.WriteString(writer, `{"detail":"permission_denied","permission":"workspaces.use"}`)
			return
		}
		_, _ = io.WriteString(writer, `{"items":[`+api.workspaceJSON(client.WorkspaceStatusReady)+`],"is_capped":false}`)
	case route == "GET /api/v1/org/workspaces/ws-1":
		api.mutex.Lock()
		status := client.WorkspaceStatusReady
		if len(api.getStatuses) > 0 {
			status, api.getStatuses = api.getStatuses[0], api.getStatuses[1:]
		}
		api.mutex.Unlock()
		_, _ = io.WriteString(writer, api.workspaceJSON(status))
	case route == "DELETE /api/v1/org/workspaces/ws-1":
		api.mutex.Lock()
		api.deleted = append(api.deleted, "ws-1")
		api.mutex.Unlock()
		_, _ = io.WriteString(writer, api.workspaceJSON(client.WorkspaceStatusDestroyed))
	case route == "GET /api/v1/org/pipeline-repositories/repo-1/workspace-profiles":
		api.mutex.Lock()
		api.profileReads++
		status, body := api.profilesStatus, api.profilesBody
		api.mutex.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		if body == "" {
			body = `{"repository_id":"repo-1","profiles":{}}`
		}
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, body)
	case route == "GET /api/v1/org/pipelines/repositories":
		encoded, _ := json.Marshal(client.PipelineRepositoryList{Repositories: api.repositories})
		_, _ = writer.Write(encoded)
	case route == "POST /api/v1/org/workspaces/ws-1/bundles":
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		api.mutex.Lock()
		api.bundleRequests = append(api.bundleRequests, body)
		_, exists := api.uploads[fmt.Sprint(body["sha256"])]
		api.mutex.Unlock()
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"object_key": "workspaces/org-1/ws-1/bundles/" + fmt.Sprint(body["sha256"]) + ".bundle",
			"upload_url": api.server.URL + "/vault/bundles/" + fmt.Sprint(body["sha256"]),
			"expires_at": "2026-10-10T12:00:00Z",
			"exists":     exists,
		})
	case request.Method == http.MethodPut && strings.HasPrefix(path, "/vault/bundles/"):
		if request.Header.Get("Authorization") != "" {
			api.t.Error("the bundle upload carried the API token")
		}
		body, _ := io.ReadAll(request.Body)
		api.mutex.Lock()
		api.uploads[strings.TrimPrefix(path, "/vault/bundles/")] = body
		api.mutex.Unlock()
	case route == "POST /api/v1/org/workspaces/ws-1/runs":
		var body client.WorkspaceRunRequest
		_ = json.NewDecoder(request.Body).Decode(&body)
		api.mutex.Lock()
		api.runRequests = append(api.runRequests, body)
		runID := fmt.Sprintf("run-%d", len(api.runRequests))
		failStatus, failBody := api.startFailStatus, api.startFailBody
		api.mutex.Unlock()
		if failStatus != 0 {
			writer.WriteHeader(failStatus)
			_, _ = io.WriteString(writer, failBody)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(writer, `{"run_id":"`+runID+`","state":"accepted"}`)
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/stream"):
		api.mutex.Lock()
		api.streamQueries = append(api.streamQueries, request.URL.RawQuery+" last="+request.Header.Get("Last-Event-ID"))
		var stream func(http.ResponseWriter, *http.Request)
		if len(api.streams) > 0 {
			stream, api.streams = api.streams[0], api.streams[1:]
		}
		api.mutex.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		if stream == nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"error_code":"AGENT_TIMEOUT","detail":"no stream scripted"}`)
			return
		}
		stream(writer, request)
	case request.Method == http.MethodGet && strings.Contains(path, "/runs/"):
		runID := path[strings.LastIndex(path, "/")+1:]
		api.mutex.Lock()
		state := api.runStates[runID]
		api.mutex.Unlock()
		if state == "" {
			state = "running"
		}
		_, _ = io.WriteString(writer, `{"run_id":"`+runID+`","workspace_id":"ws-1","state":"`+state+`","exit_code":null}`)
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/cancel"):
		api.mutex.Lock()
		api.cancels++
		api.mutex.Unlock()
		_, _ = io.WriteString(writer, `{}`)
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/exports"):
		var body client.WorkspaceRunExportRequest
		_ = json.NewDecoder(request.Body).Decode(&body)
		api.mutex.Lock()
		api.exportRequests = append(api.exportRequests, body)
		payload, hasPayload := api.exportBodies[body.Kind]
		api.mutex.Unlock()
		if !hasPayload {
			_, _ = io.WriteString(writer, `{"empty":true}`)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"empty": false,
			"download_url": api.server.URL + "/vault/exports/" + body.Kind, "size_bytes": len(payload)})
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/vault/exports/"):
		api.mutex.Lock()
		payload := api.exportBodies[strings.TrimPrefix(path, "/vault/exports/")]
		api.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(payload)
	default:
		api.t.Errorf("unexpected request %s", route)
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (api *fakeWorkspaceAPI) addStreams(streams ...func(http.ResponseWriter, *http.Request)) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	api.streams = append(api.streams, streams...)
}

func streamOutput(stdout string, stderr string, exitCode *int, isStarted bool) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, _ *http.Request) {
		if stdout != "" {
			sseTestFrame(writer, "stdout", base64.StdEncoding.EncodeToString([]byte(stdout)))
		}
		if stderr != "" {
			sseTestFrame(writer, "stderr", base64.StdEncoding.EncodeToString([]byte(stderr)))
		}
		if exitCode != nil {
			sseTestFrame(writer, "exit", fmt.Sprintf(`{"code":%d,"started":%t}`, *exitCode, isStarted))
		}
	}
}

func sseTestFrame(writer io.Writer, event string, data string) {
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, data)
}

func exitCode(code int) *int { return &code }

// execTestEnv is one test's working copy, log and marker file.
type execTestEnv struct {
	repository execTestRepository
	api        *fakeWorkspaceAPI
	logPath    string
	notRunPath string
}

func newExecTestEnv(t *testing.T) *execTestEnv {
	t.Helper()
	home := withTempHome(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	api := newFakeWorkspaceAPI(t)
	setMockClient(t, api.client)
	t.Chdir(repository.top)
	environment := &execTestEnv{
		repository: repository,
		api:        api,
		logPath:    filepath.Join(home, "ankra-exec.log"),
		notRunPath: filepath.Join(home, "notrun"),
	}
	t.Setenv("ANKRA_EXEC_LOG", environment.logPath)
	t.Setenv("ANKRA_EXEC_NOTRUN_FILE", environment.notRunPath)
	t.Setenv("ANKRA_EXEC_RETRIES", "1")
	t.Setenv("ANKRA_EXEC_FORWARD_ENV", "")
	t.Setenv("ANKRA_EXEC_ORG", "")
	t.Setenv("ANKRA_ORG", "")
	originalSleep := execSleep
	execSleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	t.Cleanup(func() { execSleep = originalSleep })
	return environment
}

func runExecCommand(t *testing.T, ctx context.Context, args ...string) (string, string, int) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(append([]string{"exec"}, args...))
	resetTreeFlags(t, execCmd)
	t.Cleanup(func() {
		resetTreeFlags(t, execCmd)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		execCmd.SilenceErrors = false
	})
	// Cobra hands the root's context only to a subcommand that has none, and
	// an earlier run left one on execCmd.
	execCmd.SetContext(ctx)
	executeError := rootCmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), exitCodeFor(executeError)
}

func (environment *execTestEnv) notRunMarked() bool {
	_, statError := os.Stat(environment.notRunPath)
	return statError == nil
}

func (environment *execTestEnv) logLines(t *testing.T) []string {
	t.Helper()
	contents, readError := os.ReadFile(environment.logPath)
	if readError != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}

func TestExecPassesTheRemoteExitCodeThrough(t *testing.T) {
	environment := newExecTestEnv(t)
	t.Setenv("GOFLAGS", "-count=1")
	t.Setenv("PATH_NOT_FORWARDED", "x")
	environment.api.addStreams(streamOutput("ok\n", "warn\n", exitCode(3), true))

	stdout, stderr, code := runExecCommand(t, context.Background(), "go", "test", "-run", "X", "./...")
	if code != 3 {
		t.Fatalf("exit = %d, want the remote 3; stderr:\n%s", code, stderr)
	}
	if stdout != "ok\n" || !strings.Contains(stderr, "warn\n") {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
	if environment.notRunMarked() {
		t.Fatal("a run that ran created the not-run file")
	}
	if len(environment.api.runRequests) != 1 {
		t.Fatalf("runs started = %d", len(environment.api.runRequests))
	}
	request := environment.api.runRequests[0]
	head := gitTestOutput(t, environment.repository.top, "rev-parse", "HEAD")
	if strings.Join(request.Argv, " ") != "go test -run X ./..." || request.SnapshotSHA != head ||
		request.HeadSHA != head || request.BaseRef != "refs/remotes/origin/main" || request.BaseSHA != head ||
		request.CwdPrefix != "" || request.Apply {
		t.Fatalf("run request = %+v", request)
	}
	if request.Env["GOFLAGS"] != "-count=1" || len(request.Env) != 1 {
		t.Fatalf("env = %v, want only the allowlisted GOFLAGS", request.Env)
	}
	if !strings.HasSuffix(request.AgentID, "-"+execAgentID(environment.repository.top)[len(execAgentID(environment.repository.top))-12:]) {
		t.Fatalf("agent id = %q", request.AgentID)
	}
	if request.BundleObjectKey == "" || len(environment.api.uploads) != 1 {
		t.Fatalf("the first run sent no bundle: key %q, uploads %d", request.BundleObjectKey, len(environment.api.uploads))
	}
	lines := environment.logLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "\trepo=acme/shop\tpod=ws-1\trc=3\tmode=remote/api\tbundle_bytes=") ||
		!strings.HasSuffix(lines[0], "\tcmd=go test -run X ./...") {
		t.Fatalf("log = %q", lines)
	}

	// Nothing changed: the second run sends no bundle at all.
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code = runExecCommand(t, context.Background(), "--", "go", "vet"); code != 0 {
		t.Fatalf("second run exit = %d; stderr:\n%s", code, stderr)
	}
	if second := environment.api.runRequests[1]; second.BundleObjectKey != "" || len(environment.api.bundleRequests) != 1 {
		t.Fatalf("the second run re-sent history: key %q, bundle requests %d", second.BundleObjectKey,
			len(environment.api.bundleRequests))
	}
}

func TestExecPassesARemote196ThroughWithoutTheNotRunFile(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.addStreams(streamOutput("", "", exitCode(execExitNotRun), true))
	if _, _, code := runExecCommand(t, context.Background(), "false"); code != execExitNotRun {
		t.Fatalf("exit = %d, want 196 passed through", code)
	}
	if environment.notRunMarked() {
		t.Fatal("a command that ran and exited 196 must not create the not-run file: the shims would re-run it")
	}
}

func TestExecResumesTheStreamFromItsOffsets(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.addStreams(
		// Drops mid-run.
		streamOutput("abc", "x", nil, true),
		// The platform asks for a reconnect.
		func(writer http.ResponseWriter, request *http.Request) {
			sseTestFrame(writer, "stdout", base64.StdEncoding.EncodeToString([]byte("de")))
			sseTestFrame(writer, "reconnect", "{}")
		},
		streamOutput("f\n", "y\n", exitCode(0), true),
	)
	stdout, stderr, code := runExecCommand(t, context.Background(), "pnpm", "typecheck")
	if code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if stdout != "abcdef\n" {
		t.Fatalf("stdout = %q, want every byte exactly once", stdout)
	}
	if !strings.HasPrefix(stderr, "x") || !strings.Contains(stderr, "y\n") {
		t.Fatalf("stderr = %q", stderr)
	}
	want := []string{
		"stderr_offset=0&stdout_offset=0 last=0:0",
		"stderr_offset=1&stdout_offset=3 last=3:1",
		"stderr_offset=1&stdout_offset=5 last=5:1",
	}
	if strings.Join(environment.api.streamQueries, "|") != strings.Join(want, "|") {
		t.Fatalf("stream connections = %q, want %q", environment.api.streamQueries, want)
	}
}

func TestExecResendsTheWholeHistoryOn197(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runExecCommand(t, context.Background(), "true"); code != 0 {
		t.Fatalf("first run exit = %d; stderr:\n%s", code, stderr)
	}
	writeTestFile(t, filepath.Join(environment.repository.top, "pkg", "c.go"), "package pkg\n\nvar C = 3\n")
	environment.api.addStreams(
		streamOutput("", "", exitCode(execExitNeedsFull), false),
		streamOutput("done\n", "", exitCode(0), true),
	)
	stdout, stderr, code := runExecCommand(t, context.Background(), "true")
	if code != 0 || stdout != "done\n" {
		t.Fatalf("exit = %d stdout %q; stderr:\n%s", code, stdout, stderr)
	}
	sizes := []float64{}
	for _, request := range environment.api.bundleRequests {
		sizes = append(sizes, request["size_bytes"].(float64))
	}
	if len(sizes) != 3 || sizes[2] <= sizes[1] {
		t.Fatalf("bundle sizes = %v, want full, thin, then full again (larger than the thin one)", sizes)
	}
	if environment.notRunMarked() {
		t.Fatal("the resend ran the command, so nothing may fall back")
	}
}

func TestExecIsNotRunWhenRefusedBeforeItStarts(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.startFailStatus = http.StatusBadRequest
	environment.api.startFailBody = `{"detail":"FOO cannot be forwarded."}`
	_, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	if code != execExitNotRun || !environment.notRunMarked() {
		t.Fatalf("exit = %d, marked %v; want 196 and the not-run file", code, environment.notRunMarked())
	}
	if !strings.Contains(stderr, "FOO cannot be forwarded.") {
		t.Fatalf("stderr = %q, want the platform's reason", stderr)
	}
	if len(environment.api.runRequests) != 1 {
		t.Fatalf("a refusal was retried %d times", len(environment.api.runRequests))
	}
	if lines := environment.logLines(t); len(lines) != 1 || !strings.Contains(lines[0], "\trc=196\tmode=not-run/api\t") {
		t.Fatalf("log = %q", lines)
	}
}

func TestExecIsNotRunWhenTheAPIIsUnreachable(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.server.Close()
	_, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	if code != execExitNotRun || !environment.notRunMarked() {
		t.Fatalf("exit = %d, marked %v; stderr:\n%s", code, environment.notRunMarked(), stderr)
	}
	if !strings.Contains(stderr, "retrying") {
		t.Fatalf("stderr = %q, want the retry before giving up", stderr)
	}
}

func TestExecRetriesAPassingAgentTimeout(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.upFailures = 1
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runExecCommand(t, context.Background(), "go", "test"); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
}

func TestExecIsNotRunOutsideAGitWorktree(t *testing.T) {
	environment := newExecTestEnv(t)
	t.Chdir(t.TempDir())
	_, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	if code != execExitNotRun || !environment.notRunMarked() || !strings.Contains(stderr, "not inside a git worktree") {
		t.Fatalf("exit = %d, marked %v, stderr %q", code, environment.notRunMarked(), stderr)
	}
}

func TestExecWaitsForAProvisioningWorkspace(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.upStatus = client.WorkspaceStatusProvisioning
	environment.api.getStatuses = []string{client.WorkspaceStatusProvisioning, client.WorkspaceStatusReady}
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runExecCommand(t, context.Background(), "go", "test"); code != 0 ||
		!strings.Contains(stderr, "bringing up the default workspace") {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
}

func TestExecIsNotRunWhenTheWorkspaceFailed(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.upStatus = client.WorkspaceStatusFailed
	_, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	if code != execExitNotRun || !strings.Contains(stderr, "preflight failed: no bash") {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
}

func TestExecInterruptStopsTheRemoteCommand(t *testing.T) {
	environment := newExecTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	environment.api.addStreams(func(writer http.ResponseWriter, request *http.Request) {
		sseTestFrame(writer, "stdout", base64.StdEncoding.EncodeToString([]byte("working\n")))
		writer.(http.Flusher).Flush()
		cancel()
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	_, stderr, code := runExecCommand(t, ctx, "go", "test")
	if code != execExitInterrupted {
		t.Fatalf("exit = %d, want 130; stderr:\n%s", code, stderr)
	}
	if environment.api.cancels != 1 || environment.notRunMarked() {
		t.Fatalf("cancels = %d, marked %v", environment.api.cancels, environment.notRunMarked())
	}
}

func TestExecApplyBringsTheChangesBack(t *testing.T) {
	environment := newExecTestEnv(t)
	top := environment.repository.top
	writeTestFile(t, filepath.Join(top, "pkg", "a.go"), "package pkg\n\n// formatted\n")
	patch := gitTestOutput(t, top, "diff", "--binary") + "\n"
	gitTestOutput(t, top, "checkout", "--", "pkg/a.go")
	environment.api.exportBodies["patch"] = []byte(patch)
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))

	_, stderr, code := runExecCommand(t, context.Background(), "--apply", "--", "gofmt", "-w", "pkg")
	if code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if !environment.api.runRequests[0].Apply || environment.api.exportRequests[0].Kind != "patch" {
		t.Fatalf("run %+v export %+v", environment.api.runRequests[0], environment.api.exportRequests)
	}
	contents, _ := os.ReadFile(filepath.Join(top, "pkg", "a.go"))
	if !strings.Contains(string(contents), "// formatted") || !strings.Contains(stderr, "applied the workspace's changes (1 file(s))") {
		t.Fatalf("a.go = %q; stderr:\n%s", contents, stderr)
	}

	// A patch that no longer applies is kept, and the run reports failure.
	environment.api.exportBodies["patch"] = []byte("diff --git a/missing.go b/missing.go\n--- a/missing.go\n+++ b/missing.go\n@@ -1 +1 @@\n-old\n+new\n")
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	_, stderr, code = runExecCommand(t, context.Background(), "--apply", "--", "gofmt", "-w", "pkg")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for changes that did not apply; stderr:\n%s", code, stderr)
	}
	kept := filepath.Join(top, ".git", "ankra-exec-unapplied-run-2.patch")
	if _, statError := os.Stat(kept); statError != nil || !strings.Contains(stderr, kept) {
		t.Fatalf("the unapplied patch was not kept at %s: %v; stderr:\n%s", kept, statError, stderr)
	}
}

func tarGz(t *testing.T, entries []tar.Header, contents map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, header := range entries {
		header := header
		body := contents[header.Name]
		header.Size = int64(len(body))
		if header.Typeflag != tar.TypeReg {
			header.Size = 0
		}
		if writeError := tarWriter.WriteHeader(&header); writeError != nil {
			t.Fatal(writeError)
		}
		if header.Typeflag == tar.TypeReg {
			_, _ = tarWriter.Write([]byte(body))
		}
	}
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	return buffer.Bytes()
}

func TestExecFetchReplacesTheDirectoriesWhateverTheExitCode(t *testing.T) {
	environment := newExecTestEnv(t)
	top := environment.repository.top
	writeTestFile(t, filepath.Join(top, "test-results", "stale.txt"), "old run\n")
	environment.api.exportBodies["dirs"] = tarGz(t, []tar.Header{
		{Name: "test-results/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "test-results/trace.zip", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "test-results/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "elsewhere.txt", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"test-results/trace.zip": "trace", "../escape.txt": "x", "elsewhere.txt": "y"})
	environment.api.addStreams(streamOutput("", "1 failed\n", exitCode(1), true))

	_, stderr, code := runExecCommand(t, context.Background(), "--kind", "playwright", "--fetch", "test-results",
		"--fetch", "../outside", "--", "pnpm", "test:e2e")
	if code != 1 {
		t.Fatalf("exit = %d, want the remote 1; stderr:\n%s", code, stderr)
	}
	if exports := environment.api.exportRequests; len(exports) != 1 || exports[0].Kind != "dirs" ||
		strings.Join(exports[0].Paths, ",") != "test-results" {
		t.Fatalf("exports = %+v, want the one usable directory", exports)
	}
	if _, statError := os.Stat(filepath.Join(top, "test-results", "stale.txt")); statError == nil {
		t.Fatal("the local directory was not replaced")
	}
	if contents, _ := os.ReadFile(filepath.Join(top, "test-results", "trace.zip")); string(contents) != "trace" {
		t.Fatalf("trace.zip = %q", contents)
	}
	for _, unwanted := range []string{filepath.Join(top, "test-results", "link"), filepath.Join(filepath.Dir(top), "escape.txt"),
		filepath.Join(top, "elsewhere.txt")} {
		if _, statError := os.Lstat(unwanted); statError == nil {
			t.Fatalf("%s was extracted", unwanted)
		}
	}
	if !strings.Contains(stderr, "copied back test-results") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestExecCheckAnswersAndCaches(t *testing.T) {
	environment := newExecTestEnv(t)
	if _, _, code := runExecCommand(t, context.Background(), "--check"); code != 0 {
		t.Fatalf("--check = %d, want 0 for a connected repository", code)
	}
	requests := environment.api.requests
	if _, _, code := runExecCommand(t, context.Background(), "--check", "--kind", "go"); code != 0 {
		t.Fatalf("cached --check = %d", code)
	}
	if environment.api.requests != requests {
		t.Fatalf("the cached --check asked the platform again (%d -> %d requests)", requests, environment.api.requests)
	}
	if environment.notRunMarked() {
		t.Fatal("--check created the not-run file")
	}
}

func TestExecCheckRefusesAnUnconnectedRepository(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.repositories = nil
	if _, _, code := runExecCommand(t, context.Background(), "--check"); code != 1 {
		t.Fatalf("--check = %d, want 1", code)
	}
}

func TestExecCheckRefusesWithoutTheWorkspacesPermission(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.listStatus = http.StatusForbidden
	if _, _, code := runExecCommand(t, context.Background(), "--check"); code != 1 {
		t.Fatalf("--check = %d, want 1", code)
	}
}

func TestExecWithoutACommandIsAUsageError(t *testing.T) {
	newExecTestEnv(t)
	if _, _, code := runExecCommand(t, context.Background()); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
}

func TestFormatExecLogLine(t *testing.T) {
	line := formatExecLogLine(time.Date(2026, 10, 9, 8, 7, 6, 0, time.UTC), "acme/shop", "ws-1", 0,
		"remote/api", 1234, 2500*time.Millisecond, []string{"go", "test", "./..."})
	want := "2026-10-09T08:07:06Z\trepo=acme/shop\tpod=ws-1\trc=0\tmode=remote/api\tbundle_bytes=1234\twall_ms=2500\tcmd=go test ./... \n"
	if line != want {
		t.Fatalf("line = %q\nwant  %q", line, want)
	}
	long := formatExecLogLine(time.Unix(0, 0), "a/b", "", 1, "not-run/api", 0, 0, []string{strings.Repeat("x", 300)})
	if command := long[strings.Index(long, "cmd=")+4 : len(long)-1]; len(command) != 200 {
		t.Fatalf("cmd field is %d characters, want 200", len(command))
	}
}

func runWorkspaceCommand(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(append([]string{"workspace"}, args...))
	workspaceCommand, _, _ := rootCmd.Find([]string{"workspace"})
	resetTreeFlags(t, workspaceCommand.Commands()...)
	t.Cleanup(func() {
		resetTreeFlags(t, workspaceCommand.Commands()...)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	executeError := rootCmd.Execute()
	if executeError != nil {
		_, _ = fmt.Fprintln(stderr, executeError)
	}
	return stdout.String(), stderr.String(), exitCodeFor(executeError)
}

func TestWorkspaceUpWaitsUntilReady(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.upStatus = client.WorkspaceStatusProvisioning
	environment.api.getStatuses = []string{client.WorkspaceStatusReady}
	shrinkWorkspacePollInterval(t)
	stdout, stderr, code := runWorkspaceCommand(t, "up", "--wait")
	if code != 0 || !strings.Contains(stdout, "Status:      ready") || !strings.Contains(stderr, "was created") {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestWorkspaceUpReportsAFailedWorkspace(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.upStatus = client.WorkspaceStatusFailed
	_, stderr, code := runWorkspaceCommand(t, "up", "-o", "json")
	if code != exitError || !strings.Contains(stderr, "preflight failed") {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
}

func TestWorkspaceStatusAndDown(t *testing.T) {
	environment := newExecTestEnv(t)
	stdout, stderr, code := runWorkspaceCommand(t, "status", "-o", "json")
	if code != 0 {
		t.Fatalf("status exit = %d; stderr:\n%s", code, stderr)
	}
	var listed []client.Workspace
	if decodeError := json.Unmarshal([]byte(stdout), &listed); decodeError != nil || len(listed) != 1 || listed[0].ID != "ws-1" {
		t.Fatalf("status output = %s (%v)", stdout, decodeError)
	}
	if _, _, code = runWorkspaceCommand(t, "down"); code != exitCancelled || len(environment.api.deleted) != 0 {
		t.Fatalf("an unconfirmed down = %d, deleted %v", code, environment.api.deleted)
	}
	stdout, stderr, code = runWorkspaceCommand(t, "down", "--yes")
	if code != 0 || len(environment.api.deleted) != 1 || !strings.Contains(stdout, "is destroyed") {
		t.Fatalf("down exit = %d deleted %v\nstdout %s\nstderr %s", code, environment.api.deleted, stdout, stderr)
	}
	environment.api.repositories = nil
	if _, stderr, code = runWorkspaceCommand(t, "status"); code != exitNotFound ||
		!strings.Contains(stderr, "not connected to Ankra Pipelines") {
		t.Fatalf("status of an unconnected repository = %d; stderr:\n%s", code, stderr)
	}
}

func shrinkWorkspacePollInterval(t *testing.T) {
	t.Helper()
	original := workspacePollInterval
	workspacePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { workspacePollInterval = original })
}

func TestExecSettlesARunThePlatformCannotFollow(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.runStates["run-1"] = "finished"
	environment.api.addStreams(func(writer http.ResponseWriter, _ *http.Request) {
		sseTestFrame(writer, "stdout", base64.StdEncoding.EncodeToString([]byte("partial\n")))
		sseTestFrame(writer, "error", `{"message":"The run's files are no longer in the workspace.","state":"lost"}`)
	})
	stdout, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	// The run answers finished with no exit code here, and it printed output,
	// so it started: no fallback, a failure exit, and the reason said.
	if code != 1 || stdout != "partial\n" || environment.notRunMarked() ||
		!strings.Contains(stderr, "no longer in the workspace") {
		t.Fatalf("exit = %d stdout %q marked %v; stderr:\n%s", code, stdout, environment.notRunMarked(), stderr)
	}
	if len(environment.api.streamQueries) != 1 {
		t.Fatalf("an error frame was retried: %q", environment.api.streamQueries)
	}
}

func TestExecFallsBackWhenNothingProvesTheRunStarted(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.runStates["run-1"] = "accepted"
	environment.api.addStreams(func(writer http.ResponseWriter, _ *http.Request) {
		sseTestFrame(writer, "error", `{"message":"The run's files are no longer in the workspace."}`)
	})
	_, stderr, code := runExecCommand(t, context.Background(), "go", "test")
	if code != execExitNotRun || !environment.notRunMarked() || environment.api.cancels != 1 {
		t.Fatalf("exit = %d marked %v cancels %d; stderr:\n%s", code, environment.notRunMarked(),
			environment.api.cancels, stderr)
	}
}

func TestExecFetchKeepsTheLocalDirectoryWhenTheArchiveIsBroken(t *testing.T) {
	environment := newExecTestEnv(t)
	top := environment.repository.top
	writeTestFile(t, filepath.Join(top, "test-results", "previous.txt"), "keep me\n")
	environment.api.exportBodies["dirs"] = []byte("not a gzip stream")
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	_, stderr, code := runExecCommand(t, context.Background(), "--fetch", "test-results", "--", "pnpm", "test")
	if code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if contents, _ := os.ReadFile(filepath.Join(top, "test-results", "previous.txt")); string(contents) != "keep me\n" {
		t.Fatalf("the local results were lost: %q; stderr:\n%s", contents, stderr)
	}
	if !strings.Contains(stderr, "the local ones are unchanged") {
		t.Fatalf("stderr = %q", stderr)
	}
	leftovers, _ := filepath.Glob(filepath.Join(top, ".git", "ankra-exec-fetch-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging directories left behind: %v", leftovers)
	}
}
