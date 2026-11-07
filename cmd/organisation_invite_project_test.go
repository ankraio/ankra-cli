package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

type projectInviteMock struct {
	projectsMock
	invites    []client.InviteUserRequest
	inviteFail error
}

func (m *projectInviteMock) InviteUserToOrganisation(request client.InviteUserRequest) (*client.InviteUserResponse, error) {
	m.invites = append(m.invites, request)
	if m.inviteFail != nil {
		return nil, m.inviteFail
	}
	return &client.InviteUserResponse{Success: true}, nil
}

// runInvite runs `ankra org invite` with a saved organisation selection, so
// the command needs no organisation listing.
func runInvite(t *testing.T, mock *projectInviteMock, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, "")
	name := "Ankra"
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	reset := func() { resetGlobalProjectFlag(t); resetTreeFlags(t, orgInviteCmd) }
	reset()
	t.Cleanup(reset)
	var runError error
	stdout := captureStdout(t, func() {
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs(append([]string{"org", "invite"}, args...))
		runError = rootCmd.Execute()
	})
	return stdout, runError
}

func TestOrgInviteProjectOnlyMember(t *testing.T) {
	mock := &projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}}
	stdout, err := runInvite(t, mock, "contractor@example.com", "--role", "project", "--project", "edge", "--project-role", "operator")
	if err != nil {
		t.Fatalf("org invite --role project: %v", err)
	}
	if len(mock.invites) != 1 {
		t.Fatalf("invites = %v", mock.invites)
	}
	invite := mock.invites[0]
	if invite.Role != "project" || invite.ProjectID == nil || *invite.ProjectID != projectTestEdgeID || invite.ProjectRoleSlug != "operator" {
		t.Fatalf("invite = %+v", invite)
	}
	if !strings.Contains(stdout, "project-only: operator in project "+projectTestEdgeID) {
		t.Fatalf("stdout = %s", stdout)
	}

	mock = &projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}}
	if _, err := runInvite(t, mock, "dev@example.com", "--role", "project", "--project", projectTestEdgeID); err != nil {
		t.Fatal(err)
	}
	if mock.invites[0].ProjectRoleSlug != "member" {
		t.Fatalf("the project role defaults to member, got %q", mock.invites[0].ProjectRoleSlug)
	}

	mock = &projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}}
	if _, err := runInvite(t, mock, "dev@example.com", "--role", "admin"); err != nil {
		t.Fatal(err)
	}
	if mock.invites[0].ProjectID != nil || mock.invites[0].ProjectRoleSlug != "" {
		t.Fatalf("an organisation-wide invite carries no project grant: %+v", mock.invites[0])
	}
}

func TestOrgInviteProjectOnlyMemberUsageErrors(t *testing.T) {
	for _, arguments := range [][]string{
		{"dev@example.com", "--role", "project"},
		{"dev@example.com", "--role", "project", "--project", "edge", "--project-role", "owner"},
		{"dev@example.com", "--role", "member", "--project-role", "viewer"},
	} {
		mock := &projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}}
		_, err := runInvite(t, mock, arguments...)
		if err == nil || exitCodeFor(err) != exitUsage || len(mock.invites) != 0 {
			t.Errorf("%v: error = %v (exit %d), invites %v", arguments, err, exitCodeFor(err), mock.invites)
		}
	}
}

func TestOrgInviteProjectOnlyMemberRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		refusal  error
		wantExit int
		wantText string
	}{
		{name: "project not found", refusal: &client.UnexpectedResponseError{StatusCode: 404, Detail: "Project not found in this organisation"},
			wantExit: exitNotFound, wantText: "inviting user"},
		{name: "grant ceiling", refusal: &client.PermissionDeniedError{Detail: "You cannot grant a role with permissions you do not hold"},
			wantExit: exitForbidden, wantText: "You cannot grant a role"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}, inviteFail: testCase.refusal}
			_, err := runInvite(t, mock, "dev@example.com", "--role", "project", "--project", "edge")
			if err == nil || exitCodeFor(err) != testCase.wantExit || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("error = %v (exit %d)", err, exitCodeFor(err))
			}
		})
	}
}

func TestProjectIsAnAssignableRole(t *testing.T) {
	if !isValidAssignableRole("project") || toLegacyWireRole("project") != "project" || roleDescriptions["project"] == "" {
		t.Fatal("project must be assignable, sent as itself, and described in 'ankra org roles'")
	}
}
