package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
	"ankra/internal/kubeconfig"
)

const (
	otherOrgClusterID = "11111111-1111-1111-1111-111111111111"
	selectedOrgID     = "org-depict"
	owningOrgID       = "org-ankra"
)

// orgScopeMock models the one behaviour that makes the bug possible: the
// backend resolves every cluster reference *inside* the organisation the
// request is scoped to, and answers 404 for anything outside it.
type orgScopeMock struct {
	baseMock
	override      string
	selected      string
	organisations []client.OrganisationSummary
	// clustersByOrg maps an organisation ID to the clusters it owns.
	clustersByOrg map[string][]client.ClusterListItem

	listOrganisationsErr   error
	listOrganisationsCalls int
	byIDScopes             []string
	tokenScopes            []string

	// failByIDCalls makes the first n by-ID lookups fail for a reason that
	// says nothing about the cluster, modelling a single bad response.
	failByIDCalls int
}

// scope is the organisation the current request resolves against: the
// per-invocation override when set, else the selected organisation.
func (m *orgScopeMock) scope() string {
	if m.override != "" {
		return m.override
	}
	return m.selected
}

func (m *orgScopeMock) SetOrganisationOverride(orgID string) { m.override = orgID }

func (m *orgScopeMock) OrganisationOverride() string { return m.override }

func (m *orgScopeMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	m.listOrganisationsCalls++
	if m.listOrganisationsErr != nil {
		return nil, m.listOrganisationsErr
	}
	return m.organisations, nil
}

