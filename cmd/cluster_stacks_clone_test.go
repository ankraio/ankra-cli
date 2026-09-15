package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	cloneTestTargetClusterID = "7f1e2d3c-4b5a-4968-8877-665544332211"
	cloneTestRunID           = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	cloneTestRestorePointID  = "4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a"
)

// cloneLaneMock answers the clone route, the vault listing a --vault name is
// resolved against, and the run a --wait follows, recording what the command
// put on the wire.
type cloneLaneMock struct {
	baseMock
	source client.ClusterListItem
	target client.ClusterListItem

	cloned      *client.CloneStackToClusterRequest
	cloneResult *client.CloneStackToClusterResult
	cloneError  error

	runSequence []*client.Run
	runReads    int
}

func (mock *cloneLaneMock) GetCluster(name string) (client.ClusterListItem, error) {
	for _, cluster := range []client.ClusterListItem{mock.source, mock.target} {
		if cluster.Name == name || cluster.ID == name {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (mock *cloneLaneMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return mock.GetCluster(clusterID)
}

func (mock *cloneLaneMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     []client.ClusterListItem{mock.source, mock.target},
		Pagination: client.Pagination{TotalPages: 1, Page: 1},
	}, nil
}

func (mock *cloneLaneMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	return &client.BackupVaultListResult{Items: []client.BackupVault{
		{ID: backupTestVaultID, Name: "production-backups"},
	}}, nil
}

func (mock *cloneLaneMock) CloneStackToCluster(_ context.Context, _ string,
	request client.CloneStackToClusterRequest) (*client.CloneStackToClusterResult, error) {
	mock.cloned = &request
	if mock.cloneError != nil {
		return nil, mock.cloneError
	}
	return mock.cloneResult, nil
}

func (mock *cloneLaneMock) GetRun(string) (*client.Run, error) {
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

func newCloneLaneMock() *cloneLaneMock {
	return &cloneLaneMock{
		source: client.ClusterListItem{ID: testClusterID, Name: "demo"},
		target: client.ClusterListItem{ID: cloneTestTargetClusterID, Name: "staging"},
	}
}

func sampleCloneWithDataResult() *client.CloneStackToClusterResult {
	return &client.CloneStackToClusterResult{
		DraftID: "draft-1", StackName: backupTestStack,
		AddonsCloned: 2, ManifestsCloned: 1,
		DataCloneRunID:     stringPointer(cloneTestRunID),
		DataRestorePointID: stringPointer(cloneTestRestorePointID),
		DataAssets: []client.CloneDataAsset{{
			ID: "velero:shop/data", Kind: "persistentvolumeclaim", Engine: "velero",
			Namespace: backupTestStack, Name: "data",
		}},
		DataWarnings: []string{
			"shop/cache: This volume is not in the backup selection.",
			"The target cluster has no storage class 'premium'; the restored volumes land on 'standard' instead.",
		},
	}
}

func runCloneCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	return runBackupCommand(t, mock, "", []*cobra.Command{clusterStacksCloneCmd}, args...)
}

func TestStacksCloneWithDataSendsTheDataBlockAndNamesTheRun(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--vault", "production-backups")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	if mock.cloned == nil {
		t.Fatal("the clone never reached the client")
	}
	if !mock.cloned.IncludeData {
		t.Error("--with-data must set include_data")
	}
	if mock.cloned.DataCloneMode != client.CloneDataModeFresh {
		t.Errorf("the default mode must be fresh, got %q", mock.cloned.DataCloneMode)
	}
	if mock.cloned.BackupVaultID != backupTestVaultID {
		t.Errorf("the vault name must be resolved to its id, got %q", mock.cloned.BackupVaultID)
	}
	if mock.cloned.SourceClusterID != testClusterID {
		t.Errorf("the source cluster must be the active one, got %q", mock.cloned.SourceClusterID)
	}
	plain := stripANSICodes(output)
	if !strings.Contains(plain, cloneTestRunID) || !strings.Contains(plain, cloneTestRestorePointID) {
		t.Errorf("the run and the restore point must be named, got:\n%s", plain)
	}
	if !strings.Contains(plain, "shop/cache") ||
		!strings.Contains(plain, "no storage class 'premium'") {
		t.Errorf("every data warning must be relayed verbatim, got:\n%s", plain)
	}
	if !strings.Contains(plain, "ankra runs get "+cloneTestRunID) {
		t.Errorf("the command must say how to watch the run, got:\n%s", plain)
	}
}

func TestStacksCloneWithDataLatestSendsTheMode(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--from", "latest")

	if executeError != nil {
		t.Fatalf("cloning from the latest restore point: %v", executeError)
	}
	if mock.cloned.DataCloneMode != client.CloneDataModeLatest {
		t.Fatalf("--from latest must reach the platform, got %q", mock.cloned.DataCloneMode)
	}
}

// A clone without --with-data must put exactly the bytes on the wire it did
// before the data lane existed.
func TestStacksCloneWithoutDataCarriesNoDataBlock(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = &client.CloneStackToClusterResult{
		DraftID: "draft-1", StackName: backupTestStack, AddonsCloned: 1,
	}

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo", "--to", "staging")

	if executeError != nil {
		t.Fatalf("cloning configuration only: %v", executeError)
	}
	if mock.cloned.IncludeData || mock.cloned.DataCloneMode != "" ||
		mock.cloned.BackupVaultID != "" || mock.cloned.DataSelection != nil ||
		mock.cloned.ProtectSource {
		t.Fatalf("a configuration clone must carry no data block, got %+v", mock.cloned)
	}
	if strings.Contains(output, "Data:") {
		t.Errorf("a configuration clone has no data section, got:\n%s", output)
	}
}

// A data flag with no --with-data beside it must be refused, not ignored: a
// --vault that quietly did nothing would send somebody away believing they
// had chosen where the data went.
func TestStacksCloneRefusesDataFlagsWithoutWithData(t *testing.T) {
	for _, flag := range []struct{ name, value string }{
		{"--vault", "production-backups"},
		{"--from", "latest"},
		{"--include-pvc", "shop/data"},
		{"--protect-source", ""},
		{"--idempotency-key", "abc"},
	} {
		t.Run(flag.name, func(t *testing.T) {
			mock := newCloneLaneMock()
			arguments := []string{"cluster", "stacks", "clone", backupTestStack,
				"--cluster", "demo", "--to", "staging", flag.name}
			if flag.value != "" {
				arguments = append(arguments, flag.value)
			}

			_, executeError := runCloneCommand(t, mock, arguments...)

			if executeError == nil {
				t.Fatalf("%s must need --with-data", flag.name)
			}
			if exitCodeFor(executeError) != exitUsage {
				t.Errorf("expected a usage exit, got %d", exitCodeFor(executeError))
			}
			if mock.cloned != nil {
				t.Error("nothing may reach the platform when the invocation is refused")
			}
		})
	}
}

func TestStacksCloneRefusesWaitWithoutWithData(t *testing.T) {
	mock := newCloneLaneMock()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo", "--to", "staging", "--wait")

	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("--wait on a configuration clone must be a usage error, got %v", executeError)
	}
	if mock.cloned != nil {
		t.Error("nothing may reach the platform when the invocation is refused")
	}
}

