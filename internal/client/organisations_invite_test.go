package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestInviteCarriesTheProjectGrantOnlyForAProjectMember(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"success":true,"message":"Invitation sent"}`)
	projectID := testProjectID
	if _, err := testClient.InviteUserToOrganisation(InviteUserRequest{
		OrganisationID: "3b7dccca-0788-4470-9910-19478ae345ae", InviteeEmail: "dev@example.com",
		Role: "project", ProjectID: &projectID, ProjectRoleSlug: "operator",
	}); err != nil {
		t.Fatalf("invite: %v", err)
	}
	body := decodeBody(t, platform.last(t).body)
	if body["role"] != "project" || body["project_id"] != testProjectID || body["project_role_slug"] != "operator" {
		t.Fatalf("project invite body = %v", body)
	}
	if _, err := testClient.InviteUserToOrganisation(InviteUserRequest{
		OrganisationID: "3b7dccca-0788-4470-9910-19478ae345ae", InviteeEmail: "dev@example.com", Role: "member",
	}); err != nil {
		t.Fatal(err)
	}
	body = decodeBody(t, platform.last(t).body)
	for _, field := range []string{"project_id", "project_role_slug"} {
		if _, present := body[field]; present {
			t.Fatalf("an organisation-wide invite must not send %s: %v", field, body)
		}
	}
}

func TestInviteRefusalsKeepThePlatformsWords(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		wantDenied bool
		wantStatus int
		wantText   string
	}{
		{name: "project not found", status: http.StatusNotFound, body: `{"detail":"Project not found in this organisation"}`,
			wantStatus: http.StatusNotFound, wantText: "Project not found in this organisation"},
		{name: "grant ceiling", status: http.StatusForbidden, body: `{"detail":"You cannot grant a role with permissions you do not hold","reason":"grant_ceiling"}`,
			wantDenied: true, wantText: "You cannot grant a role with permissions you do not hold"},
		{name: "validation", status: http.StatusUnprocessableEntity, body: `{"detail":[{"loc":["body","project_id"],"msg":"Field required","type":"missing"}]}`,
			wantStatus: http.StatusUnprocessableEntity, wantText: "project_id"},
		{name: "unauthorised", status: http.StatusUnauthorized, body: `{"detail":"Not authenticated"}`, wantText: ErrUnauthorized.Error()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient, _ := newRecordingClient(t, testCase.status, testCase.body)
			_, err := testClient.InviteUserToOrganisation(InviteUserRequest{OrganisationID: "o", InviteeEmail: "dev@example.com", Role: "project"})
			if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("error = %v, want it to carry %q", err, testCase.wantText)
			}
			var denied *PermissionDeniedError
			if testCase.wantDenied != errors.As(err, &denied) {
				t.Fatalf("permission-denied mismatch for %v", err)
			}
			var unexpected *UnexpectedResponseError
			if testCase.wantStatus != 0 && (!errors.As(err, &unexpected) || unexpected.StatusCode != testCase.wantStatus) {
				t.Fatalf("error = %#v, want status %d", err, testCase.wantStatus)
			}
		})
	}
}
