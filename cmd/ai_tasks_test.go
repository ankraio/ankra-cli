package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

const (
	aiTaskTestID   = "0b8f6c2e-0000-4000-8000-000000000001"
	aiTaskTestPath = "/api/v1/org/ai-tasks/" + aiTaskTestID
	aiTaskTestBody = `{"id":"` + aiTaskTestID + `","name":"Nightly cost review","goal_prompt":"Review spend",
		"trigger":{"kind":"cron","cron_expression":"0 6 * * *"},"tool_profile":"read_only","autonomy_level":"propose",
		"enabled":true,"cluster_id":null,"next_run_at":"2026-11-11T06:00:00Z","field_from_a_newer_platform":7}`
	aiTaskTestList = `{"tasks":[` + aiTaskTestBody + `]}`
)

func TestAITasksListRendersTheTasksAndKeepsThePlatformDocument(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-tasks": {http.StatusOK, aiTaskTestList},
	})
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "list")
	if runError != nil {
		t.Fatalf("ai tasks list: %v", runError)
	}
	for _, want := range []string{aiTaskTestID, "Nightly cost review", "cron (0 6 * * *)", "propose"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table missing %q:\n%s", want, stdout)
		}
	}
	structured, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "list", "-o", "json")
	if runError != nil || !strings.Contains(structured, `"field_from_a_newer_platform": 7`) {
		t.Fatalf("-o json must print the platform's own document, got %q (%v)", structured, runError)
	}
	requireDocumentCalls(t, calls, "GET /api/v1/org/ai-tasks", "GET /api/v1/org/ai-tasks")
}

func TestAITasksListSaysSoWhenThereAreNone(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{"GET /api/v1/org/ai-tasks": {http.StatusOK, `{"tasks":[]}`}})
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "list")
	if runError != nil || !strings.Contains(stdout, "No agent tasks") {
		t.Fatalf("stdout = %q, error = %v", stdout, runError)
	}
}

func TestAITasksCreateSendsTheDefinitionTheFlagsName(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST /api/v1/org/ai-tasks": {http.StatusCreated, aiTaskTestBody},
	})
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "create",
		"--name", "Nightly cost review", "--goal", "Review spend", "--trigger", "cron", "--cron", "0 6 * * *",
		"--autonomy-level", "propose")
	if runError != nil {
		t.Fatalf("ai tasks create: %v", runError)
	}
	requireDocumentCalls(t, calls, "POST /api/v1/org/ai-tasks")
	body := decodeDocumentCallBody(t, (*calls)[0])
	trigger, _ := body["trigger"].(map[string]any)
	if body["name"] != "Nightly cost review" || body["goal_prompt"] != "Review spend" || body["autonomy_level"] != "propose" ||
		trigger["kind"] != "cron" || trigger["cron_expression"] != "0 6 * * *" {
		t.Fatalf("create body = %v", body)
	}
	if _, isSent := body["enabled"]; isSent {
		t.Errorf("a flag nobody passed was sent: %v", body)
	}
	if !strings.Contains(stdout, "Created agent task.") || !strings.Contains(stdout, aiTaskTestID) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestAITasksCreateRefusesAnIncompleteDefinitionBeforeSending(t *testing.T) {
	server, calls := serveDocuments(t, nil)
	for _, arguments := range [][]string{
		{"create", "--name", "x", "--trigger", "manual"},
		{"create", "--name", "x", "--goal", "y", "--cron", "0 6 * * *"},
		{"create", "--name", "  ", "--goal", "y", "--trigger", "manual"},
		{"update", aiTaskTestID},
	} {
		_, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", arguments...)
		if exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: error = %v (exit %d), want exit %d", arguments, runError, exitCodeFor(runError), exitUsage)
		}
	}
	requireDocumentCalls(t, calls)
}

func TestAITasksUpdateResolvesTheNameAndSendsOnlyWhatChanged(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-tasks": {http.StatusOK, aiTaskTestList},
		"PUT " + aiTaskTestPath:    {http.StatusOK, aiTaskTestBody},
	})
	_, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "update", "Nightly cost review", "--enabled=false")
	if runError != nil {
		t.Fatalf("ai tasks update: %v", runError)
	}
	requireDocumentCalls(t, calls, "GET /api/v1/org/ai-tasks", "PUT "+aiTaskTestPath)
	if body := decodeDocumentCallBody(t, (*calls)[1]); len(body) != 1 || body["enabled"] != false {
		t.Fatalf("update body = %v, want only enabled=false", body)
	}
}

func TestAITasksNameThatMatchesNothingIsNotFound(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{"GET /api/v1/org/ai-tasks": {http.StatusOK, aiTaskTestList}})
	_, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "get", "no such agent")
	if exitCodeFor(runError) != exitNotFound {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitNotFound)
	}
}

