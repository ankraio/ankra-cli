package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

const (
	testAnkraUserID   = "5f0c3b2a-7d1e-4c6b-9a8f-2e3d4c5b6a71"
	testAssignmentID  = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d"
	testOrganisation  = "3b7dccca-0788-4470-9910-19478ae345ae"
	testProjectAdmin  = "admin"
	testProjectMember = "project"
)

func TestCreateRoleAssignmentPostsTheScopedGrant(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK,
		`{"id":"`+testAssignmentID+`","ankra_user_id":"`+testAnkraUserID+`","role_slug":"admin","role_id":null,"scope_type":"project","scope_id":"`+testProjectID+`","created_at":""}`)
	roleSlug := testProjectAdmin
	scopeID := testProjectID
	assignment, err := testClient.CreateRoleAssignment(CreateRoleAssignmentRequest{
		AnkraUserID: testAnkraUserID, RoleSlug: &roleSlug, ScopeType: "project", ScopeID: &scopeID,
	})
	if err != nil {
		t.Fatalf("CreateRoleAssignment: %v", err)
	}
	if assignment.ID != testAssignmentID || assignment.ScopeType != "project" || assignment.ScopeID == nil || *assignment.ScopeID != testProjectID {
		t.Fatalf("assignment = %+v", assignment)
	}
	request := platform.last(t)
	if request.method != http.MethodPost || request.path != "/api/v1/org/organisation/assignments" {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(request.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["ankra_user_id"] != testAnkraUserID || body["role_slug"] != "admin" || body["scope_type"] != "project" || body["scope_id"] != testProjectID {
		t.Fatalf("body = %v", body)
	}
	if _, present := body["role_id"]; present {
		t.Fatalf("a built-in role sends no role_id: %v", body)
	}
}

func TestCreateRoleAssignmentOrganisationScopeSendsNullScopeID(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"id":"`+testAssignmentID+`","scope_type":"organisation"}`)
	roleSlug := "viewer"
	if _, err := testClient.CreateRoleAssignment(CreateRoleAssignmentRequest{AnkraUserID: testAnkraUserID, RoleSlug: &roleSlug, ScopeType: "organisation"}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(platform.last(t).body), &body); err != nil {
		t.Fatal(err)
	}
	if value, present := body["scope_id"]; !present || value != nil {
		t.Fatalf("scope_id must be null for the organisation scope: %v", body)
	}
}

func TestCreateRoleAssignmentKeepsTheHandlerNotFound(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusNotFound, `{"detail":"Member not found in organisation"}`)
	_, err := testClient.CreateRoleAssignment(CreateRoleAssignmentRequest{AnkraUserID: testAnkraUserID, ScopeType: "organisation"})
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.Detail != "Member not found in organisation" {
		t.Fatalf("err = %v, want the handler's own not-found", err)
	}
}

func TestCreateRoleAssignmentRouteMissing(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusNotFound, `404 page not found`)
	if _, err := testClient.CreateRoleAssignment(CreateRoleAssignmentRequest{}); !errors.Is(err, ErrRoleAssignmentsRouteMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateMemberRolePatchesTheTokenTwin(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"success":true,"message":"Role updated"}`)
	projectID := testProjectID
	projectRole := "operator"
	response, err := testClient.UpdateMemberRole(UpdateMemberRoleRequest{
		UserID: testAnkraUserID, OrganisationID: testOrganisation, Role: testProjectMember,
		ProjectID: &projectID, ProjectRoleSlug: &projectRole,
	})
	if err != nil {
		t.Fatalf("UpdateMemberRole: %v", err)
	}
	if !response.Success || response.Message != "Role updated" {
		t.Fatalf("response = %+v", response)
	}
	request := platform.last(t)
	if request.method != http.MethodPatch || request.path != "/api/v1/org/organisation/member/role" {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(request.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["user_id"] != testAnkraUserID || body["organisation_id"] != testOrganisation || body["role"] != "project" ||
		body["project_id"] != testProjectID || body["project_role_slug"] != "operator" {
		t.Fatalf("body = %v", body)
	}
}

func TestUpdateMemberRoleOmitsProjectFieldsForOtherRoles(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"success":true,"message":""}`)
	if _, err := testClient.UpdateMemberRole(UpdateMemberRoleRequest{UserID: testAnkraUserID, OrganisationID: testOrganisation, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(platform.last(t).body), &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["project_id"]; present {
		t.Fatalf("project_id is only sent with role project: %v", body)
	}
	if _, present := body["project_role_slug"]; present {
		t.Fatalf("project_role_slug is only sent with role project: %v", body)
	}
}

func TestUpdateMemberRoleDetectsAPlatformWithoutTheTwin(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		body   string
	}{
		{"bare 404", http.StatusNotFound, `404 page not found`},
		{"router 404 detail", http.StatusNotFound, `{"detail":"Not Found"}`},
		{"405", http.StatusMethodNotAllowed, `{"detail":"Method Not Allowed"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient, _ := newRecordingClient(t, testCase.status, testCase.body)
			_, err := testClient.UpdateMemberRole(UpdateMemberRoleRequest{UserID: testAnkraUserID, OrganisationID: testOrganisation, Role: "admin"})
			if !errors.Is(err, ErrMemberRoleRouteMissing) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUpdateMemberRoleKeepsARealRefusal(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusConflict, `{"detail":"An owner cannot become project-only","reason":"owner_project_only"}`)
	_, err := testClient.UpdateMemberRole(UpdateMemberRoleRequest{UserID: testAnkraUserID, OrganisationID: testOrganisation, Role: "project"})
	if err == nil || errors.Is(err, ErrMemberRoleRouteMissing) {
		t.Fatalf("err = %v, want the platform's refusal", err)
	}
	testClient, _ = newRecordingClient(t, http.StatusNotFound, `{"detail":"Project not found"}`)
	_, err = testClient.UpdateMemberRole(UpdateMemberRoleRequest{UserID: testAnkraUserID, OrganisationID: testOrganisation, Role: "project"})
	if err == nil || errors.Is(err, ErrMemberRoleRouteMissing) {
		t.Fatalf("err = %v, want the handler's not-found", err)
	}
}
