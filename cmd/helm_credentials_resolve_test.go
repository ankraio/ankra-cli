package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	resolveTestCredentialID   = "ebe7ee12-f294-4d80-956b-f107c30b2f3d"
	resolveTestCredentialName = "ankra-harbor-app-dc62ed4f"
)

type helmCredentialResolveMock struct {
	baseMock
	credentials       []client.HelmCredentialListItem
	totalCount        int
	listError         error
	listCalls         int
	requestedName     string
	deleteRequested   string
	updateRequested   string
	updateRequestBody client.UpdateHelmCredentialRequest
}

func (m *helmCredentialResolveMock) ListHelmRegistryCredentials() (*client.ListHelmCredentialsResponse, error) {
	m.listCalls++
	if m.listError != nil {
		return nil, m.listError
	}
	totalCount := m.totalCount
	if totalCount == 0 {
		totalCount = len(m.credentials)
	}
	return &client.ListHelmCredentialsResponse{Credentials: m.credentials, TotalCount: totalCount}, nil
}

func (m *helmCredentialResolveMock) GetHelmRegistryCredential(credentialName string) (*client.GetHelmCredentialResponse, error) {
	m.requestedName = credentialName
	return &client.GetHelmCredentialResponse{
		ID:       resolveTestCredentialID,
		Name:     credentialName,
		Username: "robot$org+app",
		Provider: "helm",
	}, nil
}

func (m *helmCredentialResolveMock) UpdateHelmRegistryCredential(credentialName string, request client.UpdateHelmCredentialRequest) error {
	m.updateRequested = credentialName
	m.updateRequestBody = request
	return nil
}

func (m *helmCredentialResolveMock) DeleteHelmRegistryCredential(credentialName string) (*client.DeleteHelmCredentialResponse, error) {
	m.deleteRequested = credentialName
	return &client.DeleteHelmCredentialResponse{Success: true}, nil
}

func newHelmCredentialResolveMock() *helmCredentialResolveMock {
	return &helmCredentialResolveMock{
		credentials: []client.HelmCredentialListItem{
			{ID: "4793065f-1278-4ae2-bc4e-b87467b2ba92", Name: "ankra-harbor-app-7da15f97"},
			{ID: resolveTestCredentialID, Name: resolveTestCredentialName},
		},
	}
}

func TestResolveHelmCredentialNamePassesANameThroughWithoutListing(t *testing.T) {
	mock := newHelmCredentialResolveMock()

	resolved, resolveError := resolveHelmCredentialName(mock, "smartoptics-harbor-robot-admin")
	if resolveError != nil {
		t.Fatalf("unexpected error: %v", resolveError)
	}
	if resolved != "smartoptics-harbor-robot-admin" {
		t.Errorf("expected the name to pass through unchanged, got %q", resolved)
	}
	if mock.listCalls != 0 {
		t.Errorf("expected no listing for a name, got %d calls", mock.listCalls)
	}
}

func TestResolveHelmCredentialNameResolvesAnIDToItsName(t *testing.T) {
	mock := newHelmCredentialResolveMock()

	resolved, resolveError := resolveHelmCredentialName(mock, strings.ToUpper(resolveTestCredentialID))
	if resolveError != nil {
		t.Fatalf("unexpected error: %v", resolveError)
	}
	if resolved != resolveTestCredentialName {
		t.Errorf("expected %q, got %q", resolveTestCredentialName, resolved)
	}
}

func TestResolveHelmCredentialNameKeepsANameShapedLikeAUUID(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	uuidShapedName := "11111111-2222-4333-8444-555555555555"
	mock.credentials = append(mock.credentials, client.HelmCredentialListItem{
		ID: "9f0c1e2d-3b4a-4c5d-8e6f-70a1b2c3d4e5", Name: uuidShapedName,
	})

	resolved, resolveError := resolveHelmCredentialName(mock, uuidShapedName)
	if resolveError != nil {
		t.Fatalf("unexpected error: %v", resolveError)
	}
	if resolved != uuidShapedName {
		t.Errorf("expected the uuid-shaped name to be kept, got %q", resolved)
	}
}

