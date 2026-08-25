// Command mockapi serves just enough of the Ankra platform API to drive the
// recorded README demo (docs/demo.gif) with no account, no credentials and no
// network. It is a prop for the recording, never a test double: nothing in the
// test suite depends on it, and it makes no attempt at fidelity beyond the
// handful of routes docs/demo/driver.sh walks through.
//
// It is deliberately written against the CLI's own internal/client structs
// rather than hand-written JSON. That way a field rename in the real API
// contract breaks `go build ./...` here instead of silently producing a demo
// that records output the CLI can no longer parse.
//
// Every response is fabricated. No customer data, no real cluster, no real
// organisation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ankra/internal/client"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8378", "address to listen on; port 0 picks a free one")
	urlFile := flag.String("url-file", "", "write the resolved base URL here once listening (atomic)")
	flag.Parse()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		// A second recording (or anything else holding the port) must not
		// fail the run; fall back to whatever port is free.
		log.Printf("listen %s: %v - falling back to a free port", *listen, err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
	}

	// Advertise "localhost" rather than 127.0.0.1. `ankra cluster apply`
	// echoes the base URL back in its "View it in the UI" line, so this is
	// the one place the scaffolding shows up on camera; localhost reads as a
	// deliberate local endpoint instead of a stray IP. The CLI treats both as
	// loopback, so plaintext http needs no override either way.
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Fatalf("resolve port: %v", err)
	}
	baseURL := "http://localhost:" + port

	if *urlFile != "" {
		if err := writeAtomic(*urlFile, baseURL+"\n"); err != nil {
			log.Fatalf("write url file: %v", err)
		}
	}
	fmt.Println(baseURL)

	server := &http.Server{
		Handler:           routes(newState()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.Serve(listener); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// writeAtomic renames into place so a reader polling for the file never sees a
// half-written URL.
func writeAtomic(path, contents string) error {
	temp := path + ".tmp"
	if err := os.WriteFile(temp, []byte(contents), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Clean(path))
}

// state holds the one piece of mutable demo state: how many times the
// executions list has been polled. `ankra cluster operations list --watch`
// re-requests on every tick and stops once every execution is terminal, so
// advancing the script per request is what turns a static table into a
// deployment that visibly converges and then ends the watch on its own.
type state struct {
	mu    sync.Mutex
	polls int
}

func newState() *state { return &state{} }

func (s *state) nextPoll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	poll := s.polls
	s.polls++
	return poll
}

const (
	demoClusterID = "0f6f6e2c-7c1a-4a3e-9a63-9b8f2b41d0c7"
	demoOrgID     = "5a1d0c9e-2b44-4f10-9d2e-6c3a8e7b1f55"
)

func routes(s *state) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clusters", handleClusters)
	mux.HandleFunc("GET /api/v1/clusters/{name}", handleClusterInfo)
	mux.HandleFunc("POST /api/v1/clusters/validate", handleValidate)
	mux.HandleFunc("POST /api/v1/clusters/import", handleImport)
	mux.HandleFunc("GET /api/v1/org/executions", s.handleExecutions)
	mux.HandleFunc("GET /api/v1/org/executions/{id}", s.handleExecutionDetail)
	mux.HandleFunc("GET /api/v1/org/clusters/imported/{id}/stacks", handleStacks)
	mux.HandleFunc("GET /api/v1/clusters/{id}/kubernetes/pods", handlePods)
	mux.HandleFunc("POST /api/v1/org/clusters/{id}/kubernetes/chat", handleChat)

	// An unrouted path means the driver grew a command the mock does not
	// cover. Fail loudly rather than letting the CLI render an empty table
	// into the recording.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UNHANDLED %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"detail":"not implemented by the demo mock"}`, http.StatusNotFound)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode response: %v", err)
	}
}

// rfc3339Ago renders a timestamp the CLI's humanize.Time turns into "5 minutes
// ago". Relative to now, so the recording never shows a stale absolute date.
func rfc3339Ago(d time.Duration) string {
	return time.Now().Add(-d).Format(time.RFC3339)
}

func ptr[T any](v T) *T { return &v }

