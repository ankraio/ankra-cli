package cmd

import (
	"net/http"
	"strings"
	"testing"
)

const operationTestID = "5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c"

func TestSecurityRemediationCommandsConfirmWritesAndRenderRuns(t *testing.T) {
	organisationPromptsNotInteractive(t)
	const run = `{"id":"run-1","kind":"image_rebuild","status":"verifying","title":"Bump nginx","application_name":"shop",
		"repository":"acme/shop","branch":"ankra/security/4f1c/nginx","pull_request_url":"https://github.com/acme/shop/pull/7",
		"started_by":"person","attempts":1,"max_attempts":3,"finding_ids":["f-1"],"created_at":"2026-11-10T00:00:00Z"}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET /api/v1/org/security/remediations": {http.StatusOK, `{"result":[` + run + `]}`},
		"POST /api/v1/org/security/findings/" + operationTestID + "/remediations": {http.StatusOK, `{"finding_id":"` + operationTestID + `",
			"runs":[{"created":true,"run":` + run + `}],"refusals":[{"image_repository":"acme/legacy","reason":"no Dockerfile"}]}`},
	})
	stdout, _, runError := runDocumentCommand(t, server, newSecurityRemediationsCommand(), "", "list", "--finding", "f-1", "--all-projects")
	if runError != nil || !strings.Contains(stdout, "run-1") || !strings.Contains(stdout, "https://github.com/acme/shop/pull/7") {
		t.Fatalf("list: stdout %q, error %v", stdout, runError)
	}
	if query := (*calls)[0].query; query != "finding_id=f-1&project_id=all" {
		t.Errorf("list query = %q", query)
	}
	stdout, _, runError = runDocumentCommand(t, server, newSecurityRemediationsCommand(), "", "list", "-o", "json")
	if runError != nil || !strings.Contains(stdout, `"max_attempts": 3`) && !strings.Contains(stdout, `"max_attempts":3`) {
		t.Errorf("list -o json: stdout %q, error %v", stdout, runError)
	}

	*calls = nil
	if _, _, refusedError := runDocumentCommand(t, server, newSecurityRemediationsCommand(), "", "start", operationTestID); exitCodeFor(refusedError) != exitUsage || len(*calls) != 0 {
		t.Fatalf("start without --yes: error %v, calls %v; want a usage error and nothing sent", refusedError, *calls)
	}
	stdout, _, runError = runDocumentCommand(t, server, newSecurityRemediationsCommand(), "", "start", operationTestID, "--yes")
	if runError != nil || !strings.Contains(stdout, "Started run run-1 (verifying) for shop") || !strings.Contains(stdout, "Refused: acme/legacy: no Dockerfile") {
		t.Fatalf("start: stdout %q, error %v", stdout, runError)
	}
	_, _, missingError := runDocumentCommand(t, server, newSecurityRemediationsCommand(), "", "get", "unknown-run")
	if exitCodeFor(missingError) != exitNotFound {
		t.Errorf("get of an unknown run: error %v (exit %d), want not found", missingError, exitCodeFor(missingError))
	}
}

func TestSecurityCodeDispositionCreateValidatesBeforeSending(t *testing.T) {
	const disposition = `{"id":"d-1","tool":"semgrep","rule_id":"rule","path":"main.go","repository":"acme/shop",
		"disposition":"accepted_risk","status":"active","finding_severity":"HIGH","reason":"compensated","expires_at":"2026-12-31T23:59:59Z"}`
	server, calls := serveDocuments(t, map[string]documentRoute{"POST /api/v1/org/security/code-dispositions": {http.StatusCreated, disposition}})
	base := []string{"create", "--repository-id", operationTestID, "--tool", "semgrep", "--rule", "rule", "--path", "main.go"}
	for _, arguments := range [][]string{
		append(append([]string{}, base...), "--disposition", "accepted_risk", "--reason", "compensated"),
		append(append([]string{}, base...), "--disposition", "false_positive"),
		append(append([]string{}, base...), "--disposition", "ignored", "--reason", "x"),
		{"create", "--tool", "semgrep", "--rule", "rule", "--path", "main.go", "--disposition", "false_positive", "--reason", "x"},
	} {
		if _, _, usageError := runDocumentCommand(t, server, newSecurityCodeDispositionsCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage {
			t.Errorf("%v: error = %v, want a usage error", arguments, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("refused creates still sent %v", *calls)
	}
	stdout, _, runError := runDocumentCommand(t, server, newSecurityCodeDispositionsCommand(), "",
		append(base, "--disposition", "accepted_risk", "--reason", "compensated", "--expires-at", "2026-12-31", "--yes")...)
	if runError != nil || !strings.Contains(stdout, "accepted_risk (active)") {
		t.Fatalf("create: stdout %q, error %v", stdout, runError)
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	if body["repository_id"] != operationTestID || body["expires_at"] != "2026-12-31T23:59:59Z" || body["rule_id"] != "rule" || body["reason"] != "compensated" {
		t.Fatalf("create sent %v", body)
	}
}

func TestCostRateCardWasteAndEstimateCommands(t *testing.T) {
	organisationPromptsNotInteractive(t)
	const estimatePath = "/api/v1/org/clusters/" + operationTestID + "/cost/estimate"
	const estimate = `{"currency":"eur","pricing":{"mode":"catalog","source":"price book","reason":""},
		"baseline":{"total_node_count":3,"monthly_cents":9000,"coverage_incomplete":false},
		"estimate":{"total_node_count":5,"monthly_cents":15000,"coverage_incomplete":true},
		"metered_monthly_cents":9500,"predicted_monthly_cents":15500,"delta_monthly_cents":6000}`
	server, calls := serveDocuments(t, map[string]documentRoute{
		"PUT /api/v1/org/cloud-cost/rate-cards":                 {http.StatusOK, `{"currency":"eur","vcpu_monthly_amount":"4.50","memory_gb_monthly_amount":"0","storage_gb_monthly_amount":"0"}`},
		"DELETE /api/v1/org/cloud-cost/rate-cards/organisation": {http.StatusNoContent, ""},
		"POST /api/v1/org/cloud-cost/waste/resolve-batch":       {http.StatusOK, `{"results":[{"finding_id":"w-1","status":"started","operation_id":"op-1","detail":null},{"finding_id":"w-2","status":"refused","operation_id":null,"detail":"no consent"}],"started":1,"refused":1,"failed":0,"partial":true}`},
		"GET " + estimatePath:                                   {http.StatusOK, estimate},
		"POST " + estimatePath:                                  {http.StatusOK, estimate},
	})
	if _, _, refusedError := runDocumentCommand(t, server, newCostRateCardsCommand(), "", "set", "organisation", "--currency", "EUR", "--vcpu", "4.50", "--memory-gb", "1.20", "--storage-gb", "0.04"); exitCodeFor(refusedError) != exitUsage || len(*calls) != 0 {
		t.Fatalf("rate-cards set without --yes: error %v, calls %v", refusedError, *calls)
	}
	stdout, _, runError := runDocumentCommand(t, server, newCostRateCardsCommand(), "", "set", "organisation", "--currency", "EUR", "--vcpu", "4.50", "--memory-gb", "1.20", "--storage-gb", "0.04", "--yes")
	if runError != nil || !strings.Contains(stdout, "4.50 per vCPU") {
		t.Fatalf("rate-cards set: stdout %q, error %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["credential_id"] != "organisation" || body["currency"] != "EUR" || body["vcpu_monthly_amount"] != "4.50" {
		t.Fatalf("rate-cards set sent %v", body)
	}
	if _, _, refusedError := runDocumentCommand(t, server, newCostRateCardsCommand(), "", "delete", "organisation"); exitCodeFor(refusedError) != exitUsage || len(*calls) != 1 {
		t.Fatalf("rate-cards delete without --yes: error %v, calls %v", refusedError, *calls)
	}
	stdout, _, runError = runDocumentCommand(t, server, newCostRateCardsCommand(), "", "delete", "organisation", "--yes", "-o", "json")
	if runError != nil || !strings.Contains(stdout, `"deleted"`) {
		t.Fatalf("rate-cards delete: stdout %q, error %v", stdout, runError)
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newCostWasteCommand(), "", "resolve-batch", "w-1", "w-2",
		"--consent", "delete_unattached_volume_acknowledged", "--min-unattached-days", "45", "--yes")
	if runError != nil || !strings.Contains(stdout, "1 started, 1 refused, 0 failed.") || !strings.Contains(stdout, "no consent") {
		t.Fatalf("resolve-batch: stdout %q, error %v", stdout, runError)
	}
	body := decodeDocumentCallBody(t, (*calls)[0])
	if findingIDs, _ := body["finding_ids"].([]any); len(findingIDs) != 2 || body["consent"] != "delete_unattached_volume_acknowledged" || body["min_unattached_days"] != float64(45) {
		t.Fatalf("resolve-batch sent %v", body)
	}

	*calls = nil
	stdout, _, runError = runDocumentCommand(t, server, newCostEstimateCommand(), `{"node_groups":[{"count":5,"vcpus":4,"memory_gb":16,"disk_gb":80}]}`,
		operationTestID, "--draft-file", "-")
	if runError != nil || !strings.Contains(stdout, "for 5 nodes") || !strings.Contains(stdout, "the figures are a floor") {
		t.Fatalf("estimate draft: stdout %q, error %v", stdout, runError)
	}
	if _, _, runError = runDocumentCommand(t, server, newCostEstimateCommand(), "", operationTestID); runError != nil {
		t.Fatalf("estimate read: %v", runError)
	}
	requireDocumentCalls(t, calls, "POST "+estimatePath, "GET "+estimatePath)
	if _, _, draftError := runDocumentCommand(t, server, newCostEstimateCommand(), `{"network":{}}`, operationTestID, "--draft-file", "-"); exitCodeFor(draftError) != exitUsage {
		t.Errorf("a draft without node_groups: error %v, want a usage error", draftError)
	}
}
