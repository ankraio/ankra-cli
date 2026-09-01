package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// loginStatusMock answers the one read `login status` makes.
type loginStatusMock struct {
	baseMock
	organisations []client.OrganisationSummary
	listError     error
}

func (mock *loginStatusMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return mock.organisations, mock.listError
}

func runLoginStatusCommand(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	t.Cleanup(func() {
		outputFlag := loginStatusCmd.Flags().Lookup("output")
		_ = outputFlag.Value.Set("")
		outputFlag.Changed = false
	})
	return executeCommand(append([]string{"login", "status"}, arguments...)...)
}

// `ankra login status` used to fall through to plain `ankra login` and
// silently start a browser auth flow; unknown positional arguments must be
// rejected before anything runs.
func TestLoginRejectsUnknownPositionalArguments(t *testing.T) {
	output, executeError := executeCommand("login", "statsu")
	if executeError == nil {
		t.Fatalf("an unknown login argument must fail, not start a browser login; output: %s", output)
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(executeError), exitUsage)
	}
	if !strings.Contains(executeError.Error(), "ankra login status") {
		t.Errorf("the error must point at 'ankra login status': %v", executeError)
	}
}

func TestLoginStatusReportsNotLoggedIn(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Setenv("ANKRA_ORG", "")

	output, executeError := runLoginStatusCommand(t)
	if executeError == nil {
		t.Fatal("status without credentials must exit non-zero for scripts")
	}
	if exitCodeFor(executeError) != exitAuth {
		t.Errorf("exit code = %d, want %d (auth)", exitCodeFor(executeError), exitAuth)
	}
	if !strings.Contains(output, "Logged in:    no") {
		t.Errorf("output = %q, want the not-logged-in report", output)
	}
}

func TestLoginStatusReportsTheEnvTokenAndOrganisation(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Setenv("ANKRA_ORG", "")
	t.Setenv("ANKRA_API_TOKEN", "env-token")

	organisationName := "Acme Org"
	mockClient := &loginStatusMock{organisations: []client.OrganisationSummary{{
		OrganisationID: shipTestOrganisationID,
		Name:           &organisationName,
		UserCurrent:    true,
	}}}
	setMockClient(t, mockClient)

	output, executeError := runLoginStatusCommand(t)
	if executeError != nil {
		t.Fatalf("login status error = %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "Logged in:    yes") {
		t.Errorf("output = %q, want logged in", output)
	}
	if !strings.Contains(output, "Token source: env") {
		t.Errorf("output = %q, want the env token source", output)
	}
	if !strings.Contains(output, "Acme Org") {
		t.Errorf("output = %q, want the current organisation", output)
	}
	if !strings.Contains(output, defaultBaseURL) {
		t.Errorf("output = %q, want the base URL in use", output)
	}
}

func TestLoginStatusReportsTheConfigTokenSource(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Setenv("ANKRA_ORG", "")
	writeAnkraConfig(t, map[string]string{
		"token":      "saved-token",
		"base-url":   "https://saved.example.com",
		"token_name": "cli-laptop",
	})

	organisationName := "Acme Org"
	mockClient := &loginStatusMock{organisations: []client.OrganisationSummary{{
		OrganisationID: shipTestOrganisationID,
		Name:           &organisationName,
		UserCurrent:    true,
	}}}
	setMockClient(t, mockClient)
	// setMockClient exports ANKRA_API_TOKEN for auth-requiring commands;
	// this test is about the saved config winning, so clear it again.
	t.Setenv("ANKRA_API_TOKEN", "")

	output, executeError := runLoginStatusCommand(t)
	if executeError != nil {
		t.Fatalf("login status error = %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "Token source: config") {
		t.Errorf("output = %q, want the config token source", output)
	}
	if !strings.Contains(output, "cli-laptop") {
		t.Errorf("output = %q, want the saved token name", output)
	}
	if !strings.Contains(output, "https://saved.example.com") {
		t.Errorf("output = %q, want the saved base URL", output)
	}
}

func TestLoginStatusReportsARejectedToken(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Setenv("ANKRA_ORG", "")
	t.Setenv("ANKRA_API_TOKEN", "expired-token")

	mockClient := &loginStatusMock{listError: client.ErrUnauthorized}
	setMockClient(t, mockClient)

	output, executeError := runLoginStatusCommand(t)
	if executeError == nil {
		t.Fatal("a rejected token must exit non-zero")
	}
	if exitCodeFor(executeError) != exitAuth {
		t.Errorf("exit code = %d, want %d (auth)", exitCodeFor(executeError), exitAuth)
	}
	if !strings.Contains(output, "Logged in:    no") {
		t.Errorf("output = %q, want the rejected-token report", output)
	}
}

func TestLoginStatusStructuredOutputStaysParseable(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Setenv("ANKRA_ORG", "")
	t.Setenv("ANKRA_API_TOKEN", "env-token")

	organisationName := "Acme Org"
	mockClient := &loginStatusMock{organisations: []client.OrganisationSummary{{
		OrganisationID: shipTestOrganisationID,
		Name:           &organisationName,
		UserCurrent:    true,
	}}}
	setMockClient(t, mockClient)

	output, executeError := runLoginStatusCommand(t, "-o", "json")
	if executeError != nil {
		t.Fatalf("login status -o json error = %v\noutput: %s", executeError, output)
	}
	var status loginStatusOutput
	if decodeError := json.Unmarshal([]byte(output), &status); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if !status.LoggedIn || status.TokenSource != "env" ||
		status.Organisation == nil || status.Organisation.OrganisationID != shipTestOrganisationID {
		t.Errorf("structured status = %+v", status)
	}
}
