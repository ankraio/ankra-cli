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
	stdout, stderr, runError := runRegistryCommandSplit(t, mockClient, input, arguments...)
	return stdout + stderr, runError
}

// runRegistryCommandSplit captures stdout and stderr separately, so a test
// can prove that stdout carries nothing but the answer under -o json.
func runRegistryCommandSplit(t *testing.T, mockClient APIClient, input string, arguments ...string) (string, string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	registryCommand := newRegistryCommand()
	var stdout, stderr bytes.Buffer
	registryCommand.SetOut(&stdout)
	registryCommand.SetErr(&stderr)
	registryCommand.SetIn(strings.NewReader(input))
	registryCommand.SetArgs(arguments)
	runError := registryCommand.Execute()
	return stdout.String(), stderr.String(), runError
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

// The secret is printed exactly once, the login command reads it from stdin
// rather than carrying it, and the flags reach the wire as the request the
// platform expects. The platform's docker_login embeds the secret as -p; that
// line must never reach the human output, or the secret lands in the shell
// history the moment it is pasted.
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
	if occurrences := strings.Count(output, "s3cret"); occurrences != 1 {
		t.Fatalf("the secret appears %d times, want exactly once:\n%s", occurrences, output)
	}
	for _, expected := range []string{"will not be shown again", "docker login 'artifact.ankra.cloud' -u 'robot$org-abc+user-jenkins' --password-stdin"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"-p 's3cret'", "-p s3cret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("the secret-bearing docker login reached the human output (%q):\n%s", forbidden, output)
		}
	}
	loginLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "docker login") {
			loginLine = line
		}
	}
	if loginLine == "" || strings.Contains(loginLine, "s3cret") {
		t.Fatalf("the printed login line must exist and must not carry the secret: %q", loginLine)
	}
}

// The login line is built from the robot's own host and login; without both
// there is no safe line, and the answer is empty rather than the platform's
// secret-bearing docker_login.
func TestRegistryRobotLoginCommandNeverCarriesTheSecret(t *testing.T) {
	robot := registryRobotFixture()
	if got, want := registryRobotLoginCommand(&robot),
		"docker login 'artifact.ankra.cloud' -u 'robot$org-abc+user-jenkins' --password-stdin"; got != want {
		t.Fatalf("login = %q, want %q", got, want)
	}
	noHost := registryRobotFixture()
	noHost.Host = " "
	if got := registryRobotLoginCommand(&noHost); got != "" {
		t.Fatalf("login without a host = %q, want nothing", got)
	}
	noLogin := registryRobotFixture()
	noLogin.RobotName = ""
	if got := registryRobotLoginCommand(&noLogin); got != "" {
		t.Fatalf("login without a robot login = %q, want nothing", got)
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
	if decoded["secret"] != "s3cret" || decoded["name"] != "jenkins" ||
		decoded["docker_login"] != "docker login artifact.ankra.cloud -u 'robot$org-abc+user-jenkins' -p 's3cret'" {
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

func TestRegistryRobotsRotateConfirmsFirstThenShowsTheNewSecret(t *testing.T) {
	mock := &registryRobotsMock{}
	_, runError := runRegistryCommand(t, mock, "n\n", "robots", "rotate", "jenkins")
	if exitCodeFor(runError) != exitCancelled || mock.rotated != "" {
		t.Fatalf("a declined prompt must cancel without calling the API: error=%v rotated=%q", runError, mock.rotated)
	}
	output, runError := runRegistryCommand(t, mock, "", "robots", "rotate", "jenkins", "--yes")
	if runError != nil || mock.rotated != "jenkins" || strings.Count(output, "r0tated") != 1 {
		t.Fatalf("rotate --yes: error=%v rotated=%q output=\n%s", runError, mock.rotated, output)
	}
	mock.rotated = ""
	if _, runError := runRegistryCommand(t, mock, "", "robots", "rotate", "jenkins", "-y"); runError != nil || mock.rotated != "jenkins" {
		t.Fatalf("rotate -y: error=%v rotated=%q", runError, mock.rotated)
	}
}

// A script piping 'rotate -o json' must get the JSON and nothing else on
// stdout: the confirmation prompt belongs on stderr, where it is seen by a
// person and ignored by jq.
func TestRegistryRobotsRotateJSONKeepsThePromptOffStdout(t *testing.T) {
	mock := &registryRobotsMock{}
	stdout, stderr, runError := runRegistryCommandSplit(t, mock, "y\n", "robots", "rotate", "jenkins", "-o", "json")
	if runError != nil || mock.rotated != "jenkins" {
		t.Fatalf("rotate -o json: error=%v rotated=%q\nstdout=%s\nstderr=%s", runError, mock.rotated, stdout, stderr)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("stdout is not JSON only: %v\n%s", unmarshalError, stdout)
	}
	if decoded["secret"] != "r0tated" {
		t.Fatalf("decoded = %v", decoded)
	}
	if !strings.Contains(stderr, "Rotate the secret of robot account \"jenkins\"?") {
		t.Fatalf("the prompt did not reach stderr:\n%s", stderr)
	}
}

// A scope outside the vocabulary is refused locally, before any request.
func TestRegistryRobotsCreateRefusesAnUnknownScope(t *testing.T) {
	mock := &registryRobotsMock{}
	_, runError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins", "--scope", "admin")
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--scope must be") || mock.createRequest != nil {
		t.Fatalf("error=%v request=%+v", runError, mock.createRequest)
	}
	if _, runError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins", "--scope", " PULL "); runError != nil ||
		mock.createRequest == nil || mock.createRequest.Scope != "pull" {
		t.Fatalf("scope is normalised before the request: error=%v request=%+v", runError, mock.createRequest)
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
	mock.deleted = ""
	if _, runError := runRegistryCommand(t, mock, "", "robots", "delete", "jenkins", "-y"); runError != nil || mock.deleted != "jenkins" {
		t.Fatalf("delete -y: error=%v deleted=%q", runError, mock.deleted)
	}
}

// 'delete -o json' answers JSON only on stdout; the prompt goes to stderr.
func TestRegistryRobotsDeleteJSONKeepsThePromptOffStdout(t *testing.T) {
	mock := &registryRobotsMock{}
	stdout, stderr, runError := runRegistryCommandSplit(t, mock, "y\n", "robots", "delete", "jenkins", "-o", "json")
	if runError != nil || mock.deleted != "jenkins" {
		t.Fatalf("delete -o json: error=%v deleted=%q\nstdout=%s\nstderr=%s", runError, mock.deleted, stdout, stderr)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("stdout is not JSON only: %v\n%s", unmarshalError, stdout)
	}
	if decoded["deleted"] != true || decoded["name"] != "jenkins" {
		t.Fatalf("decoded = %v", decoded)
	}
	if !strings.Contains(stderr, "Revoke robot account \"jenkins\"?") {
		t.Fatalf("the prompt did not reach stderr:\n%s", stderr)
	}
}

func TestRegistryRobotsCreateRelaysTheBackendRefusal(t *testing.T) {
	mock := &registryRobotsMock{fail: errors.New("A robot with that name already exists in this organisation")}
	_, runError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins")
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("error = %v", runError)
	}
}
