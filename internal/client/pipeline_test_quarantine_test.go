package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const quarantineTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const quarantineJSON = `{"id":"q-1","test_key":"` + quarantineTestKey + `","suite":"orders","name":"TestCheckout",
	"file":"orders/checkout_test.go","reason":"times out","source":"manual","flaky_rate":0.25,"samples":40,
	"created_by_email":"dev@example.com","created_at":"2026-10-01T10:00:00Z","consecutive_passes":3,
	"released_at":null,"released_by_email":null,"release_reason":null,"release_note":null}`

// The quarantine routes' parity table: a path or method typo fails as a 404
// against a real platform and passes every test that only checks the
// command's flag plumbing, so each route is pinned on the request itself,
// on both selectors.
func TestPipelineTestQuarantineRoutes(t *testing.T) {
	enabled := true
	rate := 0.2
	samples := 30
	lanes := []struct {
		name      string
		method    string
		path      string
		query     string
		body      string
		response  string
		status    int
		call      func(testClient *Client, selector PipelineSelector) (any, error)
		checkBody func(t *testing.T, result any)
	}{
		{
			name: "list", method: http.MethodGet, path: "/pipeline-tests/quarantine",
			response: `{"settings":{"enabled":true,"min_flaky_rate":0.1,"min_samples":20,"release_after_passes":10},
				"quarantines":[` + quarantineJSON + `],"recently_released":[]}`,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.GetPipelineTestQuarantine(context.Background(), selector)
			},
			checkBody: func(t *testing.T, result any) {
				view := result.(*PipelineTestQuarantineView)
				if !view.Settings.Enabled || view.Settings.MinSamples != 20 || len(view.Quarantines) != 1 ||
					view.Quarantines[0].ConsecutivePasses != 3 || view.Quarantines[0].ReleasedAt != nil ||
					*view.Quarantines[0].Suite != "orders" {
					t.Errorf("view = %+v", view)
				}
			},
		},
		{
			name: "settings sends only what was named", method: http.MethodPut,
			path:     "/pipeline-tests/quarantine/settings",
			body:     `{"enabled":true,"min_flaky_rate":0.2,"min_samples":30}`,
			response: `{"enabled":true,"min_flaky_rate":0.2,"min_samples":30,"release_after_passes":10}`,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.SetPipelineTestQuarantineSettings(context.Background(), selector,
					PipelineTestQuarantineSettingsUpdate{Enabled: &enabled, MinFlakyRate: &rate, MinSamples: &samples})
			},
			checkBody: func(t *testing.T, result any) {
				if settings := result.(*PipelineTestQuarantineSettings); settings.ReleaseAfterPasses != 10 {
					t.Errorf("settings = %+v", settings)
				}
			},
		},
		{
			name: "candidates with a limit", method: http.MethodGet, path: "/pipeline-tests/quarantine/candidates",
			query: "limit=5",
			response: `{"default_branch":"main","settings":{"enabled":false,"min_flaky_rate":0.1,"min_samples":20,
				"release_after_passes":10},"candidates":[{"test_key":"` + quarantineTestKey + `","suite":"orders",
				"name":"TestCheckout","file":"orders/checkout_test.go","flaky_rate":0.25,"flaky":10,"observed":40}]}`,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.ListPipelineTestQuarantineCandidates(context.Background(), selector, 5)
			},
			checkBody: func(t *testing.T, result any) {
				candidates := result.(*PipelineTestQuarantineCandidates)
				if candidates.DefaultBranch != "main" || len(candidates.Candidates) != 1 ||
					candidates.Candidates[0].Observed != 40 {
					t.Errorf("candidates = %+v", candidates)
				}
			},
		},
		{
			name: "candidates leave the limit to the server", method: http.MethodGet,
			path: "/pipeline-tests/quarantine/candidates", response: `{"default_branch":"main","candidates":[]}`,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.ListPipelineTestQuarantineCandidates(context.Background(), selector, 0)
			},
		},
		{
			name: "add", method: http.MethodPost, path: "/pipeline-tests/quarantine", status: http.StatusCreated,
			body:     `{"test_key":"` + quarantineTestKey + `","reason":"times out"}`,
			response: quarantineJSON,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.QuarantinePipelineTest(context.Background(), selector,
					QuarantinePipelineTestRequest{TestKey: quarantineTestKey, Reason: "times out"})
			},
			checkBody: func(t *testing.T, result any) {
				if quarantine := result.(*PipelineTestQuarantine); quarantine.ID != "q-1" || quarantine.Samples != 40 {
					t.Errorf("quarantine = %+v", quarantine)
				}
			},
		},
		{
			name: "release with a reason", method: http.MethodPost, path: "/pipeline-tests/quarantine/q-1/release",
			body: `{"reason":"fixed"}`, response: quarantineJSON,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.ReleasePipelineTestQuarantine(context.Background(), selector, "q-1", "fixed")
			},
		},
		{
			// The body is optional on the platform, but a strict decoder
			// still reads it: an empty object is sent, never "null".
			name: "release without a reason", method: http.MethodPost, path: "/pipeline-tests/quarantine/q-1/release",
			body: `{}`, response: quarantineJSON,
			call: func(testClient *Client, selector PipelineSelector) (any, error) {
				return testClient.ReleasePipelineTestQuarantine(context.Background(), selector, "q-1", "")
			},
		},
	}
	selectors := map[string]PipelineSelector{
		"/api/v1/org/applications/app-1":           {ApplicationID: "app-1"},
		"/api/v1/org/pipeline-repositories/repo-1": {RepositoryID: "repo-1"},
	}
	for _, lane := range lanes {
		for base, selector := range selectors {
			t.Run(lane.name+" "+base, func(t *testing.T) {
				var seenMethod, seenPath, seenQuery, seenBody string
				testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
					seenMethod, seenPath, seenQuery = request.Method, request.URL.Path, request.URL.RawQuery
					body, _ := io.ReadAll(request.Body)
					seenBody = string(body)
					writer.Header().Set("Content-Type", "application/json")
					status := lane.status
					if status == 0 {
						status = http.StatusOK
					}
					writer.WriteHeader(status)
					_, _ = fmt.Fprint(writer, lane.response)
				})
				result, callError := lane.call(testClient, selector)
				if callError != nil {
					t.Fatalf("call error = %v", callError)
				}
				if seenMethod != lane.method || seenPath != base+lane.path || seenQuery != lane.query {
					t.Errorf("request = %s %s?%s, want %s %s?%s", seenMethod, seenPath, seenQuery,
						lane.method, base+lane.path, lane.query)
				}
				if lane.body == "" {
					if seenBody != "" {
						t.Errorf("a read sent a body: %s", seenBody)
					}
				} else if !sameJSON(t, seenBody, lane.body) {
					t.Errorf("body = %s, want %s", seenBody, lane.body)
				}
				if lane.checkBody != nil {
					lane.checkBody(t, result)
				}
			})
		}
	}
}

