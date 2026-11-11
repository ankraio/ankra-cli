package cmd

// The cluster-facing parity commands (ankra-hq41xb.15) against a scripted
// platform: the real client talks to an httptest server, so the method, path,
// query and body each command sends are pinned on the request itself.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

type clusterParityRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// clusterParityServer answers "METHOD /path" with the scripted JSON body and
// records every request. An unscripted route answers the router's own 404.
type clusterParityServer struct {
	mutex     sync.Mutex
	responses map[string]string
	statuses  map[string]int
	requests  []clusterParityRequest
}

func newClusterParityServer(t *testing.T, responses map[string]string) *clusterParityServer {
	t.Helper()
	recorder := &clusterParityServer{responses: responses, statuses: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorded := clusterParityRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery}
		if raw, _ := io.ReadAll(request.Body); len(bytes.TrimSpace(raw)) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		key := request.Method + " " + request.URL.Path
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, recorded)
		body, scripted := recorder.responses[key]
		status := recorder.statuses[key]
		recorder.mutex.Unlock()
		if !scripted {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Not Found"}`))
			return
		}
		if status != 0 {
			writer.WriteHeader(status)
		}
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

// seen returns the recorded requests without the cluster lookups a command
// makes to resolve its target.
func (recorder *clusterParityServer) seen() []clusterParityRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	var requests []clusterParityRequest
	for _, request := range recorder.requests {
		if request.path == "/api/v1/clusters" {
			continue
		}
		requests = append(requests, request)
	}
	return requests
}

func runClusterParityCommand(t *testing.T, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(""))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	rootCmd.SetArgs(arguments)
	commands := clusterTreeCommands()
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func promptsNotInteractive(t *testing.T) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return false }
	t.Cleanup(func() { promptIsInteractive = previous })
}

func requireOneClusterParityRequest(t *testing.T, recorder *clusterParityServer, method string, path string) clusterParityRequest {
	t.Helper()
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != method || requests[0].path != path {
		t.Fatalf("expected one %s %s, got %+v", method, path, requests)
	}
	return requests[0]
}

const clusterParityListing = `{"result":[{"id":"` + testClusterID + `","name":"demo","kind":"hetzner"}],"pagination":{"total_pages":1,"page":1,"page_size":1}}`

