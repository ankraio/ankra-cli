package cmd

// `ankra cluster state-snapshots retention` against a scripted platform: the
// real client talks to an httptest server, so the method, path and body each
// call sends are pinned on the request itself.

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

type stateSnapshotRetentionRequest struct {
	method string
	path   string
	body   map[string]any
}

type stateSnapshotRetentionServer struct {
	mutex         sync.Mutex
	state         client.StateSnapshotRetention
	requests      []stateSnapshotRetentionRequest
	refusalStatus int
	refusalBody   string
}

func newStateSnapshotRetentionServer(t *testing.T) *stateSnapshotRetentionServer {
	t.Helper()
	recorder := &stateSnapshotRetentionServer{state: client.StateSnapshotRetention{
		ClusterID: testClusterID, RetentionDays: 7, IsDefault: true, DefaultRetentionDays: 7,
		MinimumRetentionDays: 1, MaximumRetentionDays: 90, KeepLatest: 4,
	}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorded := stateSnapshotRetentionRequest{method: request.Method, path: request.URL.Path}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 && request.Method == http.MethodPut {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		if request.Method == http.MethodPut {
			days, isNumber := recorded.body["retention_days"].(float64)
			if !isNumber {
				writer.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = writer.Write([]byte(`{"detail":"retention_days must be an integer"}`))
				return
			}
			recorder.state.RetentionDays = int(days)
			recorder.state.IsDefault = false
		}
		_ = json.NewEncoder(writer).Encode(recorder.state)
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func (recorder *stateSnapshotRetentionServer) seen() []stateSnapshotRetentionRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]stateSnapshotRetentionRequest{}, recorder.requests...)
}

