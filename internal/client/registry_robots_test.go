package client

import (
	"context"
	"encoding/json"
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