// fleet is the fabricated multi-cloud estate the demo opens on. Four kinds
// across four providers is the point of the first frame: one CLI, one config
// language, wherever the cluster runs.
func fleet() []client.ClusterListItem {
	return []client.ClusterListItem{
		{
			ID: demoClusterID, Name: "staging-eu", State: "online",
			Description: "Pre-production", Environment: "staging",
			OrganisationID: demoOrgID, KubeVersion: "v1.34.1",
			ControlPlanes: 1, Nodes: 4, Kind: "hetzner",
			CreatedAt: rfc3339Ago(19 * 24 * time.Hour), OperationalAt: ptr(rfc3339Ago(19 * 24 * time.Hour)),
		},
		{
			ID: "2b7c1f04-9e3d-4c88-b1a5-77d2e6c4a913", Name: "prod-eu-1", State: "online",
			Description: "Production, Falkenstein", Environment: "production",
			OrganisationID: demoOrgID, KubeVersion: "v1.33.4",
			ControlPlanes: 3, Nodes: 12, Kind: "hetzner",
			CreatedAt: rfc3339Ago(214 * 24 * time.Hour), OperationalAt: ptr(rfc3339Ago(214 * 24 * time.Hour)),
		},
		{
			ID: "8d4a2e61-5b09-4f7c-8e33-1c9b0a5d7e24", Name: "prod-us-1", State: "online",
			Description: "Production, NYC", Environment: "production",
			OrganisationID: demoOrgID, KubeVersion: "v1.33.4",
			ControlPlanes: 3, Nodes: 9, Kind: "digitalocean",
			CreatedAt: rfc3339Ago(160 * 24 * time.Hour), OperationalAt: ptr(rfc3339Ago(160 * 24 * time.Hour)),
		},
		{
			ID: "c1e7b385-0a62-49d4-9f8b-3e5d2c6a4b18", Name: "onprem-lab", State: "online",
			Description: "Imported, on-premise", Environment: "development",
			OrganisationID: demoOrgID, KubeVersion: "v1.34.1",
			ControlPlanes: 3, Nodes: 6, Kind: "imported",
			CreatedAt: rfc3339Ago(41 * 24 * time.Hour), OperationalAt: ptr(rfc3339Ago(41 * 24 * time.Hour)),
		},
	}
}

// handleClusters serves the list, the ?cluster_name= lookup `ankra cluster
// select` uses, and the ?cluster_id= lookup that resolves an active cluster.
func handleClusters(w http.ResponseWriter, r *http.Request) {
	clusters := fleet()
	if name := r.URL.Query().Get("cluster_name"); name != "" {
		clusters = filterClusters(clusters, func(c client.ClusterListItem) bool { return c.Name == name })
	}
	if id := r.URL.Query().Get("cluster_id"); id != "" {
		clusters = filterClusters(clusters, func(c client.ClusterListItem) bool { return c.ID == id })
	}
	writeJSON(w, client.ClusterListResponse{
		Result: clusters,
		// TotalPages must be 1: listAllClusters keeps paging while
		// total_pages exceeds the page it just read.
		Pagination: client.Pagination{
			TotalCount: len(clusters), TotalPages: 1, Page: 1, PageSize: 100,
		},
	})
}

func filterClusters(in []client.ClusterListItem, keep func(client.ClusterListItem) bool) []client.ClusterListItem {
	out := make([]client.ClusterListItem, 0, len(in))
	for _, cluster := range in {
		if keep(cluster) {
			out = append(out, cluster)
		}
	}
	return out
}

func handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, cluster := range fleet() {
		if cluster.Name == name || cluster.ID == name {
			writeJSON(w, cluster)
			return
		}
	}
	http.Error(w, `{"detail":"cluster not found"}`, http.StatusNotFound)
}

// handleValidate answers from the submitted spec rather than a canned string,
// so editing docs/demo/platform.yaml changes what the recording shows instead
// of quietly desynchronising from it.
func handleValidate(w http.ResponseWriter, r *http.Request) {
	var request client.ValidateClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"detail":"unreadable spec"}`, http.StatusBadRequest)
		return
	}
	addons, manifests := 0, 0
	for _, stack := range request.Spec.Stacks {
		addons += len(stack.Addons)
		manifests += len(stack.Manifests)
	}
	log.Printf("validate: %d stack(s), %d addon(s), %d manifest(s)", len(request.Spec.Stacks), addons, manifests)
	writeJSON(w, client.ValidateClusterResponse{
		Errors:   []client.ImportResponseResourceError{},
		Warnings: []client.ValidationWarning{},
	})
}

// handleImport answers the apply. ImportCommand is deliberately empty: the demo
// applies to a cluster that already exists, so the CLI prints the
// "configuration applied, deploys run in the background" wording that leads
// straight into the operations watch.
func handleImport(w http.ResponseWriter, r *http.Request) {
	var request client.CreateImportClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"detail":"unreadable spec"}`, http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("wait") != "true" {
		// Without --wait the client requires exactly 202; any other 2xx is
		// an error to it.
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, client.AsyncWriteAcceptedResponse{Status: "accepted"})
		return
	}
	writeJSON(w, client.ImportResponse{
		Name:      request.Name,
		ClusterId: demoClusterID,
	})
}

// execScript is one row of the operations table and the succeeded-step count it
// reports on each successive poll. The last entry must equal total, or the
// watch never reaches a terminal state and the recording runs forever.
type execScript struct {
	id          string
	displayName string
	execType    string
	total       int
	succeeded   []int
}

