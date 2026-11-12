package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

// actingTokenOrganisationID is the organisation a --token or ANKRA_API_TOKEN
// token belongs to: the platform's current organisation for it. It differs
// from the saved selection (projectTestOrganisationID) on purpose, so a test
// can tell which of the two a write acted in.
const actingTokenOrganisationID = "9c4f1e7a-2b3d-4e5f-8a6b-7c8d9e0f1a2b"

// actingCall is one request the client made: which call, the organisation it
// sent as X-Ankra-Organisation-Id at that moment, and the organisation the
// request itself named (the body's organisation_id, or the organisation a
// member lookup reads) when it names one.
type actingCall struct {
	name              string
	header            string
	namedOrganisation string
	namesOrganisation bool
}

// actingOrganisationMock records, in order, every lookup and membership write
// with the organisation the client would send as X-Ankra-Organisation-Id at
// that moment. The platform resolves a lookup in that organisation and
// refuses a write whose header and body differ.
type actingOrganisationMock struct {
	assignMock
	calls []actingCall
}

func (m *actingOrganisationMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	tokenName := "Service Account Home"
	selectedName := "Ankra"
	overrideName := "hetzner-e2e"
	return []client.OrganisationSummary{
		{OrganisationID: actingTokenOrganisationID, Name: &tokenName, UserCurrent: true},
		{OrganisationID: projectTestOrganisationID, Name: &selectedName},
		{OrganisationID: inviteOverrideOrganisationID, Name: &overrideName},
	}, nil
}

func (m *actingOrganisationMock) record(name string) {
	m.calls = append(m.calls, actingCall{name: name, header: m.organisation})
}

func (m *actingOrganisationMock) recordNaming(name string, namedOrganisation string) {
	m.calls = append(m.calls, actingCall{name: name, header: m.organisation, namedOrganisation: namedOrganisation, namesOrganisation: true})
}

func (m *actingOrganisationMock) writes() []actingCall {
	var writes []actingCall
	for _, call := range m.calls {
		switch call.name {
		case "InviteUserToOrganisation", "RemoveUserFromOrganisation", "UpdateMemberRole", "CreateRoleAssignment":
			writes = append(writes, call)
		}
	}
	return writes
}

func (m *actingOrganisationMock) GetCluster(name string) (client.ClusterListItem, error) {
	m.record("GetCluster")
	return m.assignMock.GetCluster(name)
}

func (m *actingOrganisationMock) ListProjects() ([]client.ProjectSummary, error) {
	m.record("ListProjects")
	return m.assignMock.ListProjects()
}

func (m *actingOrganisationMock) GetOrganisation(organisationID string) (*client.OrganisationFull, error) {
	m.recordNaming("GetOrganisation", organisationID)
	return m.assignMock.GetOrganisation(organisationID)
}

func (m *actingOrganisationMock) InviteUserToOrganisation(request client.InviteUserRequest) (*client.InviteUserResponse, error) {
	m.recordNaming("InviteUserToOrganisation", request.OrganisationID)
	return &client.InviteUserResponse{Success: true}, nil
}

func (m *actingOrganisationMock) RemoveUserFromOrganisation(request client.RemoveUserRequest) (*client.RemoveUserResponse, error) {
	m.recordNaming("RemoveUserFromOrganisation", request.OrganisationID)
	return &client.RemoveUserResponse{Success: true}, nil
}

func (m *actingOrganisationMock) UpdateMemberRole(request client.UpdateMemberRoleRequest) (*client.UpdateMemberRoleResponse, error) {
	m.recordNaming("UpdateMemberRole", request.OrganisationID)
	return m.assignMock.UpdateMemberRole(request)
}

func (m *actingOrganisationMock) CreateRoleAssignment(request client.CreateRoleAssignmentRequest) (*client.RoleAssignment, error) {
	m.record("CreateRoleAssignment")
	return m.assignMock.CreateRoleAssignment(request)
}

// useSavedLogin makes this test's invocations run on a saved `ankra login`
// token instead of ANKRA_API_TOKEN: the only token the saved organisation
// selection applies to. Call it after setMockClient, which exports the
// environment token.
func useSavedLogin(t *testing.T) {
	t.Helper()
	t.Setenv("ANKRA_API_TOKEN", "")
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://test.ankra.app"})
	originalScope := organisationScopedByLocalSelection
	originalTokenSource := organisationScopeTokenSource
	t.Cleanup(func() {
		organisationScopedByLocalSelection = originalScope
		organisationScopeTokenSource = originalTokenSource
	})
}

func resetGlobalTokenFlag(t *testing.T) {
	t.Helper()
	flag := rootCmd.PersistentFlags().Lookup("token")
	if flag == nil {
		t.Fatal("the root command carries no --token flag")
	}
	_ = flag.Value.Set("")
	flag.Changed = false
}

// actingScope is where an invocation's token came from and what scopes it:
// "Ankra" (projectTestOrganisationID) as the saved selection when
// saveSelection, and environmentOrganisation as ANKRA_ORG.
type actingScope struct {
	name                    string
	tokenSource             credentialSource
	saveSelection           bool
	environmentOrganisation string
	extraArgs               []string
	wantOrganisationID      string
}

