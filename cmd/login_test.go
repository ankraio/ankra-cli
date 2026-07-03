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

func TestBuildTokenExchangeRequestDeclaresMFASupport(t *testing.T) {
	request := buildTokenExchangeRequest("auth-code", "state-1", "verifier-1", "machine-1")

	if !request.SupportsMfa {
		t.Fatal("expected supports_mfa to be declared")
	}
	if request.Code != "auth-code" || request.State != "state-1" || request.CodeVerifier != "verifier-1" || request.MachineID != "machine-1" {
		t.Fatalf("unexpected request fields: %+v", request)
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"supports_mfa":true`) {
		t.Fatalf("supports_mfa missing from wire payload: %s", encoded)
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
