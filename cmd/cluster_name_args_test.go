package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// testClusterID is id-shaped, so resolveClusterArg forwards it untouched and
// treats every other argument as a cluster name. Every command test that
// passes a cluster argument uses it: a placeholder like "cluster-1" is a NAME
// now, and sends the command to the cluster listing instead (ankra-aprvp).
const testClusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

// clusterArgMock answers the cluster listing that resolveClusterArg pages
// through, and records the ids the commands under test forward to the API.
type clusterArgMock struct {
	baseMock

	clusters  []client.ClusterListItem
	listCalls int

	// totalPages reports more pages than the resolver will read, to exercise
	// the paging cap; 0 means the fixture fits on one page.
	totalPages int

	workersRequested   string
	meshReadyRequested string
	readinessRequested []string
}

func (m *clusterArgMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	m.listCalls++
	totalPages := m.totalPages
	if totalPages == 0 {
		totalPages = 1
	}
	return &client.ClusterListResponse{
		Result:     m.clusters,
		Pagination: client.Pagination{TotalPages: totalPages, Page: page, PageSize: pageSize},
	}, nil
}

func (m *clusterArgMock) GetScalewayWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	m.workersRequested = clusterID
	return &client.WorkerCountResult{WorkerCount: 3, Min: 1, Max: 5}, nil
}

func (m *clusterArgMock) MakeClusterMeshReady(clusterID string, sitePublicIP string) (*client.ClusterMeshMakeReadyResult, error) {
	m.meshReadyRequested = clusterID
	return &client.ClusterMeshMakeReadyResult{ClusterID: clusterID}, nil
}

func (m *clusterArgMock) CheckClusterMeshReadiness(clusterIDs []string) (map[string]client.ClusterMeshReadiness, error) {
	m.readinessRequested = clusterIDs
	readiness := make(map[string]client.ClusterMeshReadiness, len(clusterIDs))
	for _, clusterID := range clusterIDs {
		readiness[clusterID] = client.ClusterMeshReadiness{Ready: true}
	}
	return readiness, nil
}

func withClusterArgMock(t *testing.T, mock *clusterArgMock) {
	t.Helper()
	previous := apiClient
	apiClient = mock
	t.Cleanup(func() { apiClient = previous })
}

func newClusterArgMock() *clusterArgMock {
	return &clusterArgMock{clusters: []client.ClusterListItem{
		{ID: "11111111-2222-4333-8444-555555555555", Name: "other"},
		{ID: testClusterID, Name: "prod-eu"},
	}}
}

// A cluster id is what the routes want, so it must reach the API untouched -
// and without the listing request a name lookup costs.
func TestResolveClusterArgPassesAnIDThroughWithoutListing(t *testing.T) {
	mock := newClusterArgMock()
	withClusterArgMock(t, mock)

	resolved, err := resolveClusterArg(testClusterID)
	if err != nil {
		t.Fatalf("an id must resolve to itself: %v", err)
	}
	if resolved != testClusterID {
		t.Errorf("id must pass through unchanged, got %q", resolved)
	}
	if mock.listCalls != 0 {
		t.Errorf("an id must not cost a cluster listing, made %d", mock.listCalls)
	}
}

func TestResolveClusterArgResolvesANameCaseInsensitively(t *testing.T) {
	withClusterArgMock(t, newClusterArgMock())

	for _, typed := range []string{"prod-eu", "PROD-EU", "Prod-Eu"} {
		resolved, err := resolveClusterArg(typed)
		if err != nil {
			t.Fatalf("%q must resolve: %v", typed, err)
		}
		if resolved != testClusterID {
			t.Errorf("%q must resolve to %q, got %q", typed, testClusterID, resolved)
		}
	}
}

