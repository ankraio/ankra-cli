package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// `ankra backup vaults contents` is the instrument the fourth live
// verification pass did not have (docs/runbooks/backups-verification-2026-09-16-fourth-pass.md
// in the cluster repo). The numbers below are the ones it measured by hand
// after the platform reported a successful delete of a restore point whose
// nine objects were all still in the bucket.

const contentsVaultID = "61688482-0000-4000-8000-000000000001"

type backupVaultContentsMock struct {
	baseMock
	vaults          []client.BackupVault
	contents        *client.BackupVaultContents
	contentsError   error
	requestReceived client.BackupVaultContentsRequest
	vaultIDReceived string
}

func (mock *backupVaultContentsMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	return &client.BackupVaultListResult{Items: mock.vaults}, nil
}

func (mock *backupVaultContentsMock) GetBackupVaultContents(vaultID string,
	request client.BackupVaultContentsRequest) (*client.BackupVaultContents, error) {
	mock.vaultIDReceived = vaultID
	mock.requestReceived = request
	if mock.contentsError != nil {
		return nil, mock.contentsError
	}
	return mock.contents, nil
}

func resetContentsFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		flags := backupVaultsContentsCmd.Flags()
		_ = flags.Set("prefix", "")
		_ = flags.Set("restore-point", "")
		_ = flags.Set("orphans-only", "false")
		_ = flags.Set("objects", "false")
		flags.VisitAll(func(flag *pflag.Flag) { flag.Changed = false })
	}
	reset()
	t.Cleanup(reset)
}

func sealedVaultContents() *client.BackupVaultContents {
	return &client.BackupVaultContents{
		VaultID: contentsVaultID, VaultName: "backups-verify",
		Bucket: "ankra-backups-verify-61688482", Endpoint: "https://fra1.digitaloceanspaces.com",
		Complete: true, OrphansDetermined: true, ObjectCount: 5, TotalBytes: 31414490,
		RestorePoints: []client.VaultRestorePointContents{{
			RestorePointID: "d800471b-c68d-4ac5-bf64-f4ff064a1605",
			Status:         "complete",
			StackNames:     []string{"notes"},
			CreatedAt:      time.Date(2026, 9, 16, 0, 33, 0, 0, time.UTC),

			DeclaredObjectPrefix: "restore-points/d800471b-c68d-4ac5-bf64-f4ff064a1605",
			DeclaredPrefixUsage: client.VaultPrefixUsage{
				Prefix: "restore-points/d800471b-c68d-4ac5-bf64-f4ff064a1605",
			},
			LocatedPrefixes: []client.VaultPrefixUsage{{
				Prefix:      "clusters/53878fde/backups/ankra-rp-d800471b/",
				ObjectCount: 3, TotalBytes: 73391,
			}},
			ObjectCount: 3, TotalBytes: 73391,
			SharedRepositories: []string{"clusters/53878fde/kopia/notes/"},
			RecordedTotalBytes: 0,
			Notes: []string{
				"Nothing is stored under the prefix this restore point records; a delete that " +
					"sweeps the recorded prefix removes nothing.",
			},
		}},
		SharedRepositories: []client.VaultSharedRepository{{
			VaultPrefixUsage: client.VaultPrefixUsage{
				Prefix: "clusters/53878fde/kopia/notes/", ObjectCount: 2, TotalBytes: 31341099,
			},
			ClusterID: "53878fde", Namespace: "notes",
			ReferencedByRestorePoints: []string{"d800471b-c68d-4ac5-bf64-f4ff064a1605"},
		}},
		Orphans: []client.VaultOrphanGroup{},
		Other:   []client.VaultPrefixUsage{},
	}
}

// The listing has to show both prefixes, because the gap between them is the
// defect: the platform sweeps the declared one and the bytes are in the other.
func TestBackupVaultsContentsShowsDeclaredAndLocatedPrefixes(t *testing.T) {
	mock := &backupVaultContentsMock{
		vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contents: sealedVaultContents(),
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID)
	})
	plain := stripANSICodes(stdoutOutput)

	for _, expected := range []string{
		"backups-verify",
		"ankra-backups-verify-61688482",
		"clusters/53878fde/backups/ankra-rp-d800471b/",
		"complete",
		"notes",
		"removes nothing",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("expected the listing to contain %q, got:\n%s", expected, plain)
		}
	}
}

// The shared repository's bytes must be reported apart from the restore
// point's, and must never be summed into it.
func TestBackupVaultsContentsKeepsTheSharedRepositorySeparate(t *testing.T) {
	mock := &backupVaultContentsMock{
		vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contents: sealedVaultContents(),
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID)
	})
	plain := stripANSICodes(stdoutOutput)

	if !strings.Contains(plain, "Shared repositories") {
		t.Errorf("the shared repository section is missing, got:\n%s", plain)
	}
	if !strings.Contains(plain, "not attributable to one restore point") {
		t.Errorf("the listing does not say the shared bytes are unattributable, got:\n%s", plain)
	}
}

