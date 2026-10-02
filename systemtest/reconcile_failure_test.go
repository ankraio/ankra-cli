package systemtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The 2026-10-02 uks:managed failure, as the step recorded it before
// cluster#3762 taught the adapter to read UpCloud's RFC 7807 title, and after.
const (
	uksNetworkRequiredRaw = `uks API 422: {"type":"https://developers.upcloud.com/1.3/errors#ERROR_INVALID_REQUEST",` +
		`"title":"Cluster network is required.","correlation_id":"01M3XZARJA8ZYDXRN5XSD8J1S4","status":422}`
	uksNetworkRequired = `uks API 422: Cluster network is required.`
)

func bashOrSkip(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	return bash
}

func TestClassifyStepError(t *testing.T) {
	bash := bashOrSkip(t)
	cases := []struct {
		name  string
		error string
		want  string
	}{
		{"uks 422 raw problem body", uksNetworkRequiredRaw, "terminal"},
		{"uks 422 title", uksNetworkRequired, "terminal"},
		{"credential refused", "doks API 401: Unable to authenticate you", "terminal"},
		{"permission refused", "gke API 403: Permission denied on resource project", "terminal"},
		{"bad request", "aks API 400: InvalidParameter: agentPoolProfile.vmSize is not allowed", "terminal"},
		{"http status form", "creating server: HTTP 404 Not Found", "terminal"},
		{"status code form", "request failed with status code 422", "terminal"},
		{"validation without a status", "validation failed: node pool name must be lowercase", "terminal"},

		{"provider 5xx", "uks API 500: Internal server error", "transient"},
		{"provider unavailable", "doks API 503: service unavailable", "transient"},
		{"rate limited 4xx", "uks API 429: Too many requests", "transient"},
		{"conflict 4xx", "ovh_mks API 409: cluster is being updated", "transient"},
		{"request timeout 4xx", "eks API 408: Request Timeout", "transient"},
		{"provisioning timeout", "timed out waiting for the bastion to accept SSH", "transient"},
		{"context deadline", "context deadline exceeded", "transient"},
		{"agent offline", "agent offline: no heartbeat for 5m", "transient"},
		{"connection refused", "dial tcp 10.0.0.4:6443: connect: connection refused", "transient"},
		{"timeout wins over a 4xx", "uks API 404: cluster not found (timed out waiting for it to appear)", "transient"},
		{"no status at all", "kubeadm init exited 1", "transient"},
		{"empty", "", "transient"},
		{"a 4xx-looking number that is not a status", "uploaded 422 manifests", "transient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(bash, "-c",
				`. ./reconcile_failure.sh && classify_step_error "$1"`, "classify", tc.error).CombinedOutput()
			if err != nil {
				t.Fatalf("classify_step_error: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("classify_step_error(%q) = %q, want %q", tc.error, got, tc.want)
			}
		})
	}
}

// fakeAnkra answers the two reads retry_failed_reconcile makes from fixture
// files and records every reconcile it is asked to start.
const fakeAnkra = `#!/bin/sh
case "$*" in
  *"operations list"*) cat "$FAKE_LIST" ;;
  *"operations steps"*) cat "$FAKE_STEPS" ;;
  *"cluster reconcile"*) echo reconcile >> "$FAKE_CALLS" ;;
esac
`

// The driver stubs the three helpers lifecycle_systemtest.sh defines the
// same way (minus the per-worker --config and the login-preamble filter).
const retryDriver = `
log() { printf '%s\n' "$*"; }
select_cluster() { :; }
ank() { "$ANKRA_BIN" "$@" 2>&1; }
ank_out() { "$ANKRA_BIN" "$@" 2>/dev/null; }
. ./reconcile_failure.sh
retry_failed_reconcile systest-uks-261002091036 "has a failed reconcile (likely transient)"
echo "rc=$?"
`