func TestAITasksDeleteAsksFirst(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{"DELETE " + aiTaskTestPath: {http.StatusNoContent, ""}})
	_, _, declinedError := runDocumentCommand(t, server, newAITasksCommand(), "n\n", "delete", aiTaskTestID)
	if !errors.Is(declinedError, errCancelled) {
		t.Fatalf("declined delete error = %v, want errCancelled", declinedError)
	}
	requireDocumentCalls(t, calls)
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "delete", aiTaskTestID, "--yes")
	if runError != nil || !strings.Contains(stdout, "Deleted agent task "+aiTaskTestID) {
		t.Fatalf("stdout = %q, error = %v", stdout, runError)
	}
	requireDocumentCalls(t, calls, "DELETE "+aiTaskTestPath)
}

func TestAITasksControlVerbsPostTheirAction(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + aiTaskTestPath + "/run":    {http.StatusAccepted, `{"detail":"Task queued for immediate dispatch."}`},
		"POST " + aiTaskTestPath + "/pause":  {http.StatusOK, `{"detail":"Task paused."}`},
		"POST " + aiTaskTestPath + "/resume": {http.StatusOK, `{"detail":"Task resumed."}`},
		"POST " + aiTaskTestPath + "/stop":   {http.StatusOK, `{"detail":"Task stopped.","cancelled_run_id":"run-7"}`},
	})
	for action, want := range map[string]string{
		"run": "Task queued for immediate dispatch.", "pause": "Task paused.", "resume": "Task resumed.",
	} {
		stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", action, aiTaskTestID)
		if runError != nil || !strings.Contains(stdout, want) {
			t.Errorf("%s: stdout = %q, error = %v", action, stdout, runError)
		}
	}
	*calls = nil
	if _, _, declinedError := runDocumentCommand(t, server, newAITasksCommand(), "n\n", "stop", aiTaskTestID); !errors.Is(declinedError, errCancelled) {
		t.Fatalf("declined stop error = %v, want errCancelled", declinedError)
	}
	requireDocumentCalls(t, calls)
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "y\n", "stop", aiTaskTestID)
	if runError != nil || !strings.Contains(stdout, "Cancelled run: run-7") {
		t.Fatalf("stop: stdout = %q, error = %v", stdout, runError)
	}
	requireDocumentCalls(t, calls, "POST "+aiTaskTestPath+"/stop")
}

func TestAITasksReapplyTemplateSurfacesTheRefusalAndForcesOnlyWhenAsked(t *testing.T) {
	refusing, calls := serveDocuments(t, map[string]documentRoute{
		"POST " + aiTaskTestPath + "/reapply-template": {http.StatusConflict,
			`{"detail":"Re-apply refused: this agent was customised.","reason":"customised"}`},
	})
	_, _, runError := runDocumentCommand(t, refusing, newAITasksCommand(), "", "reapply-template", aiTaskTestID)
	if runError == nil || !strings.Contains(runError.Error(), "this agent was customised") || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v (exit %d), want the platform's refusal with exit %d", runError, exitCodeFor(runError), exitError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["force"] != false {
		t.Fatalf("reapply body = %v, want force=false", body)
	}
	accepting, forcedCalls := serveDocuments(t, map[string]documentRoute{
		"POST " + aiTaskTestPath + "/reapply-template": {http.StatusOK, `{"detail":"Role template re-applied."}`},
	})
	if _, _, forcedError := runDocumentCommand(t, accepting, newAITasksCommand(), "", "reapply-template", aiTaskTestID, "--force", "--yes"); forcedError != nil {
		t.Fatalf("forced reapply: %v", forcedError)
	}
	if body := decodeDocumentCallBody(t, (*forcedCalls)[0]); body["force"] != true {
		t.Fatalf("forced reapply body = %v, want force=true", body)
	}
}