// The leak, rendered: a deleted restore point whose objects survived.
func TestBackupVaultsContentsRendersASurvivingDeletedRestorePoint(t *testing.T) {
	deletedAt := time.Date(2026, 9, 16, 1, 15, 0, 0, time.UTC)
	contents := sealedVaultContents()
	contents.RestorePoints = nil
	contents.Orphans = []client.VaultOrphanGroup{{
		VaultPrefixUsage: client.VaultPrefixUsage{
			Prefix: "clusters/53878fde/backups/ankra-rp-d800471b/", ObjectCount: 9, TotalBytes: 36361320,
		},
		Kind:           "deleted_restore_point",
		RestorePointID: "d800471b-c68d-4ac5-bf64-f4ff064a1605",
		RowDeletedAt:   &deletedAt,
		Reason: "The restore point was deleted from Ankra, but its objects are still in the vault.",
	}}

	mock := &backupVaultContentsMock{
		vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contents: contents,
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID)
	})
	plain := stripANSICodes(stdoutOutput)

	for _, expected := range []string{
		"Orphans", "deleted_restore_point", "still in the vault",
		"d800471b-c68d-4ac5-bf64-f4ff064a1605",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("expected the orphan report to contain %q, got:\n%s", expected, plain)
		}
	}
}

// A vault with nothing in it says so, and that claim is only made when the
// listing was complete.
func TestBackupVaultsContentsDistinguishesNoOrphansFromNotDetermined(t *testing.T) {
	t.Run("a complete listing states there are none", func(t *testing.T) {
		contents := sealedVaultContents()
		mock := &backupVaultContentsMock{
			vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
			contents: contents,
		}
		setMockClient(t, mock)
		resetContentsFlags(t)

		stdoutOutput := captureStdout(t, func() {
			_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID)
		})

		if !strings.Contains(stripANSICodes(stdoutOutput), "Every object in this vault is accounted for") {
			t.Errorf("a complete listing with no orphans must say so, got:\n%s", stdoutOutput)
		}
	})

	t.Run("a partial listing refuses to claim there are none", func(t *testing.T) {
		contents := sealedVaultContents()
		contents.Complete = false
		contents.OrphansDetermined = false
		contents.Warnings = []string{"The vault holds more than 50000 objects, so this listing stopped short."}
		mock := &backupVaultContentsMock{
			vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
			contents: contents,
		}
		setMockClient(t, mock)
		resetContentsFlags(t)

		stdoutOutput := captureStdout(t, func() {
			_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID)
		})
		plain := stripANSICodes(stdoutOutput)

		if strings.Contains(plain, "Every object in this vault is accounted for") {
			t.Errorf("a partial listing claimed there were no orphans, got:\n%s", plain)
		}
		if !strings.Contains(plain, "not determined") || !strings.Contains(plain, "stopped short") {
			t.Errorf("a partial listing must say so, got:\n%s", plain)
		}
	})
}

// An unreadable vault must fail, not print an empty listing. This is the
// conflation the whole verb exists to remove.
func TestBackupVaultsContentsFailsWhenTheVaultCannotBeRead(t *testing.T) {
	mock := &backupVaultContentsMock{
		vaults: []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contentsError: errors.New(
			"The vault's bucket could not be read, so its contents are unknown - this is NOT a report that the vault is empty."),
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	_, executeError := executeCommand("backup", "vaults", "contents", contentsVaultID)

	if executeError == nil {
		t.Fatal("an unreadable vault exited zero")
	}
	if !strings.Contains(executeError.Error(), "contents are unknown") {
		t.Errorf("the error does not say the contents are unknown: %v", executeError)
	}
}

func TestBackupVaultsContentsPassesItsNarrowingFlagsThrough(t *testing.T) {
	mock := &backupVaultContentsMock{
		vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contents: sealedVaultContents(),
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	_ = captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "contents", contentsVaultID,
			"--prefix", "clusters/53878fde/", "--restore-point", "d800471b", "--objects")
	})

	if mock.requestReceived.Prefix != "clusters/53878fde/" {
		t.Errorf("prefix = %q", mock.requestReceived.Prefix)
	}
	if mock.requestReceived.RestorePointID != "d800471b" {
		t.Errorf("restore point = %q", mock.requestReceived.RestorePointID)
	}
	if !mock.requestReceived.IncludeObjects {
		t.Error("--objects did not reach the request")
	}
}

// A vault name is what `backup vaults list` prints, so it must work here too.
func TestBackupVaultsContentsResolvesAVaultName(t *testing.T) {
	mock := &backupVaultContentsMock{
		vaults:   []client.BackupVault{{ID: contentsVaultID, Name: "backups-verify"}},
		contents: sealedVaultContents(),
	}
	setMockClient(t, mock)
	resetContentsFlags(t)

	_ = captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "contents", "backups-verify")
	})

	if mock.vaultIDReceived != contentsVaultID {
		t.Errorf("vault id = %q, want the resolved id %q", mock.vaultIDReceived, contentsVaultID)
	}
}
