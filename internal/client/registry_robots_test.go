package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Lane parity: each method must hit the exact route and body the platform
// serves (cluster#3058). A path or method typo here fails as a 404 against
// a real platform and passes every command-wiring test.
func TestRegistryRobotsLaneParity(t *testing.T) {
	type call struct {
		method string
		path   string
		body   string
	}
	var calls []call
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, call{request.Method, request.URL.Path, string(bodyBytes)})
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token on %s %s", request.Method, request.URL.Path)
		}
		robot := map[string]any{
			"name": "jenkins", "robot_name": "robot$org-abc+user-jenkins", "host": "artifact.ankra.cloud",
			"project": "org-abc", "scope": "push", "description": "", "credential_name": "ankra-harbor-robot-jenkins",
			"created_at": "2026-09-14T12:00:00Z", "rotated_at": nil,
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/org/registry-robots":
			robot["secret"] = "s3cret"
			robot["docker_login"] = "docker login artifact.ankra.cloud -u 'robot$org-abc+user-jenkins' -p 's3cret'"
			jsonResponse(t, writer, http.StatusCreated, robot)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-robots":
			jsonResponse(t, writer, http.StatusOK, map[string]any{"robots": []any{robot}, "total_count": 1})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-robots/jenkins":
			jsonResponse(t, writer, http.StatusOK, robot)
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/org/registry-robots/jenkins/rotate":
			robot["secret"] = "r0tated"
			robot["docker_login"] = "docker login ..."
			jsonResponse(t, writer, http.StatusOK, robot)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v1/org/registry-robots/jenkins":
			jsonResponse(t, writer, http.StatusOK, map[string]any{"success": true})
		default:
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusTeapot)
		}
	})

	created, createError := client.CreateRegistryRobot(context.Background(), CreateRegistryRobotRequest{Name: "jenkins", Scope: "push"})
	if createError != nil || created.Secret != "s3cret" || created.RobotName != "robot$org-abc+user-jenkins" {
		t.Fatalf("create: %+v %v", created, createError)
	}
	list, listError := client.ListRegistryRobots(context.Background())
	if listError != nil || list.TotalCount != 1 || len(list.Robots) != 1 {
		t.Fatalf("list: %+v %v", list, listError)
	}
	robot, getError := client.GetRegistryRobot(context.Background(), "jenkins")
	if getError != nil || robot.CredentialName != "ankra-harbor-robot-jenkins" {
		t.Fatalf("get: %+v %v", robot, getError)
	}
	rotated, rotateError := client.RotateRegistryRobotSecret(context.Background(), "jenkins")
	if rotateError != nil || rotated.Secret != "r0tated" {
		t.Fatalf("rotate: %+v %v", rotated, rotateError)
	}
	if deleteError := client.DeleteRegistryRobot(context.Background(), "jenkins"); deleteError != nil {
		t.Fatalf("delete: %v", deleteError)
	}

	expected := []call{
		{http.MethodPost, "/api/v1/org/registry-robots", `{"name":"jenkins","scope":"push"}`},
		{http.MethodGet, "/api/v1/org/registry-robots", ""},
		{http.MethodGet, "/api/v1/org/registry-robots/jenkins", ""},
		{http.MethodPost, "/api/v1/org/registry-robots/jenkins/rotate", ""},
		{http.MethodDelete, "/api/v1/org/registry-robots/jenkins", ""},
	}
	if len(calls) != len(expected) {
		t.Fatalf("calls = %+v", calls)
	}
	for index, expectedCall := range expected {
		if calls[index] != expectedCall {
			t.Fatalf("call %d = %+v, want %+v", index, calls[index], expectedCall)
		}
	}
}

