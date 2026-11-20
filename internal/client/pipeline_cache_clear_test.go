package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

const pipelineCacheClearTestAnswer = `{"archives_cleared":2,"archive_bytes":1610612736,"volumes_cleared":1,` +
	`"volumes_deleted_after":"2026-11-21T09:00:00Z"}`

// The clear is pinned on the request itself, on both selectors: the path,
// the method and the body a real platform reads, with trust left out when
// both scopes are meant.
func TestClearPipelineCacheSendsThePathAndScopeOnBothSelectors(t *testing.T) {
	lanes := []struct {
		name     string
		selector PipelineSelector
		trust    string
		path     string
		body     map[string]any
	}{
		{
			name: "repository, one scope", selector: PipelineSelector{RepositoryID: "repo-1"}, trust: "branch",
			path: "/api/v1/org/pipeline-repositories/repo-1/pipeline-caches/clear",
			body: map[string]any{"path": "/go/pkg/mod", "trust": "branch"},
		},
		{
			name: "application, both scopes", selector: PipelineSelector{ApplicationID: "app-1"},
			path: "/api/v1/org/applications/app-1/pipeline-caches/clear",
			body: map[string]any{"path": "/go/pkg/mod"},
		},
	}
	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != lane.path {
					t.Errorf("request = %s %s, want POST %s", request.Method, request.URL.Path, lane.path)
				}
				if got := request.Header.Get("Authorization"); got != "Bearer "+testToken {
					t.Errorf("authorization = %q", got)
				}
				raw, _ := io.ReadAll(request.Body)
				var body map[string]any
				if decodeError := json.Unmarshal(raw, &body); decodeError != nil {
					t.Fatalf("body %q: %v", raw, decodeError)
				}
				if len(body) != len(lane.body) || body["path"] != lane.body["path"] || body["trust"] != lane.body["trust"] {
					t.Errorf("body = %v, want %v", body, lane.body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, pipelineCacheClearTestAnswer)
			})
			document, clearError := testClient.ClearPipelineCache(context.Background(), lane.selector, "/go/pkg/mod", lane.trust)
			if clearError != nil {
				t.Fatalf("clear: %v", clearError)
			}
			if string(document) != pipelineCacheClearTestAnswer {
				t.Fatalf("document = %s, want the answer as sent", document)
			}
		})
	}
}

// Each refusal keeps what a command needs to tell its kind apart: the 422's
// list of problems as the platform's words, and every other status the
// shared mapping's own error.
func TestClearPipelineCacheKeepsEachRefusal(t *testing.T) {
	lanes := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, clearError error)
	}{
		{
			name: "422 names the field and the platform's message", status: http.StatusUnprocessableEntity,
			body: `{"detail":[{"type":"enum","loc":["body","trust"],"msg":"Input should be 'default_branch' or 'branch'","input":"x"}]}`,
			check: func(t *testing.T, clearError error) {
				var rejected *PipelineCacheClearRejectedError
				if !errors.As(clearError, &rejected) || rejected.Message != "trust: Input should be 'default_branch' or 'branch'" {
					t.Fatalf("error = %#v", clearError)
				}
			},
		},
		{
			name: "404 keeps its status and sentence", status: http.StatusNotFound,
			body: `{"detail":"Pipeline repository not found"}`,
			check: func(t *testing.T, clearError error) {
				var unexpected *UnexpectedResponseError
				if !errors.As(clearError, &unexpected) || unexpected.StatusCode != http.StatusNotFound ||
					unexpected.Detail != "Pipeline repository not found" {
					t.Fatalf("error = %#v", clearError)
				}
			},
		},
		{
			name: "409 keeps its status and sentence", status: http.StatusConflict,
			body: `{"detail":"Runs kept starting with this cache while it was being cleared. Nothing was cleared; try again."}`,
			check: func(t *testing.T, clearError error) {
				var unexpected *UnexpectedResponseError
				if !errors.As(clearError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
					unexpected.Detail == "" {
					t.Fatalf("error = %#v", clearError)
				}
			},
		},
		{
			name: "403 is the permission refusal", status: http.StatusForbidden,
			body: `{"detail":"permission_denied","permission":"pipelines.operate"}`,
			check: func(t *testing.T, clearError error) {
				var denied *PermissionDeniedError
				if !errors.As(clearError, &denied) || denied.Permission != "pipelines.operate" {
					t.Fatalf("error = %#v", clearError)
				}
			},
		},
	}
	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(lane.status)
				_, _ = io.WriteString(writer, lane.body)
			})
			document, clearError := testClient.ClearPipelineCache(context.Background(),
				PipelineSelector{RepositoryID: "repo-1"}, "/go/pkg/mod", "")
			if document != nil {
				t.Errorf("a refusal answered a document: %s", document)
			}
			lane.check(t, clearError)
		})
	}
}
