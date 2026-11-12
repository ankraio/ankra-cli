package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// withTokenSource scopes this test to a token that came from tokenSource, the
// way the root pre-run records it, and restores the previous source after.
func withTokenSource(t *testing.T, tokenSource credentialSource) {
	t.Helper()
	originalTokenSource := organisationScopeTokenSource
	organisationScopeTokenSource = tokenSource
	t.Cleanup(func() { organisationScopeTokenSource = originalTokenSource })
}

func saveStaleSelection(t *testing.T) {
	t.Helper()
	name := "Ankra"
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTargetOrganisationSkipsTheSavedSelectionForAnExplicitToken(t *testing.T) {
	for _, testCase := range []struct {
		name                    string
		tokenSource             credentialSource
		saveSelection           bool
		environmentOrganisation string
		wantOrganisationID      string
		wantSource              string
	}{
		{name: "the saved login with its selection", tokenSource: sourceConfigFile, saveSelection: true, wantOrganisationID: projectTestOrganisationID, wantSource: organisationSourceLocal},
		{name: "the saved login without a selection", tokenSource: sourceConfigFile, wantOrganisationID: actingTokenOrganisationID, wantSource: organisationSourceServer},
		{name: "ANKRA_API_TOKEN with a stale selection", tokenSource: sourceEnv, saveSelection: true, wantOrganisationID: actingTokenOrganisationID, wantSource: organisationSourceToken},
		{name: "--token with a stale selection", tokenSource: sourceFlag, saveSelection: true, wantOrganisationID: actingTokenOrganisationID, wantSource: organisationSourceToken},
		{name: "ANKRA_API_TOKEN and ANKRA_ORG", tokenSource: sourceEnv, saveSelection: true, environmentOrganisation: "hetzner-e2e", wantOrganisationID: inviteOverrideOrganisationID, wantSource: organisationSourceOverride},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withTempHome(t)
			t.Setenv(envAnkraOrg, testCase.environmentOrganisation)
			if testCase.saveSelection {
				saveStaleSelection(t)
			}
			setMockClient(t, &actingOrganisationMock{assignMock: *newAssignMock()})
			withTokenSource(t, testCase.tokenSource)
			organisation, source, err := resolveTargetOrganisation(commandWithOrganisationFlag(t, ""))
			if err != nil {
				t.Fatal(err)
			}
			if organisation.OrganisationID != testCase.wantOrganisationID || source != testCase.wantSource {
				t.Fatalf("organisation = %q from %q, want %q from %q",
					organisation.OrganisationID, source, testCase.wantOrganisationID, testCase.wantSource)
			}
		})
	}
}

func TestOrgCurrentReportsTheTokenOrganisationForAnExplicitToken(t *testing.T) {
	for _, testCase := range []struct {
		scope      actingScope
		wantSource string
	}{
		{scope: actingScope{tokenSource: sourceConfigFile, saveSelection: true}, wantSource: "Current organisation (local):\n  ID:   " + projectTestOrganisationID},
		{scope: actingScope{tokenSource: sourceEnv, saveSelection: true}, wantSource: "Current organisation (token):\n  ID:   " + actingTokenOrganisationID},
		{scope: actingScope{tokenSource: sourceFlag, saveSelection: true}, wantSource: "Current organisation (token):\n  ID:   " + actingTokenOrganisationID},
	} {
		t.Run(string(testCase.scope.tokenSource), func(t *testing.T) {
			mock := &actingOrganisationMock{assignMock: *newAssignMock()}
			output, err := runActingOrganisationCommandOutput(t, mock, testCase.scope, "current")
			if err != nil {
				t.Fatalf("org current: %v", err)
			}
			if !strings.Contains(output, testCase.wantSource) {
				t.Fatalf("output = %q, want %q", output, testCase.wantSource)
			}
		})
	}
}

