package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type clusterRenameCall struct {
	clusterID string
	newName   string
}

type clusterRenameMock struct {
	cluster     client.ClusterListItem
	calls       []clusterRenameCall
	renameError error
}

func (mock *clusterRenameMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if mock.cluster.ID == clusterID {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func (mock *clusterRenameMock) GetCluster(name string) (client.ClusterListItem, error) {
	if mock.cluster.Name == name {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

// RenameCluster answers the way the platform does: the new name, the old one
// and the GitOps directory the old name resolved to.
func (mock *clusterRenameMock) RenameCluster(_ context.Context, clusterID string, newName string) (*client.ClusterRenameResult, error) {
	mock.calls = append(mock.calls, clusterRenameCall{clusterID: clusterID, newName: newName})
	if mock.renameError != nil {
		return nil, mock.renameError
	}
	return &client.ClusterRenameResult{ClusterID: clusterID, Name: newName, PreviousName: mock.cluster.Name,
		IacDirectory: "clusters/" + mock.cluster.Name + "-k4m8t2x6pd", Changed: newName != mock.cluster.Name}, nil
}

func newClusterRenameMock() *clusterRenameMock {
	return &clusterRenameMock{cluster: client.ClusterListItem{
		ID: "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e", Name: "web-staging"}}
}

func runClusterRenameCommand(t *testing.T, mock any, arguments ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, "", []*cobra.Command{clusterRenameCmd}, append([]string{"cluster", "rename"}, arguments...)...)
}

// The ticket's own rename, by name: one write to the cluster's id, and the
// output says what changed and what the operator still has to do.
func TestClusterRenameRenamesTheClusterByName(t *testing.T) {
	mock := newClusterRenameMock()

	output, runError := runClusterRenameCommand(t, mock, "web-staging", "web-dev")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := clusterRenameCall{clusterID: mock.cluster.ID, newName: "web-dev"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want one %+v", mock.calls, want)
	}
	for _, line := range []string{
		"Cluster 'web-staging' renamed to 'web-dev'.",
		"GitOps files stay in clusters/web-staging-k4m8t2x6pd.",
		"Update metadata.name to 'web-dev'",
		"'ankra cluster kubeconfig add web-dev'",
	} {
		if !strings.Contains(output, line) {
			t.Errorf("output missing %q:\n%s", line, output)
		}
	}
}

// The cluster may be named by id, and -o json carries the platform's answer.
func TestClusterRenameAcceptsAClusterIDAndRendersJSON(t *testing.T) {
	mock := newClusterRenameMock()

	output, runError := runClusterRenameCommand(t, mock, mock.cluster.ID, "web-dev", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded clusterRenameOutput
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	if decoded.ClusterID != mock.cluster.ID || decoded.Name != "web-dev" || decoded.PreviousName != "web-staging" ||
		!decoded.Changed {
		t.Fatalf("json = %+v", decoded)
	}
}

// -o yaml uses the keys -o json uses, so a script that reads one format reads
// the other by the same names.
func TestClusterRenameYAMLUsesTheJSONKeys(t *testing.T) {
	mock := newClusterRenameMock()

	output, runError := runClusterRenameCommand(t, mock, "web-staging", "web-dev", "-o", "yaml")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	for _, line := range []string{
		"cluster_id: " + mock.cluster.ID,
		"\nname: web-dev",
		"previous_name: web-staging",
		"iac_directory: clusters/web-staging-k4m8t2x6pd",
		"changed: true",
	} {
		if !strings.Contains(output, line) {
			t.Errorf("yaml output lacks %q:\n%s", line, output)
		}
	}
}

// A name the platform would refuse is a usage error, and nothing is sent.
func TestClusterRenameRefusesABadNameBeforeWriting(t *testing.T) {
	for _, name := range []string{"Web-Dev", "web_dev", "web.dev", "web-", "1web",
		strings.Repeat("a", 54)} {
		t.Run(name, func(subtest *testing.T) {
			mock := newClusterRenameMock()
			_, runError := runClusterRenameCommand(subtest, mock, "web-staging", name)
			if exitCodeFor(runError) != exitUsage {
				subtest.Fatalf("exit = %d (%v), want usage", exitCodeFor(runError), runError)
			}
			if len(mock.calls) != 0 {
				subtest.Fatalf("a bad name was sent: %+v", mock.calls)
			}
		})
	}
}

// A platform refusal (a taken name, a GitOps-connected cluster) is returned
// with its detail.
func TestClusterRenameReturnsThePlatformsRefusal(t *testing.T) {
	mock := newClusterRenameMock()
	mock.renameError = errors.New("Cluster with this name already exists. Please choose a different name.")

	_, runError := runClusterRenameCommand(t, mock, "web-staging", "web-dev")
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("expected the platform's refusal, got %v", runError)
	}
}

// A cluster the organisation does not have is not-found (exit 3).
func TestClusterRenameUnknownClusterIsNotFound(t *testing.T) {
	mock := newClusterRenameMock()

	_, runError := runClusterRenameCommand(t, mock, "no-such-cluster", "web-dev")
	if exitCodeFor(runError) != exitNotFound {
		t.Fatalf("exit = %d (%v), want not found", exitCodeFor(runError), runError)
	}
	if len(mock.calls) != 0 {
		t.Fatalf("an unknown cluster was renamed: %+v", mock.calls)
	}
}

// The cluster chosen with 'ankra cluster select' follows the rename, because
// several commands look the selection up by name; another cluster's
// selection is left alone.
func TestFollowRenameInSelectionUpdatesOnlyTheRenamedCluster(t *testing.T) {
	withTempHome(t)
	selected := client.ClusterListItem{ID: "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e", Name: "web-staging"}
	if saveError := saveSelectedCluster(selected); saveError != nil {
		t.Fatalf("saving the selection: %v", saveError)
	}

	if note, warning := followRenameInSelection("another-cluster-id", "web-dev"); note != "" || warning != "" {
		t.Fatalf("another cluster's rename touched the selection: %q %q", note, warning)
	}
	if reloaded, _ := loadSelectedCluster(); reloaded.Name != "web-staging" {
		t.Fatalf("selection = %q, want unchanged", reloaded.Name)
	}

	if note, warning := followRenameInSelection(selected.ID, "web-dev"); !strings.Contains(note, "web-dev") || warning != "" {
		t.Fatalf("note = %q, warning = %q; want a note naming the new name", note, warning)
	}
	reloaded, loadError := loadSelectedCluster()
	if loadError != nil || reloaded.ID != selected.ID || reloaded.Name != "web-dev" {
		t.Fatalf("selection = %+v (%v), want the renamed cluster under its new name", reloaded, loadError)
	}
}

// A selection of the renamed cluster that cannot be saved is reported, not
// silently left on the old name.
func TestFollowRenameInSelectionWarnsWhenTheSelectionCannotBeSaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only file does not stop root from writing it")
	}
	withTempHome(t)
	selected := client.ClusterListItem{ID: "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e", Name: "web-staging"}
	if saveError := saveSelectedCluster(selected); saveError != nil {
		t.Fatalf("saving the selection: %v", saveError)
	}
	path, pathError := selectedClusterFile()
	if pathError != nil {
		t.Fatalf("selection path: %v", pathError)
	}
	if chmodError := os.Chmod(path, 0400); chmodError != nil {
		t.Fatalf("making the selection read-only: %v", chmodError)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	note, warning := followRenameInSelection(selected.ID, "web-dev")
	if note != "" || !strings.Contains(warning, "ankra cluster select web-dev") {
		t.Fatalf("note = %q, warning = %q; want a warning naming the fix", note, warning)
	}
}

// --cluster stands in for the cluster argument.
func TestClusterRenameTakesTheClusterFromTheFlag(t *testing.T) {
	mock := newClusterRenameMock()

	if _, runError := runClusterRenameCommand(t, mock, "--cluster", "web-staging", "web-dev"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := clusterRenameCall{clusterID: mock.cluster.ID, newName: "web-dev"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want one %+v", mock.calls, want)
	}
}
