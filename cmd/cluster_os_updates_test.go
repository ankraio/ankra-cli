package cmd

// `ankra cluster os-updates get|set|plan|run` against a scripted platform:
// the real client talks to an httptest server that answers the cluster
// lookup with a kind and the os-patches routes under that kind, so the
// provider each command resolves and the method, path and body it sends are
// pinned on the request itself.

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

const osUpdatesOtherClusterID = "55555555-5555-4555-8555-555555555555"

type osUpdatesRequest struct {
	method string
	path   string
	body   string
}

// osUpdatesAnswer is one scripted answer of an os-patches route.
type osUpdatesAnswer struct {
	status int
	body   string
}

// osUpdatesServer lists every cluster with one kind and answers the
// os-patches routes from answers keyed by method and route suffix ("" for
// the status read, "plan", "run", "policy").
type osUpdatesServer struct {
	mutex    sync.Mutex
	kind     string
	answers  map[string]osUpdatesAnswer
	requests []osUpdatesRequest
}

func newOSUpdatesServer(t *testing.T, kind string) *osUpdatesServer {
	t.Helper()
	recorder := &osUpdatesServer{kind: kind, answers: map[string]osUpdatesAnswer{
		http.MethodGet + " ": {status: http.StatusOK, body: osUpdatesStatusNoticePeriod},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters" {
			recorder.mutex.Lock()
			listedKind := recorder.kind
			recorder.mutex.Unlock()
			_, _ = writer.Write([]byte(`{"result":[{"id":"` + request.URL.Query().Get("cluster_id") +
				`","name":"test-cluster","kind":"` + listedKind + `"}],"pagination":{"total_count":1}}`))
			return
		}
		raw, _ := io.ReadAll(request.Body)
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, osUpdatesRequest{method: request.Method, path: request.URL.Path, body: string(raw)})
		_, afterRoute, found := strings.Cut(request.URL.Path, "/os-patches")
		if !found {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		answer, scripted := recorder.answers[request.Method+" "+strings.TrimPrefix(afterRoute, "/")]
		if !scripted {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"detail":"no answer scripted in test"}`))
			return
		}
		writer.WriteHeader(answer.status)
		_, _ = writer.Write([]byte(answer.body))
	}))
	t.Cleanup(server.Close)

	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func (recorder *osUpdatesServer) answer(method string, suffix string, status int, body string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.answers[method+" "+suffix] = osUpdatesAnswer{status: status, body: body}
}

func (recorder *osUpdatesServer) seen() []osUpdatesRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]osUpdatesRequest{}, recorder.requests...)
}

// osUpdatesCommands collects the `cluster os-updates` tree so a test can put
// its flags back: the commands are built once in init and shared.
func osUpdatesCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterCmd.Commands() {
		if candidate.Name() == "os-updates" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster os-updates' command is not registered")
	return nil
}

func runOSUpdates(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "os-updates"}, arguments...))
	resetTreeFlags(t, osUpdatesCommands(t)...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, osUpdatesCommands(t)...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func assertOSUpdatesExit(t *testing.T, runError error, want int) {
	t.Helper()
	if runError == nil {
		t.Fatalf("expected exit %d, the command succeeded", want)
	}
	if got := exitCodeFor(runError); got != want {
		t.Fatalf("exit code %d, want %d (error: %v)", got, want, runError)
	}
}

func assertOSUpdatesBody(t *testing.T, request osUpdatesRequest, want map[string]any) {
	t.Helper()
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(request.body), &decoded); decodeError != nil {
		t.Fatalf("request body is not JSON: %v\n%s", decodeError, request.body)
	}
	wantEncoded, _ := json.Marshal(want)
	gotEncoded, _ := json.Marshal(decoded)
	if string(wantEncoded) != string(gotEncoded) {
		t.Fatalf("request body %s, want %s", gotEncoded, wantEncoded)
	}
}

const osUpdatesPolicyNoticePeriod = `{"enabled":false,"chosen_mode":null,"node_group_modes":{"batch":"automatic"},
"window":{"days":["mon","tue","wed","thu"],"start_hour_utc":2,"duration_hours":4},"cadence_days":7,
"include_stateful_nodes":false,"security_lag_days":14,"mode":"advisory","mode_source":"notice_period",
"default_mode":"automatic","mode_reason":"OS updates become automatic on 2026-11-29 (the organisation was told 14 days ahead).",
"automatic_from":"2026-11-29T22:18:00Z","automatic_notice_sent_at":"2026-11-15T22:18:00Z","stored":false,
"window_description":"mon, tue, wed, thu 02:00-06:00 UTC","next_window_opens_utc":"2026-11-23T02:00:00Z","window_open_now":false}`

var osUpdatesStatusNoticePeriod = `{"policy":` + osUpdatesPolicyNoticePeriod + `,
"security_lag":{"state":"none","days_pending":0,"threshold_days":14,"message":"No held-back security update is pending."},
"last_plan":{"hosts":3,"hosts_with_work":2,"hosts_unknown":0,"pending":12,"security_pending":4,"held_back_pending":2,
"held_back_security_pending":1,"reboot_required":1,"observed_at":"2026-11-20T03:00:00Z"},"last_plan_at":"2026-11-20T03:00:00Z",
"latest_run":{"operation_id":"run-2","status":"running","trigger":"manual"},
"last_run_record":{"operation_id":"run-1","trigger":"window","status":"success","started_at":"2026-11-10T02:05:00Z"},
"last_successful_run_at":"2026-11-10T02:40:00Z"}`

func osUpdatesStatusWith(replacements ...string) string {
	return strings.NewReplacer(replacements...).Replace(osUpdatesStatusNoticePeriod)
}

func TestOSUpdatesGetReadsTheSelectedClusterOnItsOwnProvider(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")

	stdout, _, runError := runOSUpdates(t, "", "get")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/clusters/hetzner/"+testClusterID+"/os-patches" {
		t.Fatalf("expected one GET of the hetzner os-patches route, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{
		"OS updates for cluster test-cluster (hetzner):",
		"Mode:           advisory until 2026-11-29 22:18 UTC, then automatic (notice period)",
		"Why:            OS updates become automatic on 2026-11-29",
		"Default:        automatic, from the cluster's environment; nobody chose a mode",
		"Window:         mon, tue, wed, thu 02:00-06:00 UTC (closed; opens 2026-11-23 02:00 UTC)",
		"Cadence:        at most one automatic run every 7 days",
		"Stateful nodes: left out of runs",
		"Security lag:   none: No held-back security update is pending.",
		"Last dry run:   2026-11-20 03:00 UTC: 2 of 3 hosts have updates, 12 pending (4 security), 1 would reboot",
		"Running now:    running, operation run-2",
		"Last run:       success, started by window at 2026-11-10 02:05 UTC (operation run-1)",
		"Last success:   2026-11-10 02:40 UTC",
		"batch", "automatic (its own)",
		"Every other node group, the control planes and the bastion follow the cluster (advisory).",
		"To keep them advisory: ankra cluster os-updates set test-cluster --mode advisory",
	})
}

func TestOSUpdatesGetChosenAdvisoryOffersTheRunAndTheSwitch(t *testing.T) {
	recorder := newOSUpdatesServer(t, "ovh")
	recorder.answer(http.MethodGet, "", http.StatusOK, osUpdatesStatusWith(
		`"mode_source":"notice_period"`, `"mode_source":"chosen"`,
		`"chosen_mode":null`, `"chosen_mode":"advisory"`,
		`"node_group_modes":{"batch":"automatic"}`, `"node_group_modes":{}`,
		`"window_open_now":false`, `"window_open_now":true`,
		`"include_stateful_nodes":false`, `"include_stateful_nodes":true`,
		`"automatic_from":"2026-11-29T22:18:00Z",`, ``,
		`"status":"running"`, `"status":"success"`))

	stdout, _, runError := runOSUpdates(t, "", "get", "--cluster", osUpdatesOtherClusterID)
	if runError != nil {
		t.Fatalf("get --cluster failed: %v", runError)
	}
	if requests := recorder.seen(); len(requests) != 1 ||
		requests[0].path != "/api/v1/clusters/ovh/"+osUpdatesOtherClusterID+"/os-patches" {
		t.Fatalf("expected the --cluster id on the ovh route, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{
		"Mode:           advisory (chosen for this cluster)",
		"(open now)",
		"Stateful nodes: drained by runs",
		"Every node group follows the cluster (advisory).",
		"Start the rolling run: ankra cluster os-updates run " + osUpdatesOtherClusterID,
		"Make them automatic:   ankra cluster os-updates set " + osUpdatesOtherClusterID + " --mode automatic",
	})
	for _, absent := range []string{"Default:", "Automatic from:", "Running now:"} {
		if strings.Contains(stdout, absent) {
			t.Fatalf("output has %q for a chosen mode with no run in flight:\n%s", absent, stdout)
		}
	}
}

func TestOSUpdatesGetJSONIsTheResponseBody(t *testing.T) {
	newOSUpdatesServer(t, "ankracloud")

	stdout, _, runError := runOSUpdates(t, "", "get", testClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("get -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	policy, _ := decoded["policy"].(map[string]any)
	if policy["mode"] != "advisory" || policy["automatic_from"] != "2026-11-29T22:18:00Z" || policy["mode_source"] != "notice_period" {
		t.Fatalf("decoded policy %v, want the platform's mode fields", policy)
	}
}

func TestOSUpdatesRefusesAClusterTheLaneDoesNotServe(t *testing.T) {
	for _, kind := range []string{"proxmox", "imported"} {
		t.Run(kind, func(t *testing.T) {
			recorder := newOSUpdatesServer(t, kind)
			_, _, runError := runOSUpdates(t, "", "get", testClusterID)
			assertOSUpdatesExit(t, runError, exitUsage)
			if !strings.Contains(runError.Error(), "is a "+kind+" cluster; Ankra manages OS updates on Hetzner, OVH") {
				t.Fatalf("refusal %q does not name the kind and the providers", runError)
			}
			if requests := recorder.seen(); len(requests) != 0 {
				t.Fatalf("a refused kind reached the lane: %+v", requests)
			}
		})
	}
}

func TestOSUpdatesGetOnAPlatformWithoutTheRoute(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodGet, "", http.StatusNotFound, `{"detail":"Not Found"}`)

	_, _, runError := runOSUpdates(t, "", "get", testClusterID)
	assertOSUpdatesExit(t, runError, exitError)
	if !strings.Contains(runError.Error(), "does not serve OS updates for hetzner clusters") {
		t.Fatalf("error %q does not say the platform lacks the route", runError)
	}
}

func TestOSUpdatesSetSendsTheClusterMode(t *testing.T) {
	for _, mode := range []string{"automatic", "advisory", "default", " Automatic "} {
		t.Run(mode, func(t *testing.T) {
			recorder := newOSUpdatesServer(t, "hetzner")
			recorder.answer(http.MethodPatch, "policy", http.StatusOK, strings.NewReplacer(
				`"mode_source":"notice_period"`, `"mode_source":"chosen"`,
				`"window_open_now":false}`, `"window_open_now":false,"message":"OS updates are now automatic on 3 host(s).",`+
					`"hold_revert":{"operation_id":"revert-1"}}`).Replace(osUpdatesPolicyNoticePeriod))

			stdout, _, runError := runOSUpdates(t, "", "set", testClusterID, "--mode", mode)
			if runError != nil {
				t.Fatalf("set --mode %s failed: %v", mode, runError)
			}
			requests := recorder.seen()
			if len(requests) != 1 || requests[0].method != http.MethodPatch ||
				requests[0].path != "/api/v1/clusters/hetzner/"+testClusterID+"/os-patches/policy" {
				t.Fatalf("expected one PATCH of the policy, got %+v", requests)
			}
			assertOSUpdatesBody(t, requests[0], map[string]any{"mode": strings.ToLower(strings.TrimSpace(mode))})
			assertContainsAll(t, stdout, []string{
				"OS updates for cluster test-cluster (hetzner) changed.",
				"Mode:   advisory (chosen for this cluster)",
				"OS updates are now automatic on 3 host(s).",
				"ankra cluster operations list revert-1 --cluster " + testClusterID,
			})
		})
	}
}

func TestOSUpdatesSetSendsNodeGroupModes(t *testing.T) {
	recorder := newOSUpdatesServer(t, "digitalocean")
	recorder.answer(http.MethodPatch, "policy", http.StatusOK, osUpdatesPolicyNoticePeriod)

	if _, _, runError := runOSUpdates(t, "", "set", testClusterID, "--node-group", "batch", "--node-group", "gpu",
		"--mode", "inherit"); runError != nil {
		t.Fatalf("set --node-group failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 {
		t.Fatalf("expected one PATCH, got %+v", requests)
	}
	assertOSUpdatesBody(t, requests[0], map[string]any{"node_group_modes": map[string]any{"batch": "inherit", "gpu": "inherit"}})
}

func TestOSUpdatesSetSendsOnlyTheWindowFlagsPassed(t *testing.T) {
	recorder := newOSUpdatesServer(t, "aws")
	recorder.answer(http.MethodPatch, "policy", http.StatusOK, osUpdatesPolicyNoticePeriod)

	stdout, _, runError := runOSUpdates(t, "", "set", testClusterID, "--window-days", "sat,sun", "--window-start-hour", "0",
		"--window-duration-hours", "4", "--cadence-days", "14", "--security-lag-days", "7", "--include-stateful-nodes", "-o", "json")
	if runError != nil {
		t.Fatalf("set with window flags failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 {
		t.Fatalf("expected one PATCH, got %+v", requests)
	}
	assertOSUpdatesBody(t, requests[0], map[string]any{"window_days": []any{"sat", "sun"}, "window_start_hour_utc": 0,
		"window_duration_hours": 4, "cadence_days": 14, "security_lag_days": 7, "include_stateful_nodes": true})
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil || decoded["mode"] != "advisory" {
		t.Fatalf("-o json is not the platform's answer (%v):\n%s", decodeError, stdout)
	}
}

func TestOSUpdatesSetRefusesBadFlagsBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		message   string
	}{
		{"nothing", []string{"set", testClusterID}, "nothing to change"},
		{"inherit on the cluster", []string{"set", testClusterID, "--mode", "inherit"}, "automatic, advisory or default"},
		{"default on a node group", []string{"set", testClusterID, "--node-group", "batch", "--mode", "default"}, "automatic, advisory or inherit"},
		{"node group without a mode", []string{"set", testClusterID, "--node-group", "batch"}, "--node-group needs --mode"},
		{"empty node group", []string{"set", testClusterID, "--node-group", " ", "--mode", "advisory"}, "needs a node group name"},
		{"empty mode", []string{"set", testClusterID, "--mode", ""}, "--mode needs a value"},
		{"no cluster", []string{"set", "--mode", "advisory"}, "missing the cluster"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newOSUpdatesServer(t, "hetzner")
			_, _, runError := runOSUpdates(t, "", testCase.arguments...)
			assertOSUpdatesExit(t, runError, exitUsage)
			if !strings.Contains(runError.Error(), testCase.message) {
				t.Fatalf("error %q does not contain %q", runError, testCase.message)
			}
			if requests := recorder.seen(); len(requests) != 0 {
				t.Fatalf("a refused set reached the platform: %+v", requests)
			}
		})
	}
}

func TestOSUpdatesSetRefusalExitsBlocked(t *testing.T) {
	recorder := newOSUpdatesServer(t, "upcloud")
	recorder.answer(http.MethodPatch, "policy", http.StatusConflict,
		`{"detail":"The OS patch policy was not changed: node group \"nope\" is not a node group of this cluster."}`)

	_, _, runError := runOSUpdates(t, "", "set", testClusterID, "--node-group", "nope", "--mode", "advisory")
	assertOSUpdatesExit(t, runError, exitBlocked)
	if !strings.Contains(runError.Error(), `node group "nope" is not a node group of this cluster`) {
		t.Fatalf("error %q does not carry the platform's refusal", runError)
	}
}

const osUpdatesPlanAnswer = `{"operation_id":"plan-1","completed":true,
"hosts":[{"resource_id":"r1","name":"cp-1","role":"control_plane","status":"success",
"summary":{"pending":5,"security_pending":2,"held_back_pending":1,"held_back_security_pending":1},
"held_back_pending":["systemd"],"security_pending":["openssl"],"reboot_required":true},
{"resource_id":"r2","name":"db-1","role":"worker","node_group":"stateful","status":"success",
"summary":{"pending":0,"security_pending":0,"held_back_pending":0},"held_back_pending":[],"security_pending":[],
"reboot_required":false,"stateful_verdict":"would_skip"},
{"resource_id":"r3","name":"worker-2","role":"worker","node_group":"default","status":"failed",
"held_back_pending":[],"security_pending":[],"reboot_required":false,"error":"ssh timed out"}],
"summary":{"hosts":3,"hosts_with_work":1,"hosts_unknown":1,"pending":5,"security_pending":2,"held_back_pending":1,
"held_back_security_pending":1,"reboot_required":1,"observed_at":"2026-11-21T10:00:00Z"},
"run_order":["cp-1","worker-2","bastion (bastion, last)"],
"skipped":[{"resource_id":"r2","name":"db-1","role":"worker","reason":"carries a single-replica stateful workload"}],
"policy":` + osUpdatesPolicyNoticePeriod + `,"message":"Dry run finished."}`

func TestOSUpdatesPlanPrintsEachHost(t *testing.T) {
	recorder := newOSUpdatesServer(t, "scaleway")
	recorder.answer(http.MethodPost, "plan", http.StatusOK, osUpdatesPlanAnswer)

	stdout, _, runError := runOSUpdates(t, "", "plan", testClusterID)
	if runError != nil {
		t.Fatalf("plan failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodPost ||
		requests[0].path != "/api/v1/clusters/scaleway/"+testClusterID+"/os-patches/plan" {
		t.Fatalf("expected one POST of the dry run, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{
		"OS updates dry run on cluster test-cluster (scaleway); nothing was installed.",
		"cp-1", "control_plane", "yes",
		"stateful: left out unless --include-stateful-nodes",
		"ssh timed out",
		"Summary:   1 of 3 hosts have updates, 5 pending (2 security), 1 would reboot, 1 unknown",
		"Run order: cp-1, worker-2, bastion (bastion, last)",
		"db-1 (worker): carries a single-replica stateful workload",
		"A host whose status is not success has an unknown plan, not an empty one.",
	})
	if strings.Contains(stdout, "Dry run finished.") {
		t.Fatalf("the platform's tool-worded message reached the terminal:\n%s", stdout)
	}
}

func TestOSUpdatesPlanStillRunningPointsAtGet(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodPost, "plan", http.StatusOK, `{"operation_id":"plan-2","completed":false,"hosts":[],
"run_order":[],"skipped":[],"policy":`+osUpdatesPolicyNoticePeriod+`,"message":"still running"}`)

	stdout, _, runError := runOSUpdates(t, "", "plan", testClusterID)
	if runError != nil {
		t.Fatalf("plan failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"Not every host has reported yet (operation plan-2)",
		"ankra cluster os-updates get " + testClusterID})
}

const osUpdatesRunAnswer = `{"operation_id":"run-9","order":[{"resource_id":"r1","name":"cp-1","role":"control_plane"},
{"resource_id":"r3","name":"worker-2","role":"worker","node_group":"default"}],
"skipped":[{"resource_id":"r2","name":"db-1","role":"worker","reason":"stateful"}],"include_stateful_nodes":false,
"bastion_note":"The bastion was left out of this run.","policy":` + osUpdatesPolicyNoticePeriod + `,
"message":"Follow it with get_os_patch_status.","hold_revert":{"operation_id":"","not_started":"no bastion"}}`

func TestOSUpdatesRunWhileTheWindowIsClosedStartsNothing(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")

	_, _, runError := runOSUpdates(t, "y\n", "run", testClusterID)
	assertOSUpdatesExit(t, runError, exitBlocked)
	assertContainsAll(t, runError.Error(), []string{
		"the maintenance window of cluster test-cluster (mon, tue, wed, thu 02:00-06:00 UTC) is closed",
		"it next opens 2026-11-23 02:00 UTC", "Nothing was started", "advisory", "pass --now",
	})
	for _, request := range recorder.seen() {
		if request.method != http.MethodGet {
			t.Fatalf("a closed window still sent %s %s", request.method, request.path)
		}
	}
}

func TestOSUpdatesRunClosedWindowOnAnAutomaticCluster(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodGet, "", http.StatusOK, osUpdatesStatusWith(`"mode":"advisory"`, `"mode":"automatic"`))

	_, _, runError := runOSUpdates(t, "", "run", testClusterID)
	assertOSUpdatesExit(t, runError, exitBlocked)
	if !strings.Contains(runError.Error(), "Ankra starts the run in the window on its own") {
		t.Fatalf("error %q does not say the window loop runs an automatic cluster", runError)
	}
}

func TestOSUpdatesRunNowAsksFirstAndDeclineSendsNothing(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodPost, "run", http.StatusOK, osUpdatesRunAnswer)

	_, stderr, runError := runOSUpdates(t, "n\n", "run", testClusterID, "--now")
	assertOSUpdatesExit(t, runError, exitCancelled)
	assertContainsAll(t, stderr, []string{
		"Start the rolling OS update run on cluster",
		"now, outside its maintenance window (mon, tue, wed, thu 02:00-06:00 UTC)",
		"Nodes are drained and updated one at a time",
	})
	for _, request := range recorder.seen() {
		if request.method == http.MethodPost {
			t.Fatalf("a declined run was sent: %+v", request)
		}
	}
}

func TestOSUpdatesRunNowWithYesStartsOutsideTheWindow(t *testing.T) {
	recorder := newOSUpdatesServer(t, "ankracloud")
	recorder.answer(http.MethodPost, "run", http.StatusOK, osUpdatesRunAnswer)

	stdout, _, runError := runOSUpdates(t, "", "run", testClusterID, "--now", "--yes", "--exclude-bastion", "--exclude-stateful-nodes")
	if runError != nil {
		t.Fatalf("run --now --yes failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 2 || requests[1].method != http.MethodPost ||
		requests[1].path != "/api/v1/clusters/ankracloud/"+testClusterID+"/os-patches/run" {
		t.Fatalf("expected the status read then one POST of the run, got %+v", requests)
	}
	assertOSUpdatesBody(t, requests[1], map[string]any{"outside_window": true, "include_stateful_nodes": false, "include_bastion": false})
	assertContainsAll(t, stdout, []string{
		"Rolling OS update run started on cluster test-cluster (ankracloud).",
		"Operation: run-9",
		"1. cp-1 (control_plane)",
		"2. worker-2 (worker, node group default)",
		"db-1 (worker): stateful",
		"The bastion was left out of this run.",
		"A failed node stops the run.",
		"The update hold could not be removed from the hosts this run leaves out: no bastion.",
		"ankra cluster operations list run-9 --cluster " + testClusterID,
	})
	if strings.Contains(stdout, "get_os_patch_status") {
		t.Fatalf("the platform's tool-worded message reached the terminal:\n%s", stdout)
	}
}

func TestOSUpdatesRunInsideTheWindowConfirmsAndSendsNoOverride(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodGet, "", http.StatusOK, osUpdatesStatusWith(`"window_open_now":false`, `"window_open_now":true`))
	recorder.answer(http.MethodPost, "run", http.StatusOK, osUpdatesRunAnswer)

	stdout, stderr, runError := runOSUpdates(t, "y\n", "run", testClusterID, "--include-stateful-nodes", "-o", "json")
	if runError != nil {
		t.Fatalf("run failed: %v", runError)
	}
	if !strings.Contains(stderr, "now, inside its maintenance window") {
		t.Fatalf("prompt %q does not say the run starts inside the window", stderr)
	}
	requests := recorder.seen()
	if len(requests) != 2 {
		t.Fatalf("expected the status read and the run, got %+v", requests)
	}
	assertOSUpdatesBody(t, requests[1], map[string]any{"outside_window": false, "include_stateful_nodes": true})
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil || decoded["operation_id"] != "run-9" {
		t.Fatalf("-o json is not the platform's answer (%v):\n%s", decodeError, stdout)
	}
}

func TestOSUpdatesRunRefusedByThePlatformExitsBlocked(t *testing.T) {
	recorder := newOSUpdatesServer(t, "hetzner")
	recorder.answer(http.MethodPost, "run", http.StatusConflict, `{"detail":"The OS patch run cannot start: a run is in flight."}`)

	_, _, runError := runOSUpdates(t, "", "run", testClusterID, "--now", "--yes")
	assertOSUpdatesExit(t, runError, exitBlocked)
	if !strings.Contains(runError.Error(), "a run is in flight") {
		t.Fatalf("error %q does not carry the platform's refusal", runError)
	}
}

func TestOSUpdatesProvidersSentenceNamesEveryLaneProvider(t *testing.T) {
	if got, want := osUpdatesProvidersSentence(), "Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, AWS and Ankra Cloud"; got != want {
		t.Fatalf("providers sentence %q, want %q", got, want)
	}
}

func TestFormatOSUpdatesTimeKeepsAValueItCannotRead(t *testing.T) {
	if got := formatOSUpdatesTime("soon"); got != "soon" {
		t.Fatalf("formatOSUpdatesTime(soon) = %q", got)
	}
}