// An unknown name is refused before the request, naming the argument and how
// to find the right one. Forwarding it reached the route as a non-UUID path
// segment and came back as a bare 404 (ankra-aprvp).
func TestResolveClusterArgRefusesAnUnknownNameWithAHint(t *testing.T) {
	mock := newClusterArgMock()
	withClusterArgMock(t, mock)

	_, err := resolveClusterArg("staging")
	if err == nil {
		t.Fatal("an unknown name must be refused")
	}
	for _, expected := range []string{`cluster "staging" not found`, "ankra cluster list"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected %q in the error, got %q", expected, err.Error())
		}
	}
}

// The prompt for a destructive command names what the user typed, so they can
// recognise it; the resolved id follows so the cluster the API is about to be
// asked to act on is on screen too.
func TestClusterTargetNamesTheTypedArgumentAndTheResolvedID(t *testing.T) {
	if got := clusterTarget(testClusterID, testClusterID); got != fmt.Sprintf("%q", testClusterID) {
		t.Errorf("an id names only itself, got %s", got)
	}
	got := clusterTarget("prod-eu", testClusterID)
	for _, expected := range []string{`"prod-eu"`, testClusterID} {
		if !strings.Contains(got, expected) {
			t.Errorf("expected %q in the prompt target, got %s", expected, got)
		}
	}
}

// A provider command is the class this change is about: before ankra-aprvp
// only the playground verbs resolved a name.
func TestProviderCommandResolvesAClusterName(t *testing.T) {
	mock := newClusterArgMock()
	withClusterArgMock(t, mock)

	captureStdout(t, func() {
		if err := scalewayWorkersCmd.RunE(scalewayWorkersCmd, []string{"prod-eu"}); err != nil {
			t.Fatalf("workers by name failed: %v", err)
		}
	})
	if mock.workersRequested != testClusterID {
		t.Errorf("the name must resolve to the id, requested %q", mock.workersRequested)
	}
}

func TestClusterMeshMakeReadyResolvesAClusterName(t *testing.T) {
	mock := newClusterArgMock()
	withClusterArgMock(t, mock)

	captureStdout(t, func() {
		if err := clusterMeshMakeReadyCmd.RunE(clusterMeshMakeReadyCmd, []string{"prod-eu"}); err != nil {
			t.Fatalf("make-ready by name failed: %v", err)
		}
	})
	if mock.meshReadyRequested != testClusterID {
		t.Errorf("the name must resolve to the id, requested %q", mock.meshReadyRequested)
	}
}

// readiness takes several clusters at once: every argument resolves, and the
// report still labels each row with the name the user typed.
func TestClusterMeshReadinessResolvesEveryArgumentAndLabelsWhatWasTyped(t *testing.T) {
	mock := newClusterArgMock()
	withClusterArgMock(t, mock)

	output := captureStdout(t, func() {
		if err := clusterMeshReadinessCmd.RunE(clusterMeshReadinessCmd,
			[]string{"prod-eu", "11111111-2222-4333-8444-555555555555"}); err != nil {
			t.Fatalf("readiness by name failed: %v", err)
		}
	})
	want := []string{testClusterID, "11111111-2222-4333-8444-555555555555"}
	if len(mock.readinessRequested) != 2 ||
		mock.readinessRequested[0] != want[0] || mock.readinessRequested[1] != want[1] {
		t.Errorf("every argument must resolve, requested %v want %v", mock.readinessRequested, want)
	}
	if !strings.Contains(output, "prod-eu  ready") {
		t.Errorf("the report must label the row with the typed name, got: %s", output)
	}
}