func TestWorkerHealGetAndSet(t *testing.T) {
	path := "/api/v1/org/clusters/" + testClusterID + "/worker-self-heal"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + path: `{"cluster_id":"c1","enabled":true,"latest_incident":{"node_name":"worker-1","state":"closed","outcome":"restored","reason":"","not_ready_since":"2026-11-01T10:00:00Z","last_restart_at":"2026-11-01T10:11:00Z","closed_at":"2026-11-01T10:15:00Z","mode":"active"}}`,
		"PUT " + path: `{"cluster_id":"c1","enabled":false,"latest_incident":null}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "worker-heal", "get")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"Automatic restart: enabled", "worker-1", "restored", "active mode"})

	stdout, _, runError = runClusterParityCommand(t, "cluster", "worker-heal", "set", "--enabled=false")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 2 || requests[1].method != http.MethodPut || requests[1].path != path || requests[1].body["enabled"] != false {
		t.Fatalf("false must be sent, not dropped as a zero value: %+v", requests)
	}
	assertContainsAll(t, stdout, []string{"disabled for cluster 'test-cluster'", "none"})
}

func TestWorkerHealSetRequiresTheFlagAndReportsADisagreement(t *testing.T) {
	path := "/api/v1/org/clusters/" + testClusterID + "/worker-self-heal"
	recorder := newClusterParityServer(t, map[string]string{
		"PUT " + path: `{"cluster_id":"c1","enabled":true,"latest_incident":null}`,
	})
	_, _, runError := runClusterParityCommand(t, "cluster", "worker-heal", "set")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a set without --enabled must be a usage error that sends nothing, got %v and %+v", runError, recorder.seen())
	}
	_, _, runError = runClusterParityCommand(t, "cluster", "worker-heal", "set", "--enabled=false")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "not disabled as requested") {
		t.Fatalf("a stored state that disagrees must exit non-zero, got %v", runError)
	}
}

func TestClusterSettingRefusalsMapToExitCodes(t *testing.T) {
	path := "/api/v1/org/clusters/" + testClusterID + "/worker-self-heal"
	recorder := newClusterParityServer(t, map[string]string{
		"PUT " + path: `{"detail":"You do not have permission to perform this action"}`,
	})
	recorder.statuses["PUT "+path] = http.StatusForbidden
	_, _, runError := runClusterParityCommand(t, "cluster", "worker-heal", "set", "--enabled=true")
	if runError == nil || exitCodeFor(runError) != exitForbidden {
		t.Fatalf("a 403 must exit %d, got %v (exit %d)", exitForbidden, runError, exitCodeFor(runError))
	}
	_, _, runError = runClusterParityCommand(t, "cluster", "worker-heal", "get")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "does not serve this route yet") {
		t.Fatalf("the router's own 404 is a platform without the route (exit 1), got %v (exit %d)", runError, exitCodeFor(runError))
	}
	recorder.mutex.Lock()
	recorder.responses["GET "+path] = `{"detail":"Cluster not found"}`
	recorder.statuses["GET "+path] = http.StatusNotFound
	recorder.mutex.Unlock()
	_, _, runError = runClusterParityCommand(t, "cluster", "worker-heal", "get")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("the platform's own not-found must exit %d, got %v (exit %d)", exitNotFound, runError, exitCodeFor(runError))
	}
}

func TestClusterBaselinePrintsScoreAndChecks(t *testing.T) {
	path := "/api/v1/org/clusters/" + testClusterID + "/baseline"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + path: `{"cluster_id":"c1","catalogue_version":1,"computed":true,"computed_at":"2026-11-09T02:00:00Z","score":75,"coverage":0.8,"weights":{"passed":6,"failed":2,"unknown":2,"total":10},"checks":[{"id":"backups_restore_tested","title":"Backups with a tested restore","why":"w","data_source":"d","weight":2,"status":"fail","evidence":"no restore test in 90 days"}],"gaps":[],"unknowns":[]}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "baseline")
	if runError != nil {
		t.Fatalf("baseline failed: %v", runError)
	}
	requireOneClusterParityRequest(t, recorder, http.MethodGet, path)
	assertContainsAll(t, stdout, []string{"75%", "weight 8 of 10 decided", "[fail", "Backups with a tested restore", "no restore test in 90 days"})

	stdout, _, runError = runClusterParityCommand(t, "cluster", "baseline", "-o", "json")
	if runError != nil {
		t.Fatalf("baseline -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil || decoded["score"] != float64(75) {
		t.Fatalf("stdout is not the platform's answer as JSON: %v: %q", decodeError, stdout)
	}
}

