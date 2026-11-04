package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// severalProjectsRobot is a robot as the platform answers one that reaches the
// projects named.
func severalProjectsRobot(registry string, projectNames ...string) *client.RegistryRobotWithSecret {
	robot := &client.RegistryRobotWithSecret{RegistryRobot: registryRobotFixture(), Secret: "s3cret", DockerLogin: "docker login ..."}
	robot.Registry = registry
	for _, projectName := range projectNames {
		robot.ProjectAccess = append(robot.ProjectAccess, client.RegistryRobotProjectAccess{Project: projectName, ProjectName: projectName})
	}
	return robot
}

func TestRegistryRobotsCreateOnSeveralProjectsOfAnIntegratedRegistry(t *testing.T) {
	mock := &registryRobotsMock{created: severalProjectsRobot("smartoptics-harbor", "commerce-charts", "commerce-images")}
	output, runError := runRegistryCommand(t, mock, "", "robots", "create", "consumer", "--scope", "pull",
		"--registry", "smartoptics-harbor", "--admin-credential", "smartoptics-harbor-robot-admin",
		"--registry-project", "Commerce-Charts,commerce-images", "--registry-project", "commerce-charts")
	if runError != nil || mock.deleted != "" || !strings.Contains(output, "s3cret") {
		t.Fatalf("create on several projects: error=%v deleted=%q\n%s", runError, mock.deleted, output)
	}
	encoded, _ := json.Marshal(mock.createRequest)
	if string(encoded) != `{"name":"consumer","projects":[{"project":"commerce-charts","scope":"pull"},`+
		`{"project":"commerce-images","scope":"pull"}],"registry":"smartoptics-harbor",`+
		`"admin_credential_name":"smartoptics-harbor-robot-admin"}` {
		t.Fatalf("wire body = %s", encoded)
	}
}

func TestRegistryRobotsCreateOnSeveralProjectsStatesPermissionsOnEach(t *testing.T) {
	mock := &registryRobotsMock{list: registryRobotFleetFixture(), created: severalProjectsRobot("harbor", "first", "second")}
	mock.created.Permissions = []string{"repository:pull", "artifact:delete"}
	if _, runError := runRegistryCommand(t, mock, "", "robots", "create", "reader", "--permission", "repository:pull,artifact:delete",
		"--registry", "harbor", "--admin-credential", "admin", "--registry-project", "first,second"); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	if mock.createRequest.Scope != "" || len(mock.createRequest.Permissions) != 0 || mock.createRequest.Project != "" {
		t.Fatalf("grants stated on the robot as well as on each project: %+v", mock.createRequest)
	}
	for _, grant := range mock.createRequest.Projects {
		if len(grant.Permissions) != 2 || grant.Scope != "" {
			t.Fatalf("grant = %+v", grant)
		}
	}
}

// A platform that does not know the list binds the robot to one project: the
// login cannot reach what was asked for, so it is revoked before its secret is
// shown.
func TestRegistryRobotsCreateRevokesARobotThatDidNotGetEveryProject(t *testing.T) {
	for _, answered := range [][]string{{"first"}, {"first", "third"}} {
		mock := &registryRobotsMock{created: severalProjectsRobot("harbor", answered...)}
		output, runError := runRegistryCommand(t, mock, "", "robots", "create", "reader",
			"--registry", "harbor", "--admin-credential", "admin", "--registry-project", "first,second")
		if runError == nil || mock.deleted != mock.created.Name || strings.Contains(output, "s3cret") ||
			!strings.Contains(runError.Error(), "--registry-project") {
			t.Fatalf("answered %v: error=%v deleted=%q\n%s", answered, runError, mock.deleted, output)
		}
	}
}

func TestRegistryRobotsCreateRefusesMoreProjectsThanOneRobotReaches(t *testing.T) {
	var projectNames []string
	for projectIndex := range registryRobotMaximumProjects + 1 {
		projectNames = append(projectNames, fmt.Sprintf("project-%d", projectIndex))
	}
	mock := &registryRobotsMock{}
	_, usageError := runRegistryCommand(t, mock, "", "robots", "create", "reader", "--registry", "harbor",
		"--admin-credential", "admin", "--registry-project", strings.Join(projectNames, ","))
	if exitCodeFor(usageError) != exitUsage || mock.createRequest != nil {
		t.Fatalf("error=%v request=%+v", usageError, mock.createRequest)
	}
}

// One project keeps the body it always had, repeats and blanks included.
func TestRegistryRobotsCreateWithOneProjectKeepsItsBody(t *testing.T) {
	mock := &registryRobotsMock{created: severalProjectsRobot("harbor", "first")}
	if _, runError := runRegistryCommand(t, mock, "", "robots", "create", "reader", "--scope", "pull",
		"--registry", "harbor", "--admin-credential", "admin", "--registry-project", "first,FIRST, "); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	encoded, _ := json.Marshal(mock.createRequest)
	if string(encoded) != `{"name":"reader","scope":"pull","project":"first","registry":"harbor","admin_credential_name":"admin"}` {
		t.Fatalf("wire body = %s", encoded)
	}
}
