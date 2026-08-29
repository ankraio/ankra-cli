package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const migrateRestoreTestManifest = `{
  "version": 1,
  "module": "docker",
  "source_dir": "/src/shop",
  "created_at": "2026-08-29T01:02:03Z",
  "databases": [{
    "workload": "db", "engine": "postgres", "server_version": "17.2",
    "target": {"namespace": "shop", "host": "db", "port": 5432, "username": "office", "password_secret": "db-secrets", "password_key": "POSTGRES_PASSWORD"},
    "artifacts": [
      {"path": "db/globals.sql", "kind": "globals", "format": "sql", "size_bytes": 11, "sha256": "aa"},
      {"path": "db/office.dump", "kind": "database", "format": "pg_custom", "database": "office", "size_bytes": 5, "sha256": "bb"}
    ]
  }]
}`

// migrateRestoreMock scripts the platform: it records the import lifecycle
// calls and answers GET with a sequence of statuses.
type migrateRestoreMock struct {
	baseMock
	vaults       []client.BackupVault
	createdWith  *client.CreateBackupVaultImportRequest
	uploads      map[string]int64
	completed    int
	restored     int
	getStatuses  []string
	getCount     int
	failExcerpt  string
	uploadFailOn string
}

func (mock *migrateRestoreMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	return &client.BackupVaultListResult{Items: mock.vaults}, nil
}

// CreateBackupVaultImport mints one upload per artifact the submitted
// manifest lists, at the size it recorded - what the platform does.
func (mock *migrateRestoreMock) CreateBackupVaultImport(vaultID string, request client.CreateBackupVaultImportRequest) (*client.CreateBackupVaultImportResult, error) {
	mock.createdWith = &request
	var manifest struct {
		Databases []struct {
			Artifacts []struct {
				Path      string `json:"path"`
				SizeBytes int64  `json:"size_bytes"`
			} `json:"artifacts"`
		} `json:"databases"`
	}
	if unmarshalError := json.Unmarshal(request.Manifest, &manifest); unmarshalError != nil {
		return nil, unmarshalError
	}
	var uploads []client.BackupVaultImportUpload
	for _, database := range manifest.Databases {
		for _, artifact := range database.Artifacts {
			uploads = append(uploads, client.BackupVaultImportUpload{
				Path: artifact.Path, Method: "PUT", SizeBytes: artifact.SizeBytes,
				URL: "https://vault.example.com/imports/1/" + artifact.Path + "?sig=" + artifact.Path,
			})
		}
	}
	imported := mock.importView(client.BackupVaultImportStatusUploading)
	return &client.CreateBackupVaultImportResult{Import: imported, Uploads: uploads}, nil
}

func (mock *migrateRestoreMock) UploadPresignedObject(_ context.Context, uploadURL string, body io.Reader, size int64) error {
	if mock.uploadFailOn != "" && strings.Contains(uploadURL, mock.uploadFailOn) {
		return errors.New("403 SignatureDoesNotMatch")
	}
	content, readError := io.ReadAll(body)
	if readError != nil {
		return readError
	}
	if int64(len(content)) != size {
		return errors.New("body does not match the declared size")
	}
	if mock.uploads == nil {
		mock.uploads = map[string]int64{}
	}
	mock.uploads[uploadURL] = size
	return nil
}

func (mock *migrateRestoreMock) CompleteBackupVaultImport(string, string) (*client.BackupVaultImport, error) {
	mock.completed++
	imported := mock.importView(client.BackupVaultImportStatusUploaded)
	return &imported, nil
}

func (mock *migrateRestoreMock) RestoreBackupVaultImport(string, string) (*client.BackupVaultImport, error) {
	mock.restored++
	imported := mock.importView(client.BackupVaultImportStatusRestoring)
	imported.Restore = &client.BackupVaultImportRestore{OperationID: "op-1", Status: "restoring",
		Steps: []client.BackupVaultImportRestoreStep{{StepID: "step-1", Workload: "db", Status: "pending"}}}
	return &imported, nil
}

func (mock *migrateRestoreMock) GetBackupVaultImport(string, string) (*client.BackupVaultImport, error) {
	index := mock.getCount
	if index >= len(mock.getStatuses) {
		index = len(mock.getStatuses) - 1
	}
	mock.getCount++
	status := mock.getStatuses[index]
	imported := mock.importView(status)
	stepStatus := map[string]string{"restoring": "running", "completed": "success", "failed": "failed"}[status]
	step := client.BackupVaultImportRestoreStep{StepID: "step-1", Workload: "db", Status: stepStatus}
	if status == "failed" {
		step.ErrorExcerpt = mock.failExcerpt
		excerpt := "db: " + mock.failExcerpt
		imported.ErrorExcerpt = &excerpt
	}
	imported.Restore = &client.BackupVaultImportRestore{OperationID: "op-1", Status: status, Steps: []client.BackupVaultImportRestoreStep{step}}
	return &imported, nil
}

