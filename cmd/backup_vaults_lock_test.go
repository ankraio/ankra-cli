package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

// TestBackupVaultsDarkLaneExplainsTheFeatureFlag pins the CLI half of the
// backups feature gate: the platform answers every vault route with a 403
// carrying "Backups are not enabled for this organisation." while the
// organisation's flag is dark, and the CLI turns that into an actionable
// message (enable the feature, or check the selected organisation) instead
// of the bare status-coded detail.
func TestBackupVaultsDarkLaneExplainsTheFeatureFlag(t *testing.T) {
	mock := &backupVaultsMock{
		listError: client.NewUnexpectedResponseError(http.StatusForbidden, backupsNotEnabledDetail),
	}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	_, executeError := executeCommand("backup", "vaults", "list")

	if executeError == nil {
		t.Fatal("a dark lane must fail the command")
	}
	message := executeError.Error()
	for _, expected := range []string{
		backupsNotEnabledDetail,
		"enable the `backups` feature",
		"ankra org current",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("expected the error to contain %q, got: %s", expected, message)
		}
	}
}

// TestBackupVaultsPermissionRefusalStaysAPermissionError pins the boundary:
// a 403 that is NOT the dark-lane detail keeps its own wording, so a real
// permission refusal is never rewritten into feature-flag advice.
func TestBackupVaultsPermissionRefusalStaysAPermissionError(t *testing.T) {
	mock := &backupVaultsMock{
		listError: client.NewUnexpectedResponseError(http.StatusForbidden,
			"You need the backups.read permission to list backup vaults."),
	}
	setMockClient(t, mock)
	resetBackupVaultsFlags(t)

	_, executeError := executeCommand("backup", "vaults", "list")

	if executeError == nil {
		t.Fatal("a permission refusal must fail the command")
	}
	if strings.Contains(executeError.Error(), "ankra org current") {
		t.Fatalf("a permission refusal must not be rewritten as feature-flag advice: %s", executeError)
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(executeError, &unexpected) {
		t.Fatalf("the original API error must stay wrapped for the support hint: %v", executeError)
	}
}
