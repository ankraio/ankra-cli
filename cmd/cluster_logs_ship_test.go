package cmd

// `ankra cluster logs-ship status|enable|disable` against a scripted
// platform: the real client talks to an httptest server that answers the
// hosted-logs routes, so the method, path and body each verb sends are
// pinned on the request itself, not on a mock's arguments.

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

const hostedLogsTestChangedAt = "2026-10-05T20:00:00Z"

// hostedLogsOtherClusterID is a cluster reached through --cluster rather
// than the selected one.
const hostedLogsOtherClusterID = "33333333-3333-4333-8333-333333333333"

type hostedLogsRequest struct {
	method        string
	path          string
	authorization string
	body          map[string]any
}

// hostedLogsServer is a platform that serves one cluster's hosted-logs
// switch per id and records every request to it.
type hostedLogsServer struct {
	mutex    sync.Mutex
	states   map[string]*client.ClusterHostedLogs
	requests []hostedLogsRequest
	// refusalStatus, when set, answers every hosted-logs request with it
	// and refusalBody.
	refusalStatus int
	refusalBody   string
}

func newHostedLogsServer(t *testing.T) *hostedLogsServer {
	t.Helper()
	recorder := &hostedLogsServer{states: map[string]*client.ClusterHostedLogs{
		testClusterID:            {ClusterID: testClusterID, Available: true, AgentSupportsSwitch: true},
		hostedLogsOtherClusterID: {ClusterID: hostedLogsOtherClusterID, Available: true, AgentSupportsSwitch: true},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters" {
			// The --cluster <id> lookup.
			_, _ = writer.Write([]byte(`{"result":[{"id":"` + request.URL.Query().Get("cluster_id") +
				`","name":"prod"}],"pagination":{"total_count":1}}`))
			return
		}
		underClusters, hasPrefix := strings.CutPrefix(request.URL.Path, "/api/v1/org/clusters/")
		clusterID, hasSuffix := strings.CutSuffix(underClusters, "/hosted-logs")
		if !hasPrefix || !hasSuffix || strings.Contains(clusterID, "/") {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		recorded := hostedLogsRequest{method: request.Method, path: request.URL.Path,
			authorization: request.Header.Get("Authorization")}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}

		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		state, known := recorder.states[clusterID]
		if !known {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Cluster not found"}`))
			return
		}
		switch request.Method {
		case http.MethodGet:
		case http.MethodPut:
			enabled, isBool := recorded.body["shipping_enabled"].(bool)
			if !isBool {
				writer.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = writer.Write([]byte(`{"detail":"shipping_enabled must be a boolean"}`))
				return
			}
			changedAt := hostedLogsTestChangedAt
			state.ShippingEnabled, state.ChangedAt = enabled, &changedAt
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(writer).Encode(state)
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

func (recorder *hostedLogsServer) seen() []hostedLogsRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]hostedLogsRequest{}, recorder.requests...)
}

func (recorder *hostedLogsServer) setState(clusterID string, mutate func(state *client.ClusterHostedLogs)) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	mutate(recorder.states[clusterID])
}

func (recorder *hostedLogsServer) refuse(status int, body string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.refusalStatus, recorder.refusalBody = status, body
}

// logsShipCommands collects the `cluster logs-ship` tree so a test can put
// its flags back: the commands are built once in init and shared by the
// whole package, so a --yes left set would leak into the next test.
func logsShipCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterCmd.Commands() {
		if candidate.Name() == "logs-ship" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster logs-ship' command is not registered")
	return nil
}

func assertLogsShipOutput(t *testing.T, output string, wants ...string) {
	t.Helper()
	assertContainsAll(t, output, wants)
}

