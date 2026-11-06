package client

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const guardTestClusterID = "1fa7c8a4-5e42-4fd5-abff-15500899eda4"

// newCountingServer is an API stand-in that counts the requests reaching it,
// so a test can prove a refused request never left the client. handler nil
// answers 200 with an empty JSON object.
func newCountingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server, hits
}

func mustParseGuardURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, parseError := url.Parse(raw)
	if parseError != nil {
		t.Fatalf("url.Parse(%q): %v", raw, parseError)
	}
	return parsed
}

// The segments the platform's edge rewrites before routing (probed
// 2026-11-06): dot segments, percent-encoded in any case or mix, an empty
// segment it merges away, and the trailing slash no route has.
func TestCheckRequestPathRefusesSegmentsTheEdgeRewrites(t *testing.T) {
	nodeGroups := "/api/v1/clusters/hetzner/" + guardTestClusterID + "/node-groups/"
	for _, testCase := range []struct {
		path     string
		segment  string
		decoded  string
		trailing bool
	}{
		{path: nodeGroups + "..", segment: ".."},
		{path: nodeGroups + ".", segment: "."},
		{path: nodeGroups + "%2e%2e", segment: "%2e%2e", decoded: ".."},
		{path: nodeGroups + "%2E%2E", segment: "%2E%2E", decoded: ".."},
		{path: nodeGroups + "%2E%2e", segment: "%2E%2e", decoded: ".."},
		{path: nodeGroups + ".%2e", segment: ".%2e", decoded: ".."},
		{path: nodeGroups + "%2E.", segment: "%2E.", decoded: ".."},
		{path: nodeGroups + "%2e", segment: "%2e", decoded: "."},
		{path: nodeGroups + "workers/nodes/..", segment: ".."},
		{path: nodeGroups + "../nodes/x", segment: ".."},
		{path: "/api/v1/clusters/hetzner//node-groups/workers"},
		{path: "/api/v1//clusters"},
		{path: nodeGroups, trailing: true},
		{path: nodeGroups + "workers/nodes/", trailing: true},
	} {
		refusalError := checkRequestPath(http.MethodDelete, mustParseGuardURL(t, "https://platform.example"+testCase.path))
		var refusal *UnsafeRequestPathError
		if !errors.As(refusalError, &refusal) {
			t.Errorf("%s: got %v, want an *UnsafeRequestPathError", testCase.path, refusalError)
			continue
		}
		if refusal.Method != http.MethodDelete || refusal.Path != testCase.path || refusal.Segment != testCase.segment ||
			refusal.Decoded != testCase.decoded || refusal.Trailing != testCase.trailing {
			t.Errorf("%s: refusal = %+v, want segment %q decoded %q trailing %v", testCase.path, *refusal,
				testCase.segment, testCase.decoded, testCase.trailing)
		}
	}
}

// What the edge leaves alone passes: names with dots that are not dot
// segments, an escaped '/' (the edge does not decode %2F, so a providerID
// stays one segment), the root, and anything in the query.
func TestCheckRequestPathAllowsOrdinaryPaths(t *testing.T) {
	nodeGroups := "/api/v1/clusters/hetzner/" + guardTestClusterID + "/node-groups/"
	for _, path := range []string{
		"",
		"/",
		"/api/v1/clusters",
		nodeGroups + "workers",
		nodeGroups + "a%2Fb",
		nodeGroups + "opensearch/nodes/hcloud:%2F%2F168428300",
		nodeGroups + "workers/nodes/ip-10-0-0-1.eu-west-1.compute.internal",
		nodeGroups + "...",
		nodeGroups + "%2e%2e%2e",
		nodeGroups + ".hidden",
		nodeGroups + "%252e%252e",
		nodeGroups + "%252e",
		nodeGroups + "a..b",
		"/api/v1/org/stack-profiles/base/versions/1.2.3",
		"/api/v1/org/files?path=../../etc&next=/",
	} {
		if refusalError := checkRequestPath(http.MethodGet, mustParseGuardURL(t, "https://platform.example"+path)); refusalError != nil {
			t.Errorf("%q: unexpected refusal %v", path, refusalError)
		}
	}
}

