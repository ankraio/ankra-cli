package cmd

import (
	"net/http"
	"strings"
	"testing"
)

const (
	autonomyTestPath = "/api/v1/org/applications/" + testApplicationID
	autonomyTestRead = `{"application_id":"` + testApplicationID + `","storage":"stored","read_state":"ok","read_reason":"",
		"environments":[{"environment_id":"env-prod","name":"production","environment_kind":"production","level":"together",
		"areas":[{"area":"keep_it_up","level":"together"},{"area":"ship_changes","level":"you_run_it"}]}],
		"layers":{"application":{"level":"together","areas":{"ship_changes":"you_run_it"},"limits":{"monthly_cost_increase_cents":5000},
		"widened_by_admin":false},"environments":{}},
		"pause":{"read_state":"ok","paused":true,"paused_at":null,"paused_by":null,"reason":"incident 42"}}`
	autonomyTestLayer = `{"application_id":"` + testApplicationID + `","scope":"application","environment_id":null,
		"level":"together","areas":{},"limits":{},"widened_by_admin":true,"updated_at":"2026-11-10T00:00:00Z"}`
)

func TestApplicationAutonomyGetShowsLevelsAndThePause(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{"GET " + autonomyTestPath + "/autonomy": {http.StatusOK, autonomyTestRead}})
	stdout, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "get", testApplicationID)
	if runError != nil {
		t.Fatalf("autonomy get: %v", runError)
	}
	for _, want := range []string{"production", "together", "ship_changes=you_run_it", "PAUSED for this application (incident 42)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestApplicationAutonomySetChangesOnlyWhatIsNamedAndSendsWhatItRead(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + autonomyTestPath + "/autonomy": {http.StatusOK, autonomyTestRead},
		"PUT " + autonomyTestPath + "/autonomy": {http.StatusOK, autonomyTestLayer},
	})
	stdout, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "set", testApplicationID,
		"--area", "keep_costs_down=ankra_runs_it", "--may-change-data=false", "--yes")
	if runError != nil {
		t.Fatalf("autonomy set: %v", runError)
	}
	requireDocumentCalls(t, calls, "GET "+autonomyTestPath+"/autonomy", "PUT "+autonomyTestPath+"/autonomy")
	body := decodeDocumentCallBody(t, (*calls)[1])
	areas, _ := body["areas"].(map[string]any)
	limits, _ := body["limits"].(map[string]any)
	expected, _ := body["expected"].(map[string]any)
	expectedAreas, _ := expected["areas"].(map[string]any)
	if body["level"] != "together" || areas["ship_changes"] != "you_run_it" || areas["keep_costs_down"] != "ankra_runs_it" ||
		limits["monthly_cost_increase_cents"] != float64(5000) || limits["may_change_data"] != false {
		t.Fatalf("layer sent = %v", body)
	}
	if expected["level"] != "together" || len(expectedAreas) != 1 || expected["widened_by_admin"] != false {
		t.Fatalf("expected layer sent = %v, want the stored layer untouched", expected)
	}
	if !strings.Contains(stdout, "widened by an admin") {
		t.Errorf("stdout = %q, want the widened note", stdout)
	}
}

func TestApplicationAutonomySetWritesAnEnvironmentLayerOverNoStoredOne(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + autonomyTestPath + "/autonomy":                       {http.StatusOK, autonomyTestRead},
		"PUT " + autonomyTestPath + "/environments/env-prod/autonomy": {http.StatusOK, autonomyTestLayer},
	})
	_, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "y\n", "autonomy", "set", testApplicationID,
		"--environment", "production", "--level", "you_run_it")
	if runError != nil {
		t.Fatalf("autonomy set --environment: %v", runError)
	}
	body := decodeDocumentCallBody(t, (*calls)[1])
	if expected, isSent := body["expected"]; !isSent || expected != nil || body["level"] != "you_run_it" {
		t.Fatalf("layer sent = %v, want level you_run_it and expected null", body)
	}
}