func TestStacksCloneRefusesAnUnknownDataMode(t *testing.T) {
	mock := newCloneLaneMock()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--from", "yesterday")

	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("an unknown --from must be a usage error, got %v", executeError)
	}
	if !strings.Contains(executeError.Error(), "fresh") ||
		!strings.Contains(executeError.Error(), "latest") {
		t.Errorf("the refusal must name both modes, got: %v", executeError)
	}
}

func TestStacksCloneRefusesExcludingDatabasesWithoutConfirmation(t *testing.T) {
	mock := newCloneLaneMock()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--exclude-databases")

	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("excluding databases must need the acknowledgement, got %v", executeError)
	}
	if mock.cloned != nil {
		t.Error("nothing may reach the platform when the invocation is refused")
	}
}

// Absent and empty are different answers: a clone with no selection flags
// must send no selection, so the platform falls back to the stack's stored
// one rather than being told to carry nothing.
func TestStacksCloneSendsNoSelectionWhenNoneWasAsked(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	if mock.cloned.DataSelection != nil {
		t.Fatalf("an untouched selection must be omitted, got %+v", mock.cloned.DataSelection)
	}
	if mock.cloned.BackupVaultID != "" {
		t.Fatalf("an unnamed vault must be left to the platform, got %q", mock.cloned.BackupVaultID)
	}
}

// Naming a volume is not a decision about databases: sending an explicit
// `databases: true` would silently re-include the databases of a stack whose
// stored policy excludes them.
func TestStacksCloneLeavesDatabasesUndecidedWhenOnlyVolumesAreNamed(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--include-pvc", "shop/data")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	selection := mock.cloned.DataSelection
	if selection == nil || len(selection.PersistentVolumeClaims) != 1 ||
		selection.PersistentVolumeClaims[0] != "shop/data" {
		t.Fatalf("the named volume must reach the selection, got %+v", selection)
	}
	if selection.Databases != nil {
		t.Fatalf("naming a volume must not decide the databases, got %v", *selection.Databases)
	}
}

