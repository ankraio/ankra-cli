package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/viper"
)

func TestEnsureSecureConfigFileCreatesWithRestrictedPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".ankra.yaml")

	if err := ensureSecureConfigFile(path); err != nil {
		t.Fatalf("ensureSecureConfigFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected mode 0600, got %#o", mode)
	}
}

func TestEnsureSecureConfigFileTightensLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".ankra.yaml")
	if err := os.WriteFile(path, []byte("token: leaked\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := ensureSecureConfigFile(path); err != nil {
		t.Fatalf("ensureSecureConfigFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected mode tightened to 0600, got %#o", mode)
	}
}

func TestEnsureTokenIssuedAcceptsToken(t *testing.T) {
	if err := ensureTokenIssued(tokenExchangeResponse{Token: "ankra-token"}); err != nil {
		t.Fatalf("ensureTokenIssued() error = %v", err)
	}
}

func TestEnsureTokenIssuedRejectsEmptyToken(t *testing.T) {
	err := ensureTokenIssued(tokenExchangeResponse{})
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "did not issue an API token") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEnsureTokenIssuedRejectsIncompleteMFA(t *testing.T) {
	err := ensureTokenIssued(tokenExchangeResponse{MfaRequired: true})
	if err == nil {
		t.Fatal("expected error for incomplete two-factor authentication")
	}
	if !strings.Contains(err.Error(), "two-factor authentication was not completed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func withShortLoginHTTPClient(t *testing.T) {
	t.Helper()
	restoreLoginHTTPClient := loginHTTPClient
	loginHTTPClient = &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		loginHTTPClient = restoreLoginHTTPClient
	})
}

func TestPollLoginOncePendingKeepsPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		var payload loginPollRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode poll body: %v", err)
		}
		if payload.Ticket != "ticket-1" || payload.CodeVerifier != "verifier-1" {
			t.Fatalf("unexpected poll payload: %+v", payload)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(tokenExchangeResponse{Status: "pending"})
	}))
	defer server.Close()
	withShortLoginHTTPClient(t)

	result, done, err := pollLoginOnce(server.URL,
		[]byte(`{"ticket":"ticket-1","code_verifier":"verifier-1"}`), map[string]bool{})
	if err != nil {
		t.Fatalf("pollLoginOnce() error = %v", err)
	}
	if done {
		t.Fatal("expected pending poll to remain incomplete")
	}
	if result.Token != "" {
		t.Fatalf("pending token = %q", result.Token)
	}
}

func TestPollLoginOnceCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(tokenExchangeResponse{
			Token:     "ankra-token",
			ExpiresAt: "2026-07-01T00:00:00Z",
			TokenID:   "token-id",
			TokenName: "CLI Login",
			Status:    "issued",
		})
	}))
	defer server.Close()
	withShortLoginHTTPClient(t)

	result, done, err := pollLoginOnce(server.URL, []byte(`{}`), map[string]bool{})
	if err != nil {
		t.Fatalf("pollLoginOnce() error = %v", err)
	}
	if !done {
		t.Fatal("expected completed poll")
	}
	if result.Token != "ankra-token" {
		t.Fatalf("token = %q", result.Token)
	}
	if result.TokenName != "CLI Login" {
		t.Fatalf("token name = %q", result.TokenName)
	}
}

// TestPollLoginOnceClientErrorIsFatal guards the fail-fast fix: 4xx answers
// (expired session, refused verifier, upgrade required) must surface the
// platform's message immediately instead of silently retrying for the full
// ten-minute window.
func TestPollLoginOnceClientErrorIsFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusGone)
		_, _ = responseWriter.Write([]byte(`{"detail":"Your login session expired. Please run ankra login again."}`))
	}))
	defer server.Close()
	withShortLoginHTTPClient(t)

	_, _, err := pollLoginOnce(server.URL, []byte(`{}`), map[string]bool{})
	if err == nil {
		t.Fatal("expected expired login session error")
	}
	if !strings.Contains(err.Error(), "Your login session expired") {
		t.Fatalf("expected the platform detail in the error, got: %v", err)
	}
}

func TestPollLoginOnceServerErrorKeepsPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	withShortLoginHTTPClient(t)

	_, done, err := pollLoginOnce(server.URL, []byte(`{}`), map[string]bool{})
	if err != nil {
		t.Fatalf("expected 5xx to keep polling, got error: %v", err)
	}
	if done {
		t.Fatal("expected 5xx poll to remain incomplete")
	}
}

func TestApiErrorDetailPrefersDetailField(t *testing.T) {
	if got := apiErrorDetail([]byte(`{"detail":"upgrade required"}`)); got != "upgrade required" {
		t.Fatalf("detail = %q", got)
	}
	if got := apiErrorDetail([]byte(`plain text error`)); got != "plain text error" {
		t.Fatalf("detail = %q", got)
	}
	if got := apiErrorDetail(nil); got != "the platform returned an error without details" {
		t.Fatalf("detail = %q", got)
	}
}

// TestGetOrCreateMachineIDDoesNotWriteConfig guards the credential-leak fix:
// getOrCreateMachineID must be read-only. With ANKRA_API_TOKEN bound into the
// global viper and no existing ~/.ankra.yaml, the old write-on-miss behavior
// created the config file 0644 containing the env token, which persisted if
// login then failed or was cancelled. The function must now derive the ID in
// memory and touch nothing on disk.
func TestGetOrCreateMachineIDDoesNotWriteConfig(t *testing.T) {
	withFreshViperAndEnv(t)
	home := withTempHome(t)
	t.Setenv("ANKRA_API_TOKEN", "secret-env-token")

	// Bind the env token into the global viper exactly as initConfig does, so
	// a stray WriteConfig would serialize it into the file.
	viper.AutomaticEnv()
	if err := viper.BindEnv("token", envAnkraAPIToken); err != nil {
		t.Fatalf("bind token env: %v", err)
	}

	configPath := filepath.Join(home, ".ankra.yaml")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: config file already exists (err=%v)", err)
	}

	machineID := getOrCreateMachineID()
	if machineID == "" {
		t.Fatal("expected a non-empty machine ID")
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("getOrCreateMachineID must not create %s (stat err=%v)", configPath, err)
	}
}

// TestGetOrCreateMachineIDStableForExistingID confirms a saved machine_id is
// returned verbatim so previously derived IDs stay stable across the fix.
func TestGetOrCreateMachineIDStableForExistingID(t *testing.T) {
	withFreshViperAndEnv(t)
	home := withTempHome(t)

	configPath := filepath.Join(home, ".ankra.yaml")
	if err := os.WriteFile(configPath, []byte("machine_id: saved-machine-id\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if got := getOrCreateMachineID(); got != "saved-machine-id" {
		t.Fatalf("machine ID = %q, want saved-machine-id", got)
	}
}

// TestLoginSaveBlockPersistsSecureConfig exercises the final save path from
// runLogin: after ensureSecureConfigFile creates the file 0600 and the token,
// base-url, and machine_id are written, the file must be 0600 and contain the
// machine_id. This mirrors the production save block (login.go) so the belt
// (SetConfigPermissions) and the moved machine_id persistence are covered.
func TestLoginSaveBlockPersistsSecureConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission semantics differ on Windows")
	}
	withFreshViperAndEnv(t)
	home := withTempHome(t)
	t.Setenv("ANKRA_API_TOKEN", "secret-env-token")

	viper.AutomaticEnv()
	if err := viper.BindEnv("token", envAnkraAPIToken); err != nil {
		t.Fatalf("bind token env: %v", err)
	}

	machineID := getOrCreateMachineID()

	configPath := filepath.Join(home, ".ankra.yaml")
	if err := ensureSecureConfigFile(configPath); err != nil {
		t.Fatalf("ensureSecureConfigFile: %v", err)
	}

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	_ = viper.ReadInConfig()

	viper.Set("token", "login-token")
	viper.Set("base-url", "https://platform.ankra.app")
	viper.Set("machine_id", machineID)
	viper.SetConfigPermissions(0o600)

	if err := viper.WriteConfig(); err != nil {
		if safeErr := viper.SafeWriteConfig(); safeErr != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected mode 0600, got %#o", mode)
	}

	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(contents), "machine_id: "+machineID) {
		t.Errorf("config missing persisted machine_id; got:\n%s", contents)
	}
}