func TestOrgMembersWithoutAnArgumentListsTheTokenOrganisation(t *testing.T) {
	for _, testCase := range []struct {
		scope              actingScope
		wantOrganisationID string
	}{
		{scope: actingScope{tokenSource: sourceConfigFile, saveSelection: true}, wantOrganisationID: projectTestOrganisationID},
		{scope: actingScope{tokenSource: sourceEnv, saveSelection: true}, wantOrganisationID: actingTokenOrganisationID},
		{scope: actingScope{tokenSource: sourceFlag, saveSelection: true}, wantOrganisationID: actingTokenOrganisationID},
		{scope: actingScope{tokenSource: sourceEnv, saveSelection: true, extraArgs: []string{"--org", inviteOverrideOrganisationID}}, wantOrganisationID: inviteOverrideOrganisationID},
	} {
		t.Run(string(testCase.scope.tokenSource)+" "+strings.Join(testCase.scope.extraArgs, " "), func(t *testing.T) {
			mock := &actingOrganisationMock{assignMock: *newAssignMock()}
			output, err := runActingOrganisationCommandOutput(t, mock, testCase.scope, "members")
			if err != nil {
				t.Fatalf("org members: %v", err)
			}
			var listed []string
			for _, call := range mock.calls {
				if call.name == "GetOrganisation" {
					listed = append(listed, call.namedOrganisation)
				}
			}
			if len(listed) != 1 || listed[0] != testCase.wantOrganisationID {
				t.Fatalf("members read for %v, want %s", listed, testCase.wantOrganisationID)
			}
			if !strings.Contains(output, testCase.wantOrganisationID) {
				t.Fatalf("output = %q, want the organisation %s", output, testCase.wantOrganisationID)
			}
		})
	}
}

func TestLoginStatusReportsTheTokenOrganisationOverAStaleSelection(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		isSavedLogin       bool
		wantOrganisationID string
		wantDetail         string
	}{
		{name: "the saved login", isSavedLogin: true, wantOrganisationID: projectTestOrganisationID, wantDetail: "organisation selection source: local"},
		{name: "ANKRA_API_TOKEN", wantOrganisationID: actingTokenOrganisationID, wantDetail: "organisation selection source: token"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withFreshViperAndEnv(t)
			withTempHome(t)
			t.Setenv(envAnkraOrg, "")
			saveStaleSelection(t)
			withTokenSource(t, organisationScopeTokenSource)
			setMockClient(t, &actingOrganisationMock{assignMock: *newAssignMock()})
			if testCase.isSavedLogin {
				useSavedLogin(t)
			}

			output, err := runLoginStatusCommand(t, "-o", "json")
			if err != nil {
				t.Fatalf("login status: %v\n%s", err, output)
			}
			var status loginStatusOutput
			if decodeError := json.Unmarshal([]byte(output), &status); decodeError != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
			}
			if status.Organisation == nil || status.Organisation.OrganisationID != testCase.wantOrganisationID || status.Detail != testCase.wantDetail {
				t.Fatalf("status = %+v, want organisation %s with %q", status, testCase.wantOrganisationID, testCase.wantDetail)
			}
		})
	}
}

// staleSelectionShipMock is the ship journey for a member of two
// organisations whose cluster lives in the token's own organisation, while
// the saved selection names the other one.
type staleSelectionShipMock struct {
	*applicationShipMock
}

const shipStaleSelectionOrganisationID = "5a4b3c2d-1e0f-4a9b-8c7d-6e5f4a3b2c1d"

func (mock staleSelectionShipMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	tokenName := "Acme Org"
	staleName := "Stale Selection"
	return []client.OrganisationSummary{
		{OrganisationID: shipTestOrganisationID, Name: &tokenName, UserCurrent: true},
		{OrganisationID: shipStaleSelectionOrganisationID, Name: &staleName},
	}, nil
}

func TestApplicationShipTargetsTheTokenOrganisationOverAStaleSelection(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		tokenSource credentialSource
		wantRefusal bool
	}{
		{name: "the saved login ships to its saved selection", tokenSource: sourceConfigFile, wantRefusal: true},
		{name: "ANKRA_API_TOKEN ships to its own organisation", tokenSource: sourceEnv},
		{name: "--token ships to its own organisation", tokenSource: sourceFlag},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fastShipPolling(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv(envAnkraOrg, "")
			staleName := "Stale Selection"
			if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: shipStaleSelectionOrganisationID, Name: &staleName}); err != nil {
				t.Fatal(err)
			}
			withTokenSource(t, testCase.tokenSource)
			repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/shop.git")
			shipMock := newApplicationShipMock()
			previousClient := apiClient
			apiClient = staleSelectionShipMock{applicationShipMock: shipMock}
			t.Cleanup(func() { apiClient = previousClient })

			applicationCommand := newApplicationCommand()
			var progress bytes.Buffer
			applicationCommand.SetOut(new(bytes.Buffer))
			applicationCommand.SetErr(&progress)
			applicationCommand.SetArgs([]string{"ship", repositoryPath, "--cluster", "production"})
			executeError := applicationCommand.Execute()

			if testCase.wantRefusal {
				if executeError == nil || exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), `"Stale Selection"`) {
					t.Fatalf("error = %v, want the saved selection's cluster-mismatch refusal", executeError)
				}
				return
			}
			if executeError != nil {
				t.Fatalf("ship error = %v\nprogress: %s", executeError, progress.String())
			}
			if !strings.Contains(progress.String(), `Shipping to organisation "Acme Org"`) || strings.Contains(progress.String(), "Stale Selection") {
				t.Fatalf("progress = %s, want it to name the token's organisation only", progress.String())
			}
			if shipMock.deployCalls != 1 {
				t.Fatalf("deploy calls = %d, want 1", shipMock.deployCalls)
			}
		})
	}
}

