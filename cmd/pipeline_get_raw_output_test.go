package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// rawOutputTestRunBody is a run detail from a platform newer than this CLI:
// a run field and a step field the CLI does not declare, a step field it
// declares since ankra-ga9b8k, and an integer no float64 or int64 holds
// exactly. Fields the struct declares but the platform left out (queue_reason,
// approve_definition_id) are absent on purpose.
const rawOutputTestRunBody = `{"id":"` + timeSavedTestRunID + `","run_number":42,"status":"concluded",
	"outcome":"success","trigger":"push","future_run_field":{"nested":[1,2]},"steps":[
	{"id":"step-1","step_key":"build","stage":"build","kind":"run","status":"concluded","outcome":"success",
	 "memory_request_bytes":8589934592,"memory_peak_bytes":7516192768,
	 "memory_working_set_peak_bytes":5690831667,"future_step_field":12345678901234567890}]}`

// serveRunDocument answers the run detail with body and the time-saved route
// with timeSavedTestSavingsBody, through a real client.
func serveRunDocument(t *testing.T, body string) *httptest.Server {
	t.Helper()
	runPath := "/api/v1/org/pipeline-repositories/" + testPipelineRepositoryID + "/pipeline-runs/" + timeSavedTestRunID
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case runPath:
			_, _ = fmt.Fprint(writer, body)
		case runPath + "/time-saved":
			_, _ = fmt.Fprint(writer, timeSavedTestSavingsBody)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// -o json prints the run as the platform answered it, with time_saved added
// last (ankra-ga9b8k). It used to re-encode the CLI's struct, so a field the
// platform added later - memory_working_set_peak_bytes on run #9245 - was
// silently absent and a script concluded it was unset.
func TestPipelineGetJSONPrintsEveryFieldThePlatformSent(t *testing.T) {
	server := serveRunDocument(t, rawOutputTestRunBody)
	stdout, stderr, executeError := runPipelineGetAgainst(t, server, "-o", "json")
	if executeError != nil {
		t.Fatalf("pipeline get -o json: %v", executeError)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.UseNumber()
	var document map[string]any
	if decodeError := decoder.Decode(&document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if _, isPresent := document["future_run_field"]; !isPresent {
		t.Errorf("a run field this CLI does not declare was dropped:\n%s", stdout)
	}
	steps, _ := document["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("steps = %v", document["steps"])
	}
	step, _ := steps[0].(map[string]any)
	for field, want := range map[string]string{
		"memory_working_set_peak_bytes": "5690831667",
		"memory_peak_bytes":             "7516192768",
		"future_step_field":             "12345678901234567890",
	} {
		if got, _ := step[field].(json.Number); string(got) != want {
			t.Errorf("step %s = %v, want the literal %s", field, step[field], want)
		}
	}
	for _, invented := range []string{"queue_reason", "approve_definition_id"} {
		if _, isPresent := document[invented]; isPresent {
			t.Errorf("%s was not in the platform's answer, so -o json must not print it", invented)
		}
	}
	timeSaved, _ := document["time_saved"].(map[string]any)
	if timeSaved == nil || timeSaved["saved_seconds"] != json.Number("840") {
		t.Errorf("time_saved = %v, want the time-saved read", document["time_saved"])
	}
	if strings.LastIndex(stdout, `"time_saved"`) < strings.LastIndex(stdout, `"steps"`) {
		t.Errorf("time_saved must come after the platform's fields:\n%s", stdout)
	}
}

func TestPipelineGetYAMLPrintsEveryFieldThePlatformSent(t *testing.T) {
	server := serveRunDocument(t, rawOutputTestRunBody)
	stdout, _, executeError := runPipelineGetAgainst(t, server, "-o", "yaml")
	if executeError != nil {
		t.Fatalf("pipeline get -o yaml: %v", executeError)
	}
	for _, expected := range []string{
		"\nfuture_run_field:\n",
		"memory_working_set_peak_bytes: 5690831667\n",
		"future_step_field: 12345678901234567890\n",
		"\ntime_saved:\n  run_id: " + timeSavedTestRunID + "\n",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("-o yaml missing %q:\n%s", expected, stdout)
		}
	}
	if strings.Contains(stdout, "queue_reason") {
		t.Errorf("-o yaml printed a field the platform did not send:\n%s", stdout)
	}
}

// `run --wait -o json` prints the waited-for run the same way: it encodes the
// detail the last read decoded, and that detail carries its document.
func TestPipelineRunWaitJSONPrintsEveryFieldThePlatformSent(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	concluded := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	concluded.RawDocument = client.NewRawDocument([]byte(`{"id":"run-44","run_number":44,"status":"concluded",` +
		`"outcome":"success","future_run_field":"kept","steps":[]}`))
	mockClient := &pipelineLaneMock{
		createResult: &client.CreatePipelineRunResult{PipelineRunID: "run-44", RunNumber: 44},
		getResult:    &concluded,
	}
	stdout, _, executeError := runPipelineCommandSeparately(t, mockClient, "run", "--application", testApplicationID,
		"--sha", strings.Repeat("b", 40), "--wait", "-o", "json")
	if executeError != nil {
		t.Fatalf("run --wait -o json: %v", executeError)
	}
	if !strings.Contains(stdout, `"future_run_field": "kept"`) {
		t.Errorf("stdout = %q, want the platform's document", stdout)
	}
}

// `get --watch -o json` carries each run and step as the platform answered
// it, which its help has always promised: a field this CLI does not declare
// is kept, the run carries no steps member, and the top-level members are
// unchanged.
func TestPipelineGetWatchJSONCarriesTheRunAndStepsThePlatformSent(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	withDocument := func(detail client.PipelineRunDetail, document string) client.PipelineRunDetail {
		detail.RawDocument = client.NewRawDocument([]byte(document))
		return detail
	}
	running := pipelineStepFixture("step-build", "build", "running", nil)
	succeeded := pipelineStepFixture("step-build", "build", "concluded", strPipelinePtr("success"))
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		withDocument(pipelineRunDetailFixture("running", nil, running),
			`{"id":"run-44","run_number":44,"status":"running","outcome":null,"future_run_field":1,`+
				`"steps":[{"id":"step-build","step_key":"build","status":"running","outcome":null}]}`),
		withDocument(pipelineRunDetailFixture("concluded", strPipelinePtr("success"), succeeded),
			`{"id":"run-44","run_number":44,"status":"concluded","outcome":"success","future_run_field":2,`+
				`"steps":[{"id":"step-build","step_key":"build","status":"concluded","outcome":"success",`+
				`"memory_working_set_peak_bytes":5690831667,"future_step_field":"kept"}]}`),
	}}
	stdout, _, executeError := runPipelineCommandSeparately(t, mockClient, "get", "run-44",
		"--application", testApplicationID, "--watch", "-o", "json")
	if executeError != nil {
		t.Fatalf("get --watch -o json: %v", executeError)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a step and a run event per read, got:\n%s", stdout)
	}
	last := struct {
		Event   string         `json:"event"`
		Status  string         `json:"status"`
		Outcome *string        `json:"outcome"`
		Run     map[string]any `json:"run"`
	}{}
	var concludedStep map[string]any
	for index, line := range lines {
		var event map[string]any
		if decodeError := json.Unmarshal([]byte(line), &event); decodeError != nil {
			t.Fatalf("line %d is not one JSON object: %v", index, decodeError)
		}
		if index == 2 {
			concludedStep, _ = event["step"].(map[string]any)
		}
		if index == 3 {
			if decodeError := json.Unmarshal([]byte(line), &last); decodeError != nil {
				t.Fatalf("line %d: %v", index, decodeError)
			}
		}
	}
	if concludedStep["future_step_field"] != "kept" || concludedStep["memory_working_set_peak_bytes"] != float64(5690831667) {
		t.Errorf("step = %v, want the platform's step", concludedStep)
	}
	if _, isPresent := concludedStep["cache_result"]; isPresent {
		t.Errorf("step = %v, want no member the platform did not send", concludedStep)
	}
	if last.Event != "run" || last.Status != "concluded" || last.Outcome == nil || *last.Outcome != "success" {
		t.Errorf("last event = %+v, want the run's conclusion", last)
	}
	if last.Run["future_run_field"] != float64(2) {
		t.Errorf("run = %v, want the platform's run", last.Run)
	}
	if _, hasSteps := last.Run["steps"]; hasSteps {
		t.Errorf("run = %v, want the steps member left out: each step has its own event", last.Run)
	}
	if !strings.HasPrefix(lines[3], `{"event":"run","observed_at":`) || !strings.Contains(lines[3], `,"run":{"id":"run-44",`) {
		t.Errorf("the event's own members must keep their order, got %s", lines[3])
	}
}

