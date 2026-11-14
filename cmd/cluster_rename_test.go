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
	baseMock
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
		IacDirectory: "clusters/" + mock.cluster.Name + "-qa234i7ojs", Changed: newName != mock.cluster.Name}, nil
}

func newClusterRenameMock() *clusterRenameMock {
	return &clusterRenameMock{cluster: client.ClusterListItem{
		ID: "22de7a2c-a671-4e7b-b6db-b32d03490a08", Name: "socloud-cheap"}}
}

func runClusterRenameCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, "", []*cobra.Command{clusterRenameCmd}, append([]string{"cluster", "rename"}, arguments...)...)
}

// The ticket's own rename, by name: one write to the cluster's id, and the
// output says what changed and what the operator still has to do.
func TestClusterRenameRenamesTheClusterByName(t *testing.T) {
	mock := newClusterRenameMock()

	output, runError := runClusterRenameCommand(t, mock, "socloud-cheap", "socloud-dev")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := clusterRenameCall{clusterID: mock.cluster.ID, newName: "socloud-dev"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want one %+v", mock.calls, want)
	}
	for _, line := range []string{
		"Cluster 'socloud-cheap' renamed to 'socloud-dev'.",
		"GitOps files stay in clusters/socloud-cheap-qa234i7ojs.",
		"Update metadata.name to 'socloud-dev'",
		"'ankra cluster kubeconfig add socloud-dev'",
	} {
		if !strings.Contains(output, line) {
			t.Errorf("output missing %q:\n%s", line, output)
		}
	}
}

// The cluster may be named by id, and -o json carries the platform's answer.
func TestClusterRenameAcceptsAClusterIDAndRendersJSON(t *testing.T) {
	mock := newClusterRenameMock()

	output, runError := runClusterRenameCommand(t, mock, mock.cluster.ID, "socloud-dev", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded clusterRenameOutput
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	if decoded.ClusterID != mock.cluster.ID || decoded.Name != "socloud-dev" || decoded.PreviousName != "socloud-cheap" ||
		!decoded.Changed {
		t.Fatalf("json = %+v", decoded)
	}
}

// A name the platform would refuse is a usage error, and nothing is sent.
func TestClusterRenameRefusesABadNameBeforeWriting(t *testing.T) {
	for _, name := range []string{"Socloud-Dev", "socloud_dev", "-socloud", "socloud-", "1socloud",
		strings.Repeat("a", 54)} {
		t.Run(name, func(subtest *testing.T) {
			mock := newClusterRenameMock()
			_, runError := runClusterRenameCommand(subtest, mock, "socloud-cheap", name)
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

	_, runError := runClusterRenameCommand(t, mock, "socloud-cheap", "socloud-dev")
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("expected the platform's refusal, got %v", runError)
	}
}

// A cluster the organisation does not have is not-found (exit 3).
func TestClusterRenameUnknownClusterIsNotFound(t *testing.T) {
	mock := newClusterRenameMock()

	_, runError := runClusterRenameCommand(t, mock, "no-such-cluster", "socloud-dev")
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
	selected := client.ClusterListItem{ID: "22de7a2c-a671-4e7b-b6db-b32d03490a08", Name: "socloud-cheap"}
	if saveError := saveSelectedCluster(selected); saveError != nil {
		t.Fatalf("saving the selection: %v", saveError)
	}

	if note, warning := followRenameInSelection("another-cluster-id", "socloud-dev"); note != "" || warning != "" {
		t.Fatalf("another cluster's rename touched the selection: %q %q", note, warning)
	}
	if reloaded, _ := loadSelectedCluster(); reloaded.Name != "socloud-cheap" {
		t.Fatalf("selection = %q, want unchanged", reloaded.Name)
	}

	if note, warning := followRenameInSelection(selected.ID, "socloud-dev"); !strings.Contains(note, "socloud-dev") || warning != "" {
		t.Fatalf("note = %q, warning = %q; want a note naming the new name", note, warning)
	}
	reloaded, loadError := loadSelectedCluster()
	if loadError != nil || reloaded.ID != selected.ID || reloaded.Name != "socloud-dev" {
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
	selected := client.ClusterListItem{ID: "22de7a2c-a671-4e7b-b6db-b32d03490a08", Name: "socloud-cheap"}
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

	note, warning := followRenameInSelection(selected.ID, "socloud-dev")
	if note != "" || !strings.Contains(warning, "ankra cluster select socloud-dev") {
		t.Fatalf("note = %q, warning = %q; want a warning naming the fix", note, warning)
	}
}

// --cluster stands in for the cluster argument.
func TestClusterRenameTakesTheClusterFromTheFlag(t *testing.T) {
	mock := newClusterRenameMock()

	if _, runError := runClusterRenameCommand(t, mock, "--cluster", "socloud-cheap", "socloud-dev"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := clusterRenameCall{clusterID: mock.cluster.ID, newName: "socloud-dev"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want one %+v", mock.calls, want)
	}
}
