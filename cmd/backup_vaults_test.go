package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// backupVaultsMock answers the backup vault surface and records what the
// commands sent, so the tests can assert the wire-facing behaviour without
// a server.
type backupVaultsMock struct {
	baseMock
	vaults           []client.BackupVault
	listError        error
	listCalls        int
	vault            *client.BackupVault
	created          *client.CreateBackupVaultRequest
	credentials      []client.Credential
	provisioned      *client.ProvisionBackupVaultRequest
	provisionedVault *client.BackupVault
	provisionError   error
	createdVault     *client.BackupVault
	verifiedVault    *client.BackupVault
	deletedID        string
	destroyRequested bool
	deleteCalls      int
}

func (mock *backupVaultsMock) ListBackupVaults() (*client.BackupVaultListResult, error) {
	mock.listCalls++
	if mock.listError != nil {
		return nil, mock.listError
	}
	return &client.BackupVaultListResult{Items: mock.vaults}, nil
}

func (mock *backupVaultsMock) GetBackupVault(vaultID string) (*client.BackupVault, error) {
	if mock.vault == nil || mock.vault.ID != vaultID {
		return nil, errors.New("Backup vault not found.")
	}
	return mock.vault, nil
}

func (mock *backupVaultsMock) ListCredentials(_ *string) ([]client.Credential, error) {
	return mock.credentials, nil
}

func (mock *backupVaultsMock) ProvisionBackupVault(request client.ProvisionBackupVaultRequest) (*client.BackupVault, error) {
	mock.provisioned = &request
	if mock.provisionError != nil {
		return nil, mock.provisionError
	}
	return mock.provisionedVault, nil
}

func (mock *backupVaultsMock) CreateBackupVault(request client.CreateBackupVaultRequest) (*client.BackupVault, error) {
	mock.created = &request
	return mock.createdVault, nil
}

func (mock *backupVaultsMock) VerifyBackupVault(vaultID string) (*client.BackupVault, error) {
	if mock.verifiedVault == nil {
		return nil, errors.New("Backup vault not found.")
	}
	return mock.verifiedVault, nil
}

func (mock *backupVaultsMock) DeleteBackupVault(vaultID string, destroyProviderResources bool) error {
	mock.deleteCalls++
	mock.deletedID = vaultID
	mock.destroyRequested = destroyProviderResources
	return nil
}

// resetBackupVaultsFlags restores the backup vault commands' flag values and
// Changed markers before and after each test: the cobra tree is shared across
// the test binary, so an earlier invocation's flags would otherwise leak.
func resetBackupVaultsFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		createFlags := backupVaultsCreateCmd.Flags()
		for name, value := range map[string]string{
			"endpoint":          "",
			"bucket":            "",
			"provider":          "other",
			"region":            "",
			"path-style":        "true",
			"access-key-id":     "",
			"secret-access-key": "",
		} {
			_ = createFlags.Set(name, value)
			createFlags.Lookup(name).Changed = false
		}
		_ = backupVaultsDeleteCmd.Flags().Set("yes", "false")
		backupVaultsDeleteCmd.Flags().Lookup("yes").Changed = false
		_ = backupVaultsListCmd.Flags().Set("output", "")
		backupVaultsListCmd.Flags().Lookup("output").Changed = false
		_ = backupVaultsGetCmd.Flags().Set("output", "")
		backupVaultsGetCmd.Flags().Lookup("output").Changed = false
	}
	reset()
	t.Cleanup(reset)
}

const backupVaultTestID = "0d9c1f9e-5f2a-4c53-9d3e-6a1f2b3c4d5e"

func TestResolveBackupVaultIDPassesAUUIDStraightThrough(t *testing.T) {
	mock := &backupVaultsMock{listError: errors.New("listing must not be called for a uuid")}

	resolved, resolveError := resolveBackupVaultID(mock, backupVaultTestID)

	if resolveError != nil {
		t.Fatalf("a uuid must resolve to itself: %v", resolveError)
	}
	if resolved != backupVaultTestID {
		t.Fatalf("expected %q, got %q", backupVaultTestID, resolved)
	}
	if mock.listCalls != 0 {
		t.Fatal("resolving a uuid must not cost a listing round-trip")
	}
}