// runLogsShip executes the command with input on stdin and returns stdout
// and stderr apart, so structured output can be checked for being clean.
func runLogsShip(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "logs-ship"}, arguments...))
	resetTreeFlags(t, logsShipCommands(t)...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, logsShipCommands(t)...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestClusterLogsShipStatusPrintsTheSwitch(t *testing.T) {
	recorder := newHostedLogsServer(t)
	changedAt := hostedLogsTestChangedAt
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) {
		state.ShippingEnabled, state.ChangedAt = true, &changedAt
	})

	stdout, _, runError := runLogsShip(t, "", "status")
	if runError != nil {
		t.Fatalf("status failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/org/clusters/"+testClusterID+"/hosted-logs" {
		t.Fatalf("expected one GET of the selected cluster's switch, got %+v", requests)
	}
	if requests[0].authorization != "Bearer test-token" {
		t.Fatalf("the read must use the bearer route, got Authorization %q", requests[0].authorization)
	}
	assertLogsShipOutput(t, stdout,
		"Hosted log shipping for cluster 'test-cluster'",
		"Shipping:       enabled",
		"Hosted logging: available on this platform",
		"Agent:          follows the switch",
		"Last changed:   ",
		"("+hostedLogsTestChangedAt+")",
	)
	if strings.Contains(stdout, "Upgrade the agent") || strings.Contains(stdout, "not turned hosted logging on") {
		t.Fatalf("a switch that takes effect printed a caveat:\n%s", stdout)
	}
}

// A switch that has never been written, on a platform whose store is not
// live, for an agent that cannot follow it: each condition is named, with
// what to do about the agent.
func TestClusterLogsShipStatusNamesWhatKeepsTheSwitchFromTakingEffect(t *testing.T) {
	recorder := newHostedLogsServer(t)
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) {
		state.Available, state.AgentSupportsSwitch = false, false
	})

	stdout, _, runError := runLogsShip(t, "", "status")
	if runError != nil {
		t.Fatalf("status failed: %v", runError)
	}
	assertLogsShipOutput(t, stdout,
		"Shipping:       disabled",
		"Hosted logging: not yet available on this platform",
		"Agent:          too old to follow the switch (upgrade the agent)",
		"Last changed:   never",
		hostedLogsUnavailableSentence,
		"Upgrade the agent with 'ankra cluster agent upgrade'",
		"Turn it on with 'ankra cluster logs-ship enable'",
	)
}

func TestClusterLogsShipStatusJSONIsTheResponseBody(t *testing.T) {
	recorder := newHostedLogsServer(t)
	changedAt := hostedLogsTestChangedAt
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) {
		state.ShippingEnabled, state.AgentSupportsSwitch, state.ChangedAt = true, false, &changedAt
	})

	stdout, _, runError := runLogsShip(t, "", "status", "-o", "json")
	if runError != nil {
		t.Fatalf("status -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	want := map[string]any{
		"cluster_id":            testClusterID,
		"shipping_enabled":      true,
		"available":             true,
		"agent_supports_switch": false,
		"changed_at":            hostedLogsTestChangedAt,
	}
	if len(decoded) != len(want) {
		t.Fatalf("decoded %v, want exactly the response fields %v", decoded, want)
	}
	for key, value := range want {
		if decoded[key] != value {
			t.Errorf("%s = %v, want %v", key, decoded[key], value)
		}
	}

	// A never-written switch keeps changed_at as null rather than dropping it.
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) { state.ChangedAt = nil })
	stdout, _, runError = runLogsShip(t, "", "status", "-o", "json")
	if runError != nil {
		t.Fatalf("status -o json failed: %v", runError)
	}
	if !strings.Contains(stdout, `"changed_at": null`) {
		t.Fatalf("changed_at must stay in the body as null:\n%s", stdout)
	}
}

