package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	backupTestStack          = "shop"
	backupTestRestorePointID = "0b2f1c3d-4e5a-4b6c-8d7e-9f0a1b2c3d4e"
	backupTestRunID          = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	backupTestVaultID        = "2b3c4d5e-6f7a-4b8c-9d0e-1f2a3b4c5d6e"
)

// backupLaneMock answers the restore point, protection, data inventory and run
// surfaces, and records what the commands sent, so the tests assert the
// wire-facing behaviour without a server.
type backupLaneMock struct {
	baseMock
	cluster client.ClusterListItem

	listing      *client.RestorePointListResult
	listError    error
	listOptions  []client.ListRestorePointsOptions
	organisation *client.RestorePointListResult

	restorePoint      *client.RestorePoint
	restorePointReads int
	getError          error

	created       *client.CreateRestorePointRequest
	createResult  *client.CreateRestorePointResult
	createError   error
	deleteResult  *client.DeleteRestorePointResult
	deleteCalls   int
	restored      *client.RestoreRestorePointRequest
	restoreResult *client.RestoreRestorePointResult
	restoreError  error

	protected      *client.ProtectStackRequest
	protection     *client.StackProtection
	protectError   error
	unprotected    *client.UnprotectStackRequest
	unprotectCalls int

	inventory      *client.StackDataInventory
	inventoryError error

	runs        *client.RunListResult
	runSequence []*client.Run
	runReads    int
	runError    error
	cancelled   string
	retried     string
}

func (mock *backupLaneMock) GetCluster(name string) (client.ClusterListItem, error) {
	if mock.cluster.Name == name || mock.cluster.ID == name {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (mock *backupLaneMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return mock.GetCluster(clusterID)
}

// ListClusters is what resolveClusterID pages through for the commands that
// take a --cluster of their own rather than inheriting the cluster group's.
func (mock *backupLaneMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     []client.ClusterListItem{mock.cluster},
		Pagination: client.Pagination{TotalPages: 1, Page: 1},
	}, nil
}

func (mock *backupLaneMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	return &client.BackupVaultListResult{Items: []client.BackupVault{
		{ID: backupTestVaultID, Name: "production-backups"},
	}}, nil
}

func (mock *backupLaneMock) ListStackRestorePoints(_ string, _ string,
	options client.ListRestorePointsOptions) (*client.RestorePointListResult, error) {
	mock.listOptions = append(mock.listOptions, options)
	if mock.listError != nil {
		return nil, mock.listError
	}
	if mock.listing == nil {
		return &client.RestorePointListResult{}, nil
	}
	return mock.listing, nil
}

func (mock *backupLaneMock) ListOrganisationRestorePoints(
	options client.ListRestorePointsOptions) (*client.RestorePointListResult, error) {
	mock.listOptions = append(mock.listOptions, options)
	if mock.listError != nil {
		return nil, mock.listError
	}
	if mock.organisation == nil {
		return &client.RestorePointListResult{}, nil
	}
	return mock.organisation, nil
}

func (mock *backupLaneMock) GetStackRestorePoint(_ string, _ string, _ string) (*client.RestorePoint, error) {
	mock.restorePointReads++
	if mock.getError != nil {
		return nil, mock.getError
	}
	return mock.restorePoint, nil
}

func (mock *backupLaneMock) CreateStackRestorePoint(_ string, _ string,
	request client.CreateRestorePointRequest) (*client.CreateRestorePointResult, error) {
	mock.created = &request
	if mock.createError != nil {
		return nil, mock.createError
	}
	return mock.createResult, nil
}

func (mock *backupLaneMock) DeleteStackRestorePoint(_ string, _ string, _ string) (*client.DeleteRestorePointResult, error) {
	mock.deleteCalls++
	return mock.deleteResult, nil
}

func (mock *backupLaneMock) RestoreStackRestorePoint(_ string, _ string, _ string,
	request client.RestoreRestorePointRequest) (*client.RestoreRestorePointResult, error) {
	mock.restored = &request
	if mock.restoreError != nil {
		return nil, mock.restoreError
	}
	return mock.restoreResult, nil
}

func (mock *backupLaneMock) ProtectStack(_ string, _ string,
	request client.ProtectStackRequest) (*client.StackProtection, error) {
	mock.protected = &request
	if mock.protectError != nil {
		return nil, mock.protectError
	}
	return mock.protection, nil
}

func (mock *backupLaneMock) UnprotectStack(_ string, _ string,
	request client.UnprotectStackRequest) (*client.StackProtection, error) {
	mock.unprotectCalls++
	mock.unprotected = &request
	return mock.protection, nil
}

func (mock *backupLaneMock) GetStackDataAssets(_ string, _ string) (*client.StackDataInventory, error) {
	if mock.inventoryError != nil {
		return nil, mock.inventoryError
	}
	return mock.inventory, nil
}

func (mock *backupLaneMock) ListRuns(_ client.ListRunsOptions) (*client.RunListResult, error) {
	if mock.runError != nil {
		return nil, mock.runError
	}
	if mock.runs == nil {
		return &client.RunListResult{}, nil
	}
	return mock.runs, nil
}

func (mock *backupLaneMock) GetRun(string) (*client.Run, error) {
	if mock.runError != nil {
		return nil, mock.runError
	}
	index := mock.runReads
	mock.runReads++
	if index >= len(mock.runSequence) {
		index = len(mock.runSequence) - 1
	}
	if index < 0 {
		return nil, errors.New("no run scripted")
	}
	return mock.runSequence[index], nil
}

func (mock *backupLaneMock) CancelRun(runID string) (*client.Run, error) {
	mock.cancelled = runID
	return &client.Run{ID: runID, Status: client.RunStatusCancelled}, nil
}

func (mock *backupLaneMock) RetryRun(runID string) (*client.Run, error) {
	mock.retried = runID
	return &client.Run{ID: backupTestRunID, Kind: client.RunKindRestore, Status: client.RunStatusPending}, nil
}

func newBackupLaneMock() *backupLaneMock {
	return &backupLaneMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "demo"}}
}

