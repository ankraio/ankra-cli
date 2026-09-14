package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type registryRobotsMock struct {
	baseMock
	createRequest *client.CreateRegistryRobotRequest
	rotated       string
	deleted       string
	fail          error
}

func registryRobotFixture() client.RegistryRobot {
	return client.RegistryRobot{
		Name: "jenkins", RobotName: "robot$org-abc+user-jenkins", Host: "artifact.ankra.cloud",
		Project: "org-abc", Scope: "push", Description: "Jenkins", CredentialName: "ankra-harbor-robot-jenkins",
		CreatedAt: "2026-09-14T12:00:00Z",
	}
}

func (mock *registryRobotsMock) CreateRegistryRobot(_ context.Context, request client.CreateRegistryRobotRequest) (*client.RegistryRobotWithSecret, error) {
	mock.createRequest = &request
	if mock.fail != nil {
		return nil, mock.fail
	}
	return &client.RegistryRobotWithSecret{RegistryRobot: registryRobotFixture(), Secret: "s3cret",
		DockerLogin: "docker login artifact.ankra.cloud -u 'robot$org-abc+user-jenkins' -p 's3cret'"}, nil
}

func (mock *registryRobotsMock) ListRegistryRobots(context.Context) (*client.RegistryRobotList, error) {
	return &client.RegistryRobotList{Robots: []client.RegistryRobot{registryRobotFixture()}, TotalCount: 1}, nil
}

func (mock *registryRobotsMock) GetRegistryRobot(_ context.Context, robotName string) (*client.RegistryRobot, error) {
	robot := registryRobotFixture()
	return &robot, nil
}

func (mock *registryRobotsMock) RotateRegistryRobotSecret(_ context.Context, robotName string) (*client.RegistryRobotWithSecret, error) {
	mock.rotated = robotName
	return &client.RegistryRobotWithSecret{RegistryRobot: registryRobotFixture(), Secret: "r0tated", DockerLogin: "docker login ..."}, nil
}

func (mock *registryRobotsMock) DeleteRegistryRobot(_ context.Context, robotName string) error {
	mock.deleted = robotName
	return nil
}

func runRegistryCommand(t *testing.T, mockClient APIClient, input string, arguments ...string) (string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	registryCommand := newRegistryCommand()
	var output bytes.Buffer
	registryCommand.SetOut(&output)
	registryCommand.SetErr(&output)
	registryCommand.SetIn(strings.NewReader(input))
	registryCommand.SetArgs(arguments)
	runError := registryCommand.Execute()
	return output.String(), runError
}

func TestRegistryRobotsCommandsRegistered(t *testing.T) {
	registryCommand := newRegistryCommand()
	var robots []string
	for _, subcommand := range registryCommand.Commands() {
		if subcommand.Name() != "robots" {
			continue
		}
		for _, robotSubcommand := range subcommand.Commands() {
			robots = append(robots, robotSubcommand.Name())
		}
	}
	for _, expected := range []string{"create", "list", "get", "rotate", "delete"} {
		found := false
		for _, name := range robots {
			if name == expected {
				found = true
			}
		}
		if !found {
			t.Fatalf("registry robots %s is not registered (have %v)", expected, robots)
		}
	}
}

// The secret is printed once, with the login command, and the flags reach
// the wire as the request the platform expects.
func TestRegistryRobotsCreateShowsTheSecretOnce(t *testing.T) {
	mock := &registryRobotsMock{}
	output, runError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins", "--scope", "pull", "--description", "Jenkins")
	if runError != nil {
		t.Fatalf("create: %v", runError)
	}
	if mock.createRequest == nil || mock.createRequest.Name != "jenkins" || mock.createRequest.Scope != "pull" ||
		mock.createRequest.Description != "Jenkins" {
		t.Fatalf("request = %+v", mock.createRequest)
	}
	for _, expected := range []string{"s3cret", "will not be shown again", "docker login artifact.ankra.cloud", "robot$org-abc+user-jenkins"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

// -o json keeps stdout parseable and carries the secret as a field.
func TestRegistryRobotsCreateJSONCarriesTheSecret(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "create", "jenkins", "-o", "json")
	if runError != nil {
		t.Fatalf("create -o json: %v", runError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["secret"] != "s3cret" || decoded["name"] != "jenkins" {
		t.Fatalf("decoded = %v", decoded)
	}
}

func TestRegistryRobotsListAndGet(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "list")
	if runError != nil || !strings.Contains(output, "jenkins") || strings.Contains(output, "s3cret") {
		t.Fatalf("list: error=%v output=\n%s", runError, output)
	}
	output, runError = runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "get", "jenkins")
	if runError != nil || !strings.Contains(output, "ankra-harbor-robot-jenkins") || strings.Contains(output, "s3cret") {
		t.Fatalf("get: error=%v output=\n%s", runError, output)
	}
}

func TestRegistryRobotsRotateShowsTheNewSecret(t *testing.T) {
	mock := &registryRobotsMock{}
	output, runError := runRegistryCommand(t, mock, "", "robots", "rotate", "jenkins")
	if runError != nil || mock.rotated != "jenkins" || !strings.Contains(output, "r0tated") {
		t.Fatalf("rotate: error=%v rotated=%q output=\n%s", runError, mock.rotated, output)
	}
}

func TestRegistryRobotsDeleteConfirmsFirst(t *testing.T) {
	mock := &registryRobotsMock{}
	_, runError := runRegistryCommand(t, mock, "n\n", "robots", "delete", "jenkins")
	if exitCodeFor(runError) != exitCancelled || mock.deleted != "" {
		t.Fatalf("a declined prompt must cancel without calling the API: error=%v deleted=%q", runError, mock.deleted)
	}
	output, runError := runRegistryCommand(t, mock, "", "robots", "delete", "jenkins", "--yes")
	if runError != nil || mock.deleted != "jenkins" || !strings.Contains(output, "revoked") {
		t.Fatalf("delete --yes: error=%v deleted=%q output=\n%s", runError, mock.deleted, output)
	}
}

func TestRegistryRobotsCreateRelaysTheBackendRefusal(t *testing.T) {
	mock := &registryRobotsMock{fail: errors.New("A robot with that name already exists in this organisation")}
	_, runError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins")
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("error = %v", runError)
	}
}
