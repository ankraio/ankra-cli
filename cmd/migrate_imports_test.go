package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// migrateImportsMock scripts the vault's import listing and deletion on top
// of the restore mock, which already answers the vault lookup and GET.
type migrateImportsMock struct {
	migrateRestoreMock
	listed  []client.BackupVaultImport
	deleted []string
}

func (mock *migrateImportsMock) ListBackupVaultImports(string) (*client.BackupVaultImportListResult, error) {
	return &client.BackupVaultImportListResult{Imports: mock.listed}, nil
}

func (mock *migrateImportsMock) DeleteBackupVaultImport(_ string, importID string) error {
	if strings.HasSuffix(importID, "-refused") {
		return errors.New("409 Conflict: The restore of this import is already running.")
	}
	mock.deleted = append(mock.deleted, importID)
	return nil
}

func installMigrateImportsMock(t *testing.T, mock *migrateImportsMock) {
	t.Helper()
	installMigrateRestoreMock(t, &mock.migrateRestoreMock)
	apiClient = mock
	migrateImportsListVault = ""
	migrateImportsDeleteVault = ""
	migrateImportsDeleteYes = false
	_ = migrateImportsListCmd.Flags().Set("output", "")
}

func TestMigrateImportsListShowsWhatTheVaultHolds(t *testing.T) {
	mock := &migrateImportsMock{migrateRestoreMock: migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}}}
	installMigrateImportsMock(t, mock)
	mock.listed = []client.BackupVaultImport{{
		ID: "6f1c8e2a-0000-4000-8000-000000000001", StackName: "shop", Status: "completed", CreatedAt: "2026-08-29T10:00:00Z",
		Databases: []client.BackupVaultImportDatabase{{Workload: "db", Artifacts: []client.BackupVaultImportArtifact{{SizeBytes: 5 << 20}, {SizeBytes: 3 << 20}}}},
	}}

	stdout, _, err := runMigrate(t, "imports", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"6f1c8e2a-0000-4000-8000-000000000001", "shop", "completed", "db", "8.0 MiB"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("listing must show %q:\n%s", want, stdout)
		}
	}

	mock.listed = nil
	stdout, _, err = runMigrate(t, "imports", "list")
	if err != nil || !strings.Contains(stdout, "holds no imports") {
		t.Errorf("an empty vault must say so, got %v:\n%s", err, stdout)
	}

	installMigrateImportsMock(t, &migrateImportsMock{})
	_, _, err = runMigrate(t, "imports", "list")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "no ready backup vault") {
		t.Errorf("no vault is a usage error, got %v", err)
	}
}

func TestMigrateImportsDeleteConfirmsAndRefusesARunningRestore(t *testing.T) {
	mock := &migrateImportsMock{migrateRestoreMock: migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}}}
	installMigrateImportsMock(t, mock)

	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	_, _, err := runMigrate(t, "imports", "delete", "6f1c8e2a-0000-4000-8000-000000000001")
	if exitCodeFor(err) != exitCancelled || len(mock.deleted) != 0 {
		t.Fatalf("a declined prompt must exit %d and delete nothing, got %v (deleted %v)", exitCancelled, err, mock.deleted)
	}

	stdout, _, err := runMigrate(t, "imports", "delete", "6f1c8e2a-0000-4000-8000-000000000001", "--yes")
	if err != nil || len(mock.deleted) != 1 || mock.deleted[0] != "6f1c8e2a-0000-4000-8000-000000000001" || !strings.Contains(stdout, "deleted; its dumps are removed") {
		t.Errorf("--yes must delete: err=%v deleted=%v\n%s", err, mock.deleted, stdout)
	}

	mock.getStatuses = []string{"restoring"}
	mock.getCount = 0
	_, _, err = runMigrate(t, "imports", "delete", "6f1c8e2a-0000-4000-8000-000000000002", "--yes")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "being restored right now") || len(mock.deleted) != 1 {
		t.Errorf("a restoring import is refused before the platform is asked, got %v", err)
	}

	mock.getStatuses = []string{"completed"}
	mock.getCount = 0
	_, _, err = runMigrate(t, "imports", "delete", "6f1c8e2a-0000-4000-8000-00000-refused", "--yes")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("the platform's refusal must surface, got %v", err)
	}
}
