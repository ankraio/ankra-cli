package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"ankra/internal/client"
)

const (
	flagOrderOwningOrganisationID   = "organisation-owning-id"
	flagOrderOwningOrganisationSlug = "owning-org"
	flagOrderSavedOrganisationID    = "organisation-saved-id"
	flagOrderClusterName            = "e2e-cluster"
	flagOrderClusterID              = "e2e-cluster-id"
)

// organisationScopedClusterMock answers cluster lookups the way the platform
// does: a cluster name only resolves inside the organisation that owns it, so
// a lookup made before --org is applied misses.
type organisationScopedClusterMock struct {
	terminalMock
	organisationOverride string
	lookups              []string
	targetedClusterIDs   []string
	lookupError          error
}

func (m *organisationScopedClusterMock) SetOrganisationOverride(organisationID string) {
	m.organisationOverride = organisationID
}

func (m *organisationScopedClusterMock) OrganisationOverride() string {
	return m.organisationOverride
}

func (m *organisationScopedClusterMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	owningName := "Owning Organisation"
	owningSlug := flagOrderOwningOrganisationSlug
	savedName := "Saved Organisation"
	return []client.OrganisationSummary{
		{OrganisationID: flagOrderSavedOrganisationID, Name: &savedName},
		{OrganisationID: flagOrderOwningOrganisationID, Name: &owningName, Slug: &owningSlug},
	}, nil
}

func (m *organisationScopedClusterMock) GetCluster(name string) (client.ClusterListItem, error) {
	m.lookups = append(m.lookups, name+"@"+m.organisationOverride)
	if m.lookupError != nil {
		return client.ClusterListItem{}, m.lookupError
	}
	if name == flagOrderClusterName && m.organisationOverride == flagOrderOwningOrganisationID {
		return client.ClusterListItem{ID: flagOrderClusterID, Name: flagOrderClusterName}, nil
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for name %q: %w", name, client.ErrClusterNotFound)
}

func (m *organisationScopedClusterMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	m.targetedClusterIDs = append(m.targetedClusterIDs, clusterID)
	pod := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "web-1", "namespace": "payments"},
		"spec":       map[string]any{"containers": []any{map[string]any{"name": "app"}}},
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{
		{Status: "success", Kind: "Pod", Items: []any{pod}},
	}}, nil
}

func (m *organisationScopedClusterMock) ListPods(clusterID string, options *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	m.targetedClusterIDs = append(m.targetedClusterIDs, clusterID)
	return &client.ListPodsResponse{Page: 1, TotalPages: 1}, nil
}

func (m *organisationScopedClusterMock) OpenPodTerminal(ctx context.Context, clusterID string, request client.PodTerminalRequest) (client.PodTerminal, error) {
	m.targetedClusterIDs = append(m.targetedClusterIDs, clusterID)
	return m.terminalMock.OpenPodTerminal(ctx, clusterID, request)
}

func (m *organisationScopedClusterMock) StreamPodLogs(ctx context.Context, clusterID string, options client.PodLogOptions, writer io.Writer) error {
	m.targetedClusterIDs = append(m.targetedClusterIDs, clusterID)
	return nil
}

func newOrganisationScopedClusterMock() *organisationScopedClusterMock {
	terminal := newFakePodTerminal(nil, exitFrame(0), client.PodTerminalFrame{Type: "end"})
	return &organisationScopedClusterMock{terminalMock: terminalMock{terminal: terminal}}
}

// writeSavedSelectionInAnotherOrganisation recreates the reported setup: the
// CLI's saved organisation and cluster belong somewhere other than the
// organisation the command names with --org.
func writeSavedSelectionInAnotherOrganisation(t *testing.T) {
	t.Helper()
	writeSelectedClusterJSON(t)
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		t.Fatalf("home directory: %v", homeError)
	}
	document := fmt.Sprintf(`{"organisation_id":%q,"name":"Saved Organisation"}`, flagOrderSavedOrganisationID)
	if writeError := os.WriteFile(filepath.Join(home, ".ankra", "organisation.json"), []byte(document), 0o600); writeError != nil {
		t.Fatalf("write organisation.json: %v", writeError)
	}
}

// resetFlagsToDefaults puts every flag of command and its ancestors back to
// its default, so one invocation's flags never leak into the next through the
// shared command tree.
func resetFlagsToDefaults(command *cobra.Command) {
	reset := func(flag *pflag.Flag) {
		if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
			_ = sliceValue.Replace(nil)
		} else {
			_ = flag.Value.Set(flag.DefValue)
		}
		flag.Changed = false
	}
	for current := command; current != nil; current = current.Parent() {
		current.LocalFlags().VisitAll(reset)
		current.PersistentFlags().VisitAll(reset)
	}
}

type flagOrderCase struct {
	name string
	// commandPath and commandArguments are joined with the --org/--cluster
	// pair either before the command path or after the leaf command.
	commandPath      []string
	commandArguments []string
}