// An opaque URL carries its path in Opaque. "//host/path" is sent in
// absolute form, so the authority's leading "//" is not an empty segment;
// the path after it is checked like any other.
func TestCheckRequestPathReadsAnOpaqueURLsPath(t *testing.T) {
	for opaque, wantPath := range map[string]string{
		"//platform.example/api/v1/clusters":                    "",
		"//platform.example":                                    "",
		"/api/v1/clusters":                                      "",
		"//platform.example/api/v1/clusters/x/node-groups/..":   "/api/v1/clusters/x/node-groups/..",
		"//platform.example/api/v1/clusters/x/node-groups/%2e.": "/api/v1/clusters/x/node-groups/%2e.",
		"/api/v1//clusters":                                     "/api/v1//clusters",
	} {
		requestURL := &url.URL{Scheme: "https", Host: "platform.example", Opaque: opaque}
		refusalError := checkRequestPath(http.MethodGet, requestURL)
		if wantPath == "" {
			if refusalError != nil {
				t.Errorf("%q: unexpected refusal %v", opaque, refusalError)
			}
			continue
		}
		var refusal *UnsafeRequestPathError
		if !errors.As(refusalError, &refusal) || refusal.Path != wantPath {
			t.Errorf("%q: got %v, want a refusal of %s", opaque, refusalError, wantPath)
		}
	}
}

// The edge decodes a segment once, so the client's rule does too: what is a
// dot after one decoding is unsafe, and what is still escaped is not.
func TestUnsafePathSegmentDecodesOnceLikeTheEdge(t *testing.T) {
	for segment, want := range map[string]bool{
		"": true, ".": true, "..": true, "%2e": true, "%2E%2e": true, ".%2e": true,
		"%252e": false, "%252e%252e": false, "...": false, "%2e%2e%2e": false, "a..b": false,
		"workers": false, "hcloud:%2F%2F1": false, "%zz": false, " ": false,
	} {
		if _, unsafe := UnsafePathSegment(segment); unsafe != want {
			t.Errorf("UnsafePathSegment(%q) unsafe = %v, want %v", segment, unsafe, want)
		}
	}
}

func TestUnsafeRequestPathErrorSaysWhatIsWrongInPlainWords(t *testing.T) {
	nodeGroups := "/api/v1/clusters/hetzner/" + guardTestClusterID + "/node-groups/"
	for path, want := range map[string]string{
		nodeGroups + "..": "refusing to send DELETE " + nodeGroups + "..: a path segment is '..', " +
			"which the platform's edge would resolve to a different route",
		nodeGroups + "%2E%2e": "refusing to send DELETE " + nodeGroups + "%2E%2e: a path segment is '%2E%2e', " +
			"which decodes to '..', and the platform's edge would resolve it to a different route",
		"/api/v1/clusters/hetzner//node-groups/x": "refusing to send DELETE /api/v1/clusters/hetzner//node-groups/x: " +
			"a path segment is empty ('//'), which the platform's edge would merge away, sending the request " +
			"to a different route (an argument is probably blank)",
		nodeGroups: "refusing to send DELETE " + nodeGroups + ": the path ends in '/', so its last segment is empty " +
			"(an argument is probably blank), and no platform route ends in '/'",
	} {
		refusalError := checkRequestPath(http.MethodDelete, mustParseGuardURL(t, "https://platform.example"+path))
		if refusalError == nil || refusalError.Error() != want {
			t.Errorf("%s:\n got %v\nwant %s", path, refusalError, want)
		}
	}
}

type nodeGroupDeleteBuilder func(c *Client, ctx context.Context, clusterID, groupName string,
	drainOptions DrainOptions, wait bool) (*DeleteNodeGroupResult, bool, error)

// nodeGroupDeleteBuilders is every provider's node-group delete, keyed by
// the cluster kind in its path.
func nodeGroupDeleteBuilders() map[string]nodeGroupDeleteBuilder {
	return map[string]nodeGroupDeleteBuilder{
		"hetzner":      (*Client).DeleteHetznerNodeGroup,
		"ovh":          (*Client).DeleteOvhNodeGroup,
		"upcloud":      (*Client).DeleteUpcloudNodeGroup,
		"digitalocean": (*Client).DeleteDigitaloceanNodeGroup,
		"scaleway":     (*Client).DeleteScalewayNodeGroup,
		"ankracloud":   (*Client).DeleteAnkraCloudNodeGroup,
		"aws":          (*Client).DeleteAwsNodeGroup,
		"proxmox":      (*Client).DeleteProxmoxNodeGroup,
		"morpheus":     (*Client).DeleteMorpheusNodeGroup,
	}
}