func sameJSON(t *testing.T, got string, want string) bool {
	t.Helper()
	var gotValue, wantValue any
	if decodeError := json.Unmarshal([]byte(got), &gotValue); decodeError != nil {
		t.Fatalf("request body is not JSON (%v): %s", decodeError, got)
	}
	if decodeError := json.Unmarshal([]byte(want), &wantValue); decodeError != nil {
		t.Fatalf("expected body is not JSON (%v): %s", decodeError, want)
	}
	gotEncoded, _ := json.Marshal(gotValue)
	wantEncoded, _ := json.Marshal(wantValue)
	return string(gotEncoded) == string(wantEncoded)
}

// An empty update sends an empty object: no member is ever sent as null,
// which the platform refuses.
func TestPipelineTestQuarantineSettingsUpdateOmitsUnnamedMembers(t *testing.T) {
	encoded, marshalError := json.Marshal(PipelineTestQuarantineSettingsUpdate{})
	if marshalError != nil || string(encoded) != "{}" {
		t.Fatalf("empty update = %s (%v), want {}", encoded, marshalError)
	}
	disabled := false
	encoded, _ = json.Marshal(PipelineTestQuarantineSettingsUpdate{Enabled: &disabled})
	if string(encoded) != `{"enabled":false}` {
		t.Errorf("enabled false = %s, want it sent", encoded)
	}
	if !(PipelineTestQuarantineSettingsUpdate{}).IsEmpty() ||
		(PipelineTestQuarantineSettingsUpdate{Enabled: &disabled}).IsEmpty() {
		t.Error("IsEmpty disagrees with the members")
	}
}