func TestRetryFailedReconcile(t *testing.T) {
	bash := bashOrSkip(t)
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	stepsFailedWith := func(stepError string) string {
		return `{"execution":{"id":"exec-new","display_name":"Reconcile","status":"failed","error_excerpt":` +
			jsonString(stepError) + `},"steps":[` +
			`{"id":"s1","name":"uks_validate_credential","status":"success","error_excerpt":null},` +
			`{"id":"s2","name":"uks_create_managed_k8s","status":"failed","error_excerpt":` + jsonString(stepError) + `}]}`
	}
	cases := []struct {
		name          string
		list          string
		steps         string
		wantRC        string
		wantReconcile int
		wantLog       []string
	}{
		{
			name: "newest reconcile refused with a 422: fail now, no retry",
			list: `[{"id":"exec-new","name":"reconcile","display_name":"Reconcile","status":"failed","error_excerpt":` +
				jsonString(uksNetworkRequiredRaw) + `},` +
				`{"id":"exec-old","name":"reconcile","display_name":"Reconcile","status":"failed"}]`,
			steps:         stepsFailedWith(uksNetworkRequiredRaw),
			wantRC:        "rc=1",
			wantReconcile: 0,
			wantLog:       []string{"uks_create_managed_k8s", "Cluster network is required.", "not retrying"},
		},
		{
			name:          "newest reconcile failed with a 5xx: retry",
			list:          `[{"id":"exec-new","name":"reconcile","display_name":"Reconcile","status":"failed"}]`,
			steps:         stepsFailedWith("uks API 503: Service Unavailable"),
			wantRC:        "rc=0",
			wantReconcile: 1,
			wantLog:       []string{"(likely transient) -> retrying"},
		},
		{
			name:          "newest reconcile timed out: retry",
			list:          `[{"id":"exec-new","name":"reconcile","display_name":"Reconcile","status":"critical"}]`,
			steps:         stepsFailedWith("timed out waiting for cluster to become running"),
			wantRC:        "rc=0",
			wantReconcile: 1,
		},
		{
			name: "a 422 a newer reconcile superseded keeps the old retry",
			list: `[{"id":"exec-newer","name":"reconcile","display_name":"Reconcile","status":"running"},` +
				`{"id":"exec-new","name":"reconcile","display_name":"Reconcile","status":"failed"}]`,
			steps:         stepsFailedWith(uksNetworkRequired),
			wantRC:        "rc=0",
			wantReconcile: 1,
		},
		{
			name:          "no failed reconcile: nothing to do",
			list:          `[{"id":"exec-new","name":"reconcile","display_name":"Reconcile","status":"success"},{"id":"x","name":"install_addon","display_name":"Install traefik","status":"failed"}]`,
			steps:         `{}`,
			wantRC:        "rc=0",
			wantReconcile: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "ankra")
			files := map[string]string{bin: fakeAnkra, filepath.Join(dir, "list.json"): tc.list, filepath.Join(dir, "steps.json"): tc.steps}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			calls := filepath.Join(dir, "calls")
			cmd := exec.Command(bash, "-c", retryDriver)
			cmd.Env = append(os.Environ(),
				"ANKRA_BIN="+bin,
				"FAKE_LIST="+filepath.Join(dir, "list.json"),
				"FAKE_STEPS="+filepath.Join(dir, "steps.json"),
				"FAKE_CALLS="+calls,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			output := string(out)
			if !strings.Contains(output, tc.wantRC) {
				t.Fatalf("want %s, got:\n%s", tc.wantRC, output)
			}
			recorded, _ := os.ReadFile(calls)
			if got := strings.Count(string(recorded), "reconcile"); got != tc.wantReconcile {
				t.Fatalf("reconciles started = %d, want %d\n%s", got, tc.wantReconcile, output)
			}
			for _, want := range tc.wantLog {
				if !strings.Contains(output, want) {
					t.Fatalf("log lacks %q:\n%s", want, output)
				}
			}
		})
	}
}

func jsonString(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
