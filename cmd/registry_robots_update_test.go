package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// updatableRobot reaches first with pull and second with its own permissions.
func updatableRobot() *client.RegistryRobot {
	robot := registryRobotFixture()
	robot.Registry = "harbor"
	robot.ProjectAccess = []client.RegistryRobotProjectAccess{
		{Project: "first", Scope: client.RegistryRobotScopePull},
		{Project: "second", Scope: client.RegistryRobotScopeCustom, Permissions: []string{"repository:pull", "artifact:delete"}},
	}
	return &robot
}

func TestRegistryRobotsUpdateKeepsWhatItHoldsAndGivesNewProjectsTheScope(t *testing.T) {
	robot := updatableRobot()
	mock := &registryRobotsMock{current: robot, updated: &client.RegistryRobotProjectsUpdated{
		RegistryRobotWithSecret: client.RegistryRobotWithSecret{RegistryRobot: *robot},
	}}
	output, runError := runRegistryCommand(t, mock, "", "robots", "update", "consumer", "--registry-project", "first,second", "--registry-project", "third")
	if runError != nil || !strings.Contains(output, "keep working") {
		t.Fatalf("update: error=%v\n%s", runError, output)
	}
	encoded, _ := json.Marshal(mock.updateRequest)
	if string(encoded) != `{"projects":[{"project":"first","scope":"pull"},`+
		`{"project":"second","permissions":["repository:pull","artifact:delete"]},`+
		`{"project":"third","scope":"pull"}],"allow_new_secret":false}` {
		t.Fatalf("wire body = %s", encoded)
	}
}

func TestRegistryRobotsUpdateKeepsAccessWhateverCaseThePlatformAnswers(t *testing.T) {
	grants := registryRobotUpdatedGrants(&client.RegistryRobot{Registry: "harbor", ProjectAccess: []client.RegistryRobotProjectAccess{
		{Project: "First", Scope: client.RegistryRobotScopePush},
	}}, []string{"first"}, client.RegistryRobotScopePull)
	if len(grants) != 1 || grants[0].Scope != client.RegistryRobotScopePush {
		t.Fatalf("grants = %+v", grants)
	}
}

// Without the access it holds, every project would read as new and be reset.
func TestRegistryRobotsUpdateRefusesARobotWhoseAccessItCannotRead(t *testing.T) {
	robot := updatableRobot()
	robot.ProjectAccess = nil
	mock := &registryRobotsMock{current: robot}
	_, runError := runRegistryCommand(t, mock, "", "robots", "update", "consumer", "--registry-project", "first")
	if runError == nil || !strings.Contains(runError.Error(), "cannot be kept") || mock.updateRequest != nil {
		t.Fatalf("error=%v request=%+v", runError, mock.updateRequest)
	}
}

func TestRegistryRobotsUpdateSaysHowToAcceptANewSecret(t *testing.T) {
	mock := &registryRobotsMock{current: updatableRobot(), updateFail: errors.New("This change needs a new secret: confirm it")}
	_, runError := runRegistryCommand(t, mock, "", "robots", "update", "consumer", "--registry-project", "first")
	if runError == nil || !strings.Contains(runError.Error(), "--new-secret") || mock.updateRequest.AllowNewSecret {
		t.Fatalf("error=%v request=%+v", runError, mock.updateRequest)
	}
}

func TestRegistryRobotsUpdateWithANewSecretAsksAndShowsItOnce(t *testing.T) {
	robot := updatableRobot()
	answer := &client.RegistryRobotProjectsUpdated{SecretChanged: true,
		RegistryRobotWithSecret: client.RegistryRobotWithSecret{RegistryRobot: *robot, Secret: "s3cret"}}

	declined := &registryRobotsMock{current: robot, updated: answer}
	_, declinedError := runRegistryCommand(t, declined, "n\n", "robots", "update", "consumer", "--registry-project", "first", "--new-secret")
	if exitCodeFor(declinedError) != exitCancelled || declined.updateRequest != nil {
		t.Fatalf("declined: error=%v request=%+v", declinedError, declined.updateRequest)
	}

	confirmed := &registryRobotsMock{current: robot, updated: answer}
	output, runError := runRegistryCommand(t, confirmed, "", "robots", "update", "consumer", "--registry-project", "first", "--new-secret", "--yes")
	if runError != nil || !confirmed.updateRequest.AllowNewSecret || !strings.Contains(output, "s3cret") {
		t.Fatalf("confirmed: error=%v request=%+v\n%s", runError, confirmed.updateRequest, output)
	}
}

func TestRegistryRobotsUpdateRefusesAUsageMistakeBeforeAskingThePlatform(t *testing.T) {
	var manyProjects []string
	for range registryRobotMaximumProjects + 1 {
		manyProjects = append(manyProjects, "project-"+strings.Repeat("x", len(manyProjects)+1))
	}
	for name, arguments := range map[string][]string{
		"no project":    {"robots", "update", "consumer"},
		"too many":      {"robots", "update", "consumer", "--registry-project", strings.Join(manyProjects, ",")},
		"unknown scope": {"robots", "update", "consumer", "--registry-project", "first", "--scope", "admin"},
	} {
		mock := &registryRobotsMock{current: updatableRobot()}
		_, usageError := runRegistryCommand(t, mock, "", arguments...)
		if exitCodeFor(usageError) != exitUsage || mock.updateRequest != nil {
			t.Fatalf("%s: error=%v request=%+v", name, usageError, mock.updateRequest)
		}
	}
}
