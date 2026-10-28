package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func sampleDataRun(status string) client.Run {
	startedAt := "2026-09-15T10:04:11Z"
	restorePointID := backupTestRestorePointID
	return client.Run{
		ID: backupTestRunID, Kind: client.RunKindBackup, Status: status,
		StartedAt: &startedAt, CreatedAt: startedAt, UpdatedAt: startedAt,
		DataRun: &client.DataRun{
			ID: backupTestRunID, Kind: client.RunKindBackup, Mode: client.RestoreModeInPlace,
			Phase: "backup", Plan: []string{"verify_vault_access", "backup", "upload_verify"},
			BackupVaultID: backupTestVaultID, RestorePointID: &restorePointID,
			SourceStackName: backupTestStack, AttemptBudget: 3,
		},
	}
}

func TestRunsListRendersTheRunTable(t *testing.T) {
	mock := newBackupLaneMock()
	mock.runs = &client.RunListResult{Runs: []client.Run{sampleDataRun(client.RunStatusSucceeded)}}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsListCmd}, "runs", "list", "--kind", "backup")

	if executeError != nil {
		t.Fatalf("listing runs: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{"ID", "KIND", "STATUS", "NAME", backupTestRunID, "backup", "succeeded", backupTestStack} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the run listing, got:\n%s", expected, stripped)
		}
	}
}

func TestRunsListRefusesAnUnknownKindBeforeTheRoundTrip(t *testing.T) {
	mock := newBackupLaneMock()
	mock.runs = &client.RunListResult{Runs: []client.Run{sampleDataRun(client.RunStatusSucceeded)}}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsListCmd}, "runs", "list", "--kind", "backups")

	if executeError == nil {
		t.Fatal("a mistyped kind must be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("a mistyped kind is a bad invocation, got exit %d", exitCodeFor(executeError))
	}
	if !strings.Contains(executeError.Error(), client.RunKindBackup) {
		t.Fatalf("the refusal must name the kinds that exist, got: %v", executeError)
	}
}

func TestRunsListEmptySaysSo(t *testing.T) {
	mock := newBackupLaneMock()

	output, executeError := runBackupCommand(t, mock, "", []*cobra.Command{runsListCmd}, "runs", "list")

	if executeError != nil {
		t.Fatalf("listing runs: %v", executeError)
	}
	if !strings.Contains(output, "No runs found.") {
		t.Fatalf("an empty listing must say so, got:\n%s", output)
	}
}

func TestRunsListStructuredOutputStaysParseable(t *testing.T) {
	mock := newBackupLaneMock()
	mock.runs = &client.RunListResult{Runs: []client.Run{sampleDataRun(client.RunStatusRunning)}}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsListCmd}, "runs", "list", "-o", "json")

	if executeError != nil {
		t.Fatalf("listing runs: %v", executeError)
	}
	var decoded client.RunListResult
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json must stay parseable, got %v for:\n%s", decodeError, output)
	}
	if len(decoded.Runs) != 1 || decoded.Runs[0].DataRun == nil {
		t.Fatalf("the data-movement payload must survive into the structured output: %+v", decoded.Runs)
	}
}

// Every attempt of every step is what explains an hour nobody can otherwise
// account for, so the detail prints them all.
func TestRunsGetPrintsEveryAttemptOfEveryStep(t *testing.T) {
	excerpt := "the volume was still attached"
	run := sampleDataRun(client.RunStatusSucceeded)
	run.DataRun.Steps = []client.DataRunStep{
		{ID: "s1", StepKey: "verify_vault_access", Position: 1, Attempt: 1, Status: "succeeded", JobName: "verify_backup_vault_access"},
		{ID: "s2", StepKey: "backup", Position: 2, Attempt: 1, Status: "failed", JobName: "backup_stack_data", ErrorExcerpt: &excerpt},
		{ID: "s3", StepKey: "backup", Position: 2, Attempt: 2, Status: "succeeded", JobName: "backup_stack_data"},
	}
	run.DataRun.AssetPlan = client.DataRunAssetPlan{
		Engine: "velero",
		Assets: []client.DataRunAssetSelection{{ID: "volume-shop-data", Kind: "volume", Engine: "velero", Name: "data"}},
		NotCarried: []client.RestorePointNotCarried{{
			Kind: "kafka_topic_data", Name: "shop/events", Reason: "Kafka topic data is not carried.",
		}},
	}
	mock := newBackupLaneMock()
	mock.runSequence = []*client.Run{&run}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsGetCmd}, "runs", "get", backupTestRunID)

	if executeError != nil {
		t.Fatalf("getting a run: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"Data movement:", "verify_vault_access -> backup -> upload_verify",
		"backup_stack_data", excerpt, "Not carried:", "kafka_topic_data",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the run detail, got:\n%s", expected, stripped)
		}
	}
	if strings.Count(stripped, "backup_stack_data") < 2 {
		t.Errorf("both attempts of the failed step must be rendered, got:\n%s", stripped)
	}
}

