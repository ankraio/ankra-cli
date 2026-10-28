package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// The environment write is the portal's route: PATCH with the environment, and
// the criticality only when one is given, so an environment-only change leaves
// the stored reporting level alone.
func TestSetClusterEnvironmentSendsTheCriticalityOnlyWhenGiven(t *testing.T) {
	for name, testCase := range map[string]struct {
		criticality string
		wantBody    map[string]any
		response    map[string]any
	}{
		"environment and level": {"development",
			map[string]any{"environment": "development", "criticality": "development"},
			map[string]any{"environment": "development", "criticality": "development"}},
		"environment alone": {"",
			map[string]any{"environment": "development"},
			map[string]any{"environment": "development"}},
	} {
		var receivedMethod, receivedPath string
		var receivedBody []byte
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			receivedMethod, receivedPath = r.Method, r.URL.Path
			receivedBody, _ = io.ReadAll(r.Body)
			jsonResponse(t, w, http.StatusOK, testCase.response)
		})
		result, setError := testClient.SetClusterEnvironment(context.Background(), "cluster-id", "development", testCase.criticality)
		if setError != nil {
			t.Fatalf("%s: unexpected error %v", name, setError)
		}
		if receivedMethod != http.MethodPatch || receivedPath != "/api/v1/org/clusters/imported/cluster-id/environment" {
			t.Errorf("%s: request = %s %s, want PATCH /api/v1/org/clusters/imported/cluster-id/environment",
				name, receivedMethod, receivedPath)
		}
		var decoded map[string]any
		if decodeError := json.Unmarshal(receivedBody, &decoded); decodeError != nil {
			t.Fatalf("%s: body is not JSON: %v", name, decodeError)
		}
		if !reflect.DeepEqual(decoded, testCase.wantBody) {
			t.Errorf("%s: body = %v, want %v", name, decoded, testCase.wantBody)
		}
		if result.Environment != "development" {
			t.Errorf("%s: environment = %q, want development", name, result.Environment)
		}
		if gotLevel := result.Criticality != nil; gotLevel != (testCase.criticality != "") {
			t.Errorf("%s: criticality decoded = %v, want present only when sent", name, result.Criticality)
		}
	}
}

// A refusal carries the platform's detail, not a bare status code.
func TestSetClusterEnvironmentRefusalCarriesTheDetail(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]any{"detail": "Cluster not found"})
	})
	_, setError := testClient.SetClusterEnvironment(context.Background(), "cluster-id", "development", "")
	if setError == nil || !strings.Contains(setError.Error(), "Cluster not found") {
		t.Fatalf("expected the platform detail, got %v", setError)
	}
}
