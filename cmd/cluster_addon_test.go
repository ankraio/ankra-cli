package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

type clusterAddonUninstallMock struct {
	baseMock
	addons        []client.ClusterAddonListItem
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

func addonFixture(name, id string) client.ClusterAddonListItem {
	return client.ClusterAddonListItem{ID: id, Name: name}
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
		addons: []client.ClusterAddonListItem{addonFixture("present", "addon-1")},
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
		addons: []client.ClusterAddonListItem{addonFixture("present", "addon-1")},
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
		addons: []client.ClusterAddonListItem{addonFixture("present", "addon-1")},
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
		addons: []client.ClusterAddonListItem{addonFixture("present", "addon-1")},
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
