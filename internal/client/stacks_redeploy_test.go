package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestRedeployStackSendsForceOnlyWhenAsked pins the opt-in on the wire: the
// plain call carries no query at all, so nothing but the flag can ask the
// platform to force.
func TestRedeployStackSendsForceOnlyWhenAsked(t *testing.T) {
	for name, testCase := range map[string]struct {
		force     bool
		wantQuery string
	}{
		"plain":  {force: false, wantQuery: ""},
		"forced": {force: true, wantQuery: "force=true"},
	} {
		t.Run(name, func(t *testing.T) {
			handler := func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", request.Method)
				}
				if request.URL.EscapedPath() != "/api/v1/org/clusters/imported/cluster-1/stacks/my%20stack/redeploy" {
					t.Errorf("path = %s", request.URL.EscapedPath())
				}
				if request.URL.RawQuery != testCase.wantQuery {
					t.Errorf("query = %q, want %q", request.URL.RawQuery, testCase.wantQuery)
				}
				if request.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("authorization = %q", request.Header.Get("Authorization"))
				}
				jsonResponse(t, writer, http.StatusOK, map[string]any{
					"stack_name":           "my stack",
					"operation_id":         "operation-1",
					"job_count":            2,
					"operations":           []map[string]any{{"operation_id": "operation-1", "job_count": 2, "resource_ids": []string{"manifest-1"}}},
					"skipped_resource_ids": []string{"manifest-2"},
					"skipped_resources":    []map[string]any{{"resource_id": "manifest-2", "reason": "overlap"}},
					"force_conflicts":      testCase.force,
				})
			}
			result, redeployError := newTestClient(t, handler).RedeployStack(context.Background(), "cluster-1", "my stack", testCase.force)
			if redeployError != nil {
				t.Fatalf("RedeployStack: %v", redeployError)
			}
			if result.StackName != "my stack" || result.JobCount != 2 || result.ForceConflicts != testCase.force {
				t.Fatalf("result = %+v", result)
			}
			if result.OperationID == nil || *result.OperationID != "operation-1" {
				t.Fatalf("operation id = %v", result.OperationID)
			}
			if len(result.Operations) != 1 || result.Operations[0].JobCount != 2 || result.Operations[0].ResourceIDs[0] != "manifest-1" {
				t.Fatalf("operations = %+v", result.Operations)
			}
			if len(result.SkippedResources) != 1 || result.SkippedResources[0].Reason != "overlap" {
				t.Fatalf("skipped = %+v", result.SkippedResources)
			}
		})
	}
}

// TestRedeployStackKeepsTheRefusalsApart pins what the command classifies: the
// floor's 403 is a permission denial naming clusters.write, and a handler's 404
// keeps its sentence so it can be told from a platform without the route.
func TestRedeployStackKeepsTheRefusalsApart(t *testing.T) {
	denied := func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]any{
			"detail": "permission_denied", "permission": "clusters.write", "scope_type": "cluster", "scope_id": "cluster-1",
		})
	}
	_, deniedError := newTestClient(t, denied).RedeployStack(context.Background(), "cluster-1", "web", true)
	var permissionDenied *PermissionDeniedError
	if !errors.As(deniedError, &permissionDenied) || permissionDenied.Permission != "clusters.write" {
		t.Fatalf("a floor refusal must be a permission denial naming clusters.write, got %v", deniedError)
	}

	missing := func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Stack not found"})
	}
	_, missingError := newTestClient(t, missing).RedeployStack(context.Background(), "cluster-1", "web", false)
	var unexpected *UnexpectedResponseError
	if !errors.As(missingError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "Stack not found" {
		t.Fatalf("a stack's 404 must keep its sentence, got %v", missingError)
	}
}

// TestRedeployStackKeepsTheForcedReachThreeValued pins what the verb's output
// depends on: a named reach, an empty one and a missing one are three answers,
// and each survives being decoded and encoded again for -o json.
func TestRedeployStackKeepsTheForcedReachThreeValued(t *testing.T) {
	for name, testCase := range map[string]struct {
		member     string
		wantIsNil  bool
		wantLength int
		wantJSON   string
	}{
		"named":   {member: `,"forced_resource_ids":["manifest-1"]`, wantLength: 1, wantJSON: `"forced_resource_ids":["manifest-1"]`},
		"empty":   {member: `,"forced_resource_ids":[]`, wantJSON: `"forced_resource_ids":[]`},
		"missing": {wantIsNil: true},
	} {
		t.Run(name, func(t *testing.T) {
			var result RedeployStackResult
			if decodeError := json.Unmarshal([]byte(`{"stack_name":"web","force_conflicts":true`+testCase.member+`}`), &result); decodeError != nil {
				t.Fatalf("decoding: %v", decodeError)
			}
			if (result.ForcedResourceIDs == nil) != testCase.wantIsNil || len(result.ForcedResourceIDs) != testCase.wantLength {
				t.Fatalf("ForcedResourceIDs = %#v", result.ForcedResourceIDs)
			}
			encoded, encodeError := json.Marshal(result)
			if encodeError != nil {
				t.Fatalf("encoding: %v", encodeError)
			}
			if testCase.wantJSON == "" {
				if strings.Contains(string(encoded), "forced_resource_ids") {
					t.Fatalf("a missing reach was invented: %s", encoded)
				}
			} else if !strings.Contains(string(encoded), testCase.wantJSON) {
				t.Fatalf("encoded = %s, want %s", encoded, testCase.wantJSON)
			}
		})
	}
}
