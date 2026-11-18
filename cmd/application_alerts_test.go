package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

// These drive `ankra application alerts` through the real client against a
// stub platform, so what is asserted is the request that leaves and what the
// command prints from the answer.

const (
	alertsTestEnvironmentID = "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d"
	alertsTestErrorsID      = "8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6a"
	alertsTestOrgRuleID     = "c4a2e6f1-7d3b-4a5c-8e9f-0a1b2c3d4e5f"
	alertsTestSlackID       = "3f6c2a8e-1b4d-4c7a-9e2f-5d8b7a6c4e1f"
	alertsTestPagerID       = "7b2d9e4c-3a1f-4b6e-8d5c-9e0f1a2b3c4d"
	alertsTestCollection    = "/api/v1/org/applications/" + testApplicationID + "/alerts"
	alertsTestErrorsPath    = alertsTestCollection + "/" + alertsTestErrorsID
	alertsTestAutoDeploy    = "/api/v1/org/applications/" + testApplicationID + "/auto-deploy"
)

const alertsTestErrorsAlert = `{
  "alert_id": "8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6a",
  "rule_id": "11111111-2222-4333-8444-555555555555",
  "name": "Errors",
  "own": true,
  "environment_id": "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d",
  "says": "failed requests above 5% for 10 minutes",
  "condition": {"condition_type": "app_metric", "metric": "failed_share_pct", "route": null, "operator": "gt", "threshold": 5, "hold_minutes": 10},
  "severity": "critical",
  "send_to": [{"integration_id": "3f6c2a8e-1b4d-4c7a-9e2f-5d8b7a6c4e1f", "type": "slack", "label": "ops-slack"}],
  "enabled": true,
  "last_firing": {
    "trigger_id": "99999999-8888-4777-8666-555555555555",
    "triggered_at": "2026-11-18T09:30:00Z",
    "resolved_at": null,
    "acknowledged_by": {"user_id": "u-1", "name": "On-call engineer"},
    "acknowledged_after_seconds": 120,
    "resolved_by": null,
    "resolution_note": null
  },
  "evaluated_at": "2026-11-18T10:00:00Z",
  "evaluation": "ok",
  "unknown_reason": null
}`

const alertsTestListPayload = `{
  "alerts": [
    ` + alertsTestErrorsAlert + `,
    {
      "alert_id": "8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6b",
      "rule_id": "11111111-2222-4333-8444-555555555556",
      "name": "Slow responses",
      "own": true,
      "environment_id": "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d",
      "says": "p95 response time above 800 ms for 5 minutes",
      "condition": {"condition_type": "app_metric", "metric": "p95_ms", "operator": "gt", "threshold": 800, "hold_minutes": 5},
      "severity": "warning",
      "send_to": [
        {"integration_id": "3f6c2a8e-1b4d-4c7a-9e2f-5d8b7a6c4e1f", "type": "slack", "label": "ops-slack"},
        {"integration_id": "7b2d9e4c-3a1f-4b6e-8d5c-9e0f1a2b3c4d", "type": "pagerduty", "label": "pager"}
      ],
      "enabled": true,
      "last_firing": null,
      "evaluated_at": null,
      "evaluation": "unknown",
      "unknown_reason": "no_installations"
    },
    {
      "alert_id": "8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6c",
      "rule_id": "11111111-2222-4333-8444-555555555557",
      "name": "Restarts",
      "own": true,
      "environment_id": "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d",
      "says": "restarts above 3 for 15 minutes",
      "condition": {"condition_type": "app_metric", "metric": "restarts", "operator": "gt", "threshold": 3, "hold_minutes": 15},
      "severity": "info",
      "send_to": [{"integration_id": "3f6c2a8e-1b4d-4c7a-9e2f-5d8b7a6c4e1f", "type": "slack", "label": "ops-slack"}],
      "enabled": false,
      "last_firing": null,
      "evaluated_at": null,
      "evaluation": "not_evaluated",
      "unknown_reason": null
    },
    {
      "alert_id": "c4a2e6f1-7d3b-4a5c-8e9f-0a1b2c3d4e5f",
      "rule_id": "11111111-2222-4333-8444-555555555558",
      "name": "Node disk pressure",
      "own": false,
      "environment_id": null,
      "says": "Node disk pressure",
      "condition": null,
      "severity": "critical",
      "send_to": [{"integration_id": "7b2d9e4c-3a1f-4b6e-8d5c-9e0f1a2b3c4d", "type": "pagerduty", "label": "pager"}],
      "enabled": true,
      "last_firing": {
        "trigger_id": "99999999-8888-4777-8666-555555555556",
        "triggered_at": "2026-11-17T08:00:00Z",
        "resolved_at": "2026-11-17T08:20:00Z",
        "acknowledged_by": null,
        "acknowledged_after_seconds": null,
        "resolved_by": "itself",
        "resolution_note": null
      },
      "evaluated_at": "2026-11-18T10:00:00Z",
      "evaluation": "ok",
      "unknown_reason": null
    }
  ],
  "truncated": true,
  "limit": 100,
  "covered_by_organisation_rules": null,
  "answered_by_a_newer_platform": "kept"
}`