// runBackupCommand executes rootCmd with stdin scripted and stdout and stderr
// captured together, then restores the flags of the commands it touched.
func runBackupCommand(t *testing.T, mock APIClient, input string, resets []*cobra.Command,
	args ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, input, resets, args...)
}

func sampleRestorePoint() client.RestorePoint {
	completedAt := "2026-09-15T10:31:02Z"
	return client.RestorePoint{
		ID: backupTestRestorePointID, BackupVaultID: backupTestVaultID,
		ScopeKind: "stack", SourceClusterName: "ankra-prod-hel1",
		StackNames: []string{backupTestStack},
		Trigger:    client.RestorePointTriggerManual, Status: client.RestorePointStatusComplete,
		ObjectPrefix: "restore-points/" + backupTestRestorePointID,
		TotalBytes:   5368709120, ImmutabilityMode: "none", VerificationStatus: "unverified",
		CreatedAt: "2026-09-15T10:04:11Z", UpdatedAt: completedAt, CompletedAt: &completedAt,
		AssetCount: 1,
		NotCarried: []client.RestorePointNotCarried{{
			Kind: "pvc", Name: "shop/cache",
			Reason: "This volume is not in the backup selection.",
			Remedy: "Name it in the request's selection.",
		}},
	}
}

func TestRestorePointsListRendersEveryColumnIncludingOmissions(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{sampleRestorePoint()}}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing restore points: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"ID", "STATUS", "TRIGGER", "SIZE", "ASSETS", "NOT CARRIED", "CREATED",
		backupTestRestorePointID, "complete", "manual", "5.0 GiB",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected the listing to contain %q, got:\n%s", expected, stripped)
		}
	}
}