// ankra-6pi023: `node-group delete <cluster> ..` reached the cluster's own
// DELETE, the deprovision, because the edge resolves the dot segment. Every
// provider's delete refuses such a group name, and a blank one, before
// sending anything, on both the fire-and-forget and the --wait lane. One
// server serves the whole test.
func TestNodeGroupDeleteBuildersRefuseDotAndBlankGroupNames(t *testing.T) {
	server, hits := newCountingServer(t, nil)
	testClient := New(testToken, server.URL)
	for kind, deleteNodeGroup := range nodeGroupDeleteBuilders() {
		for _, groupName := range []string{"..", ".", ""} {
			for _, wait := range []bool{false, true} {
				_, submitted, deleteError := deleteNodeGroup(testClient, context.Background(), guardTestClusterID,
					groupName, DrainOptions{ForceDrain: true}, wait)
				var refusal *UnsafeRequestPathError
				if !errors.As(deleteError, &refusal) {
					t.Errorf("%s %q wait=%v: got %v, want an *UnsafeRequestPathError", kind, groupName, wait, deleteError)
					continue
				}
				if refusal.Method != http.MethodDelete || submitted {
					t.Errorf("%s %q wait=%v: refusal %+v submitted=%v", kind, groupName, wait, *refusal, submitted)
				}
			}
		}
	}
	if hits.Load() != 0 {
		t.Errorf("%d request(s) reached the server, want none", hits.Load())
	}
}

// A group name's characters stay inside its own segment on every provider:
// a '/' cannot split it into two segments (the edge does not decode %2F),
// and '?' or '#' cannot end the path early.
func TestNodeGroupDeleteBuildersKeepTheGroupNameOneSegment(t *testing.T) {
	recorder := newRequestRecorder(t, http.StatusAccepted, `{"status":"accepted"}`)
	testClient := New(testToken, recorder.server.URL)
	for kind, deleteNodeGroup := range nodeGroupDeleteBuilders() {
		for groupName, wantSegment := range map[string]string{
			"workers": "workers",
			"a/b":     "a%2Fb",
			"a?b":     "a%3Fb",
			"a#b":     "a%23b",
			// Escaped, a name that spells a dot is a literal name to the
			// edge, which decodes once: it is sent, and no route changes.
			"%2e%2e": "%252e%252e",
		} {
			recorder.reset()
			_, submitted, deleteError := deleteNodeGroup(testClient, context.Background(), guardTestClusterID,
				groupName, DrainOptions{}, false)
			if deleteError != nil || !submitted {
				t.Errorf("%s %q: err=%v submitted=%v, want the delete sent", kind, groupName, deleteError, submitted)
				continue
			}
			wantPath := "/api/v1/clusters/" + kind + "/" + guardTestClusterID + "/node-groups/" + wantSegment
			if method, path := recorder.last(); recorder.count() != 1 || method != http.MethodDelete || path != wantPath {
				t.Errorf("%s %q: %d request(s), last %s %s, want one DELETE %s", kind, groupName, recorder.count(),
					method, path, wantPath)
			}
		}
	}
}

// requestRecorder is one API stand-in a whole test shares: it answers every
// request with the same status and body and remembers each one.
type requestRecorder struct {
	server   *httptest.Server
	mutex    sync.Mutex
	requests []string
}

func newRequestRecorder(t *testing.T, status int, body string) *requestRecorder {
	t.Helper()
	recorder := &requestRecorder{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, r.Method+" "+strings.SplitN(r.RequestURI, "?", 2)[0])
		recorder.mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (recorder *requestRecorder) reset() {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.requests = nil
}

func (recorder *requestRecorder) count() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return len(recorder.requests)
}

// last is the method and path (escaped, as sent) of the latest request.
func (recorder *requestRecorder) last() (string, string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if len(recorder.requests) == 0 {
		return "", ""
	}
	method, path, _ := strings.Cut(recorder.requests[len(recorder.requests)-1], " ")
	return method, path
}