func TestStacksCloneExcludesDatabasesExplicitlyWhenConfirmed(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--exclude-databases", "--confirm-exclude-databases")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	selection := mock.cloned.DataSelection
	if selection == nil || selection.Databases == nil || *selection.Databases {
		t.Fatalf("the exclusion must be explicit, got %+v", selection)
	}
}

func TestStacksCloneProtectSourceAndDeployReachThePlatform(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--protect-source", "--deploy",
		"--idempotency-key", "clone-shop-1")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	if !mock.cloned.ProtectSource {
		t.Error("--protect-source must reach the platform")
	}
	if !mock.cloned.DeployAfterClone {
		t.Error("--deploy must reach the platform")
	}
	if mock.cloned.IdempotencyKey != "clone-shop-1" {
		t.Errorf("--idempotency-key must reach the client, got %q", mock.cloned.IdempotencyKey)
	}
}

func TestStacksCloneWaitFollowsTheRunToSuccess(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()
	mock.runSequence = []*client.Run{
		{ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusRunning},
		{ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusSucceeded},
	}
	withFastRunPolling(t)

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--wait")

	if executeError != nil {
		t.Fatalf("waiting for the clone: %v", executeError)
	}
	if mock.runReads < 2 {
		t.Fatalf("--wait must poll the run, got %d reads", mock.runReads)
	}
	if !strings.Contains(stripANSICodes(output), "data has been restored onto the target") {
		t.Fatalf("a succeeded run must be reported, got:\n%s", output)
	}
}

// A clone onto a cluster whose agent has never connected parks by design, so
// the command reports the platform's own sentence and exits zero rather than
// waiting out its whole timeout on a state nothing will leave on its own.
func TestStacksCloneWaitStopsOnBlockedAndRelaysTheReasonVerbatim(t *testing.T) {
	blockedReason := "restore starts when the new cluster connects"
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()
	mock.runSequence = []*client.Run{{
		ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusBlocked,
		DataRun: &client.DataRun{ID: cloneTestRunID, Phase: "restore",
			BlockedReason: stringPointer(blockedReason)},
	}}
	withFastRunPolling(t)

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--wait")

	if executeError != nil {
		t.Fatalf("a blocked clone is not a failure, got %v", executeError)
	}
	if !strings.Contains(stripANSICodes(output), blockedReason) {
		t.Fatalf("the blocked reason must be relayed verbatim, got:\n%s", output)
	}
}

func TestStacksCloneWaitReportsAwaitingDeploy(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()
	mock.runSequence = []*client.Run{{
		ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusBlocked,
		DataRun: &client.DataRun{ID: cloneTestRunID, BlockedReason: stringPointer("awaiting_deploy")},
	}}
	withFastRunPolling(t)

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--deploy", "--wait")

	if executeError != nil {
		t.Fatalf("a clone parked on awaiting_deploy is not a failure, got %v", executeError)
	}
	if !strings.Contains(stripANSICodes(output), "awaiting_deploy") {
		t.Fatalf("the parked reason must be shown, got:\n%s", output)
	}
}

func TestStacksCloneWaitFailsWhenTheRunFails(t *testing.T) {
	excerpt := "the target agent could not reach the vault"
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()
	mock.runSequence = []*client.Run{{
		ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusFailed,
		ErrorExcerpt: &excerpt,
	}}
	withFastRunPolling(t)

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--wait")

	if executeError == nil {
		t.Fatal("a failed run must fail the command")
	}
	if !strings.Contains(executeError.Error(), excerpt) {
		t.Fatalf("the platform's own reason must survive, got: %v", executeError)
	}
}

// Every refusal the data half raises is the platform's sentence, and the
// command's job is to relay it rather than paraphrase it.
func TestStacksCloneRelaysEveryRefusalVerbatim(t *testing.T) {
	refusals := []struct {
		name       string
		statusCode int
		detail     string
	}{
		{"D8 name taken", 409,
			"The target cluster already has a stack with this name, and a clone carrying data keeps the names."},
		{"D9 cross organisation", 409,
			"A clone into another organisation cannot carry data: the vault, its credential and the audit trail belong to one organisation."},
		{"D10 name parity", 409,
			"Restoring volume data requires the target to keep the source stack, Helm release and namespace names."},
		{"playground quota", 409,
			"This playground does not have room for the data this clone would restore."},
		{"agent floor", 409,
			"The target cluster's agent cannot restore data yet: it does not advertise restore_stack_data."},
		{"empty selection", 422,
			"This selection covers none of the stack's data."},
		{"backups permission", 403,
			"You need the backups.operate permission to clone a stack with its data."},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			mock := newCloneLaneMock()
			mock.cloneError = client.NewUnexpectedResponseError(refusal.statusCode, refusal.detail)

			_, executeError := runCloneCommand(t, mock,
				"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
				"--to", "staging", "--with-data")

			if executeError == nil {
				t.Fatal("a refused clone must fail the command")
			}
			if !strings.Contains(executeError.Error(), refusal.detail) {
				t.Fatalf("the platform's sentence must survive, got: %v", executeError)
			}
		})
	}
}