// A listing that showed sizes and hid omissions is the silence this lane
// exists to remove, so the count of un-carried things is a column.
func TestRestorePointsListCountsWhatIsNotCarried(t *testing.T) {
	restorePoint := sampleRestorePoint()
	restorePoint.NotCarried = append(restorePoint.NotCarried, client.RestorePointNotCarried{
		Kind: "pvc", Name: "shop/tmp", Reason: "Not selected.",
	})
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{restorePoint}}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing restore points: %v", executeError)
	}
	if !strings.Contains(stripANSICodes(output), "2") {
		t.Fatalf("expected the not-carried count in the row, got:\n%s", output)
	}
}

func TestRestorePointsListEmptyPointsAtCreate(t *testing.T) {
	mock := newBackupLaneMock()

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing restore points: %v", executeError)
	}
	if !strings.Contains(output, "ankra cluster stacks restore-points create shop") {
		t.Fatalf("an empty listing must point at create, got:\n%s", output)
	}
}

func TestRestorePointsListStructuredOutputStaysParseable(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{sampleRestorePoint()}}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack, "--cluster", "demo", "-o", "json")

	if executeError != nil {
		t.Fatalf("listing restore points: %v", executeError)
	}
	var decoded client.RestorePointListResult
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json must stay parseable, got %v for:\n%s", decodeError, output)
	}
	if len(decoded.RestorePoints) != 1 || decoded.RestorePoints[0].ID != backupTestRestorePointID {
		t.Fatalf("the json carried the wrong restore points: %+v", decoded.RestorePoints)
	}
	if len(decoded.RestorePoints[0].NotCarried) != 1 {
		t.Fatal("not_carried must survive into the structured output")
	}
}

// The dark-lane 403 is not a permission problem and not a typo: the fix is to
// have the feature switched on, so the message says that rather than relaying
// the bare detail.
func TestRestorePointsListExplainsTheFeatureFlagRefusal(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listError = client.NewUnexpectedResponseError(http.StatusForbidden, backupsNotEnabledDetail)

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack, "--cluster", "demo")

	if executeError == nil {
		t.Fatal("a 403 from the dark lane must fail the command")
	}
	if !strings.Contains(executeError.Error(), backupsNotEnabledDetail) ||
		!strings.Contains(executeError.Error(), "ankra org current") {
		t.Fatalf("expected the flag-off hint, got: %v", executeError)
	}
}

func TestRestorePointsListResolvesAVaultName(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsListCmd},
		"cluster", "stacks", "restore-points", "list", backupTestStack,
		"--cluster", "demo", "--vault", "production-backups")

	if executeError != nil {
		t.Fatalf("listing restore points: %v", executeError)
	}
	if len(mock.listOptions) == 0 || mock.listOptions[len(mock.listOptions)-1].VaultID != backupTestVaultID {
		t.Fatalf("the vault name must be resolved to its id, got %+v", mock.listOptions)
	}
}

func TestRestorePointsGetPrintsManifestAssetsAndOmissions(t *testing.T) {
	restorePoint := sampleRestorePoint()
	restorePoint.Assets = []client.RestorePointAsset{{
		ID: "volume-shop-data", Kind: "volume", Engine: "velero", Consistency: "crash",
		Namespace: "shop", Name: "data", SizeBytes: 5368709120, Path: "volumes/shop/data",
	}}
	restorePoint.Manifest = &client.RestorePointManifest{
		SchemaVersion: 2,
		Source: client.RestorePointSource{
			Kind: "cluster", ClusterName: "ankra-prod-hel1",
			KubernetesVersion: "1.33.4", Topology: "k3s", StorageClasses: []string{"standard"},
		},
		Sizes:    client.RestorePointSizes{TotalBytes: 5368709120, VolumesBytes: 5368709120},
		Warnings: []string{"This cluster's storage capabilities were never probed."},
	}
	restorePoint.Run = &client.RestorePointRun{
		ID: backupTestRunID, Kind: client.RunKindBackup,
		Status: client.RunStatusSucceeded, Phase: "settled",
	}
	mock := newBackupLaneMock()
	mock.restorePoint = &restorePoint

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsGetCmd},
		"cluster", "stacks", "restore-points", "get", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo")

	if executeError != nil {
		t.Fatalf("getting a restore point: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		backupTestRestorePointID, "1.33.4", "volume-shop-data", "velero",
		"Not carried:", "shop/cache", "Remedy:", "Warnings:",
		"ankra runs get " + backupTestRunID,
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected the detail to contain %q, got:\n%s", expected, stripped)
		}
	}
}