const alertsTestAutoDeployPayload = `{"enabled": true, "branch": "main", "environments": [
  {"id": "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d", "name": "production", "role": "production", "enabled": true, "inherited": false, "branches": ["main"]},
  {"id": "6b2e8d4f-0c3a-4f7b-9d2e-3a4b5c6d7e8f", "name": "development", "role": "development", "enabled": true, "inherited": true, "branches": null}
]}`

const alertsTestPermissionDenied = `{"detail": "permission_denied", "permission": "applications.write", "scope_type": "application"}`

type alertsStubAnswer struct {
	status int
	body   string
}

type alertsStubRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// alertsStub is a fake platform: one answer per "METHOD path", and every
// request it received, in order.
type alertsStub struct {
	t        *testing.T
	answers  map[string]alertsStubAnswer
	mutex    sync.Mutex
	requests []alertsStubRequest
}

func newAlertsStub(t *testing.T, answers map[string]alertsStubAnswer) *alertsStub {
	return &alertsStub{t: t, answers: answers}
}

func (stub *alertsStub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	recorded := alertsStubRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery}
	if payload, _ := io.ReadAll(request.Body); len(payload) > 0 {
		if decodeError := json.Unmarshal(payload, &recorded.body); decodeError != nil {
			stub.t.Errorf("request body is not a JSON object: %v: %s", decodeError, payload)
		}
	}
	stub.mutex.Lock()
	stub.requests = append(stub.requests, recorded)
	stub.mutex.Unlock()
	answer, isKnown := stub.answers[request.Method+" "+request.URL.Path]
	if !isKnown {
		stub.t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		answer = alertsStubAnswer{http.StatusNotFound, `{"detail": "Not Found"}`}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(answer.status)
	_, _ = writer.Write([]byte(answer.body))
}

