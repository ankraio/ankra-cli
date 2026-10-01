package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type registryRobotsMock struct {
	baseMock
	createRequest *client.CreateRegistryRobotRequest
	rotated       string
	deleted       string
	fail          error
	// list, when set, is what the listing answers instead of the one
	// member's robot of the fixture.
	list *client.RegistryRobotList
	// warning rides the create and rotate answers when set.
	warning string
}

// registryRobotFleetFixture is a listing as a platform that manages robots
// answers it: two of the member's own, the organisation's two, and an
// application's.
func registryRobotFleetFixture() *client.RegistryRobotList {
	expiresAt := "2026-01-01T00:00:00Z"
	return &client.RegistryRobotList{
		Robots: []client.RegistryRobot{
			{Name: "jenkins", Kind: "user", RobotName: "robot$org-abc+user-jenkins", Host: "artifact.ankra.cloud",
				Project: "org-abc", Projects: []string{"org-abc"}, Scope: "push",
				Permissions: []string{"repository:pull", "repository:push"}, Description: "Jenkins",
				CredentialName: "ankra-harbor-robot-jenkins", CreatedAt: "2026-09-14T12:00:00Z"},
			{Name: "cleanup", Kind: "user", RobotName: "robot$org-abc+user-cleanup", Host: "artifact.ankra.cloud",
				Project: "org-abc", Projects: []string{"org-abc"}, Scope: "custom",
				Permissions:    []string{"repository:pull", "artifact:delete"},
				CredentialName: "ankra-harbor-robot-cleanup", CreatedAt: "2025-10-01T00:00:00Z", ExpiresAt: &expiresAt},
			{Name: "ci", Kind: "organisation", Managed: true, RobotName: "robot$org-abc+ci", Host: "artifact.ankra.cloud",
				Project: "org-abc", Projects: []string{"org-abc"}, Scope: "push",
				Permissions:    []string{"repository:pull", "repository:push"},
				Description:    "Managed by Ankra: the push and pull login the organisation's builds publish with.",
				CredentialName: "ankra-harbor-ci", CreatedAt: "2026-07-22T17:16:00Z"},
			{Name: "pull", Kind: "organisation", Managed: true, RobotName: "robot$org-abc+pull", Host: "artifact.ankra.cloud",
				Project: "org-abc", Projects: []string{"org-abc"}, Scope: "pull", Permissions: []string{"repository:pull"},
				CredentialName: "ankra-harbor-pull", CreatedAt: "2026-07-22T17:16:00Z"},
			{Name: "app-23298741", Kind: "application", Managed: true, RobotName: "robot$commerce-images+app-23298741",
				Host: "artifact.customer.test", Project: "commerce-images", Projects: []string{"commerce-images"},
				Scope: "push", Permissions: []string{"repository:pull", "repository:push"},
				Application:    &client.RegistryRobotApplication{ID: "23298741", Name: "commerce"},
				CredentialName: "ankra-harbor-app-23298741", CreatedAt: "2026-08-25T09:17:40Z"},
		},
		TotalCount: 5,
		Registry:   &client.RegistryRobotRegistry{Host: "artifact.ankra.cloud", Project: "org-abc"},
		AvailablePermissions: []client.RegistryRobotPermission{
			{Permission: "repository:pull", Label: "Pull", Description: "Pull images and charts from the project."},
			{Permission: "artifact:delete", Label: "Delete artifacts", Description: "Delete artifacts, which a cleanup job needs."},
		},
	}
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
		DockerLogin: "docker login artifact.ankra.cloud -u 'robot$org-abc+user-jenkins' -p 's3cret'",
		Warning:     mock.warning}, nil
}

func (mock *registryRobotsMock) ListRegistryRobots(context.Context) (*client.RegistryRobotList, error) {
	if mock.list != nil {
		return mock.list, nil
	}
	return &client.RegistryRobotList{Robots: []client.RegistryRobot{registryRobotFixture()}, TotalCount: 1}, nil
}

func (mock *registryRobotsMock) GetRegistryRobot(_ context.Context, robotName string) (*client.RegistryRobot, error) {
	if mock.list != nil {
		for _, robot := range mock.list.Robots {
			if robot.Name == robotName {
				return &robot, nil
			}
		}
	}
	robot := registryRobotFixture()
	return &robot, nil
}

