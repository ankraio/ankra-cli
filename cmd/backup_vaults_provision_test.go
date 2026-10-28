package cmd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const backupVaultProvisionCredentialID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"

func provisionCredentials() []client.Credential {
	return []client.Credential{
		{ID: backupVaultProvisionCredentialID, Name: "upcloud-main", Provider: "upcloud", Available: true},
		{ID: "9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f", Name: "hetzner-main", Provider: "hetzner", Available: true},
		{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Name: "github-main", Provider: "github", Available: true},
	}
}

func provisioningVault(name string, provider string) *client.BackupVault {
	credentialID := backupVaultProvisionCredentialID
	return &client.BackupVault{
		ID: backupVaultTestID, Name: name, Kind: "ankra_provisioned", Provider: provider,
		Region: "europe-1", Bucket: "ankra-" + name + "-0d9c1f9e", PathStyle: true, Status: "provisioning",
		ProvisionedViaCredentialID: &credentialID, CreatedAt: "2026-08-28T00:00:00Z", UpdatedAt: "2026-08-28T00:00:00Z",
	}
}

func resetBackupVaultsProvisionFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, name := range []string{"credential", "region", "bucket", "access-key-id", "secret-access-key"} {
			flag := backupVaultsProvisionCmd.Flags().Lookup(name)
			_ = flag.Value.Set("")
			flag.Changed = false
		}
		waitFlag := backupVaultsProvisionCmd.Flags().Lookup("wait")
		_ = waitFlag.Value.Set("false")
		waitFlag.Changed = false
		timeoutFlag := backupVaultsProvisionCmd.Flags().Lookup("timeout")
		_ = timeoutFlag.Value.Set(defaultAsyncWriteTimeout.String())
		timeoutFlag.Changed = false
	}
	reset()
	t.Cleanup(reset)
}

// TestBackupVaultsProvisionResolvesTheCredentialByNameAndPostsIdsOnly pins
// the request shape: the credential id (resolved from its name), the
// region, no provider (the platform reads it off the credential) and no
// keys for a provider that mints its own.
func TestBackupVaultsProvisionResolvesTheCredentialByNameAndPostsIdsOnly(t *testing.T) {
	mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("offsite", "upcloud")}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "provision", "offsite",
			"--credential", "upcloud-main", "--region", "europe-1")
	})
	if executeError != nil {
		t.Fatalf("unexpected error: %v", executeError)
	}
	if mock.provisioned == nil {
		t.Fatal("expected a provision request")
	}
	expected := client.ProvisionBackupVaultRequest{Name: "offsite", CredentialID: backupVaultProvisionCredentialID, Region: "europe-1"}
	if *mock.provisioned != expected {
		t.Fatalf("request = %+v, want %+v", *mock.provisioned, expected)
	}
	for _, fragment := range []string{"is being provisioned on upcloud", "ankra backup vaults get offsite", "--wait",
		"Provisioned:   by Ankra via credential " + backupVaultProvisionCredentialID} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output missing %q:\n%s", fragment, output)
		}
	}
}

func TestBackupVaultsProvisionPassesHetznerKeysThroughWhenGiven(t *testing.T) {
	mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("hz", "hetzner")}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	if _, executeError := executeCommand("backup", "vaults", "provision", "hz",
		"--credential", "hetzner-main", "--region", "fsn1", "--bucket", "hz-backups",
		"--access-key-id", "HZKEY", "--secret-access-key", "HZSECRET"); executeError != nil {
		t.Fatalf("unexpected error: %v", executeError)
	}
	if mock.provisioned == nil || mock.provisioned.AccessKeyID != "HZKEY" || mock.provisioned.SecretAccessKey != "HZSECRET" ||
		mock.provisioned.Bucket != "hz-backups" || mock.provisioned.CredentialID != "9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f" {
		t.Fatalf("request = %+v", mock.provisioned)
	}
}

func TestBackupVaultsProvisionRefusesWhatThePlatformWouldRefuse(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
	}{
		{"unknown credential", []string{"--credential", "nope", "--region", "fsn1"}, `credential "nope" not found`},
		{"non-provisionable credential", []string{"--credential", "github-main", "--region", "fsn1"}, "ankra backup vaults create"},
		{"keys for a minting provider", []string{"--credential", "upcloud-main", "--region", "europe-1", "--access-key-id", "k", "--secret-access-key", "s"},
			"only used for hetzner credentials"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("x", "upcloud")}
			setMockClient(t, mock)
			resetBackupVaultsProvisionFlags(t)

			_, executeError := executeCommand(append([]string{"backup", "vaults", "provision", "x"}, testCase.args...)...)
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expected) {
				t.Fatalf("expected an error containing %q, got %v", testCase.expected, executeError)
			}
			if mock.provisioned != nil {
				t.Fatal("no request may be sent for a refused invocation")
			}
		})
	}
}

func TestBackupVaultsProvisionWaitReportsTheOutcome(t *testing.T) {
	previousInterval := backupVaultPollInterval
	backupVaultPollInterval = time.Millisecond
	t.Cleanup(func() { backupVaultPollInterval = previousInterval })

	ready := provisioningVault("offsite", "upcloud")
	ready.Status = "ready"
	ready.Endpoint = "https://9qk50.upcloudobjects.com"
	mock := &backupVaultsMock{credentials: provisionCredentials(), provisionedVault: provisioningVault("offsite", "upcloud"), vault: ready}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "provision", "offsite",
			"--credential", "upcloud-main", "--region", "europe-1", "--wait")
	})
	if executeError != nil {
		t.Fatalf("unexpected error: %v", executeError)
	}
	if !strings.Contains(output, "is ready") || !strings.Contains(output, "9qk50.upcloudobjects.com") {
		t.Fatalf("output:\n%s", output)
	}

	failed := provisioningVault("offsite", "upcloud")
	failed.Status = "error"
	excerpt := "creating the Managed Object Storage service: UpCloud answered 403: forbidden"
	failed.ErrorExcerpt = &excerpt
	mock.vault = failed
	resetBackupVaultsProvisionFlags(t)
	_, failedError := executeCommand("backup", "vaults", "provision", "offsite",
		"--credential", "upcloud-main", "--region", "europe-1", "--wait")
	if failedError == nil || !strings.Contains(failedError.Error(), "forbidden") {
		t.Fatalf("a failed provisioning must exit non-zero with the excerpt, got %v", failedError)
	}
}

func TestBackupVaultsProvisionExplainsTheDarkLane(t *testing.T) {
	mock := &backupVaultsMock{
		credentials:    provisionCredentials(),
		provisionError: client.NewUnexpectedResponseError(http.StatusForbidden, backupsNotEnabledDetail),
	}
	setMockClient(t, mock)
	resetBackupVaultsProvisionFlags(t)

	_, executeError := executeCommand("backup", "vaults", "provision", "offsite", "--credential", "upcloud-main", "--region", "europe-1")
	if executeError == nil || !strings.Contains(executeError.Error(), "ankra org current") {
		t.Fatalf("expected the dark-lane advice, got %v", executeError)
	}
}
