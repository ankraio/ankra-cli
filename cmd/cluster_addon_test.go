package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

type clusterAddonUninstallMock struct {
	baseMock
	addons []client.ClusterAddonListItem
	// resourceIDs maps addon name -> resource id, mirroring the stack-history
	// resolution the real client performs (the listing carries no id).
	resourceIDs   map[string]string
	uninstalls    []uninstallCall
	uninstallErr  error
	getClusterErr error
}

type uninstallCall struct {
	ClusterID       string
	AddonResourceID string
	DeletePermanent bool
}

func (m *clusterAddonUninstallMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.getClusterErr != nil {
		return client.ClusterListItem{}, m.getClusterErr
	}
	return client.ClusterListItem{ID: "11111111-2222-3333-4444-555555555555", Name: name}, nil
}

func (m *clusterAddonUninstallMock) ListClusterAddons(clusterID string) ([]client.ClusterAddonListItem, error) {
	return m.addons, nil
}

// GetAddonByName mirrors the real client: it returns ErrAddonNotFound (wrapped)
// when the addon is absent so the handler can classify not-found.
func (m *clusterAddonUninstallMock) GetAddonByName(clusterID, addonName string) (*client.ClusterAddonListItem, error) {
	for i := range m.addons {
		if m.addons[i].Name == addonName {
			return &m.addons[i], nil
		}
	}
	return nil, fmt.Errorf("addon %q: %w", addonName, client.ErrAddonNotFound)
}

// GetStackAddonResourceID mirrors the real client: the resource id is
// resolved through the stack history, not the addon listing.
func (m *clusterAddonUninstallMock) GetStackAddonResourceID(clusterID, stackName, addonName string) (string, error) {
	if id, ok := m.resourceIDs[addonName]; ok {
		return id, nil
	}
	return "", fmt.Errorf("addon %q: %w", addonName, client.ErrAddonNotFound)
}

func (m *clusterAddonUninstallMock) UninstallAddon(ctx context.Context, clusterID, addonResourceID string, deletePermanently bool) (*client.UninstallAddonResult, error) {
	m.uninstalls = append(m.uninstalls, uninstallCall{
		ClusterID:       clusterID,
		AddonResourceID: addonResourceID,
		DeletePermanent: deletePermanently,
	})
	if m.uninstallErr != nil {
		return nil, m.uninstallErr
	}
	return &client.UninstallAddonResult{Success: true, Message: "Addon uninstalled"}, nil
}

func addonFixture(name string) client.ClusterAddonListItem {
	stackName := "core"
	return client.ClusterAddonListItem{Name: name, StackName: &stackName}
}

// executeAddonCommand runs the given args against rootCmd with stdin wired to
// stdinContent (so the [y/N] prompt can be answered), returning the error.
// It resets the uninstall command's flag state first: rootCmd is a process
// global, so --delete/--yes from a prior test would otherwise leak in.
func executeAddonCommand(t *testing.T, stdinContent string, args ...string) error {
	t.Helper()
	resetAddonUninstallFlags()
	// rootCmd is a process global; reset the mutated flag state on the way out
	// too so a leaked --cluster override does not bleed into unrelated tests.
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetAddonUninstallFlags()
	})
	rootCmd.SetOut(new(strings.Builder))
	rootCmd.SetErr(new(strings.Builder))
	rootCmd.SetIn(strings.NewReader(stdinContent))
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func resetAddonUninstallFlags() {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	reset(clusterAddonsUninstallCmd.Flags())
	reset(clusterCmd.PersistentFlags())
}

func TestClusterAddonUninstall_MissingAddonExitsNotFound(t *testing.T) {
	mock := &clusterAddonUninstallMock{
		addons:      []client.ClusterAddonListItem{addonFixture("present")},
		resourceIDs: map[string]string{"present": "addon-1"},
	}
	setMockClient(t, mock)

	err := executeAddonCommand(t, "y\n", "cluster", "addons", "uninstall", "absent", "--cluster", "my-cluster")
	if err == nil {
		t.Fatal("expected an error for a missing addon, got nil")
	}
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("exitCodeFor(%v) = %d, want %d (exitNotFound)", err, got, exitNotFound)
	}
	if !errors.Is(err, client.ErrAddonNotFound) {
		t.Errorf("expected error to wrap client.ErrAddonNotFound, got: %v", err)
	}
	if len(mock.uninstalls) != 0 {
		t.Errorf("UninstallAddon must not be called for a missing addon, got %d calls", len(mock.uninstalls))
	}
}

