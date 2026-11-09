package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"
)

const testEnvironmentsRunPath = "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID +
	"/pipeline-runs/" + timeSavedTestRunID

// testEnvironmentLeg is one leg of the test documents below.
func testEnvironmentLeg(kubernetes string, postgres string, status string, result string,
	durationSeconds string, clusterID string, logTail string) string {
	nullable := func(value string, isQuoted bool) string {
		switch {
		case value == "":
			return "null"
		case isQuoted:
			return `"` + value + `"`
		default:
			return value
		}
	}
	destroyedAt := "null"
	if status == "destroyed" {
		destroyedAt = `"2026-11-09T10:10:00Z"`
	}
	message := ""
	switch result {
	case "passed":
		message = "Tests passed."
	case "failed":
		message = "Tests failed: 3 of 212."
	}
	return fmt.Sprintf(`{"step_id":"step-%[1]s-%[2]s","step_key":"test-matrix:kubernetes=%[1]s,postgres=%[2]s",
		"stage":"test-matrix","attempt":1,"matrix":{"kubernetes":"%[1]s","postgres":"%[2]s"},
		"kubernetes_version":"%[1]s","postgres_version":"%[2]s","provider":"ankracloud","plan":"",
		"status":"%[3]s","result":%[4]s,"cluster_id":%[5]s,"message":"%[6]s","test_log_tail":%[7]s,
		"created_at":null,"ready_at":null,"concluded_at":null,"destroyed_at":%[8]s,"expires_at":null,
		"duration_seconds":%[9]s}`,
		kubernetes, postgres, status, nullable(result, true), nullable(clusterID, true), message,
		mustJSONString(logTail), destroyedAt, nullable(durationSeconds, false))
}

func mustJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func testFailedLogTail(lineCount int) string {
	var lines []string
	for index := 1; index <= lineCount; index++ {
		lines = append(lines, fmt.Sprintf("log line %d", index))
	}
	return strings.Join(lines, "\n") + "\n"
}

// testEnvironmentsConcludedBody is the 3x2 matrix of the feature: five legs
// passed, Kubernetes 1.37 against PostgreSQL 16 failed, every cluster gone.
var testEnvironmentsConcludedBody = `{"run_id":"` + timeSavedTestRunID + `","axes":["kubernetes","postgres"],
	"summary":{"total":6,"passed":5,"failed":1,"errored":0,"cancelled":0,"timed_out":0,"running":0,
	 "queued":0,"torn_down":6,"verdict":"failed"},
	"extra_member":{"kept":true},
	"environments":[` + strings.Join([]string{
	testEnvironmentLeg("1.35", "16", "destroyed", "passed", "312", "c-1", ""),
	testEnvironmentLeg("1.35", "17", "destroyed", "passed", "298", "c-2", ""),
	testEnvironmentLeg("1.36", "16", "destroyed", "passed", "340", "c-3", ""),
	testEnvironmentLeg("1.36", "17", "destroyed", "passed", "302", "c-4", ""),
	testEnvironmentLeg("1.37", "16", "destroyed", "failed", "421", "c-5", testFailedLogTail(14)),
	testEnvironmentLeg("1.37", "17", "destroyed", "passed", "370", "c-6", ""),
}, ",") + `]}`

// testEnvironmentsQueuedBody is a run the concurrency cap holds back: two
// legs running, two queued with nothing recorded yet, two passed.
var testEnvironmentsQueuedBody = `{"run_id":"` + timeSavedTestRunID + `","axes":["kubernetes","postgres"],
	"summary":{"total":6,"passed":2,"failed":0,"errored":0,"cancelled":0,"timed_out":0,"running":2,
	 "queued":2,"torn_down":2,"verdict":"running"},
	"environments":[` + strings.Join([]string{
	testEnvironmentLeg("1.35", "16", "destroyed", "passed", "312", "c-1", ""),
	testEnvironmentLeg("1.35", "17", "destroyed", "passed", "298", "c-2", ""),
	testEnvironmentLeg("1.36", "16", "testing", "", "", "c-3", ""),
	testEnvironmentLeg("1.36", "17", "provisioning", "", "", "c-4", ""),
	testEnvironmentLeg("1.37", "16", "queued", "", "", "", ""),
	testEnvironmentLeg("1.37", "17", "queued", "", "", "", ""),
}, ",") + `]}`

