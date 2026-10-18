package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	localSelectionID  = "3b7dccca-0788-4470-9910-19478ae345ae"
	serverCurrentID   = "b1e6e5bd-0000-4000-8000-000000000001"
	staleSelectionID  = "9f9f9f9f-0000-4000-8000-000000000009"
	organisationTitle = "Ankra"
)

// organisationHeaderServer records the organisation header of every request
// and answers the organisation list the way the platform does: a header that
// names an organisation the login does not belong to is refused with 403.
type organisationHeaderServer struct {
	mutex          sync.Mutex
	headers        []string
	memberships    []client.OrganisationSummary
	refuseHeaderID string
}

func (s *organisationHeaderServer) seenHeaders() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.headers...)
}

func (s *organisationHeaderServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	header := request.Header.Get("X-Ankra-Organisation-Id")
	s.mutex.Lock()
	s.headers = append(s.headers, header)
	s.mutex.Unlock()
	if header != "" && header == s.refuseHeaderID {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"Not a member of the requested organisation"}`))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(s.memberships)
}

func membershipsWithServerCurrent() []client.OrganisationSummary {
	return []client.OrganisationSummary{
		{OrganisationID: localSelectionID, Name: organisationName(organisationTitle)},
		{OrganisationID: serverCurrentID, Name: organisationName("Ankra Agent Studio"), UserCurrent: true},
	}
}

// withOrganisationHeaderServer installs a real API client against a recording
// server and an isolated HOME, so the test exercises the header on the wire.
func withOrganisationHeaderServer(t *testing.T, server *organisationHeaderServer) {
	t.Helper()
	withTempHome(t)
	t.Setenv(envAnkraOrg, "")
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	originalClient := apiClient
	originalScope := organisationScopedByLocalSelection
	apiClient = client.New("saved-token", httpServer.URL)
	t.Cleanup(func() {
		apiClient = originalClient
		organisationScopedByLocalSelection = originalScope
	})
}

func writeLocalOrganisationSelection(t *testing.T, organisationID string) {
	t.Helper()
	name := organisationTitle
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: organisationID, Name: &name}); err != nil {
		t.Fatalf("saving selection: %v", err)
	}
}

// commandWithOrganisationFlag builds a root carrying the global --org flag,
// optionally set, and returns its child command.
func commandWithOrganisationFlag(t *testing.T, organisationFlag string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "ankra"}
	root.PersistentFlags().String("org", "", "")
	if organisationFlag != "" {
		if err := root.PersistentFlags().Set("org", organisationFlag); err != nil {
			t.Fatal(err)
		}
	}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	return child
}

func TestLocalSelectionIsSentAsTheOrganisationHeader(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
	withOrganisationHeaderServer(t, server)
	writeLocalOrganisationSelection(t, localSelectionID)

	if err := applyOrganisationOverride(commandWithOrganisationFlag(t, ""), sourceConfigFile); err != nil {
		t.Fatalf("applyOrganisationOverride: %v", err)
	}
	if _, err := apiClient.ListOrganisations(); err != nil {
		t.Fatalf("request: %v", err)
	}
	headers := server.seenHeaders()
	if len(headers) != 1 || headers[0] != localSelectionID {
		t.Fatalf("organisation headers = %v, want the local selection %s", headers, localSelectionID)
	}
	if !organisationScopedByLocalSelection {
		t.Error("scope should be recorded as coming from the local selection")
	}
}

func TestExplicitOrganisationWinsOverTheLocalSelection(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		organisationFlag string
		environmentValue string
	}{
		{name: "flag", organisationFlag: serverCurrentID},
		{name: "environment", environmentValue: serverCurrentID},
		{name: "flag over environment", organisationFlag: serverCurrentID, environmentValue: localSelectionID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
			withOrganisationHeaderServer(t, server)
			writeLocalOrganisationSelection(t, localSelectionID)
			t.Setenv(envAnkraOrg, testCase.environmentValue)

			if err := applyOrganisationOverride(commandWithOrganisationFlag(t, testCase.organisationFlag), sourceConfigFile); err != nil {
				t.Fatalf("applyOrganisationOverride: %v", err)
			}
			if got := apiClient.OrganisationOverride(); got != serverCurrentID {
				t.Errorf("organisation override = %q, want the explicit %q", got, serverCurrentID)
			}
			if organisationScopedByLocalSelection {
				t.Error("an explicit organisation must not be recorded as the local selection")
			}
		})
	}
}

func TestNoLocalSelectionSendsNoOrganisationHeader(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
	withOrganisationHeaderServer(t, server)

	if err := applyOrganisationOverride(commandWithOrganisationFlag(t, ""), sourceConfigFile); err != nil {
		t.Fatalf("applyOrganisationOverride: %v", err)
	}
	if _, err := apiClient.ListOrganisations(); err != nil {
		t.Fatalf("request: %v", err)
	}
	if headers := server.seenHeaders(); len(headers) != 1 || headers[0] != "" {
		t.Fatalf("organisation headers = %v, want none", headers)
	}
}

func TestLocalSelectionIsNotPairedWithAnExplicitToken(t *testing.T) {
	for _, tokenSource := range []credentialSource{sourceFlag, sourceEnv} {
		t.Run(string(tokenSource), func(t *testing.T) {
			server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
			withOrganisationHeaderServer(t, server)
			writeLocalOrganisationSelection(t, localSelectionID)

			if err := applyOrganisationOverride(commandWithOrganisationFlag(t, ""), tokenSource); err != nil {
				t.Fatalf("applyOrganisationOverride: %v", err)
			}
			if got := apiClient.OrganisationOverride(); got != "" {
				t.Errorf("organisation override = %q, want none for a %s token", got, tokenSource)
			}
		})
	}
}

func TestSelectionCommandsIgnoreTheLocalSelection(t *testing.T) {
	for _, command := range []*cobra.Command{orgListCmd, orgSwitchCmd, orgCurrentCmd} {
		t.Run(command.Name(), func(t *testing.T) {
			server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
			withOrganisationHeaderServer(t, server)
			writeLocalOrganisationSelection(t, localSelectionID)

			if err := applyOrganisationOverride(command, sourceConfigFile); err != nil {
				t.Fatalf("applyOrganisationOverride: %v", err)
			}
			if got := apiClient.OrganisationOverride(); got != "" {
				t.Errorf("organisation override = %q, want none: 'ankra org %s' must work with any selection", got, command.Name())
			}
		})
	}
}

func TestStaleSelectionRefusalCarriesTheSwitchHint(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent(), refuseHeaderID: staleSelectionID}
	withOrganisationHeaderServer(t, server)
	writeLocalOrganisationSelection(t, staleSelectionID)

	if err := applyOrganisationOverride(commandWithOrganisationFlag(t, ""), sourceConfigFile); err != nil {
		t.Fatalf("applyOrganisationOverride: %v", err)
	}
	_, requestError := apiClient.ListOrganisations()
	if requestError == nil {
		t.Fatal("expected the platform to refuse the stale selection")
	}

	var output bytes.Buffer
	printOrganisationSelectionHint(&output, requestError)
	for _, fragment := range []string{
		staleSelectionID,
		"does not match this login",
		"ankra org switch",
		"ANKRA_ORG",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("hint %q is missing %q", output.String(), fragment)
		}
	}
	if got := apiClient.OrganisationOverride(); got != staleSelectionID {
		t.Errorf("organisation override = %q after the hint, want it restored to %q", got, staleSelectionID)
	}
}

func TestRefusalInsideAMemberOrganisationGetsNoSelectionHint(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
	withOrganisationHeaderServer(t, server)
	writeLocalOrganisationSelection(t, localSelectionID)

	if err := applyOrganisationOverride(commandWithOrganisationFlag(t, ""), sourceConfigFile); err != nil {
		t.Fatalf("applyOrganisationOverride: %v", err)
	}
	roleRefusal := fmt.Errorf("deleting cluster: %w", &client.PermissionDeniedError{Permission: "clusters.write"})
	if hint := organisationSelectionHint(roleRefusal); hint != "" {
		t.Errorf("a role refusal in an organisation the login belongs to got a selection hint: %q", hint)
	}
	if hint := organisationSelectionHint(errors.New("connection refused")); hint != "" {
		t.Errorf("a non-refusal error got a selection hint: %q", hint)
	}
}

func TestSelectionHintNeedsTheLocalSelectionInScope(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent(), refuseHeaderID: staleSelectionID}
	withOrganisationHeaderServer(t, server)
	t.Setenv(envAnkraOrg, "")
	organisationScopedByLocalSelection = false
	apiClient.SetOrganisationOverride(staleSelectionID)

	refusal := &client.PermissionDeniedError{Detail: "Not a member of the requested organisation"}
	if hint := organisationSelectionHint(refusal); hint != "" {
		t.Errorf("an explicit --org refusal got the local-selection hint: %q", hint)
	}
}

func TestOrganisationScopeWarning(t *testing.T) {
	memberships := membershipsWithServerCurrent()

	t.Run("local selection differs from the platform's current", func(t *testing.T) {
		withTempHome(t)
		warning := organisationScopeWarning(memberships[0], "local", memberships)
		for _, fragment := range []string{"Ankra Agent Studio", serverCurrentID, localSelectionID, "ankra org switch " + localSelectionID} {
			if !strings.Contains(warning, fragment) {
				t.Errorf("warning %q is missing %q", warning, fragment)
			}
		}
	})

	t.Run("local selection matches the platform's current", func(t *testing.T) {
		withTempHome(t)
		if warning := organisationScopeWarning(memberships[1], "local", memberships); warning != "" {
			t.Errorf("unexpected warning %q", warning)
		}
	})

	t.Run("stale local selection", func(t *testing.T) {
		withTempHome(t)
		writeLocalOrganisationSelection(t, staleSelectionID)
		warning := organisationScopeWarning(memberships[1], "server", memberships)
		if !strings.Contains(warning, staleSelectionID) || !strings.Contains(warning, "ankra org switch") {
			t.Errorf("warning %q should name the stale selection and the fix", warning)
		}
	})

	t.Run("no local selection", func(t *testing.T) {
		withTempHome(t)
		if warning := organisationScopeWarning(memberships[1], "server", memberships); warning != "" {
			t.Errorf("unexpected warning %q", warning)
		}
	})
}

func TestOrgCurrentWarnsWhenTheLocalAndPlatformOrganisationsDiffer(t *testing.T) {
	server := &organisationHeaderServer{memberships: membershipsWithServerCurrent()}
	withOrganisationHeaderServer(t, server)
	writeLocalOrganisationSelection(t, localSelectionID)

	var standardError bytes.Buffer
	orgCurrentCmd.SetErr(&standardError)
	t.Cleanup(func() { orgCurrentCmd.SetErr(nil) })
	standardOutput := os.Stdout
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devNull
	runError := orgCurrentCmd.RunE(orgCurrentCmd, nil)
	os.Stdout = standardOutput
	_ = devNull.Close()
	if runError != nil {
		t.Fatalf("org current: %v", runError)
	}
	if !strings.Contains(standardError.String(), "Warning: the platform's current organisation is") {
		t.Errorf("stderr %q should warn that the organisations differ", standardError.String())
	}
}

func TestGatewayAdoptsTheOwningOrganisationOverTheLocalSelection(t *testing.T) {
	// The local selection is a default scope, not a pin: a cluster ID owned by
	// another organisation the user belongs to is still adopted.
	mock := withOrgScopeMock(t, crossOrgMock())
	mock.override = selectedOrgID
	originalScope := organisationScopedByLocalSelection
	organisationScopedByLocalSelection = true
	t.Cleanup(func() { organisationScopedByLocalSelection = originalScope })

	if _, _, err := resolveGatewayClusterID(otherOrgClusterID, nil); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mock.override != owningOrgID {
		t.Errorf("organisation scope = %q, want the owning organisation %q", mock.override, owningOrgID)
	}
}