func TestClusterAddonUninstall_DeclinedPromptCancels(t *testing.T) {
	mock := &clusterAddonUninstallMock{
		addons:      []client.ClusterAddonListItem{addonFixture("present")},
		resourceIDs: map[string]string{"present": "addon-1"},
	}
	setMockClient(t, mock)

	err := executeAddonCommand(t, "n\n", "cluster", "addons", "uninstall", "present", "--cluster", "my-cluster")
	if err == nil {
		t.Fatal("expected declined prompt to return an error, got nil")
	}
	if !errors.Is(err, errCancelled) {
		t.Errorf("expected errCancelled, got: %v", err)
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("exitCodeFor(%v) = %d, want %d (exitCancelled)", err, got, exitCancelled)
	}
	if len(mock.uninstalls) != 0 {
		t.Errorf("UninstallAddon must not be called after a declined prompt, got %d calls", len(mock.uninstalls))
	}
}

func TestClusterAddonUninstall_ConfirmProceeds(t *testing.T) {
	mock := &clusterAddonUninstallMock{
		addons:      []client.ClusterAddonListItem{addonFixture("present")},
		resourceIDs: map[string]string{"present": "addon-1"},
	}
	setMockClient(t, mock)

	err := executeAddonCommand(t, "y\n", "cluster", "addons", "uninstall", "present", "--cluster", "my-cluster")
	if err != nil {
		t.Fatalf("expected confirmed uninstall to succeed, got: %v", err)
	}
	if len(mock.uninstalls) != 1 {
		t.Fatalf("expected exactly one UninstallAddon call, got %d", len(mock.uninstalls))
	}
	if mock.uninstalls[0].AddonResourceID != "addon-1" {
		t.Errorf("uninstalled addon ID = %q, want %q", mock.uninstalls[0].AddonResourceID, "addon-1")
	}
	if mock.uninstalls[0].DeletePermanent {
		t.Error("delete-permanently should be false without --delete")
	}
}

func TestClusterAddonUninstall_YesSkipsPromptAndProceeds(t *testing.T) {
	mock := &clusterAddonUninstallMock{
		addons:      []client.ClusterAddonListItem{addonFixture("present")},
		resourceIDs: map[string]string{"present": "addon-1"},
	}
	setMockClient(t, mock)

	// Empty stdin: if --yes did not skip the prompt, confirmPrompt would read
	// EOF and cancel, so a successful uninstall proves the prompt was skipped.
	err := executeAddonCommand(t, "", "cluster", "addons", "uninstall", "present", "--cluster", "my-cluster", "--yes", "--delete")
	if err != nil {
		t.Fatalf("expected --yes uninstall to succeed, got: %v", err)
	}
	if len(mock.uninstalls) != 1 {
		t.Fatalf("expected exactly one UninstallAddon call, got %d", len(mock.uninstalls))
	}
	if !mock.uninstalls[0].DeletePermanent {
		t.Error("delete-permanently should be true with --delete")
	}
}

func resetClusterAddonsListOutput(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = clusterAddonsListCmd.Flags().Set("output", "")
		clusterAddonsListCmd.Flags().Lookup("output").Changed = false
	})
}

