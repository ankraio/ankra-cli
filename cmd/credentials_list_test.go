package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// credentialsGitListMock answers the credentials listing per provider filter,
// recording which filters were asked for.
type credentialsGitListMock struct {
	baseMock
	unfiltered    []client.Credential
	github        []client.Credential
	providerCalls []string
}

func (mock *credentialsGitListMock) ListCredentials(provider *string) ([]client.Credential, error) {
	if provider == nil {
		mock.providerCalls = append(mock.providerCalls, "")
		return mock.unfiltered, nil
	}
	mock.providerCalls = append(mock.providerCalls, *provider)
	if *provider == "github" {
		return mock.github, nil
	}
	return nil, nil
}

func runCredentialsList(t *testing.T, mock APIClient, arguments ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	t.Cleanup(func() {
		for _, flagName := range []string{"provider", "output", "sort"} {
			if flag := credentialsListCmd.Flags().Lookup(flagName); flag != nil {
				_ = flag.Value.Set(flag.DefValue)
				flag.Changed = false
			}
		}
	})
	var executeError error
	var hints string
	tableOutput := captureStdout(t, func() {
		hints, executeError = executeCommand(append([]string{"credentials", "list"}, arguments...)...)
	})
	return tableOutput, hints, executeError
}

func installationBackedGithubCredential(name string) client.Credential {
	installationID := 145425934
	accountLogin := "acme"
	return client.Credential{
		ID:             "9b8a7c6d-4444-4555-8666-777788889999",
		Name:           name,
		Provider:       "github",
		Available:      true,
		InstallationID: &installationID,
		AccountLogin:   &accountLogin,
		CreatedAt:      "2026-01-01T00:00:00Z",
	}
}

// The org's GitHub connection is the first prerequisite of `application add`;
// when the unfiltered listing omits git credentials they are fetched through
// the same provider-filtered read the add flow resolves its credential from,
// and merged in.
func TestCredentialsListMergesOmittedGitCredentials(t *testing.T) {
	mock := &credentialsGitListMock{
		unfiltered: []client.Credential{{
			ID: "1c2d3e4f-5555-4666-8777-888899990000", Name: "hetzner-production",
			Provider: "hetzner", Available: true, CreatedAt: "2026-01-01T00:00:00Z",
		}},
		github: []client.Credential{installationBackedGithubCredential("github-app-acme")},
	}
	tableOutput, hints, executeError := runCredentialsList(t, mock)
	if executeError != nil {
		t.Fatalf("credentials list error = %v", executeError)
	}
	if len(mock.providerCalls) != 2 || mock.providerCalls[0] != "" || mock.providerCalls[1] != "github" {
		t.Errorf("provider calls = %v, want the unfiltered read then the github read", mock.providerCalls)
	}
	cleanOutput := stripANSICodes(tableOutput)
	if !strings.Contains(cleanOutput, "github-app-acme") {
		t.Errorf("the GitHub credential must be listed: %s", cleanOutput)
	}
	if !strings.Contains(cleanOutput, "github (App)") {
		t.Errorf("an installation-backed credential must read as App-backed: %s", cleanOutput)
	}
	if !strings.Contains(hints, "ankra credentials repositories") {
		t.Errorf("the github rows must point at 'credentials repositories': %s", hints)
	}
}

// A backend whose unfiltered listing already includes git credentials is
// left alone: no second read, no duplicate rows.
func TestCredentialsListDoesNotDoubleReadWhenGitCredentialsAreListed(t *testing.T) {
	mock := &credentialsGitListMock{
		unfiltered: []client.Credential{installationBackedGithubCredential("github-app-acme")},
	}
	tableOutput, _, executeError := runCredentialsList(t, mock)
	if executeError != nil {
		t.Fatalf("credentials list error = %v", executeError)
	}
	if len(mock.providerCalls) != 1 || mock.providerCalls[0] != "" {
		t.Errorf("provider calls = %v, want only the unfiltered read", mock.providerCalls)
	}
	if count := strings.Count(stripANSICodes(tableOutput), "github-app-acme"); count != 1 {
		t.Errorf("the credential must appear exactly once, got %d rows", count)
	}
}

// An explicit --provider filter is the caller's scope; the merge only guards
// the unfiltered listing.
func TestCredentialsListProviderFilterIsPassedThroughUnchanged(t *testing.T) {
	mock := &credentialsGitListMock{
		github: []client.Credential{installationBackedGithubCredential("github-app-acme")},
	}
	_, _, executeError := runCredentialsList(t, mock, "--provider", "github")
	if executeError != nil {
		t.Fatalf("credentials list error = %v", executeError)
	}
	if len(mock.providerCalls) != 1 || mock.providerCalls[0] != "github" {
		t.Errorf("provider calls = %v, want exactly the filtered read", mock.providerCalls)
	}
}

// Structured output carries the merged rows and stays parseable - the
// App-backed marker is table decoration only.
func TestCredentialsListStructuredOutputCarriesTheMergedCredentials(t *testing.T) {
	mock := &credentialsGitListMock{
		unfiltered: []client.Credential{{
			ID: "1c2d3e4f-5555-4666-8777-888899990000", Name: "hetzner-production",
			Provider: "hetzner", Available: true, CreatedAt: "2026-01-01T00:00:00Z",
		}},
		github: []client.Credential{installationBackedGithubCredential("github-app-acme")},
	}
	_, output, executeError := runCredentialsList(t, mock, "-o", "json")
	if executeError != nil {
		t.Fatalf("credentials list -o json error = %v", executeError)
	}
	var credentials []client.Credential
	if decodeError := json.Unmarshal([]byte(output), &credentials); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if len(credentials) != 2 {
		t.Fatalf("credentials = %d, want the merged 2", len(credentials))
	}
	for _, credential := range credentials {
		if credential.Provider != "github" && credential.Provider != "hetzner" {
			t.Errorf("unexpected provider %q in structured output", credential.Provider)
		}
	}
}