// The dark feature flag is the one refusal with something better to say than
// the raw detail: the fix is organisational, not a permission or a typo.
func TestStacksCloneExplainsTheFeatureFlagRefusal(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneError = client.NewUnexpectedResponseError(403, backupsNotEnabledDetail)

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data")

	if executeError == nil {
		t.Fatal("the dark lane must fail the command")
	}
	if !strings.Contains(executeError.Error(), backupsNotEnabledDetail) ||
		!strings.Contains(executeError.Error(), "ankra org current") {
		t.Fatalf("the refusal must carry the platform's sentence and the hint, got: %v", executeError)
	}
}

// A platform that accepted the clone and named no run carried no data: the
// target holds a configuration copy nobody asked for, and saying "cloned"
// there would be the silence this lane exists to remove.
func TestStacksCloneReportsAPlatformThatCarriedNoData(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = &client.CloneStackToClusterResult{DraftID: "draft-1", StackName: backupTestStack}

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data")

	if executeError == nil {
		t.Fatal("a clone that carried no data must not report success")
	}
	if !strings.Contains(executeError.Error(), "carried no data") {
		t.Fatalf("the reason must say the data did not travel, got: %v", executeError)
	}
}

func TestStacksCloneStructuredOutputCarriesTheDataFields(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "-o", "json")

	if executeError != nil {
		t.Fatalf("cloning with data: %v", executeError)
	}
	var decoded struct {
		DraftID            string `json:"draft_id"`
		DataCloneRunID     string `json:"data_clone_run_id"`
		DataRestorePointID string `json:"data_restore_point_id"`
		DataAssets         []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"data_assets"`
		DataWarnings []string `json:"data_warnings"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json must stay parseable: %v\n%s", decodeError, output)
	}
	if decoded.DataCloneRunID != cloneTestRunID || decoded.DataRestorePointID != cloneTestRestorePointID {
		t.Errorf("the data identifiers must be carried, got %+v", decoded)
	}
	if len(decoded.DataAssets) != 1 || decoded.DataAssets[0].ID != "velero:shop/data" {
		t.Errorf("the data assets must be carried, got %+v", decoded.DataAssets)
	}
	if len(decoded.DataWarnings) != 2 {
		t.Errorf("every data warning must be carried, got %+v", decoded.DataWarnings)
	}
}

func TestStacksCloneStructuredWaitCarriesTheCloneAndTheRun(t *testing.T) {
	mock := newCloneLaneMock()
	mock.cloneResult = sampleCloneWithDataResult()
	mock.runSequence = []*client.Run{{
		ID: cloneTestRunID, Kind: client.RunKindClone, Status: client.RunStatusSucceeded,
	}}
	withFastRunPolling(t)

	output, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "--wait", "-o", "json")

	if executeError != nil {
		t.Fatalf("waiting for the clone: %v", executeError)
	}
	structured := output[strings.Index(output, "{"):]
	var decoded struct {
		Clone struct {
			DataCloneRunID string `json:"data_clone_run_id"`
		} `json:"clone"`
		Run struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if decodeError := json.Unmarshal([]byte(structured), &decoded); decodeError != nil {
		t.Fatalf("-o json must stay parseable after --wait: %v\n%s", decodeError, output)
	}
	if decoded.Clone.DataCloneRunID != cloneTestRunID || decoded.Run.Status != client.RunStatusSucceeded {
		t.Fatalf("both halves must be carried, got %+v", decoded)
	}
}

// -o is validated before anything is dispatched, so a mistyped format never
// costs a restore point.
func TestStacksCloneChecksTheOutputFormatBeforeDispatching(t *testing.T) {
	mock := newCloneLaneMock()

	_, executeError := runCloneCommand(t, mock,
		"cluster", "stacks", "clone", backupTestStack, "--cluster", "demo",
		"--to", "staging", "--with-data", "-o", "toml")

	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("an unknown output format must be a usage error, got %v", executeError)
	}
	if mock.cloned != nil {
		t.Error("nothing may reach the platform when the invocation is refused")
	}
}
