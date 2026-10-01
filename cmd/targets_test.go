package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	testHostTargetWebID   = "1f0c6c1e-2b1a-4c55-8f3a-0a1b2c3d4e51"
	testHostTargetOtherID = "1f0c6c1e-2b1a-4c55-8f3a-0a1b2c3d4e52"
	testDeploymentID      = "7d0c5a4e-7f55-4f0e-9d8a-2f5a3c1b9e70"
	testJoinTokenSecret   = "ankra_hjt_0123456789abcdef"
)

// deployPlatform is a fake of the org deploy API: it answers each
// "METHOD /path" from routes and records every request it was sent.
type deployPlatform struct {
	mutex    sync.Mutex
	routes   map[string]deployPlatformAnswer
	requests []recordedDeployCall
}

type deployPlatformAnswer struct {
	status int
	body   string
}

type recordedDeployCall struct {
	route string
	query string
	body  string
}

func newDeployPlatform(t *testing.T, routes map[string]deployPlatformAnswer) *deployPlatform {
	t.Helper()
	platform := &deployPlatform{routes: routes}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		route := request.Method + " " + request.URL.Path
		platform.mutex.Lock()
		platform.requests = append(platform.requests, recordedDeployCall{route: route, query: request.URL.RawQuery, body: string(body)})
		answer, isKnown := platform.routes[route]
		platform.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if !isKnown {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Not Found"}`))
			return
		}
		status := answer.status
		if status == 0 {
			status = http.StatusOK
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(answer.body))
	}))
	t.Cleanup(server.Close)
	previousClient := apiClient
	apiClient = client.New("test-token", server.URL)
	t.Cleanup(func() { apiClient = previousClient })
	return platform
}

func (platform *deployPlatform) calls() []recordedDeployCall {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	return append([]recordedDeployCall{}, platform.requests...)
}

func (platform *deployPlatform) callsTo(route string) []recordedDeployCall {
	matching := []recordedDeployCall{}
	for _, call := range platform.calls() {
		if call.route == route {
			matching = append(matching, call)
		}
	}
	return matching
}

// runDeployCommand runs a fresh command tree with separate stdout and stderr
// so the tests can hold -o json output to being parseable on its own.
func runDeployCommand(t *testing.T, command *cobra.Command, stdin string, arguments ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetIn(strings.NewReader(stdin))
	command.SetArgs(arguments)
	command.SilenceUsage = true
	command.SilenceErrors = true
	executeError := command.Execute()
	return stdout.String(), stderr.String(), executeError
}

const hostTargetsAnswer = `{"host_targets":[
 {"id":"` + testHostTargetWebID + `","name":"web-1","environment":"production","status":"online","labels":{"role":"web","zone":"a"},
  "agent_version":"2.1.40","last_heartbeat_at":"2026-09-30T10:00:00Z"},
 {"id":"` + testHostTargetOtherID + `","name":"web-1","environment":"staging","status":"unknown"}]}`

func TestJoinTokenCreatePrintsTheTokenOnceAndOnlyTheTokenOnStdout(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"POST /api/v1/org/environments/production/host-join-tokens": {status: http.StatusCreated,
			body: `{"join_token":"` + testJoinTokenSecret + `","expires_at":"2026-09-30T11:00:00Z","environment":"production"}`},
	})
	stdout, stderr, executeError := runDeployCommand(t, newTargetsCommand(), "", "join-token", "create", "--environment", "production")
	if executeError != nil {
		t.Fatalf("join-token create failed: %v", executeError)
	}
	if stdout != testJoinTokenSecret+"\n" {
		t.Fatalf("stdout must be the token alone, got %q", stdout)
	}
	if strings.Contains(stderr, testJoinTokenSecret) {
		t.Fatalf("the token must be printed once, stderr repeats it:\n%s", stderr)
	}
	if !strings.Contains(stderr, "expires 2026-09-30T11:00:00Z") || !strings.Contains(stderr, "--token-stdin") {
		t.Fatalf("stderr should carry the expiry and the next step:\n%s", stderr)
	}
	calls := platform.callsTo("POST /api/v1/org/environments/production/host-join-tokens")
	if len(calls) != 1 || calls[0].body != `{}` {
		t.Fatalf("without --ttl the platform default applies, calls = %+v", calls)
	}
}