func runStateSnapshotRetention(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	rootCmd.SetArgs(append([]string{"cluster", "state-snapshots", "retention"}, arguments...))
	var commands []*cobra.Command
	for _, candidate := range clusterCmd.Commands() {
		if candidate.Name() == "state-snapshots" {
			commands = append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

const stateSnapshotRetentionTestPath = "/api/v1/org/clusters/" + testClusterID + "/state-snapshot-retention"

func TestStateSnapshotRetentionShowsTheDefaultWindow(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	stdout, _, runError := runStateSnapshotRetention(t, "")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet || requests[0].path != stateSnapshotRetentionTestPath {
		t.Fatalf("expected one GET of the selected cluster's window, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{"7 days (default)", "4 newest", "1 to 90 days"})
}

func TestStateSnapshotRetentionJSONKeepsStdoutClean(t *testing.T) {
	newStateSnapshotRetentionServer(t)
	stdout, _, runError := runStateSnapshotRetention(t, "", "-o", "json")
	if runError != nil {
		t.Fatalf("show -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v: %q", decodeError, stdout)
	}
	if decoded["retention_days"] != float64(7) || decoded["is_default"] != true {
		t.Fatalf("unexpected body %v", decoded)
	}
}

func TestStateSnapshotRetentionLongerWindowIsSetWithoutAsking(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	stdout, stderr, runError := runStateSnapshotRetention(t, "", "--days", "30")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 2 || requests[1].method != http.MethodPut || requests[1].path != stateSnapshotRetentionTestPath ||
		requests[1].body["retention_days"] != float64(30) {
		t.Fatalf("expected a GET then a PUT of 30, got %+v", requests)
	}
	if strings.Contains(stderr, "Continue?") {
		t.Fatalf("a longer window must not ask: %q", stderr)
	}
	assertContainsAll(t, stdout, []string{"set to 30 days for cluster 'test-cluster'"})
}

func TestStateSnapshotRetentionShorterWindowAsksFirst(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	recorder.state.RetentionDays, recorder.state.IsDefault = 30, false
	_, stderr, runError := runStateSnapshotRetention(t, "n\n", "--days", "3")
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("a declined shortening must be cancelled, got %v", runError)
	}
	if !strings.Contains(stderr, "Now: 30 days") || !strings.Contains(stderr, "New: 3 days") {
		t.Fatalf("the prompt does not say what changes: %q", stderr)
	}
	for _, request := range recorder.seen() {
		if request.method == http.MethodPut {
			t.Fatalf("a declined shortening sent %+v", request)
		}
	}
	if _, _, runError = runStateSnapshotRetention(t, "", "--days", "3", "--yes"); runError != nil {
		t.Fatalf("--yes must skip the question: %v", runError)
	}
	if recorder.state.RetentionDays != 3 {
		t.Fatalf("stored %d, want 3", recorder.state.RetentionDays)
	}
}

func TestStateSnapshotRetentionRefusesAnOutOfRangeWindowAndSendsNothing(t *testing.T) {
	for _, days := range []string{"0", "91", "-5"} {
		recorder := newStateSnapshotRetentionServer(t)
		_, _, runError := runStateSnapshotRetention(t, "", "--days="+days)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Fatalf("--days %s must be a usage error, got %v", days, runError)
		}
		for _, request := range recorder.seen() {
			if request.method != http.MethodGet {
				t.Fatalf("--days %s sent %+v", days, recorder.seen())
			}
		}
	}
}

func TestStateSnapshotRetentionFollowsTheAdvertisedBounds(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	recorder.state.MaximumRetentionDays = 120
	if _, _, runError := runStateSnapshotRetention(t, "", "--days=100"); runError != nil {
		t.Fatalf("a window inside the advertised bounds must be set, got %v", runError)
	}
	if recorder.state.RetentionDays != 100 {
		t.Fatalf("expected 100 days stored, got %d", recorder.state.RetentionDays)
	}
	recorder.state.MaximumRetentionDays = 30
	_, _, runError := runStateSnapshotRetention(t, "", "--days=40", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "1 to 30") {
		t.Fatalf("a window above the advertised maximum must be a usage error naming it, got %v", runError)
	}
}

func TestStateSnapshotRetentionWithoutKeepLatestDoesNotPrintZero(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	recorder.state.KeepLatest = 0
	stdout, _, runError := runStateSnapshotRetention(t, "")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	if strings.Contains(stdout, "0 newest") || !strings.Contains(stdout, "the newest backups") {
		t.Fatalf("a missing keep_latest must not print as zero, got %q", stdout)
	}
}

func TestStateSnapshotRetentionForbiddenWriteExitsSeven(t *testing.T) {
	recorder := newStateSnapshotRetentionServer(t)
	recorder.refusalStatus = http.StatusForbidden
	recorder.refusalBody = `{"detail":"Permission check failed (clusters.write)"}`
	_, _, runError := runStateSnapshotRetention(t, "", "--days", "30")
	if runError == nil || exitCodeFor(runError) != exitForbidden || !strings.Contains(runError.Error(), "clusters.write") {
		t.Fatalf("a forbidden write must exit 7 naming clusters.write, got %v", runError)
	}
}

func TestStateSnapshotRetentionErrorsMapToExitCodes(t *testing.T) {
	cluster := client.ClusterListItem{ID: testClusterID, Name: "test-cluster"}
	cases := []struct {
		name    string
		err     error
		writing bool
		code    int
	}{
		{"forbidden write", &client.UnexpectedResponseError{StatusCode: http.StatusForbidden}, true, exitForbidden},
		{"forbidden read", &client.UnexpectedResponseError{StatusCode: http.StatusForbidden}, false, exitForbidden},
		{"cluster outside the organisation", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Cluster not found"}, false, exitNotFound},
		{"platform without the route", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Not Found"}, false, exitError},
		{"404 without a detail", &client.UnexpectedResponseError{StatusCode: http.StatusNotFound}, false, exitError},
		{"refused window", &client.UnexpectedResponseError{StatusCode: http.StatusUnprocessableEntity}, true, exitUsage},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := exitCodeFor(stateSnapshotRetentionError(testCase.err, cluster, testCase.writing)); got != testCase.code {
				t.Fatalf("exit code %d, want %d", got, testCase.code)
			}
		})
	}
	if plain := stateSnapshotRetentionError(errors.New("boom"), cluster, false); plain == nil {
		t.Fatal("an unclassified error must still be returned")
	}
}