func TestAITasksRunsListsWithTheLimit(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + aiTaskTestPath + "/runs": {http.StatusOK,
			`{"runs":[{"id":"run-1","status":"completed","started_at":"2026-11-10T06:00:00Z","finished_at":null,"outcome_summary":"Nothing to do"}]}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "runs", aiTaskTestID, "--limit", "5")
	if runError != nil || !strings.Contains(stdout, "run-1") || !strings.Contains(stdout, "Nothing to do") {
		t.Fatalf("stdout = %q, error = %v", stdout, runError)
	}
	if (*calls)[0].query != "limit=5" {
		t.Fatalf("query = %q, want limit=5", (*calls)[0].query)
	}
	if _, _, limitError := runDocumentCommand(t, server, newAITasksCommand(), "", "runs", aiTaskTestID, "--limit", "0"); exitCodeFor(limitError) != exitUsage {
		t.Fatalf("--limit 0 error = %v, want exit %d", limitError, exitUsage)
	}
}

func TestAITaskTemplatesListAndHire(t *testing.T) {
	const clusterID = "1fa7c8a4-5e42-4fd5-abff-15500899eda4"
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-tasks/templates": {http.StatusOK,
			`{"templates":[{"id":"reviewer","name":"Reviewer","category":"board","autonomy_level":"propose","hired":false,"tagline":"Reviews tickets"}]}`},
		"POST /api/v1/org/ai-tasks/templates/reviewer/hire": {http.StatusCreated, aiTaskTestBody},
	})
	stdout, _, listError := runDocumentCommand(t, server, newAITasksCommand(), "", "templates", "list")
	if listError != nil || !strings.Contains(stdout, "reviewer") || !strings.Contains(stdout, "Reviews tickets") {
		t.Fatalf("templates list: stdout = %q, error = %v", stdout, listError)
	}
	stdout, _, hireError := runDocumentCommand(t, server, newAITasksCommand(), "", "templates", "hire", "reviewer", "--cluster", clusterID)
	if hireError != nil || !strings.Contains(stdout, "Hired.") {
		t.Fatalf("templates hire: stdout = %q, error = %v", stdout, hireError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[1]); body["cluster_id"] != clusterID {
		t.Fatalf("hire body = %v, want cluster_id %s", body, clusterID)
	}
}

func TestAITasksMapARefusalToItsExitCode(t *testing.T) {
	for status, testCase := range map[int]struct {
		body     string
		exitCode int
	}{
		http.StatusUnauthorized: {`{"detail":"Not authenticated"}`, exitAuth},
		http.StatusForbidden:    {`{"detail":"permission_denied","permission":"ai.manage"}`, exitForbidden},
		http.StatusNotFound:     {`{"detail":"Task not found."}`, exitNotFound},
	} {
		server, _ := serveDocuments(t, map[string]documentRoute{"POST " + aiTaskTestPath + "/run": {status, testCase.body}})
		_, _, runError := runDocumentCommand(t, server, newAITasksCommand(), "", "run", aiTaskTestID)
		if exitCodeFor(runError) != testCase.exitCode {
			t.Errorf("status %d: error = %v (exit %d), want exit %d", status, runError, exitCodeFor(runError), testCase.exitCode)
		}
	}
}

func TestAIConditionsListAndGet(t *testing.T) {
	const condition = `{"id":"c-1","subject_key":"deploy/shop","condition_kind":"crash_loop","family":"workload",
		"occurrence_count":9,"episode_count":3,"no_action_streak":3,"is_chronic":true,"last_outcome":"no_action",
		"current_ticket":null,"first_observed_at":"2026-10-01T00:00:00Z","last_observed_at":"2026-11-09T00:00:00Z","last_opened_at":null}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-conditions": {http.StatusOK, `{"conditions":[` + condition + `],"counts":{"total":4,"chronic":1,"open":2}}`},
		"GET /api/v1/org/ai-conditions/c-1": {http.StatusOK, `{"condition":` + condition + `,"tickets":[],"entries":[
			{"at":"2026-11-09T00:00:00Z","kind":"ticket_closed","ticket_id":"t","ticket_number":41,"title":"x","status":"closed",
			 "detail":"Closed without action","reference":null,"author":"ankra"}]}`},
	})
	stdout, _, listError := runDocumentCommand(t, server, newAIConditionsCommand(), "", "list", "--chronic", "--limit", "20")
	if listError != nil || !strings.Contains(stdout, "crash_loop") || !strings.Contains(stdout, "4 conditions: 2 open, 1 chronic.") {
		t.Fatalf("conditions list: stdout = %q, error = %v", stdout, listError)
	}
	if query := (*calls)[0].query; !strings.Contains(query, "chronic=true") || !strings.Contains(query, "limit=20") {
		t.Fatalf("query = %q, want chronic=true and limit=20", query)
	}
	stdout, _, getError := runDocumentCommand(t, server, newAIConditionsCommand(), "", "get", "c-1")
	if getError != nil || !strings.Contains(stdout, "9 times over 3 episodes") || !strings.Contains(stdout, "#41") {
		t.Fatalf("conditions get: stdout = %q, error = %v", stdout, getError)
	}
	_, _, missingError := runDocumentCommand(t, server, newAIConditionsCommand(), "", "get", "c-2")
	if exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("unknown condition error = %v, want exit %d", missingError, exitNotFound)
	}
}

