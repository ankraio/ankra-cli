package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	timeSavedTestRunID = "8d6c3f2a-1b4e-4c5d-9e7f-0a1b2c3d4e5f"

	timeSavedTestRunBody = `{"id":"` + timeSavedTestRunID + `","run_number":42,"status":"concluded",
		"outcome":"success","trigger":"push","trigger_ref":"refs/heads/main","head_sha":"abc123",
		"queued_at":"2026-10-17T10:00:00Z","steps":[
		{"id":"step-1","step_key":"lint","stage":"lint","kind":"run","status":"concluded","outcome":"success"}]}`

	timeSavedTestSavingsBody = `{"run_id":"` + timeSavedTestRunID + `","saved_seconds":840,"saved_cpu_minutes":null,
		"estimated_count":2,"unknown_count":1,"savings":[
		{"step_key":"build","stage":"build","reason":"path_filter","cause_stage":null,"basis":"stage_history",
		 "sample_count":20,"saved_seconds":720,"saved_cpu_minutes":null,"source_run_id":null},
		{"step_key":"e2e","stage":"e2e","reason":"dependency_skipped","cause_stage":"build","basis":"no_history",
		 "sample_count":0,"saved_seconds":null,"saved_cpu_minutes":null,"source_run_id":null},
		{"step_key":"test","stage":"test","reason":"promoted","cause_stage":null,"basis":"promoted_source_run",
		 "sample_count":1,"saved_seconds":120,"saved_cpu_minutes":null,"source_run_id":"run-41"}],
		"method":{"statistic":"median","sample_window":20,"history_run_scan":300}}`

	timeSavedTestEmptyBody = `{"run_id":"` + timeSavedTestRunID + `","saved_seconds":0,"saved_cpu_minutes":null,
		"estimated_count":0,"unknown_count":0,"savings":[],
		"method":{"statistic":"median","sample_window":20,"history_run_scan":300}}`
)

