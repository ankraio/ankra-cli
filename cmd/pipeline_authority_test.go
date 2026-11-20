package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	authorityTestRequestID = "7a1d6c2e-0000-4000-8000-000000000007"
	authorityTestPath      = "/api/v1/org/pipelines/authority-requests"
	authorityTestRequest   = `{"id":"` + authorityTestRequestID + `","repository_id":"` + testPipelineRepositoryID + `",
		"definition_id":"def-9","run_id":"run-3","head_sha":"abc123","ref":"refs/heads/feature","pull_request_number":12,
		"requested_by":"github:mark","status":"pending","resolved_by":null,"resolved_at":null,"reason":null,
		"created_at":"2026-11-10T08:00:00Z","diff":{"sections":[
		{"section":"caches","path":"stages.build.cache.size","from":"5Gi","to":"50Gi"},
		{"section":"caches","path":"stages.test.cache.size","from":null,"to":"10Gi"}],"yaml":"caches: {}"}}`
	workspaceProfilesTestPath = "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID + "/workspace-profiles"
)

func TestPipelineAuthorityListReadsThePendingInbox(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + authorityTestPath: {http.StatusOK, `{"items":[` + authorityTestRequest + `],"next_cursor":"next-1"}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "list",
		"--repository", testPipelineRepositoryID, "--limit", "10")
	if runError != nil {
		t.Fatalf("authority list: %v", runError)
	}
	for _, want := range []string{authorityTestRequestID, "github:mark", "caches", "--cursor next-1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Count(stdout, "caches") != 1 {
		t.Errorf("a section two paths share must be listed once:\n%s", stdout)
	}
	query := (*calls)[0].query
	for _, want := range []string{"status=pending", "limit=10", "repository_id=" + testPipelineRepositoryID} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
	if _, _, allError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "list", "--status", "all"); allError != nil {
		t.Fatalf("authority list --status all: %v", allError)
	}
	if strings.Contains((*calls)[1].query, "status=") {
		t.Errorf("--status all must not filter, query = %q", (*calls)[1].query)
	}
	if _, _, limitError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "list", "--limit", "500"); exitCodeFor(limitError) != exitUsage {
		t.Fatalf("--limit 500 error = %v, want exit %d", limitError, exitUsage)
	}
}

func TestPipelineAuthorityGetPrintsWhatIsAskedAndHowToDecide(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"GET " + authorityTestPath + "/" + authorityTestRequestID: {http.StatusOK, authorityTestRequest},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "get", authorityTestRequestID)
	if runError != nil {
		t.Fatalf("authority get: %v", runError)
	}
	for _, want := range []string{"Pull request: #12", "caches stages.build.cache.size: 5Gi -> 50Gi", "- -> 10Gi",
		"ankra pipeline definitions approve def-9", "ankra pipeline authority dismiss " + authorityTestRequestID} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	_, _, missingError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "get", "9d2f6c2e-0000-4000-8000-000000000009")
	if exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("unknown request error = %v (exit %d), want exit %d", missingError, exitCodeFor(missingError), exitNotFound)
	}
}

func TestPipelineAuthorityDismissNeedsAReasonAndAsksFirst(t *testing.T) {
	dismissPath := authorityTestPath + "/" + authorityTestRequestID + "/dismiss"
	server, calls := serveDocuments(t, map[string]documentRoute{"POST " + dismissPath: {http.StatusOK, authorityTestRequest}})
	if _, _, reasonError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "dismiss", authorityTestRequestID, "--yes"); exitCodeFor(reasonError) != exitUsage {
		t.Fatalf("dismiss without a reason error = %v, want exit %d", reasonError, exitUsage)
	}
	if _, _, declinedError := runDocumentCommand(t, server, newPipelineCommand(), "n\n", "authority", "dismiss", authorityTestRequestID,
		"--reason", "too large"); !errors.Is(declinedError, errCancelled) {
		t.Fatalf("declined dismiss error = %v, want errCancelled", declinedError)
	}
	requireDocumentCalls(t, calls)
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "dismiss", authorityTestRequestID,
		"--reason", "too large", "--yes")
	if runError != nil || !strings.Contains(stdout, "Dismissed pipeline authority request") {
		t.Fatalf("dismiss: stdout = %q, error = %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["reason"] != "too large" {
		t.Fatalf("dismiss body = %v", body)
	}
}

func TestPipelineAuthorityDismissSurfacesARequestNoLongerPending(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"POST " + authorityTestPath + "/" + authorityTestRequestID + "/dismiss": {http.StatusConflict, `{"detail":"The request is no longer pending."}`},
	})
	_, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "authority", "dismiss", authorityTestRequestID, "--reason", "x", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "no longer pending") {
		t.Fatalf("error = %v, want the platform's refusal", runError)
	}
}

func TestPipelinePolicyGetAndSet(t *testing.T) {
	const policy = `{"allowed_image_prefixes":[],"allowed_network_tiers":["restricted"],"max_cpu":"8","max_gpu":0}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/pipeline-policy": {http.StatusOK, policy},
		"PUT /api/v1/org/pipeline-policy": {http.StatusOK, policy},
	})
	stdout, _, getError := runDocumentCommand(t, server, newPipelineCommand(), "", "policy", "get")
	if getError != nil || !strings.Contains(stdout, `"max_cpu": "8"`) {
		t.Fatalf("policy get: stdout = %q, error = %v", stdout, getError)
	}
	*calls = nil
	for name, testCase := range map[string]struct {
		stdin     string
		arguments []string
		exitCode  int
	}{
		"nothing named": {"", []string{"--yes"}, exitUsage},
		"declined":      {"n\n", []string{"--max-cpu", "8"}, exitCancelled},
	} {
		_, _, runError := runDocumentCommand(t, server, newPipelineCommand(), testCase.stdin, append([]string{"policy", "set"}, testCase.arguments...)...)
		if exitCodeFor(runError) != testCase.exitCode {
			t.Errorf("%s: error = %v (exit %d), want exit %d", name, runError, exitCodeFor(runError), testCase.exitCode)
		}
	}
	requireDocumentCalls(t, calls)
	policyFile := filepath.Join(t.TempDir(), "policy.yaml")
	if writeError := os.WriteFile(policyFile, []byte("allowed_image_prefixes: [ghcr.io/]\nupdated_at: null\nallowed_node_selectors:\n  pool: [ci]\nmax_cpu: \"4\"\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if _, _, setError := runDocumentCommand(t, server, newPipelineCommand(), "", "policy", "set", "-f", policyFile,
		"--max-cpu", "8", "--max-gpu", "1", "--allowed-network-tiers", "restricted,internet", "--allowed-credentials", "", "--yes"); setError != nil {
		t.Fatalf("policy set: %v", setError)
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	tiers, _ := body["allowed_network_tiers"].([]any)
	credentials, isCleared := body["allowed_credentials"].([]any)
	selectors, _ := body["allowed_node_selectors"].(map[string]any)
	if body["max_cpu"] != "8" || body["max_gpu"] != float64(1) || len(tiers) != 2 || !isCleared || len(credentials) != 0 || selectors["pool"] == nil {
		t.Fatalf("policy body = %v", body)
	}
	for _, member := range []string{"max_memory", "allowed_image_prefixes", "updated_at"} {
		if _, isSent := body[member]; isSent {
			t.Errorf("%s must not be sent: %v", member, body)
		}
	}
}

func TestPipelinePolicyNeedsPipelinesManage(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/pipeline-policy": {http.StatusForbidden, `{"detail":"permission_denied","permission":"pipelines.manage"}`},
	})
	_, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "policy", "get")
	if exitCodeFor(runError) != exitForbidden {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitForbidden)
	}
}