func TestClusterBaselineNotScoredIsNotZero(t *testing.T) {
	newClusterParityServer(t, map[string]string{
		"GET /api/v1/org/clusters/" + testClusterID + "/baseline": `{"cluster_id":"c1","catalogue_version":1,"computed":false,"computed_at":null,"score":null,"coverage":0,"weights":{"passed":0,"failed":0,"unknown":10,"total":10},"checks":[],"gaps":[],"unknowns":[]}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "baseline")
	if runError != nil {
		t.Fatalf("baseline failed: %v", runError)
	}
	if !strings.Contains(stdout, "not scored yet") || strings.Contains(stdout, "0%") {
		t.Fatalf("an unscored cluster must not read as a score of zero: %q", stdout)
	}
}

func TestClusterExportPrintsAndWritesTheYAML(t *testing.T) {
	path := "/api/v1/org/clusters/imported/" + testClusterID + "/iac"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + path: `{"yaml_string_base64":"a2luZDogSW1wb3J0Q2x1c3Rlcgo="}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "export")
	if runError != nil {
		t.Fatalf("export failed: %v", runError)
	}
	requireOneClusterParityRequest(t, recorder, http.MethodGet, path)
	if stdout != "kind: ImportCluster\n" {
		t.Fatalf("stdout must be the YAML alone, got %q", stdout)
	}
	target := filepath.Join(t.TempDir(), "cluster.yaml")
	stdout, stderr, runError := runClusterParityCommand(t, "cluster", "export", "-f", target)
	if runError != nil {
		t.Fatalf("export -f failed: %v", runError)
	}
	written, readError := os.ReadFile(target)
	if readError != nil || string(written) != "kind: ImportCluster\n" || stdout != "" || !strings.Contains(stderr, target) {
		t.Fatalf("the YAML must go to the file and the note to stderr: file %q (%v), stdout %q, stderr %q", written, readError, stdout, stderr)
	}
}

func TestHeldUpdatesTakeSendsTheFingerprint(t *testing.T) {
	notificationID := "0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c"
	path := "/api/v1/org/clusters/" + testClusterID + "/held-platform-updates/" + notificationID + "/take"
	recorder := newClusterParityServer(t, map[string]string{
		"POST " + path: `{"cluster_id":"c1","notification_id":"n1","taken":[{"stack":"platform","member_kind":"addon","member":"cert-manager","pin_kind":"chart_version","image":"","running":"1.15.0","pinned":"1.16.1","reason":"production"}],"kept":[{"stack":"platform","member_kind":"addon","member":"traefik","pin_kind":"chart_version","image":"","running":"30.0.0","pinned":"31.0.0","reason":"customer_version"}]}`,
	})
	_, _, runError := runClusterParityCommand(t, "cluster", "held-updates", "take", notificationID)
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a take without --fingerprint must be a usage error that sends nothing, got %v and %+v", runError, recorder.seen())
	}
	stdout, _, runError := runClusterParityCommand(t, "cluster", "held-updates", "take", notificationID, "--fingerprint", "abc123")
	if runError != nil {
		t.Fatalf("take failed: %v", runError)
	}
	request := requireOneClusterParityRequest(t, recorder, http.MethodPost, path)
	if request.body["fingerprint"] != "abc123" {
		t.Fatalf("the fingerprint must be sent, got %+v", request.body)
	}
	assertContainsAll(t, stdout, []string{"Took 1 held platform update", "cert-manager", "1.15.0 -> 1.16.1", "Kept", "customer_version"})
}

func TestHeldUpdatesTakeOnAChangedCardSaysNothingWasTaken(t *testing.T) {
	notificationID := "0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c"
	path := "/api/v1/org/clusters/" + testClusterID + "/held-platform-updates/" + notificationID + "/take"
	recorder := newClusterParityServer(t, map[string]string{"POST " + path: `{"detail":"The card changed since it was read"}`})
	recorder.statuses["POST "+path] = http.StatusConflict
	_, _, runError := runClusterParityCommand(t, "cluster", "held-updates", "take", notificationID, "--fingerprint", "stale")
	if runError == nil || !strings.Contains(runError.Error(), "nothing was taken") || !strings.Contains(runError.Error(), "card changed") {
		t.Fatalf("a 409 must say nothing was taken and why, got %v", runError)
	}
}