func actingScopes() []actingScope {
	return []actingScope{
		{name: "the saved login with its saved selection", tokenSource: sourceConfigFile, saveSelection: true, wantOrganisationID: projectTestOrganisationID},
		{name: "the saved login without a selection", tokenSource: sourceConfigFile, wantOrganisationID: actingTokenOrganisationID},
		{name: "the saved login and --org", tokenSource: sourceConfigFile, saveSelection: true, extraArgs: []string{"--org", inviteOverrideOrganisationID}, wantOrganisationID: inviteOverrideOrganisationID},
		{name: "ANKRA_API_TOKEN with a stale saved selection", tokenSource: sourceEnv, saveSelection: true, wantOrganisationID: actingTokenOrganisationID},
		{name: "--token with a stale saved selection", tokenSource: sourceFlag, saveSelection: true, wantOrganisationID: actingTokenOrganisationID},
		{name: "ANKRA_API_TOKEN without a selection", tokenSource: sourceEnv, wantOrganisationID: actingTokenOrganisationID},
		{name: "ANKRA_API_TOKEN and --org", tokenSource: sourceEnv, saveSelection: true, extraArgs: []string{"--org", inviteOverrideOrganisationID}, wantOrganisationID: inviteOverrideOrganisationID},
		{name: "ANKRA_API_TOKEN and ANKRA_ORG", tokenSource: sourceEnv, saveSelection: true, environmentOrganisation: "hetzner-e2e", wantOrganisationID: inviteOverrideOrganisationID},
	}
}

// runActingOrganisationCommand runs an `ankra org ...` command on the token
// and organisation scope that scope names.
func runActingOrganisationCommand(t *testing.T, mock *actingOrganisationMock, scope actingScope, args ...string) error {
	t.Helper()
	_, runError := runActingOrganisationCommandOutput(t, mock, scope, args...)
	return runError
}

