package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationCredentialMock struct {
	baseMock
	payload json.RawMessage

	getCalls      int
	setCalls      int
	setRequest    client.SetApplicationRepositoryCredentialRequest
	applicationID string
}

func (mock *applicationCredentialMock) GetApplicationRepositoryCredential(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.getCalls++
	mock.applicationID = applicationID
	return mock.payload, nil
}

func (mock *applicationCredentialMock) SetApplicationRepositoryCredential(requestContext context.Context, applicationID string, credentialRequest client.SetApplicationRepositoryCredentialRequest) (json.RawMessage, error) {
	mock.setCalls++
	mock.applicationID = applicationID
	mock.setRequest = credentialRequest
	return mock.payload, nil
}

func TestApplicationCredentialCommandsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() != "credential" {
			continue
		}
		for _, child := range subcommand.Commands() {
			registered[child.Name()] = true
		}
	}
	for _, expected := range []string{"get", "set"} {
		if !registered[expected] {
			t.Errorf("credential subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationCredentialGetRendersThePayload(t *testing.T) {
	mockClient := &applicationCredentialMock{payload: json.RawMessage(`{"credential_name":"github-app-acme","resolved":true}`)}
	output, executeError := runApplicationCommand(t, mockClient, "credential", "get", testApplicationID)
	if executeError != nil {
		t.Fatalf("credential get error = %v", executeError)
	}
	if mockClient.getCalls != 1 || mockClient.applicationID != testApplicationID {
		t.Errorf("get calls = %d for %q", mockClient.getCalls, mockClient.applicationID)
	}
	if !strings.Contains(output, "\"credential_name\": \"github-app-acme\"") {
		t.Errorf("output is not indented JSON: %q", output)
	}
}

func TestApplicationCredentialSetSendsTheTrimmedName(t *testing.T) {
	mockClient := &applicationCredentialMock{payload: json.RawMessage(`{"credential_name":"github-app-acme","resolved":true}`)}
	_, executeError := runApplicationCommand(t, mockClient, "credential", "set", testApplicationID, "--credential", " github-app-acme ")
	if executeError != nil {
		t.Fatalf("credential set error = %v", executeError)
	}
	if mockClient.setCalls != 1 || mockClient.setRequest.CredentialName != "github-app-acme" {
		t.Errorf("set calls = %d request = %+v", mockClient.setCalls, mockClient.setRequest)
	}
}

func TestApplicationCredentialSetRequiresTheCredential(t *testing.T) {
	mockClient := &applicationCredentialMock{}
	_, executeError := runApplicationCommand(t, mockClient, "credential", "set", testApplicationID)
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error, got %v", executeError)
	}
	if mockClient.setCalls != 0 {
		t.Errorf("set calls = %d, want 0", mockClient.setCalls)
	}
}
