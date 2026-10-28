package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// The GitOps conflict routes are pinned on the request itself: a path,
// method or body typo fails as a 404 or a different decision against a real
// platform and passes every command test that only checks the wiring. The
// resolve bodies matter most: the routes read a missing resolution as "clear
// undecided", so the side must always be sent.
func TestClusterGitopsConflictsLaneParity(t *testing.T) {
	lanes := []struct {
		name       string
		wantMethod string
		wantPath   string
		wantBody   map[string]any
		response   string
		call       func(testClient *Client) (any, error)
		want       any
	}{
		{
			name:       "list reads the cluster's conflicts",
			wantMethod: http.MethodGet,
			wantPath:   "/api/v1/org/clusters/cluster-1/gitops/conflicts",
			response: `{"conflicts":[{"resource_key":"stack:web/addon:nginx","stack_name":"web",` +
				`"resource_kind":"addon","resource_name":"nginx","git_change_type":"modified",` +
				`"db_change_type":"removed","last_applied_commit_sha":"sha1","detected_at":"2026-07-02T08:00:00Z",` +
				`"resolution_choice":"git","resolved_at":"2026-07-02T09:00:00Z"}],"total":1}`,
			call: func(testClient *Client) (any, error) {
				return testClient.ListClusterGitopsConflicts(context.Background(), "cluster-1")
			},
			want: &GitopsConflictList{Total: 1, Conflicts: []GitopsConflict{{
				ResourceKey: "stack:web/addon:nginx", StackName: strPtr("web"), ResourceKind: "addon",
				ResourceName: strPtr("nginx"), GitChangeType: "modified", DBChangeType: "removed",
				LastAppliedCommitSHA: strPtr("sha1"), DetectedAt: strPtr("2026-07-02T08:00:00Z"),
				ResolutionChoice: strPtr("git"), ResolvedAt: strPtr("2026-07-02T09:00:00Z"),
			}}},
		},
		{
			name:       "resolve one sends the key and the side",
			wantMethod: http.MethodPost,
			wantPath:   "/api/v1/org/clusters/cluster-1/gitops/conflicts/resolve-resource",
			wantBody:   map[string]any{"resource_key": "stack:web/addon:nginx", "resolution": "git"},
			response: `{"cleared_count":1,"sync_triggered":true,` +
				`"message":"Marked 'stack:web/addon:nginx' to keep the Git version; converging on the next sync."}`,
			call: func(testClient *Client) (any, error) {
				return testClient.ResolveClusterGitopsConflict(context.Background(), "cluster-1", "stack:web/addon:nginx", "git")
			},
			want: &GitopsConflictResolution{ClearedCount: 1, SyncTriggered: true,
				Message: "Marked 'stack:web/addon:nginx' to keep the Git version; converging on the next sync."},
		},
		{
			name:       "resolve all sends only the side",
			wantMethod: http.MethodPost,
			wantPath:   "/api/v1/org/clusters/cluster-1/gitops/conflicts/resolve",
			wantBody:   map[string]any{"resolution": "cluster"},
			response:   `{"cleared_count":2,"sync_triggered":false,"message":"Marked 2 conflicting resource(s)."}`,
			call: func(testClient *Client) (any, error) {
				return testClient.ResolveAllClusterGitopsConflicts(context.Background(), "cluster-1", "cluster")
			},
			want: &GitopsConflictResolution{ClearedCount: 2, Message: "Marked 2 conflicting resource(s)."},
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
			if !reflect.DeepEqual(got, lane.want) {
				t.Errorf("decoded = %+v, want %+v", got, lane.want)
			}
		})
	}
}

// A side other than git or cluster is refused before anything is sent, so no
// caller reaches the routes' undecided clear by passing an empty side.
func TestClusterGitopsConflictResolveRefusesAMissingSide(t *testing.T) {
	requests := 0
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.WriteHeader(http.StatusOK)
	})
	for _, keep := range []string{"", "Git", "both"} {
		if _, resolveError := testClient.ResolveClusterGitopsConflict(context.Background(), "cluster-1", "stack:web", keep); resolveError == nil {
			t.Errorf("keep %q resolving one: want a refusal", keep)
		}
		if _, resolveError := testClient.ResolveAllClusterGitopsConflicts(context.Background(), "cluster-1", keep); resolveError == nil {
			t.Errorf("keep %q resolving all: want a refusal", keep)
		}
	}
	if _, resolveError := testClient.ResolveClusterGitopsConflict(context.Background(), "cluster-1", "", "git"); resolveError == nil {
		t.Error("an empty resource key: want a refusal")
	}
	if requests != 0 {
		t.Errorf("a refused resolve still sent %d request(s)", requests)
	}
}

// A clusters.write refusal comes back as the client's permission error, so
// the command exits 7 rather than asking the user to log in again.
func TestClusterGitopsConflictResolvePermissionDenied(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"permission_denied","permission":"clusters.write","scope_type":"cluster"}`))
	})
	_, resolveError := testClient.ResolveAllClusterGitopsConflicts(context.Background(), "cluster-1", "git")
	denied, isDenied := resolveError.(*PermissionDeniedError)
	if !isDenied || denied.Permission != "clusters.write" {
		t.Fatalf("error = %#v, want a PermissionDeniedError for clusters.write", resolveError)
	}
}