func TestResolveRestorePointIDPassesAUUIDStraightThrough(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listError = errors.New("listing must not be called for a uuid")

	resolved, resolveError := resolveRestorePointID(mock, testClusterID, backupTestStack, backupTestRestorePointID)

	if resolveError != nil {
		t.Fatalf("a uuid must resolve to itself: %v", resolveError)
	}
	if resolved != backupTestRestorePointID {
		t.Fatalf("resolved to %q", resolved)
	}
}

func TestResolveRestorePointIDAcceptsAnUnambiguousPrefix(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{
		sampleRestorePoint(),
		{ID: "ffffffff-1111-4111-8111-111111111111"},
	}}

	resolved, resolveError := resolveRestorePointID(mock, testClusterID, backupTestStack, "0b2f")

	if resolveError != nil {
		t.Fatalf("an unambiguous prefix must resolve: %v", resolveError)
	}
	if resolved != backupTestRestorePointID {
		t.Fatalf("resolved to %q", resolved)
	}
}

// Two restore points sharing a prefix must be reported, never resolved to
// whichever the listing happened to order first.
func TestResolveRestorePointIDRefusesAnAmbiguousPrefix(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{
		{ID: "0b2f1111-1111-4111-8111-111111111111"},
		{ID: "0b2f2222-2222-4222-8222-222222222222"},
	}}

	_, resolveError := resolveRestorePointID(mock, testClusterID, backupTestStack, "0b2f")

	if resolveError == nil {
		t.Fatal("an ambiguous prefix must be refused")
	}
	if exitCodeFor(resolveError) != exitUsage {
		t.Fatalf("an ambiguous prefix is a bad invocation, got exit %d", exitCodeFor(resolveError))
	}
	if !strings.Contains(resolveError.Error(), "0b2f1111") || !strings.Contains(resolveError.Error(), "0b2f2222") {
		t.Fatalf("the refusal must name the candidates: %v", resolveError)
	}
}

func TestResolveRestorePointIDReportsAnUnknownPrefixAsNotFound(t *testing.T) {
	mock := newBackupLaneMock()
	mock.listing = &client.RestorePointListResult{RestorePoints: []client.RestorePoint{sampleRestorePoint()}}

	_, resolveError := resolveRestorePointID(mock, testClusterID, backupTestStack, "dead")

	if exitCodeFor(resolveError) != exitNotFound {
		t.Fatalf("an unknown prefix must exit not-found, got %d (%v)", exitCodeFor(resolveError), resolveError)
	}
}

func TestRestorePointsCreateWithoutWaitNamesTheRunToWatch(t *testing.T) {
	mock := newBackupLaneMock()
	mock.createResult = &client.CreateRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
		Warnings: []string{"shop/cache: This volume is not in the backup selection."},
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack,
		"--cluster", "demo", "--vault", "production-backups", "--include-pvc", "shop/data",
		"--note", "before the 3.2 upgrade")

	if executeError != nil {
		t.Fatalf("creating a restore point: %v", executeError)
	}
	if mock.created == nil {
		t.Fatal("the create never reached the client")
	}
	if mock.created.VaultID != backupTestVaultID {
		t.Errorf("the vault name must be resolved to its id, got %q", mock.created.VaultID)
	}
	if mock.created.Note != "before the 3.2 upgrade" {
		t.Errorf("the note must be sent, got %q", mock.created.Note)
	}
	if mock.created.Selection == nil ||
		len(mock.created.Selection.PersistentVolumeClaims) != 1 ||
		mock.created.Selection.PersistentVolumeClaims[0] != "shop/data" {
		t.Errorf("--include-pvc must reach the selection, got %+v", mock.created.Selection)
	}
	if !strings.Contains(output, "ankra runs get "+backupTestRunID) {
		t.Errorf("the command must name the run to watch, got:\n%s", output)
	}
	if !strings.Contains(output, "shop/cache") {
		t.Errorf("the platform's warnings must be shown, got:\n%s", output)
	}
}

