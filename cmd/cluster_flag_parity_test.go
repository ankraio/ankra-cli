package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The parity table (ankra-v3qdto.19, ankra-cli#760): every way of naming the
// cluster that worked before --cluster could stand in for the cluster
// argument still sends the same requests and exits with the same code.
// Rows that worked are the --cluster name, the --cluster id, a --cluster that
// names no cluster, the argument, the argument beside the same --cluster, and
// the argument beside a different --cluster, the last refused with exit 2
// (ankra-oznf7k.10). One row per way the CLI resolves a cluster.
//
// Each row runs the real entry point, Execute with the wrappers it installs,
// in a child process (this test binary again) against a fake Ankra API, and
// uses nothing else from this package. So the table runs unchanged on a tree
// from before the resolver, which is how it was red-proofed: it passes there
// too.

const (
	parityChildArgumentsEnv = "ANKRA_CLUSTER_FLAG_PARITY_ARGS"
	parityDemoID            = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"
	parityOtherID           = "9d3e2c1b-0a4f-4e5d-8c7b-6a5f4e3d2c1b"
)

// TestClusterFlagParityChildProcess is the child: it runs `ankra <args>`
// through Execute, which exits with the command's code, and does nothing in
// a normal test run.
func TestClusterFlagParityChildProcess(t *testing.T) {
	encoded := os.Getenv(parityChildArgumentsEnv)
	if encoded == "" {
		t.Skip("runs only as the child of TestClusterFlagFormsBehaveAsBefore")
	}
	var arguments []string
	if decodeError := json.Unmarshal([]byte(encoded), &arguments); decodeError != nil {
		_, _ = os.Stderr.WriteString("parity child: " + decodeError.Error() + "\n")
		os.Exit(99)
	}
	apiClient = nil
	os.Args = append([]string{"ankra"}, arguments...)
	Execute()
	os.Exit(0)
}

// parityAPI is a fake Ankra API with two Hetzner clusters, demo and other,
// that records every request it gets.
type parityAPI struct {
	mutex    sync.Mutex
	requests []string
}

func (api *parityAPI) recorded() []string {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return append([]string{}, api.requests...)
}

func (api *parityAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// The audit trail is read for a window ending now, so its query is
	// left out.
	recorded := request.Method + " " + request.URL.RequestURI()
	if strings.Contains(request.URL.Path, "audit-logs") {
		recorded = request.Method + " " + request.URL.Path
	}
	api.mutex.Lock()
	api.requests = append(api.requests, recorded)
	api.mutex.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	path := request.URL.Path
	answer := func(body any) { _ = json.NewEncoder(writer).Encode(body) }
	switch {
	case request.Method == http.MethodGet && path == "/api/v1/clusters":
		query := request.URL.Query()
		clusters := []map[string]string{}
		for _, cluster := range []map[string]string{
			{"id": parityDemoID, "name": "demo", "kind": "hetzner"},
			{"id": parityOtherID, "name": "other", "kind": "hetzner"},
		} {
			if name := query.Get("name"); name != "" && !strings.Contains(cluster["name"], name) {
				continue
			}
			if id := query.Get("cluster_id"); id != "" && cluster["id"] != id {
				continue
			}
			clusters = append(clusters, cluster)
		}
		answer(map[string]any{"result": clusters,
			"pagination": map[string]int{"total_count": len(clusters), "total_pages": 1, "page": 1, "page_size": 100}})
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/reconcile"):
		answer(map[string]int{"created_operations": 1})
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/provision"):
		answer(map[string]string{"marked_to_start_at": "2026-11-09T00:00:00Z"})
	case request.Method == http.MethodPatch && strings.HasSuffix(path, "/environment"):
		answer(map[string]string{"environment": "staging", "criticality": "staging"})
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/dns-zone"):
		answer(map[string]any{"success": true, "fqdn": "demo.org1234.ankra.cc", "state": "active"})
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/node-groups"):
		answer(map[string]any{"node_groups": []any{}})
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/ssh-keys"):
		answer(map[string]any{})
	case request.Method == http.MethodGet && (strings.Contains(path, "/gitops/") || strings.Contains(path, "audit-logs")):
		answer(map[string]any{})
	case request.Method == http.MethodPost && path == "/api/v1/org/cluster-meshes/readiness":
		answer(map[string]any{"readiness": map[string]any{
			parityDemoID: map[string]any{"ready": true}, parityOtherID: map[string]any{"ready": true}}})
	default:
		writer.WriteHeader(http.StatusNotFound)
		answer(map[string]string{"detail": "the parity fake does not serve " + request.Method + " " + path})
	}
}

// parityRun is one child run: what it sent, printed and exited with.
type parityRun struct {
	requests []string
	stdout   string
	stderr   string
	exitCode int
}

func runParityChild(t *testing.T, selected bool, arguments []string) parityRun {
	t.Helper()
	api := &parityAPI{}
	server := httptest.NewServer(api)
	defer server.Close()

	home := t.TempDir()
	if selected {
		if mkdirError := os.MkdirAll(filepath.Join(home, ".ankra"), 0o700); mkdirError != nil {
			t.Fatal(mkdirError)
		}
		selection := `{"id":"` + parityDemoID + `","name":"demo","kind":"hetzner"}`
		if writeError := os.WriteFile(filepath.Join(home, ".ankra", "selected.json"), []byte(selection), 0o600); writeError != nil {
			t.Fatal(writeError)
		}
	}
	encoded, _ := json.Marshal(arguments)
	child := exec.Command(os.Args[0], "-test.run=^TestClusterFlagParityChildProcess$", "-test.count=1")
	child.Env = []string{
		parityChildArgumentsEnv + "=" + string(encoded),
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"ANKRA_API_TOKEN=parity-token",
		"ANKRA_BASE_URL=" + server.URL,
		"ANKRA_ALLOW_INSECURE_HTTP=1",
		"DO_NOT_TRACK=1",
		"NO_COLOR=1",
	}
	child.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	child.Stdout = &stdout
	child.Stderr = &stderr
	runError := child.Run()
	exitCode := 0
	var exitError *exec.ExitError
	switch {
	case errors.As(runError, &exitError):
		exitCode = exitError.ExitCode()
	case runError != nil:
		t.Fatalf("running the child: %v", runError)
	}
	return parityRun{requests: api.recorded(), stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

func TestClusterFlagFormsBehaveAsBefore(t *testing.T) {
	byName := func(name string) string {
		return "GET /api/v1/clusters?name=" + name + "&page=1&page_size=100"
	}
	byID := func(id string) string {
		return "GET /api/v1/clusters?cluster_id=" + id + "&page=1&page_size=1"
	}
	const everyCluster = "GET /api/v1/clusters?page=1&page_size=100"
	reconcile := "POST /api/v1/org/clusters/imported/" + parityDemoID + "/reconcile"
	relabel := "PATCH /api/v1/org/clusters/imported/" + parityDemoID + "/environment"
	provision := "POST /api/v1/clusters/" + parityDemoID + "/provision"
	nodeGroups := "GET /api/v1/clusters/hetzner/" + parityDemoID + "/node-groups"
	sshKeys := "GET /api/v1/clusters/hetzner/" + parityDemoID + "/ssh-keys"
	dnsZone := "GET /api/v1/clusters/" + parityDemoID + "/dns-zone"
	const readiness = "POST /api/v1/org/cluster-meshes/readiness"
	const twoClusters = "name different clusters"

	for _, row := range []struct {
		name     string
		selected bool
		args     []string
		requests []string
		exitCode int
		// stdout and stderr are substrings each stream must hold.
		stdout, stderr string
	}{
		// A command whose cluster may be left out reads --cluster itself,
		// by name or by id (resolveActiveCluster), and exits 3 for one
		// that does not exist.
		{name: "reconcile --cluster name", args: []string{"cluster", "reconcile", "--cluster", "demo"},
			requests: []string{byName("demo"), reconcile}, stdout: "Triggering reconciliation for cluster: demo"},
		{name: "reconcile --cluster id", args: []string{"cluster", "reconcile", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), reconcile}, stdout: "Triggering reconciliation for cluster: demo"},
		{name: "reconcile --cluster missing", args: []string{"cluster", "reconcile", "--cluster", "gone"},
			requests: []string{byName("gone")}, exitCode: 3, stderr: `cluster "gone" not found`},
		{name: "reconcile argument", args: []string{"cluster", "reconcile", "demo"},
			requests: []string{byName("demo"), reconcile}},
		{name: "reconcile argument missing", args: []string{"cluster", "reconcile", "gone"},
			requests: []string{byName("gone")}, exitCode: 1, stderr: "finding cluster gone"},
		{name: "reconcile argument and the same --cluster", args: []string{"cluster", "reconcile", "demo", "--cluster", "demo"},
			requests: []string{byName("demo"), reconcile}},
		{name: "reconcile argument and another --cluster", args: []string{"cluster", "reconcile", "demo", "--cluster", "other"},
			exitCode: 2, stderr: twoClusters},
		{name: "reconcile the selected cluster", selected: true, args: []string{"cluster", "reconcile"},
			requests: []string{reconcile}},

		// The same through resolveClusterFromArgsWithKind (update, move,
		// deprovision): the --cluster lookup, then the kind by name.
		{name: "update --cluster name", args: []string{"cluster", "update", "--cluster", "demo", "--environment", "staging"},
			requests: []string{byName("demo"), byName("demo"), relabel}, stdout: "environment set to 'staging'"},
		{name: "update --cluster id", args: []string{"cluster", "update", "--cluster", parityDemoID, "--environment", "staging"},
			requests: []string{byID(parityDemoID), byName("demo"), relabel}},
		{name: "update --cluster missing", args: []string{"cluster", "update", "--cluster", "gone", "--environment", "staging"},
			requests: []string{byName("gone")}, exitCode: 3, stderr: `cluster "gone" not found`},
		{name: "update argument", args: []string{"cluster", "update", "demo", "--environment", "staging"},
			requests: []string{byID("demo"), byName("demo"), relabel}},
		{name: "update argument and the same --cluster",
			args:     []string{"cluster", "update", "demo", "--cluster", "demo", "--environment", "staging"},
			requests: []string{byID("demo"), byName("demo"), relabel}},
		{name: "update argument and another --cluster",
			args:     []string{"cluster", "update", "demo", "--cluster", "other", "--environment", "staging"},
			exitCode: 2, stderr: twoClusters},

		// provision, through resolveClusterFromArgs.
		{name: "provision --cluster id", args: []string{"cluster", "provision", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), provision}, stdout: "Provisioning cluster: demo"},
		{name: "provision --cluster missing", args: []string{"cluster", "provision", "--cluster", "gone"},
			requests: []string{byName("gone")}, exitCode: 3},
		{name: "provision argument", args: []string{"cluster", "provision", "demo"},
			requests: []string{byName("demo"), provision}},

		// The other commands that read --cluster themselves, which looked
		// an id up as a name for a while (ankra-cli#760 review).
		{name: "audit-logs --cluster id", args: []string{"cluster", "audit-logs", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), "GET /api/v1/org/organisation/audit-logs/export"}},
		{name: "gitops status --cluster id", args: []string{"cluster", "gitops", "status", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), "GET /api/v1/clusters/" + parityDemoID + "/gitops/status"}},
		{name: "gitops conflicts list --cluster id",
			args: []string{"cluster", "gitops", "conflicts", "list", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), "GET /api/v1/org/clusters/" + parityDemoID + "/gitops/conflicts",
				"GET /api/v1/clusters/" + parityDemoID + "/gitops/status"}},
		{name: "gitops preserved list --cluster id",
			args:     []string{"cluster", "gitops", "preserved", "list", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), "GET /api/v1/org/clusters/" + parityDemoID + "/gitops/preserved-paths"}},
		// A --cluster that names no cluster exits 3 on each of them.
		{name: "audit-logs --cluster missing", args: []string{"cluster", "audit-logs", "--cluster", "gone"},
			requests: []string{byName("gone")}, exitCode: 3},
		{name: "gitops status --cluster missing", args: []string{"cluster", "gitops", "status", "--cluster", "gone"},
			requests: []string{byName("gone")}, exitCode: 3},
		{name: "gitops conflicts list --cluster missing",
			args: []string{"cluster", "gitops", "conflicts", "list", "--cluster", "gone"}, requests: []string{byName("gone")}, exitCode: 3},
		{name: "gitops preserved list --cluster missing",
			args: []string{"cluster", "gitops", "preserved", "list", "--cluster", "gone"}, requests: []string{byName("gone")}, exitCode: 3},
		{name: "deprovision --cluster missing", args: []string{"cluster", "deprovision", "--cluster", "gone", "--yes"},
			requests: []string{byName("gone")}, exitCode: 3},
		{name: "move --cluster missing",
			args:     []string{"cluster", "move", "--cluster", "gone", "--organisation", "acme", "--yes"},
			requests: []string{byName("gone")}, exitCode: 3},

		// node-group list, through resolveOptionalClusterArg.
		{name: "node-group list --cluster name", args: []string{"cluster", "node-group", "list", "--cluster", "demo"},
			requests: []string{everyCluster, byID(parityDemoID), nodeGroups}, stdout: "No node groups found."},
		{name: "node-group list --cluster id", args: []string{"cluster", "node-group", "list", "--cluster", parityDemoID},
			requests: []string{byID(parityDemoID), nodeGroups}},
		{name: "node-group list argument", args: []string{"cluster", "node-group", "list", "demo"},
			requests: []string{everyCluster, byID(parityDemoID), nodeGroups}},
		{name: "node-group list argument and another --cluster",
			args: []string{"cluster", "node-group", "list", "demo", "--cluster", "other"}, exitCode: 2, stderr: twoClusters},

		// A required cluster argument (resolveClusterArg).
		{name: "ssh-keys get argument", args: []string{"cluster", "ssh-keys", "get", "demo"},
			requests: []string{everyCluster, byID(parityDemoID), sshKeys}},
		{name: "ssh-keys get argument and the same --cluster", args: []string{"cluster", "ssh-keys", "get", "demo", "--cluster", "demo"},
			requests: []string{everyCluster, byID(parityDemoID), sshKeys}},
		{name: "ssh-keys get argument and another --cluster",
			args: []string{"cluster", "ssh-keys", "get", "demo", "--cluster", "other"}, exitCode: 2, stderr: twoClusters},
		// The same cluster in both places is the argument, counted by the
		// command's own check: the group is missing, nothing is deleted.
		{name: "node-group delete with only the cluster, twice",
			args: []string{"cluster", "node-group", "delete", "demo", "--cluster", "demo"}, exitCode: 2,
			stderr: "accepts 2 arg(s), received 1"},

		// A list of clusters.
		{name: "mesh readiness arguments", args: []string{"cluster", "mesh", "readiness", "demo", "other"},
			requests: []string{everyCluster, everyCluster, readiness}},
		{name: "mesh readiness arguments and the first as --cluster",
			args:     []string{"cluster", "mesh", "readiness", "demo", "other", "--cluster", "demo"},
			requests: []string{everyCluster, everyCluster, readiness}},
		{name: "mesh readiness argument and another --cluster",
			args: []string{"cluster", "mesh", "readiness", "other", "--cluster", "demo"}, exitCode: 2, stderr: twoClusters},
		{name: "bastion allowed-ips argument and another --cluster",
			args: []string{"cluster", "hetzner", "bastion", "allowed-ips", "other", "--cluster", "demo"}, exitCode: 2,
			stderr: twoClusters},

		// Commands that read their argument and --cluster themselves.
		{name: "select --cluster name", args: []string{"cluster", "select", "--cluster", "demo"},
			requests: []string{byName("demo")}, stdout: "Selected cluster: demo is now active."},
		{name: "select argument and the same --cluster", args: []string{"cluster", "select", "demo", "--cluster", "demo"},
			requests: []string{byName("demo")}, stdout: "Selected cluster: demo is now active."},
		{name: "select argument and another --cluster", args: []string{"cluster", "select", "demo", "--cluster", "other"},
			exitCode: 2, stderr: twoClusters},
		{name: "kubeconfig add argument and another --cluster",
			args: []string{"cluster", "kubeconfig", "add", "demo", "--cluster", "other"}, exitCode: 2, stderr: twoClusters},

		// domain names the cluster it resolved without an argument.
		{name: "domain --cluster name", args: []string{"cluster", "domain", "--cluster", "demo"},
			requests: []string{byName("demo"), dnsZone}, stdout: "demo.org1234.ankra.cc", stderr: "Cluster: demo"},
		{name: "domain argument", args: []string{"cluster", "domain", "demo"},
			requests: []string{everyCluster, dnsZone}, stdout: "demo.org1234.ankra.cc"},
		{name: "domain the selected cluster", selected: true, args: []string{"cluster", "domain"},
			requests: []string{dnsZone}, stderr: "Cluster: demo"},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Each row is its own child process with its own fake API and
			// HOME, and touches no state of this process, so the rows run
			// side by side. One after another they were ~0.6s each, the
			// slowest test in the package (ankra-hq41xb.28).
			t.Parallel()
			run := runParityChild(t, row.selected, row.args)
			if run.exitCode != row.exitCode || !slices.Equal(run.requests, row.requests) ||
				!strings.Contains(run.stdout, row.stdout) || !strings.Contains(run.stderr, row.stderr) {
				t.Errorf("ankra %s\n exit %d, want %d\n requests %q\n want     %q\n stdout must hold %q:\n%s\n stderr must hold %q:\n%s",
					strings.Join(row.args, " "), run.exitCode, row.exitCode, run.requests, row.requests,
					row.stdout, run.stdout, row.stderr, run.stderr)
			}
		})
	}
}