func TestApplicationAutonomySetRefusals(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + autonomyTestPath + "/autonomy": {http.StatusOK, autonomyTestRead},
		"PUT " + autonomyTestPath + "/autonomy": {http.StatusConflict, `{"detail":"The layer changed since it was read."}`},
	})
	for name, testCase := range map[string]struct {
		stdin     string
		arguments []string
		exitCode  int
	}{
		"nothing named":       {"", []string{"--yes"}, exitUsage},
		"unknown environment": {"", []string{"--environment", "staging", "--level", "together"}, exitUsage},
		"malformed area":      {"", []string{"--area", "ship_changes"}, exitUsage},
		"declined":            {"n\n", []string{"--level", "together"}, exitCancelled},
		"changed underneath":  {"", []string{"--level", "together", "--yes"}, exitError},
	} {
		_, _, runError := runDocumentCommand(t, server, newApplicationCommand(), testCase.stdin,
			append([]string{"autonomy", "set", testApplicationID}, testCase.arguments...)...)
		if exitCodeFor(runError) != testCase.exitCode {
			t.Errorf("%s: error = %v (exit %d), want exit %d", name, runError, exitCodeFor(runError), testCase.exitCode)
		}
	}
	puts := 0
	for _, call := range *calls {
		if call.method == http.MethodPut {
			puts++
		}
	}
	if puts != 1 {
		t.Fatalf("PUT requests = %d, want only the confirmed one", puts)
	}
}

func TestApplicationAutonomySetRefusesWhenTheStoredLayersWereNotRead(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + autonomyTestPath + "/autonomy": {http.StatusOK, `{"read_state":"unknown","environments":[],"layers":null,"pause":{"paused":null}}`},
	})
	_, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "set", testApplicationID, "--level", "together", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "nothing was written") {
		t.Fatalf("error = %v, want the refusal", runError)
	}
	requireDocumentCalls(t, calls, "GET "+autonomyTestPath+"/autonomy")
}