func TestJoinTokenCreateJSONCarriesTheContractFields(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"POST /api/v1/org/environments/production/host-join-tokens": {
			body: `{"join_token":"` + testJoinTokenSecret + `","expires_at":"2026-09-30T14:00:00Z","environment":"production"}`},
	})
	stdout, stderr, executeError := runDeployCommand(t, newTargetsCommand(), "",
		"join-token", "create", "--environment", "production", "--ttl", "4h", "-o", "json")
	if executeError != nil {
		t.Fatalf("join-token create -o json failed: %v", executeError)
	}
	var document map[string]string
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if document["join_token"] != testJoinTokenSecret || document["expires_at"] != "2026-09-30T14:00:00Z" ||
		document["environment"] != "production" || len(document) != 3 {
		t.Fatalf("JSON document = %v", document)
	}
	if strings.Contains(stderr, testJoinTokenSecret) {
		t.Fatalf("the token leaks to stderr: %s", stderr)
	}
	if calls := platform.callsTo("POST /api/v1/org/environments/production/host-join-tokens"); len(calls) != 1 ||
		calls[0].body != `{"ttl_seconds":14400}` {
		t.Fatalf("--ttl 4h must send ttl_seconds 14400, calls = %+v", calls)
	}
}

func TestJoinTokenCreateRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := [][]string{
		{"join-token", "create", "--environment", "production", "--ttl", "25h"},
		{"join-token", "create", "--environment", "production", "--ttl", "0s"},
		{"join-token", "create", "--environment", "Production!"},
	}
	for _, arguments := range cases {
		platform := newDeployPlatform(t, map[string]deployPlatformAnswer{})
		_, _, executeError := runDeployCommand(t, newTargetsCommand(), "", arguments...)
		if exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: exit code %d (%v), want %d", arguments, exitCodeFor(executeError), executeError, exitUsage)
		}
		if calls := platform.calls(); len(calls) != 0 {
			t.Fatalf("%v: no request expected, got %+v", arguments, calls)
		}
	}
}

