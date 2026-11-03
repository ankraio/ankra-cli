package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

// actingOrganisationMock records, for every membership write, the
// organisation_id the body named and the organisation the client would send
// as X-Ankra-Organisation-Id at that moment. The platform refuses a write
// whose two differ.
type actingOrganisationMock struct {
	assignMock
	bodyOrganisations   []string
	headerOrganisations []string
}

func (m *actingOrganisationMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	selectedName := "Ankra"
	overrideName := "hetzner-e2e"
	return []client.OrganisationSummary{
		{OrganisationID: projectTestOrganisationID, Name: &selectedName, UserCurrent: true},
		{OrganisationID: inviteOverrideOrganisationID, Name: &overrideName},
	}, nil
}

func (m *actingOrganisationMock) record(bodyOrganisation string) {
	m.bodyOrganisations = append(m.bodyOrganisations, bodyOrganisation)
	m.headerOrganisations = append(m.headerOrganisations, m.organisation)
}

func (m *actingOrganisationMock) InviteUserToOrganisation(request client.InviteUserRequest) (*client.InviteUserResponse, error) {
	m.record(request.OrganisationID)
	return &client.InviteUserResponse{Success: true}, nil
}

func (m *actingOrganisationMock) RemoveUserFromOrganisation(request client.RemoveUserRequest) (*client.RemoveUserResponse, error) {
	m.record(request.OrganisationID)
	return &client.RemoveUserResponse{Success: true}, nil
}

func (m *actingOrganisationMock) UpdateMemberRole(request client.UpdateMemberRoleRequest) (*client.UpdateMemberRoleResponse, error) {
	m.record(request.OrganisationID)
	return &client.UpdateMemberRoleResponse{Success: true}, nil
}

// runActingOrganisationCommand runs an `ankra org ...` write. With
// saveSelection the "Ankra" organisation is the saved selection.
func runActingOrganisationCommand(t *testing.T, mock *actingOrganisationMock, saveSelection bool, environmentOrganisation string, args ...string) error {
	t.Helper()
	home := withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, environmentOrganisation)
	if saveSelection {
		name := "Ankra"
		if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(filepath.Join(home, ".ankra", "organisation.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	originalScope := organisationScopedByLocalSelection
	reset := func() {
		resetGlobalProjectFlag(t)
		resetGlobalOrganisationFlag(t)
		resetTreeFlags(t, orgInviteCmd, orgRemoveCmd, orgMemberRoleCmd)
	}
	reset()
	t.Cleanup(func() {
		reset()
		organisationScopedByLocalSelection = originalScope
	})
	var runError error
	captureStdout(t, func() {
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs(append([]string{"org"}, args...))
		runError = rootCmd.Execute()
	})
	return runError
}

func TestMembershipWritesSendTheHeaderOrganisationInTheBody(t *testing.T) {
	commands := map[string][]string{
		"invite":      {"invite", "dev@example.com"},
		"remove":      {"remove", assignTestUserID, "--force"},
		"member role": {"member", "role", assignTestMemberEmail, "--role", "viewer"},
	}
	scopes := []struct {
		name                    string
		saveSelection           bool
		environmentOrganisation string
		extraArgs               []string
		wantOrganisationID      string
	}{
		{name: "--org", saveSelection: true, extraArgs: []string{"--org", inviteOverrideOrganisationID}, wantOrganisationID: inviteOverrideOrganisationID},
		{name: "ANKRA_ORG", saveSelection: true, environmentOrganisation: "hetzner-e2e", wantOrganisationID: inviteOverrideOrganisationID},
		{name: "saved selection", saveSelection: true, wantOrganisationID: projectTestOrganisationID},
		{name: "no selection: the current organisation", wantOrganisationID: projectTestOrganisationID},
	}
	for commandName, commandArgs := range commands {
		for _, scope := range scopes {
			t.Run(commandName+" with "+scope.name, func(t *testing.T) {
				mock := &actingOrganisationMock{assignMock: *newAssignMock()}
				args := append(append([]string{}, commandArgs...), scope.extraArgs...)
				if err := runActingOrganisationCommand(t, mock, scope.saveSelection, scope.environmentOrganisation, args...); err != nil {
					t.Fatalf("org %s: %v", commandName, err)
				}
				if len(mock.bodyOrganisations) != 1 {
					t.Fatalf("writes = %v", mock.bodyOrganisations)
				}
				if mock.bodyOrganisations[0] != scope.wantOrganisationID {
					t.Errorf("body organisation_id = %q, want %q", mock.bodyOrganisations[0], scope.wantOrganisationID)
				}
				if mock.headerOrganisations[0] != mock.bodyOrganisations[0] {
					t.Errorf("X-Ankra-Organisation-Id = %q but body organisation_id = %q; the platform refuses the mismatch",
						mock.headerOrganisations[0], mock.bodyOrganisations[0])
				}
			})
		}
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