func (mock *registryRobotsMock) RotateRegistryRobotSecret(_ context.Context, robotName string) (*client.RegistryRobotWithSecret, error) {
	mock.rotated = robotName
	return &client.RegistryRobotWithSecret{RegistryRobot: registryRobotFixture(), Secret: "r0tated",
		DockerLogin: "docker login ...", Warning: mock.warning}, nil
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
	for _, expected := range []string{"create", "list", "permissions", "get", "rotate", "delete"} {
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

// The listing shows the robots Ankra manages beside the member's own: every
// login that reaches the organisation's images, each with its kind, what it
// may do, the project it is bound to and when it expires. A managed robot on
// a registry the organisation runs itself shows that registry's project.
func TestRegistryRobotsListShowsManagedRobotsBesideTheMembersOwn(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "", "robots", "list")
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	for _, expected := range []string{
		"Registry project: artifact.ankra.cloud/org-abc",
		"robot$org-abc+ci", "robot$org-abc+pull", "robot$commerce-images+app-23298741",
		"organisation", "application", "commerce-images",
		"push and pull", "repository:pull, artifact:delete",
		"never", "2026-01-01T00:00:00Z (expired)",
		"managed by Ankra",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("listing lacks %q:\n%s", expected, output)
		}
	}
}

// --kind narrows the listing, in the table and in the structured answer
// alike, and a value outside the vocabulary is a usage error before any
// request.
func TestRegistryRobotsListKindFilter(t *testing.T) {
	cases := map[string][]string{
		"all":          {"jenkins", "cleanup", "ci", "pull", "app-23298741"},
		"user":         {"jenkins", "cleanup"},
		"managed":      {"ci", "pull", "app-23298741"},
		"organisation": {"ci", "pull"},
		"application":  {"app-23298741"},
	}
	for kind, wantNames := range cases {
		output, runError := runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "",
			"robots", "list", "--kind", kind, "-o", "json")
		if runError != nil {
			t.Fatalf("list --kind %s: %v", kind, runError)
		}
		var decoded client.RegistryRobotList
		if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
			t.Fatalf("list --kind %s is not JSON: %v\n%s", kind, unmarshalError, output)
		}
		gotNames := []string{}
		for _, robot := range decoded.Robots {
			gotNames = append(gotNames, robot.Name)
		}
		if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") || decoded.TotalCount != len(wantNames) {
			t.Errorf("--kind %s listed %v (total %d), want %v", kind, gotNames, decoded.TotalCount, wantNames)
		}
	}

	output, runError := runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "", "robots", "list", "--kind", "user")
	if runError != nil || strings.Contains(output, "robot$org-abc+ci") || strings.Contains(output, "managed by Ankra") {
		t.Fatalf("--kind user must list no managed robot and no managed footer: error=%v\n%s", runError, output)
	}

	_, usageError := runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "list", "--kind", "system")
	if exitCodeFor(usageError) != exitUsage || !strings.Contains(usageError.Error(), "--kind must be") {
		t.Fatalf("an unknown kind = %v, want a usage error", usageError)
	}
}

// A platform that predates managed robots lists only the member's own, with
// no kind: they read as the member's, and filter as such.
func TestRegistryRobotsListReadsAnOlderPlatformsRobotsAsTheMembersOwn(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "list", "--kind", "user")
	if runError != nil || !strings.Contains(output, "jenkins") || !strings.Contains(output, "user") ||
		strings.Contains(output, "Registry project:") {
		t.Fatalf("list against an older platform: error=%v\n%s", runError, output)
	}
	output, runError = runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "list", "--kind", "managed")
	if runError != nil || !strings.Contains(output, "No managed robot accounts.") {
		t.Fatalf("list --kind managed against an older platform: error=%v\n%s", runError, output)
	}
}