func TestClusterMetricsSourceNeverPrintsASecret(t *testing.T) {
	path := "/api/v1/org/clusters/imported/" + testClusterID + "/metrics-source"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + path: `{"provider":"prometheus","endpoint":"https://prom.example.com","state":"up","flavor":"mimir","tenant_id":"team-a","credential_name":"prom-cred","credential":{"name":"prom-cred","state":"up","username":"reader"},"health":{"state":"healthy","checked_at":"2026-11-10T08:00:00Z","detail":null,"sample_count":12}}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "metrics", "source")
	if runError != nil {
		t.Fatalf("metrics source failed: %v", runError)
	}
	requireOneClusterParityRequest(t, recorder, http.MethodGet, path)
	assertContainsAll(t, stdout, []string{"prometheus", "https://prom.example.com", "mimir", "team-a", "prom-cred (up)", "healthy"})
}

func TestClusterMetricsSourceWithoutOneExitsNotFound(t *testing.T) {
	path := "/api/v1/org/clusters/imported/" + testClusterID + "/metrics-source"
	recorder := newClusterParityServer(t, map[string]string{"GET " + path: `{"detail":"Metrics source not found"}`})
	recorder.statuses["GET "+path] = http.StatusNotFound
	_, _, runError := runClusterParityCommand(t, "cluster", "metrics", "source")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("a cluster without a metrics source must exit %d, got %v", exitNotFound, runError)
	}
	recorder.mutex.Lock()
	delete(recorder.responses, "GET "+path)
	recorder.mutex.Unlock()
	_, _, runError = runClusterParityCommand(t, "cluster", "metrics", "source")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "does not serve this route yet") {
		t.Fatalf("the router's own 404 is a platform without the route (exit 1), got %v (exit %d)", runError, exitCodeFor(runError))
	}
}

func TestClusterDNSTokenRotationFlow(t *testing.T) {
	base := "/api/v1/clusters/" + testClusterID + "/dns-zone/token-rotation"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + base:                       `{"success":true,"fqdn":"abc.ankra.cc","state":"active","rotation_requested_at":null,"previous_token_pending_revocation":true,"revocation_requested_at":null}`,
		"POST " + base:                      `{"success":true,"fqdn":"abc.ankra.cc","state":"active","rotation_requested_at":"2026-11-10T09:00:00Z","previous_token_pending_revocation":false,"revocation_requested_at":null}`,
		"POST " + base + "/revoke-previous": `{"success":true,"fqdn":"abc.ankra.cc","state":"active","rotation_requested_at":null,"previous_token_pending_revocation":true,"revocation_requested_at":"2026-11-10T09:30:00Z"}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "dns-token", "status")
	if runError != nil {
		t.Fatalf("status failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"abc.ankra.cc (active)", "none pending", "still valid"})

	stdout, _, runError = runClusterParityCommand(t, "cluster", "dns-token", "rotate")
	if runError != nil {
		t.Fatalf("rotate failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"waiting for the replacement token", "revoke-previous"})

	promptsNotInteractive(t)
	_, _, runError = runClusterParityCommand(t, "cluster", "dns-token", "revoke-previous")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("revoking without a terminal or --yes must be a usage error, got %v", runError)
	}
	if requests := recorder.seen(); len(requests) != 2 {
		t.Fatalf("an unconfirmed revoke must send nothing, got %+v", requests)
	}
	stdout, _, runError = runClusterParityCommand(t, "cluster", "dns-token", "revoke-previous", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("revoke-previous failed: %v", runError)
	}
	requests := recorder.seen()
	if last := requests[len(requests)-1]; last.method != http.MethodPost || last.path != base+"/revoke-previous" {
		t.Fatalf("expected the revoke-previous POST last, got %+v", requests)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil || decoded["revocation_requested_at"] != "2026-11-10T09:30:00Z" {
		t.Fatalf("stdout is not the platform's answer as JSON: %v: %q", decodeError, stdout)
	}
}

func TestK3sDisabledComponentsUseTheClustersProviderRoute(t *testing.T) {
	path := "/api/v1/clusters/hetzner/" + testClusterID + "/k3s-disabled-components"
	recorder := newClusterParityServer(t, map[string]string{
		"GET /api/v1/clusters": clusterParityListing,
		"GET " + path:          `{"k3s_disabled_components":["traefik"],"resource_ids":["r1","r2"],"control_plane_count":1,"applies":"Converged within five minutes."}`,
		"PUT " + path:          `{"k3s_disabled_components":["traefik","servicelb"],"resource_ids":["r1","r2"],"control_plane_count":1,"applies":"Converged within five minutes."}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "k3s-disabled-components", "get", testClusterID)
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	requireOneClusterParityRequest(t, recorder, http.MethodGet, path)
	assertContainsAll(t, stdout, []string{"traefik", "Control planes:          1", "Converged within five minutes."})

	promptsNotInteractive(t)
	_, _, runError = runClusterParityCommand(t, "cluster", "k3s-disabled-components", "set", testClusterID, "--components", "traefik,servicelb")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 1 {
		t.Fatalf("an unconfirmed set must be a usage error that sends nothing, got %v and %+v", runError, recorder.seen())
	}
	stdout, _, runError = runClusterParityCommand(t, "cluster", "k3s-disabled-components", "set", testClusterID, "--components", "traefik,servicelb", "--yes")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	last := requests[len(requests)-1]
	sent, _ := last.body["k3s_disabled_components"].([]any)
	if last.method != http.MethodPut || last.path != path || len(sent) != 2 || sent[0] != "traefik" || sent[1] != "servicelb" {
		t.Fatalf("the complete list must be sent, got %+v", last)
	}
	assertContainsAll(t, stdout, []string{"traefik, servicelb"})
}

func TestK3sDisabledComponentsSetRequiresComponents(t *testing.T) {
	recorder := newClusterParityServer(t, map[string]string{"GET /api/v1/clusters": clusterParityListing})
	_, _, runError := runClusterParityCommand(t, "cluster", "k3s-disabled-components", "set", testClusterID, "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a set without --components must be a usage error that sends nothing, got %v", runError)
	}
}

func TestK3sDisabledComponentsRefuseAnImportedCluster(t *testing.T) {
	recorder := newClusterParityServer(t, map[string]string{
		"GET /api/v1/clusters": `{"result":[{"id":"` + testClusterID + `","name":"demo","kind":"imported"}],"pagination":{"total_pages":1,"page":1,"page_size":1}}`,
	})
	_, _, runError := runClusterParityCommand(t, "cluster", "k3s-disabled-components", "get", testClusterID)
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "imported") || len(recorder.seen()) != 0 {
		t.Fatalf("a cluster of another kind must be a usage error naming its kind, got %v", runError)
	}
}

func TestUpcloudCatalogueReadsSendTheCredential(t *testing.T) {
	recorder := newClusterParityServer(t, map[string]string{
		"GET /api/v1/clusters/upcloud/gateway-plans":       `{"plans":[{"name":"standard","bandwidth_mbps":500,"server_number":1,"high_availability":false,"max_connections":100000,"vpn_tunnel_amount":0,"supported_features":["nat"]}],"default_plan":"standard","plan_change_notice":"Egress is interrupted for several minutes."}`,
		"GET /api/v1/clusters/upcloud/load-balancer-plans": `[{"name":"production-small","server_number":2,"per_server_max_sessions":50000,"highly_available":true}]`,
		"GET /api/v1/clusters/upcloud/zones":               `[{"id":"fi-hel2","description":"Helsinki #2"}]`,
	})
	for _, testCase := range []struct {
		command string
		path    string
		wants   []string
	}{
		{"gateway-plans", "/api/v1/clusters/upcloud/gateway-plans", []string{"standard (default)", "500", "Egress is interrupted"}},
		{"load-balancer-plans", "/api/v1/clusters/upcloud/load-balancer-plans", []string{"production-small", "yes", "50000"}},
		{"available-zones", "/api/v1/clusters/upcloud/zones", []string{"fi-hel2", "Helsinki #2"}},
	} {
		t.Run(testCase.command, func(t *testing.T) {
			before := len(recorder.seen())
			_, _, runError := runClusterParityCommand(t, "cluster", "upcloud", testCase.command)
			if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != before {
				t.Fatalf("without --credential-id it must be a usage error that sends nothing, got %v", runError)
			}
			stdout, _, runError := runClusterParityCommand(t, "cluster", "upcloud", testCase.command, "--credential-id", "cred-1")
			if runError != nil {
				t.Fatalf("%s failed: %v", testCase.command, runError)
			}
			requests := recorder.seen()
			last := requests[len(requests)-1]
			if last.method != http.MethodGet || last.path != testCase.path || last.query != "credential_id=cred-1" {
				t.Fatalf("expected GET %s?credential_id=cred-1, got %+v", testCase.path, last)
			}
			assertContainsAll(t, stdout, testCase.wants)
		})
	}
}

func TestUpcloudGatewayPlanChangeConfirmsFirst(t *testing.T) {
	path := "/api/v1/clusters/upcloud/" + testClusterID + "/gateway-plan"
	recorder := newClusterParityServer(t, map[string]string{
		"PUT " + path: `{"gateway_plan":"production","plan_change_request_id":"req-1","resource_ids":["g1"],"deferred_resource_ids":[],"notice":"Outbound access is interrupted."}`,
	})
	recorder.statuses["PUT "+path] = http.StatusAccepted
	promptsNotInteractive(t)
	_, _, runError := runClusterParityCommand(t, "cluster", "upcloud", "gateway-plan", testClusterID, "--plan", "production")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("an unconfirmed plan change must be a usage error that sends nothing, got %v", runError)
	}
	_, _, runError = runClusterParityCommand(t, "cluster", "upcloud", "gateway-plan", testClusterID, "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a plan change without --plan must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError := runClusterParityCommand(t, "cluster", "upcloud", "gateway-plan", testClusterID, "--plan", "production", "--yes")
	if runError != nil {
		t.Fatalf("gateway-plan failed: %v", runError)
	}
	request := requireOneClusterParityRequest(t, recorder, http.MethodPut, path)
	if request.body["gateway_plan"] != "production" {
		t.Fatalf("the plan must be sent, got %+v", request.body)
	}
	assertContainsAll(t, stdout, []string{"production", "req-1", "Outbound access is interrupted."})
}

func TestUpcloudLoadBalancerPlanGetAndSet(t *testing.T) {
	path := "/api/v1/clusters/upcloud/" + testClusterID + "/load-balancer-plan"
	recorder := newClusterParityServer(t, map[string]string{
		"GET " + path: `{"load_balancer_plan":null,"managed":true,"load_balancers_observed":false,"load_balancers":null,"observation_error":"UpCloud answered 503","available_plans":null}`,
		"PUT " + path: `{"load_balancer_plan":"production-small","previous_load_balancer_plan":"development","changed":true,"resource_ids":["r1"]}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "upcloud", "load-balancer-plan", "get", testClusterID)
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"Recorded plan: none", "not observed (UpCloud answered 503)"})

	stdout, _, runError = runClusterParityCommand(t, "cluster", "upcloud", "load-balancer-plan", "set", testClusterID, "--plan", "production-small")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	last := requests[len(requests)-1]
	if last.method != http.MethodPut || last.path != path || last.body["load_balancer_plan"] != "production-small" {
		t.Fatalf("the plan must be sent, got %+v", last)
	}
	assertContainsAll(t, stdout, []string{"production-small (was development)"})
}

func TestOvhGatewayModelAndHetznerConverge(t *testing.T) {
	modelPath := "/api/v1/clusters/ovh/" + testClusterID + "/gateway-model"
	convergePath := "/api/v1/clusters/hetzner/" + testClusterID + "/external-cloud-provider/converge"
	recorder := newClusterParityServer(t, map[string]string{
		"PUT " + modelPath:     `{"gateway_model":"m","resource_ids":["g1"]}`,
		"POST " + convergePath: `{"external_cloud_provider":true,"converged_resource_ids":["n1","n2"],"already_external_resource_ids":[],"verified_node_count":2,"applies":"Only the stored definitions changed."}`,
	})
	stdout, _, runError := runClusterParityCommand(t, "cluster", "ovh", "gateway-model", testClusterID, "--model", "m", "--yes")
	if runError != nil {
		t.Fatalf("gateway-model failed: %v", runError)
	}
	request := requireOneClusterParityRequest(t, recorder, http.MethodPut, modelPath)
	if request.body["gateway_model"] != "m" {
		t.Fatalf("the model must be sent, got %+v", request.body)
	}
	assertContainsAll(t, stdout, []string{"Gateway model set to m"})

	stdout, _, runError = runClusterParityCommand(t, "cluster", "hetzner", "external-cloud-provider", "converge", testClusterID)
	if runError != nil {
		t.Fatalf("converge failed: %v", runError)
	}
	requests := recorder.seen()
	if last := requests[len(requests)-1]; last.method != http.MethodPost || last.path != convergePath {
		t.Fatalf("expected POST %s, got %+v", convergePath, last)
	}
	assertContainsAll(t, stdout, []string{"converged:     2", "verified as external:     2", "Only the stored definitions changed."})
}

func TestProviderSettingRefusalKeepsThePlatformsReason(t *testing.T) {
	path := "/api/v1/clusters/ovh/" + testClusterID + "/gateway-model"
	recorder := newClusterParityServer(t, map[string]string{"PUT " + path: `{"detail":"The cluster has no recorded managed gateway"}`})
	recorder.statuses["PUT "+path] = http.StatusConflict
	_, _, runError := runClusterParityCommand(t, "cluster", "ovh", "gateway-model", testClusterID, "--model", "m", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "no recorded managed gateway") {
		t.Fatalf("a 409 must keep the platform's reason, got %v", runError)
	}
}