// The platform's refusals name their reason; the sentence must reach the
// user verbatim rather than a bare status.
func TestRegistryRobotsRelayRefusals(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "A robot with that name already exists in this organisation"})
	})
	_, createError := client.CreateRegistryRobot(context.Background(), CreateRegistryRobotRequest{Name: "jenkins"})
	if createError == nil || !strings.Contains(createError.Error(), "already exists") {
		t.Fatalf("error = %v", createError)
	}
}

// The listing a current platform answers decodes in full - managed robots,
// their application, grants, expiry, the registry and the catalogue - and the
// one an older platform answers decodes to robots that read as the member's
// own.
func TestRegistryRobotsListDecodesManagedRobotsAndAnOlderPlatform(t *testing.T) {
	current := `{"robots":[
		{"name":"cleanup","kind":"user","managed":false,"robot_name":"robot$org-abc+user-cleanup","host":"artifact.ankra.cloud",
		 "project":"org-abc","projects":["org-abc"],"scope":"custom","permissions":["repository:pull","artifact:delete"],
		 "description":"","credential_name":"ankra-harbor-robot-cleanup","application":null,
		 "created_at":"2026-09-30T12:00:00Z","rotated_at":null,"expires_at":"2026-12-29T12:00:00Z"},
		{"name":"app-1","kind":"application","managed":true,"robot_name":"robot$org-abc+app-1","host":"artifact.ankra.cloud",
		 "project":"org-abc","projects":["org-abc"],"scope":"push","permissions":["repository:pull","repository:push"],
		 "description":"Managed by Ankra","credential_name":"ankra-harbor-app-1","application":{"id":"1","name":"shipfortune"},
		 "created_at":"2026-08-25T09:00:00Z","rotated_at":null,"expires_at":null}],
		"total_count":2,"registry":{"host":"artifact.ankra.cloud","project":"org-abc"},
		"available_permissions":[{"permission":"repository:pull","label":"Pull","description":"Pull images and charts from the project."}]}`
	older := `{"robots":[{"name":"jenkins","robot_name":"robot$org-abc+user-jenkins","host":"artifact.ankra.cloud",
		"project":"org-abc","scope":"push","description":"","credential_name":"ankra-harbor-robot-jenkins",
		"created_at":"2026-09-14T12:00:00Z","rotated_at":null}],"total_count":1}`
	body := current
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	})

	list, listError := client.ListRegistryRobots(context.Background())
	if listError != nil || len(list.Robots) != 2 || list.Registry == nil || list.Registry.Project != "org-abc" ||
		len(list.AvailablePermissions) != 1 {
		t.Fatalf("current listing: %+v %v", list, listError)
	}
	cleanup, application := list.Robots[0], list.Robots[1]
	if cleanup.Managed || cleanup.KindOrUser() != RegistryRobotKindUser || cleanup.Scope != RegistryRobotScopeCustom ||
		len(cleanup.Permissions) != 2 || cleanup.ExpiresAt == nil || *cleanup.ExpiresAt != "2026-12-29T12:00:00Z" {
		t.Fatalf("member's robot = %+v", cleanup)
	}
	if !application.Managed || application.KindOrUser() != RegistryRobotKindApplication ||
		application.Application == nil || application.Application.Name != "shipfortune" || application.ExpiresAt != nil {
		t.Fatalf("application robot = %+v", application)
	}

	body = older
	list, listError = client.ListRegistryRobots(context.Background())
	if listError != nil || len(list.Robots) != 1 || list.Registry != nil || list.AvailablePermissions != nil {
		t.Fatalf("older listing: %+v %v", list, listError)
	}
	if robot := list.Robots[0]; robot.Managed || robot.KindOrUser() != RegistryRobotKindUser || robot.Kind != "" {
		t.Fatalf("an older platform's robot = %+v", robot)
	}
}