func demoExecutions() []execScript {
	return []execScript{
		{
			id: "op-8f21c4", displayName: "Deploy stack: observability", execType: "stack_deploy",
			total: 6, succeeded: []int{0, 1, 3, 4, 5, 6},
		},
		{
			id: "op-3a90de", displayName: "Deploy stack: ingress", execType: "stack_deploy",
			total: 4, succeeded: []int{1, 2, 3, 4, 4, 4},
		},
		{
			id: "op-15b7ae", displayName: "Apply cluster configuration", execType: "cluster_write",
			total: 3, succeeded: []int{3, 3, 3, 3, 3, 3},
		},
	}
}

// summariesAtPoll renders every scripted execution as it stands on the given
// poll.
func summariesAtPoll(poll int) []client.ExecutionSummary {
	clusterID, clusterName := demoClusterID, "staging-eu"

	summaries := make([]client.ExecutionSummary, 0, 3)
	for _, script := range demoExecutions() {
		// Clamp: any poll past the script's end holds the final state, so an
		// extra tick can never rewind the table or restart the watch.
		index := poll
		if index >= len(script.succeeded) {
			index = len(script.succeeded) - 1
		}
		succeeded := script.succeeded[index]

		running, status := 0, "success"
		if succeeded < script.total {
			running, status = 1, "running"
		}
		summaries = append(summaries, client.ExecutionSummary{
			ID: script.id, Scope: "cluster",
			ClusterID: &clusterID, ClusterName: &clusterName,
			OrganisationID: demoOrgID,
			Name:           script.displayName, DisplayName: script.displayName,
			Type: script.execType, Status: status,
			StepSummary: client.StepSummary{
				Total:     script.total,
				Succeeded: succeeded,
				Running:   running,
				Pending:   script.total - succeeded - running,
			},
			CreatedAt: ptr(rfc3339Ago(52 * time.Second)),
			UpdatedAt: ptr(rfc3339Ago(2 * time.Second)),
		})
	}
	return summaries
}

func (s *state) handleExecutions(w http.ResponseWriter, _ *http.Request) {
	summaries := summariesAtPoll(s.nextPoll())
	writeJSON(w, client.ExecutionListResponse{
		Result: summaries,
		Pagination: client.Pagination{
			TotalCount: len(summaries), TotalPages: 1, Page: 1, PageSize: 50,
		},
	})
}

// handleExecutionDetail backs `ankra cluster operations list <id> --watch`,
// which re-reads this route on every tick. Steps are left empty: the watch
// renders the summary counters, not the per-step table.
func (s *state) handleExecutionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, summary := range summariesAtPoll(s.nextPoll()) {
		if summary.ID != id {
			continue
		}
		writeJSON(w, client.ExecutionDetail{
			Execution:   summary,
			Steps:       []client.ExecutionStep{},
			StepSummary: summary.StepSummary,
		})
		return
	}
	http.Error(w, `{"detail":"execution not found"}`, http.StatusNotFound)
}

// handleStacks closes the demo's loop: the same two stacks the recording
// applied from platform.yaml, now deployed, in the wave order the file asked
// for. Keep it in step with docs/demo/platform.yaml - a viewer reads the file
// and this table in the same GIF.
func handleStacks(w http.ResponseWriter, _ *http.Request) {
	addon := func(name, chart, version, namespace string) client.StackAddon {
		return client.StackAddon{
			Name: name, ChartName: chart, ChartVersion: version,
			Namespace: namespace, State: "up",
		}
	}
	stacks := []client.ClusterStackListItem{
		{
			Name: "ingress", Description: "TLS termination and public entry",
			State: "up", DeployWave: ptr(1),
			Manifests: []client.StackManifest{},
			Addons: []client.StackAddon{
				addon("cert-manager", "cert-manager", "v1.19.1", "cert-manager"),
				addon("ingress-nginx", "ingress-nginx", "4.13.3", "ingress"),
			},
		},
		{
			Name: "observability", Description: "Metrics, dashboards and logs",
			State: "up", DeployWave: ptr(2),
			Manifests: []client.StackManifest{},
			Addons: []client.StackAddon{
				addon("loki", "loki", "6.42.0", "observability"),
			},
		},
	}
	writeJSON(w, client.ListClusterStacksResponse{
		Stacks: stacks,
		Pagination: client.Pagination{
			TotalCount: len(stacks), TotalPages: 1, Page: 1, PageSize: 100,
		},
	})
}