// sent returns the requests made with method to path.
func (stub *alertsStub) sent(method string, path string) []alertsStubRequest {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	matched := []alertsStubRequest{}
	for _, request := range stub.requests {
		if request.method == method && request.path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

func runApplicationAlerts(t *testing.T, stub *alertsStub, input string, arguments ...string) (string, error) {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	return runApplicationCommandWithInput(t, client.New("test-token", server.URL), input,
		append([]string{"alerts"}, arguments...)...)
}

func TestApplicationAlertsListPrintsEachAlertsStateAsThePlatformReportsIt(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID)

	if executeError != nil {
		t.Fatalf("alerts list error = %v", executeError)
	}
	expected := strings.Join([]string{
		"NAME                CONDITION                                     SEVERITY  STATE                                                                SENDS TO          ENVIRONMENT                           ALERT ID",
		"Errors              failed requests above 5% for 10 minutes       critical  firing since 2026-11-18 09:30 UTC, acknowledged by On-call engineer  ops-slack         5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d  8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6a",
		"Slow responses      p95 response time above 800 ms for 5 minutes  warning   unknown (nothing of the application runs on the environment)         ops-slack, pager  5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d  8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6b",
		"Restarts            restarts above 3 for 15 minutes               info      off                                                                  ops-slack         5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d  8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6c",
		"Node disk pressure  Node disk pressure                            critical  ok                                                                   pager             organisation rule                     c4a2e6f1-7d3b-4a5c-8e9f-0a1b2c3d4e5f",
		"Showing the first 100 alerts. The application has more.",
		"",
	}, "\n")
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
	if requests := stub.sent(http.MethodGet, alertsTestCollection); len(requests) != 1 || requests[0].query != "" {
		t.Errorf("list requests = %+v, want one without a query", requests)
	}
}

func TestApplicationAlertsListJSONPrintsThePlatformsDocument(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID, "-o", "json")

	if executeError != nil {
		t.Fatalf("alerts list -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(alertsTestListPayload), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
}

func TestApplicationAlertsListForAnEnvironmentSendsItsIDAndSaysWhetherOrganisationRulesWatchIt(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestAutoDeploy: {http.StatusOK, alertsTestAutoDeployPayload},
		"GET " + alertsTestCollection: {http.StatusOK, `{"alerts": [], "truncated": false, "limit": 100, "covered_by_organisation_rules": false}`},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID, "--environment", "Production")

	if executeError != nil {
		t.Fatalf("alerts list error = %v", executeError)
	}
	requests := stub.sent(http.MethodGet, alertsTestCollection)
	if len(requests) != 1 || requests[0].query != "environment_id="+alertsTestEnvironmentID {
		t.Errorf("list requests = %+v, want environment_id=%s", requests, alertsTestEnvironmentID)
	}
	expected := "No alerts on this environment.\nNo organisation alert rule watches this environment's clusters.\n"
	if output != expected {
		t.Errorf("output = %q, want %q", output, expected)
	}
}

func TestApplicationAlertsListRefusalPrintsThePlatformsReason(t *testing.T) {
	cases := map[string]struct {
		answer           alertsStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"permission denied": {
			alertsStubAnswer{http.StatusForbidden, `{"detail": "permission_denied", "permission": "alerts.read", "scope_type": "application"}`},
			exitForbidden, `"alerts.read"`,
		},
		"not found": {
			alertsStubAnswer{http.StatusNotFound, `{"detail": "Application not found"}`},
			exitNotFound, "Application not found",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newAlertsStub(t, map[string]alertsStubAnswer{"GET " + alertsTestCollection: testCase.answer})

			output, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID)

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if strings.Contains(output, "No alerts") {
				t.Errorf("a refused read printed an empty list:\n%s", output)
			}
		})
	}
}

func TestApplicationAlertsListFailedReadIsNeverAnEmptyList(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusInternalServerError, `Internal Server Error`},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID)

	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitError, executeError)
	}
	if executeError == nil || !strings.Contains(executeError.Error(), "reading the alerts of application") {
		t.Errorf("error = %v", executeError)
	}
	if strings.Contains(output, "No alerts") || strings.Contains(output, "NAME") {
		t.Errorf("a failed read printed a list:\n%s", output)
	}
}

func TestApplicationAlertsListFailedEnvironmentReadStopsBeforeTheList(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestAutoDeploy: {http.StatusInternalServerError, `Internal Server Error`},
	})

	_, executeError := runApplicationAlerts(t, stub, "", "list", testApplicationID, "--environment", "production")

	if executeError == nil || !strings.Contains(executeError.Error(), `reading the application's environments to find "production"`) {
		t.Errorf("error = %v", executeError)
	}
	if requests := stub.sent(http.MethodGet, alertsTestCollection); len(requests) != 0 {
		t.Errorf("list was read after the environment read failed: %+v", requests)
	}
}

