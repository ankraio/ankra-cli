package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartTOTPEnrollmentSendsCSRFHeaderAndCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/account/mfa/totp/start" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		headerToken := request.Header.Get(csrfHeaderName)
		if headerToken == "" {
			t.Fatal("missing csrf header")
		}
		cookie, err := request.Cookie("ankra_csrf")
		if err != nil {
			t.Fatalf("missing csrf cookie: %v", err)
		}
		if cookie.Value != headerToken {
			t.Fatalf("csrf cookie = %q, header = %q", cookie.Value, headerToken)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(StartTOTPEnrollmentResponse{
			Secret:     "SECRET",
			OtpAuthURI: "otpauth://totp/Ankra:test",
		})
	}))
	defer server.Close()

	apiClient := New("test-token", server.URL)
	response, err := apiClient.StartTOTPEnrollment()
	if err != nil {
		t.Fatalf("StartTOTPEnrollment() error = %v", err)
	}
	if response.Secret != "SECRET" {
		t.Fatalf("secret = %q", response.Secret)
	}
}

func TestRegenerateRecoveryCodesParsesCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/account/mfa/recovery-code" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(RecoveryCodesResponse{
			RecoveryCodes: []string{"AAAAA-BBBBB", "CCCCC-DDDDD"},
		})
	}))
	defer server.Close()

	apiClient := New("test-token", server.URL)
	response, err := apiClient.RegenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes() error = %v", err)
	}
	if len(response.RecoveryCodes) != 2 {
		t.Fatalf("len(recovery_codes) = %d", len(response.RecoveryCodes))
	}
}