// TestClusterAddonsListMarksAStaleAdvisoryFeed (ankra-0bm15): when the
// platform says the advisory feed behind the listing is stale, a clean
// answer must not read as a plain "ok" - the column says so and a footer
// names when the feed was last read. -o json keeps its array shape and
// says it on stderr.
func TestClusterAddonsListMarksAStaleAdvisoryFeed(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetClusterAddonsListOutput(t)
	readAt := time.Now().Add(-50 * time.Hour)
	mock := &clusterAddonsListMock{
		addons: []client.ClusterAddonListItem{{
			Name: "traefik", ChartName: "traefik", ChartVersion: "39.0.7", Namespace: "traefik", ThroughAnkra: true,
			SecurityAdvisoryStatus: client.SecurityAdvisoryStatusChecked,
		}},
		advisoryStale:  true,
		advisoryReadAt: &readAt,
	}
	setMockClient(t, mock)

	listing := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "addons", "list"); err != nil {
			t.Errorf("list: %v", err)
		}
	})
	for _, expected := range []string{"ok (stale)", "Security advisory data is stale (last read 2 days ago)",
		`"ok" means no advisory was known when it was read`} {
		if !strings.Contains(listing, expected) {
			t.Errorf("listing is missing %q:\n%s", expected, listing)
		}
	}

	details := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "addons", "list", "traefik")
	})
	if !strings.Contains(details, "Advisory data:   stale (last read 2 days ago)") {
		t.Errorf("details do not say the feed is stale:\n%s", details)
	}

	// executeCommand sends the command's stdout and stderr to one buffer:
	// the JSON array comes first and must decode on its own, the note
	// follows it (on stderr in a real run).
	var combined string
	_ = captureStdout(t, func() {
		combined, _ = executeCommand("cluster", "addons", "list", "-o", "json")
	})
	noteAt := strings.Index(combined, "Security advisory data is stale")
	if noteAt < 0 {
		t.Fatalf("the stale note is missing beside the JSON: %s", combined)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(combined[:noteAt]), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("the JSON before the note must be the addon array (%v): %s", err, combined)
	}
}

// TestClusterAddonsListFreshFeedReadsOk: a fresh feed, or a platform that
// reports none, keeps the plain "ok" and prints no footer.
func TestClusterAddonsListFreshFeedReadsOk(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &clusterAddonsListMock{
		addons: []client.ClusterAddonListItem{{
			Name: "traefik", ChartName: "traefik", ChartVersion: "39.0.7", Namespace: "traefik", ThroughAnkra: true,
			SecurityAdvisoryStatus: client.SecurityAdvisoryStatusChecked,
		}},
	}
	setMockClient(t, mock)
	listing := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "addons", "list")
	})
	if !strings.Contains(listing, " ok ") || strings.Contains(listing, "stale") {
		t.Errorf("a fresh feed must read plain ok with no footer:\n%s", listing)
	}
}

// TestClusterAddonsListNamedAddonOnAnEmptyCluster (ankra-0bm15): asking for
// one addon on a cluster with none is a not-found (exit 3) with nothing on
// stdout, not the human "No addons found" line printed into -o json.
func TestClusterAddonsListNamedAddonOnAnEmptyCluster(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetClusterAddonsListOutput(t)
	setMockClient(t, &clusterAddonsListMock{})
	for _, arguments := range [][]string{
		{"cluster", "addons", "list", "traefik", "-o", "json"},
		{"cluster", "addons", "list", "traefik"},
	} {
		var executeError error
		stdout := captureStdout(t, func() {
			_, executeError = executeCommand(arguments...)
		})
		if exitCodeFor(executeError) != exitNotFound || !strings.Contains(fmt.Sprint(executeError), `addon "traefik" not found`) {
			t.Errorf("%v: got %v (exit %d), want not-found exit 3", arguments, executeError, exitCodeFor(executeError))
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%v: stdout must be empty, got %q", arguments, stdout)
		}
	}
}

// TestClusterAddonsListEmptyClusterJSON: the whole listing of an empty
// cluster is an empty JSON array.
func TestClusterAddonsListEmptyClusterJSON(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetClusterAddonsListOutput(t)
	setMockClient(t, &clusterAddonsListMock{})
	var combined string
	_ = captureStdout(t, func() {
		combined, _ = executeCommand("cluster", "addons", "list", "-o", "json")
	})
	if strings.TrimSpace(combined) != "[]" {
		t.Errorf("want [], got %q", combined)
	}
}