func TestApplicationAlertsCreateSendsTheConditionAndPrintsTheNewAlert(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestAutoDeploy: {http.StatusOK, alertsTestAutoDeployPayload},
		"POST " + alertsTestCollection: {http.StatusCreated, `{
		  "alert_id": "8d1e4f2a-6b3c-4d5e-9f7a-1c2b3d4e5f6a", "rule_id": "11111111-2222-4333-8444-555555555555",
		  "name": "Errors", "own": true, "environment_id": "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d",
		  "says": "failed requests above 5% for 10 minutes",
		  "condition": {"condition_type": "app_metric", "metric": "failed_share_pct", "operator": "gt", "threshold": 5, "hold_minutes": 10},
		  "severity": "critical",
		  "send_to": [{"integration_id": "3f6c2a8e-1b4d-4c7a-9e2f-5d8b7a6c4e1f", "type": "slack", "label": "ops-slack"},
		              {"integration_id": "7b2d9e4c-3a1f-4b6e-8d5c-9e0f1a2b3c4d", "type": "pagerduty", "label": "pager"}],
		  "enabled": true, "last_firing": null, "evaluated_at": null, "evaluation": "not_evaluated", "unknown_reason": null}`},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "create", testApplicationID,
		"--name", "Errors", "--environment", "production", "--metric", "failed_share_pct",
		"--threshold", "5", "--hold-minutes", "10", "--send-to", alertsTestSlackID+", "+alertsTestPagerID,
		"--severity", "critical")

	if executeError != nil {
		t.Fatalf("alerts create error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, alertsTestCollection)
	if len(requests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(requests))
	}
	expectedBody := map[string]any{
		"name":           "Errors",
		"environment_id": alertsTestEnvironmentID,
		"condition": map[string]any{
			"condition_type": "app_metric", "metric": "failed_share_pct", "threshold": float64(5), "hold_minutes": float64(10),
		},
		"send_to":  []any{alertsTestSlackID, alertsTestPagerID},
		"severity": "critical",
	}
	if !reflect.DeepEqual(requests[0].body, expectedBody) {
		t.Errorf("create body = %v\nwant %v", requests[0].body, expectedBody)
	}
	for _, fragment := range []string{`Alert "Errors" created.`, "not evaluated yet", "ops-slack, pager", alertsTestErrorsID} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output lacks %q:\n%s", fragment, output)
		}
	}
}

func TestApplicationAlertsCreateLeavesSeverityToThePlatformWhenNotGiven(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"POST " + alertsTestCollection: {http.StatusCreated, alertsTestErrorsAlert},
	})

	_, executeError := runApplicationAlerts(t, stub, "", "create", testApplicationID,
		"--name", "Errors", "--environment", alertsTestEnvironmentID, "--metric", "restarts",
		"--threshold", "3", "--hold-minutes", "15", "--send-to", alertsTestSlackID)

	if executeError != nil {
		t.Fatalf("alerts create error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, alertsTestCollection)
	if len(requests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(requests))
	}
	if _, hasSeverity := requests[0].body["severity"]; hasSeverity {
		t.Errorf("severity was sent: %v", requests[0].body)
	}
	if len(stub.sent(http.MethodGet, alertsTestAutoDeploy)) != 0 {
		t.Errorf("an environment id was looked up")
	}
}

