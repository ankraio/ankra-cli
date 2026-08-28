package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func soleUpCloudCredential() []client.Credential {
	return []client.Credential{
		{ID: backupVaultProvisionCredentialID, Name: "upcloud-main", Provider: "upcloud", Available: true},
		// A credential Ankra cannot provision from must not count towards
		// the choice, so this one is ignored rather than ambiguous.
		{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Name: "github-main", Provider: "github", Available: true},
	}
}

// TestBackupVaultsProvisionNeedsNoArgumentsWithOneCredential pins the
// one-command path: no name, no credential, no region, and the command
// still knows what to do - and says so before it does it.
func TestBackupVaultsProvisionNeedsNoArgumentsWithOneCredential(t *testing.T) {
	mock := &backupVaultsMock{credentials: soleUpCloudCredential(), provisionedVault: provisioningVault("backups", "upcloud")}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "provision")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	expected := client.ProvisionBackupVaultRequest{
		Name: "backups", CredentialID: backupVaultProvisionCredentialID, Region: "europe-1",
	}
	if mock.provisioned == nil || *mock.provisioned != expected {
		t.Fatalf("request = %+v, want %+v", mock.provisioned, expected)
	}
	// Never silent about a default it chose.
	for _, fragment := range []string{"backups", "upcloud-main", "europe-1"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output missing %q:\n%s", fragment, output)
		}
	}
}

// The default name steps past the vaults that already exist, so the second
// vault also needs no argument.
func TestBackupVaultsProvisionDefaultNameStepsPastExistingVaults(t *testing.T) {
	existing := []client.BackupVault{
		{ID: backupVaultTestID, Name: "backups"},
		{ID: "e22a87f2-3ec4-41fe-a975-325661b7b7e2", Name: "backups-2"},
	}
	mock := &backupVaultsMock{
		credentials: soleUpCloudCredential(), vaults: existing,
		provisionedVault: provisioningVault("backups-3", "upcloud"),
	}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	if _, executeError := executeCommand("backup", "vaults", "provision"); executeError != nil {
		t.Fatal(executeError)
	}
	if mock.provisioned == nil || mock.provisioned.Name != "backups-3" {
		t.Fatalf("name = %+v, want backups-3", mock.provisioned)
	}
}

// With more than one usable credential the command asks instead of
// guessing: the dashboard shows its preselection before you press the
// button, a command cannot.
func TestBackupVaultsProvisionAsksWhichCredentialWhenSeveralQualify(t *testing.T) {
	mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("backups", "upcloud")}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	_, executeError := executeCommand("backup", "vaults", "provision")
	if executeError == nil || !strings.Contains(executeError.Error(), "pass --credential") {
		t.Fatalf("expected the ambiguity refusal, got %v", executeError)
	}
	if !strings.Contains(executeError.Error(), "upcloud-main") || !strings.Contains(executeError.Error(), "hetzner-main") {
		t.Fatalf("the refusal must name the candidates: %v", executeError)
	}
	if mock.provisioned != nil {
		t.Fatal("no request may be sent while the credential is ambiguous")
	}
}

func TestBackupVaultsProvisionExplainsAnOrganisationWithNoUsableCredential(t *testing.T) {
	mock := &backupVaultsMock{
		credentials:      []client.Credential{{ID: "x", Name: "github-main", Provider: "github"}},
		provisionedVault: provisioningVault("backups", "upcloud"),
	}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	_, executeError := executeCommand("backup", "vaults", "provision")
	if executeError == nil || !strings.Contains(executeError.Error(), "no Hetzner, UpCloud, DigitalOcean or Scaleway credential") {
		t.Fatalf("expected the no-credential explanation, got %v", executeError)
	}
	if !strings.Contains(executeError.Error(), "backup vaults create") {
		t.Fatalf("the explanation must offer the bring-your-own path: %v", executeError)
	}
}

// An explicit name, credential and region still win over every default.
func TestBackupVaultsProvisionExplicitArgumentsWin(t *testing.T) {
	mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("offsite", "upcloud")}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	if _, executeError := executeCommand("backup", "vaults", "provision", "offsite",
		"--credential", "upcloud-main", "--region", "us-1"); executeError != nil {
		t.Fatal(executeError)
	}
	expected := client.ProvisionBackupVaultRequest{
		Name: "offsite", CredentialID: backupVaultProvisionCredentialID, Region: "us-1",
	}
	if mock.provisioned == nil || *mock.provisioned != expected {
		t.Fatalf("request = %+v, want %+v", mock.provisioned, expected)
	}
}