// A detail with no document - a test mock, or one whose steps do not line up
// with its decode - watches as the CLI decoded it, as before.
func TestPipelineRunDocumentPartsRefusesADocumentThatDoesNotMatch(t *testing.T) {
	detail := pipelineRunDetailFixture("running", nil, pipelineStepFixture("step-1", "build", "running", nil))
	if _, _, isCut := pipelineRunDocumentParts(&detail); isCut {
		t.Fatal("a detail without a document has nothing to cut")
	}
	detail.RawDocument = client.NewRawDocument([]byte(`{"id":"run-44","steps":[]}`))
	if _, _, isCut := pipelineRunDocumentParts(&detail); isCut {
		t.Fatal("a document with fewer steps than the decode must not be cut")
	}
	detail.RawDocument = client.NewRawDocument([]byte(`{"id":"run-44","steps":[{"id":"step-1"}],"after":true}`))
	run, steps, isCut := pipelineRunDocumentParts(&detail)
	if !isCut || string(run) != `{"id":"run-44","after":true}` || len(steps) != 1 || string(steps[0]) != `{"id":"step-1"}` {
		t.Fatalf("cut = %v, run %s, steps %s", isCut, run, steps)
	}
}

func TestAppendJSONObjectMember(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		want     string
	}{
		{name: "an empty object", document: `{}`, want: `{"time_saved":1}`},
		{name: "members kept byte for byte", document: " {\"b\" : 2, \"a\":1} \n", want: `{"b" : 2, "a":1,"time_saved":1}`},
		{name: "the platform's own value wins", document: `{"time_saved":null,"a":1}`, want: `{"time_saved":null,"a":1}`},
		{name: "not an object", document: `[1]`},
		{name: "not JSON", document: `{"a":`},
		{name: "no document", document: ``},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := appendJSONObjectMember(json.RawMessage(testCase.document), "time_saved", 1)
			if testCase.want == "" {
				if got != nil {
					t.Fatalf("got %s, want nil so the caller encodes its struct", got)
				}
				return
			}
			if string(got) != testCase.want {
				t.Fatalf("got %s, want %s", got, testCase.want)
			}
		})
	}
}

// A detail with no document - built in code, a test mock - prints as the
// struct, as before.
func TestPipelineGetOutputWithoutADocumentEncodesTheStruct(t *testing.T) {
	detail := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	var encoded bytes.Buffer
	if encodeError := encodeStructured(&encoded, outputJSON, newPipelineGetOutput(detail, nil)); encodeError != nil {
		t.Fatalf("encode: %v", encodeError)
	}
	if !strings.Contains(encoded.String(), `"queue_reason": ""`) ||
		!strings.HasSuffix(encoded.String(), ",\n  \"time_saved\": null\n}\n") {
		t.Fatalf("want the struct encoding with time_saved last, got:\n%s", encoded.String())
	}
}