// A node id is one path segment whatever it holds. Unescaped, a read such as
// `cloud-init-log <cluster> 'abc/restart?'` reached POST .../nodes/abc/restart,
// a node restart from a read command with no prompt.
func TestNodeBuildersKeepTheNodeIDOneSegment(t *testing.T) {
	recorder := newRequestRecorder(t, http.StatusOK, `{}`)
	testClient := New(testToken, recorder.server.URL)
	nodes := "/api/v1/clusters/hetzner/" + guardTestClusterID + "/nodes/"
	calls := map[string]struct {
		send   func(nodeID string) error
		method string
		suffix string
	}{
		"get": {func(nodeID string) error {
			_, sendError := testClient.GetHetznerClusterNode(guardTestClusterID, nodeID)
			return sendError
		}, http.MethodGet, ""},
		"restart": {func(nodeID string) error {
			_, sendError := testClient.RestartHetznerClusterNode(guardTestClusterID, nodeID)
			return sendError
		}, http.MethodPost, "/restart"},
		"cloud-init-log": {func(nodeID string) error {
			_, sendError := testClient.HetznerNodeCloudInitLog(guardTestClusterID, nodeID)
			return sendError
		}, http.MethodPost, "/cloud-init-log"},
		"diagnose": {func(nodeID string) error {
			_, sendError := testClient.DiagnoseClusterNode(context.Background(), "hetzner", guardTestClusterID, nodeID)
			return sendError
		}, http.MethodPost, "/diagnose"},
	}
	for name, call := range calls {
		for nodeID, wantSegment := range map[string]string{
			"abc/restart?": "abc%2Frestart%3F",
			"a#b":          "a%23b",
			"a?b=c":        "a%3Fb=c",
			"node-1":       "node-1",
		} {
			recorder.reset()
			_ = call.send(nodeID)
			wantPath := nodes + wantSegment + call.suffix
			if method, path := recorder.last(); recorder.count() != 1 || method != call.method || path != wantPath {
				t.Errorf("%s %q: %d request(s), last %s %s, want one %s %s", name, nodeID, recorder.count(), method,
					path, call.method, wantPath)
			}
		}
	}
}

// apiClientLanes is every *http.Client the package builds to talk to the
// API, keyed by the file and function that build it (the key the source
// scan below uses). The terminal's websocket handshake is the one more API
// lane, checked through OpenPodTerminal itself.
func apiClientLanes(c *Client) map[string][]*http.Client {
	return map[string][]*http.Client{
		"client.go:New":                          {c.HTTP, c.StreamingHTTP},
		"client.go:httpClientForSlowWrite":       {c.httpClientForSlowWrite()},
		"async_write.go:httpClientForAsyncWrite": {c.httpClientForAsyncWrite(true), c.httpClientForAsyncWrite(false)},
		"workspace_runs.go:workspaceStreamHTTP":  {c.workspaceStreamHTTP()},
		"staff_cluster_recovery.go:NewStaffSessionClient": {
			NewStaffSessionClient(c.BaseURL, "staff-session").HTTP,
		},
		"request_path_guard.go:NewGuardedHTTPClient": {NewGuardedHTTPClient(time.Minute)},
	}
}

const terminalLane = "terminal.go:OpenPodTerminal"

// Every lane to the API refuses a path the edge would rewrite, before the
// request leaves: a builder that uses any of them cannot skip the check.
func TestEveryAPIClientLaneRefusesAnUnsafePath(t *testing.T) {
	server, hits := newCountingServer(t, nil)
	testClient := New(testToken, server.URL)
	for lane, httpClients := range apiClientLanes(testClient) {
		for index, httpClient := range httpClients {
			for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPost} {
				request, requestError := http.NewRequest(method,
					server.URL+"/api/v1/clusters/hetzner/"+guardTestClusterID+"/node-groups/..", strings.NewReader("{}"))
				if requestError != nil {
					t.Fatal(requestError)
				}
				response, sendError := httpClient.Do(request)
				if response != nil {
					_ = response.Body.Close()
				}
				var refusal *UnsafeRequestPathError
				if !errors.As(sendError, &refusal) {
					t.Errorf("%s[%d] %s: got %v, want an *UnsafeRequestPathError", lane, index, method, sendError)
				}
			}
		}
	}

	_, terminalError := testClient.OpenPodTerminal(context.Background(), "..",
		PodTerminalRequest{Namespace: "default", PodName: "web-0", ContainerName: "web"})
	var refusal *UnsafeRequestPathError
	if !errors.As(terminalError, &refusal) {
		t.Errorf("%s: got %v, want an *UnsafeRequestPathError", terminalLane, terminalError)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d request(s) reached the server, want none", hits.Load())
	}
}