// --permission states the robot's own grants instead of a preset: the request
// carries them and no scope, repeats and commas both work, and stating a
// scope beside them is refused locally.
func TestRegistryRobotsCreateWithPermissionsAndExpiry(t *testing.T) {
	mock := &registryRobotsMock{list: registryRobotFleetFixture()}
	_, runError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup",
		"--permission", "repository:pull, Artifact:Delete", "--permission", "repository:pull",
		"--expires-in-days", "90")
	if runError != nil {
		t.Fatalf("create with permissions: %v", runError)
	}
	if mock.createRequest == nil || mock.createRequest.Scope != "" || mock.createRequest.ExpiresInDays != 90 ||
		strings.Join(mock.createRequest.Permissions, ",") != "repository:pull,artifact:delete" {
		t.Fatalf("request = %+v", mock.createRequest)
	}
	encoded, _ := json.Marshal(mock.createRequest)
	if string(encoded) != `{"name":"cleanup","permissions":["repository:pull","artifact:delete"],"expires_in_days":90}` {
		t.Fatalf("wire body = %s", encoded)
	}

	mock = &registryRobotsMock{list: registryRobotFleetFixture()}
	_, unknownError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup", "--permission", "member:create")
	if exitCodeFor(unknownError) != exitUsage || !strings.Contains(unknownError.Error(), "choose from: repository:pull, artifact:delete") ||
		mock.createRequest != nil {
		t.Fatalf("a permission the platform does not offer: error=%v request=%+v", unknownError, mock.createRequest)
	}

	mock = &registryRobotsMock{}
	_, bothError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup", "--scope", "pull", "--permission", "artifact:delete")
	if exitCodeFor(bothError) != exitUsage || !strings.Contains(bothError.Error(), "alternatives") || mock.createRequest != nil {
		t.Fatalf("a scope beside permissions: error=%v request=%+v", bothError, mock.createRequest)
	}
	_, negativeError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup", "--expires-in-days", "-3")
	if exitCodeFor(negativeError) != exitUsage || mock.createRequest != nil {
		t.Fatalf("a negative expiry: error=%v request=%+v", negativeError, mock.createRequest)
	}
	_, longError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup", "--expires-in-days", "3651")
	if exitCodeFor(longError) != exitUsage || !strings.Contains(longError.Error(), "1 to 3650 days") || mock.createRequest != nil {
		t.Fatalf("an expiry past the platform's ceiling: error=%v request=%+v", longError, mock.createRequest)
	}
	for _, empty := range []string{"", ",", " , "} {
		_, emptyError := runRegistryCommand(t, mock, "", "robots", "create", "cleanup", "--permission", empty)
		if exitCodeFor(emptyError) != exitUsage || !strings.Contains(emptyError.Error(), "names no permission") ||
			mock.createRequest != nil {
			t.Fatalf("--permission %q must not fall back to a push robot: error=%v request=%+v", empty, emptyError, mock.createRequest)
		}
	}

	mock = &registryRobotsMock{}
	if _, plainError := runRegistryCommand(t, mock, "", "robots", "create", "jenkins"); plainError != nil {
		t.Fatalf("plain create: %v", plainError)
	}
	encoded, _ = json.Marshal(mock.createRequest)
	if string(encoded) != `{"name":"jenkins","scope":"push"}` {
		t.Fatalf("a plain create must keep the wire body an older platform accepts, got %s", encoded)
	}
}

// 'permissions' lists what --permission accepts, from the platform's own
// catalogue, and says so plainly when the platform has none to offer.
func TestRegistryRobotsPermissionsListsTheCatalogue(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "", "robots", "permissions")
	if runError != nil || !strings.Contains(output, "artifact:delete") || !strings.Contains(output, "which a cleanup job needs") {
		t.Fatalf("permissions: error=%v\n%s", runError, output)
	}
	output, runError = runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "", "robots", "permissions", "-o", "json")
	var decoded struct {
		Permissions []client.RegistryRobotPermission `json:"permissions"`
	}
	if runError != nil || json.Unmarshal([]byte(output), &decoded) != nil || len(decoded.Permissions) != 2 {
		t.Fatalf("permissions -o json: error=%v\n%s", runError, output)
	}
	output, runError = runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "permissions")
	if runError != nil || !strings.Contains(output, "two presets only") {
		t.Fatalf("permissions against an older platform: error=%v\n%s", runError, output)
	}
	output, runError = runRegistryCommand(t, &registryRobotsMock{}, "", "robots", "permissions", "-o", "json")
	if runError != nil || strings.TrimSpace(output) == "" || !strings.Contains(output, `"permissions": []`) {
		t.Fatalf("permissions -o json against an older platform must answer an empty list, not null: error=%v\n%s", runError, output)
	}
}