// The refusals this lane added - a robot Ankra manages, an expired robot -
// reach the user as the platform wrote them.
func TestRegistryRobotsRelayTheManagedRefusal(t *testing.T) {
	const detail = "This robot is managed by Ankra and cannot be rotated or revoked here"
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(writer).Encode(map[string]string{"detail": detail})
	})
	if _, rotateError := client.RotateRegistryRobotSecret(context.Background(), "ci"); rotateError == nil ||
		!strings.Contains(rotateError.Error(), detail) {
		t.Fatalf("rotate error = %v", rotateError)
	}
	if deleteError := client.DeleteRegistryRobot(context.Background(), "ci"); deleteError == nil ||
		!strings.Contains(deleteError.Error(), detail) {
		t.Fatalf("delete error = %v", deleteError)
	}
}

// Lane parity for registry projects: each method hits the exact route and
// body the platform serves, and a route the platform does not register
// answers an error that carries no backend detail, which is how callers tell
// "this platform predates projects" from "no such project".
func TestRegistryProjectsLaneParity(t *testing.T) {
	type call struct {
		method string
		path   string
		body   string
	}
	var calls []call
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, call{request.Method, request.URL.Path, string(bodyBytes)})
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-projects":
			jsonResponse(t, writer, http.StatusOK, map[string]any{
				"projects": []any{
					map[string]any{"name": "default", "project": "org-abc", "host": "artifact.ankra.cloud", "is_default": true,
						"repository_count": 12, "robot_count": 3, "created_at": "2026-07-22T17:16:00.000Z"},
					map[string]any{"name": "staging", "project": "org-abc-staging", "host": "artifact.ankra.cloud", "is_default": false,
						"repository_count": 0, "robot_count": 1, "created_at": ""},
				},
				"total_count": 2, "extra_project_limit": 5,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/org/registry-projects":
			jsonResponse(t, writer, http.StatusCreated, map[string]any{"name": "edge", "project": "org-abc-edge", "host": "artifact.ankra.cloud"})
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v1/org/registry-projects/edge":
			jsonResponse(t, writer, http.StatusOK, map[string]any{"success": true})
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v1/org/registry-projects/busy":
			jsonResponse(t, writer, http.StatusConflict, map[string]any{"detail": "Robot accounts are still bound to this project; revoke them first"})
		default:
			http.NotFound(writer, request)
		}
	})

	list, listError := client.ListRegistryProjects(context.Background())
	if listError != nil || len(list.Projects) != 2 || !list.Projects[0].IsDefault || list.Projects[0].RepositoryCount != 12 ||
		list.Projects[1].Name != "staging" || list.ExtraProjectLimit != 5 {
		t.Fatalf("list: %+v %v", list, listError)
	}
	created, createError := client.CreateRegistryProject(context.Background(), "edge")
	if createError != nil || created.Project != "org-abc-edge" {
		t.Fatalf("create: %+v %v", created, createError)
	}
	if deleteError := client.DeleteRegistryProject(context.Background(), "edge"); deleteError != nil {
		t.Fatalf("delete: %v", deleteError)
	}
	if busyError := client.DeleteRegistryProject(context.Background(), "busy"); busyError == nil || !strings.Contains(busyError.Error(), "still bound") {
		t.Fatalf("a refused delete must relay the platform's sentence, got %v", busyError)
	}

	expected := []call{
		{http.MethodGet, "/api/v1/org/registry-projects", ""},
		{http.MethodPost, "/api/v1/org/registry-projects", `{"name":"edge"}`},
		{http.MethodDelete, "/api/v1/org/registry-projects/edge", ""},
		{http.MethodDelete, "/api/v1/org/registry-projects/busy", ""},
	}
	for index, expectedCall := range expected {
		if index >= len(calls) || calls[index] != expectedCall {
			t.Fatalf("call %d = %+v, want %+v", index, calls, expectedCall)
		}
	}

	unserved := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) { http.NotFound(writer, request) })
	_, unservedError := unserved.ListRegistryProjects(context.Background())
	var unexpected *UnexpectedResponseError
	if !errors.As(unservedError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("an unregistered route = %v, want a 404 with no backend detail", unservedError)
	}
}
