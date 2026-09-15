package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	applicationBackupsAppID     = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	applicationBackupsClusterID = "4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a"
)

// applicationBackupsMock answers the application backup surface and records
// what the commands sent, so the tests assert the wire-facing behaviour - and
// in particular that a stack name is never sent.
type applicationBackupsMock struct {
	baseMock
	backups     *client.ApplicationBackups
	backupsRead int
	readError   error

	protected      *client.ProtectApplicationDeploymentRequest
	protectedOn    string
	protection     *client.StackProtection
	protectError   error
	created        *client.CreateRestorePointRequest
	createdOn      string
	createResult   *client.CreateRestorePointResult
	createError    error
	sealed         *client.RestorePoint
	sealedReadStub bool
}

func (mock *applicationBackupsMock) GetApplicationBackups(string) (*client.ApplicationBackups, error) {
	mock.backupsRead++
	if mock.readError != nil {
		return nil, mock.readError
	}
	return mock.backups, nil
}

func (mock *applicationBackupsMock) ProtectApplicationDeployment(_ string, deploymentID string,
	request client.ProtectApplicationDeploymentRequest) (*client.StackProtection, error) {
	mock.protected = &request
	mock.protectedOn = deploymentID
	if mock.protectError != nil {
		return nil, mock.protectError
	}
	return mock.protection, nil
}

func (mock *applicationBackupsMock) CreateApplicationRestorePoint(_ string, deploymentID string,
	request client.CreateRestorePointRequest) (*client.CreateRestorePointResult, error) {
	mock.created = &request
	mock.createdOn = deploymentID
	if mock.createError != nil {
		return nil, mock.createError
	}
	return mock.createResult, nil
}

func (mock *applicationBackupsMock) GetStackRestorePoint(string, string, string) (*client.RestorePoint, error) {
	mock.sealedReadStub = true
	return mock.sealed, nil
}

func (mock *applicationBackupsMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	return &client.BackupVaultListResult{Items: []client.BackupVault{
		{ID: backupTestVaultID, Name: "production-backups"},
	}}, nil
}

func applicationBackupText(value string) *string { return &value }

// applicationBackupCommands is what every test below hands runBackupCommand
// as its flag resets.
//
// The commands are built inside registerApplicationResourceCommands rather
// than held in package-level vars, so there is nothing to name directly - and
// without the reset, `--cluster production` set by one test is still set for
// the next one. That is not a cosmetic leak: the test asserting that a
// capture with no --cluster is refused passed the refusal and ran the capture
// instead, on a mock with no result, and panicked.
func applicationBackupCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	commands := []*cobra.Command{}
	for _, family := range rootCmd.Commands() {
		if family.Name() != "application" {
			continue
		}
		for _, subcommand := range family.Commands() {
			switch subcommand.Name() {
			case "backups", "protect", "backup":
				commands = append(commands, subcommand)
			}
		}
	}
	if len(commands) != 3 {
		t.Fatalf("found %d application backup commands, want 3", len(commands))
	}
	return commands
}

func sampleApplicationBackups() *client.ApplicationBackups {
	lastRestorePointAt := "2026-09-14T02:17:00Z"
	nextScheduledAt := "2026-09-16T02:17:00Z"
	return &client.ApplicationBackups{
		ApplicationID:   applicationBackupsAppID,
		ApplicationName: "shop",
		Database: client.ApplicationDatabase{
			Status: "recorded", Engine: "postgresql", Operator: "cloudnative-pg",
		},
		DatabaseBackup: true,
		Deployments: []client.ApplicationBackupDeployment{{
			DeploymentID:     applicationBackupsClusterID,
			ClusterID:        applicationBackupsClusterID,
			ClusterName:      "production",
			StackName:        applicationBackupText("deploy-shop"),
			Namespace:        applicationBackupText("shop"),
			HasDatabase:      true,
			HasDatabaseKnown: true,
			Posture: &client.StackBackupPosture{
				ClusterID: applicationBackupsClusterID, StackName: "deploy-shop",
				ProtectionState: "protected", Stateful: true,
				VaultName: "production-backups", VaultState: "ready", Schedule: "daily",
				DataAssetCount: 2, DataAssetCountKnown: true, DatabaseAssetCount: 1,
				LastRestorePointAt: &lastRestorePointAt, NextScheduledAt: &nextScheduledAt,
			},
		}},
		ReadyVaultCount: 1,
		ReadyVaultKnown: true,
		Warnings:        []string{},
	}
}

func TestApplicationBackupsListsEveryDeploymentWithItsVerdict(t *testing.T) {
	mock := &applicationBackupsMock{backups: sampleApplicationBackups()}

	output, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backups", applicationBackupsAppID)

	if executeError != nil {
		t.Fatalf("reading the application's backups: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"Application 'shop'", "postgresql", "Database backup: on",
		"production", "deploy-shop", "protected", "production-backups", "daily",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected the section to contain %q, got:\n%s", expected, stripped)
		}
	}
}

