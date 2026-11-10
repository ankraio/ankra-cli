package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationRollbackMock struct {
	baseMock
	targets        string
	targetClusters []string
	history        string
	startAnswer    string
	startError     error
	started        [][2]string
}

func (mock *applicationRollbackMock) GetApplicationRollbackTargets(_ context.Context, _ string, clusterID string) (json.RawMessage, error) {
	mock.targetClusters = append(mock.targetClusters, clusterID)
	return json.RawMessage(mock.targets), nil
}

func (mock *applicationRollbackMock) ListApplicationRollbacks(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(mock.history), nil
}

func (mock *applicationRollbackMock) StartApplicationRollback(_ context.Context, _ string, clusterID string, imageTag string) (json.RawMessage, error) {
	mock.started = append(mock.started, [2]string{clusterID, imageTag})
	return json.RawMessage(mock.startAnswer), mock.startError
}

const rollbackTargetsDocument = `{"cluster_id":"` + testClusterID + `","target_kind":"stack","unavailable_reason":null,
	"stack_name":"shop-web","namespace":"shop","image_repository":"registry.example/shop",
	"current_image_tag":"sha-3333333","targets":[
	 {"image_tag":"sha-3333333","pushed_at":"2026-11-09T10:00:00Z","is_current":true,"is_latest_build":true},
	 {"image_tag":"sha-2222222","pushed_at":"2026-11-08T10:00:00Z","is_current":false,"is_latest_build":false}],
	"targets_reason":null,"latest_build_image_tag":"sha-3333333","auto_deploy_enabled":true,
	"not_reverted":["A migrated database schema"],"last_rollback":null,"added_later":true}`

const rollbackRecordDocument = `{"id":"rollback-1","cluster_id":"` + testClusterID + `","target_kind":"stack",
	"stack_name":"shop-web","installation_id":null,"image_tag":"sha-2222222","previous_image_tag":"sha-3333333",
	"watermark_commit_sha":"3333333","status":"pending","message":null,"requested_by":"mark@example.com",
	"requested_at":"2026-11-10T09:00:00Z","not_reverted":["A migrated database schema"]}`

// runRollback runs the application command against mock; the answer is
// stdout and stderr together, the prompt included.
func runRollback(t *testing.T, mock *applicationRollbackMock, input string, arguments ...string) (string, error) {
	t.Helper()
	return runApplicationCommandWithInput(t, mock, input, arguments...)
}

func TestApplicationRollbackWithoutToListsTargetsAndWritesNothing(t *testing.T) {
	mock := &applicationRollbackMock{targets: rollbackTargetsDocument}
	output, executeError := runRollback(t, mock, "", "rollback", testApplicationID, "--cluster", testClusterID)
	if executeError != nil {
		t.Fatalf("rollback failed: %v", executeError)
	}
	if len(mock.started) != 0 {
		t.Fatalf("a read started a rollback: %+v", mock.started)
	}
	if len(mock.targetClusters) != 1 || mock.targetClusters[0] != testClusterID {
		t.Errorf("targets were read for %v, want %s", mock.targetClusters, testClusterID)
	}
	for _, want := range []string{"Running:        sha-3333333", "Latest build:   sha-3333333", "sha-2222222",
		"A migrated database schema", "--to <image-tag>"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
}

func TestApplicationRollbackStructuredReadPrintsThePlatformDocument(t *testing.T) {
	mock := &applicationRollbackMock{targets: rollbackTargetsDocument}
	output, executeError := runRollback(t, mock, "", "rollback", testApplicationID, "--cluster", testClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("rollback failed: %v", executeError)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(output), &document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, output)
	}
	if document["added_later"] != true {
		t.Errorf("a field this CLI does not declare was dropped: %s", output)
	}
}

func TestApplicationRollbackToConfirmsThenStartsAndPrintsTheRecord(t *testing.T) {
	mock := &applicationRollbackMock{targets: rollbackTargetsDocument, startAnswer: rollbackRecordDocument}
	output, executeError := runRollback(t, mock, "y\n",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222")
	if executeError != nil {
		t.Fatalf("rollback failed: %v", executeError)
	}
	if len(mock.started) != 1 || mock.started[0] != [2]string{testClusterID, "sha-2222222"} {
		t.Fatalf("started = %+v", mock.started)
	}
	if !strings.Contains(output, "back to sha-2222222? This deploys now.") {
		t.Errorf("the prompt must name the tag: %q", output)
	}
	for _, want := range []string{"back to sha-2222222 (was sha-3333333)", "Status: pending", "rollback-1",
		"holds until a newer build is observed"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
}

func TestApplicationRollbackDeclinedStartsNothing(t *testing.T) {
	mock := &applicationRollbackMock{targets: rollbackTargetsDocument, startAnswer: rollbackRecordDocument}
	_, executeError := runRollback(t, mock, "n\n",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222")
	if exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("error = %v, want the declined exit", executeError)
	}
	if len(mock.started) != 0 {
		t.Fatalf("a declined rollback was started: %+v", mock.started)
	}
}

func TestApplicationRollbackRefusesBeforeAnyRequest(t *testing.T) {
	for name, arguments := range map[string][]string{
		"no cluster":               {"rollback", testApplicationID},
		"blank tag":                {"rollback", testApplicationID, "--cluster", testClusterID, "--to", " "},
		"structured without --yes": {"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222", "-o", "json"},
		"history without cluster":  {"rollbacks", testApplicationID},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &applicationRollbackMock{targets: rollbackTargetsDocument, startAnswer: rollbackRecordDocument}
			_, executeError := runRollback(t, mock, "y\n", arguments...)
			if exitCodeFor(executeError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", executeError)
			}
			if len(mock.targetClusters) != 0 || len(mock.started) != 0 {
				t.Fatalf("a request was sent: targets %v, started %v", mock.targetClusters, mock.started)
			}
		})
	}
}