// The platform's refusals reach the caller as its own sentences: the 409s
// as the detail string, the pydantic 422 as field: message, and the RBAC 403
// as a typed permission denial.
func TestPipelineTestQuarantineRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		status   int
		body     string
		expected string
	}{
		"quarantine off": {status: http.StatusConflict,
			body:     `{"detail":"Test quarantine is off for this repository; turn it on first."}`,
			expected: "Test quarantine is off for this repository; turn it on first."},
		"not flaky enough": {status: http.StatusConflict,
			body:     `{"detail":"This test is not flaky enough on the default branch to quarantine."}`,
			expected: "This test is not flaky enough on the default branch to quarantine."},
		"no history": {status: http.StatusNotFound,
			body:     `{"detail":"This test has no recorded results on the repository's default branch in the last 30 days."}`,
			expected: "This test has no recorded results on the repository's default branch in the last 30 days."},
		"reason too long": {status: http.StatusUnprocessableEntity,
			body:     `{"detail":[{"loc":["body","reason"],"msg":"String should have at most 500 characters"}]}`,
			expected: "reason: String should have at most 500 characters"},
	} {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(writer, testCase.body)
			})
			_, quarantineError := testClient.QuarantinePipelineTest(context.Background(),
				PipelineSelector{ApplicationID: "app-1"},
				QuarantinePipelineTestRequest{TestKey: quarantineTestKey, Reason: "x"})
			if quarantineError == nil || quarantineError.Error() != testCase.expected {
				t.Fatalf("error = %v, want %q", quarantineError, testCase.expected)
			}
		})
	}

	t.Run("403 names the permission", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusForbidden,
				map[string]any{"detail": "permission_denied", "permission": "pipelines.manage"})
		})
		_, releaseError := testClient.ReleasePipelineTestQuarantine(context.Background(),
			PipelineSelector{RepositoryID: "repo-1"}, "q-1", "")
		var denied *PermissionDeniedError
		if !errors.As(releaseError, &denied) || denied.Permission != "pipelines.manage" {
			t.Fatalf("error = %v, want a permission denial naming pipelines.manage", releaseError)
		}
	})

	t.Run("no selector is refused before any request", func(t *testing.T) {
		requested := false
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) { requested = true })
		if _, readError := testClient.GetPipelineTestQuarantine(context.Background(), PipelineSelector{}); readError == nil {
			t.Fatal("an empty selector was accepted")
		}
		if requested {
			t.Error("an empty selector reached the server")
		}
	})
}

// The quarantine id reaches the client from the command line, so it is
// escaped into one path segment.
func TestReleasePipelineTestQuarantineEscapesTheID(t *testing.T) {
	var seenPath string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.EscapedPath()
		_, _ = fmt.Fprint(writer, quarantineJSON)
	})
	if _, releaseError := testClient.ReleasePipelineTestQuarantine(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "a/../b", ""); releaseError != nil {
		t.Fatalf("release error = %v", releaseError)
	}
	if !strings.HasSuffix(seenPath, "/pipeline-tests/quarantine/a%2F..%2Fb/release") {
		t.Errorf("path = %s, want the id escaped into one segment", seenPath)
	}
}

// The run summary's quarantined failures and the run detail's per-step count
// decode when the platform sends them, and an older platform's bodies decode
// to nothing new: no quarantined list, no count.
func TestPipelineRunTestsAndStepsDecodeTheQuarantine(t *testing.T) {
	var summary PipelineRunTests
	if decodeError := json.Unmarshal([]byte(`{"run_id":"run-1","has_reports":true,"quarantined":[
		{"test_key":"`+quarantineTestKey+`","suite":"orders","name":"TestCheckout","file":"f","stage":"test",
		"step_key":"unit","failure_message":"boom","did_rescue_step":true,"quarantine_id":null}]}`),
		&summary); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	if len(summary.Quarantined) != 1 || !summary.Quarantined[0].DidRescueStep ||
		summary.Quarantined[0].QuarantineID != nil || summary.Quarantined[0].StepKey != "unit" {
		t.Errorf("quarantined = %+v", summary.Quarantined)
	}

	var older PipelineRunTests
	if decodeError := json.Unmarshal([]byte(`{"run_id":"run-1","has_reports":true}`), &older); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	encoded, _ := json.Marshal(older)
	if strings.Contains(string(encoded), "quarantined") {
		t.Errorf("an older platform's summary gained a quarantined member: %s", encoded)
	}

	var step PipelineStep
	if decodeError := json.Unmarshal([]byte(`{"id":"s-1","quarantined_test_failures":2}`), &step); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	if step.QuarantinedTestFailures == nil || *step.QuarantinedTestFailures != 2 {
		t.Errorf("quarantined_test_failures = %v, want 2", step.QuarantinedTestFailures)
	}
}
