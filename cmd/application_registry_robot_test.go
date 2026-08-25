package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type applicationRegistryRobotMock struct {
	baseMock
	payload json.RawMessage
	fail    error

	getCalls      int
	ensureCalls   int
	ensureRequest client.EnsureApplicationRegistryRobotRequest
	revokeCalls   int
	applicationID string
}

func (mock *applicationRegistryRobotMock) GetApplicationRegistryRobot(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.getCalls++
	mock.applicationID = applicationID
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationRegistryRobotMock) EnsureApplicationRegistryRobot(requestContext context.Context, applicationID string, robotRequest client.EnsureApplicationRegistryRobotRequest) (json.RawMessage, error) {
	mock.ensureCalls++
	mock.applicationID = applicationID
	mock.ensureRequest = robotRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func (mock *applicationRegistryRobotMock) RevokeApplicationRegistryRobot(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.revokeCalls++
	mock.applicationID = applicationID
	if mock.fail != nil {
		return nil, mock.fail
	}
	return mock.payload, nil
}

func registryRobotCommand(t *testing.T) *cobra.Command {
	t.Helper()
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() != "registry" {
			continue
		}
		for _, registrySubcommand := range subcommand.Commands() {
			if registrySubcommand.Name() == "robot" {
				return registrySubcommand
			}
		}
	}
	t.Fatal("the registry robot subcommand is not registered")
	return nil
}

func TestApplicationRegistryRobotCommandsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, subcommand := range registryRobotCommand(t).Commands() {
		registered[subcommand.Name()] = true
	}
	for _, expected := range []string{"get", "ensure", "rotate", "revoke"} {
		if !registered[expected] {
			t.Errorf("registry robot subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationRegistryRobotGetRendersThePayload(t *testing.T) {
	mockClient := &applicationRegistryRobotMock{
		payload: json.RawMessage(`{"provisioned":true,"robot_name":"robot$org-acme+app-1"}`),
	}
	output, executeError := runApplicationCommand(t, mockClient, "registry", "robot", "get", testApplicationID)
	if executeError != nil {
		t.Fatalf("registry robot get error = %v", executeError)
	}
	if mockClient.getCalls != 1 || mockClient.applicationID != testApplicationID {
		t.Errorf("get calls = %d for %q, want 1 for %s", mockClient.getCalls, mockClient.applicationID, testApplicationID)
	}
	if !strings.Contains(output, "\"robot_name\": \"robot$org-acme+app-1\"") {
		t.Errorf("output is not indented JSON: %q", output)
	}
}

// ensure and rotate ride the same endpoint and differ only in the rotate flag,
// so the wire body is what tells them apart.
func TestApplicationRegistryRobotEnsureAndRotateSetTheRotateFlag(t *testing.T) {
	for _, testCase := range []struct {
		subcommand string
		wantRotate bool
	}{
		{subcommand: "ensure", wantRotate: false},
		{subcommand: "rotate", wantRotate: true},
	} {
		mockClient := &applicationRegistryRobotMock{payload: json.RawMessage(`{"changed":true}`)}
		_, executeError := runApplicationCommand(t, mockClient, "registry", "robot", testCase.subcommand, testApplicationID)
		if executeError != nil {
			t.Fatalf("registry robot %s error = %v", testCase.subcommand, executeError)
		}
		if mockClient.ensureCalls != 1 {
			t.Fatalf("%s: ensure calls = %d, want 1", testCase.subcommand, mockClient.ensureCalls)
		}
		if mockClient.ensureRequest.Rotate != testCase.wantRotate {
			t.Errorf("%s: rotate = %v, want %v", testCase.subcommand, mockClient.ensureRequest.Rotate, testCase.wantRotate)
		}
	}
}

func TestApplicationRegistryRobotRevokeConfirmsFirst(t *testing.T) {
	declined := &applicationRegistryRobotMock{}
	_, declineError := runApplicationCommandWithInput(t, declined, "n\n", "registry", "robot", "revoke", testApplicationID)
	if declineError == nil {
		t.Fatal("a declined confirmation must fail")
	}
	if exitCodeFor(declineError) != exitCancelled {
		t.Errorf("exit code = %d, want %d", exitCodeFor(declineError), exitCancelled)
	}
	if declined.revokeCalls != 0 {
		t.Errorf("revoke calls = %d, want 0 after a decline", declined.revokeCalls)
	}

	confirmed := &applicationRegistryRobotMock{payload: json.RawMessage(`{"changed":true}`)}
	_, executeError := runApplicationCommand(t, confirmed, "registry", "robot", "revoke", testApplicationID, "--yes")
	if executeError != nil {
		t.Fatalf("registry robot revoke error = %v", executeError)
	}
	if confirmed.revokeCalls != 1 || confirmed.applicationID != testApplicationID {
		t.Errorf("revoke calls = %d for %q, want 1 for %s", confirmed.revokeCalls, confirmed.applicationID, testApplicationID)
	}
}