// serveRunWithTestEnvironments serves the run detail (with no time saved)
// and answers the test environments route with status and body.
func serveRunWithTestEnvironments(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case testEnvironmentsRunPath:
			_, _ = fmt.Fprint(writer, timeSavedTestRunBody)
		case testEnvironmentsRunPath + "/test-environments":
			writer.WriteHeader(status)
			_, _ = fmt.Fprint(writer, body)
		case testEnvironmentsRunPath + "/tests":
			_, _ = fmt.Fprint(writer, `{"has_reports":false}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runPipelineCommandAgainst(t *testing.T, server *httptest.Server, arguments ...string) (string, string, error) {
	t.Helper()
	useTestClient(t, server.URL)
	pipelineCommand := newPipelineCommand()
	var stdout, stderr bytes.Buffer
	pipelineCommand.SetOut(&stdout)
	pipelineCommand.SetErr(&stderr)
	pipelineCommand.SetArgs(append(arguments, "--repository", testPipelineRepositoryID))
	executeError := pipelineCommand.Execute()
	return stdout.String(), stderr.String(), executeError
}

const testEnvironmentsConcludedGrid = `╭─────────────────┬──────────────┬──────────────╮
│                 │ postgres 16  │ postgres 17  │
├─────────────────┼──────────────┼──────────────┤
│ kubernetes 1.35 │ passed 5m12s │ passed 4m58s │
│ kubernetes 1.36 │ passed 5m40s │ passed 5m02s │
│ kubernetes 1.37 │ FAILED       │ passed 6m10s │
╰─────────────────┴──────────────┴──────────────╯
5/6 legs passed - 1 failed (kubernetes 1.37 x postgres 16) - all 6 clusters torn down
`

func TestPipelineEnvironmentsPrintsTheGrid(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsConcludedBody)
	stdout, stderr, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline environments: %v", executeError)
	}
	expected := testEnvironmentsConcludedGrid + `
FAILED  kubernetes 1.37 x postgres 16
  Tests failed: 3 of 212.
    log line 5
    log line 6
    log line 7
    log line 8
    log line 9
    log line 10
    log line 11
    log line 12
    log line 13
    log line 14
  (last 10 of 14 log lines; --logs prints the whole tail)
`
	if stdout != expected {
		t.Fatalf("output:\n%s\nwant:\n%s", stdout, expected)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestPipelineEnvironmentsLogsPrintsTheWholeTail(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsConcludedBody)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "envs", timeSavedTestRunID, "--logs")
	if executeError != nil {
		t.Fatalf("pipeline envs --logs: %v", executeError)
	}
	if !strings.Contains(stdout, "    log line 1\n    log line 2\n") || strings.Contains(stdout, "last 10 of") {
		t.Fatalf("expected the whole tail, got:\n%s", stdout)
	}
}

func TestPipelineEnvironmentsShowsQueuedAndRunningLegs(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsQueuedBody)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline environments: %v", executeError)
	}
	expected := `╭─────────────────┬──────────────┬──────────────╮
│                 │ postgres 16  │ postgres 17  │
├─────────────────┼──────────────┼──────────────┤
│ kubernetes 1.35 │ passed 5m12s │ passed 4m58s │
│ kubernetes 1.36 │ testing      │ provisioning │
│ kubernetes 1.37 │ queued       │ queued       │
╰─────────────────┴──────────────┴──────────────╯
2/6 legs passed - 2 running - 2 queued - 2 clusters still up, 2 torn down
`
	if stdout != expected {
		t.Fatalf("output:\n%s\nwant:\n%s", stdout, expected)
	}
}

func TestPipelineEnvironmentsFallsBackToATableForOneAxis(t *testing.T) {
	body := `{"run_id":"` + timeSavedTestRunID + `","axes":["kubernetes"],
		"summary":{"total":2,"passed":1,"failed":0,"errored":1,"cancelled":0,"timed_out":0,"running":0,
		 "queued":0,"torn_down":2,"verdict":"failed"},
		"environments":[
		{"step_id":"s1","step_key":"smoke:kubernetes=1.36","stage":"smoke","attempt":1,
		 "matrix":{"kubernetes":"1.36"},"status":"destroyed","result":"passed","cluster_id":"c-1",
		 "duration_seconds":95},
		{"step_id":"s2","step_key":"smoke:kubernetes=1.37","stage":"smoke","attempt":1,
		 "matrix":{"kubernetes":"1.37"},"status":"destroyed","result":"error","cluster_id":"c-2",
		 "message":"The cluster did not become ready.","duration_seconds":3725}]}`
	server := serveRunWithTestEnvironments(t, http.StatusOK, body)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline environments: %v", executeError)
	}
	expected := `╭───────────────────────┬─────────────────┬───────────┬────────┬──────────┬─────────╮
│ STEP KEY              │ MATRIX          │ STATUS    │ RESULT │ DURATION │ CLUSTER │
├───────────────────────┼─────────────────┼───────────┼────────┼──────────┼─────────┤
│ smoke:kubernetes=1.36 │ kubernetes=1.36 │ destroyed │ passed │ 1m35s    │ c-1     │
│ smoke:kubernetes=1.37 │ kubernetes=1.37 │ destroyed │ error  │ 1h02m    │ c-2     │
╰───────────────────────┴─────────────────┴───────────┴────────┴──────────┴─────────╯
1/2 legs passed - 1 errored (kubernetes 1.37) - all 2 clusters torn down

ERROR  kubernetes 1.37
  The cluster did not become ready.
`
	if stdout != expected {
		t.Fatalf("output:\n%s\nwant:\n%s", stdout, expected)
	}
}

// -o json prints the platform's document, members this CLI does not declare
// included.
func TestPipelineEnvironmentsJSONPassesTheBodyThrough(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsConcludedBody)
	stdout, stderr, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline environments -o json: %v", executeError)
	}
	var printed, sent any
	if decodeError := json.Unmarshal([]byte(stdout), &printed); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if decodeError := json.Unmarshal([]byte(testEnvironmentsConcludedBody), &sent); decodeError != nil {
		t.Fatalf("decoding the body: %v", decodeError)
	}
	if !reflect.DeepEqual(printed, sent) {
		t.Fatalf("printed document differs from the platform's:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestPipelineEnvironmentsExitsNotFoundOnA404(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusNotFound, `{"detail":"Pipeline run not found"}`)
	_, _, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID)
	if executeError == nil || exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("error = %v (exit %d), want exit %d", executeError, exitCodeFor(executeError), exitNotFound)
	}
}

func TestPipelineEnvironmentsSaysWhenTheRunHasNone(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK,
		`{"run_id":"`+timeSavedTestRunID+`","axes":[],"summary":{"total":0,"verdict":"none"},"environments":[]}`)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "environments", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline environments: %v", executeError)
	}
	if stdout != "This run has no test environments.\n" {
		t.Fatalf("output = %q", stdout)
	}
}

func TestPipelineGetPrintsTheTestEnvironments(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsConcludedBody)
	stdout, stderr, executeError := runPipelineCommandAgainst(t, server, "get", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline get: %v", executeError)
	}
	if !strings.Contains(stdout, "Run #42") || !strings.Contains(stdout, "\nTest environments\n"+testEnvironmentsConcludedGrid) {
		t.Fatalf("expected the run and its test environments, got:\n%s", stdout)
	}
	hint := "'ankra pipeline environments " + timeSavedTestRunID + " --repository " + testPipelineRepositoryID +
		" --logs' prints the whole tail"
	if !strings.Contains(stdout, hint) {
		t.Errorf("expected the full-log hint %q in:\n%s", hint, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestPipelineGetToleratesAPlatformWithoutTheTestEnvironmentsRoute(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusNotFound, `{"detail":"Not Found"}`)
	stdout, stderr, executeError := runPipelineCommandAgainst(t, server, "get", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline get failed on a 404 from the test environments route: %v", executeError)
	}
	if strings.Contains(stdout, "Test environments") || stderr != "" {
		t.Fatalf("expected the run alone, got stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestPipelineGetKeepsTheRunWhenTheTestEnvironmentsReadFails(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusInternalServerError, `{"detail":"database unavailable"}`)
	stdout, stderr, executeError := runPipelineCommandAgainst(t, server, "get", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline get failed on a test environments error: %v", executeError)
	}
	if !strings.Contains(stdout, "Run #42") || strings.Contains(stdout, "Test environments") {
		t.Errorf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, "could not read the run's test environments: database unavailable") {
		t.Errorf("stderr = %q", stderr)
	}
}

// -o json of `pipeline get` stays what it was: no request for the test
// environments, no member for them.
func TestPipelineGetJSONLeavesTheTestEnvironmentsOut(t *testing.T) {
	isEnvironmentsRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case testEnvironmentsRunPath:
			_, _ = fmt.Fprint(writer, timeSavedTestRunBody)
		case testEnvironmentsRunPath + "/test-environments":
			isEnvironmentsRequested = true
			_, _ = fmt.Fprint(writer, testEnvironmentsConcludedBody)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
		}
	}))
	t.Cleanup(server.Close)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "get", timeSavedTestRunID, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline get -o json: %v", executeError)
	}
	if isEnvironmentsRequested || strings.Contains(stdout, "environments") {
		t.Fatalf("pipeline get -o json read or printed the test environments:\n%s", stdout)
	}
}

func TestPipelineTestsPrintsTheTestEnvironments(t *testing.T) {
	server := serveRunWithTestEnvironments(t, http.StatusOK, testEnvironmentsConcludedBody)
	stdout, _, executeError := runPipelineCommandAgainst(t, server, "tests", timeSavedTestRunID)
	if executeError != nil {
		t.Fatalf("pipeline tests: %v", executeError)
	}
	if !strings.HasPrefix(stdout, "This run declared no test reports") ||
		!strings.Contains(stdout, "\nTest environments\n"+testEnvironmentsConcludedGrid) {
		t.Fatalf("expected the test summary and the test environments, got:\n%s", stdout)
	}
}

func TestPipelineTestEnvironmentsKeepEachCellsNewestAttempt(t *testing.T) {
	failed, passed := "failed", "passed"
	legs := latestPipelineTestEnvironmentLegs([]client.PipelineTestEnvironment{
		{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.37"}, Result: &failed},
		{Stage: "matrix", Attempt: 2, Matrix: map[string]string{"kubernetes": "1.37"}, Result: &passed},
		{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.36"}, Result: &passed},
	}, []string{"kubernetes"})
	if len(legs) != 2 || legs[0].Attempt != 2 || *legs[0].Result != "passed" || legs[1].Matrix["kubernetes"] != "1.36" {
		t.Fatalf("legs = %+v", legs)
	}
}

func TestCompareMatrixValuesOrdersVersions(t *testing.T) {
	for _, testCase := range []struct {
		left, right string
		isLess      bool
	}{
		{"1.9", "1.10", true},
		{"1.35", "1.36", true},
		{"16", "17", true},
		{"17", "16", false},
		{"1.36", "1.36.1", true},
		{"alpine", "bookworm", true},
	} {
		if isLess := compareMatrixValues(testCase.left, testCase.right) < 0; isLess != testCase.isLess {
			t.Errorf("compareMatrixValues(%q, %q) < 0 = %v, want %v", testCase.left, testCase.right, isLess, testCase.isLess)
		}
	}
}

func TestPipelineTestEnvironmentDurationFormats(t *testing.T) {
	for seconds, expected := range map[int64]string{45: "45s", 312: "5m12s", 3725: "1h02m", 60: "1m00s"} {
		value := seconds
		if formatted := pipelineTestEnvironmentDuration(&value); formatted != expected {
			t.Errorf("pipelineTestEnvironmentDuration(%d) = %q, want %q", seconds, formatted, expected)
		}
	}
	if formatted := pipelineTestEnvironmentDuration(nil); formatted != "" {
		t.Errorf("pipelineTestEnvironmentDuration(nil) = %q", formatted)
	}
}

// A retried leg is counted once, by its newest attempt: the line under the
// grid never contradicts the cells above it.
func TestPipelineTestEnvironmentsSummaryCountsTheShownAttempts(t *testing.T) {
	failed, passed := "failed", "passed"
	firstCluster, secondCluster := "c-1", "c-2"
	destroyedAt := "2026-11-09T10:10:00Z"
	legs := latestPipelineTestEnvironmentLegs([]client.PipelineTestEnvironment{
		{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.37", "postgres": "16"},
			Status: "destroyed", Result: &failed, ClusterID: &firstCluster, DestroyedAt: &destroyedAt},
		{Stage: "matrix", Attempt: 2, Matrix: map[string]string{"kubernetes": "1.37", "postgres": "16"},
			Status: "destroyed", Result: &passed, ClusterID: &secondCluster, DestroyedAt: &destroyedAt},
	}, []string{"kubernetes", "postgres"})
	line := pipelineTestEnvironmentsSummaryLine(legs, []string{"kubernetes", "postgres"})
	if line != "1/1 leg passed - its cluster torn down" {
		t.Fatalf("line = %q", line)
	}
}

// A leg torn down without a recorded result is over, and says so: it is not
// shown as a phase a leg still in flight would be in.
func TestPipelineTestEnvironmentsNameALegThatEndedWithoutAResult(t *testing.T) {
	passed := "passed"
	legs := []client.PipelineTestEnvironment{
		{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.36"}, Status: "destroyed", Result: &passed},
		{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.37"}, Status: "destroyed"},
	}
	if cell := pipelineTestEnvironmentCell(legs[1]); cell != "no result" {
		t.Errorf("cell = %q, want \"no result\"", cell)
	}
	line := pipelineTestEnvironmentsSummaryLine(legs, []string{"kubernetes"})
	if line != "1/2 legs passed - 1 ended without a result (kubernetes 1.37) - all 2 clusters torn down" {
		t.Fatalf("line = %q", line)
	}
}

// A leg whose teardown time is recorded while its status still lags is torn
// down everywhere: cell, counts and teardown line agree.
func TestPipelineTestEnvironmentsTreatARecordedTeardownAsTornDown(t *testing.T) {
	clusterID, destroyedAt := "c-1", "2026-11-09T10:10:00Z"
	leg := client.PipelineTestEnvironment{Stage: "matrix", Attempt: 1, Matrix: map[string]string{"kubernetes": "1.37"},
		Status: "tearing_down", ClusterID: &clusterID, DestroyedAt: &destroyedAt}
	if cell := pipelineTestEnvironmentCell(leg); cell != "no result" {
		t.Errorf("cell = %q, want \"no result\"", cell)
	}
	line := pipelineTestEnvironmentsSummaryLine([]client.PipelineTestEnvironment{leg}, []string{"kubernetes"})
	if line != "0/1 leg passed - 1 ended without a result (kubernetes 1.37) - its cluster torn down" {
		t.Fatalf("line = %q", line)
	}
}