func TestOnboardContinuesInTheTokenOrganisationOverAStaleSelection(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		isSavedLogin       bool
		wantOrganisationID string
	}{
		{name: "the saved login keeps its saved selection", isSavedLogin: true, wantOrganisationID: "org-a"},
		{name: "ANKRA_API_TOKEN continues in its own organisation", wantOrganisationID: "org-b"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &onboardMock{
				organisations: []client.OrganisationSummary{onboardOrg("org-a", "Alpha", false), onboardOrg("org-b", "Beta", true)},
				clusters:      1,
			}
			setUpOnboardTest(t, mock)
			withTokenSource(t, organisationScopeTokenSource)
			alpha := "Alpha"
			if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: "org-a", Name: &alpha}); err != nil {
				t.Fatal(err)
			}
			if testCase.isSavedLogin {
				writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})
			} else {
				t.Setenv("ANKRA_API_TOKEN", "environment-token")
			}

			stdout, stderr, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
			if runError != nil {
				t.Fatalf("onboard: %v\nstderr: %s", runError, stderr)
			}
			event := findOnboardEvent(t, parseOnboardEvents(t, stdout), "organisation")
			chosen := event["organisation"].(map[string]any)
			if chosen["organisation_id"] != testCase.wantOrganisationID || mock.override != testCase.wantOrganisationID {
				t.Fatalf("onboard continued in %v (override %q), want %s", chosen["organisation_id"], mock.override, testCase.wantOrganisationID)
			}
			if len(mock.switchedTo) != 0 {
				t.Fatalf("an agent run must not switch, switched %v", mock.switchedTo)
			}
		})
	}
}

func TestBillingHintLinksTheSavedSelectionOnlyForTheSavedLogin(t *testing.T) {
	for _, testCase := range []struct {
		tokenSource credentialSource
		want        string
	}{
		{tokenSource: sourceConfigFile, want: "Add credit in the portal under Billing: https://platform.example.test/next/organisation/billing?org=org-selected"},
		{tokenSource: sourceEnv, want: "Add credit in the portal under Billing."},
		{tokenSource: sourceFlag, want: "Add credit in the portal under Billing."},
	} {
		t.Run(string(testCase.tokenSource), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			setMockClient(t, &orgOverrideMock{})
			setTestBaseURL(t, "https://platform.example.test")
			if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: "org-selected"}); err != nil {
				t.Fatal(err)
			}
			withTokenSource(t, testCase.tokenSource)
			if got := billingRestrictedHint(); got != testCase.want {
				t.Fatalf("billingRestrictedHint() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestScopedOrganisationLabelNamesTheSavedSelectionOnlyForTheSavedLogin(t *testing.T) {
	for _, testCase := range []struct {
		tokenSource credentialSource
		want        string
	}{
		{tokenSource: sourceConfigFile, want: `"Ankra" (` + projectTestOrganisationID + `)`},
		{tokenSource: sourceEnv, want: ""},
		{tokenSource: sourceFlag, want: ""},
	} {
		t.Run(string(testCase.tokenSource), func(t *testing.T) {
			withTempHome(t)
			saveStaleSelection(t)
			setMockClient(t, &actingOrganisationMock{assignMock: *newAssignMock()})
			withTokenSource(t, testCase.tokenSource)
			if got := scopedOrganisationLabel(); got != testCase.want {
				t.Fatalf("scopedOrganisationLabel() = %q, want %q", got, testCase.want)
			}
		})
	}
}