// A ratchet: a cluster-scoped command added later must advertise that it takes
// a name too, so the fix does not quietly regress one command at a time.
//
// It reads every bracketed placeholder in a Use string rather than two literal
// spellings, so `[cluster_id]`, `<cluster-id>` and `<clusterID>` are caught as
// well as `<cluster_id>` - any of them would forward a typed name verbatim.
func TestEveryClusterArgumentAdvertisesTheName(t *testing.T) {
	placeholder := regexp.MustCompile(`<[^<>]+>|\[[^\[\]]+\]`)
	clusterID := regexp.MustCompile(`(?i)cluster[-_ ]?id`)
	var offenders []string
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		for _, token := range placeholder.FindAllString(command.Use, -1) {
			// The widened form is the whole point, so it is never an offender.
			if clusterID.MatchString(token) && !strings.Contains(token, "|name") {
				offenders = append(offenders, command.CommandPath()+" "+token)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
	if len(offenders) > 0 {
		t.Errorf("these commands still advertise an id-only cluster argument; "+
			"resolve it with resolveClusterArg and widen the Use string to <cluster_id|name>: %s",
			strings.Join(offenders, ", "))
	}
}

// Two clusters whose names differ only by case used to resolve to whichever
// the listing ordered first, silently pointing a command - `deprovision`
// included - at an arbitrary one of them.
func TestResolveClusterArgRefusesAnAmbiguousName(t *testing.T) {
	withClusterArgMock(t, &clusterArgMock{clusters: []client.ClusterListItem{
		{ID: testClusterID, Name: "Prod-EU"},
		{ID: "11111111-2222-4333-8444-555555555555", Name: "prod-eu"},
	}})

	_, err := resolveClusterArg("PROD-eu")
	if err == nil {
		t.Fatal("a name matching two clusters must be refused, not resolved to one of them")
	}
	for _, expected := range []string{"ambiguous", "Prod-EU", "prod-eu", testClusterID, "pass the cluster id"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected %q in the error, got %q", expected, err.Error())
		}
	}
}

// An exact match is the user's unambiguous intent, so it wins over a
// case-insensitive twin rather than being reported as ambiguous.
func TestResolveClusterArgPrefersTheExactName(t *testing.T) {
	withClusterArgMock(t, &clusterArgMock{clusters: []client.ClusterListItem{
		{ID: "11111111-2222-4333-8444-555555555555", Name: "PROD-EU"},
		{ID: testClusterID, Name: "prod-eu"},
	}})

	resolved, err := resolveClusterArg("prod-eu")
	if err != nil {
		t.Fatalf("an exact name must resolve even with a case-insensitive twin: %v", err)
	}
	if resolved != testClusterID {
		t.Errorf("the exactly-matching cluster must win, got %q", resolved)
	}
}

// "36 characters with four dashes" describes plenty of real cluster names, not
// just a UUID. Such a name used to be forwarded to the API as an id.
func TestResolveClusterArgTreatsAUUIDShapedNameAsAName(t *testing.T) {
	const name = "production-eu-primary-cluster-abcdef"
	if len(name) != 36 || strings.Count(name, "-") != 4 {
		t.Fatalf("the fixture must be 36 chars with 4 dashes to exercise the old heuristic, got %d/%d",
			len(name), strings.Count(name, "-"))
	}
	withClusterArgMock(t, &clusterArgMock{clusters: []client.ClusterListItem{{ID: testClusterID, Name: name}}})

	resolved, err := resolveClusterArg(name)
	if err != nil {
		t.Fatalf("a 36-char name with four dashes must be looked up, not forwarded as an id: %v", err)
	}
	if resolved != testClusterID {
		t.Errorf("expected the name to resolve to %q, got %q", testClusterID, resolved)
	}
}

// The resolver reads at most 5000 clusters. Beyond that it used to answer
// "not found", presenting a truncated listing as a verified negative on the
// resolution path of every command, destructive ones included.
func TestResolveClusterArgSaysTheListingWasTruncatedRatherThanNotFound(t *testing.T) {
	withClusterArgMock(t, &clusterArgMock{
		clusters:   []client.ClusterListItem{{ID: testClusterID, Name: "prod-eu"}},
		totalPages: 999,
	})

	_, err := resolveClusterArg("a-cluster-on-a-later-page")
	if err == nil {
		t.Fatal("an unresolved name must still be an error")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("a truncated listing must not be reported as a verified absence, got %q", err.Error())
	}
	for _, expected := range []string{"was not among the first", "pass the cluster id"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected %q in the error, got %q", expected, err.Error())
		}
	}
}