func TestPipelineCachesListsTheRepositoryCaches(t *testing.T) {
	cachesPath := "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID + "/pipeline-caches"
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + cachesPath: {http.StatusOK, `{"caches":[{"id":"c1","key":"go-mod-abc","mount_path":"/go/pkg/mod","trust":"default_branch",
			"size":"5Gi","cluster_name":"ci","last_used_at":"2026-11-10T08:00:00Z","expires_at":"2026-11-24T08:00:00Z","state":"active"}],
			"retention_days":14,"next_cursor":null}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "--repository", testPipelineRepositoryID, "--cursor", "page-2")
	if runError != nil {
		t.Fatalf("pipeline caches: %v", runError)
	}
	for _, want := range []string{"go-mod-abc", "/go/pkg/mod", "default_branch", "lapses 14 days after its last use"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	if query := (*calls)[0].query; query != "cursor=page-2&limit=50" {
		t.Fatalf("query = %q, want cursor=page-2&limit=50", query)
	}
}

func TestPipelineCachesSaysSoWhenThereAreNoneAndWhenNothingIsConnected(t *testing.T) {
	cachesPath := "/api/v1/org/applications/" + testApplicationID + "/pipeline-caches"
	empty, _ := serveDocuments(t, map[string]documentRoute{
		"GET " + cachesPath: {http.StatusOK, `{"caches":[],"retention_days":14,"next_cursor":null}`},
	})
	stdout, _, runError := runDocumentCommand(t, empty, newPipelineCommand(), "", "caches", "--application", testApplicationID)
	if runError != nil || !strings.Contains(stdout, "keeps no caches") {
		t.Fatalf("stdout = %q, error = %v", stdout, runError)
	}
	unconnected, _ := serveDocuments(t, nil)
	_, _, missingError := runDocumentCommand(t, unconnected, newPipelineCommand(), "", "caches", "--application", testApplicationID)
	if exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("error = %v (exit %d), want exit %d", missingError, exitCodeFor(missingError), exitNotFound)
	}
}

const (
	pipelineCacheClearTestRepositoryPath  = "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID + "/pipeline-caches/clear"
	pipelineCacheClearTestApplicationPath = "/api/v1/org/applications/" + testApplicationID + "/pipeline-caches/clear"
)

func TestPipelineCachesClearSendsThePathAndScopeAndSaysWhatWasCleared(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + pipelineCacheClearTestRepositoryPath: {http.StatusOK,
			`{"archives_cleared":2,"archive_bytes":1610612736,"volumes_cleared":1,"volumes_deleted_after":"2026-11-21T09:00:00Z"}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "clear",
		"--repository", testPipelineRepositoryID, "--path", " /go/pkg/mod ", "--trust", "branch", "--yes")
	if runError != nil {
		t.Fatalf("caches clear: %v", runError)
	}
	want := "Cleared the cache at /go/pkg/mod (branch scope): 2 archives (1.5 GiB) that no new run restores, " +
		"and 1 volume that no new run mounts. The platform deletes the cleared volume no earlier than 2026-11-21T09:00:00Z.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	requireDocumentCalls(t, calls, "POST "+pipelineCacheClearTestRepositoryPath)
	body := decodeDocumentCallBody(t, (*calls)[0])
	if len(body) != 2 || body["path"] != "/go/pkg/mod" || body["trust"] != "branch" {
		t.Fatalf("body = %v", body)
	}
}

