package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	assignTestUserID      = "5f0c3b2a-7d1e-4c6b-9a8f-2e3d4c5b6a71"
	assignTestGroupID     = "6a7b8c9d-0e1f-4a2b-8c3d-4e5f6a7b8c9d"
	assignTestCustomRole  = "7b8c9d0e-1f2a-4b3c-9d4e-5f6a7b8c9d0e"
	assignTestMemberEmail = "dev@example.com"
)

type assignMock struct {
	projectsMock
	assignments  []client.CreateRoleAssignmentRequest
	assignError  error
	roleChanges  []client.UpdateMemberRoleRequest
	roleError    error
	roleResponse *client.UpdateMemberRoleResponse
}

func (m *assignMock) GetOrganisation(organisationID string) (*client.OrganisationFull, error) {
	userID := assignTestUserID
	email := "Dev@Example.com"
	pendingEmail := "pending@example.com"
	return &client.OrganisationFull{
		OrganisationID: organisationID,
		Members: []client.OrganisationMember{
			{Email: &pendingEmail},
			{ID: &userID, Email: &email},
		},
	}, nil
}

func (m *assignMock) CreateRoleAssignment(request client.CreateRoleAssignmentRequest) (*client.RoleAssignment, error) {
	m.assignments = append(m.assignments, request)
	if m.assignError != nil {
		return nil, m.assignError
	}
	return &client.RoleAssignment{ID: "assignment-1", AnkraUserID: request.AnkraUserID, RoleSlug: request.RoleSlug, RoleID: request.RoleID, ScopeType: request.ScopeType, ScopeID: request.ScopeID}, nil
}

func (m *assignMock) UpdateMemberRole(request client.UpdateMemberRoleRequest) (*client.UpdateMemberRoleResponse, error) {
	m.roleChanges = append(m.roleChanges, request)
	if m.roleError != nil {
		return nil, m.roleError
	}
	if m.roleResponse != nil {
		return m.roleResponse, nil
	}
	return &client.UpdateMemberRoleResponse{Success: true}, nil
}

func newAssignMock() *assignMock {
	return &assignMock{projectsMock: projectsMock{
		projects: twoProjects(),
		clusters: []client.ClusterListItem{{ID: projectTestClusterID, Name: "prod-eu"}},
	}}
}

// runOrgCommand runs an `ankra org ...` command with a saved organisation
// selection, so no organisation listing is needed.
func runOrgCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, "")
	name := "Ankra"
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	reset := func() { resetGlobalProjectFlag(t); resetTreeFlags(t, orgAssignCmd, orgMemberRoleCmd) }
	reset()
	t.Cleanup(reset)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(append([]string{"org"}, args...))
	runError := rootCmd.Execute()
	return stdout.String(), runError
}

func TestOrgAssignScopes(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		args      []string
		scope     string
		scopeID   string
		roleSlug  string
		roleID    string
		mentioned string
	}{
		{"organisation by default", []string{"--role", "Viewer"}, "organisation", "", "viewer", "", "at organisation"},
		{"cluster by name", []string{"--role", "operator", "--scope", "cluster", "--scope-id", "prod-eu"}, "cluster", projectTestClusterID, "operator", "", "at cluster " + projectTestClusterID},
		{"cluster by id", []string{"--role", "operator", "--scope", "cluster", "--scope-id", projectTestClusterID}, "cluster", projectTestClusterID, "operator", "", ""},
		{"cluster group by id", []string{"--role", "member", "--scope", "cluster_group", "--scope-id", assignTestGroupID}, "cluster_group", assignTestGroupID, "member", "", ""},
		{"project by slug", []string{"--role", "admin", "--scope", "project", "--scope-id", "edge"}, "project", projectTestEdgeID, "admin", "", ""},
		{"project from the global flag", []string{"--role", "admin", "--scope", "project", "--project", "edge"}, "project", projectTestEdgeID, "admin", "", ""},
		{"custom role id", []string{"--role", assignTestCustomRole}, "organisation", "", "", assignTestCustomRole, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newAssignMock()
			stdout, err := runOrgCommand(t, mock, append([]string{"assign", assignTestMemberEmail}, testCase.args...)...)
			if err != nil {
				t.Fatalf("org assign: %v", err)
			}
			if len(mock.assignments) != 1 {
				t.Fatalf("assignments = %v", mock.assignments)
			}
			request := mock.assignments[0]
			if request.AnkraUserID != assignTestUserID || request.ScopeType != testCase.scope {
				t.Fatalf("request = %+v", request)
			}
			if got := assignOptionalText(request.ScopeID); got != testCase.scopeID {
				t.Errorf("scope id = %q, want %q", got, testCase.scopeID)
			}
			if got := assignOptionalText(request.RoleSlug); got != testCase.roleSlug {
				t.Errorf("role slug = %q, want %q", got, testCase.roleSlug)
			}
			if got := assignOptionalText(request.RoleID); got != testCase.roleID {
				t.Errorf("role id = %q, want %q", got, testCase.roleID)
			}
			if testCase.mentioned != "" && !strings.Contains(stdout, testCase.mentioned) {
				t.Errorf("stdout = %s, want %q", stdout, testCase.mentioned)
			}
		})
	}
}