// The check is about the platform's edge, so a request the shared client
// sends to another host (a presigned storage URL) is not refused.
func TestTheGuardLeavesRequestsToAnotherHostAlone(t *testing.T) {
	apiServer, apiHits := newCountingServer(t, nil)
	storageServer, storageHits := newCountingServer(t, nil)
	testClient := New(testToken, apiServer.URL)
	request, requestError := http.NewRequest(http.MethodGet, storageServer.URL+"/bucket//objects/../x", nil)
	if requestError != nil {
		t.Fatal(requestError)
	}
	response, sendError := testClient.HTTP.Do(request)
	if sendError != nil {
		t.Fatalf("a request to another host was refused: %v", sendError)
	}
	_ = response.Body.Close()
	if storageHits.Load() != 1 || apiHits.Load() != 0 {
		t.Fatalf("storage hits %d, api hits %d, want 1 and 0", storageHits.Load(), apiHits.Load())
	}
}

// A redirect's target comes from the server, not from an argument, so a hop
// to a path the check refuses is followed on every lane, to the API's host or
// another: refusing it would blame a blank argument nobody gave. Only the
// first hop is the client's own path, and that one is still checked.
func TestARedirectHopIsNotRefused(t *testing.T) {
	storageServer, _ := newCountingServer(t, nil)
	hops := map[string]struct{ location, wantPath string }{
		"/api/v1/hop/empty-segment":  {"/target//x", "/target//x"},
		"/api/v1/hop/trailing-slash": {"/target/", "/target/"},
		"/api/v1/hop/another-host":   {storageServer.URL + "/bucket//x/", "/bucket//x/"},
	}
	apiServer, _ := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if hop, isHop := hops[r.URL.Path]; isHop {
			w.Header().Set("Location", hop.location)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	testClient := New(testToken, apiServer.URL)
	for lane, httpClients := range apiClientLanes(testClient) {
		for index, httpClient := range httpClients {
			for start, hop := range hops {
				response, sendError := httpClient.Get(apiServer.URL + start)
				if sendError != nil {
					t.Errorf("%s[%d] %s: %v", lane, index, start, sendError)
					continue
				}
				_ = response.Body.Close()
				if got := response.Request.URL.Path; got != hop.wantPath {
					t.Errorf("%s[%d] %s: ended at %q, want %q", lane, index, start, got, hop.wantPath)
				}
			}
		}
	}
}

// ExchangeGitHubOIDCToken sends with the caller's client, so it checks its
// own request: a base URL whose path the edge would rewrite is refused.
func TestGitHubOIDCExchangeRefusesAnUnsafePath(t *testing.T) {
	server, hits := newCountingServer(t, nil)
	_, exchangeError := ExchangeGitHubOIDCToken(context.Background(), &http.Client{}, server.URL+"/a/..",
		"organisation", "id-token")
	var refusal *UnsafeRequestPathError
	if !errors.As(exchangeError, &refusal) || hits.Load() != 0 {
		t.Fatalf("got %v with %d request(s), want a refusal and none sent", exchangeError, hits.Load())
	}
}

// The guarded lanes keep net/http's defaults (proxy from the environment,
// dial and TLS handshake timeouts, idle connection reaping, HTTP/2) on a
// pool of their own.
func TestGuardedLanesKeepTheDefaultTransportSettings(t *testing.T) {
	defaults := http.DefaultTransport.(*http.Transport)
	for name, httpClient := range map[string]*http.Client{
		"staff session": NewStaffSessionClient("https://platform.example", "staff-session").HTTP,
		"guarded":       NewGuardedHTTPClient(time.Minute),
	} {
		guard, isGuard := httpClient.Transport.(*requestPathGuardTransport)
		if !isGuard {
			t.Fatalf("%s: transport %T, want the path guard", name, httpClient.Transport)
		}
		base, isTransport := guard.base.(*http.Transport)
		if !isTransport || base == defaults {
			t.Fatalf("%s: base %T, want a clone of http.DefaultTransport", name, guard.base)
		}
		if base.Proxy == nil || base.TLSHandshakeTimeout != defaults.TLSHandshakeTimeout ||
			base.IdleConnTimeout != defaults.IdleConnTimeout || base.MaxIdleConns != defaults.MaxIdleConns ||
			base.ForceAttemptHTTP2 != defaults.ForceAttemptHTTP2 || base.DialContext == nil {
			t.Errorf("%s: the base transport lost net/http's defaults", name)
		}
	}
}

// wrappedDefaultTransport is what an instrumented program leaves in
// http.DefaultTransport: a RoundTripper that is not an *http.Transport.
type wrappedDefaultTransport struct {
	http.RoundTripper
}

// cmd builds the login client at package init, so a default transport that
// cannot be cloned must not panic every command at startup: it is used as it
// is, still behind the check.
func TestGuardedLanesSurviveAWrappedDefaultTransport(t *testing.T) {
	server, hits := newCountingServer(t, nil)
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	wrapped := wrappedDefaultTransport{RoundTripper: original}
	http.DefaultTransport = wrapped

	httpClient := NewGuardedHTTPClient(time.Minute)
	guard, isGuard := httpClient.Transport.(*requestPathGuardTransport)
	if !isGuard || guard.base != http.RoundTripper(wrapped) {
		t.Fatalf("transport %T, want the path guard over the wrapped default", httpClient.Transport)
	}
	response, sendError := httpClient.Get(server.URL + "/api/v1/clusters")
	if sendError != nil {
		t.Fatalf("an ordinary request through the wrapped default: %v", sendError)
	}
	_ = response.Body.Close()
	if _, sendError = httpClient.Get(server.URL + "/api/v1/clusters//nodes"); sendError == nil {
		t.Fatal("the wrapped default lost the path check")
	}
	var refusal *UnsafeRequestPathError
	if !errors.As(sendError, &refusal) || hits.Load() != 1 {
		t.Fatalf("got %v with %d request(s), want a refusal and only the ordinary request sent", sendError, hits.Load())
	}
}

type idleClosingTransport struct {
	closed int
}

func (t *idleClosingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

func (t *idleClosingTransport) CloseIdleConnections() { t.closed++ }

type roundTripperOnly struct{}

func (roundTripperOnly) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

// http.Client.CloseIdleConnections reaches the pooled connections through
// the guard, and a base that pools nothing is left alone.
func TestTheGuardForwardsCloseIdleConnections(t *testing.T) {
	base := &idleClosingTransport{}
	(&http.Client{Transport: &requestPathGuardTransport{base: base}}).CloseIdleConnections()
	if base.closed != 1 {
		t.Fatalf("CloseIdleConnections reached the base %d time(s), want 1", base.closed)
	}
	(&requestPathGuardTransport{base: roundTripperOnly{}}).CloseIdleConnections()
}

// httpClientConstructions is every place in this package that builds an
// http.Client, and what it talks to. A client built anywhere else fails
// TestEveryHTTPClientIsAKnownLane: one that talks to the API must ride
// scopeOverrideTransport (or requestPathGuardTransport) and be added to
// apiClientLanes; one that never talks to the API is listed here with the
// host it talks to.
var httpClientConstructions = map[string]string{
	"client.go:New":                                   "api",
	"client.go:httpClientForSlowWrite":                "api",
	"async_write.go:httpClientForAsyncWrite":          "api",
	"workspace_runs.go:workspaceStreamHTTP":           "api",
	"staff_cluster_recovery.go:NewStaffSessionClient": "api",
	"request_path_guard.go:NewGuardedHTTPClient":      "api",
	terminalLane:                                    "api",
	"workspace_runs.go:presignedHTTP":               "presigned vault URLs on the storage host",
	"backup_vault_imports.go:UploadPresignedObject": "presigned vault URLs on the storage host",
}

// httpClientParameters is every function in this package that sends with an
// *http.Client its caller passes in, and why that does not skip the check.
var httpClientParameters = map[string]string{
	"oidc_trust.go:ExchangeGitHubOIDCToken":     "checks its own request with checkRequestPath before sending",
	"csrf.go:doJSONWithClient":                  "called only with the package's own lanes (c.HTTP, httpClientForSlowWrite)",
	"backup_vault_imports.go:putPresigned":      "presigned vault URLs on the storage host",
	"backup_vault_imports.go:uploadMultipart":   "presigned vault URLs on the storage host",
	"backup_vault_imports.go:completeMultipart": "presigned vault URLs on the storage host",
	"backup_vault_imports.go:abortMultipart":    "presigned vault URLs on the storage host",
}

// defaultTransportReader is the one function allowed to read
// http.DefaultTransport: it clones it behind the path check.
const defaultTransportReader = "request_path_guard.go:guardedDefaultTransport"

// The source scan that keeps the lanes above complete, so a new builder
// cannot reach the network around the check: every http.Client the package
// makes (a literal, new(http.Client) or a declared http.Client value) is a
// listed lane, every function that takes a caller's *http.Client is listed,
// nothing uses net/http's package-level client, nothing but
// guardedDefaultTransport reads its default transport, the only websocket
// dial is the terminal's, and a RoundTrip is only ever called by another
// transport. Clients built outside the package are cmd's to list
// (cmd/http_client_lanes_test.go).
func TestEveryHTTPClientIsAKnownLane(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	fileSet := token.NewFileSet()
	seen, seenParameters := map[string]bool{}, map[string]bool{}
	var problems []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, readError := os.ReadFile(file)
		if readError != nil {
			t.Fatal(readError)
		}
		parsed, parseError := parser.ParseFile(fileSet, file, source, 0)
		if parseError != nil {
			t.Fatal(parseError)
		}
		for _, declaration := range parsed.Decls {
			owner, isRoundTrip := declarationOwner(declaration)
			key := file + ":" + owner
			builds := func(position token.Pos, form string) {
				seen[key] = true
				if _, listed := httpClientConstructions[key]; !listed {
					problems = append(problems, fileSet.Position(position).String()+
						": "+form+" built outside the known lanes ("+key+")")
				}
			}
			if function, isFunction := declaration.(*ast.FuncDecl); isFunction {
				for _, parameter := range function.Type.Params.List {
					star, isStar := parameter.Type.(*ast.StarExpr)
					if !isStar || !isPackageSelector(star.X, "http", "Client") {
						continue
					}
					seenParameters[key] = true
					if _, listed := httpClientParameters[key]; !listed {
						problems = append(problems, fileSet.Position(parameter.Pos()).String()+
							": "+key+" sends with a caller's *http.Client; list it in httpClientParameters with "+
							"why that does not skip the path check")
					}
				}
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CompositeLit:
					if isPackageSelector(typed.Type, "http", "Client") {
						builds(typed.Pos(), "an http.Client")
					}
				case *ast.ValueSpec:
					if typed.Type != nil && isPackageSelector(typed.Type, "http", "Client") {
						builds(typed.Pos(), "a declared http.Client value")
					}
				case *ast.SelectorExpr:
					for _, name := range []string{"DefaultClient", "Get", "Head", "Post", "PostForm"} {
						if isPackageSelector(typed, "http", name) {
							problems = append(problems, fileSet.Position(typed.Pos()).String()+
								": http."+name+" sends around the client's transports")
						}
					}
					if isPackageSelector(typed, "http", "DefaultTransport") && key != defaultTransportReader {
						problems = append(problems, fileSet.Position(typed.Pos()).String()+
							": http.DefaultTransport read outside "+defaultTransportReader+", which clones it behind "+
							"the path check")
					}
					if typed.Sel.Name == "RoundTrip" && !isRoundTrip {
						problems = append(problems, fileSet.Position(typed.Pos()).String()+
							": RoundTrip called outside a transport ("+key+")")
					}
				case *ast.CallExpr:
					if isPackageSelector(typed.Fun, "websocket", "Dial") && key != terminalLane {
						problems = append(problems, fileSet.Position(typed.Pos()).String()+
							": a websocket dial outside "+terminalLane)
					}
					if builtin, isIdent := typed.Fun.(*ast.Ident); isIdent && builtin.Name == "new" &&
						len(typed.Args) == 1 && isPackageSelector(typed.Args[0], "http", "Client") {
						builds(typed.Pos(), "new(http.Client)")
					}
				}
				return true
			})
		}
	}
	for key := range httpClientParameters {
		if !seenParameters[key] {
			problems = append(problems, key+" takes no *http.Client any more: drop it from httpClientParameters")
		}
	}
	for key, talksTo := range httpClientConstructions {
		if !seen[key] {
			problems = append(problems, key+" builds no http.Client any more: drop it from httpClientConstructions")
		}
		_, isLane := apiClientLanes(New(testToken, "https://platform.example"))[key]
		if talksTo == "api" && key != terminalLane && !isLane {
			problems = append(problems, key+" talks to the API but has no entry in apiClientLanes")
		}
	}
	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
}