func TestApplicationRollbackToTheRunningTagStartsNothing(t *testing.T) {
	mock := &applicationRollbackMock{targets: rollbackTargetsDocument}
	output, executeError := runRollback(t, mock, "",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-3333333", "--yes")
	if executeError != nil {
		t.Fatalf("rollback failed: %v", executeError)
	}
	if len(mock.started) != 0 || !strings.Contains(output, "already runs sha-3333333") {
		t.Fatalf("started = %+v, output:\n%s", mock.started, output)
	}
}

func TestApplicationRollbackOfAnUnresolvedDeploymentIsBlocked(t *testing.T) {
	mock := &applicationRollbackMock{targets: `{"target_kind":"none",
		"unavailable_reason":"This application is not deployed to that cluster.","targets":[],"not_reverted":[]}`}
	_, executeError := runRollback(t, mock, "",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222", "--yes")
	if exitCodeFor(executeError) != exitBlocked || !strings.Contains(executeError.Error(), "not deployed to that cluster") {
		t.Fatalf("error = %v, want the blocked exit with the platform's reason", executeError)
	}
	if len(mock.started) != 0 {
		t.Fatalf("a blocked rollback was started: %+v", mock.started)
	}
}

func TestApplicationRollbackSurfacesARefusedStartAndAFailedRecord(t *testing.T) {
	denied := &applicationRollbackMock{targets: rollbackTargetsDocument,
		startError: &client.PermissionDeniedError{Permission: "applications.deploy"}}
	_, deniedError := runRollback(t, denied, "",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222", "--yes")
	if exitCodeFor(deniedError) != exitForbidden {
		t.Fatalf("error = %v, want the permission exit", deniedError)
	}

	failed := &applicationRollbackMock{targets: rollbackTargetsDocument,
		startAnswer: `{"id":"rollback-2","image_tag":"sha-2222222","status":"failed","message":"the stack write was refused"}`}
	output, failedError := runRollback(t, failed, "",
		"rollback", testApplicationID, "--cluster", testClusterID, "--to", "sha-2222222", "--yes", "-o", "json")
	if exitCodeFor(failedError) != exitError || !strings.Contains(failedError.Error(), "the stack write was refused") {
		t.Fatalf("error = %v, want exit 1 with the record's message", failedError)
	}
	if !strings.Contains(output, `"rollback-2"`) {
		t.Errorf("the record must still be printed: %s", output)
	}
}

func TestApplicationRollbacksListsHistoryAndSaysWhenThereIsNone(t *testing.T) {
	mock := &applicationRollbackMock{history: `{"rollbacks":[` + rollbackRecordDocument + `]}`}
	output, executeError := runRollback(t, mock, "", "rollbacks", testApplicationID, "--cluster", testClusterID)
	if executeError != nil {
		t.Fatalf("rollbacks failed: %v", executeError)
	}
	for _, want := range []string{"sha-2222222", "sha-3333333", "pending", "mark@example.com"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q:\n%s", want, output)
		}
	}
	empty := &applicationRollbackMock{history: `{"rollbacks":[]}`}
	output, executeError = runRollback(t, empty, "", "rollbacks", testApplicationID, "--cluster", testClusterID)
	if executeError != nil || !strings.Contains(output, "No rollbacks recorded") {
		t.Fatalf("error = %v, output:\n%s", executeError, output)
	}
}

func TestApplicationRollbackWithNoTargetsDoesNotInventAReason(t *testing.T) {
	silent := &applicationRollbackMock{targets: `{"target_kind":"stack","current_image_tag":"sha-3333333","targets":[],"targets_reason":null}`}
	output, executeError := runRollback(t, silent, "", "rollback", testApplicationID, "--cluster", testClusterID)
	if executeError != nil || !strings.Contains(output, "No rollback targets: the platform listed none and gave no reason") {
		t.Fatalf("error = %v, output:\n%s", executeError, output)
	}
	if strings.Contains(output, "--to <image-tag>") {
		t.Errorf("no target means no rollback hint:\n%s", output)
	}
	stated := &applicationRollbackMock{targets: `{"target_kind":"stack","targets":[],"targets_reason":"The registry could not be read."}`}
	output, executeError = runRollback(t, stated, "", "rollback", testApplicationID, "--cluster", testClusterID)
	if executeError != nil || !strings.Contains(output, "No rollback targets: The registry could not be read.") {
		t.Fatalf("error = %v, output:\n%s", executeError, output)
	}
}