type logoutRevokeMock struct {
	baseMock
	revokedTokenID string
	revokeErr      error
}

func (m *logoutRevokeMock) RevokeAPIToken(tokenID string) (*client.RevokeAPITokenResponse, error) {
	m.revokedTokenID = tokenID
	if m.revokeErr != nil {
		return nil, m.revokeErr
	}
	return &client.RevokeAPITokenResponse{Success: true, Message: "Token revoked"}, nil
}

func resetLocalOnlyFlag(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = logoutCmd.Flags().Set("local-only", "false")
	})
}

func TestLogoutRevokesSavedToken(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	resetLocalOnlyFlag(t)
	writeAnkraConfig(t, map[string]string{
		"token":    "saved-token",
		"token_id": "token-id-42",
		"base-url": "https://platform.ankra.dev",
	})
	mock := &logoutRevokeMock{}
	setMockClient(t, mock)

	output, err := executeCommand("logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if mock.revokedTokenID != "token-id-42" {
		t.Errorf("expected RevokeAPIToken called with token-id-42, got %q", mock.revokedTokenID)
	}
	if !strings.Contains(output, "Revoked the login token") {
		t.Errorf("expected revocation confirmation in output, got:\n%s", output)
	}

	contents, err := os.ReadFile(getConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(contents), "saved-token") {
		t.Errorf("token not cleared from config:\n%s", contents)
	}
}

func TestLogoutLocalOnlySkipsRevocation(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	resetLocalOnlyFlag(t)
	writeAnkraConfig(t, map[string]string{
		"token":    "saved-token",
		"token_id": "token-id-42",
	})
	mock := &logoutRevokeMock{}
	setMockClient(t, mock)

	if _, err := executeCommand("logout", "--local-only"); err != nil {
		t.Fatalf("logout --local-only: %v", err)
	}
	if mock.revokedTokenID != "" {
		t.Errorf("expected no revocation call with --local-only, got token ID %q", mock.revokedTokenID)
	}

	contents, err := os.ReadFile(getConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(contents), "saved-token") {
		t.Errorf("token not cleared from config:\n%s", contents)
	}
}

func TestLogoutStillClearsCredentialsWhenRevocationFails(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	resetLocalOnlyFlag(t)
	writeAnkraConfig(t, map[string]string{
		"token":    "saved-token",
		"token_id": "token-id-42",
	})
	mock := &logoutRevokeMock{revokeErr: errors.New("platform unreachable")}
	setMockClient(t, mock)

	output, err := executeCommand("logout")
	if err != nil {
		t.Fatalf("logout should succeed despite revocation failure: %v", err)
	}
	if !strings.Contains(output, "could not revoke the token") {
		t.Errorf("expected revocation warning, got:\n%s", output)
	}
	if !strings.Contains(output, "Logged out successfully.") {
		t.Errorf("expected logout success message, got:\n%s", output)
	}

	contents, err := os.ReadFile(getConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(contents), "saved-token") {
		t.Errorf("token not cleared from config:\n%s", contents)
	}
}

func TestLogoutWithoutTokenIDWarnsAndClears(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	resetLocalOnlyFlag(t)
	writeAnkraConfig(t, map[string]string{
		"token": "saved-token",
	})
	mock := &logoutRevokeMock{}
	setMockClient(t, mock)

	output, err := executeCommand("logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if mock.revokedTokenID != "" {
		t.Errorf("expected no revocation call without token_id, got %q", mock.revokedTokenID)
	}
	if !strings.Contains(output, "cannot be revoked remotely") {
		t.Errorf("expected warning about non-revocable token, got:\n%s", output)
	}
}
