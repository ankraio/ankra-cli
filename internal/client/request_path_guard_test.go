package client

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
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
		{path: nodeGroups + "%252e%252e", segment: "%252e%252e", decoded: ".."},
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
		nodeGroups + "a..b",
		"/api/v1/org/stack-profiles/base/versions/1.2.3",
		"/api/v1/org/files?path=../../etc&next=/",
	} {
		if refusalError := checkRequestPath(http.MethodGet, mustParseGuardURL(t, "https://platform.example"+path)); refusalError != nil {
			t.Errorf("%q: unexpected refusal %v", path, refusalError)
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
// provider's delete refuses such a group name before sending anything, on
// both the fire-and-forget and the --wait lane.
func TestNodeGroupDeleteBuildersRefuseDotAndBlankGroupNames(t *testing.T) {
	for kind, deleteNodeGroup := range nodeGroupDeleteBuilders() {
		for _, groupName := range []string{"..", ".", "", "%2e%2e", "%2E%2e", ".%2E"} {
			for _, wait := range []bool{false, true} {
				server, hits := newCountingServer(t, nil)
				testClient := New(testToken, server.URL)
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
				if hits.Load() != 0 {
					t.Errorf("%s %q wait=%v: %d request(s) reached the server, want none", kind, groupName, wait, hits.Load())
				}
			}
		}
	}
}

// A group name's characters stay inside its own segment on every provider:
// a '/' cannot split it into two segments (the edge does not decode %2F),
// and '?' or '#' cannot end the path early.
func TestNodeGroupDeleteBuildersKeepTheGroupNameOneSegment(t *testing.T) {
	for kind, deleteNodeGroup := range nodeGroupDeleteBuilders() {
		for groupName, wantSegment := range map[string]string{
			"workers": "workers",
			"a/b":     "a%2Fb",
			"a?b":     "a%3Fb",
			"a#b":     "a%23b",
		} {
			var gotMethod, gotPath string
			server, hits := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = strings.SplitN(r.RequestURI, "?", 2)[0]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			})
			testClient := New(testToken, server.URL)
			_, submitted, deleteError := deleteNodeGroup(testClient, context.Background(), guardTestClusterID,
				groupName, DrainOptions{}, false)
			if deleteError != nil || !submitted {
				t.Errorf("%s %q: err=%v submitted=%v, want the delete sent", kind, groupName, deleteError, submitted)
				continue
			}
			wantPath := "/api/v1/clusters/" + kind + "/" + guardTestClusterID + "/node-groups/" + wantSegment
			if hits.Load() != 1 || gotMethod != http.MethodDelete || gotPath != wantPath {
				t.Errorf("%s %q: %d request(s), last %s %s, want one DELETE %s", kind, groupName, hits.Load(),
					gotMethod, gotPath, wantPath)
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
	terminalLane:                                    "api",
	"workspace_runs.go:presignedHTTP":               "presigned vault URLs on the storage host",
	"backup_vault_imports.go:UploadPresignedObject": "presigned vault URLs on the storage host",
}

// The source scan that keeps the lanes above complete, so a new builder
// cannot reach the network around the check: every http.Client literal in
// the package is a listed lane, nothing uses net/http's package-level
// client or transport, the only websocket dial is the terminal's, and a
// RoundTrip is only ever called by another transport.
func TestEveryHTTPClientIsAKnownLane(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	fileSet := token.NewFileSet()
	seen := map[string]bool{}
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
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CompositeLit:
					if isPackageSelector(typed.Type, "http", "Client") {
						seen[key] = true
						if _, listed := httpClientConstructions[key]; !listed {
							problems = append(problems, fileSet.Position(typed.Pos()).String()+
								": an http.Client built outside the known lanes ("+key+")")
						}
					}
				case *ast.SelectorExpr:
					for _, name := range []string{"DefaultClient", "DefaultTransport", "Get", "Head", "Post", "PostForm"} {
						if isPackageSelector(typed, "http", name) {
							problems = append(problems, fileSet.Position(typed.Pos()).String()+
								": http."+name+" sends around the client's transports")
						}
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
				}
				return true
			})
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