// A deployment whose posture could not be read is unknown, never
// unprotected: telling an operator their data is unprotected on the strength
// of a read that did not happen is the failure the three-valued verdict
// exists to refuse.
func TestApplicationBackupsNeverCallsAnUnreadPostureUnprotected(t *testing.T) {
	backups := sampleApplicationBackups()
	backups.Deployments[0].Posture = nil
	backups.Deployments[0].StackName = nil
	mock := &applicationBackupsMock{backups: backups}

	output, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backups", applicationBackupsAppID)

	if executeError != nil {
		t.Fatalf("reading the application's backups: %v", executeError)
	}
	stripped := stripANSICodes(output)
	if !strings.Contains(stripped, "unknown") {
		t.Fatalf("an unread posture must render as unknown, got:\n%s", stripped)
	}
	if strings.Contains(stripped, "Protection:  unprotected") {
		t.Fatalf("an unread posture was reported as unprotected:\n%s", stripped)
	}
	if !strings.Contains(stripped, "not created yet") {
		t.Fatalf("expected the stack to be reported as not created yet, got:\n%s", stripped)
	}
}

func TestApplicationProtectSendsTheDeploymentAndNeverAStackName(t *testing.T) {
	mock := &applicationBackupsMock{
		backups: sampleApplicationBackups(),
		protection: &client.StackProtection{
			StackName:   "deploy-shop",
			Policy:      client.BackupPolicy{Enabled: true, Vault: "production-backups", Schedule: "daily"},
			BackupStack: client.BackupStackReady,
		},
	}

	output, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "protect", applicationBackupsAppID, "--cluster", "production")

	if executeError != nil {
		t.Fatalf("protecting the deployment: %v", executeError)
	}
	if mock.protectedOn != applicationBackupsClusterID {
		t.Errorf("protected deployment = %q, want the cluster id", mock.protectedOn)
	}
	if mock.protected == nil {
		t.Fatal("no protect request was sent")
	}
	if mock.protected.VaultID != "" {
		t.Errorf("vault id = %q, want empty so the platform resolves the single ready vault",
			mock.protected.VaultID)
	}
	if !strings.Contains(stripANSICodes(output), "deploy-shop") {
		t.Errorf("expected the resolved stack in the output, got:\n%s", output)
	}
}

func TestApplicationProtectRefusesAClusterTheApplicationIsNotOn(t *testing.T) {
	mock := &applicationBackupsMock{backups: sampleApplicationBackups()}

	_, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "protect", applicationBackupsAppID, "--cluster", "staging")

	if executeError == nil {
		t.Fatal("protecting a cluster with no deployment was allowed")
	}
	if !strings.Contains(executeError.Error(), "production") {
		t.Errorf("the refusal does not name the clusters it does run on: %v", executeError)
	}
	if mock.protected != nil {
		t.Error("a protect was sent for a deployment that does not exist")
	}
}

func TestApplicationProtectRefusesADeploymentWithNoStackYet(t *testing.T) {
	backups := sampleApplicationBackups()
	backups.Deployments[0].StackName = nil
	backups.Deployments[0].Posture = nil
	mock := &applicationBackupsMock{backups: backups}

	_, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "protect", applicationBackupsAppID, "--cluster", "production")

	if executeError == nil {
		t.Fatal("protecting a deployment with no stack was allowed")
	}
	if !strings.Contains(executeError.Error(), "deploy it first") {
		t.Errorf("the refusal does not say what to do: %v", executeError)
	}
	if mock.protected != nil {
		t.Error("a protect was sent for a deployment with no stack")
	}
}

func TestApplicationBackupOpensTheCaptureAndNamesTheRunToWatch(t *testing.T) {
	mock := &applicationBackupsMock{
		backups: sampleApplicationBackups(),
		createResult: &client.CreateRestorePointResult{
			RestorePointID: backupTestRestorePointID,
			RunID:          backupTestRunID,
			Warnings:       []string{"The cache volume is not in the selection."},
		},
	}

	output, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backup", applicationBackupsAppID,
		"--cluster", "production", "--note", "before the 3.2 migration")

	if executeError != nil {
		t.Fatalf("backing up the deployment: %v", executeError)
	}
	if mock.createdOn != applicationBackupsClusterID {
		t.Errorf("captured deployment = %q, want the cluster id", mock.createdOn)
	}
	if mock.created == nil || mock.created.Note != "before the 3.2 migration" {
		t.Errorf("the note did not reach the request: %+v", mock.created)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{backupTestRestorePointID, "production", backupTestRunID, "--wait"} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the output, got:\n%s", expected, stripped)
		}
	}
	if !strings.Contains(stripped, "The cache volume is not in the selection.") {
		t.Errorf("the capture's warnings were swallowed:\n%s", stripped)
	}
}

func TestApplicationBackupRequiresACluster(t *testing.T) {
	mock := &applicationBackupsMock{backups: sampleApplicationBackups()}

	_, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backup", applicationBackupsAppID)

	if executeError == nil {
		t.Fatal("a capture with no --cluster was allowed")
	}
	if !strings.Contains(executeError.Error(), "--cluster is required") {
		t.Errorf("unexpected refusal: %v", executeError)
	}
	if mock.created != nil {
		t.Error("a capture was sent without a deployment")
	}
}

func TestApplicationBackupsReportsAFailedReadRatherThanAnEmptySection(t *testing.T) {
	mock := &applicationBackupsMock{readError: errors.New("connection reset")}

	_, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backups", applicationBackupsAppID)

	if executeError == nil {
		t.Fatal("a failed read was reported as success")
	}
	if !strings.Contains(executeError.Error(), "reading the application's backups") {
		t.Errorf("unexpected error: %v", executeError)
	}
}
