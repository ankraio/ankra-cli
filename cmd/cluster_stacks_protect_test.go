package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func TestParseRetentionReadsEveryBucket(t *testing.T) {
	retention, parseError := parseRetention("hourly=6,daily=7,weekly=4,monthly=6,yearly=1,minimum_count=3,minimum-age=24h")

	if parseError != nil {
		t.Fatalf("parsing a full retention: %v", parseError)
	}
	if retention.Hourly != 6 || retention.Daily != 7 || retention.Weekly != 4 ||
		retention.Monthly != 6 || retention.Yearly != 1 {
		t.Errorf("buckets read wrong: %+v", retention)
	}
	if retention.MinimumCount != 3 {
		t.Errorf("minimum_count must be accepted underscored too, got %d", retention.MinimumCount)
	}
	if retention.MinimumAge != "24h" {
		t.Errorf("minimum-age must stay the platform's duration string, got %q", retention.MinimumAge)
	}
}

func TestParseRetentionIsOmittedWhenUnset(t *testing.T) {
	retention, parseError := parseRetention("")
	if parseError != nil {
		t.Fatalf("an empty retention is not an error: %v", parseError)
	}
	if retention != nil {
		t.Fatal("an unset --retention must be omitted so the platform keeps its own default")
	}
}

func TestParseRetentionRefusesNonsense(t *testing.T) {
	for name, raw := range map[string]string{
		"no equals sign":  "daily",
		"unknown bucket":  "fortnightly=2",
		"not a number":    "daily=lots",
		"negative bucket": "daily=-1",
	} {
		if _, parseError := parseRetention(raw); parseError == nil {
			t.Errorf("%s (%q) must be refused", name, raw)
		} else if exitCodeFor(parseError) != exitUsage {
			t.Errorf("%s (%q) is a bad invocation, got exit %d", name, raw, exitCodeFor(parseError))
		}
	}
}

func TestProtectSendsTheResolvedVaultScheduleAndRetention(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName: backupTestStack,
		Policy: client.BackupPolicy{
			Enabled: true, Vault: "production-backups", Schedule: "17 2 * * *",
			Retention: client.BackupRetention{Daily: 7, Weekly: 4},
			Selection: client.BackupPolicySelection{Databases: true},
		},
		BackupStack: client.BackupStackReady,
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--schedule", "daily", "--retention", "daily=7,weekly=4")

	if executeError != nil {
		t.Fatalf("protecting a stack: %v", executeError)
	}
	if mock.protected == nil {
		t.Fatal("the protect never reached the client")
	}
	if mock.protected.VaultID != backupTestVaultID {
		t.Errorf("the vault name must be resolved to its id, got %q", mock.protected.VaultID)
	}
	if mock.protected.Schedule != "daily" {
		t.Errorf("the schedule must be sent as typed, got %q", mock.protected.Schedule)
	}
	if mock.protected.Retention == nil || mock.protected.Retention.Daily != 7 {
		t.Errorf("the retention must reach the platform, got %+v", mock.protected.Retention)
	}
	for _, expected := range []string{"Protection:   on", "17 2 * * *", "7 daily, 4 weekly"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in the policy output, got:\n%s", expected, output)
		}
	}
}

// A caller must never be told "protected" while the Velero that would do the
// protecting is still being installed.
func TestProtectSaysWhenTheBackupStackIsStillInstalling(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName:   backupTestStack,
		Policy:      client.BackupPolicy{Enabled: true, Vault: "production-backups"},
		BackupStack: client.BackupStackInstalling,
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups")

	if executeError != nil {
		t.Fatalf("protecting a stack: %v", executeError)
	}
	if !strings.Contains(output, "still installing") {
		t.Fatalf("an installing data plane must be said out loud, got:\n%s", output)
	}
}

func TestProtectBackupNowNamesTheRun(t *testing.T) {
	runID := backupTestRunID
	restorePointID := backupTestRestorePointID
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName:      backupTestStack,
		Policy:         client.BackupPolicy{Enabled: true, Vault: "production-backups"},
		BackupStack:    client.BackupStackReady,
		RunID:          &runID,
		RestorePointID: &restorePointID,
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--backup-now")

	if executeError != nil {
		t.Fatalf("protecting a stack: %v", executeError)
	}
	if !mock.protected.BackupNow {
		t.Fatal("--backup-now must reach the platform")
	}
	if !strings.Contains(output, "ankra runs get "+runID) {
		t.Fatalf("the first capture's run must be named, got:\n%s", output)
	}
}