func (m *orgScopeMock) GetCluster(name string) (client.ClusterListItem, error) {
	for _, cluster := range m.clustersByOrg[m.scope()] {
		if cluster.Name == name {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for name %q: %w", name, client.ErrClusterNotFound)
}

func (m *orgScopeMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	m.byIDScopes = append(m.byIDScopes, m.scope())
	if m.failByIDCalls > 0 {
		m.failByIDCalls--
		return client.ClusterListItem{}, errors.New("request failed: unexpected EOF")
	}
	for _, cluster := range m.clustersByOrg[m.scope()] {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for id %q: %w", clusterID, client.ErrClusterNotFound)
}

func (m *orgScopeMock) GetClusterKubeToken(_ context.Context, clusterID string) (*client.KubeToken, error) {
	m.tokenScopes = append(m.tokenScopes, m.scope())
	for _, cluster := range m.clustersByOrg[m.scope()] {
		if cluster.ID == clusterID {
			return &client.KubeToken{
				Token:  "kube-token",
				Server: "https://api.platform.ankra.dev/api/v1/clusters/" + clusterID + "/k8s",
			}, nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, `kube token request failed: status 404, body: {"detail":"Cluster not found"}`)
}

func organisationName(name string) *string { return &name }

// crossOrgMock is the bead's exact situation: the CLI is selected on one
// organisation while the cluster lives in another one the user also belongs
// to, and holds a grant in.
func crossOrgMock() *orgScopeMock {
	return &orgScopeMock{
		selected: selectedOrgID,
		organisations: []client.OrganisationSummary{
			{OrganisationID: selectedOrgID, Name: organisationName("Depict"), UserCurrent: true},
			{OrganisationID: "org-acme", Name: organisationName("Acme")},
			{OrganisationID: owningOrgID, Name: organisationName("Ankra AB")},
		},
		clustersByOrg: map[string][]client.ClusterListItem{
			selectedOrgID: {{ID: "22222222-2222-2222-2222-222222222222", Name: "depict-prod", OrganisationID: selectedOrgID}},
			owningOrgID:   {{ID: otherOrgClusterID, Name: "ankra-prod", OrganisationID: owningOrgID}},
		},
	}
}

// withOrgScopeMock installs the mock and an isolated HOME carrying the
// persistently selected organisation, so nothing reads the developer's real
// ~/.ankra state.
func withOrgScopeMock(t *testing.T, mock *orgScopeMock) *orgScopeMock {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if mock.selected != "" {
		selectionDir := filepath.Join(home, ".ankra")
		if err := os.MkdirAll(selectionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		selection := `{"organisation_id":"` + mock.selected + `","name":"Depict","role":"admin"}`
		if err := os.WriteFile(filepath.Join(selectionDir, "organisation.json"), []byte(selection), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	originalClient := apiClient
	originalBaseURL := baseURL
	apiClient = mock
	baseURL = "https://test.ankra.app"
	t.Cleanup(func() {
		apiClient = originalClient
		baseURL = originalBaseURL
	})
	return mock
}

func TestResolveKubeTokenClusterIDAdoptsTheOwningOrganisation(t *testing.T) {
	// The bug: the exec credential plugin passes no organisation, so a
	// cluster ID from another organisation used to be forwarded verbatim and
	// 404'd. It must resolve against the organisation that owns it instead.
	mock := withOrgScopeMock(t, crossOrgMock())

	clusterID, err := resolveKubeTokenClusterID(otherOrgClusterID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if clusterID != otherOrgClusterID {
		t.Fatalf("cluster id = %q, want %q", clusterID, otherOrgClusterID)
	}
	if mock.override != owningOrgID {
		t.Fatalf("organisation scope = %q, want the owning organisation %q", mock.override, owningOrgID)
	}

	// The mint that follows now runs in the owning organisation and succeeds.
	if _, err := apiClient.GetClusterKubeToken(context.Background(), clusterID); err != nil {
		t.Fatalf("token mint after adoption failed: %v", err)
	}
	if got := mock.tokenScopes; len(got) != 1 || got[0] != owningOrgID {
		t.Errorf("token minted in scopes %v, want [%s]", got, owningOrgID)
	}
}

func TestResolveGatewayClusterIDNotifiesWhenAdopting(t *testing.T) {
	withOrgScopeMock(t, crossOrgMock())

	var notes bytes.Buffer
	if _, err := resolveGatewayClusterID(otherOrgClusterID, &notes); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	note := notes.String()
	for _, fragment := range []string{otherOrgClusterID, `"Ankra AB" (` + owningOrgID + `)`, `"Depict" (` + selectedOrgID + `)`} {
		if !strings.Contains(note, fragment) {
			t.Errorf("note %q is missing %q", note, fragment)
		}
	}
}

func TestResolveKubeTokenClusterIDStaysQuiet(t *testing.T) {
	// kubectl re-runs the credential plugin on every command, so the quiet
	// variant must not write a per-invocation note anywhere.
	mock := withOrgScopeMock(t, crossOrgMock())
	stderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	_, resolveErr := resolveKubeTokenClusterID(otherOrgClusterID)
	os.Stderr = stderr
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	if resolveErr != nil {
		t.Fatalf("resolve failed: %v", resolveErr)
	}
	var captured bytes.Buffer
	if _, err := captured.ReadFrom(read); err != nil {
		t.Fatal(err)
	}
	if captured.Len() != 0 {
		t.Errorf("kube-token wrote %q to stderr; the credential plugin must stay silent", captured.String())
	}
	if mock.override != owningOrgID {
		t.Errorf("organisation scope = %q, want %q", mock.override, owningOrgID)
	}
}

func TestResolveGatewayClusterIDLeavesScopeAloneWhenTheClusterIsInScope(t *testing.T) {
	// The common case must not pay for the cross-organisation search, and
	// must not touch the organisation scope.
	mock := withOrgScopeMock(t, crossOrgMock())

	const inScope = "22222222-2222-2222-2222-222222222222"
	if _, err := resolveGatewayClusterID(inScope, nil); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mock.override != "" {
		t.Errorf("organisation scope = %q, want it untouched", mock.override)
	}
	if mock.listOrganisationsCalls != 0 {
		t.Errorf("organisations listed %d times, want 0", mock.listOrganisationsCalls)
	}
	if len(mock.byIDScopes) != 1 {
		t.Errorf("by-id lookups = %v, want exactly one", mock.byIDScopes)
	}
}

func TestResolveGatewayClusterIDHonoursAnExplicitOrganisationOverride(t *testing.T) {
	// ANKRA_ORG=<org> kubectl ... is the documented workaround: root resolves
	// it into the client's override before the command runs. That path must
	// still resolve in one lookup, with no search and no re-scoping.
	mock := withOrgScopeMock(t, crossOrgMock())
	mock.override = owningOrgID

	if _, err := resolveGatewayClusterID(otherOrgClusterID, nil); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mock.override != owningOrgID {
		t.Errorf("organisation scope = %q, want the override %q to survive", mock.override, owningOrgID)
	}
	if mock.listOrganisationsCalls != 0 {
		t.Errorf("organisations listed %d times, want 0", mock.listOrganisationsCalls)
	}
}

func TestResolveGatewayClusterIDNamesTheOrganisationsSearched(t *testing.T) {
	// A cluster ID that exists nowhere must say which organisations were
	// looked in, not "Cluster not found".
	mock := withOrgScopeMock(t, crossOrgMock())
	const unknown = "99999999-9999-9999-9999-999999999999"

	_, err := resolveGatewayClusterID(unknown, nil)
	if err == nil {
		t.Fatal("expected an error for a cluster no organisation has")
	}
	for _, fragment := range []string{
		`cluster ` + unknown + ` is not in organisation "Depict" (` + selectedOrgID + `)`,
		"not found in the 2 other organisation(s) you belong to",
		`"Acme" (org-acme)`,
		`"Ankra AB" (` + owningOrgID + `)`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q is missing %q", err, fragment)
		}
	}
	if code := exitCodeFor(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
	if mock.override != "" {
		t.Errorf("a failed search left the organisation scope at %q; it must be restored", mock.override)
	}
}

func TestResolveGatewayClusterIDPassesThroughWhenTheSearchCannotRun(t *testing.T) {
	// An unreachable organisation list is not evidence about a cluster, so
	// the ID goes to the backend exactly as it did before the search existed.
	mock := withOrgScopeMock(t, crossOrgMock())
	mock.listOrganisationsErr = errors.New("dial tcp: connection refused")

	clusterID, err := resolveGatewayClusterID(otherOrgClusterID, nil)
	if err != nil {
		t.Fatalf("an inconclusive search must not fail the command: %v", err)
	}
	if clusterID != otherOrgClusterID {
		t.Errorf("cluster id = %q, want the input passed through", clusterID)
	}
}

func TestResolveGatewayClusterIDSurvivesOneBadLookup(t *testing.T) {
	// A single failed response must not be promoted into "this cluster does
	// not exist": the cluster is in the scoped organisation all along, and
	// the command has to keep working.
	mock := withOrgScopeMock(t, crossOrgMock())
	mock.failByIDCalls = 1
	const inScope = "22222222-2222-2222-2222-222222222222"

	clusterID, err := resolveGatewayClusterID(inScope, nil)
	if err != nil {
		t.Fatalf("one bad lookup failed the command: %v", err)
	}
	if clusterID != inScope {
		t.Errorf("cluster id = %q, want %q", clusterID, inScope)
	}
	if mock.override != "" {
		t.Errorf("organisation scope = %q, want it untouched", mock.override)
	}
}

func TestExplainKubeTokenNotFoundNamesTheOwningOrganisation(t *testing.T) {
	// Defence in depth for the mint itself: a 404 must never be reported as
	// a missing cluster while an organisation the caller belongs to has it.
	withOrgScopeMock(t, crossOrgMock())
	original := client.NewUnexpectedResponseError(404, `kube token request failed: status 404, body: {"detail":"Cluster not found"}`)

	err := decorateKubeTokenError(original, otherOrgClusterID, otherOrgClusterID)
	for _, fragment := range []string{
		`is not in organisation "Depict" (` + selectedOrgID + `)`,
		`it belongs to "Ankra AB" (` + owningOrgID + `)`,
		"ankra --org " + owningOrgID,
		"ANKRA_ORG=" + owningOrgID,
		"ankra cluster kubeconfig add " + otherOrgClusterID,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q is missing %q", err, fragment)
		}
	}
	// The original response stays in the chain, so the exit code is unchanged.
	var unexpected *client.UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != 404 {
		t.Fatalf("expected the 404 to stay wrapped, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
}

func TestDecorateKubeTokenErrorLeavesOtherStatusesToTheAccessSuggestion(t *testing.T) {
	withOrgScopeMock(t, crossOrgMock())
	denied := client.NewUnexpectedResponseError(403, `kube token request failed: status 403, body: {"detail":"You do not have access to this cluster"}`)

	err := decorateKubeTokenError(denied, "ankra-prod", otherOrgClusterID)
	if !strings.Contains(err.Error(), "ankra cluster access grant <your-email> --cluster ankra-prod --role view") {
		t.Errorf("403 error = %v, want the access-grant suggestion", err)
	}

	transport := errors.New("dial tcp: connection refused")
	if got := decorateKubeTokenError(transport, "ankra-prod", otherOrgClusterID); got != transport {
		t.Errorf("transport error = %v, want it passed through unchanged", got)
	}
}

func TestKubeconfigAddPinsTheOwningOrganisationForACrossOrgID(t *testing.T) {
	// The other half of the fix: the written context must be self-contained.
	// Pinning the (wrong) selected organisation here is what produced a
	// kubeconfig entry that 404s on first use.
	withOrgScopeMock(t, crossOrgMock())
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = otherOrgClusterID

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatalf("kubeconfig add failed: %v", err)
	}
	config, err := kubeconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	names := config.ManagedContextNames()
	if len(names) != 1 || names[0] != "ankra-ankra-prod" {
		t.Fatalf("context names = %v, want [ankra-ankra-prod]", names)
	}
	want := "cluster kube-token --cluster " + otherOrgClusterID + " --org " + owningOrgID
	if got := strings.Join(execArgsForUser(t, config, "ankra-ankra-prod"), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
}

func TestFindClusterInOtherOrganisationsNeverCountsAFailedLookupAsAMiss(t *testing.T) {
	// A lookup that failed is not a miss. Counting one would claim an
	// organisation was searched when it never answered, and turn "we do not
	// know" into "it does not exist".
	mock := withOrgScopeMock(t, crossOrgMock())
	// Two organisations are searched (Acme, then Ankra AB); fail both.
	mock.failByIDCalls = 3 // the in-scope lookup plus both searches
	const unknown = "99999999-9999-9999-9999-999999999999"

	search := findClusterInOtherOrganisations(unknown)
	if search.found {
		t.Fatal("no organisation answered; nothing can have been found")
	}
	if search.err == nil {
		t.Fatal("failed lookups must make the search inconclusive")
	}
	if len(search.searchedLabels) != 0 {
		t.Errorf("searched labels = %v, want none: those organisations never answered", search.searchedLabels)
	}

	// And an inconclusive search must not fail the command.
	mock.failByIDCalls = 3
	if _, err := resolveGatewayClusterID(unknown, nil); err != nil {
		t.Errorf("inconclusive search failed the command: %v", err)
	}
}

func TestResolveGatewayClusterIDNeverReplacesAnExplicitOrganisation(t *testing.T) {
	// --org / ANKRA_ORG states which organisation to use. Silently running
	// somewhere else would defeat the point, so the search only informs the
	// error: it names the owner and the exact retry.
	mock := withOrgScopeMock(t, crossOrgMock())
	mock.override = "org-acme"

	_, err := resolveGatewayClusterID(otherOrgClusterID, nil)
	if err == nil {
		t.Fatal("expected an error rather than a silent switch away from the pinned organisation")
	}
	if mock.override != "org-acme" {
		t.Errorf("organisation scope = %q, want the explicit pin %q to survive", mock.override, "org-acme")
	}
	for _, fragment := range []string{
		`it belongs to "Ankra AB" (` + owningOrgID + `)`,
		"ankra --org " + owningOrgID,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q is missing %q", err, fragment)
		}
	}
}

func TestKubeconfigAddRefusesToWriteAContextItKnowsIsBroken(t *testing.T) {
	// A raw-ID context pins whatever organisation is in scope, so writing one
	// after a search that settled the question just moves the 404 to first
	// use.
	withOrgScopeMock(t, crossOrgMock())
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "99999999-9999-9999-9999-999999999999"

	var out bytes.Buffer
	err := kubeconfigAdd(&out)
	if err == nil {
		t.Fatal("expected an error for a cluster no organisation has")
	}
	if !strings.Contains(err.Error(), "not found in the 2 other organisation(s) you belong to") {
		t.Errorf("error = %v, want the organisations searched to be named", err)
	}
	if code := exitCodeFor(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("no kubeconfig should be written on failure: %s", path)
	}
}

func TestNotInScopedOrganisationErrorReadsAsProseWithoutAScopeLabel(t *testing.T) {
	// Nothing local identifies the scope (no override, no saved selection),
	// so the message must not name an organisation called "the selected
	// organisation".
	search := organisationScopeSearch{err: errors.New("dial tcp: connection refused")}

	err := notInScopedOrganisationError(search, otherOrgClusterID, nil)
	want := "cluster " + otherOrgClusterID + " is not in the organisation this command is scoped to"
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want it to start with %q", err, want)
	}
	if !strings.Contains(err.Error(), "could not be checked") {
		t.Errorf("error = %q, want it to admit the search did not run", err)
	}
}

func TestFindClusterInOtherOrganisationsRejectsNames(t *testing.T) {
	// Cluster names are unique only within an organisation, so searching for
	// one across organisations would mean guessing. The search declines, and
	// declining counts as inconclusive rather than as "does not exist".
	withOrgScopeMock(t, crossOrgMock())

	search := findClusterInOtherOrganisations("ankra-prod")
	if search.found {
		t.Fatal("a name must not resolve across organisations")
	}
	if search.err == nil {
		t.Fatal("declining to search must be reported as inconclusive")
	}
}