func TestApplicationAlertsCreateRefusalPrintsThePlatformsSentence(t *testing.T) {
	cases := map[string]struct {
		answer           alertsStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"no destination": {
			alertsStubAnswer{http.StatusBadRequest, `{"detail": "Connect a place to send alerts to first."}`},
			exitError, "Connect a place to send alerts to first.",
		},
		"field refused": {
			alertsStubAnswer{http.StatusUnprocessableEntity, `{"detail": "threshold must be at most 100 for failed_share_pct."}`},
			exitError, "threshold must be at most 100 for failed_share_pct.",
		},
		"permission denied": {
			alertsStubAnswer{http.StatusForbidden, alertsTestPermissionDenied},
			exitForbidden, `"applications.write"`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newAlertsStub(t, map[string]alertsStubAnswer{"POST " + alertsTestCollection: testCase.answer})

			_, executeError := runApplicationAlerts(t, stub, "", "create", testApplicationID,
				"--name", "Errors", "--environment", alertsTestEnvironmentID, "--metric", "failed_share_pct",
				"--threshold", "500", "--hold-minutes", "10", "--send-to", alertsTestSlackID)

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
		})
	}
}

func TestApplicationAlertsCreateFailedEnvironmentReadCreatesNothing(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestAutoDeploy: {http.StatusInternalServerError, `Internal Server Error`},
	})

	_, executeError := runApplicationAlerts(t, stub, "", "create", testApplicationID,
		"--name", "Errors", "--environment", "production", "--metric", "failed_share_pct",
		"--threshold", "5", "--hold-minutes", "10", "--send-to", alertsTestSlackID)

	if executeError == nil {
		t.Fatal("alerts create succeeded after the environment read failed")
	}
	if requests := stub.sent(http.MethodPost, alertsTestCollection); len(requests) != 0 {
		t.Errorf("an alert was created: %+v", requests)
	}
}

func TestApplicationAlertsCreateNeedsEveryRequiredFlag(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{})

	_, executeError := runApplicationAlerts(t, stub, "", "create", testApplicationID, "--name", "Errors")

	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if len(stub.requests) != 0 {
		t.Errorf("requests were sent: %+v", stub.requests)
	}
}

func TestApplicationAlertsUpdateKeepsWhatIsNotChanged(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
		"PUT " + alertsTestErrorsPath: {http.StatusOK, alertsTestErrorsAlert},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "update", testApplicationID, alertsTestErrorsID, "--threshold", "2")

	if executeError != nil {
		t.Fatalf("alerts update error = %v", executeError)
	}
	requests := stub.sent(http.MethodPut, alertsTestErrorsPath)
	if len(requests) != 1 {
		t.Fatalf("update requests = %d, want 1", len(requests))
	}
	expectedBody := map[string]any{
		"name":           "Errors",
		"environment_id": alertsTestEnvironmentID,
		"condition": map[string]any{
			"condition_type": "app_metric", "metric": "failed_share_pct", "operator": "gt",
			"threshold": float64(2), "hold_minutes": float64(10),
		},
		"send_to": []any{alertsTestSlackID},
	}
	if !reflect.DeepEqual(requests[0].body, expectedBody) {
		t.Errorf("update body = %v\nwant %v", requests[0].body, expectedBody)
	}
	if !strings.Contains(output, `Alert "Errors" changed.`) {
		t.Errorf("output:\n%s", output)
	}
}

func TestApplicationAlertsUpdateRefusesWhatTheApplicationDidNotMake(t *testing.T) {
	cases := map[string]struct {
		alertID          string
		expectedExitCode int
		expectedText     string
	}{
		"organisation rule": {alertsTestOrgRuleID, exitUsage, "is an organisation alert rule"},
		"not on the list":   {"00000000-0000-4000-8000-000000000000", exitNotFound, "has no alert 00000000-0000-4000-8000-000000000000"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newAlertsStub(t, map[string]alertsStubAnswer{
				"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
			})

			_, executeError := runApplicationAlerts(t, stub, "", "update", testApplicationID, testCase.alertID, "--threshold", "2")

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			for _, request := range stub.requests {
				if request.method == http.MethodPut {
					t.Errorf("an update was sent: %+v", request)
				}
			}
		})
	}
}