func TestTargetsListRendersTableAndJSON(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: hostTargetsAnswer},
	})
	stdout, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "list")
	if executeError != nil {
		t.Fatalf("targets list failed: %v", executeError)
	}
	for _, want := range []string{"web-1", "production", "online", "role=web,zone=a", "2.1.40", testHostTargetWebID} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("table misses %q:\n%s", want, stdout)
		}
	}

	stdout, _, executeError = runDeployCommand(t, newTargetsCommand(), "", "list", "--environment", "production", "-o", "json")
	if executeError != nil {
		t.Fatalf("targets list -o json failed: %v", executeError)
	}
	var document struct {
		HostTargets []map[string]any `json:"host_targets"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || len(document.HostTargets) != 2 {
		t.Fatalf("JSON = %s (%v)", stdout, decodeError)
	}
	calls := platform.callsTo("GET /api/v1/org/host-targets")
	if calls[len(calls)-1].query != "environment=production" {
		t.Fatalf("--environment must filter server-side, query = %q", calls[len(calls)-1].query)
	}
}

func TestTargetsListNamesAnEnvironmentThatDoesNotExist(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {status: http.StatusNotFound, body: `{"detail":"Environment not found"}`},
		"GET /api/v1/org/deployments":  {status: http.StatusNotFound, body: `{"detail":"Environment not found"}`},
	})
	commands := [][]string{
		{"targets", "list", "--environment", "prodution"},
		{"targets", "get", "web-1", "--environment", "prodution"},
		{"deployments", "list", "--environment", "prodution"},
	}
	for _, arguments := range commands {
		root := &cobra.Command{Use: "ankra"}
		root.AddCommand(newTargetsCommand(), newDeploymentsCommand())
		_, _, executeError := runDeployCommand(t, root, "", arguments...)
		if exitCodeFor(executeError) != exitNotFound ||
			!strings.Contains(executeError.Error(), `environment "prodution" does not exist`) ||
			!strings.Contains(executeError.Error(), "ankra targets join-token create --environment prodution") {
			t.Fatalf("%v: an unknown environment should exit %d and say how to create it, got %v", arguments, exitNotFound, executeError)
		}
	}
}

func TestTargetsListEmptyAndTruncated(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: `{"host_targets":[],"truncated":false}`},
	})
	stdout, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "list", "--environment", "production")
	if executeError != nil || !strings.Contains(stdout, `No host targets in environment "production"`) {
		t.Fatalf("an empty environment should say so: %v\n%s", executeError, stdout)
	}
	stdout, _, _ = runDeployCommand(t, newTargetsCommand(), "", "list", "-o", "json")
	if strings.TrimSpace(stdout) != "{\n  \"host_targets\": [],\n  \"truncated\": false\n}" {
		t.Fatalf("empty JSON must be an empty list, got %s", stdout)
	}
	if calls := platform.callsTo("GET /api/v1/org/environments"); len(calls) != 0 {
		t.Fatalf("an empty listing needs no environments lookup, got %d", len(calls))
	}

	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: `{"host_targets":[{"id":"` + testHostTargetWebID +
			`","name":"web-1","environment":"production","status":"online"}],"truncated":true}`},
	})
	stdout, stderr, executeError := runDeployCommand(t, newTargetsCommand(), "", "list")
	if executeError != nil || !strings.Contains(stdout, "web-1") ||
		!strings.Contains(stderr, "Showing the first 1 host targets") {
		t.Fatalf("a truncated listing must say so on stderr: %v\nstdout:\n%s\nstderr:\n%s", executeError, stdout, stderr)
	}
	stdout, _, _ = runDeployCommand(t, newTargetsCommand(), "", "list", "-o", "json")
	var document struct {
		Truncated bool `json:"truncated"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || !document.Truncated {
		t.Fatalf("-o json must carry truncated: %s (%v)", stdout, decodeError)
	}
	_, _, executeError = runDeployCommand(t, newTargetsCommand(), "", "get", "web-9")
	if exitCodeFor(executeError) != exitNotFound || !strings.Contains(executeError.Error(), "row cap") {
		t.Fatalf("a name missing from a truncated listing must say the listing was cut, got %v", executeError)
	}
}

func TestTargetsGetResolvesANameThroughTheListing(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: `{"host_targets":[{"id":"` + testHostTargetWebID + `","name":"web-1","environment":"production"}]}`},
		"GET /api/v1/org/host-targets/" + testHostTargetWebID: {body: `{"id":"` + testHostTargetWebID + `","name":"web-1",
			"environment":"production","status":"online","hostname":"web-1.internal","os":"linux","arch":"amd64",
			"supported_job_types":["deploy_release"],"running":[{"release":"ai-portal","digest":"sha256:abc"}]}`},
	})
	stdout, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "get", "web-1")
	if executeError != nil {
		t.Fatalf("targets get failed: %v", executeError)
	}
	for _, want := range []string{"web-1.internal", "linux/amd64", "deploy_release", "ai-portal  sha256:abc"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("detail misses %q:\n%s", want, stdout)
		}
	}
	if len(platform.callsTo("GET /api/v1/org/host-targets/"+testHostTargetWebID)) != 1 {
		t.Fatalf("the name must resolve to the id route, calls = %+v", platform.calls())
	}

	stdout, _, executeError = runDeployCommand(t, newTargetsCommand(), "", "get", testHostTargetWebID, "-o", "json")
	if executeError != nil {
		t.Fatalf("targets get <id> -o json failed: %v", executeError)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || document["status"] != "online" {
		t.Fatalf("JSON = %s (%v)", stdout, decodeError)
	}
}

func TestTargetsGetNameResolutionFailures(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: hostTargetsAnswer},
	})
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "get", "web-1")
	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "pass --environment or the id") {
		t.Fatalf("an ambiguous name should be a usage error naming both ids, got %v", executeError)
	}
	_, _, executeError = runDeployCommand(t, newTargetsCommand(), "", "get", "db-9")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("an unknown name should exit %d, got %d (%v)", exitNotFound, exitCodeFor(executeError), executeError)
	}
}

func TestTargetsRevokeRequiresConfirmation(t *testing.T) {
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: `{"host_targets":[{"id":"` + testHostTargetWebID + `","name":"web-1","environment":"production"}]}`},
		"POST /api/v1/org/host-targets/" + testHostTargetWebID + "/revoke": {body: `{"id":"` + testHostTargetWebID + `","name":"web-1",
			"environment":"production","revoked_at":"2026-09-30T12:00:00Z"}`},
	})
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "revoke", "web-1")
	if exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("revoke without --yes and no answer must be cancelled, got %v", executeError)
	}
	if calls := platform.callsTo("POST /api/v1/org/host-targets/" + testHostTargetWebID + "/revoke"); len(calls) != 0 {
		t.Fatalf("nothing may be revoked without confirmation, calls = %+v", calls)
	}

	stdout, stderr, executeError := runDeployCommand(t, newTargetsCommand(), "", "revoke", "web-1", "--yes", "-o", "json")
	if executeError != nil {
		t.Fatalf("revoke --yes failed: %v", executeError)
	}
	if stderr != "" {
		t.Fatalf("--yes must not prompt, stderr = %q", stderr)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || document["revoked_at"] == nil {
		t.Fatalf("JSON = %s (%v)", stdout, decodeError)
	}
	if calls := platform.callsTo("POST /api/v1/org/host-targets/" + testHostTargetWebID + "/revoke"); len(calls) != 1 {
		t.Fatalf("one revoke expected, calls = %+v", calls)
	}

	_, _, executeError = runDeployCommand(t, newTargetsCommand(), "y\n", "revoke", testHostTargetWebID)
	if executeError != nil {
		t.Fatalf("an interactive yes must revoke: %v", executeError)
	}
}

func TestTargetsRevokeIgnoresRevokedTargetsWhenResolvingAName(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/host-targets": {body: `{"host_targets":[{"id":"` + testHostTargetWebID + `","name":"web-1",
			"environment":"production","revoked_at":"2026-09-01T00:00:00Z"}]}`},
	})
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), "", "revoke", "web-1", "--yes")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("an already revoked name is not a revocable target, got %v", executeError)
	}
}

func TestDeployCommandsExplainAPlatformWithoutTheRoutes(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{})
	commands := [][]string{
		{"targets", "list"},
		{"targets", "join-token", "create", "--environment", "production"},
		{"deployments", "list"},
	}
	for _, arguments := range commands {
		root := &cobra.Command{Use: "ankra"}
		root.AddCommand(newTargetsCommand(), newDeploymentsCommand())
		_, _, executeError := runDeployCommand(t, root, "", arguments...)
		if executeError == nil || !strings.Contains(executeError.Error(), "does not serve host deploy targets yet") {
			t.Fatalf("%v: got %v", arguments, executeError)
		}
		if exitCodeFor(executeError) != exitError {
			t.Fatalf("%v: a missing route is not a missing resource, exit %d", arguments, exitCodeFor(executeError))
		}
	}
}

func TestDeployCommandsMapNotFoundAndPermissionDenied(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/deployments/" + testDeploymentID: {status: http.StatusNotFound, body: `{"detail":"deployment not found"}`},
		"POST /api/v1/org/host-targets/" + testHostTargetWebID + "/revoke": {status: http.StatusForbidden,
			body: `{"detail":"permission_denied","permission":"pipelines:manage"}`},
	})
	_, _, executeError := runDeployCommand(t, newDeploymentsCommand(), "", "get", testDeploymentID)
	if exitCodeFor(executeError) != exitNotFound || !strings.Contains(executeError.Error(), "deployment not found") {
		t.Fatalf("a missing deployment should exit %d with the platform's detail, got %v", exitNotFound, executeError)
	}
	_, _, executeError = runDeployCommand(t, newTargetsCommand(), "", "revoke", testHostTargetWebID, "--yes")
	if exitCodeFor(executeError) != exitForbidden || !strings.Contains(executeError.Error(), "pipelines:manage") {
		t.Fatalf("an RBAC refusal should exit %d naming the permission, got %v", exitForbidden, executeError)
	}
}

func TestDeploymentsListSendsTheFiltersAndRendersBothFormats(t *testing.T) {
	const repositoryID = "7d0c5a4e-7f55-4f0e-9d8a-2f5a3c1b9e61"
	platform := newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/deployments": {body: `{"deployments":[{"id":"` + testDeploymentID + `","environment":"production",
			"release_name":"ai-portal","artifact_repository":"harbor.example/p/ankra-cloud/ai-portal",
			"artifact_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","state":"succeeded",
			"target_count":1,"succeeded_count":1,"failed_count":0}],"next_cursor":"next-page"}`},
	})
	stdout, stderr, executeError := runDeployCommand(t, newDeploymentsCommand(), "",
		"list", "--environment", "production", "--repository", repositoryID, "--limit", "10")
	if executeError != nil {
		t.Fatalf("deployments list failed: %v", executeError)
	}
	for _, want := range []string{testDeploymentID, "ai-portal", "sha256:0123456789ab", "succeeded", "1/1 ok, 0 failed"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("table misses %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "--cursor next-page") {
		t.Fatalf("the next page hint belongs on stderr:\n%s", stderr)
	}
	calls := platform.callsTo("GET /api/v1/org/deployments")
	if calls[0].query != "environment=production&limit=10&repository_id="+repositoryID {
		t.Fatalf("query = %q", calls[0].query)
	}

	stdout, _, executeError = runDeployCommand(t, newDeploymentsCommand(), "", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("deployments list -o json failed: %v", executeError)
	}
	var document struct {
		Deployments []map[string]any `json:"deployments"`
		NextCursor  string           `json:"next_cursor"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil ||
		len(document.Deployments) != 1 || document.NextCursor != "next-page" {
		t.Fatalf("JSON = %s (%v)", stdout, decodeError)
	}

	_, _, executeError = runDeployCommand(t, newDeploymentsCommand(), "", "list", "--repository", "ankraio/ankra-cloud")
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("--repository must be an id, got %v", executeError)
	}
}