// declarationOwner names a top-level declaration for the scan: a function or
// method by its name, a package-level variable by its first name. It also
// reports whether the declaration is a RoundTrip method.
func declarationOwner(declaration ast.Decl) (string, bool) {
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		return typed.Name.Name, typed.Recv != nil && typed.Name.Name == "RoundTrip"
	case *ast.GenDecl:
		for _, spec := range typed.Specs {
			if valueSpec, isValue := spec.(*ast.ValueSpec); isValue && len(valueSpec.Names) > 0 {
				return valueSpec.Names[0].Name, false
			}
		}
	}
	return "", false
}

func isPackageSelector(expression ast.Expr, packageName, name string) bool {
	selector, isSelector := expression.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != name {
		return false
	}
	identifier, isIdentifier := selector.X.(*ast.Ident)
	return isIdentifier && identifier.Name == packageName
}

// unescapedPathArguments is every argument the package puts into an API path
// unescaped on purpose, keyed by file and the argument as written: a fixed
// catalog name, a relay sub-path of several segments, a suffix whose own
// segments are escaped where it is built, and a query string.
var unescapedPathArguments = map[string]bool{
	"ankracloud_clusters.go catalog":         true,
	"aws_clusters.go catalog":                true,
	"morpheus_clusters.go catalog":           true,
	"proxmox_clusters.go catalog":            true,
	"scaleway_clusters.go catalog":           true,
	"kubernetes.go relayPath":                true,
	"mcp_servers.go suffix":                  true,
	"secret_slots.go suffix":                 true,
	"cost_trend.go costTrendDaysQuery(days)": true,
}

