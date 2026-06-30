package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type profileMock struct {
	baseMock

	status                 *client.MFAStatus
	regeneratedCodes       *client.RecoveryCodesResponse
	regenerateCalled       bool
	confirmedTotpCode      string
	confirmTotpResponse    *client.RecoveryCodesResponse
	removedPasskeyID       string
	removedAuthenticatorID string
}

func (m *profileMock) GetMFAStatus() (*client.MFAStatus, error) {
	if m.status != nil {
		return m.status, nil
	}
	return &client.MFAStatus{}, nil
}

func (m *profileMock) ConfirmTOTPEnrollment(code string) (*client.RecoveryCodesResponse, error) {
	m.confirmedTotpCode = code
	if m.confirmTotpResponse != nil {
		return m.confirmTotpResponse, nil
	}
	return &client.RecoveryCodesResponse{}, nil
}

func (m *profileMock) RemoveMFAMethod(methodID string) (*client.RemoveMFAResponse, error) {
	m.removedAuthenticatorID = methodID
	return &client.RemoveMFAResponse{Success: true}, nil
}

func (m *profileMock) RegenerateRecoveryCodes() (*client.RecoveryCodesResponse, error) {
	m.regenerateCalled = true
	if m.regeneratedCodes != nil {
		return m.regeneratedCodes, nil
	}
	return &client.RecoveryCodesResponse{}, nil
}

func (m *profileMock) RemovePasskey(credentialID string) (*client.RemoveMFAResponse, error) {
	m.removedPasskeyID = credentialID
	return &client.RemoveMFAResponse{Success: true}, nil
}

func resetProfileFlags(t *testing.T) {
	t.Helper()
	for _, command := range []*cobra.Command{
		profileAuthStatusCmd,
		profileAuthTotpStartCmd,
		profileAuthTotpConfirmCmd,
		profileAuthTotpRemoveCmd,
		profileAuthRecoveryCodesRegenerateCmd,
		profileAuthPasskeysListCmd,
		profileAuthPasskeysRemoveCmd,
	} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

func runProfileWithInput(t *testing.T, mock APIClient, input string, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetProfileFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return output.String(), err
}

func TestProfileAuthStatusRendersMFAState(t *testing.T) {
	passkeyName := "Touch ID"
	createdAt := "2026-06-30T10:00:00Z"
	mock := &profileMock{status: &client.MFAStatus{
		Required:               true,
		Enrolled:               true,
		RecoveryCodesRemaining: 4,
		Methods: []client.MFAMethod{{
			ID:        "totp",
			Type:      "totp",
			Confirmed: true,
		}},
		Passkeys: []client.PasskeyInfo{{
			ID:        "passkey-1",
			Type:      "webauthn-platform",
			Name:      &passkeyName,
			CreatedAt: &createdAt,
		}},
	}}

	output, err := runProfileWithInput(t, mock, "", "profile", "auth", "status")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, output)
	}
	for _, expected := range []string{
		"Two-factor authentication:",
		"Recovery codes remaining: 4",
		"Authenticator apps:",
		"Passkeys/security keys:",
		"Touch ID",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestProfileAuthRecoveryCodesRegenerateRequiresConfirmation(t *testing.T) {
	mock := &profileMock{regeneratedCodes: &client.RecoveryCodesResponse{
		RecoveryCodes: []string{"AAAAA-BBBBB", "CCCCC-DDDDD"},
	}}

	_, err := runProfileWithInput(t, mock, "n\n", "profile", "auth", "recovery-codes", "regenerate")
	if err == nil {
		t.Fatal("expected confirmation refusal to fail")
	}
	if mock.regenerateCalled {
		t.Fatal("regenerate should not be called when confirmation is refused")
	}
}

func TestProfileAuthRecoveryCodesRegenerateYesPrintsCodes(t *testing.T) {
	mock := &profileMock{regeneratedCodes: &client.RecoveryCodesResponse{
		RecoveryCodes: []string{"AAAAA-BBBBB", "CCCCC-DDDDD"},
	}}

	output, err := runProfileWithInput(t, mock, "", "profile", "auth", "recovery-codes", "regenerate", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, output)
	}
	if !mock.regenerateCalled {
		t.Fatal("expected regenerate call")
	}
	if !strings.Contains(output, "AAAAA-BBBBB") || !strings.Contains(output, "will not be shown again") {
		t.Fatalf("expected recovery codes in output, got:\n%s", output)
	}
}

func TestProfileAuthPasskeysRemoveYesCallsClient(t *testing.T) {
	mock := &profileMock{}

	output, err := runProfileWithInput(t, mock, "", "profile", "auth", "passkeys", "remove", "passkey-1", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, output)
	}
	if mock.removedPasskeyID != "passkey-1" {
		t.Fatalf("removed passkey = %q", mock.removedPasskeyID)
	}
}