// 'get' reads a managed robot by the name the listing gives it and says whose
// it is.
func TestRegistryRobotsGetShowsAManagedRobot(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryRobotsMock{list: registryRobotFleetFixture()}, "", "robots", "get", "app-23298741")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	for _, expected := range []string{"application (managed by Ankra)", "commerce (23298741)", "artifact.customer.test",
		"commerce-images", "push and pull", "Expires:     never"} {
		if !strings.Contains(output, expected) {
			t.Errorf("get lacks %q:\n%s", expected, output)
		}
	}
}

// A secret the platform could not store is the only copy: the warning reaches
// the person on stderr and never pollutes the structured stdout.
func TestRegistryRobotsRotateRelaysTheOnlyCopyWarning(t *testing.T) {
	const warning = "The registry accepted the new secret, but the platform could not store it"
	stdout, stderr, runError := runRegistryCommandSplit(t, &registryRobotsMock{warning: warning}, "", "robots", "rotate", "jenkins", "--yes")
	if runError != nil || !strings.Contains(stderr, "Warning: "+warning) || strings.Contains(stdout, "Warning:") {
		t.Fatalf("rotate: error=%v\nstdout=%s\nstderr=%s", runError, stdout, stderr)
	}
	stdout, _, runError = runRegistryCommandSplit(t, &registryRobotsMock{warning: warning}, "", "robots", "rotate", "jenkins", "--yes", "-o", "json")
	var decoded map[string]any
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil || decoded["warning"] != warning {
		t.Fatalf("rotate -o json: error=%v\nstdout=%s", runError, stdout)
	}
}

func TestRegistryRobotSummaries(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	future, past, unreadable := "2026-12-30T12:00:00Z", "2026-10-01T12:00:00Z", "soon"
	expiryCases := []struct {
		expiresAt *string
		want      string
	}{
		{nil, "never"},
		{&future, "2026-12-30T12:00:00Z"},
		{&past, "2026-10-01T12:00:00Z (expired)"},
		{&unreadable, "soon"},
	}
	for _, testCase := range expiryCases {
		if got := registryRobotExpirySummary(client.RegistryRobot{ExpiresAt: testCase.expiresAt}, now); got != testCase.want {
			t.Errorf("expiry summary = %q, want %q", got, testCase.want)
		}
	}
	accessCases := map[string]client.RegistryRobot{
		"push and pull":   {Scope: "push"},
		"pull":            {Scope: "pull"},
		"scan:read":       {Scope: "custom", Permissions: []string{"scan:read"}},
		"custom":          {Scope: "custom"},
		"-":               {},
		"repository:list": {Permissions: []string{"repository:list"}},
	}
	for want, robot := range accessCases {
		if got := registryRobotAccessSummary(robot); got != want {
			t.Errorf("access summary of %+v = %q, want %q", robot, got, want)
		}
	}
	if got := registryRobotProjectsSummary(client.RegistryRobot{Project: "org-abc"}); got != "org-abc" {
		t.Errorf("projects summary of an older platform's robot = %q", got)
	}
	if got := registryRobotProjectsSummary(client.RegistryRobot{}); got != "-" {
		t.Errorf("projects summary of a robot with no recorded project = %q", got)
	}
}

// A platform that predates permissions and expiry ignores the fields it does
// not know and would mint a push-and-pull robot that never expires. The
// create is refused before any robot exists rather than handing out a login
// broader than the one asked for.
func TestRegistryRobotsCreateRefusesPermissionsAndExpiryOnAnOlderPlatform(t *testing.T) {
	for _, arguments := range [][]string{
		{"robots", "create", "cleanup", "--permission", "repository:pull"},
		{"robots", "create", "contractor", "--scope", "pull", "--expires-in-days", "30"},
	} {
		mock := &registryRobotsMock{}
		_, runError := runRegistryCommand(t, mock, "", arguments...)
		if runError == nil || !strings.Contains(runError.Error(), "does not support robot permissions or an expiry yet") ||
			mock.createRequest != nil {
			t.Fatalf("%v against an older platform: error=%v request=%+v", arguments, runError, mock.createRequest)
		}
	}
}
