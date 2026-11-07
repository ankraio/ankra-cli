package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// The preserved-path routes are pinned on the request itself: a path, method
// or body typo fails as a 404 or a 422 against a real platform and passes
// every command test that only checks the wiring. The resolve body matters
// most: the platform records a decision only over the two digests it names.
func TestClusterGitopsPreservedPathsLaneParity(t *testing.T) {
	lanes := []struct {
		name       string
		wantMethod string
		wantPath   string
		wantBody   map[string]any
		response   string
		call       func(testClient *Client) (any, error)
		check      func(t *testing.T, got any)
	}{
		{
			name:       "list reads the cluster's preserved paths",
			wantMethod: http.MethodGet,
			wantPath:   "/api/v1/org/clusters/cluster-1/gitops/preserved-paths",
			response: `{"preserved_paths":[{"path":"clusters/prod/prod.yaml","head_content":"","head_content_hash":"h1",` +
				`"render_content":"","render_content_hash":"r1","content_redacted":true,` +
				`"content_redacted_reason":"sensitive_value","content_omitted":false,"head_commit_sha":"abc",` +
				`"detected_at":"2026-11-03T20:22:19.887235Z","last_seen_at":"2026-11-03T21:00:00Z",` +
				`"resolution_choice":null,"resolved_at":null,"import_refused_reason":null,"import_refused_at":null,` +
				`"structural_diff":{"status":"computed","values_withheld_reason":null,"total":111,"truncated":true,` +
				`"changes":[{"path":"spec.stacks[name=web].addons[name=nginx].chart_version","change":"changed",` +
				`"git_value":"1.0.0","ankra_value":"1.1.0","value_masked":false}]}}],` +
				`"total":1,"truncated":false,"structural_diff_values":"revealed"}`,
			call: func(testClient *Client) (any, error) {
				return testClient.ListClusterGitopsPreservedPaths(context.Background(), "cluster-1")
			},
			check: func(t *testing.T, got any) {
				listing := got.(*GitopsPreservedPathList)
				if listing.Total != 1 || listing.StructuralDiffValues != "revealed" || len(listing.PreservedPaths) != 1 {
					t.Fatalf("listing = %+v", listing)
				}
				entry := listing.PreservedPaths[0]
				if entry.Path != "clusters/prod/prod.yaml" || entry.HeadContentHash != "h1" || entry.RenderContentHash != "r1" ||
					!entry.ContentRedacted || entry.ContentRedactedReason == nil || *entry.ContentRedactedReason != "sensitive_value" ||
					entry.ResolutionChoice != nil || entry.DetectedAt == nil {
					t.Fatalf("entry = %+v", entry)
				}
				diff := entry.StructuralDiff
				wantChange := GitopsPreservedPathChange{Path: "spec.stacks[name=web].addons[name=nginx].chart_version",
					Change: "changed", GitValue: strPtr("1.0.0"), AnkraValue: strPtr("1.1.0")}
				if diff.Status != "computed" || diff.Total != 111 || !diff.Truncated || len(diff.Changes) != 1 ||
					!reflect.DeepEqual(diff.Changes[0], wantChange) {
					t.Fatalf("structural diff = %+v", diff)
				}
				if len(listing.RawJSON()) == 0 {
					t.Fatal("the listing must keep the platform's document for -o json")
				}
			},
		},
		{
			name:       "resolve sends the path, the resolution and both digests",
			wantMethod: http.MethodPost,
			wantPath:   "/api/v1/org/clusters/cluster-1/gitops/preserved-paths/resolve",
			wantBody: map[string]any{"path": "clusters/prod/prod.yaml", "resolution": "cluster",
				"head_content_hash": "h1", "render_content_hash": "r1"},
			response: `{"path":"clusters/prod/prod.yaml","resolution":"cluster","sync_triggered":true,` +
				`"message":"Marked clusters/prod/prod.yaml to write Ankra's version into Git; the next sync commits it."}`,
			call: func(testClient *Client) (any, error) {
				return testClient.ResolveClusterGitopsPreservedPath(context.Background(), "cluster-1",
					GitopsPreservedPathDecision{Path: "clusters/prod/prod.yaml", Resolution: "cluster",
						HeadContentHash: "h1", RenderContentHash: "r1"})
			},
			check: func(t *testing.T, got any) {
				resolution := got.(*GitopsPreservedPathResolution)
				if resolution.Path != "clusters/prod/prod.yaml" || resolution.Resolution != "cluster" ||
					!resolution.SyncTriggered || resolution.Message == "" {
					t.Fatalf("resolution = %+v", resolution)
				}
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath, seenAuthorization string
			var seenBody map[string]any
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.EscapedPath()
				seenAuthorization = request.Header.Get("Authorization")
				if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
					if decodeError := json.Unmarshal(raw, &seenBody); decodeError != nil {
						t.Fatalf("decode request body: %v", decodeError)
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(lane.response))
			})

			got, callError := lane.call(testClient)
			if callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod || seenPath != lane.wantPath {
				t.Errorf("request = %s %s, want %s %s", seenMethod, seenPath, lane.wantMethod, lane.wantPath)
			}
			if seenAuthorization != "Bearer "+testToken {
				t.Errorf("Authorization = %q, want the bearer token", seenAuthorization)
			}
			if lane.wantBody != nil && !reflect.DeepEqual(seenBody, lane.wantBody) {
				t.Errorf("body = %v, want %v", seenBody, lane.wantBody)
			}
			if lane.wantBody == nil && seenBody != nil {
				t.Errorf("the read sent a body: %v", seenBody)
			}
			lane.check(t, got)
		})
	}
}