func TestResolveHelmCredentialNameUnknownIDExitsNotFound(t *testing.T) {
	mock := newHelmCredentialResolveMock()

	_, resolveError := resolveHelmCredentialName(mock, "00000000-0000-4000-8000-000000000000")
	if resolveError == nil {
		t.Fatal("expected an error for an id the listing does not hold")
	}
	if exitCodeFor(resolveError) != exitNotFound {
		t.Errorf("expected exit %d, got %d (%v)", exitNotFound, exitCodeFor(resolveError), resolveError)
	}
	if !strings.Contains(resolveError.Error(), "ankra helm credentials list") {
		t.Errorf("expected the error to point at the listing, got: %v", resolveError)
	}
}

func TestResolveHelmCredentialNameRefusesToAnswerFromAPartialListing(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	mock.totalCount = 250

	_, resolveError := resolveHelmCredentialName(mock, "00000000-0000-4000-8000-000000000000")
	if resolveError == nil {
		t.Fatal("expected an error when the listing was only partly read")
	}
	if exitCodeFor(resolveError) == exitNotFound {
		t.Errorf("a partial listing must not claim the credential is absent: %v", resolveError)
	}
	if !strings.Contains(resolveError.Error(), "only 2 were read") {
		t.Errorf("expected the partial read to be named, got: %v", resolveError)
	}
}

func TestResolveHelmCredentialNameSurfacesAListingFailure(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	mock.listError = client.ErrUnauthorized

	_, resolveError := resolveHelmCredentialName(mock, resolveTestCredentialID)
	if !errors.Is(resolveError, client.ErrUnauthorized) {
		t.Fatalf("expected the listing failure to be wrapped, got: %v", resolveError)
	}
	if exitCodeFor(resolveError) != exitAuth {
		t.Errorf("expected exit %d, got %d", exitAuth, exitCodeFor(resolveError))
	}
}

func TestResolveHelmCredentialNameEmptyReferenceIsAUsageError(t *testing.T) {
	mock := newHelmCredentialResolveMock()

	_, resolveError := resolveHelmCredentialName(mock, "  ")
	if exitCodeFor(resolveError) != exitUsage {
		t.Errorf("expected exit %d, got %d (%v)", exitUsage, exitCodeFor(resolveError), resolveError)
	}
}

func TestHelmCredentialsGetAcceptsTheIDListPrints(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "credentials", "get", resolveTestCredentialID)
	})

	if mock.requestedName != resolveTestCredentialName {
		t.Errorf("expected the API to be asked for %q, got %q", resolveTestCredentialName, mock.requestedName)
	}
	if !strings.Contains(stdoutOutput, "Credential: "+resolveTestCredentialName) {
		t.Errorf("expected the credential detail, got: %s", stdoutOutput)
	}
}

func TestHelmCredentialsDeleteForceAcceptsTheID(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	setMockClient(t, mock)
	t.Cleanup(func() {
		_ = helmCredentialsDeleteCmd.Flags().Set("force", "false")
	})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "credentials", "delete", resolveTestCredentialID, "--force")
	})

	if mock.deleteRequested != resolveTestCredentialName {
		t.Errorf("expected the API to delete %q, got %q", resolveTestCredentialName, mock.deleteRequested)
	}
	if !strings.Contains(stdoutOutput, "Credential '"+resolveTestCredentialName+"' deleted.") {
		t.Errorf("expected the deletion to name the credential, got: %s", stdoutOutput)
	}
}

func TestHelmCredentialsGetUnknownIDExitsNotFound(t *testing.T) {
	mock := newHelmCredentialResolveMock()
	setMockClient(t, mock)

	_, commandError := executeCommand("helm", "credentials", "get", "00000000-0000-4000-8000-000000000000")
	if commandError == nil {
		t.Fatal("expected an error")
	}
	if exitCodeFor(commandError) != exitNotFound {
		t.Errorf("expected exit %d, got %d (%v)", exitNotFound, exitCodeFor(commandError), commandError)
	}
	if mock.requestedName != "" {
		t.Errorf("expected no API call for an unknown id, got a request for %q", mock.requestedName)
	}
}