func TestDeploymentsGetShowsEveryTarget(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/deployments/" + testDeploymentID: {body: `{"id":"` + testDeploymentID + `","environment":"production",
			"release_name":"ai-portal","artifact_repository":"harbor.example/p/repo","artifact_digest":"sha256:abc","state":"failed",
			"target_count":2,"succeeded_count":1,"failed_count":1,"error_class":"deploy_targets_failed",
			"targets":[
			 {"id":"j1","host_target_id":"t1","host_target_name":"web-1","status":"succeeded","attempt":1,"running_digest":"sha256:abc"},
			 {"id":"j2","host_target_id":"t2","host_target_name":"web-2","status":"rolled_back","attempt":2,
			  "error_class":"health_failed","error_message":"GET /api/health answered 503"}]}`},
	})
	stdout, _, executeError := runDeployCommand(t, newDeploymentsCommand(), "", "get", testDeploymentID)
	if executeError != nil {
		t.Fatalf("deployments get failed: %v", executeError)
	}
	for _, want := range []string{"web-1", "web-2", "rolled_back", "health_failed: GET /api/health answered 503", "2, 1 succeeded, 1 failed"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("detail misses %q:\n%s", want, stdout)
		}
	}
	stdout, _, executeError = runDeployCommand(t, newDeploymentsCommand(), "", "get", testDeploymentID, "-o", "json")
	if executeError != nil {
		t.Fatalf("deployments get -o json failed: %v", executeError)
	}
	var document struct {
		Targets []map[string]any `json:"targets"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || len(document.Targets) != 2 {
		t.Fatalf("JSON = %s (%v)", stdout, decodeError)
	}
}