func (mock *migrateRestoreMock) importView(status string) client.BackupVaultImport {
	return client.BackupVaultImport{
		ID: "6f1c8e2a-0000-4000-8000-000000000001", BackupVaultID: "vault-1", ClusterID: "cluster-1", StackName: "shop",
		Status: status, ObjectPrefix: "imports/6f1c8e2a", Warnings: []string{},
		Databases: []client.BackupVaultImportDatabase{{
			Workload: "db", Engine: "postgres", ServerVersion: "17.2",
			Target: client.BackupVaultImportTarget{Namespace: "shop", Host: "db", Port: 5432, Username: "office"},
		}},
	}
}

func writeMigrateRestoreFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range map[string]string{
		"manifest.json":  migrateRestoreTestManifest,
		"db/globals.sql": "CREATE ROLE",
		"db/office.dump": "PGDMP",
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// installMigrateRestoreMock points the commands at the scripted platform.
// The token satisfies the login gate without touching this machine's
// configuration, so the tests never depend on a real session.
func installMigrateRestoreMock(t *testing.T, mock *migrateRestoreMock) {
	t.Helper()
	previous := apiClient
	apiClient = mock
	original := migrateRestorePollInterval
	migrateRestorePollInterval = time.Millisecond
	t.Setenv("ANKRA_API_TOKEN", "migrate-restore-test-token")
	t.Cleanup(func() {
		apiClient = previous
		migrateRestorePollInterval = original
	})
	resetMigrateRestoreFlags()
}

func resetMigrateRestoreFlags() {
	migrateRestoreCluster = ""
	migrateRestoreStack = ""
	migrateRestoreVault = ""
	migrateRestoreStatusVault = ""
	for _, command := range []string{"restore", "restore-status", "data"} {
		sub, _, _ := migrateCmd.Find([]string{command})
		if sub == nil {
			continue
		}
		_ = sub.Flags().Set("wait", "false")
		_ = sub.Flags().Set("output", "")
	}
}

const migrateRestoreTestCluster = "11111111-1111-4111-8111-111111111111"

func readyVault(name string) client.BackupVault {
	return client.BackupVault{ID: "22222222-2222-4222-8222-22222222222" + name[len(name)-1:], Name: name, Status: "ready"}
}

func TestMigrateRestoreCommandsRequireLogin(t *testing.T) {
	for _, command := range []string{"restore", "restore-status", "data"} {
		sub, _, err := migrateCmd.Find([]string{command})
		if err != nil {
			t.Fatal(err)
		}
		if !commandRequiresAuth(sub) {
			t.Errorf("ankra migrate %s talks to the platform and must require a login", command)
		}
	}
}

func TestMigrateRestoreUploadsVerifiesAndFollowsTheRestore(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"restoring", "restoring", "completed"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)

	stdout, stderr, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster, "--wait", "--timeout", "10s")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if mock.createdWith == nil || mock.createdWith.ClusterID != migrateRestoreTestCluster || mock.createdWith.StackName != "shop" ||
		!strings.Contains(string(mock.createdWith.Manifest), `"workload": "db"`) {
		t.Errorf("import registered with %+v", mock.createdWith)
	}
	if len(mock.uploads) != 2 || mock.uploads["https://vault.example.com/imports/1/db/office.dump?sig=db/office.dump"] != 5 {
		t.Errorf("uploads = %v", mock.uploads)
	}
	if mock.completed != 1 || mock.restored != 1 {
		t.Errorf("completed=%d restored=%d, want one each", mock.completed, mock.restored)
	}
	if mock.getCount < 3 {
		t.Errorf("--wait must poll until the import settles, polled %d times", mock.getCount)
	}
	for _, fragment := range []string{"Uploading db/office.dump (5 B)", "Verifying the upload", "Restoring 1 database server(s)", "db: running", "db: success"} {
		if !strings.Contains(stderr, fragment) {
			t.Errorf("progress lacks %q:\n%s", fragment, stderr)
		}
	}
	if !strings.Contains(stdout, "Restore complete: 1 database server(s) loaded") || !strings.Contains(stdout, "shop/db:5432") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestMigrateRestoreWithoutWaitReportsTheImport(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)

	stdout, _, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster)
	if err != nil {
		t.Fatal(err)
	}
	if mock.getCount != 0 {
		t.Errorf("without --wait nothing should be polled, polled %d times", mock.getCount)
	}
	if !strings.Contains(stdout, "Restore started (import 6f1c8e2a-0000-4000-8000-000000000001)") || !strings.Contains(stdout, "ankra migrate restore-status 6f1c8e2a-0000-4000-8000-000000000001 --wait") {
		t.Errorf("stdout:\n%s", stdout)
	}

	stdout, _, err = runMigrate(t, "restore-status", "6f1c8e2a-0000-4000-8000-000000000001", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Restore complete") {
		t.Errorf("restore-status stdout:\n%s", stdout)
	}
}