func TestResolveBackupVaultIDResolvesAName(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{
		{ID: "aaaaaaaa-1111-4111-8111-111111111111", Name: "minio-lab"},
		{ID: backupVaultTestID, Name: "offsite"},
	}}

	resolved, resolveError := resolveBackupVaultID(mock, "offsite")

	if resolveError != nil {
		t.Fatalf("resolving a name printed by `list`: %v", resolveError)
	}
	if resolved != backupVaultTestID {
		t.Fatalf("resolved to the wrong vault: %q", resolved)
	}
}

func TestResolveBackupVaultIDExplainsAnUnknownName(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{{ID: "id-1", Name: "offsite"}}}

	_, resolveError := resolveBackupVaultID(mock, "offsit")

	if resolveError == nil {
		t.Fatal("an unknown name must be reported, not passed to the API as a bogus uuid")
	}
	if !strings.Contains(resolveError.Error(), "backup vaults list") {
		t.Fatalf("the error must point at how to find the right name, got: %v", resolveError)
	}
}

func TestResolveBackupVaultIDReportsDuplicateNames(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{
		{ID: "aaaaaaaa-1111-4111-8111-111111111111", Name: "offsite"},
		{ID: "bbbbbbbb-2222-4222-8222-222222222222", Name: "offsite"},
	}}

	_, resolveError := resolveBackupVaultID(mock, "offsite")

	if resolveError == nil {
		t.Fatal("duplicate names must be reported so the user passes an id")
	}
	if !strings.Contains(resolveError.Error(), "aaaaaaaa-1111-4111-8111-111111111111") ||
		!strings.Contains(resolveError.Error(), "bbbbbbbb-2222-4222-8222-222222222222") {
		t.Fatalf("the error must list the candidate ids, got: %v", resolveError)
	}
}

// A name can only be resolved by listing; passing it through to a
// uuid-typed path guaranteed a validation error that read as "you typed the
// name wrong" for a name that was right. Report the real cause instead.
func TestResolveBackupVaultIDReportsWhyTheLookupFailed(t *testing.T) {
	mock := &backupVaultsMock{listError: errors.New("listing unavailable")}

	resolved, resolveError := resolveBackupVaultID(mock, "offsite")

	if resolveError == nil {
		t.Fatal("a failed lookup must surface, not pass the name through")
	}
	if !strings.Contains(resolveError.Error(), "listing unavailable") ||
		!strings.Contains(resolveError.Error(), "offsite") {
		t.Fatalf("the error must name the vault and the cause: %v", resolveError)
	}
	if resolved != "" {
		t.Fatalf("no id may be returned alongside the error, got %q", resolved)
	}
}

// An id needs no lookup, so a broken listing never blocks it.
func TestResolveBackupVaultIDSkipsTheLookupForAnID(t *testing.T) {
	mock := &backupVaultsMock{listError: errors.New("listing unavailable")}

	resolved, resolveError := resolveBackupVaultID(mock, backupVaultTestID)

	if resolveError != nil {
		t.Fatalf("an id must not need the listing: %v", resolveError)
	}
	if resolved != backupVaultTestID {
		t.Fatalf("resolved = %q", resolved)
	}
}