func TestDeploymentsGetSaysWhenTheTargetsAreTruncated(t *testing.T) {
	newDeployPlatform(t, map[string]deployPlatformAnswer{
		"GET /api/v1/org/deployments/" + testDeploymentID: {body: `{"id":"` + testDeploymentID + `","environment":"production",
			"release_name":"ai-portal","artifact_repository":"harbor.example/p/repo","artifact_digest":"sha256:abc","state":"running",
			"target_count":1500,"succeeded_count":0,"failed_count":0,"targets_truncated":true,
			"targets":[{"id":"j1","host_target_id":"t1","host_target_name":"web-1","status":"queued","attempt":1}]}`},
	})
	_, stderr, executeError := runDeployCommand(t, newDeploymentsCommand(), "", "get", testDeploymentID)
	if executeError != nil || !strings.Contains(stderr, "Showing the first 1 of 1500 host targets") {
		t.Fatalf("truncated targets must be said on stderr: %v\n%s", executeError, stderr)
	}
	stdout, _, executeError := runDeployCommand(t, newDeploymentsCommand(), "", "get", testDeploymentID, "-o", "json")
	var document struct {
		TargetsTruncated bool `json:"targets_truncated"`
	}
	if executeError != nil || json.Unmarshal([]byte(stdout), &document) != nil || !document.TargetsTruncated {
		t.Fatalf("-o json must carry targets_truncated: %s (%v)", stdout, executeError)
	}
}

func TestTargetsAndDeploymentsAreRegisteredOnTheRoot(t *testing.T) {
	for _, path := range [][]string{{"targets", "list"}, {"target", "get"}, {"targets", "join-token", "create"},
		{"targets", "register"}, {"targets", "revoke"}, {"deployments", "list"}, {"deployments", "get"}} {
		found, _, findError := rootCmd.Find(path)
		if findError != nil || found == rootCmd {
			t.Fatalf("%v is not registered: %v", path, findError)
		}
	}
}
