package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestPollMFATokenPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		if request.URL.Path != "/mfa/poll" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(tokenExchangeResponse{MfaRequired: true})
	}))
	defer server.Close()

	restoreLoginHTTPClient := loginHTTPClient
	loginHTTPClient = &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		loginHTTPClient = restoreLoginHTTPClient
	})

	result, done, err := pollMFAToken(server.URL+"/mfa/poll", []byte(`{"ticket":"ticket-1"}`))
	if err != nil {
		t.Fatalf("pollMFAToken() error = %v", err)
	}
	if done {
		t.Fatal("expected pending MFA poll to remain incomplete")
	}
	if result.Token != "" {
		t.Fatalf("pending token = %q", result.Token)
	}
}

func TestPollMFATokenCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(tokenExchangeResponse{
			Token:     "ankra-token",
			ExpiresAt: "2026-07-01T00:00:00Z",
			TokenID:   "token-id",
			TokenName: "CLI Login",
		})
	}))
	defer server.Close()

	restoreLoginHTTPClient := loginHTTPClient
	loginHTTPClient = &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		loginHTTPClient = restoreLoginHTTPClient
	})

	result, done, err := pollMFAToken(server.URL, []byte(`{"ticket":"ticket-1"}`))
	if err != nil {
		t.Fatalf("pollMFAToken() error = %v", err)
	}
	if !done {
		t.Fatal("expected completed MFA poll")
	}
	if result.Token != "ankra-token" {
		t.Fatalf("token = %q", result.Token)
	}
	if result.TokenName != "CLI Login" {
		t.Fatalf("token name = %q", result.TokenName)
	}
}

func TestPollMFATokenExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	restoreLoginHTTPClient := loginHTTPClient
	loginHTTPClient = &http.Client{Timeout: time.Second}
	t.Cleanup(func() {
		loginHTTPClient = restoreLoginHTTPClient
	})

	_, _, err := pollMFAToken(server.URL, []byte(`{"ticket":"gone"}`))
	if err == nil {
		t.Fatal("expected expired MFA ticket error")
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