// A command whose selection flags were all left alone must send no selection,
// so the platform falls back to the stack's stored one instead of being told
// to widen to everything.
func TestRestorePointsCreateSendsNoSelectionWhenNoneWasAsked(t *testing.T) {
	mock := newBackupLaneMock()
	mock.createResult = &client.CreateRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
	}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("creating a restore point: %v", executeError)
	}
	if mock.created.Selection != nil {
		t.Fatalf("an untouched selection must be omitted, got %+v", mock.created.Selection)
	}
	if mock.created.VaultID != "" {
		t.Fatalf("an unnamed vault must be left to the platform, got %q", mock.created.VaultID)
	}
}

// Naming volumes is not a decision about databases. Sending an explicit
// `databases: true` alongside --include-pvc would silently re-include the
// databases of a stack whose stored policy excludes them.
func TestRestorePointsCreateLeavesDatabasesUndecidedWhenOnlyVolumesAreNamed(t *testing.T) {
	mock := newBackupLaneMock()
	mock.createResult = &client.CreateRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
	}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack,
		"--cluster", "demo", "--include-pvc", "shop/data")

	if executeError != nil {
		t.Fatalf("creating a restore point: %v", executeError)
	}
	selection := mock.created.Selection
	if selection == nil || len(selection.PersistentVolumeClaims) != 1 {
		t.Fatalf("the named volume must reach the selection, got %+v", selection)
	}
	if selection.Databases != nil {
		t.Fatalf("naming a volume must not decide the databases, got %v", *selection.Databases)
	}
	if selection.ConfirmExcludeDatabases {
		t.Fatal("no exclusion was asked for, so nothing may be acknowledged")
	}
}

func TestProtectLeavesDatabasesUndecidedWhenOnlyVolumesAreNamed(t *testing.T) {
	mock := newBackupLaneMock()
	mock.protection = &client.StackProtection{
		StackName: backupTestStack,
		Policy:    client.BackupPolicy{Enabled: true, Vault: "production-backups"},
	}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksProtectCmd},
		"cluster", "stacks", "protect", backupTestStack, "--cluster", "demo",
		"--vault", "production-backups", "--include-pvc", "shop/data")

	if executeError != nil {
		t.Fatalf("protecting a stack: %v", executeError)
	}
	selection := mock.protected.Selection
	if selection == nil || len(selection.PersistentVolumeClaims) != 1 {
		t.Fatalf("the named volume must reach the selection, got %+v", selection)
	}
	if selection.Databases != nil {
		t.Fatalf("naming a volume must not decide the databases, got %v", *selection.Databases)
	}
}

func TestRestorePointsCreateRefusesExcludingDatabasesWithoutConfirmation(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack,
		"--cluster", "demo", "--exclude-databases")

	if executeError == nil {
		t.Fatal("excluding databases must need the acknowledgement")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage exit, got %d", exitCodeFor(executeError))
	}
	if mock.created != nil {
		t.Fatal("nothing may reach the platform when the invocation is refused")
	}
}