// The recovery hint has to match how the vault was meant to get its bucket.
func TestBackupVaultVerifyHintMatchesTheVaultKind(t *testing.T) {
	verifiedAt := "2026-08-27T10:00:00Z"
	cases := map[string]struct {
		vault    client.BackupVault
		expected string
	}{
		"provisioning never finished": {
			vault:    client.BackupVault{Name: "offsite", Kind: "ankra_provisioned"},
			expected: "--destroy-provider-resources",
		},
		// The cause is not knowable from here - rotated keys, a deleted
		// bucket, or a failed teardown that put the row back - so the hint
		// must not name one. It used to say "fix the access keys", which a
		// failed teardown makes plainly wrong (ankra-0xsdd.41).
		"provisioned but verified before": {
			vault:    client.BackupVault{Name: "offsite", Kind: "ankra_provisioned", LastVerifiedAt: &verifiedAt},
			expected: "if a teardown left resources behind",
		},
		"bring your own": {
			vault:    client.BackupVault{Name: "offsite", Kind: "customer_s3"},
			expected: "fix the access keys",
		},
	}
	for name, testCase := range cases {
		hint := backupVaultVerifyHint(&testCase.vault)
		if !strings.Contains(hint, testCase.expected) {
			t.Errorf("%s: hint %q does not contain %q", name, hint, testCase.expected)
		}
	}

	// A vault Ankra provisioned and verified before must never be told its
	// keys are the problem: a failed teardown leaves them working.
	provisioned := client.BackupVault{Name: "offsite", Kind: "ankra_provisioned", LastVerifiedAt: &verifiedAt}
	if hint := backupVaultVerifyHint(&provisioned); strings.Contains(hint, "fix the access keys") {
		t.Errorf("hint still names the keys as the cause: %q", hint)
	}
}

func TestBackupVaultsListRendersEveryColumn(t *testing.T) {
	lastVerifiedAt := "2026-08-25T09:00:00Z"
	mock := &backupVaultsMock{vaults: []client.BackupVault{
		{ID: backupVaultTestID, Name: "offsite", Provider: "other",
			Endpoint: "https://s3.example.com", Bucket: "cluster-backups",
			Status: "ready", LastVerifiedAt: &lastVerifiedAt},
	}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "list")
	})

	for _, expected := range []string{
		"NAME", "PROVIDER", "BUCKET", "ENDPOINT", "STATUS", "LAST VERIFIED",
		"offsite", "other", "cluster-backups", "https://s3.example.com", "ready",
	} {
		if !strings.Contains(stripANSICodes(stdoutOutput), expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

func TestBackupVaultsListEmptySuggestsCreate(t *testing.T) {
	mock := &backupVaultsMock{}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "list")
	})

	if !strings.Contains(stdoutOutput, "No backup vaults found") ||
		!strings.Contains(stdoutOutput, "ankra backup vaults create") {
		t.Fatalf("an empty listing must point at create, got: %s", stdoutOutput)
	}
}

func TestBackupVaultsGetPrintsErrorExcerptAndRecoveryHint(t *testing.T) {
	excerpt := "SignatureDoesNotMatch: the request signature we calculated does not match"
	mock := &backupVaultsMock{vault: &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Provider: "other",
		Endpoint: "https://s3.example.com", Bucket: "cluster-backups",
		Status: "error", ErrorExcerpt: &excerpt,
	}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "get", backupVaultTestID)
	})

	if !strings.Contains(stdoutOutput, "SignatureDoesNotMatch") {
		t.Errorf("expected the error excerpt in the output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ankra backup vaults verify offsite") {
		t.Errorf("expected the verify recovery hint, got: %s", stdoutOutput)
	}
}

func TestBackupVaultsGetResolvesAName(t *testing.T) {
	mock := &backupVaultsMock{
		vaults: []client.BackupVault{{ID: backupVaultTestID, Name: "offsite"}},
		vault: &client.BackupVault{
			ID: backupVaultTestID, Name: "offsite", Provider: "other", Status: "ready"},
	}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("backup", "vaults", "get", "offsite")
	})

	if !strings.Contains(stdoutOutput, backupVaultTestID) {
		t.Fatalf("expected the vault resolved by name to be printed, got: %s", stdoutOutput)
	}
}

