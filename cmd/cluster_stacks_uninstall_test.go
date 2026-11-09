package cmd

// `ankra cluster stacks uninstall|install` against a scripted platform: the
// real client talks to an httptest server, so the method, path, token and
// body each call sends are pinned on the request itself.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// stackInstallationTargetClusterID is the cluster --cluster names, apart from
// the selected one (testClusterID), so a test can tell which one was used.
const stackInstallationTargetClusterID = "8c1f0b7e-5d2a-4c3b-9e61-2f4a7d9b0c15"

type stackInstallationRequest struct {
	method        string
	path          string
	authorization string
	body          string
}

type stackInstallationServer struct {
	mutex    sync.Mutex
	status   int
	body     string
	requests []stackInstallationRequest
}

// newStackInstallationServer answers the --cluster <id> lookup as cluster
// "prod", and every other request with status and body, recording it.
func newStackInstallationServer(t *testing.T, status int, body string) *stackInstallationServer {
	t.Helper()
	recorder := &stackInstallationServer{status: status, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters" {
			_, _ = writer.Write([]byte(`{"result":[{"id":"` + request.URL.Query().Get("cluster_id") +
				`","name":"prod"}],"pagination":{"total_count":1}}`))
			return
		}
		raw, _ := io.ReadAll(request.Body)
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, stackInstallationRequest{
			method: request.Method, path: request.URL.EscapedPath(),
			authorization: request.Header.Get("Authorization"), body: string(raw),
		})
		status, responseBody := recorder.status, recorder.body
		recorder.mutex.Unlock()
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func (recorder *stackInstallationServer) seen() []stackInstallationRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]stackInstallationRequest{}, recorder.requests...)
}

// runStackInstallation runs `ankra cluster stacks <arguments>` with input on
// stdin; a prompt is only answered when isInteractive says a terminal is there
// to ask on.
func runStackInstallation(t *testing.T, input string, isInteractive bool, arguments ...string) (string, string, error) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return isInteractive }
	t.Cleanup(func() { promptIsInteractive = previous })

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "stacks"}, arguments...))
	commands := []*cobra.Command{clusterStacksUninstallCmd, clusterStacksInstallCmd}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, commands...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func stackInstallationPath(clusterID, verb string) string {
	return "/api/v1/org/clusters/imported/" + clusterID + "/stacks/web/" + verb
}

// confirmedStackInstallationArgs runs verb on stack web on the --cluster
// target, answering uninstall's question with --yes; install asks nothing and
// has no --yes.
func confirmedStackInstallationArgs(verb string, extra ...string) []string {
	arguments := []string{verb, "web", "--cluster", stackInstallationTargetClusterID}
	if verb == "uninstall" {
		arguments = append(arguments, "--yes")
	}
	return append(arguments, extra...)
}

const (
	stackUninstalledBody = `{"stack_name":"web","installed":false,"resource_ids":["addon-1","manifest-2"]}`
	stackInstalledBody   = `{"stack_name":"web","installed":true,"resource_ids":["addon-1","manifest-2"]}`
)

func TestStackUninstallNeedsTheClusterNamedAndSendsNothing(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackUninstalledBody)
	_, _, runError := runStackInstallation(t, "y\n", true, "uninstall", "web", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("error = %v (exit %d), want the usage exit", runError, exitCodeFor(runError))
	}
	for _, want := range []string{"--cluster", "ankra cluster select", "Nothing was changed"} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, runError)
		}
	}
	if requests := recorder.seen(); len(requests) != 0 {
		t.Fatalf("an uninstall on the selected cluster reached the platform: %+v", requests)
	}
}

func TestStackUninstallWithoutATerminalNeedsYesAndSendsNothing(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackUninstalledBody)
	_, stderr, runError := runStackInstallation(t, "", false, "uninstall", "web", "--cluster", stackInstallationTargetClusterID)
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "re-run with --yes") {
		t.Fatalf("error = %v (exit %d), want the usage exit naming --yes", runError, exitCodeFor(runError))
	}
	if requests := recorder.seen(); len(requests) != 0 {
		t.Fatalf("an unconfirmed uninstall reached the platform: %+v", requests)
	}
	for _, want := range []string{
		"takes every add-on, manifest and application in it off the cluster",
		"Data is treated exactly as a delete treats it",
		"ankra cluster stacks data list web",
		"ankra cluster stacks restore-points create web",
		"stays declared in Ankra and in Git",
		"ankra cluster stacks install web",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the consequence was not stated before refusing, missing %q:\n%s", want, stderr)
		}
	}
}