// serveRunWithTimeSaved serves the run detail and answers the time-saved
// route with status and body.
func serveRunWithTimeSaved(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	runPath := "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID + "/pipeline-runs/" + timeSavedTestRunID
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case runPath:
			_, _ = fmt.Fprint(writer, timeSavedTestRunBody)
		case runPath + "/time-saved":
			writer.WriteHeader(status)
			_, _ = fmt.Fprint(writer, body)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// runPipelineGetAgainst runs `pipeline get` against server and returns stdout
// and stderr apart, so structured output can be checked byte for byte.
func runPipelineGetAgainst(t *testing.T, server *httptest.Server, extra ...string) (string, string, error) {
	t.Helper()
	useTestClient(t, server.URL)
	pipelineCommand := newPipelineCommand()
	var stdout, stderr bytes.Buffer
	pipelineCommand.SetOut(&stdout)
	pipelineCommand.SetErr(&stderr)
	pipelineCommand.SetArgs(append([]string{"get", timeSavedTestRunID, "--repository", testPipelineRepositoryID}, extra...))
	executeError := pipelineCommand.Execute()
	return stdout.String(), stderr.String(), executeError
}

func TestPipelineGetPrintsTheTimeTheRunSaved(t *testing.T) {
	server := serveRunWithTimeSaved(t, http.StatusOK, timeSavedTestSavingsBody)
	stdout, stderr, executeError := runPipelineGetAgainst(t, server)
	if executeError != nil {
		t.Fatalf("pipeline get: %v", executeError)
	}
	expected := "\nTime saved: about 14 min of CI time (3 stages skipped, 1 unknown)\n" +
		"  build: left out by a path filter, about 12 min (median of 20 successful runs of this stage)\n" +
		"  e2e: skipped because build was left out by a path filter, unknown (no history to estimate from)\n" +
		"  test: not run again, the publish was promoted, about 2 min (its time in run run-41)\n"
	if !strings.HasSuffix(stdout, expected) {
		t.Fatalf("output does not end with the time saved:\nwant suffix:\n%s\ngot:\n%s", expected, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

func TestPipelineGetPrintsNothingWhenTheRunSavedNothing(t *testing.T) {
	server := serveRunWithTimeSaved(t, http.StatusOK, timeSavedTestEmptyBody)
	stdout, stderr, executeError := runPipelineGetAgainst(t, server)
	if executeError != nil {
		t.Fatalf("pipeline get: %v", executeError)
	}
	if strings.Contains(stdout, "Time saved") || stderr != "" {
		t.Fatalf("expected no time saved output, got stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// A platform older than the route answers 404: `pipeline get` prints the run
// exactly as it did before, with no note and no failure.
func TestPipelineGetToleratesAPlatformWithoutTheTimeSavedRoute(t *testing.T) {
	server := serveRunWithTimeSaved(t, http.StatusNotFound, `{"detail":"Not Found"}`)
	stdout, stderr, executeError := runPipelineGetAgainst(t, server)
	if executeError != nil {
		t.Fatalf("pipeline get failed on a 404 from the time-saved route: %v", executeError)
	}
	if !strings.Contains(stdout, "Run #42") || strings.Contains(stdout, "Time saved") || stderr != "" {
		t.Fatalf("expected the run alone, got stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// Any other error leaves the run printed and says so on stderr, once.
func TestPipelineGetKeepsTheRunWhenTheTimeSavedReadFails(t *testing.T) {
	server := serveRunWithTimeSaved(t, http.StatusInternalServerError, `{"detail":"database unavailable"}`)
	stdout, stderr, executeError := runPipelineGetAgainst(t, server, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline get failed on a time-saved error: %v", executeError)
	}
	if !strings.Contains(stderr, "could not read the time this run saved: database unavailable") {
		t.Errorf("stderr = %q", stderr)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if value, isPresent := decoded["time_saved"]; !isPresent || value != nil {
		t.Errorf("time_saved = %v (present %v), want null", value, isPresent)
	}
}

// baselineRunEncoding is what `pipeline get -o <format>` printed before the
// time-saved field: the run detail encoded on its own.
func baselineRunEncoding(t *testing.T, format outputFormat) string {
	t.Helper()
	var detail client.PipelineRunDetail
	if decodeError := json.Unmarshal([]byte(timeSavedTestRunBody), &detail); decodeError != nil {
		t.Fatalf("decoding the run: %v", decodeError)
	}
	var baseline bytes.Buffer
	if encodeError := encodeStructured(&baseline, format, &detail); encodeError != nil {
		t.Fatalf("encoding the run: %v", encodeError)
	}
	return baseline.String()
}

// -o json adds time_saved after the run's last field and leaves every other
// byte as it was.
func TestPipelineGetJSONAddsTimeSavedAndKeepsEveryExistingField(t *testing.T) {
	baseline := baselineRunEncoding(t, outputJSON)
	if !strings.HasSuffix(baseline, "\n}\n") {
		t.Fatalf("unexpected baseline shape:\n%s", baseline)
	}

	unavailable := serveRunWithTimeSaved(t, http.StatusNotFound, `{"detail":"Not Found"}`)
	stdout, _, executeError := runPipelineGetAgainst(t, unavailable, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline get -o json: %v", executeError)
	}
	want := strings.TrimSuffix(baseline, "\n}\n") + ",\n  \"time_saved\": null\n}\n"
	if stdout != want {
		t.Fatalf("-o json changed beyond the added field:\nwant:\n%s\ngot:\n%s", want, stdout)
	}

	withSavings := serveRunWithTimeSaved(t, http.StatusOK, timeSavedTestSavingsBody)
	stdout, _, executeError = runPipelineGetAgainst(t, withSavings, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline get -o json: %v", executeError)
	}
	if !strings.HasPrefix(stdout, strings.TrimSuffix(baseline, "\n}\n")+",\n  \"time_saved\": {") {
		t.Fatalf("the run's fields must come first, unchanged:\n%s", stdout)
	}
	var decoded struct {
		ID        string                       `json:"id"`
		TimeSaved *client.PipelineRunTimeSaved `json:"time_saved"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v", decodeError)
	}
	if decoded.ID != timeSavedTestRunID || decoded.TimeSaved == nil || decoded.TimeSaved.SavedSeconds != 840 ||
		len(decoded.TimeSaved.Savings) != 3 || decoded.TimeSaved.Savings[1].SavedSeconds != nil {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestPipelineGetYAMLAddsTimeSavedAndKeepsEveryExistingField(t *testing.T) {
	baseline := baselineRunEncoding(t, outputYAML)
	server := serveRunWithTimeSaved(t, http.StatusNotFound, `{"detail":"Not Found"}`)
	stdout, _, executeError := runPipelineGetAgainst(t, server, "-o", "yaml")
	if executeError != nil {
		t.Fatalf("pipeline get -o yaml: %v", executeError)
	}
	if want := baseline + "time_saved: null\n"; stdout != want {
		t.Fatalf("-o yaml changed beyond the added field:\nwant:\n%s\ngot:\n%s", want, stdout)
	}
}

func TestDescribeSavedSeconds(t *testing.T) {
	for seconds, want := range map[int64]string{
		0: "about 0 s", 45: "about 45 s", 60: "about 1 min", 840: "about 14 min",
		3600: "about 1 h", 7500: "about 2 h 5 min",
	} {
		if got := describeSavedSeconds(seconds); got != want {
			t.Errorf("describeSavedSeconds(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestPipelineTimeSavedSummaryReadsTheOrganisation(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requested = request.URL.Path + "?" + request.URL.RawQuery
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"window":"7d","from":"2026-10-10T00:00:00Z","to":"2026-10-17T00:00:00Z",
			"runs":12,"saved_seconds":5400,"saved_cpu_minutes":null,"estimated_count":30,"unknown_count":2,
			"reasons":[{"reason":"path_filter","saved_seconds":3600,"saved_cpu_minutes":null,"estimated_count":20,"unknown_count":2},
			{"reason":"promoted","saved_seconds":1800,"saved_cpu_minutes":null,"estimated_count":10,"unknown_count":0}],
			"repositories":[{"repository_id":"repo-1","full_name":"acme/api","runs":12,"saved_seconds":5400,
			"saved_cpu_minutes":null,"estimated_count":30,"unknown_count":2}],
			"days":[],"method":{"statistic":"median","sample_window":20,"history_run_scan":300}}`)
	}))
	t.Cleanup(server.Close)
	useTestClient(t, server.URL)
	pipelineCommand := newPipelineCommand()
	var stdout bytes.Buffer
	pipelineCommand.SetOut(&stdout)
	pipelineCommand.SetErr(&stdout)
	pipelineCommand.SetArgs([]string{"time-saved", "--window", "7d"})
	if executeError := pipelineCommand.Execute(); executeError != nil {
		t.Fatalf("pipeline time-saved: %v", executeError)
	}
	if requested != "/api/v1/org/pipelines/time-saved?window=7d" {
		t.Errorf("requested %q", requested)
	}
	want := "Time saved in the last 7 days: about 1 h 30 min of CI time across 12 runs " +
		"(2 skipped with no history, not counted)\n" +
		"\nBy reason:\n" +
		"  stages left out by a path filter: about 1 h\n" +
		"  steps a promoted publish did not run again: about 30 min\n" +
		"\nBy repository:\n" +
		"  acme/api: about 1 h 30 min (12 runs)\n"
	if stdout.String() != want {
		t.Fatalf("output:\nwant:\n%s\ngot:\n%s", want, stdout.String())
	}
}

func TestPipelineTimeSavedRefusesAnUnknownWindow(t *testing.T) {
	pipelineCommand := newPipelineCommand()
	var output bytes.Buffer
	pipelineCommand.SetOut(&output)
	pipelineCommand.SetErr(&output)
	pipelineCommand.SetArgs([]string{"time-saved", "--window", "90d"})
	executeError := pipelineCommand.Execute()
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("error = %v, want a usage error", executeError)
	}
}
