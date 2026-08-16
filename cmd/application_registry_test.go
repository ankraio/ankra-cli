package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type applicationRegistryMock struct {
	baseMock
	payload json.RawMessage
	fail    error

	getApplicationID    string
	getCalls            int
	updateApplicationID string
	updateRequest       client.UpdateApplicationImageRegistryRequest
	updateCalls         int
}

func (mock *applicationRegistryMock) GetApplicationImageRegistry(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.getCalls++
	mock.getApplicationID = applicationID
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationRegistryMock) UpdateApplicationImageRegistry(requestContext context.Context, applicationID string, registryRequest client.UpdateApplicationImageRegistryRequest) (json.RawMessage, error) {
	mock.updateCalls++
	mock.updateApplicationID = applicationID
	mock.updateRequest = registryRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func TestApplicationRegistryCommandsRegistered(t *testing.T) {
	applicationCommand := newApplicationCommand()
	var registryCommand *cobra.Command
	for _, subcommand := range applicationCommand.Commands() {
		if subcommand.Name() == "registry" {
			registryCommand = subcommand
		}
	}
	if registryCommand == nil {
		t.Fatal("the registry subcommand is not registered")
	}
	registered := map[string]bool{}
	for _, subcommand := range registryCommand.Commands() {
		registered[subcommand.Name()] = true
	}
	for _, expected := range []string{"get", "set", "clear"} {
		if !registered[expected] {
			t.Errorf("registry subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationRegistryGetRendersThePayload(t *testing.T) {
	mockClient := &applicationRegistryMock{
		payload: json.RawMessage(`{"declared":true,"host":"artifact.example.com","project":"commerce"}`),
	}
	output, executeError := runApplicationCommand(t, mockClient, "registry", "get", "app-1")
	if executeError != nil {
		t.Fatalf("registry get error = %v", executeError)
	}
	if mockClient.getApplicationID != "app-1" {
		t.Errorf("application id = %q, want app-1", mockClient.getApplicationID)
	}
	if !strings.Contains(output, "\"host\": \"artifact.example.com\"") {
		t.Errorf("output is not indented JSON: %q", output)
	}
}

func TestApplicationRegistrySetMapsEveryFlag(t *testing.T) {
	mockClient := &applicationRegistryMock{payload: json.RawMessage(`{"declared":true}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"registry", "set", "app-1",
		"--url", " oci://artifact.example.com/commerce ",
		"--credential", "example-harbor",
		"--api-url", "https://artifact.example.com",
		"--pull-secret", "harbor-pull",
		"--username-secret", "HARBOR_USERNAME",
		"--password-secret", "HARBOR_PASSWORD",
		"--manage-actions-secrets",
	)
	if executeError != nil {
		t.Fatalf("registry set error = %v", executeError)
	}
	if mockClient.updateCalls != 1 {
		t.Fatalf("UpdateApplicationImageRegistry calls = %d, want 1", mockClient.updateCalls)
	}
	declaration := mockClient.updateRequest.ImageRegistry
	if declaration == nil {
		t.Fatal("the declaration must be sent")
	}
	if declaration.URL != "oci://artifact.example.com/commerce" {
		t.Errorf("url = %q, want it trimmed", declaration.URL)
	}
	if declaration.CredentialName != "example-harbor" || declaration.APIURL != "https://artifact.example.com" ||
		declaration.PullSecretName != "harbor-pull" || declaration.UsernameSecretName != "HARBOR_USERNAME" ||
		declaration.PasswordSecretName != "HARBOR_PASSWORD" || !declaration.ManageActionsSecrets {
		t.Errorf("declaration = %+v", declaration)
	}
}

func TestApplicationRegistrySetRequiresURL(t *testing.T) {
	mockClient := &applicationRegistryMock{}
	_, executeError := runApplicationCommand(t, mockClient, "registry", "set", "app-1", "--credential", "harbor")
	if executeError == nil {
		t.Fatal("expected a missing --url to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.updateCalls != 0 {
		t.Errorf("UpdateApplicationImageRegistry calls = %d, want 0", mockClient.updateCalls)
	}
}

// Without a credential Ankra can describe where the images live but cannot
// read or pull them, so builds keep reporting as never published - the
// declaration is accepted, but the user is told on stderr so structured
// output stays parseable.
func TestApplicationRegistrySetWarnsWithoutACredential(t *testing.T) {
	mockClient := &applicationRegistryMock{payload: json.RawMessage(`{"declared":true}`)}
	output, executeError := runApplicationCommand(t, mockClient,
		"registry", "set", "app-1", "--url", "oci://artifact.example.com/commerce")
	if executeError != nil {
		t.Fatalf("registry set error = %v", executeError)
	}
	if mockClient.updateCalls != 1 {
		t.Fatalf("UpdateApplicationImageRegistry calls = %d, want 1", mockClient.updateCalls)
	}
	if !strings.Contains(output, "no --credential was named") {
		t.Errorf("expected the missing-credential warning, got %q", output)
	}
}

// Clearing sends an explicit null rather than omitting the key: an absent
// key would leave the declaration in place.
func TestApplicationRegistryClearSendsAnExplicitNull(t *testing.T) {
	mockClient := &applicationRegistryMock{payload: json.RawMessage(`{"declared":false}`)}
	_, executeError := runApplicationCommand(t, mockClient, "registry", "clear", "app-1", "--yes")
	if executeError != nil {
		t.Fatalf("registry clear error = %v", executeError)
	}
	if mockClient.updateCalls != 1 {
		t.Fatalf("UpdateApplicationImageRegistry calls = %d, want 1", mockClient.updateCalls)
	}
	if mockClient.updateRequest.ImageRegistry != nil {
		t.Errorf("clear must send a nil declaration, got %+v", mockClient.updateRequest.ImageRegistry)
	}
	encoded, marshalError := json.Marshal(mockClient.updateRequest)
	if marshalError != nil {
		t.Fatalf("marshalling the request: %v", marshalError)
	}
	if !strings.Contains(string(encoded), `"image_registry":null`) {
		t.Errorf("the cleared key must ride as an explicit null, got %s", encoded)
	}
}

func TestApplicationRegistryClearRefusesWhenDeclined(t *testing.T) {
	mockClient := &applicationRegistryMock{}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n", "registry", "clear", "app-1")
	if executeError == nil {
		t.Fatal("a declined confirmation must fail")
	}
	if exitCodeFor(executeError) != exitCancelled {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitCancelled)
	}
	if mockClient.updateCalls != 0 {
		t.Errorf("UpdateApplicationImageRegistry calls = %d, want 0", mockClient.updateCalls)
	}
}
