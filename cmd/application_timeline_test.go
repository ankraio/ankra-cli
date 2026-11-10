package cmd

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

type applicationTimelineMock struct {
	baseMock
	timeline      string
	timelineQuery url.Values
	launchPlan    string
	launchQuery   url.Values
}

func (mock *applicationTimelineMock) GetApplicationTimeline(_ context.Context, _ string, query url.Values) (json.RawMessage, error) {
	mock.timelineQuery = query
	return json.RawMessage(mock.timeline), nil
}

func (mock *applicationTimelineMock) GetApplicationLaunchPlan(_ context.Context, _ string, query url.Values) (json.RawMessage, error) {
	mock.launchQuery = query
	return json.RawMessage(mock.launchPlan), nil
}

func runApplicationRead(t *testing.T, mock *applicationTimelineMock, input string, arguments ...string) (string, string, error) {
	t.Helper()
	return runFreshCommand(t, newApplicationCommand(), mock, input, arguments...)
}

func TestApplicationTimelinePrintsEventsAndNamesWhatItCouldNotRead(t *testing.T) {
	mock := &applicationTimelineMock{timeline: `{"events":[{"id":"e-1","kind":"rollback","status":"succeeded",
		"occurred_at":"2026-11-10T09:00:00Z","cluster_name":"production","namespace":"shop","image_tag":"sha-2222222",
		"actor_name":"Mark","message":"Rolled back"}],"capped":true,"unavailable_sources":["demos"],"next_before":"cursor-2"}`}
	output, notes, executeError := runApplicationRead(t, mock, "", "timeline", testApplicationID, "--limit", "20", "--before", "cursor-1")
	if executeError != nil {
		t.Fatalf("timeline failed: %v", executeError)
	}
	if mock.timelineQuery.Get("limit") != "20" || mock.timelineQuery.Get("before") != "cursor-1" {
		t.Errorf("query = %v", mock.timelineQuery)
	}
	for _, want := range []string{"rollback", "production/shop", "sha-2222222", "Mark"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
	if !strings.Contains(notes, "capped this read") {
		t.Errorf("stderr must say the read was capped: %q", notes)
	}
	if !strings.Contains(notes, "demos") || !strings.Contains(notes, "--before cursor-2") {
		t.Errorf("stderr must name the unread source and the next page: %q", notes)
	}

	_, _, limitError := runApplicationRead(t, mock, "", "timeline", testApplicationID, "--limit", "101")
	if exitCodeFor(limitError) != exitUsage {
		t.Fatalf("error = %v, want a usage error for an out-of-range limit", limitError)
	}
	namespaceOnly := &applicationTimelineMock{timeline: `{"events":[{"kind":"deploy","status":"succeeded",
		"occurred_at":"2026-11-10T09:00:00Z","namespace":"shop"}],"capped":false,"unavailable_sources":[]}`}
	output, notes, executeError = runApplicationRead(t, namespaceOnly, "", "timeline", testApplicationID)
	if executeError != nil || !strings.Contains(output, "shop") || strings.Contains(notes, "capped") {
		t.Fatalf("error = %v, stderr = %q, output:\n%s", executeError, notes, output)
	}
	empty := &applicationTimelineMock{timeline: `{"events":[],"capped":false,"unavailable_sources":[]}`}
	output, _, executeError = runApplicationRead(t, empty, "", "timeline", testApplicationID)
	if executeError != nil || !strings.Contains(output, "No events") || len(empty.timelineQuery) != 0 {
		t.Fatalf("error = %v, query = %v, output:\n%s", executeError, empty.timelineQuery, output)
	}
}

func TestApplicationLaunchPlanSendsItsSelectionAndPrintsThePlan(t *testing.T) {
	mock := &applicationTimelineMock{launchPlan: `{"application_id":"` + testApplicationID + `","status":"ready","plan":{"services":[]}}`}
	output, _, executeError := runApplicationRead(t, mock, "", "launch-plan", testApplicationID, "--commit", "abc1234",
		"--cluster", testClusterID, "--backup-vault", "vault-1",
		"--data-migration-choice", "postgres:ankra-open-source", "--data-migration-choice", "redis:needs-portability-pr")
	if executeError != nil {
		t.Fatalf("launch-plan failed: %v", executeError)
	}
	want := url.Values{"commit_sha": {"abc1234"}, "cluster_id": {testClusterID}, "backup_vault_id": {"vault-1"},
		"data_migration_choices": {"postgres:ankra-open-source,redis:needs-portability-pr"}}
	if mock.launchQuery.Encode() != want.Encode() {
		t.Errorf("query = %q, want %q", mock.launchQuery.Encode(), want.Encode())
	}
	if !strings.Contains(output, `"status": "ready"`) {
		t.Errorf("the plan must be printed: %s", output)
	}

	_, _, choiceError := runApplicationRead(t, mock, "", "launch-plan", testApplicationID, "--data-migration-choice", "postgres")
	if exitCodeFor(choiceError) != exitUsage {
		t.Fatalf("error = %v, want a usage error for a choice without a colon", choiceError)
	}
}