func TestRestorePointsCreateWaitFollowsTheRunAndPrintsTheSealedPoint(t *testing.T) {
	restorePoint := sampleRestorePoint()
	mock := newBackupLaneMock()
	mock.createResult = &client.CreateRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
	}
	mock.runSequence = []*client.Run{
		{ID: backupTestRunID, Kind: client.RunKindBackup, Status: client.RunStatusRunning},
		{ID: backupTestRunID, Kind: client.RunKindBackup, Status: client.RunStatusSucceeded},
	}
	mock.restorePoint = &restorePoint
	withFastRunPolling(t)

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack, "--cluster", "demo", "--wait")

	if executeError != nil {
		t.Fatalf("waiting for a restore point: %v", executeError)
	}
	if mock.restorePointReads == 0 {
		t.Fatal("--wait must read the sealed restore point back")
	}
	if !strings.Contains(stripANSICodes(output), "Restore Point:") {
		t.Fatalf("--wait must print the sealed restore point, got:\n%s", output)
	}
}

// Giving up waiting and the run failing are different outcomes, and a run that
// failed must not exit zero.
func TestRestorePointsCreateWaitFailsWhenTheRunFails(t *testing.T) {
	excerpt := "the agent could not reach the vault"
	mock := newBackupLaneMock()
	mock.createResult = &client.CreateRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
	}
	mock.runSequence = []*client.Run{{
		ID: backupTestRunID, Kind: client.RunKindBackup,
		Status: client.RunStatusFailed, ErrorExcerpt: &excerpt,
	}}
	withFastRunPolling(t)

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsCreateCmd},
		"cluster", "stacks", "restore-points", "create", backupTestStack, "--cluster", "demo", "--wait")

	if executeError == nil {
		t.Fatal("a failed run must fail the command")
	}
	if !strings.Contains(executeError.Error(), excerpt) {
		t.Fatalf("the platform's own reason must survive, got: %v", executeError)
	}
}

func TestRestorePointsDeleteDeclinedDeletesNothing(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "n\n",
		[]*cobra.Command{clusterStacksRestorePointsDeleteCmd},
		"cluster", "stacks", "restore-points", "delete", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo")

	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", executeError)
	}
	if mock.deleteCalls != 0 {
		t.Fatalf("a declined prompt must delete nothing, got %d calls", mock.deleteCalls)
	}
}

// A delete that quietly left the objects behind would understate somebody's
// storage bill forever, so the retained-objects reason is printed.
func TestRestorePointsDeleteReportsRetainedObjects(t *testing.T) {
	mock := newBackupLaneMock()
	mock.deleteResult = &client.DeleteRestorePointResult{
		RestorePointID: backupTestRestorePointID, Status: client.RestorePointStatusDeleted,
		ObjectsRetainedReason: "the source cluster no longer exists",
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsDeleteCmd},
		"cluster", "stacks", "restore-points", "delete", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo", "--yes")

	if executeError != nil {
		t.Fatalf("deleting a restore point: %v", executeError)
	}
	if !strings.Contains(output, "the source cluster no longer exists") {
		t.Fatalf("the retained-objects reason must be printed, got:\n%s", output)
	}
}

func TestRestorePointsRestorePrintsTheSequenceAndSendsTheTypedConfirmation(t *testing.T) {
	restorePoint := sampleRestorePoint()
	mock := newBackupLaneMock()
	mock.restoreResult = &client.RestoreRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID,
		Mode: client.RestoreModeInPlace,
		Sequence: []string{
			"Scale down the workloads in 'shop' that own the data being restored.",
			"Remove the volumes the restore point replaces.",
			"Restore the restore point's assets onto the cluster.",
			"Scale the workloads back up over the restored data.",
		},
		RestorePoint: restorePoint,
	}

	output, executeError := runBackupCommand(t, mock, backupTestStack+"\n",
		[]*cobra.Command{clusterStacksRestorePointsRestoreCmd},
		"cluster", "stacks", "restore-points", "restore", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo")

	if executeError != nil {
		t.Fatalf("restoring: %v", executeError)
	}
	if mock.restored == nil || mock.restored.Confirm != backupTestStack {
		t.Fatalf("the stack name must be sent as the confirmation, got %+v", mock.restored)
	}
	if mock.restored.Mode != client.RestoreModeInPlace {
		t.Fatalf("the restore mode must be in_place, got %q", mock.restored.Mode)
	}
	for position := 1; position <= 4; position++ {
		if !strings.Contains(output, "  "+string(rune('0'+position))+". ") {
			t.Errorf("expected step %d of the sequence in the output, got:\n%s", position, output)
		}
	}
}