func TestProtectWaitFollowsTheFirstCapture(t *testing.T) {
	runID := backupTestRunID
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName:   backupTestStack,
		Policy:      client.BackupPolicy{Enabled: true, Vault: "production-backups"},
		BackupStack: client.BackupStackReady,
		RunID:       &runID,
	}
	mock.runSequence = []*client.Run{
		{ID: runID, Kind: client.RunKindBackup, Status: client.RunStatusRunning},
		{ID: runID, Kind: client.RunKindBackup, Status: client.RunStatusSucceeded},
	}
	withFastRunPolling(t)

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--backup-now", "--wait")

	if executeError != nil {
		t.Fatalf("waiting for the first capture: %v", executeError)
	}
	if mock.runReads < 2 {
		t.Fatalf("--wait must follow the run, read it %d times", mock.runReads)
	}
	if !strings.Contains(output, "is sealed") {
		t.Fatalf("a completed first capture must say so, got:\n%s", output)
	}
}

func TestProtectRefusesExcludingDatabasesWithoutConfirmation(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--exclude-databases")

	if executeError == nil {
		t.Fatal("excluding databases must need the acknowledgement")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage exit, got %d", exitCodeFor(executeError))
	}
	if mock.protected != nil {
		t.Fatal("nothing may reach the platform when the invocation is refused")
	}
}

func TestProtectSendsTheAcknowledgedExclusion(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName: backupTestStack,
		Policy:    client.BackupPolicy{Enabled: true, Vault: "production-backups"},
	}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--exclude-databases", "--confirm-exclude-databases")

	if executeError != nil {
		t.Fatalf("protecting a stack: %v", executeError)
	}
	selection := mock.protected.Selection
	if selection == nil || selection.Databases == nil || *selection.Databases {
		t.Fatalf("the exclusion must be explicit, got %+v", selection)
	}
	if !selection.ConfirmExcludeDatabases {
		t.Fatal("the acknowledgement must travel with the exclusion")
	}
}

// The platform answers every field-level reason at once so an operator can fix
// a schedule and a retention in one edit; printing only the first would throw
// that away.
func TestProtectPrintsEveryPolicyViolation(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protectError = &client.BackupPolicyValidationError{
		Detail: "The backup policy is not valid.",
		Violations: []client.BackupPolicyViolation{
			{Key: "backup.schedule", Message: "'evry day' is neither a five-field cron expression nor one of hourly, daily, weekly."},
			{Key: "backup.retention.daily", Message: "Keeping 0 dailies with no other bucket keeps nothing."},
		},
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--schedule", "evry day")

	if executeError == nil {
		t.Fatal("an invalid policy must fail the command")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("an invalid policy is a bad invocation, got exit %d", exitCodeFor(executeError))
	}
	for _, expected := range []string{"backup.schedule", "evry day", "backup.retention.daily"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q among the printed violations, got:\n%s", expected, output)
		}
	}
}

func TestProtectExplainsTheFeatureFlagRefusal(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protectError = client.NewUnexpectedResponseError(http.StatusForbidden, backupsNotEnabledDetail)

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups")

	if executeError == nil {
		t.Fatal("a 403 from the dark lane must fail the command")
	}
	if !strings.Contains(executeError.Error(), backupsNotEnabledDetail) {
		t.Fatalf("expected the flag-off detail, got: %v", executeError)
	}
}

func TestUnprotectNeedsTheTypedStackName(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "not-the-stack\n",
		[]*cobra.Command{clusterStacksUnprotectCmd},
		"cluster", "stacks", "unprotect", backupTestStack, "--cluster", "demo")

	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("a mistyped stack name must cancel, got %v", executeError)
	}
	if exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("expected exit %d, got %d", exitCancelled, exitCodeFor(executeError))
	}
	if mock.unprotectCalls != 0 {
		t.Fatal("nothing may reach the platform when the confirmation failed")
	}
}

func TestUnprotectSendsTheConfirmTokenAndKeepsTheRestorePoints(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName: backupTestStack,
		Policy:    client.BackupPolicy{Enabled: false},
		Warnings:  []string{"Protection was removed from this stack."},
	}

	output, executeError := runBackupCommand(t, mock, backupTestStack+"\n",
		[]*cobra.Command{clusterStacksUnprotectCmd},
		"cluster", "stacks", "unprotect", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("unprotecting a stack: %v", executeError)
	}
	if mock.unprotected == nil || mock.unprotected.Confirm != backupTestStack {
		t.Fatalf("the stack name must be sent as the confirm token, got %+v", mock.unprotected)
	}
	if !strings.Contains(output, "Protection:   off") {
		t.Errorf("the new policy state must be printed, got:\n%s", output)
	}
	if !strings.Contains(output, "restore points are still there") {
		t.Errorf("unprotect must say the restore points survive, got:\n%s", output)
	}
}

func TestUnprotectYesSkipsTheTypedConfirmation(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{StackName: backupTestStack}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksUnprotectCmd},
		"cluster", "stacks", "unprotect", backupTestStack, "--cluster", "demo", "--yes")

	if executeError != nil {
		t.Fatalf("unprotecting with --yes: %v", executeError)
	}
	if mock.unprotectCalls != 1 {
		t.Fatalf("expected one unprotect call, got %d", mock.unprotectCalls)
	}
}
