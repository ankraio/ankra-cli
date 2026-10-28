package cmd

import (
	"bytes"
	"testing"

	"ankra/internal/client"
)

const inviteOverrideOrganisationID = "059cc5c3-06bd-4c36-80c9-269417da51f3"

type overrideInviteMock struct {
	projectInviteMock
}

func (m *overrideInviteMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	selectedName := "Ankra"
	overrideName := "hetzner-e2e"
	return []client.OrganisationSummary{
		{OrganisationID: projectTestOrganisationID, Name: &selectedName, UserCurrent: true},
		{OrganisationID: inviteOverrideOrganisationID, Name: &overrideName},
	}, nil
}

func resetGlobalOrganisationFlag(t *testing.T) {
	t.Helper()
	flag := rootCmd.PersistentFlags().Lookup("org")
	if flag == nil {
		t.Fatal("the root command carries no --org flag")
	}
	_ = flag.Value.Set("")
	flag.Changed = false
}

// runOverrideInvite runs `ankra org invite` with "Ankra" saved as the local
// selection and environmentOrganisation as ANKRA_ORG.
func runOverrideInvite(t *testing.T, mock *overrideInviteMock, environmentOrganisation string, args ...string) error {
	t.Helper()
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, environmentOrganisation)
	name := "Ankra"
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	reset := func() {
		resetGlobalProjectFlag(t)
		resetGlobalOrganisationFlag(t)
		resetTreeFlags(t, orgInviteCmd)
	}
	reset()
	t.Cleanup(reset)
	var runError error
	captureStdout(t, func() {
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs(append([]string{"org", "invite"}, args...))
		runError = rootCmd.Execute()
	})
	return runError
}

func TestOrgInviteTargetsTheOrganisationOverride(t *testing.T) {
	testCases := []struct {
		name                    string
		environmentOrganisation string
		args                    []string
		wantOrganisationID      string
	}{
		{
			name:               "org flag wins over the saved selection",
			args:               []string{"dev@example.com", "--role", "admin", "--org", inviteOverrideOrganisationID},
			wantOrganisationID: inviteOverrideOrganisationID,
		},
		{
			name:                    "ANKRA_ORG wins over the saved selection",
			environmentOrganisation: "hetzner-e2e",
			args:                    []string{"dev@example.com", "--role", "admin"},
			wantOrganisationID:      inviteOverrideOrganisationID,
		},
		{
			name:               "without an override the saved selection is used",
			args:               []string{"dev@example.com"},
			wantOrganisationID: projectTestOrganisationID,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &overrideInviteMock{projectInviteMock{projectsMock: projectsMock{projects: twoProjects()}}}
			if err := runOverrideInvite(t, mock, testCase.environmentOrganisation, testCase.args...); err != nil {
				t.Fatalf("org invite: %v", err)
			}
			if len(mock.invites) != 1 {
				t.Fatalf("invites = %+v", mock.invites)
			}
			if mock.invites[0].OrganisationID != testCase.wantOrganisationID {
				t.Fatalf("invite organisation = %q, want %q", mock.invites[0].OrganisationID, testCase.wantOrganisationID)
			}
		})
	}
}
