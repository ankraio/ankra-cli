package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// clusterBackupsMock answers the cluster Backups read and records what the
// command asked for, so the tests assert the wire-facing behaviour without a
// server.
type clusterBackupsMock struct {
	baseMock
	cluster client.ClusterListItem

	page      *client.ClusterBackupsPage
	readError error
	options   []client.ClusterBackupsOptions
}

func (mock *clusterBackupsMock) GetCluster(name string) (client.ClusterListItem, error) {
	if mock.cluster.Name == name || mock.cluster.ID == name {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (mock *clusterBackupsMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return mock.GetCluster(clusterID)
}

func (mock *clusterBackupsMock) GetClusterBackups(_ string,
	options client.ClusterBackupsOptions) (*client.ClusterBackupsPage, error) {
	mock.options = append(mock.options, options)
	if mock.readError != nil {
		return nil, mock.readError
	}
	return mock.page, nil
}

// backupsTimePointer keeps the fixture's optional timestamps readable.
func backupsTimePointer(value string) *string { return &value }

func newClusterBackupsMock() *clusterBackupsMock {
	return &clusterBackupsMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "demo"}}
}

func sampleClusterBackupsPage() *client.ClusterBackupsPage {
	return &client.ClusterBackupsPage{
		ClusterID:        testClusterID,
		BackupStackState: client.ClusterBackupStackReady,
		Rollup: client.ClusterBackupsRollup{
			TotalStacks: 4, StatefulStacks: 3, ProtectedStacks: 1,
			UnprotectedStatefulStacks: 1, UnknownStacks: 1, FailingSchedules: 1,
			LastRestorePointAt: backupsTimePointer("2026-09-14T09:00:00Z"),
			VaultsTotal:        2, VaultsReady: 1, VaultsKnown: true,
		},
		Stacks: []client.StackBackupsRow{
			{
				StackName: "shop", Stateful: true,
				ProtectionState: client.ProtectionStateProtected,
				DataAssetCount:  2, DataAssetCountKnown: true, DatabaseAssetCount: 1,
				VaultName: "prod-vault", VaultState: "ready", Schedule: "0 3 * * *",
				NextScheduledAt: backupsTimePointer("2026-09-16T03:00:00Z"),
				LastRestorePoint: &client.StackRestorePointSummary{
					ID: backupTestRestorePointID, Status: "complete",
					SizeBytes: 5368709120, SizeBytesKnown: true,
					CreatedAt: "2026-09-14T09:00:00Z",
				},
				LatestRun: &client.StackRunSummary{
					ID: backupTestRunID, Kind: "restore", Status: "running",
					CreatedAt: "2026-09-15T08:00:00Z",
				},
			},
			{
				StackName: "analytics", Stateful: true,
				ProtectionState: client.ProtectionStateUnprotected, UnprotectedReason: "no_policy",
				DataAssetCount: 1, DataAssetCountKnown: true,
			},
			{
				StackName:       "mystery",
				ProtectionState: client.ProtectionStateUnknown, UnknownReason: "data_assets_unknown",
			},
			{
				StackName: "ankra-backup", SystemStack: true,
				ProtectionState: client.ProtectionStateUnprotected, UnprotectedReason: "no_data_assets",
				DataAssetCountKnown: true,
			},
		},
	}
}

func runClusterBackups(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, "", []*cobra.Command{clusterBackupsStatusCmd}, args...)
}

func TestClusterBackupsStatusRendersEveryStackAgainstWhatProtectsIt(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()

	output, executeError := runClusterBackups(t, mock,
		"cluster", "backups", "status", "--cluster", "demo")
	if executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}

	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"STACK", "PROTECTION", "DATA", "VAULT", "SCHEDULE", "LAST RESTORE POINT", "NEXT RUN", "LAST RUN",
		"1 of 3 stacks with data are backed up",
		"1 could not be assessed",
		"1 had a backup fail in the last 24 hours",
		"Backup components: ready",
		"Backup vaults: 1 of 2 ready.",
		"shop", "protected", "prod-vault", "0 3 * * *", "5.0 GiB",
		"restore running",
		"analytics", "unprotected (no_policy)",
		"mystery", "unknown (data_assets_unknown)",
		// The platform stack is in the listing so the counts are the
		// posture's, and it is marked so nobody wonders why it is unprotected.
		"ankra-backup (Ankra)",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected the status to contain %q, got:\n%s", expected, stripped)
		}
	}
}