// handlePods returns the observability namespace mid-rollout, with loki-0
// wedged in CrashLoopBackOff. That failure is the demo's hand-off into
// `ankra chat`: the table poses the question the AI scene answers.
func handlePods(w http.ResponseWriter, _ *http.Request) {
	namespace := "observability"
	node := func(n string) *string { return &n }
	pods := []client.PodSummary{
		{
			UID: "1", Name: "loki-0", Namespace: &namespace,
			Phase: "CrashLoopBackOff", Ready: "0/1", Restarts: 7,
			NodeName: node("staging-eu-worker-2"), StartTime: ptr(rfc3339Ago(9 * time.Minute)),
		},
		{
			UID: "2", Name: "kube-prometheus-stack-operator-6d4f8c9b7-x2ttl", Namespace: &namespace,
			Phase: "Running", Ready: "1/1", Restarts: 0,
			NodeName: node("staging-eu-worker-1"), StartTime: ptr(rfc3339Ago(8 * time.Minute)),
		},
		{
			UID: "3", Name: "prometheus-kube-prometheus-stack-prometheus-0", Namespace: &namespace,
			Phase: "Running", Ready: "2/2", Restarts: 0,
			NodeName: node("staging-eu-worker-3"), StartTime: ptr(rfc3339Ago(7 * time.Minute)),
		},
		{
			UID: "4", Name: "grafana-7c8b5d9f64-mn4kq", Namespace: &namespace,
			Phase: "Running", Ready: "1/1", Restarts: 0,
			NodeName: node("staging-eu-worker-1"), StartTime: ptr(rfc3339Ago(7 * time.Minute)),
		},
		{
			UID: "5", Name: "promtail-9v6dz", Namespace: &namespace,
			Phase: "Running", Ready: "1/1", Restarts: 0,
			NodeName: node("staging-eu-worker-2"), StartTime: ptr(rfc3339Ago(6 * time.Minute)),
		},
	}
	writeJSON(w, client.ListPodsResponse{
		Pods: pods, TotalCount: len(pods), Page: 1, PageSize: 50, TotalPages: 1,
		CacheInfo:  client.CacheInfo{ServedFromCache: false, SyncStatus: "synced"},
		Namespaces: []string{"default", "ingress", "kube-system", namespace},
	})
}

// chatFrame is one server-sent event plus how long to wait before sending it.
type chatFrame struct {
	event client.ChatStreamEvent
	delay time.Duration
}

// chatScript is the answer to the driver's question about loki-0. Content is
// split into small frames because the CLI prints each one as it arrives: the
// typewriter effect on camera is the wire format, not a rendering trick.
func chatScript() []chatFrame {
	// One status frame, not two: before any content arrives the CLI prints
	// each one as a bare "[...]" with no separator, so consecutive frames run
	// together on a single line.
	frames := []chatFrame{
		{delay: 900 * time.Millisecond, event: client.ChatStreamEvent{
			Type: "status",
			Data: map[string]any{
				"intent":    "Inspecting cluster resources",
				"mechanism": "pod/loki-0, container logs, events",
			},
		}},
	}

	answer := strings.Join([]string{
		"loki-0 is in CrashLoopBackOff because the ingester cannot claim its",
		"write-ahead log directory:",
		"",
		`  level=error msg="failed to create WAL" err="open /loki/wal: permission denied"`,
		"",
		"The chart runs as UID 10001 but podSecurityContext.fsGroup is unset, so the",
		"mounted PVC stays root-owned. All 7 restarts are the same fault - this is not",
		"a memory or scheduling problem.",
		"",
		"Fix it in the stack that owns the addon, not on the live cluster, or the next",
		"deploy reverts it:",
		"",
		"  # platform.yaml -> stack observability -> addon loki",
		"  configuration:",
		"    values: |-",
		"      loki:",
		"        podSecurityContext:",
		"          fsGroup: 10001",
		"",
		"  ankra cluster apply -f platform.yaml",
		"",
	}, "\n")

	for _, chunk := range chunkForTypewriter(answer) {
		frames = append(frames, chatFrame{
			delay: 22 * time.Millisecond,
			event: client.ChatStreamEvent{Type: "content", Data: chunk},
		})
	}
	return frames
}

// chunkForTypewriter splits text on word boundaries, keeping the trailing space
// with each word so the reassembled stream is byte-identical to the input.
func chunkForTypewriter(text string) []string {
	var chunks []string
	var current strings.Builder
	for _, r := range text {
		current.WriteRune(r)
		if r == ' ' || r == '\n' {
			chunks = append(chunks, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func handleChat(w http.ResponseWriter, _ *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"detail":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	for _, frame := range chatScript() {
		time.Sleep(frame.delay)
		payload, err := json.Marshal(frame.event)
		if err != nil {
			log.Printf("marshal chat frame: %v", err)
			return
		}
		// The client only reads lines prefixed with "data: " (with the space).
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return
		}
		flusher.Flush()
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return
	}
	flusher.Flush()
}