func TestPipelineCachesClearOfAnApplicationLeavesTrustOutForBothScopes(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + pipelineCacheClearTestApplicationPath: {http.StatusOK,
			`{"archives_cleared":1,"archive_bytes":524288,"volumes_cleared":0,"volumes_deleted_after":null}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "clear",
		"--application", testApplicationID, "--path", "/workspace/.pnpm-store", "--yes")
	if runError != nil {
		t.Fatalf("caches clear: %v", runError)
	}
	want := "Cleared the cache at /workspace/.pnpm-store (both scopes): 1 archive (512.0 KiB) that no new run restores.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	if _, isSent := body["trust"]; isSent || body["path"] != "/workspace/.pnpm-store" {
		t.Fatalf("body = %v, want the path alone", body)
	}
}

func TestPipelineCachesClearSaysNothingWasHeldWhenTheAnswerIsZeros(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"POST " + pipelineCacheClearTestRepositoryPath: {http.StatusOK,
			`{"archives_cleared":0,"archive_bytes":0,"volumes_cleared":0,"volumes_deleted_after":null}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "clear",
		"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod", "--trust", "default_branch", "--yes")
	if runError != nil {
		t.Fatalf("a zero answer is not a failure: %v", runError)
	}
	want := "Nothing was cleared: no ready archive and no volume was held for the cache at /go/pkg/mod " +
		"(default_branch scope). It may have been cleared already, or the path or scope names no cache; " +
		"'ankra pipeline caches --repository " + testPipelineRepositoryID + "' lists them.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, "Cleared ") {
		t.Fatalf("a zero answer must not read as a clear: %q", stdout)
	}
}