// A stack whose inventory has not landed reports zero assets, and zero assets
// is not "this stack holds no data".
func TestClusterBackupsStatusSeparatesAnUnreadInventoryFromAnEmptyOne(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()

	output, executeError := runClusterBackups(t, mock,
		"cluster", "backups", "status", "--cluster", "demo")
	if executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}
	stripped := stripANSICodes(output)
	if !strings.Contains(stripped, "not reported") {
		t.Errorf("expected an unread inventory to read 'not reported', got:\n%s", stripped)
	}
}

func TestClusterBackupsStatusExplainsAnInstallingDataPlane(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.BackupStackState = client.ClusterBackupStackInstalling

	output, _ := runClusterBackups(t, mock, "cluster", "backups", "status", "--cluster", "demo")
	if !strings.Contains(stripANSICodes(output), "installing - a backup started now waits") {
		t.Errorf("expected the installing state to say what it means, got:\n%s", output)
	}
}

func TestClusterBackupsStatusNudgesAnOrganisationWithNoReadyVault(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.Rollup.VaultsTotal = 0
	mock.page.Rollup.VaultsReady = 0

	output, _ := runClusterBackups(t, mock, "cluster", "backups", "status", "--cluster", "demo")
	if !strings.Contains(stripANSICodes(output), "ankra backup vaults create") {
		t.Errorf("expected the no-vault state to say what to do, got:\n%s", output)
	}
}

// An unreadable vault listing is not an organisation without vaults, so the
// setup nudge is withheld rather than printed on a blip.
func TestClusterBackupsStatusWithholdsTheVaultNudgeWhenTheListingWasNotRead(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.Rollup.VaultsKnown = false
	mock.page.Rollup.VaultsTotal = 0
	mock.page.Rollup.VaultsReady = 0

	output, _ := runClusterBackups(t, mock, "cluster", "backups", "status", "--cluster", "demo")
	stripped := stripANSICodes(output)
	if strings.Contains(stripped, "ankra backup vaults create") {
		t.Errorf("an unread vault listing produced the no-vault nudge:\n%s", stripped)
	}
	if !strings.Contains(stripped, "could not be read") {
		t.Errorf("expected the unread vault listing to say so, got:\n%s", stripped)
	}
}

func TestClusterBackupsStatusCarriesTheFilterCursorAndLimit(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()

	if _, executeError := runClusterBackups(t, mock, "cluster", "backups", "status",
		"--cluster", "demo", "--protection", "unprotected", "--protection", "UNKNOWN",
		"--cursor", "c2hvcA", "--limit", "10"); executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}
	if len(mock.options) != 1 {
		t.Fatalf("the command made %d reads, want 1", len(mock.options))
	}
	asked := mock.options[0]
	if len(asked.ProtectionStates) != 2 ||
		asked.ProtectionStates[0] != client.ProtectionStateUnprotected ||
		asked.ProtectionStates[1] != client.ProtectionStateUnknown {
		t.Errorf("protection states = %v, want them normalised and both carried", asked.ProtectionStates)
	}
	if asked.Cursor != "c2hvcA" || asked.Limit != 10 {
		t.Errorf("cursor = %q, limit = %d", asked.Cursor, asked.Limit)
	}
}

// A verdict that does not exist is refused rather than sent and answered with
// an empty page, which would read as "nothing matched".
func TestClusterBackupsStatusRefusesAVerdictThatDoesNotExist(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()

	_, executeError := runClusterBackups(t, mock, "cluster", "backups", "status",
		"--cluster", "demo", "--protection", "partially")
	if executeError == nil {
		t.Fatal("an unknown protection state was accepted")
	}
	if !strings.Contains(executeError.Error(), "unknown protection state") {
		t.Errorf("error = %v, want it to name the bad value", executeError)
	}
	if len(mock.options) != 0 {
		t.Error("the refused filter still reached the API")
	}
}