// ankra-npa652: an argument pasted into a path unescaped can change the
// route ('/' splits it, '?' and '#' end the path), so a read such as
// cloud-init-log reached a node restart. Every argument in a path position
// of an fmt.Sprintf or a '+' chain that builds an /api or /admin URL is
// url.PathEscape'd (or QueryEscape'd), or listed above.
func TestEveryPathArgumentIsEscaped(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	verb := regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)
	fileSet := token.NewFileSet()
	var problems []string
	seen := map[string]bool{}
	report := func(file string, argument ast.Expr) {
		var written strings.Builder
		_ = printer.Fprint(&written, fileSet, argument)
		key := file + " " + written.String()
		seen[key] = true
		if !unescapedPathArguments[key] {
			problems = append(problems, fileSet.Position(argument.Pos()).String()+": "+written.String()+
				" is put into an API path unescaped; wrap it in url.PathEscape")
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseError := parser.ParseFile(fileSet, file, nil, 0)
		if parseError != nil {
			t.Fatal(parseError)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				selector, isSelector := typed.Fun.(*ast.SelectorExpr)
				if !isSelector || selector.Sel.Name != "Sprintf" || len(typed.Args) < 2 {
					return true
				}
				literal, isLiteral := typed.Args[0].(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					return true
				}
				format, _ := strconv.Unquote(literal.Value)
				if !strings.Contains(format, "/api/") && !strings.Contains(format, "/admin/") {
					return true
				}
				pathEnd := len(format)
				if query := strings.IndexByte(format, '?'); query >= 0 {
					pathEnd = query
				}
				argumentIndex := 1
				for _, location := range verb.FindAllStringIndex(format, -1) {
					text := format[location[0]:location[1]]
					if text == "%%" {
						continue
					}
					if argumentIndex >= len(typed.Args) {
						break
					}
					argument := typed.Args[argumentIndex]
					argumentIndex++
					// The base URL comes first; a number cannot change a route.
					if location[0] == 0 || location[0] >= pathEnd || strings.HasSuffix(text, "d") ||
						isEscapeCall(argument) {
						continue
					}
					report(file, argument)
				}
				return true
			case *ast.BinaryExpr:
				if typed.Op != token.ADD {
					return true
				}
				var operands []ast.Expr
				var flatten func(expression ast.Expr)
				flatten = func(expression ast.Expr) {
					if sum, isSum := expression.(*ast.BinaryExpr); isSum && sum.Op == token.ADD {
						flatten(sum.X)
						flatten(sum.Y)
						return
					}
					operands = append(operands, expression)
				}
				flatten(typed)
				buildsAPIPath := false
				for _, operand := range operands {
					if literal, isLiteral := operand.(*ast.BasicLit); isLiteral &&
						(strings.Contains(literal.Value, "/api/") || strings.Contains(literal.Value, "/admin/")) {
						buildsAPIPath = true
					}
				}
				if !buildsAPIPath {
					return true
				}
				inQuery := false
				for index, operand := range operands {
					if literal, isLiteral := operand.(*ast.BasicLit); isLiteral {
						inQuery = inQuery || strings.Contains(literal.Value, "?")
						continue
					}
					if index == 0 || inQuery || isEscapeCall(operand) {
						continue
					}
					report(file, operand)
				}
				return false
			}
			return true
		})
	}
	for key := range unescapedPathArguments {
		if !seen[key] {
			problems = append(problems, key+" is no longer put into a path: drop it from unescapedPathArguments")
		}
	}
	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
}

func isEscapeCall(expression ast.Expr) bool {
	call, isCall := expression.(*ast.CallExpr)
	if !isCall {
		return false
	}
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	return isSelector && (selector.Sel.Name == "PathEscape" || selector.Sel.Name == "QueryEscape")
}