func TestClusterLogsShipStatusTargetsTheClusterFlag(t *testing.T) {
	recorder := newHostedLogsServer(t)

	stdout, _, runError := runLogsShip(t, "", "status", "--cluster", hostedLogsOtherClusterID)
	if runError != nil {
		t.Fatalf("status --cluster failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].path != "/api/v1/org/clusters/"+hostedLogsOtherClusterID+"/hosted-logs" {
		t.Fatalf("--cluster must override the selected cluster, got %+v", requests)
	}
	assertLogsShipOutput(t, stdout, "Hosted log shipping for cluster 'prod'")
}

func TestClusterLogsShipEnableWithYesExplainsAndTurnsItOn(t *testing.T) {
	recorder := newHostedLogsServer(t)

	stdout, stderr, runError := runLogsShip(t, "", "enable", "--yes")
	if runError != nil {
		t.Fatalf("enable --yes failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodPut ||
		requests[0].path != "/api/v1/org/clusters/"+testClusterID+"/hosted-logs" {
		t.Fatalf("expected one PUT of the selected cluster's switch, got %+v", requests)
	}
	if len(requests[0].body) != 1 || requests[0].body["shipping_enabled"] != true {
		t.Fatalf("body = %v, want exactly {\"shipping_enabled\": true}", requests[0].body)
	}
	// What is sent, where, how long, who reads it and how to stop it,
	// even when the question itself is skipped.
	assertLogsShipOutput(t, stderr,
		"log lines of every running container in cluster 'test-cluster'",
		"Ankra's hosted log store",
		"kept for 7 days",
		"members of your organisation",
		"'ankra cluster logs-ship disable'",
	)
	if strings.Contains(stderr, "[y/N]") {
		t.Fatalf("--yes must skip the question:\n%s", stderr)
	}
	assertLogsShipOutput(t, stdout,
		"Hosted log shipping enabled for cluster 'test-cluster'",
		"Shipping:       enabled",
		"The agent starts sending log lines at its next check-in",
	)
}

func TestClusterLogsShipEnableWithoutYesIsRefusedWithoutATerminal(t *testing.T) {
	recorder := newHostedLogsServer(t)

	_, stderr, runError := runLogsShip(t, "y\n", "enable")
	if runError == nil {
		t.Fatal("enable without --yes and without a terminal must be refused")
	}
	if code := exitCodeFor(runError); code != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", code, exitUsage)
	}
	if !strings.Contains(runError.Error(), "re-run with --yes to confirm") {
		t.Errorf("the refusal must say how to confirm: %v", runError)
	}
	// It still says what enabling would do.
	assertLogsShipOutput(t, stderr, "kept for 7 days")
	for _, request := range recorder.seen() {
		if request.method != http.MethodGet {
			t.Fatalf("a refused enable still wrote the switch: %+v", request)
		}
	}
}

func TestClusterLogsShipEnableAsksOnATerminal(t *testing.T) {
	t.Run("yes turns it on", func(t *testing.T) {
		recorder := newHostedLogsServer(t)
		answerPromptInteractively(t)

		stdout, stderr, runError := runLogsShip(t, "y\n", "enable")
		if runError != nil {
			t.Fatalf("a confirmed enable failed: %v", runError)
		}
		assertLogsShipOutput(t, stderr, "kept for 7 days", "Ship this cluster's logs to Ankra? [y/N]")
		if requests := recorder.seen(); len(requests) != 1 || requests[0].body["shipping_enabled"] != true {
			t.Fatalf("a confirmed enable must write the switch on once, got %+v", requests)
		}
		assertLogsShipOutput(t, stdout, "Hosted log shipping enabled for cluster 'test-cluster'")
	})

	t.Run("anything else cancels", func(t *testing.T) {
		recorder := newHostedLogsServer(t)
		answerPromptInteractively(t)

		stdout, _, runError := runLogsShip(t, "n\n", "enable")
		if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
			t.Fatalf("a declined enable must return errCancelled (exit 4), got %v", runError)
		}
		if requests := recorder.seen(); len(requests) != 0 {
			t.Fatalf("a declined enable wrote the switch: %+v", requests)
		}
		if strings.Contains(stdout, "enabled for cluster") {
			t.Fatalf("a declined enable printed success:\n%s", stdout)
		}
	})
}

// The platform stores the switch while its hosted store is not live; the
// command says nothing ships until Ankra turns hosted logging on, and names
// an agent that cannot follow the switch.
func TestClusterLogsShipEnableSaysWhenNothingShipsYet(t *testing.T) {
	recorder := newHostedLogsServer(t)
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) {
		state.Available, state.AgentSupportsSwitch = false, false
	})

	stdout, _, runError := runLogsShip(t, "", "enable", "-y")
	if runError != nil {
		t.Fatalf("enable -y failed: %v", runError)
	}
	assertLogsShipOutput(t, stdout,
		"Hosted log shipping enabled for cluster 'test-cluster'",
		"the switch is stored, but nothing ships until it does",
		"Upgrade the agent with 'ankra cluster agent upgrade'",
	)
	if strings.Contains(stdout, "starts sending log lines") {
		t.Fatalf("an enable that ships nothing yet claimed the agent starts sending:\n%s", stdout)
	}
}

func TestClusterLogsShipEnableJSONStaysParseable(t *testing.T) {
	newHostedLogsServer(t)

	stdout, stderr, runError := runLogsShip(t, "", "enable", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("enable -o json failed: %v", runError)
	}
	var decoded client.ClusterHostedLogs
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON (the explanation belongs on stderr): %v\n%s", decodeError, stdout)
	}
	if !decoded.ShippingEnabled || decoded.ChangedAt == nil || *decoded.ChangedAt != hostedLogsTestChangedAt {
		t.Fatalf("decoded %+v, want the stored state", decoded)
	}
	assertLogsShipOutput(t, stderr, "kept for 7 days")
}