func TestStackUninstallDeclinedOnATerminalSendsNothing(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackUninstalledBody)
	_, stderr, runError := runStackInstallation(t, "n\n", true, "uninstall", "web", "--cluster", stackInstallationTargetClusterID)
	if runError == nil || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("error = %v (exit %d), want the declined exit %d", runError, exitCodeFor(runError), exitCancelled)
	}
	if requests := recorder.seen(); len(requests) != 0 {
		t.Fatalf("a declined uninstall reached the platform: %+v", requests)
	}
	for _, want := range []string{"Uninstall stack 'web' from cluster 'prod'? [y/N]", "Nothing was changed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q:\n%s", want, stderr)
		}
	}
}

func TestStackUninstallConfirmedOnATerminalSendsTheBearerPost(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackUninstalledBody)
	_, _, runError := runStackInstallation(t, "y\n", true, "uninstall", "web", "--cluster", stackInstallationTargetClusterID)
	if runError != nil {
		t.Fatalf("a confirmed uninstall failed: %v", runError)
	}
	requests := recorder.seen()
	want := stackInstallationRequest{method: http.MethodPost,
		path: stackInstallationPath(stackInstallationTargetClusterID, "uninstall"), authorization: "Bearer test-token"}
	if len(requests) != 1 || requests[0] != want {
		t.Fatalf("requests = %+v, want exactly %+v", requests, want)
	}
}

func TestStackUninstallWithYesPrintsWhatItStarted(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackUninstalledBody)
	stdout, stderr, runError := runStackInstallation(t, "", false, "uninstall", "web", "--cluster", stackInstallationTargetClusterID, "--yes")
	if runError != nil {
		t.Fatalf("uninstall --yes failed: %v", runError)
	}
	if requests := recorder.seen(); len(requests) != 1 || requests[0].path != stackInstallationPath(stackInstallationTargetClusterID, "uninstall") {
		t.Fatalf("requests = %+v", requests)
	}
	for _, want := range []string{
		"Stack 'web' is uninstalled on cluster 'prod'. Ankra is taking its members off the cluster now.",
		"Members being taken off: 2", "- addon-1", "- manifest-2",
		"stays declared in Ankra and in Git",
		"'ankra cluster stacks list web --cluster prod'",
		"'ankra cluster stacks install web --cluster prod' deploys the stack again",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not say %q:\n%s", want, stdout)
		}
	}
	// --yes answers the question; it does not hide what the command does.
	for _, want := range []string{"Data is treated exactly as a delete treats it", "Uninstalling stack 'web' on cluster 'prod'..."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("--yes must not ask:\n%s", stderr)
	}
}

func TestStackUninstallJSONIsThePlatformsDocument(t *testing.T) {
	document := `{"stack_name":"web","installed":false,"resource_ids":[],"a_member_this_cli_does_not_know":1}`
	newStackInstallationServer(t, http.StatusOK, document)
	stdout, stderr, runError := runStackInstallation(t, "", false, "uninstall", "web", "--cluster", stackInstallationTargetClusterID, "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("uninstall -o json failed: %v", runError)
	}
	var printed, sent map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &printed); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	_ = json.Unmarshal([]byte(document), &sent)
	if !jsonDocumentsEqual(printed, sent) {
		t.Fatalf("stdout = %s, want the platform's document %s", stdout, document)
	}
	if strings.Contains(stderr, "Uninstalling stack") {
		t.Errorf("structured output prints no progress line:\n%s", stderr)
	}
}

func TestStackInstallUsesTheSelectedClusterAndNeverAsks(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackInstalledBody)
	stdout, stderr, runError := runStackInstallation(t, "", true, "install", "web")
	if runError != nil {
		t.Fatalf("install failed: %v", runError)
	}
	requests := recorder.seen()
	want := stackInstallationRequest{method: http.MethodPost, path: stackInstallationPath(testClusterID, "install"),
		authorization: "Bearer test-token"}
	if len(requests) != 1 || requests[0] != want {
		t.Fatalf("requests = %+v, want exactly %+v", requests, want)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("install must not ask:\n%s", stderr)
	}
	for _, want := range []string{
		"Stack 'web' is installed again on cluster 'test-cluster'. Ankra is deploying its members now.",
		"Members being deployed: 2", "- addon-1",
		"'ankra cluster stacks list web --cluster test-cluster' shows each member's state",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not say %q:\n%s", want, stdout)
		}
	}
}

