package cmd

// `ankra cluster control-plane-heal get|set` against a scripted platform: the
// real client talks to an httptest server, so the method, path and body each
// verb sends are pinned on the request itself.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type controlPlaneHealRequest struct {
	method string
	path   string
	body   map[string]any
}

type controlPlaneHealServer struct {
	mutex         sync.Mutex
	state         client.ControlPlaneHeal
	requests      []controlPlaneHealRequest
	refusalStatus int
	refusalBody   string
	ignoreWrites  bool
}

func newControlPlaneHealServer(t *testing.T) *controlPlaneHealServer {
	t.Helper()
	recorder := &controlPlaneHealServer{state: client.ControlPlaneHeal{ClusterID: testClusterID, Enabled: true}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorded := controlPlaneHealRequest{method: request.Method, path: request.URL.Path}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		if request.Method == http.MethodPut {
			enabled, isBool := recorded.body["enabled"].(bool)
			if !isBool {
				writer.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = writer.Write([]byte(`{"detail":"enabled must be a boolean"}`))
				return
			}
			if !recorder.ignoreWrites {
				recorder.state.Enabled = enabled
			}
		}
		_ = json.NewEncoder(writer).Encode(recorder.state)
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func (recorder *controlPlaneHealServer) seen() []controlPlaneHealRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]controlPlaneHealRequest{}, recorder.requests...)
}

func runControlPlaneHeal(t *testing.T, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(append([]string{"cluster", "control-plane-heal"}, arguments...))
	var commands []*cobra.Command
	for _, candidate := range clusterCmd.Commands() {
		if candidate.Name() == "control-plane-heal" {
			commands = append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestControlPlaneHealGetPrintsTheSwitchAndEpisode(t *testing.T) {
	recorder := newControlPlaneHealServer(t)
	recorder.state.LatestIncident = &client.ControlPlaneHealIncident{
		NodeName: "cp-1", State: "open", Reason: "etcd_not_quorate", Mode: "shadow",
		NotReadySince: "2026-10-13T12:00:00Z",
	}
	stdout, _, runError := runControlPlaneHeal(t, "get")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/org/clusters/"+testClusterID+"/control-plane-self-heal" {
		t.Fatalf("expected one GET of the selected cluster's switch, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{"enabled", "cp-1", "open", "shadow", "etcd_not_quorate", "observing only"})
}

func TestControlPlaneHealGetWithoutEpisode(t *testing.T) {
	newControlPlaneHealServer(t)
	stdout, _, runError := runControlPlaneHeal(t, "get")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"Automatic restart: enabled", "none"})
}

func TestControlPlaneHealGetJSONKeepsStdoutClean(t *testing.T) {
	newControlPlaneHealServer(t)
	stdout, _, runError := runControlPlaneHeal(t, "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v: %q", decodeError, stdout)
	}
	if decoded["enabled"] != true || decoded["latest_incident"] != nil {
		t.Fatalf("unexpected body %v", decoded)
	}
}

func TestControlPlaneHealSetSendsEnabledFalse(t *testing.T) {
	recorder := newControlPlaneHealServer(t)
	stdout, _, runError := runControlPlaneHeal(t, "set", "--enabled=false")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodPut || requests[0].body["enabled"] != false {
		t.Fatalf("false must be sent, not dropped as a zero value: %+v", requests)
	}
	assertContainsAll(t, stdout, []string{"disabled for cluster 'test-cluster'"})
}

func TestControlPlaneHealSetSendsEnabledTrue(t *testing.T) {
	recorder := newControlPlaneHealServer(t)
	recorder.state.Enabled = false
	if _, _, runError := runControlPlaneHeal(t, "set", "--enabled=true"); runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].body["enabled"] != true {
		t.Fatalf("expected enabled true, got %+v", requests)
	}
}

func TestControlPlaneHealSetRequiresTheFlagAndSendsNothing(t *testing.T) {
	recorder := newControlPlaneHealServer(t)
	_, _, runError := runControlPlaneHeal(t, "set")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("a set without --enabled must be a usage error, got %v", runError)
	}
	if len(recorder.seen()) != 0 {
		t.Fatalf("a refused set must send nothing, got %+v", recorder.seen())
	}
}

func TestControlPlaneHealSetReportsADisagreement(t *testing.T) {
	recorder := newControlPlaneHealServer(t)
	recorder.ignoreWrites = true
	_, _, runError := runControlPlaneHeal(t, "set", "--enabled=false")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "not disabled as requested") {
		t.Fatalf("a stored state that disagrees must exit non-zero, got %v", runError)
	}
}

func TestControlPlaneHealErrorsMapToExitCodes(t *testing.T) {
	cluster := client.ClusterListItem{ID: testClusterID, Name: "test-cluster"}
	cases := []struct {
		name    string
		err     error
		writing bool
		code    int
	}{
		{"forbidden write", &client.UnexpectedResponseError{StatusCode: http.StatusForbidden}, true, exitForbidden},
		{"cluster outside the organisation", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Cluster not found"}, false, exitNotFound},
		{"platform without the route", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Not Found"}, false, exitError},
		{"404 without a detail", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound}, false, exitError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := exitCodeFor(controlPlaneHealError(testCase.err, cluster, testCase.writing)); got != testCase.code {
				t.Fatalf("exit code %d, want %d", got, testCase.code)
			}
		})
	}
	if plain := controlPlaneHealError(errors.New("boom"), cluster, false); plain == nil {
		t.Fatal("an unclassified error must still be returned")
	}
}