func TestClusterLogsShipDisableTurnsItOffWithoutAsking(t *testing.T) {
	recorder := newHostedLogsServer(t)
	recorder.setState(testClusterID, func(state *client.ClusterHostedLogs) { state.ShippingEnabled = true })

	stdout, stderr, runError := runLogsShip(t, "", "disable")
	if runError != nil {
		t.Fatalf("disable failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodPut || len(requests[0].body) != 1 ||
		requests[0].body["shipping_enabled"] != false {
		t.Fatalf("expected one PUT of {\"shipping_enabled\": false}, got %+v", requests)
	}
	if stderr != "" {
		t.Fatalf("disable must not explain or ask:\n%s", stderr)
	}
	assertLogsShipOutput(t, stdout,
		"Hosted log shipping disabled for cluster 'test-cluster'",
		"Shipping:       disabled",
		"The agent stops sending log lines at its next check-in",
	)
}

func TestClusterLogsShipDisableJSONIsTheResponseBody(t *testing.T) {
	newHostedLogsServer(t)

	stdout, _, runError := runLogsShip(t, "", "disable", "-o", "json")
	if runError != nil {
		t.Fatalf("disable -o json failed: %v", runError)
	}
	var decoded client.ClusterHostedLogs
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	if decoded.ShippingEnabled || decoded.ClusterID != testClusterID {
		t.Fatalf("decoded %+v, want the cluster's switch off", decoded)
	}
}

func TestClusterLogsShipRefusals(t *testing.T) {
	cases := []struct {
		name          string
		arguments     []string
		status        int
		body          string
		wantExit      int
		wantMessage   string
		forbidMessage string
	}{
		{
			name:        "RBAC 403 on a write names clusters.write",
			arguments:   []string{"enable", "--yes"},
			status:      http.StatusForbidden,
			body:        `{"detail":"permission_denied","permission":"clusters.write"}`,
			wantExit:    exitForbidden,
			wantMessage: "you need permission to change cluster settings (clusters.write)",
		},
		{
			name:        "a plain 403 on a write is the same refusal",
			arguments:   []string{"disable"},
			status:      http.StatusForbidden,
			body:        `{"detail":"Forbidden"}`,
			wantExit:    exitForbidden,
			wantMessage: "you need permission to change cluster settings (clusters.write)",
		},
		{
			name:          "a 403 on a read keeps the platform's refusal",
			arguments:     []string{"status"},
			status:        http.StatusForbidden,
			body:          `{"detail":"permission_denied","permission":"clusters.read"}`,
			wantExit:      exitForbidden,
			wantMessage:   `requires the "clusters.read" permission`,
			forbidMessage: "clusters.write",
		},
		{
			name:        "the platform's 404 is the cluster",
			arguments:   []string{"status"},
			status:      http.StatusNotFound,
			body:        `{"detail":"Cluster not found"}`,
			wantExit:    exitNotFound,
			wantMessage: "cluster 'test-cluster' (" + testClusterID + ") not found in this organisation",
		},
		{
			name:          "the router's 404 is a platform without the switch",
			arguments:     []string{"disable"},
			status:        http.StatusNotFound,
			body:          `{"detail":"Not Found"}`,
			wantExit:      exitError,
			wantMessage:   "this Ankra platform does not offer hosted log shipping yet",
			forbidMessage: "not found in this organisation",
		},
		{
			name:        "any other refusal keeps the platform's detail",
			arguments:   []string{"enable", "--yes"},
			status:      http.StatusUnprocessableEntity,
			body:        `{"detail":"shipping_enabled must be a boolean"}`,
			wantExit:    exitError,
			wantMessage: "changing hosted log shipping for cluster 'test-cluster': shipping_enabled must be a boolean",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newHostedLogsServer(t)
			recorder.refuse(testCase.status, testCase.body)

			stdout, _, runError := runLogsShip(t, "", testCase.arguments...)
			if runError == nil {
				t.Fatalf("expected the refusal, got success:\n%s", stdout)
			}
			if code := exitCodeFor(runError); code != testCase.wantExit {
				t.Errorf("exit code = %d, want %d (%v)", code, testCase.wantExit, runError)
			}
			if !strings.Contains(runError.Error(), testCase.wantMessage) {
				t.Errorf("error = %q, want it to contain %q", runError.Error(), testCase.wantMessage)
			}
			if testCase.forbidMessage != "" && strings.Contains(runError.Error(), testCase.forbidMessage) {
				t.Errorf("error = %q, must not contain %q", runError.Error(), testCase.forbidMessage)
			}
			if strings.Contains(stdout, "Hosted log shipping") {
				t.Errorf("a refused request printed a state:\n%s", stdout)
			}
		})
	}
}

func TestClusterLogsShipVerbsTakeNoArguments(t *testing.T) {
	for _, verb := range []string{"status", "enable", "disable"} {
		recorder := newHostedLogsServer(t)
		_, _, runError := runLogsShip(t, "", verb, "prod")
		if runError == nil {
			t.Fatalf("%s: expected a usage error for a positional argument", verb)
		}
		if requests := recorder.seen(); len(requests) != 0 {
			t.Fatalf("%s: a usage error still reached the platform: %+v", verb, requests)
		}
	}
}