// A decision the route would misread is refused before anything is sent: an
// unknown resolution, no path, or a missing digest (the platform would answer
// 422, or worse, a later platform could read a missing digest as "any").
func TestClusterGitopsPreservedPathResolveRefusesAnIncompleteDecision(t *testing.T) {
	requests := 0
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.WriteHeader(http.StatusOK)
	})
	complete := GitopsPreservedPathDecision{Path: "a.yaml", Resolution: "git", HeadContentHash: "h", RenderContentHash: "r"}
	cases := map[string]func(decision *GitopsPreservedPathDecision){
		"an empty resolution":      func(decision *GitopsPreservedPathDecision) { decision.Resolution = "" },
		"the CLI's own word ankra": func(decision *GitopsPreservedPathDecision) { decision.Resolution = "ankra" },
		"a capitalised side":       func(decision *GitopsPreservedPathDecision) { decision.Resolution = "Git" },
		"no path":                  func(decision *GitopsPreservedPathDecision) { decision.Path = "" },
		"no head digest":           func(decision *GitopsPreservedPathDecision) { decision.HeadContentHash = "" },
		"no render digest":         func(decision *GitopsPreservedPathDecision) { decision.RenderContentHash = "" },
	}
	for name, breakIt := range cases {
		decision := complete
		breakIt(&decision)
		if _, resolveError := testClient.ResolveClusterGitopsPreservedPath(context.Background(), "cluster-1", decision); resolveError == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if requests != 0 {
		t.Errorf("a refused decision still sent %d request(s)", requests)
	}
}

// A clusters.write refusal comes back as the client's permission error, so
// the command exits 7 rather than asking the user to log in again.
func TestClusterGitopsPreservedPathResolvePermissionDenied(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"permission_denied","permission":"clusters.write","scope_type":"cluster"}`))
	})
	_, resolveError := testClient.ResolveClusterGitopsPreservedPath(context.Background(), "cluster-1",
		GitopsPreservedPathDecision{Path: "a.yaml", Resolution: "git", HeadContentHash: "h", RenderContentHash: "r"})
	denied, isDenied := resolveError.(*PermissionDeniedError)
	if !isDenied || denied.Permission != "clusters.write" {
		t.Fatalf("error = %#v, want a PermissionDeniedError for clusters.write", resolveError)
	}
}