func TestApplicationAlertsUpdateRefusalPrintsThePlatformsSentence(t *testing.T) {
	sentence := "Another alert of this application is named Production · Errors."
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
		"PUT " + alertsTestErrorsPath: {http.StatusConflict, `{"detail": "` + sentence + `"}`},
	})

	_, executeError := runApplicationAlerts(t, stub, "", "update", testApplicationID, alertsTestErrorsID, "--name", "Slow responses")

	if executeError == nil || !strings.Contains(executeError.Error(), sentence) {
		t.Errorf("error = %v, want it to carry %q", executeError, sentence)
	}
}

func TestApplicationAlertsUpdateFailedReadChangesNothing(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusForbidden, `{"detail": "permission_denied", "permission": "alerts.read", "scope_type": "application"}`},
	})

	_, executeError := runApplicationAlerts(t, stub, "", "update", testApplicationID, alertsTestErrorsID, "--threshold", "2")

	if exitCodeFor(executeError) != exitForbidden {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitForbidden, executeError)
	}
	if requests := stub.sent(http.MethodPut, alertsTestErrorsPath); len(requests) != 0 {
		t.Errorf("an update was sent: %+v", requests)
	}
}

func TestApplicationAlertsUpdateNeedsSomethingToChange(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{})

	_, executeError := runApplicationAlerts(t, stub, "", "update", testApplicationID, alertsTestErrorsID)

	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if len(stub.requests) != 0 {
		t.Errorf("requests were sent: %+v", stub.requests)
	}
}

func TestApplicationAlertsDeleteNamesTheAlertBeforeItDeletesIt(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection:    {http.StatusOK, alertsTestListPayload},
		"DELETE " + alertsTestErrorsPath: {http.StatusOK, `{"success": true, "message": "Alert deleted"}`},
	})

	output, executeError := runApplicationAlerts(t, stub, "y\n", "delete", testApplicationID, alertsTestErrorsID)

	if executeError != nil {
		t.Fatalf("alerts delete error = %v", executeError)
	}
	for _, fragment := range []string{
		`Delete alert "Errors" (failed requests above 5% for 10 minutes)`,
		"It stops telling anyone and cannot be undone. [y/N]",
		`Alert "Errors" deleted.`,
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output lacks %q:\n%s", fragment, output)
		}
	}
	if requests := stub.sent(http.MethodDelete, alertsTestErrorsPath); len(requests) != 1 {
		t.Errorf("delete requests = %d, want 1", len(requests))
	}
}

func TestApplicationAlertsDeleteDeclinedDeletesNothing(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusOK, alertsTestListPayload},
	})

	_, executeError := runApplicationAlerts(t, stub, "n\n", "delete", testApplicationID, alertsTestErrorsID)

	if exitCodeFor(executeError) != exitCancelled {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitCancelled, executeError)
	}
	if requests := stub.sent(http.MethodDelete, alertsTestErrorsPath); len(requests) != 0 {
		t.Errorf("a delete was sent: %+v", requests)
	}
}

func TestApplicationAlertsDeleteWithYesSkipsTheQuestion(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"DELETE " + alertsTestErrorsPath: {http.StatusOK, `{"success": true, "message": "Alert deleted"}`},
	})

	output, executeError := runApplicationAlerts(t, stub, "", "delete", testApplicationID, alertsTestErrorsID, "--yes")

	if executeError != nil {
		t.Fatalf("alerts delete --yes error = %v", executeError)
	}
	if strings.Contains(output, "[y/N]") {
		t.Errorf("--yes still asked:\n%s", output)
	}
	if requests := stub.sent(http.MethodDelete, alertsTestErrorsPath); len(requests) != 1 {
		t.Errorf("delete requests = %d, want 1", len(requests))
	}
}