func TestRunsGetExplainsTheFeatureFlagRefusal(t *testing.T) {
	mock := newBackupLaneMock()
	mock.runError = client.NewUnexpectedResponseError(http.StatusForbidden, backupsNotEnabledDetail)

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsGetCmd}, "runs", "get", backupTestRunID)

	if executeError == nil {
		t.Fatal("a 403 from the dark lane must fail the command")
	}
	if !strings.Contains(executeError.Error(), backupsNotEnabledDetail) {
		t.Fatalf("expected the flag-off detail, got: %v", executeError)
	}
}

func TestRunsCancelDeclinedCancelsNothing(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "n\n",
		[]*cobra.Command{runsCancelCmd}, "runs", "cancel", backupTestRunID)

	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", executeError)
	}
	if mock.cancelled != "" {
		t.Fatal("a declined prompt must cancel nothing")
	}
}

func TestRunsCancelReportsTheNewStatus(t *testing.T) {
	mock := newBackupLaneMock()

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsCancelCmd}, "runs", "cancel", backupTestRunID, "--yes")

	if executeError != nil {
		t.Fatalf("cancelling a run: %v", executeError)
	}
	if mock.cancelled != backupTestRunID {
		t.Fatalf("the cancel must reach the platform, got %q", mock.cancelled)
	}
	if !strings.Contains(output, client.RunStatusCancelled) {
		t.Fatalf("the new status must be reported, got:\n%s", output)
	}
}

// A retry opens a NEW run; naming the old one would send anything watching it
// to poll a row that will never move again.
func TestRunsRetryNamesTheNewRun(t *testing.T) {
	mock := newBackupLaneMock()
	const failedRunID = "9f8e7d6c-5b4a-4938-8271-605f4e3d2c1b"

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsRetryCmd}, "runs", "retry", failedRunID)

	if executeError != nil {
		t.Fatalf("retrying a run: %v", executeError)
	}
	if mock.retried != failedRunID {
		t.Fatalf("the retry must name the failed run, got %q", mock.retried)
	}
	if !strings.Contains(output, "ankra runs get "+backupTestRunID) {
		t.Fatalf("the new run must be the one to watch, got:\n%s", output)
	}
}

func TestRunsListFiltersByClusterAndStack(t *testing.T) {
	mock := &runsFilterMock{backupLaneMock: *newBackupLaneMock()}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{runsListCmd},
		"runs", "list", "--kind", "restore", "--cluster", "demo", "--stack", backupTestStack,
		"--status", "failed", "--limit", "10")

	if executeError != nil {
		t.Fatalf("listing runs: %v", executeError)
	}
	if mock.options.ClusterID != testClusterID {
		t.Errorf("the cluster name must be resolved to its id, got %q", mock.options.ClusterID)
	}
	if mock.options.StackName != backupTestStack || mock.options.Kind != client.RunKindRestore ||
		mock.options.Status != "failed" || mock.options.Limit != 10 {
		t.Errorf("every filter must reach the query, got %+v", mock.options)
	}
}

// runsFilterMock records the listing options the command built.
type runsFilterMock struct {
	backupLaneMock
	options client.ListRunsOptions
}

func (mock *runsFilterMock) ListRuns(options client.ListRunsOptions) (*client.RunListResult, error) {
	mock.options = options
	return &client.RunListResult{}, nil
}

func TestIsTerminalRunStatusCoversTheThreeEndings(t *testing.T) {
	for _, status := range []string{client.RunStatusSucceeded, client.RunStatusFailed, client.RunStatusCancelled} {
		if !client.IsTerminalRunStatus(status) {
			t.Errorf("%s ends a run's life", status)
		}
	}
	for _, status := range []string{
		client.RunStatusPending, client.RunStatusRunning,
		client.RunStatusBlocked, client.RunStatusAwaitingApproval,
	} {
		if client.IsTerminalRunStatus(status) {
			t.Errorf("%s does not end a run's life, so a follow must keep polling", status)
		}
	}
}