func TestApplicationAutonomyPauseResumeAndStatus(t *testing.T) {
	const paused = `{"application_id":"a","paused":true,"paused_at":"2026-11-10T00:00:00Z","paused_by":"mark","reason":"incident 42"}`
	const running = `{"application_id":"a","paused":false,"paused_at":null,"paused_by":null,"reason":null}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"PUT " + autonomyTestPath + "/autonomy-pause":    {http.StatusOK, paused},
		"DELETE " + autonomyTestPath + "/autonomy-pause": {http.StatusOK, running},
		"GET " + autonomyTestPath + "/autonomy-pause":    {http.StatusOK, paused},
	})
	stdout, _, pauseError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "pause", testApplicationID, "--reason", "incident 42")
	if pauseError != nil || !strings.Contains(stdout, "PAUSED") || !strings.Contains(stdout, "incident 42") {
		t.Fatalf("pause: stdout = %q, error = %v", stdout, pauseError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["reason"] != "incident 42" {
		t.Fatalf("pause body = %v", body)
	}
	stdout, _, resumeError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "resume", testApplicationID)
	if resumeError != nil || !strings.Contains(stdout, "not paused") {
		t.Fatalf("resume: stdout = %q, error = %v", stdout, resumeError)
	}
	stdout, _, statusError := runDocumentCommand(t, server, newApplicationCommand(), "", "autonomy", "pause-status", testApplicationID, "-o", "json")
	if statusError != nil || !strings.Contains(stdout, `"paused_by": "mark"`) {
		t.Fatalf("pause-status: stdout = %q, error = %v", stdout, statusError)
	}
	requireDocumentCalls(t, calls, "PUT "+autonomyTestPath+"/autonomy-pause", "DELETE "+autonomyTestPath+"/autonomy-pause",
		"GET "+autonomyTestPath+"/autonomy-pause")
}

func TestApplicationSwitchesSendTheAnswerAsked(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + autonomyTestPath + "/auto-fix-builds":                {http.StatusOK, `{"enabled":true}`},
		"PUT " + autonomyTestPath + "/auto-fix-builds":                {http.StatusOK, `{"enabled":false}`},
		"PUT " + autonomyTestPath + "/auto-merge-setup-pull-requests": {http.StatusOK, `{"enabled":null,"organisation_enabled":true,"effective":true}`},
		"PUT " + autonomyTestPath + "/backups/database-backup":        {http.StatusOK, `{"application_id":"a","database_backup":false,"deployments":[]}`},
	})
	for _, testCase := range []struct {
		arguments []string
		sent      string
		printed   string
	}{
		{[]string{"auto-fix-builds", "get", testApplicationID}, "", "Automatic build fixing: on"},
		{[]string{"auto-fix-builds", "set", testApplicationID, "--enabled=false"}, `{"enabled":false}`, "Automatic build fixing: off"},
		{[]string{"setup-auto-merge", "set", testApplicationID, "--follow-organisation"}, `{"enabled":null}`, "follows the organisation"},
		{[]string{"database-backup", "set", testApplicationID, "--enabled=false"}, `{"enabled":false}`, "Database backup default: off"},
	} {
		*calls = nil
		stdout, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", testCase.arguments...)
		if runError != nil || !strings.Contains(stdout, testCase.printed) {
			t.Errorf("%v: stdout = %q, error = %v", testCase.arguments, stdout, runError)
			continue
		}
		if (*calls)[0].body != testCase.sent {
			t.Errorf("%v: body = %q, want %q", testCase.arguments, (*calls)[0].body, testCase.sent)
		}
	}
	*calls = nil
	for _, arguments := range [][]string{
		{"auto-fix-builds", "set", testApplicationID},
		{"setup-auto-merge", "set", testApplicationID},
		{"setup-auto-merge", "set", testApplicationID, "--enabled", "--follow-organisation"},
		{"database-backup", "set", testApplicationID},
	} {
		if _, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", arguments...); exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: error = %v, want exit %d", arguments, runError, exitUsage)
		}
	}
	requireDocumentCalls(t, calls)
}

func TestApplicationSwitchSurfacesAForbiddenWrite(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"PUT " + autonomyTestPath + "/auto-merge-setup-pull-requests": {http.StatusForbidden, `{"detail":"Only organisation administrators can change this."}`},
	})
	_, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "setup-auto-merge", "set", testApplicationID, "--enabled")
	if exitCodeFor(runError) != exitForbidden {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitForbidden)
	}
}

func TestApplicationCostEstimateSendsTheChangeAndPrintsTheDelta(t *testing.T) {
	const clusterID = "1fa7c8a4-5e42-4fd5-abff-15500899eda4"
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + autonomyTestPath + "/cost/estimate": {http.StatusOK, `{"change":"replicas","currency":"eur","priced":true,
			"unpriced_reason":null,"monthly_delta_cents":1250,"current_monthly_cents":2500,"confidence":"high",
			"coverage_incomplete":true,"basis":"Two more pods at the deployment's requests."}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "cost-estimate", testApplicationID,
		"--change", "replicas", "--cluster", clusterID, "--namespace", "shop", "--replicas", "4", "--parameter", "instances=2")
	if runError != nil {
		t.Fatalf("cost-estimate: %v", runError)
	}
	for _, want := range []string{"+12.50 EUR", "25.00 EUR a month", "This is a floor", "Two more pods"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	parameters, _ := body["parameters"].(map[string]any)
	if body["change"] != "replicas" || body["cluster_id"] != clusterID || body["namespace"] != "shop" ||
		body["replicas"] != float64(4) || parameters["instances"] != float64(2) {
		t.Fatalf("estimate body = %v", body)
	}
}

func TestApplicationCostEstimateSaysWhyAChangeIsNotPriced(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + autonomyTestPath + "/cost/estimate": {http.StatusOK, `{"change":"park","currency":"eur","priced":false,
			"unpriced_reason":"no metering pass has run on this cluster yet","monthly_delta_cents":null}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newApplicationCommand(), "", "cost-estimate", testApplicationID, "--change", "park")
	if runError != nil || !strings.Contains(stdout, "could not be priced: no metering pass") {
		t.Fatalf("stdout = %q, error = %v", stdout, runError)
	}
	*calls = nil
	for _, arguments := range [][]string{
		{"cost-estimate", testApplicationID},
		{"cost-estimate", testApplicationID, "--change", "database", "--parameter", "instances=two"},
	} {
		if _, _, usageError := runDocumentCommand(t, server, newApplicationCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage {
			t.Errorf("%v: error = %v, want exit %d", arguments, usageError, exitUsage)
		}
	}
	requireDocumentCalls(t, calls)
	if _, _, missingError := runDocumentCommand(t, server, newApplicationCommand(), "", "cost-estimate",
		"9d2f6c2e-0000-4000-8000-000000000009", "--change", "park"); exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("unknown application error = %v, want exit %d", missingError, exitNotFound)
	}
}