func TestMigrateRestoreReportsAFailedRestore(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"restoring", "failed"}, failExcerpt: "pg_restore reported errors for office"}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)

	_, stderr, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster, "--wait", "--timeout", "10s")
	if err == nil || !strings.Contains(err.Error(), "the restore of import 6f1c8e2a-0000-4000-8000-000000000001 failed: db: pg_restore reported errors for office") {
		t.Errorf("a failed restore must fail the command with the excerpt, got %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "db: failed") {
		t.Errorf("the step transition belongs in the progress output:\n%s", stderr)
	}
}

func TestMigrateRestoreTimesOutWithTheWaitExitCode(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"restoring"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)

	_, _, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster, "--wait", "--timeout", "30ms")
	if exitCodeFor(err) != exitWaitTimeout {
		t.Errorf("an expired --timeout should exit %d, got %v", exitWaitTimeout, err)
	}
}

func TestMigrateRestoreVaultSelection(t *testing.T) {
	dir := writeMigrateRestoreFixture(t)

	installMigrateRestoreMock(t, &migrateRestoreMock{getStatuses: []string{"completed"}})
	_, _, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster)
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "no ready backup vault") {
		t.Errorf("no vault should be a usage error naming the fix, got %v", err)
	}

	installMigrateRestoreMock(t, &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1"), readyVault("archive2")}, getStatuses: []string{"completed"}})
	_, _, err = runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster)
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "2 ready backup vaults (backups1, archive2)") {
		t.Errorf("two vaults should ask for --vault, got %v", err)
	}

	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1"), readyVault("archive2")}, getStatuses: []string{"completed"}}
	installMigrateRestoreMock(t, mock)
	if _, _, err = runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster, "--vault", "archive2"); err != nil {
		t.Errorf("--vault by name should resolve: %v", err)
	}
}

func TestMigrateRestoreRefusesAChangedArtifact(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "db", "office.dump"), []byte("PGDMP-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster)
	if err == nil || !strings.Contains(err.Error(), "artifact db/office.dump is 13 bytes but the manifest recorded 5") {
		t.Errorf("a changed artifact must be refused before upload, got %v", err)
	}
	if mock.completed != 0 {
		t.Error("nothing may be completed when an upload was refused")
	}

	mock = &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}, uploadFailOn: "office.dump"}
	installMigrateRestoreMock(t, mock)
	dir = writeMigrateRestoreFixture(t)
	_, _, err = runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster)
	if err == nil || !strings.Contains(err.Error(), "uploading db/office.dump: 403 SignatureDoesNotMatch") {
		t.Errorf("an upload failure must name the artifact, got %v", err)
	}
	if _, _, err = runMigrate(t, "restore", t.TempDir(), "--cluster", migrateRestoreTestCluster); exitCodeFor(err) != exitUsage {
		t.Errorf("a directory without a manifest should exit %d, got %v", exitUsage, err)
	}
}

func TestMigrateRestoreStructuredOutput(t *testing.T) {
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateRestoreFixture(t)
	stdout, _, err := runMigrate(t, "restore", dir, "--cluster", migrateRestoreTestCluster, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "{") || !strings.Contains(stdout, `"status": "restoring"`) || !strings.Contains(stdout, `"object_prefix": "imports/6f1c8e2a"`) {
		t.Errorf("json output must be the only thing on stdout:\n%s", stdout)
	}
}

func TestMigrateDataExportsThenRestores(t *testing.T) {
	fakeDockerOnPath(t)
	mock := &migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}}
	installMigrateRestoreMock(t, mock)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "data")

	stdout, stderr, err := runMigrate(t, "data", dir, "--out", out, "--cluster", migrateRestoreTestCluster, "--wait", "--timeout", "10s")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if _, statError := os.Stat(filepath.Join(out, "manifest.json")); statError != nil {
		t.Errorf("data must leave the export behind: %v", statError)
	}
	if mock.createdWith == nil || !strings.Contains(string(mock.createdWith.Manifest), `"database": "office"`) || mock.createdWith.StackName != "shop" {
		t.Errorf("the export's manifest must be what is registered, got %+v", mock.createdWith)
	}
	if len(mock.uploads) != 2 {
		t.Errorf("both exported artifacts must be uploaded, got %v", mock.uploads)
	}
	if !strings.Contains(stderr, "Exported 1 database server(s)") || !strings.Contains(stdout, "Restore complete") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