func TestStackInstallHonoursTheClusterFlagAndPrintsJSON(t *testing.T) {
	recorder := newStackInstallationServer(t, http.StatusOK, stackInstalledBody)
	stdout, _, runError := runStackInstallation(t, "", false, "install", "web", "--cluster", stackInstallationTargetClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("install -o json failed: %v", runError)
	}
	if requests := recorder.seen(); len(requests) != 1 || requests[0].path != stackInstallationPath(stackInstallationTargetClusterID, "install") {
		t.Fatalf("requests = %+v", requests)
	}
	var printed, sent map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &printed); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	_ = json.Unmarshal([]byte(stackInstalledBody), &sent)
	if !jsonDocumentsEqual(printed, sent) {
		t.Fatalf("stdout = %s, want %s", stdout, stackInstalledBody)
	}
}

func TestStackInstallationWithNothingToMoveSaysSo(t *testing.T) {
	for verb, body := range map[string]string{
		"uninstall": `{"stack_name":"web","installed":false,"resource_ids":[]}`,
		"install":   `{"stack_name":"web","installed":true,"resource_ids":[]}`,
	} {
		t.Run(verb, func(t *testing.T) {
			newStackInstallationServer(t, http.StatusOK, body)
			stdout, _, runError := runStackInstallation(t, "", false, confirmedStackInstallationArgs(verb)...)
			if runError != nil {
				t.Fatalf("%s failed: %v", verb, runError)
			}
			want := map[string]string{
				"uninstall": "No member was left to take off the cluster.",
				"install":   "No member needed to be deployed.",
			}[verb]
			if !strings.Contains(stdout, want) || strings.Contains(stdout, "Members being") {
				t.Fatalf("stdout does not say %q:\n%s", want, stdout)
			}
		})
	}
}

// TestStackInstallationRefusalsPrintTheSentenceAndExitBlocked pins every 409
// the routes document: nothing was changed, the platform's sentence is printed
// as it came, and the exit is 8 whatever the error_code.
func TestStackInstallationRefusalsPrintTheSentenceAndExitBlocked(t *testing.T) {
	refusals := map[string]string{
		"uninstall in progress": `{"detail":"Stack 'web' is still being uninstalled: wait until its members are off the cluster.","error_code":"STACK_UNINSTALL_IN_PROGRESS"}`,
		"state refusal":         `{"detail":"Stack 'web' has an open draft. Deploy or discard the draft, then uninstall the stack.","error_code":"stack_installation_refused"}`,
		"cluster offline":       `{"detail":"Cluster has to be online to uninstall a stack.","error_code":"cluster_unavailable"}`,
		"service-owned":         `{"detail":"Stack 'web' belongs to the managed service 'db'. Retire the service instead."}`,
	}
	for _, verb := range []string{"uninstall", "install"} {
		for name, body := range refusals {
			t.Run(verb+"/"+name, func(t *testing.T) {
				newStackInstallationServer(t, http.StatusConflict, body)
				stdout, _, runError := runStackInstallation(t, "", false, confirmedStackInstallationArgs(verb, "-o", "json")...)
				if runError == nil || exitCodeFor(runError) != exitBlocked {
					t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitBlocked)
				}
				var envelope struct {
					Detail string `json:"detail"`
				}
				_ = json.Unmarshal([]byte(body), &envelope)
				for _, want := range []string{envelope.Detail, "nothing was changed", "refused to " + verb + " stack 'web' on cluster 'prod'"} {
					if !strings.Contains(runError.Error(), want) {
						t.Errorf("the error does not say %q: %v", want, runError)
					}
				}
				if stdout != "" {
					t.Errorf("a refusal printed on stdout: %q", stdout)
				}
			})
		}
	}
}