func flagOrderCases() []flagOrderCase {
	return []flagOrderCase{
		{name: "exec", commandPath: []string{"cluster", "exec"}, commandArguments: []string{"web-1", "-n", "payments", "-c", "app", "--", "true"}},
		{name: "get", commandPath: []string{"cluster", "get", "pods"}, commandArguments: []string{"-n", "payments"}},
		{name: "logs", commandPath: []string{"cluster", "logs"}, commandArguments: []string{"web-1", "-n", "payments", "-c", "app", "--follow=false"}},
		{name: "describe", commandPath: []string{"cluster", "describe"}, commandArguments: []string{"pod", "web-1", "-n", "payments"}},
		{name: "info", commandPath: []string{"cluster", "info"}},
	}
}

func TestClusterCommandsResolveClusterInOrganisationFromFlagsInEitherPosition(t *testing.T) {
	targetingFlags := []string{"--org", flagOrderOwningOrganisationSlug, "--cluster", flagOrderClusterName}
	for _, testCase := range flagOrderCases() {
		for _, position := range []string{"before_command_path", "after_command"} {
			t.Run(testCase.name+"/"+position, func(t *testing.T) {
				var arguments []string
				if position == "before_command_path" {
					arguments = slices.Concat(targetingFlags, testCase.commandPath, testCase.commandArguments)
				} else {
					arguments = slices.Concat(testCase.commandPath, targetingFlags, testCase.commandArguments)
				}
				leaf, _, findError := rootCmd.Find(testCase.commandPath)
				if findError != nil {
					t.Fatalf("finding %v: %v", testCase.commandPath, findError)
				}
				resetFlagsToDefaults(leaf)
				t.Cleanup(func() {
					resetFlagsToDefaults(leaf)
					leaf.SilenceErrors = false
				})
				mock := newOrganisationScopedClusterMock()
				setMockClient(t, mock)
				writeSavedSelectionInAnotherOrganisation(t)

				var runError error
				output := captureStdout(t, func() {
					_, runError = executeCommand(arguments...)
				})
				if runError != nil {
					t.Fatalf("ankra %s: %v\n%s", strings.Join(arguments, " "), runError, output)
				}
				expectedFirstLookup := flagOrderClusterName + "@" + flagOrderOwningOrganisationID
				if len(mock.lookups) == 0 || mock.lookups[0] != expectedFirstLookup {
					t.Errorf("cluster lookups = %v, want the first one scoped to --org (%s)", mock.lookups, expectedFirstLookup)
				}
				if testCase.name == "info" {
					if !strings.Contains(output, flagOrderClusterID) {
						t.Errorf("info output does not show %s:\n%s", flagOrderClusterID, output)
					}
					return
				}
				if len(mock.targetedClusterIDs) == 0 {
					t.Fatal("the command never reached the cluster")
				}
				for _, clusterID := range mock.targetedClusterIDs {
					if clusterID != flagOrderClusterID {
						t.Errorf("request targeted cluster %q, want %q (not the saved selection)", clusterID, flagOrderClusterID)
					}
				}
			})
		}
	}
}

func TestClusterFlagNotFoundNamesTheOrganisationSearched(t *testing.T) {
	leaf, _, _ := rootCmd.Find([]string{"cluster", "get", "pods"})
	resetFlagsToDefaults(leaf)
	t.Cleanup(func() { resetFlagsToDefaults(leaf) })
	mock := newOrganisationScopedClusterMock()
	setMockClient(t, mock)
	writeSavedSelectionInAnotherOrganisation(t)

	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand("--cluster", "nowhere", "cluster", "get", "pods", "-n", "payments")
	})
	if runError == nil {
		t.Fatal("an unknown cluster must fail")
	}
	if code := exitCodeFor(runError); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
	for _, expected := range []string{`cluster "nowhere" not found`, flagOrderSavedOrganisationID, "--org"} {
		if !strings.Contains(runError.Error(), expected) {
			t.Errorf("error %q is missing %q", runError.Error(), expected)
		}
	}
	if len(mock.targetedClusterIDs) != 0 {
		t.Errorf("nothing should reach a cluster: %v", mock.targetedClusterIDs)
	}
}

func TestClusterFlagLookupFailureIsNotReportedAsNotFound(t *testing.T) {
	leaf, _, _ := rootCmd.Find([]string{"cluster", "get", "pods"})
	resetFlagsToDefaults(leaf)
	t.Cleanup(func() { resetFlagsToDefaults(leaf) })
	mock := newOrganisationScopedClusterMock()
	mock.lookupError = errors.New("connection reset by peer")
	setMockClient(t, mock)
	writeSavedSelectionInAnotherOrganisation(t)

	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand("--org", flagOrderOwningOrganisationSlug, "--cluster", flagOrderClusterName,
			"cluster", "get", "pods", "-n", "payments")
	})
	if runError == nil {
		t.Fatal("a failed lookup must fail the command")
	}
	if strings.Contains(runError.Error(), "not found") {
		t.Errorf("a lookup that never got an answer must not claim the cluster is missing: %v", runError)
	}
	if !strings.Contains(runError.Error(), "connection reset by peer") {
		t.Errorf("the underlying failure is lost: %v", runError)
	}
	if code := exitCodeFor(runError); code == exitNotFound {
		t.Errorf("exit code = %d, a failed lookup is not a not-found", code)
	}
}
