package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func provisionedVaultForDelete() *client.BackupVault {
	credentialID := backupVaultProvisionCredentialID
	return &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Kind: "ankra_provisioned", Provider: "upcloud",
		Endpoint: "https://9qk50.upcloudobjects.com", Region: "europe-1", Bucket: "ankra-offsite-0d9c1f9e",
		PathStyle: true, Status: "ready", ProvisionedViaCredentialID: &credentialID,
		CreatedAt: "2026-08-28T00:00:00Z", UpdatedAt: "2026-08-28T00:00:00Z",
	}
}

func resetBackupVaultsDeleteFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, name := range []string{"yes", "destroy-provider-resources"} {
			flag := backupVaultsDeleteCmd.Flags().Lookup(name)
			_ = flag.Value.Set("false")
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// TestBackupVaultsDeleteLeavesProviderResourcesByDefault pins the safe
// default: no teardown is requested, and the output says the bucket stayed.
func TestBackupVaultsDeleteLeavesProviderResourcesByDefault(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{*provisionedVaultForDelete()}, vault: provisionedVaultForDelete()}
	setMockClient(t, mock)
	resetBackupVaultsDeleteFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "delete", "offsite", "--yes")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	if mock.destroyRequested {
		t.Fatal("a plain delete must not ask for a teardown")
	}
	if !strings.Contains(output, "untouched") {
		t.Fatalf("output must say the bucket stayed:\n%s", output)
	}
}

// The forced delete names the bucket in the prompt before it destroys it -
// the vault name alone does not tell an operator what data is at stake.
func TestBackupVaultsDeleteWithDestroyNamesTheBucketAndRequests(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{*provisionedVaultForDelete()}, vault: provisionedVaultForDelete()}
	setMockClient(t, mock)
	resetBackupVaultsDeleteFlags(t)

	// No --yes here: the prompt itself is what must name the bucket. It is
	// written to the command's out writer, which executeCommand captures.
	rootCmd.SetIn(strings.NewReader("y\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	var executeError error
	var prompt string
	stdout := captureStdout(t, func() {
		prompt, executeError = executeCommand("backup", "vaults", "delete", "offsite", "--destroy-provider-resources")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	if !mock.destroyRequested {
		t.Fatal("the flag must reach the API call")
	}
	for _, fragment := range []string{"ankra-offsite-0d9c1f9e", "upcloud", "every restore point in it"} {
		if !strings.Contains(prompt, fragment) {
			t.Errorf("prompt missing %q:\n%s", fragment, prompt)
		}
	}
	if !strings.Contains(stdout, "being destroyed") {
		t.Errorf("output missing the teardown notice:\n%s", stdout)
	}
}

// A bring-your-own vault is refused client-side, so no destructive request
// is ever sent for a bucket Ankra did not create.
func TestBackupVaultsDeleteRefusesToDestroyABringYourOwnBucket(t *testing.T) {
	vault := provisionedVaultForDelete()
	vault.Kind = "customer_s3"
	vault.ProvisionedViaCredentialID = nil
	mock := &backupVaultsMock{vaults: []client.BackupVault{*vault}, vault: vault}
	setMockClient(t, mock)
	resetBackupVaultsDeleteFlags(t)

	_, executeError := executeCommand("backup", "vaults", "delete", "offsite", "--destroy-provider-resources", "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "registers a bucket you created") {
		t.Fatalf("expected the refusal, got %v", executeError)
	}
	if mock.deleteCalls != 0 {
		t.Fatal("no delete may be sent for a refused teardown")
	}
}