func TestAIPlaybooksAdoptAndRetireAskFirst(t *testing.T) {
	const playbookID = "5f0c1d6e-0000-4000-8000-000000000001"
	playbook := func(status string) string {
		return `{"id":"` + playbookID + `","name":"Restart crash loop","version":2,"condition_kind":"crash_loop","status":"` +
			status + `","risk_class":"low","clean_runs":4,"platform_template":false}`
	}
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-playbooks":                            {http.StatusOK, `{"items":[` + playbook("candidate") + `]}`},
		"POST /api/v1/org/ai-playbooks/" + playbookID + "/adopt":  {http.StatusOK, playbook("approved")},
		"POST /api/v1/org/ai-playbooks/" + playbookID + "/retire": {http.StatusOK, playbook("retired")},
	})
	stdout, _, listError := runDocumentCommand(t, server, newAIPlaybooksCommand(), "", "list")
	if listError != nil || !strings.Contains(stdout, "Restart crash loop") || !strings.Contains(stdout, "candidate") {
		t.Fatalf("playbooks list: stdout = %q, error = %v", stdout, listError)
	}
	*calls = nil
	for _, verb := range []string{"adopt", "retire"} {
		if _, _, declinedError := runDocumentCommand(t, server, newAIPlaybooksCommand(), "\n", verb, playbookID); !errors.Is(declinedError, errCancelled) {
			t.Fatalf("declined %s error = %v, want errCancelled", verb, declinedError)
		}
	}
	requireDocumentCalls(t, calls)
	stdout, _, adoptError := runDocumentCommand(t, server, newAIPlaybooksCommand(), "", "adopt", playbookID, "--yes")
	if adoptError != nil || !strings.Contains(stdout, "now approved") {
		t.Fatalf("adopt: stdout = %q, error = %v", stdout, adoptError)
	}
	stdout, _, retireError := runDocumentCommand(t, server, newAIPlaybooksCommand(), "", "retire", playbookID, "--yes", "--reason", "superseded")
	if retireError != nil || !strings.Contains(stdout, "now retired") {
		t.Fatalf("retire: stdout = %q, error = %v", stdout, retireError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[1]); body["reason"] != "superseded" {
		t.Fatalf("retire body = %v, want the reason", body)
	}
}

func TestAIPlaybooksAdoptSurfacesAConflict(t *testing.T) {
	server, _ := serveDocuments(t, map[string]documentRoute{
		"POST /api/v1/org/ai-playbooks/p-1/adopt": {http.StatusConflict, `{"detail":"A retired playbook cannot be adopted."}`},
	})
	_, _, adoptError := runDocumentCommand(t, server, newAIPlaybooksCommand(), "", "adopt", "p-1", "--yes")
	if adoptError == nil || !strings.Contains(adoptError.Error(), "retired playbook cannot be adopted") {
		t.Fatalf("error = %v, want the platform's refusal", adoptError)
	}
}

func TestAIHandoffsListAndAcknowledge(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/ai-engineering-handoffs": {http.StatusOK, `{"handoffs":[{"id":"h-1","alert_name":"EtcdQuorumLost",
			"severity":"critical","status":"open","occurrence_count":3,"last_seen_at":"2026-11-09T00:00:00Z","tracker_reference":""}],
			"counts":{"open":1,"filed":0,"resolved":0}}`},
		"POST /api/v1/org/ai-engineering-handoffs/h-1/acknowledge": {http.StatusOK, `{"id":"h-1","status":"filed","tracker_reference":"ENG-482"}`},
	})
	stdout, _, listError := runDocumentCommand(t, server, newAIHandoffsCommand(), "", "list", "--status", "open,filed")
	if listError != nil || !strings.Contains(stdout, "EtcdQuorumLost") {
		t.Fatalf("handoffs list: stdout = %q, error = %v", stdout, listError)
	}
	if query := (*calls)[0].query; query != "status=open%2Cfiled" {
		t.Fatalf("query = %q, want status=open,filed", query)
	}
	if _, _, missingError := runDocumentCommand(t, server, newAIHandoffsCommand(), "", "acknowledge", "h-1"); exitCodeFor(missingError) != exitUsage {
		t.Fatalf("acknowledge without a reference error = %v, want exit %d", missingError, exitUsage)
	}
	stdout, _, acknowledgeError := runDocumentCommand(t, server, newAIHandoffsCommand(), "", "acknowledge", "h-1", "--tracker-reference", "ENG-482")
	if acknowledgeError != nil || !strings.Contains(stdout, "Acknowledged handoff h-1 as ENG-482.") {
		t.Fatalf("acknowledge: stdout = %q, error = %v", stdout, acknowledgeError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[1]); body["tracker_reference"] != "ENG-482" {
		t.Fatalf("acknowledge body = %v", body)
	}
}