func TestRestorePointsRestoreRefusesAMistypedStackName(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "shopp\n",
		[]*cobra.Command{clusterStacksRestorePointsRestoreCmd},
		"cluster", "stacks", "restore-points", "restore", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo")

	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("a mistyped name must cancel, got %v", executeError)
	}
	if mock.restored != nil {
		t.Fatal("nothing may reach the platform when the confirmation failed")
	}
}

// The platform refuses a restore point carrying a CloudNativePG or Percona
// database with a reason the CLI has no business rewording.
func TestRestorePointsRestoreRelaysTheConflictReasonVerbatim(t *testing.T) {
	const reason = "Restoring this restore point in place is not supported yet: it carries a " +
		"database this platform cannot yet recreate in place."
	mock := newBackupLaneMock()
	mock.restoreError = client.NewUnexpectedResponseError(http.StatusConflict, reason)

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsRestoreCmd},
		"cluster", "stacks", "restore-points", "restore", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo", "--yes")

	if executeError == nil {
		t.Fatal("a 409 must fail the command")
	}
	if !strings.Contains(executeError.Error(), reason) {
		t.Fatalf("the platform's reason must be relayed verbatim, got: %v", executeError)
	}
}

func TestRestorePointsRestoreForceReachesThePlatform(t *testing.T) {
	mock := newBackupLaneMock()
	mock.restoreResult = &client.RestoreRestorePointResult{
		RestorePointID: backupTestRestorePointID, RunID: backupTestRunID, Mode: client.RestoreModeInPlace,
	}

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksRestorePointsRestoreCmd},
		"cluster", "stacks", "restore-points", "restore", backupTestStack, backupTestRestorePointID,
		"--cluster", "demo", "--yes", "--force")

	if executeError != nil {
		t.Fatalf("restoring with --force: %v", executeError)
	}
	if mock.restored == nil || !mock.restored.Force {
		t.Fatalf("--force must reach the platform, got %+v", mock.restored)
	}
}

func TestOrganisationRestorePointsListShowsWhereEachCameFrom(t *testing.T) {
	mock := newBackupLaneMock()
	mock.organisation = &client.RestorePointListResult{
		RestorePoints: []client.RestorePoint{sampleRestorePoint()},
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{backupRestorePointsListCmd},
		"backup", "restore-points", "list")

	if executeError != nil {
		t.Fatalf("listing the organisation's restore points: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{"CLUSTER", "STACK", "ankra-prod-hel1", backupTestStack} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the organisation-wide listing, got:\n%s", expected, stripped)
		}
	}
}

func TestOrganisationRestorePointsListFiltersByClusterAndStack(t *testing.T) {
	mock := newBackupLaneMock()

	_, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{backupRestorePointsListCmd},
		"backup", "restore-points", "list", "--cluster", "demo", "--stack", backupTestStack,
		"--status", "complete")

	if executeError != nil {
		t.Fatalf("listing the organisation's restore points: %v", executeError)
	}
	if len(mock.listOptions) != 1 {
		t.Fatalf("expected one listing call, got %d", len(mock.listOptions))
	}
	options := mock.listOptions[0]
	if options.ClusterID != testClusterID || options.StackName != backupTestStack {
		t.Errorf("the cluster and stack filters must reach the query, got %+v", options)
	}
	if len(options.Statuses) != 1 || options.Statuses[0] != "complete" {
		t.Errorf("the status filter must reach the query, got %+v", options.Statuses)
	}
}

// withFastRunPolling makes the --wait tests finish in milliseconds rather than
// the interval a real follow uses.
func withFastRunPolling(t *testing.T) {
	t.Helper()
	original := dataRunPollInterval
	dataRunPollInterval = time.Millisecond
	t.Cleanup(func() { dataRunPollInterval = original })
}