func TestBackupVaultsCreateSendsFlagsWithoutPrompting(t *testing.T) {
	mock := &backupVaultsMock{createdVault: &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Provider: "other", Status: "ready"}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	var executeError error
	stdoutOutput := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "create", "offsite",
			"--endpoint", "https://s3.example.com",
			"--bucket", "cluster-backups",
			"--region", "eu-north-1",
			"--path-style=false",
			"--access-key-id", "AKIA123",
			"--secret-access-key", "shhh")
	})

	if executeError != nil {
		t.Fatalf("create with a ready outcome must succeed: %v", executeError)
	}
	if mock.created == nil {
		t.Fatal("the create request never reached the client")
	}
	expected := client.CreateBackupVaultRequest{
		Name: "offsite", Provider: "other", Endpoint: "https://s3.example.com",
		Region: "eu-north-1", Bucket: "cluster-backups", PathStyle: false,
		AccessKeyID: "AKIA123", SecretAccessKey: "shhh",
	}
	if *mock.created != expected {
		t.Fatalf("request = %+v, want %+v", *mock.created, expected)
	}
	if !strings.Contains(stdoutOutput, "created") || !strings.Contains(stdoutOutput, "ready") {
		t.Errorf("expected a creation confirmation with the status, got: %s", stdoutOutput)
	}
}

func TestBackupVaultsCreateFailedVerificationExitsNonZero(t *testing.T) {
	excerpt := "AccessDenied: access denied"
	mock := &backupVaultsMock{createdVault: &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Status: "error", ErrorExcerpt: &excerpt}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	var executeError error
	captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "create", "offsite",
			"--endpoint", "https://s3.example.com",
			"--bucket", "cluster-backups",
			"--access-key-id", "AKIA123",
			"--secret-access-key", "wrong")
	})

	if executeError == nil {
		t.Fatal("a vault created in status error must exit non-zero")
	}
	if !strings.Contains(executeError.Error(), "AccessDenied") {
		t.Errorf("the error must carry the excerpt, got: %v", executeError)
	}
	if !strings.Contains(executeError.Error(), "ankra backup vaults verify offsite") {
		t.Errorf("the error must point at verify after fixing the keys, got: %v", executeError)
	}
}

func TestBackupVaultsVerifyPrintsTheNewStatus(t *testing.T) {
	mock := &backupVaultsMock{verifiedVault: &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Status: "ready"}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	var executeError error
	stdoutOutput := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "verify", backupVaultTestID)
	})

	if executeError != nil {
		t.Fatalf("verify with a ready outcome must succeed: %v", executeError)
	}
	if !strings.Contains(stdoutOutput, "ready") {
		t.Errorf("expected the refreshed status, got: %s", stdoutOutput)
	}
}

func TestBackupVaultsVerifyFailureExitsNonZero(t *testing.T) {
	excerpt := "connection refused"
	mock := &backupVaultsMock{verifiedVault: &client.BackupVault{
		ID: backupVaultTestID, Name: "offsite", Status: "error", ErrorExcerpt: &excerpt}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	var executeError error
	captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "verify", backupVaultTestID)
	})

	if executeError == nil {
		t.Fatal("a verification that leaves the vault in error must exit non-zero")
	}
	if !strings.Contains(executeError.Error(), "connection refused") {
		t.Errorf("the error must carry the excerpt, got: %v", executeError)
	}
}

func TestBackupVaultsDeleteDeclinedLeavesTheVault(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{
		{ID: backupVaultTestID, Name: "offsite"}}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)
	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	_, executeError := executeCommand("backup", "vaults", "delete", "offsite")

	if executeError == nil {
		t.Fatal("declining the confirmation must not succeed silently")
	}
	if got := exitCodeFor(executeError); got != exitCancelled {
		t.Errorf("declined confirmation should exit %d, got %d", exitCancelled, got)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("expected no delete call after a declined confirmation, got %d", mock.deleteCalls)
	}
}

func TestBackupVaultsDeleteYesSkipsThePrompt(t *testing.T) {
	mock := &backupVaultsMock{vaults: []client.BackupVault{
		{ID: backupVaultTestID, Name: "offsite"}}}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	var executeError error
	stdoutOutput := captureStdout(t, func() {
		_, executeError = executeCommand("backup", "vaults", "delete", "offsite", "--yes")
	})

	if executeError != nil {
		t.Fatalf("delete --yes must not prompt: %v", executeError)
	}
	if mock.deletedID != backupVaultTestID {
		t.Errorf("expected the resolved vault id to be deleted, got %q", mock.deletedID)
	}
	if !strings.Contains(stdoutOutput, "deleted") {
		t.Errorf("expected a deletion confirmation, got: %s", stdoutOutput)
	}
}