func TestPipelineCachesClearPrintsTheAnswerAsSentForJSONAndYAML(t *testing.T) {
	const answer = `{"archives_cleared":0,"archive_bytes":0,"volumes_cleared":3,"volumes_deleted_after":"2026-11-21T09:00:00Z"}`
	server, _ := serveDocuments(t, map[string]documentRoute{
		"POST " + pipelineCacheClearTestRepositoryPath: {http.StatusOK, answer},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "clear",
		"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("caches clear -o json: %v", runError)
	}
	var printed, sent map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &printed); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	_ = json.Unmarshal([]byte(answer), &sent)
	if !reflect.DeepEqual(printed, sent) {
		t.Fatalf("printed = %v, want the answer as sent %v", printed, sent)
	}
	yamlOut, _, yamlError := runDocumentCommand(t, server, newPipelineCommand(), "", "caches", "clear",
		"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod", "--yes", "-o", "yaml")
	if yamlError != nil || !strings.Contains(yamlOut, "volumes_cleared: 3") ||
		!strings.Contains(yamlOut, "volumes_deleted_after:") || strings.Contains(yamlOut, "Cleared") {
		t.Fatalf("yaml stdout = %q, error = %v", yamlOut, yamlError)
	}
}

func TestPipelineCachesClearAsksFirstAndRefusesABadRequestBeforeSending(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + pipelineCacheClearTestRepositoryPath: {http.StatusOK,
			`{"archives_cleared":0,"archive_bytes":0,"volumes_cleared":0,"volumes_deleted_after":null}`},
	})
	_, stderr, declinedError := runDocumentCommand(t, server, newPipelineCommand(), "n\n", "caches", "clear",
		"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod")
	if !errors.Is(declinedError, errCancelled) || exitCodeFor(declinedError) != exitCancelled {
		t.Fatalf("declined clear error = %v, want errCancelled", declinedError)
	}
	if !strings.Contains(stderr, "Clear the cache at /go/pkg/mod (both scopes)?") {
		t.Fatalf("the question must name the cache, stderr = %q", stderr)
	}
	for name, arguments := range map[string][]string{
		"no path":       {"--repository", testPipelineRepositoryID, "--yes"},
		"blank path":    {"--repository", testPipelineRepositoryID, "--path", "  ", "--yes"},
		"unknown trust": {"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod", "--trust", "main", "--yes"},
		"bad output":    {"--repository", testPipelineRepositoryID, "--path", "/go/pkg/mod", "--yes", "-o", "jsn"},
	} {
		_, _, usageError := runDocumentCommand(t, server, newPipelineCommand(), "", append([]string{"caches", "clear"}, arguments...)...)
		if exitCodeFor(usageError) != exitUsage {
			t.Errorf("%s: error = %v (exit %d), want exit %d", name, usageError, exitCodeFor(usageError), exitUsage)
		}
	}
	requireDocumentCalls(t, calls)
}

func TestPipelineCachesClearPrintsEachRefusalWithItsExitCode(t *testing.T) {
	lanes := []struct {
		name     string
		path     string
		selector []string
		status   int
		body     string
		message  string
		exitCode int
	}{
		{"unknown repository", pipelineCacheClearTestRepositoryPath, []string{"--repository", testPipelineRepositoryID},
			http.StatusNotFound, `{"detail":"Pipeline repository not found"}`, "Pipeline repository not found", exitNotFound},
		{"application without a pipeline", pipelineCacheClearTestApplicationPath, []string{"--application", testApplicationID},
			http.StatusNotFound, `{"detail":"This application has no connected pipeline repository"}`,
			"This application has no connected pipeline repository", exitNotFound},
		{"runs kept starting", pipelineCacheClearTestRepositoryPath, []string{"--repository", testPipelineRepositoryID},
			http.StatusConflict, `{"detail":"Runs kept starting with this cache while it was being cleared. Nothing was cleared; try again."}`,
			"Runs kept starting with this cache while it was being cleared. Nothing was cleared; try again.", exitBlocked},
		{"request the platform would not read", pipelineCacheClearTestRepositoryPath, []string{"--repository", testPipelineRepositoryID},
			http.StatusUnprocessableEntity,
			`{"detail":[{"type":"value_error","loc":["body","path"],"msg":"Value error, A cache path is required","input":" "}]}`,
			"Value error, A cache path is required", exitUsage},
		{"role without pipelines.operate", pipelineCacheClearTestRepositoryPath, []string{"--repository", testPipelineRepositoryID},
			http.StatusForbidden, `{"detail":"permission_denied","permission":"pipelines.operate"}`, "pipelines.operate", exitForbidden},
		{"platform failure", pipelineCacheClearTestRepositoryPath, []string{"--repository", testPipelineRepositoryID},
			http.StatusInternalServerError, `{"detail":"Internal error"}`, "Internal error", exitError},
	}
	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			server, _ := serveDocuments(t, map[string]documentRoute{"POST " + lane.path: {lane.status, lane.body}})
			arguments := append([]string{"caches", "clear", "--path", "/go/pkg/mod", "--yes"}, lane.selector...)
			// The root command silences cobra's usage text on an error; this
			// tree runs without the root, so it is silenced here the same way.
			pipelineCommand := newPipelineCommand()
			pipelineCommand.SilenceUsage = true
			stdout, _, runError := runDocumentCommand(t, server, pipelineCommand, "", arguments...)
			if runError == nil || !strings.Contains(runError.Error(), lane.message) {
				t.Fatalf("error = %v, want the platform's %q", runError, lane.message)
			}
			if exitCodeFor(runError) != lane.exitCode {
				t.Fatalf("exit = %d, want %d (error %v)", exitCodeFor(runError), lane.exitCode, runError)
			}
			if stdout != "" {
				t.Fatalf("a refusal printed on stdout: %q", stdout)
			}
		})
	}
}