func assignOptionalText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestOrgAssignRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		exit int
	}{
		{"no role", []string{"assign", assignTestMemberEmail}, exitUsage},
		{"unknown scope", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "namespace"}, exitUsage},
		{"scope id on organisation", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope-id", "x"}, exitUsage},
		{"cluster without id", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "cluster"}, exitUsage},
		{"project without id", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "project"}, exitUsage},
		{"cluster group by name", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "cluster_group", "--scope-id", "edge-sites"}, exitUsage},
		{"unknown cluster", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "cluster", "--scope-id", "nowhere"}, exitNotFound},
		{"unknown project", []string{"assign", assignTestMemberEmail, "--role", "admin", "--scope", "project", "--scope-id", "nowhere"}, exitNotFound},
		{"unknown member", []string{"assign", "stranger@example.com", "--role", "admin"}, exitNotFound},
		{"pending member", []string{"assign", "pending@example.com", "--role", "admin"}, exitNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newAssignMock()
			_, err := runOrgCommand(t, mock, testCase.args...)
			if err == nil || exitCodeFor(err) != testCase.exit {
				t.Fatalf("err = %v (exit %d), want exit %d", err, exitCodeFor(err), testCase.exit)
			}
			if len(mock.assignments) != 0 {
				t.Fatalf("nothing may be assigned: %v", mock.assignments)
			}
		})
	}
}

func TestOrgAssignSurfacesThePlatformRefusal(t *testing.T) {
	mock := newAssignMock()
	mock.assignError = errors.New("Assignment already exists")
	_, err := runOrgCommand(t, mock, "assign", assignTestUserID, "--role", "admin")
	if err == nil || !strings.Contains(err.Error(), "Assignment already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestOrgAssignStructuredOutput(t *testing.T) {
	mock := newAssignMock()
	stdout, err := runOrgCommand(t, mock, "assign", assignTestMemberEmail, "--role", "viewer", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"scope_type": "organisation"`) || !strings.Contains(stdout, `"id": "assignment-1"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestOrgMemberRoleProjectOnly(t *testing.T) {
	mock := newAssignMock()
	stdout, err := runOrgCommand(t, mock, "member", "role", assignTestMemberEmail, "--role", "project", "--project", "edge", "--project-role", "operator")
	if err != nil {
		t.Fatalf("org member role: %v", err)
	}
	if len(mock.roleChanges) != 1 {
		t.Fatalf("role changes = %v", mock.roleChanges)
	}
	change := mock.roleChanges[0]
	if change.UserID != assignTestUserID || change.OrganisationID != projectTestOrganisationID || change.Role != "project" ||
		assignOptionalText(change.ProjectID) != projectTestEdgeID || assignOptionalText(change.ProjectRoleSlug) != "operator" {
		t.Fatalf("change = %+v", change)
	}
	if !strings.Contains(stdout, "project-only member: operator in project "+projectTestEdgeID) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestOrgMemberRoleOrganisationRole(t *testing.T) {
	mock := newAssignMock()
	mock.roleResponse = &client.UpdateMemberRoleResponse{Success: true, Message: "Role updated"}
	stdout, err := runOrgCommand(t, mock, "member", "role", assignTestUserID, "--role", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	change := mock.roleChanges[0]
	if change.Role != "read-only" || change.ProjectID != nil || change.ProjectRoleSlug != nil {
		t.Fatalf("change = %+v", change)
	}
	if !strings.Contains(stdout, "now has role viewer") || !strings.Contains(stdout, "Message: Role updated") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestOrgMemberRoleRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		exit int
	}{
		{"unknown role", []string{"member", "role", assignTestMemberEmail, "--role", "superuser"}, exitUsage},
		{"project role without project", []string{"member", "role", assignTestMemberEmail, "--role", "project"}, exitUsage},
		{"project role on another role", []string{"member", "role", assignTestMemberEmail, "--role", "admin", "--project-role", "admin"}, exitUsage},
		{"unknown member", []string{"member", "role", "stranger@example.com", "--role", "admin"}, exitNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newAssignMock()
			_, err := runOrgCommand(t, mock, testCase.args...)
			if err == nil || exitCodeFor(err) != testCase.exit {
				t.Fatalf("err = %v (exit %d), want exit %d", err, exitCodeFor(err), testCase.exit)
			}
			if len(mock.roleChanges) != 0 {
				t.Fatalf("no role may change: %v", mock.roleChanges)
			}
		})
	}
}

func TestOrgMemberRoleOnAPlatformWithoutTheTokenTwin(t *testing.T) {
	mock := newAssignMock()
	mock.roleError = client.ErrMemberRoleRouteMissing
	_, err := runOrgCommand(t, mock, "member", "role", assignTestMemberEmail, "--role", "admin")
	if err == nil || exitCodeFor(err) != exitNotFound {
		t.Fatalf("err = %v (exit %d)", err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "this platform does not support changing a member's role with a token yet") {
		t.Fatalf("err = %v", err)
	}

	mock = newAssignMock()
	mock.roleError = errors.New("An owner cannot become project-only")
	_, err = runOrgCommand(t, mock, "member", "role", assignTestMemberEmail, "--role", "project", "--project", "edge")
	if err == nil || exitCodeFor(err) == exitNotFound || !strings.Contains(err.Error(), "changing role") {
		t.Fatalf("err = %v (exit %d)", err, exitCodeFor(err))
	}
}