// runActingOrganisationCommandOutput is runActingOrganisationCommand that
// also returns what the command printed.
func runActingOrganisationCommandOutput(t *testing.T, mock *actingOrganisationMock, scope actingScope, args ...string) (string, error) {
	t.Helper()
	home := withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, scope.environmentOrganisation)
	if scope.saveSelection {
		name := "Ankra"
		if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(filepath.Join(home, ".ankra", "organisation.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	arguments := append(append([]string{"org"}, args...), scope.extraArgs...)
	switch scope.tokenSource {
	case sourceConfigFile:
		useSavedLogin(t)
	case sourceFlag:
		t.Setenv("ANKRA_API_TOKEN", "")
		arguments = append(arguments, "--token", "flag-token")
	}
	originalScope := organisationScopedByLocalSelection
	originalTokenSource := organisationScopeTokenSource
	reset := func() {
		resetGlobalProjectFlag(t)
		resetGlobalOrganisationFlag(t)
		resetGlobalTokenFlag(t)
		resetTreeFlags(t, orgInviteCmd, orgRemoveCmd, orgMemberRoleCmd, orgAssignCmd, orgCurrentCmd, orgMembersCmd)
	}
	reset()
	t.Cleanup(func() {
		reset()
		organisationScopedByLocalSelection = originalScope
		organisationScopeTokenSource = originalTokenSource
	})
	var runError error
	commandOutput := new(bytes.Buffer)
	printed := captureStdout(t, func() {
		rootCmd.SetOut(commandOutput)
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs(arguments)
		runError = rootCmd.Execute()
	})
	return printed + commandOutput.String(), runError
}

// assertEveryCallActsIn fails unless every recorded call sent
// wantOrganisationID as its header and, when it names one, in its body.
func assertEveryCallActsIn(t *testing.T, mock *actingOrganisationMock, wantOrganisationID string) {
	t.Helper()
	for _, call := range mock.calls {
		if call.header != wantOrganisationID {
			t.Errorf("%s sent X-Ankra-Organisation-Id %q, want %q (calls %+v)", call.name, call.header, wantOrganisationID, mock.calls)
		}
		if call.namesOrganisation && call.namedOrganisation != wantOrganisationID {
			t.Errorf("%s named organisation %q, want %q", call.name, call.namedOrganisation, wantOrganisationID)
		}
	}
}

func TestMembershipWritesActInTheOrganisationTheTokenIsScopedTo(t *testing.T) {
	commands := map[string][]string{
		"invite":      {"invite", "dev@example.com"},
		"remove":      {"remove", assignTestUserID, "--force"},
		"member role": {"member", "role", assignTestMemberEmail, "--role", "viewer"},
		"assign":      {"assign", assignTestMemberEmail, "--role", "viewer"},
	}
	for commandName, commandArgs := range commands {
		for _, scope := range actingScopes() {
			t.Run(commandName+" with "+scope.name, func(t *testing.T) {
				mock := &actingOrganisationMock{assignMock: *newAssignMock()}
				if err := runActingOrganisationCommand(t, mock, scope, commandArgs...); err != nil {
					t.Fatalf("org %s: %v", commandName, err)
				}
				if writes := mock.writes(); len(writes) != 1 {
					t.Fatalf("writes = %+v", writes)
				}
				assertEveryCallActsIn(t, mock, scope.wantOrganisationID)
			})
		}
	}
}

func TestOrgAssignResolvesTheScopeInTheOrganisationItWritesTo(t *testing.T) {
	resolutions := []struct {
		name           string
		args           []string
		resolutionCall string
		wantScopeID    string
	}{
		{name: "a cluster by name", args: []string{"--scope", "cluster", "--scope-id", "prod-eu"}, resolutionCall: "GetCluster", wantScopeID: projectTestClusterID},
		{name: "a project by slug", args: []string{"--scope", "project", "--scope-id", "edge"}, resolutionCall: "ListProjects", wantScopeID: projectTestEdgeID},
	}
	for _, resolution := range resolutions {
		for _, scope := range actingScopes() {
			t.Run(resolution.name+" with "+scope.name, func(t *testing.T) {
				mock := &actingOrganisationMock{assignMock: *newAssignMock()}
				args := append([]string{"assign", assignTestMemberEmail, "--role", "operator"}, resolution.args...)
				if err := runActingOrganisationCommand(t, mock, scope, args...); err != nil {
					t.Fatalf("org assign: %v", err)
				}
				resolutionIndex, writeIndex := -1, -1
				for index, call := range mock.calls {
					switch call.name {
					case resolution.resolutionCall:
						if resolutionIndex == -1 {
							resolutionIndex = index
						}
					case "CreateRoleAssignment":
						writeIndex = index
					}
				}
				if resolutionIndex == -1 || writeIndex == -1 || resolutionIndex > writeIndex {
					t.Fatalf("want %s before CreateRoleAssignment, calls %+v", resolution.resolutionCall, mock.calls)
				}
				assertEveryCallActsIn(t, mock, scope.wantOrganisationID)
				if len(mock.assignments) != 1 || assignOptionalText(mock.assignments[0].ScopeID) != resolution.wantScopeID {
					t.Fatalf("assignments = %+v, want scope id %s", mock.assignments, resolution.wantScopeID)
				}
			})
		}
	}
}

func TestOrgMemberRoleProjectOnlyActsInTheTokenOrganisation(t *testing.T) {
	scope := actingScope{tokenSource: sourceEnv, saveSelection: true, wantOrganisationID: actingTokenOrganisationID}
	mock := &actingOrganisationMock{assignMock: *newAssignMock()}
	if err := runActingOrganisationCommand(t, mock, scope, "member", "role", assignTestMemberEmail, "--role", "project", "--project", "edge"); err != nil {
		t.Fatalf("org member role: %v", err)
	}
	writes := mock.writes()
	if len(writes) != 1 || writes[0].header != actingTokenOrganisationID || writes[0].namedOrganisation != actingTokenOrganisationID {
		t.Fatalf("writes = %+v, want one in %s", writes, actingTokenOrganisationID)
	}
	if len(mock.roleChanges) != 1 || assignOptionalText(mock.roleChanges[0].ProjectID) != projectTestEdgeID {
		t.Fatalf("role changes = %+v, want the project-only grant in %s", mock.roleChanges, projectTestEdgeID)
	}
}

func TestResolveOrganisationIDSkipsTheSavedSelectionForAnExplicitToken(t *testing.T) {
	for _, testCase := range []struct {
		tokenSource credentialSource
		want        string
	}{
		{tokenSource: sourceConfigFile, want: projectTestOrganisationID},
		{tokenSource: sourceEnv, want: actingTokenOrganisationID},
		{tokenSource: sourceFlag, want: actingTokenOrganisationID},
	} {
		t.Run(string(testCase.tokenSource), func(t *testing.T) {
			withTempHome(t)
			name := "Ankra"
			if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
				t.Fatal(err)
			}
			setMockClient(t, &actingOrganisationMock{assignMock: *newAssignMock()})
			originalTokenSource := organisationScopeTokenSource
			organisationScopeTokenSource = testCase.tokenSource
			t.Cleanup(func() { organisationScopeTokenSource = originalTokenSource })
			organisationID, err := resolveOrganisationID()
			if err != nil {
				t.Fatal(err)
			}
			if organisationID != testCase.want {
				t.Fatalf("organisation = %q, want %q", organisationID, testCase.want)
			}
		})
	}
}

func TestResolveActingOrganisationKeepsAnExplicitOverride(t *testing.T) {
	mock := &actingOrganisationMock{assignMock: *newAssignMock()}
	withTempHome(t)
	setMockClient(t, mock)
	mock.organisation = inviteOverrideOrganisationID
	organisationID, err := resolveActingOrganisationID()
	if err != nil {
		t.Fatal(err)
	}
	if organisationID != inviteOverrideOrganisationID || mock.organisation != inviteOverrideOrganisationID {
		t.Fatalf("acting organisation = %q, header = %q", organisationID, mock.organisation)
	}
}