func TestApplicationAlertsDeleteRefusalPrintsThePlatformsSentence(t *testing.T) {
	cases := map[string]struct {
		answer           alertsStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"organisation alert": {
			alertsStubAnswer{http.StatusConflict, `{"detail": "This alert is not this application's: change it under Alerts."}`},
			exitError, "change it under Alerts.",
		},
		"permission denied": {
			alertsStubAnswer{http.StatusForbidden, alertsTestPermissionDenied},
			exitForbidden, `"applications.write"`,
		},
		"not deleted": {
			alertsStubAnswer{http.StatusOK, `{"success": false, "message": "The alert has an open incident."}`},
			exitError, "The alert has an open incident.",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newAlertsStub(t, map[string]alertsStubAnswer{"DELETE " + alertsTestErrorsPath: testCase.answer})

			output, executeError := runApplicationAlerts(t, stub, "", "delete", testApplicationID, alertsTestErrorsID, "--yes")

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if strings.Contains(output, "deleted.") {
				t.Errorf("a refused delete was reported done:\n%s", output)
			}
		})
	}
}

func TestApplicationAlertsDeleteFailedReadDeletesNothing(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + alertsTestCollection: {http.StatusInternalServerError, `Internal Server Error`},
	})

	_, executeError := runApplicationAlerts(t, stub, "y\n", "delete", testApplicationID, alertsTestErrorsID)

	if executeError == nil || !strings.Contains(executeError.Error(), "reading the alerts of application") {
		t.Errorf("error = %v", executeError)
	}
	if requests := stub.sent(http.MethodDelete, alertsTestErrorsPath); len(requests) != 0 {
		t.Errorf("a delete was sent: %+v", requests)
	}
}

func TestApplicationAlertsStateIsOnlyWhatThePlatformReported(t *testing.T) {
	acknowledgerName := "Sam"
	resolved := "2026-11-18T10:00:00Z"
	reason := "metric_store_unavailable"
	cases := map[string]struct {
		alert    client.ApplicationAlert
		expected string
	}{
		"ok":                  {client.ApplicationAlert{Enabled: true, Evaluation: "ok"}, "ok"},
		"not evaluated":       {client.ApplicationAlert{Enabled: true, Evaluation: "not_evaluated"}, "not evaluated yet"},
		"unknown, raw reason": {client.ApplicationAlert{Enabled: true, Evaluation: "unknown", UnknownReason: &reason}, "unknown (metric_store_unavailable)"},
		"unknown, no reason":  {client.ApplicationAlert{Enabled: true, Evaluation: "unknown"}, "unknown"},
		"resolved firing":     {client.ApplicationAlert{Enabled: true, Evaluation: "ok", LastFiring: &client.ApplicationAlertFiring{TriggeredAt: "2026-11-18T09:00:00Z", ResolvedAt: &resolved}}, "ok"},
		"open firing":         {client.ApplicationAlert{Enabled: true, Evaluation: "ok", LastFiring: &client.ApplicationAlertFiring{TriggeredAt: "2026-11-18T09:00:00Z"}}, "firing since 2026-11-18 09:00 UTC"},
		"acknowledged": {client.ApplicationAlert{Enabled: true, LastFiring: &client.ApplicationAlertFiring{
			TriggeredAt: "2026-11-18T09:00:00+01:00", AcknowledgedBy: &client.ApplicationAlertAcknowledger{Name: &acknowledgerName}}},
			"firing since 2026-11-18 08:00 UTC, acknowledged by Sam"},
		"off":               {client.ApplicationAlert{Enabled: false, Evaluation: "ok"}, "off"},
		"off, still firing": {client.ApplicationAlert{Enabled: false, LastFiring: &client.ApplicationAlertFiring{TriggeredAt: "2026-11-18T09:00:00Z"}}, "off, firing since 2026-11-18 09:00 UTC"},
		"new evaluation":    {client.ApplicationAlert{Enabled: true, Evaluation: "paused"}, "paused"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if state := applicationAlertState(testCase.alert); state != testCase.expected {
				t.Errorf("state = %q, want %q", state, testCase.expected)
			}
		})
	}
}