// The help promises a bound, so the command enforces it rather than relaying
// a server validation error - and a negative limit is refused rather than
// dropped into a page size nobody gets and nobody is told about.
func TestClusterBackupsStatusRefusesALimitOutsideTheBoundItPromises(t *testing.T) {
	for _, limit := range []string{"0", "-3", "5000"} {
		mock := newClusterBackupsMock()
		mock.page = sampleClusterBackupsPage()
		_, executeError := runClusterBackups(t, mock, "cluster", "backups", "status",
			"--cluster", "demo", "--limit", limit)
		if executeError == nil {
			t.Errorf("--limit %s was accepted", limit)
			continue
		}
		if !strings.Contains(executeError.Error(), "between 1 and 100") {
			t.Errorf("--limit %s: error = %v, want it to name the bound", limit, executeError)
		}
		if len(mock.options) != 0 {
			t.Errorf("--limit %s still reached the API", limit)
		}
	}
}

// A data-plane state this build does not know is reported as unrecognised: a
// bare word reads as a verdict the command stands behind.
func TestClusterBackupsStatusSaysWhenItDoesNotRecogniseTheDataPlaneState(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.BackupStackState = "quiescing"

	output, _ := runClusterBackups(t, mock, "cluster", "backups", "status", "--cluster", "demo")
	if !strings.Contains(stripANSICodes(output), "does not recognise that state") {
		t.Errorf("expected an unrecognised state to say so, got:\n%s", output)
	}
}

func TestClusterBackupsStatusRendersStructuredOutputVerbatim(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()

	output, executeError := runClusterBackups(t, mock,
		"cluster", "backups", "status", "--cluster", "demo", "--output", "json")
	if executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}
	var decoded client.ClusterBackupsPage
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("decoding the structured output: %v\n%s", decodeError, output)
	}
	if decoded.BackupStackState != client.ClusterBackupStackReady || len(decoded.Stacks) != 4 {
		t.Errorf("structured output = %+v", decoded)
	}
	if decoded.Rollup.VaultsReady != 1 || !decoded.Rollup.VaultsKnown {
		t.Errorf("the rollup's vault readiness did not survive: %+v", decoded.Rollup)
	}
}

func TestClusterBackupsStatusSaysWhenTheFilterMatchedNothing(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.Stacks = nil

	output, executeError := runClusterBackups(t, mock,
		"cluster", "backups", "status", "--cluster", "demo", "--protection", "protected")
	if executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}
	if !strings.Contains(stripANSICodes(output), "No stack matched.") {
		t.Errorf("expected an empty page to say so, got:\n%s", output)
	}
}

func TestClusterBackupsStatusPrintsTheNextPageCommand(t *testing.T) {
	mock := newClusterBackupsMock()
	mock.page = sampleClusterBackupsPage()
	mock.page.NextCursor = backupsTimePointer("c2hvcA")

	output, executeError := runClusterBackups(t, mock,
		"cluster", "backups", "status", "--cluster", "demo")
	if executeError != nil {
		t.Fatalf("reading the cluster's backups: %v", executeError)
	}
	if !strings.Contains(stripANSICodes(output), "ankra cluster backups status --cursor c2hvcA") {
		t.Errorf("expected the next page to be printed as a command, got:\n%s", output)
	}
}

// TestClusterBackupsStatusSaysUnknownWhenNothingMeasuredTheRestorePoint: the
// posture's LAST RESTORE POINT cell reads the same measured/unmeasured
// distinction as every other size surface. A capture whose engine published
// no byte count must not be reported as a backup holding nothing
// (ankra-0xsdd.76).
func TestClusterBackupsStatusSaysUnknownWhenNothingMeasuredTheRestorePoint(t *testing.T) {
	if got := describeRestorePointSize(0, false); got != "unknown" {
		t.Fatalf("an unmeasured restore point size is unknown, got %q", got)
	}
	if got := describeRestorePointSize(0, true); got != "0 B" {
		t.Fatalf("a measured empty restore point still renders its measurement, got %q", got)
	}
}
