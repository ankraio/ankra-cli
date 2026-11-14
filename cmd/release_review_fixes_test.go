package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The fixes a pre-release review of v0.77.0 found in commands that had not
// shipped yet.

func TestCostRateCardSetRefusesARateLeftOut(t *testing.T) {
	organisationPromptsNotInteractive(t)
	server, calls := serveDocuments(t, map[string]documentRoute{
		"PUT /api/v1/org/cloud-cost/rate-cards": {http.StatusOK, `{"currency":"eur","vcpu_monthly_amount":"5","memory_gb_monthly_amount":"1.2","storage_gb_monthly_amount":"0"}`},
	})
	for _, rates := range [][]string{
		{"--vcpu", "5"},
		{"--vcpu", "5", "--memory-gb", "1.2"},
		{"--memory-gb", "1.2", "--storage-gb", "0.04"},
	} {
		arguments := append([]string{"set", "organisation", "--currency", "EUR", "--yes"}, rates...)
		if _, _, usageError := runDocumentCommand(t, server, newCostRateCardsCommand(), "", arguments...); exitCodeFor(usageError) != exitUsage ||
			!strings.Contains(usageError.Error(), "stored as 0") {
			t.Errorf("%v: error %v, want a usage error saying a left-out rate would be stored as 0", rates, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a card with a rate left out was still sent: %v", *calls)
	}
	stdout, _, runError := runDocumentCommand(t, server, newCostRateCardsCommand(), "",
		"set", "organisation", "--currency", "EUR", "--vcpu", "5", "--memory-gb", "1.2", "--storage-gb", "0", "--yes")
	if runError != nil || !strings.Contains(stdout, "Rate card stated") {
		t.Fatalf("set with all three rates, one of them 0: stdout %q, error %v", stdout, runError)
	}
	if body := decodeDocumentCallBody(t, (*calls)[0]); body["vcpu_monthly_amount"] != "5" || body["memory_gb_monthly_amount"] != "1.2" || body["storage_gb_monthly_amount"] != "0" {
		t.Fatalf("set sent %v", body)
	}
}

func TestCostWasteResolveBatchShowsEveryReasonWhenNoneStarted(t *testing.T) {
	organisationPromptsNotInteractive(t)
	const refusedBatch = `{"results":[{"finding_id":"w-1","status":"refused","operation_id":null,"detail":"Already resolved."},
		{"finding_id":"w-2","status":"refused","operation_id":null,"detail":"Needs delete_unattached_volume_acknowledged."}],
		"started":0,"refused":2,"failed":0,"partial":false}`
	server, _ := serveDocuments(t, map[string]documentRoute{
		"POST /api/v1/org/cloud-cost/waste/resolve-batch": {http.StatusConflict, refusedBatch},
	})
	stdout, _, runError := runDocumentCommand(t, server, newCostWasteCommand(), "", "resolve-batch", "w-1", "w-2", "--yes")
	if exitCodeFor(runError) != exitError {
		t.Fatalf("error %v, want exit %d when no cleanup started", runError, exitError)
	}
	for _, want := range []string{"Already resolved.", "Needs delete_unattached_volume_acknowledged.", "0 started, 2 refused, 0 failed."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	stdout, _, runError = runDocumentCommand(t, server, newCostWasteCommand(), "", "resolve-batch", "w-1", "w-2", "--yes", "-o", "json")
	var document struct {
		Results []map[string]any `json:"results"`
		Started int              `json:"started"`
	}
	// Decode the first value only: run on its own, without the root command's
	// SilenceUsage, cobra appends its usage text after an error.
	if exitCodeFor(runError) != exitError || json.NewDecoder(strings.NewReader(stdout)).Decode(&document) != nil || len(document.Results) != 2 || document.Started != 0 {
		t.Fatalf("-o json: stdout %q, error %v, want the outcome document and exit 1", stdout, runError)
	}

	// A 409 that is not an outcome document stays an ordinary error.
	plain, _ := serveDocuments(t, map[string]documentRoute{
		"POST /api/v1/org/cloud-cost/waste/resolve-batch": {http.StatusConflict, `{"detail":"Another cleanup is running."}`},
	})
	stdout, _, runError = runDocumentCommand(t, plain, newCostWasteCommand(), "", "resolve-batch", "w-1", "--yes")
	// No outcome table: stdout carries at most cobra's usage text (see above).
	if runError == nil || !strings.Contains(runError.Error(), "Another cleanup is running.") || strings.Contains(stdout, "started,") || strings.Contains(stdout, "Finding") {
		t.Fatalf("plain 409: stdout %q, error %v", stdout, runError)
	}
}

func TestTicketSyncUpdateToANewLinearTeamDropsTheOldTeamsLabel(t *testing.T) {
	const linearConnection = `{"id":"` + ticketOperationTestID + `","name":"linear","provider":"linear",
		"config":{"team_id":"team-1","team_key":"ENG","team_name":"Engineering"},
		"enabled":true,"credential_configured":true,"sync_kinds":[],"sync_min_priority":"low","sync_event_scope":"all","last_synced_at":null,"last_error":null}`
	connectionPath := ticketSyncTestPath + "/" + ticketOperationTestID
	server, calls := serveDocuments(t, map[string]documentRoute{
		"GET " + connectionPath: {http.StatusOK, linearConnection},
		"PUT " + connectionPath: {http.StatusOK, linearConnection},
	})
	if _, _, runError := runDocumentCommand(t, server, newTicketsSyncCommand(), "", "connections", "update", ticketOperationTestID, "--team-id", "team-2"); runError != nil {
		t.Fatalf("update: %v", runError)
	}
	config, _ := decodeDocumentCallBody(t, (*calls)[1])["config"].(map[string]any)
	if config["team_id"] != "team-2" || config["team_key"] != nil || config["team_name"] != nil {
		t.Fatalf("update to a new team sent config %v, want team-2 without the old team's key and name", config)
	}

	*calls = nil
	if _, _, runError := runDocumentCommand(t, server, newTicketsSyncCommand(), "", "connections", "update", ticketOperationTestID, "--team-id", "team-1"); runError != nil {
		t.Fatalf("update to the same team: %v", runError)
	}
	config, _ = decodeDocumentCallBody(t, (*calls)[1])["config"].(map[string]any)
	if config["team_key"] != "ENG" || config["team_name"] != "Engineering" {
		t.Fatalf("update to the same team sent config %v, want its label kept", config)
	}
}

func TestTicketSyncDestinationsRefusesJiraAndWebhookBeforeAskingForASecret(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{})
	for _, provider := range []string{"jira", "webhook"} {
		_, _, usageError := runDocumentCommand(t, server, newTicketsSyncCommand(), organisationParitySecret,
			"destinations", "--provider", provider, "--secret-stdin")
		if exitCodeFor(usageError) != exitUsage || !strings.Contains(usageError.Error(), "only linear has a destination list") {
			t.Errorf("%s: error %v, want the usage error naming linear", provider, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("refused lookups still sent %v", *calls)
	}
}

func TestAIMemoryProfileSetRefusesAnEmptyProfileFile(t *testing.T) {
	server, calls := serveDocuments(t, map[string]documentRoute{})
	for _, arguments := range [][]string{{"set", "--file", "-"}, {"set", "--learning", "off", "--file", "-"}} {
		_, _, usageError := runDocumentCommand(t, server, newAIMemoriesProfileCommand(), "{}", arguments...)
		if exitCodeFor(usageError) != exitUsage || !strings.Contains(usageError.Error(), "profile reset") {
			t.Errorf("%v with {}: error %v, want a usage error pointing at profile reset", arguments, usageError)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("an empty profile was still sent: %v", *calls)
	}
}