func TestStackInstallationForbiddenExitsSevenNamingThePermission(t *testing.T) {
	for verb, permission := range map[string]string{"uninstall": "stacks.delete", "install": "stacks.deploy"} {
		t.Run(verb, func(t *testing.T) {
			newStackInstallationServer(t, http.StatusForbidden,
				`{"detail":"permission_denied","permission":"`+permission+`","scope_type":"cluster"}`)
			_, _, runError := runStackInstallation(t, "", false, confirmedStackInstallationArgs(verb)...)
			if runError == nil || exitCodeFor(runError) != exitForbidden || !strings.Contains(runError.Error(), permission) {
				t.Fatalf("error = %v (exit %d), want exit %d naming %s", runError, exitCodeFor(runError), exitForbidden, permission)
			}
		})
	}
}

// TestStackInstallationNotFoundKeepsTheRouteAbsenceApart: only the stack's
// and the cluster's own 404 sentences exit 3. A platform without the route
// (its router's "Not Found") or a 404 with no reason says nothing about the
// stack and exits 1.
func TestStackInstallationNotFoundKeepsTheRouteAbsenceApart(t *testing.T) {
	cases := []struct {
		name string
		body string
		exit int
		want string
	}{
		{"stack missing", `{"detail":"Stack 'web' could not be found in this cluster."}`, exitNotFound, "stack 'web' not found on cluster 'prod'"},
		{"cluster missing", `{"detail":"Cluster not found"}`, exitNotFound, "not found in this organisation"},
		{"route absent", `{"detail":"Not Found"}`, exitError, "does not offer stack"},
		{"no reason", ``, exitError, "without saying why"},
	}
	for _, verb := range []string{"uninstall", "install"} {
		for _, testCase := range cases {
			t.Run(verb+"/"+testCase.name, func(t *testing.T) {
				newStackInstallationServer(t, http.StatusNotFound, testCase.body)
				_, _, runError := runStackInstallation(t, "", false, confirmedStackInstallationArgs(verb)...)
				if runError == nil || exitCodeFor(runError) != testCase.exit || !strings.Contains(runError.Error(), testCase.want) {
					t.Fatalf("error = %v (exit %d), want exit %d saying %q", runError, exitCodeFor(runError), testCase.exit, testCase.want)
				}
			})
		}
	}
}

// TestClusterStacksListSaysAStackIsUninstalled: an uninstalled stack's own
// state still reads up while its members are off the cluster, so the listing
// must not show it as running, and the detail view says how to bring it back.
func TestClusterStacksListSaysAStackIsUninstalled(t *testing.T) {
	resetTreeFlags(t, clusterStacksListCmd)
	t.Cleanup(func() { resetTreeFlags(t, clusterStacksListCmd) })
	writeSelectedClusterJSON(t)
	setMockClient(t, &clusterStacksListMock{stacks: []client.ClusterStackListItem{
		{Name: "web", State: "up", Uninstalled: true},
		{Name: "db", State: "up"},
	}})

	table := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "stacks", "list")
	})
	var webRow, dbRow string
	for _, line := range strings.Split(stripANSICodes(table), "\n") {
		switch {
		case strings.Contains(line, " web "):
			webRow = line
		case strings.Contains(line, " db "):
			dbRow = line
		}
	}
	if !strings.Contains(webRow, "uninstalled") || strings.Contains(webRow, "✓") {
		t.Errorf("the uninstalled stack's row = %q, want it marked uninstalled and not up:\n%s", webRow, table)
	}
	if !strings.Contains(dbRow, "✓ up") || strings.Contains(dbRow, "uninstalled") {
		t.Errorf("the installed stack's row = %q, want it up as before:\n%s", dbRow, table)
	}

	detail := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "stacks", "list", "web")
	})
	if !strings.Contains(detail, "Uninstalled:  yes") || !strings.Contains(detail, "'ankra cluster stacks install web'") {
		t.Errorf("the detail view does not say the stack is uninstalled:\n%s", detail)
	}
	installedDetail := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "stacks", "list", "db")
	})
	if strings.Contains(installedDetail, "Uninstalled") {
		t.Errorf("an installed stack's detail view changed:\n%s", installedDetail)
	}
}

// jsonDocumentsEqual compares two decoded JSON documents.
func jsonDocumentsEqual(left, right map[string]any) bool {
	leftEncoded, leftError := json.Marshal(left)
	rightEncoded, rightError := json.Marshal(right)
	return leftError == nil && rightError == nil && bytes.Equal(leftEncoded, rightEncoded)
}