func TestPipelineCachesClearSentenceCountsWhatTheAnswerHolds(t *testing.T) {
	deletedAfter := "2026-11-21T09:00:00Z"
	selector := client.PipelineSelector{RepositoryID: testPipelineRepositoryID}
	sentence := pipelineCacheClearSentence("/go/pkg/mod", "", selector,
		pipelineCacheClearAnswer{VolumesCleared: 2, VolumesDeletedAfter: &deletedAfter})
	want := "Cleared the cache at /go/pkg/mod (both scopes): 2 volumes that no new run mounts. " +
		"The platform deletes the cleared volumes no earlier than 2026-11-21T09:00:00Z."
	if sentence != want {
		t.Fatalf("sentence = %q, want %q", sentence, want)
	}
}

func TestPipelineWorkspaceProfilesGetPrintsTheWholeDocument(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"GET " + workspaceProfilesTestPath: {http.StatusOK, `{"repository_id":"` + testPipelineRepositoryID +
			`","profiles":{"go":{"image":"golang:1.26","services":[{"name":"postgres","image":"postgres:17"}]}}}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newPipelineCommand(), "", "workspace-profiles", "get", "--repository", testPipelineRepositoryID)
	if runError != nil || !strings.Contains(stdout, `"name": "postgres"`) {
		t.Fatalf("the services a typed read drops must be printed, got %q (%v)", stdout, runError)
	}
}

func TestPipelineWorkspaceProfilesPutReplacesThemFromTheFile(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"PUT " + workspaceProfilesTestPath: {http.StatusOK, `{"repository_id":"` + testPipelineRepositoryID + `","profiles":{"go":{"image":"golang:1.26"}}}`},
	})
	profilesFile := filepath.Join(t.TempDir(), "profiles.yaml")
	if writeError := os.WriteFile(profilesFile, []byte("repository_id: x\nprofiles:\n  go:\n    image: golang:1.26\n    setup: [make deps]\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	arguments := []string{"workspace-profiles", "put", "--repository", testPipelineRepositoryID, "-f", profilesFile}
	_, stderr, declinedError := runDocumentCommand(t, server, newPipelineCommand(), "n\n", arguments...)
	if !errors.Is(declinedError, errCancelled) || !strings.Contains(stderr, "the profiles go") {
		t.Fatalf("declined put error = %v, prompt = %q", declinedError, stderr)
	}
	requireDocumentCalls(t, calls)
	if _, _, putError := runDocumentCommand(t, server, newPipelineCommand(), "", append(arguments, "--yes")...); putError != nil {
		t.Fatalf("workspace-profiles put: %v", putError)
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	profiles, _ := body["profiles"].(map[string]any)
	goProfile, _ := profiles["go"].(map[string]any)
	if len(body) != 1 || len(profiles) != 1 || goProfile["image"] != "golang:1.26" || goProfile["setup"] == nil {
		t.Fatalf("put body = %v, want the file's profiles member alone", body)
	}
	if _, _, fileError := runDocumentCommand(t, server, newPipelineCommand(), "", "workspace-profiles", "put", "--repository", testPipelineRepositoryID); exitCodeFor(fileError) != exitUsage {
		t.Fatalf("put without -f error = %v, want exit %d", fileError, exitUsage)
	}
}
