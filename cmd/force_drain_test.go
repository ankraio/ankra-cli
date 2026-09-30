package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// --force-drain is the CLI's way to act on the platform's refusal notice,
// which tells a person to re-run a scale-down or node-group delete with
// force_drain=true (ankra-r9nng). These tests pin that each command takes the
// flag, that it is off unless given, and that it reaches the client call.

// forceDrainNodeGroupMock records the drain options each node-group write was
// called with. The cluster is a Hetzner one; the kind dispatch itself is
// pinned by TestNodeGroupSelectorsForKind.
type forceDrainNodeGroupMock struct {
	baseMock
	cluster      client.ClusterListItem
	scaleDrains  []client.DrainOptions
	resizeDrains []client.DrainOptions
	deleteDrains []client.DrainOptions
}

func newForceDrainNodeGroupMock() *forceDrainNodeGroupMock {
	return &forceDrainNodeGroupMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "demo", Kind: "hetzner"}}
}

func (m *forceDrainNodeGroupMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *forceDrainNodeGroupMock) ScaleHetznerNodeGroup(ctx context.Context, clusterID, groupName string, count int, drainOptions client.DrainOptions, wait bool) (*client.ScaleNodeGroupResult, bool, error) {
	m.scaleDrains = append(m.scaleDrains, drainOptions)
	return &client.ScaleNodeGroupResult{GroupName: groupName, PreviousCount: 3, NewCount: count}, false, nil
}

func (m *forceDrainNodeGroupMock) UpdateHetznerNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, drainOptions client.DrainOptions, wait bool) (*client.UpdateNodeGroupResult, bool, error) {
	m.resizeDrains = append(m.resizeDrains, drainOptions)
	return &client.UpdateNodeGroupResult{GroupName: groupName, Updated: 3}, false, nil
}

func (m *forceDrainNodeGroupMock) DeleteHetznerNodeGroup(ctx context.Context, clusterID, groupName string, drainOptions client.DrainOptions, wait bool) (*client.DeleteNodeGroupResult, bool, error) {
	m.deleteDrains = append(m.deleteDrains, drainOptions)
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 3}, false, nil
}

func TestNodeGroupWritesPassForceDrainThrough(t *testing.T) {
	commands := []struct {
		name      string
		command   *cobra.Command
		arguments []string
		recorded  func(*forceDrainNodeGroupMock) []client.DrainOptions
	}{
		{
			name:      "scale",
			command:   clusterNodeGroupScaleCmd,
			arguments: []string{"cluster", "node-group", "scale", testClusterID, "workers", "1"},
			recorded:  func(mock *forceDrainNodeGroupMock) []client.DrainOptions { return mock.scaleDrains },
		},
		{
			name:      "upgrade",
			command:   clusterNodeGroupUpgradeCmd,
			arguments: []string{"cluster", "node-group", "upgrade", testClusterID, "workers", "cx43"},
			recorded:  func(mock *forceDrainNodeGroupMock) []client.DrainOptions { return mock.resizeDrains },
		},
		{
			name:      "delete",
			command:   clusterNodeGroupDeleteCmd,
			arguments: []string{"cluster", "node-group", "delete", testClusterID, "workers", "--yes"},
			recorded:  func(mock *forceDrainNodeGroupMock) []client.DrainOptions { return mock.deleteDrains },
		},
	}
	for _, command := range commands {
		for _, isForced := range []bool{false, true} {
			name := command.name + "/unset"
			arguments := command.arguments
			if isForced {
				name = command.name + "/force-drain"
				arguments = append(append([]string{}, arguments...), "--force-drain")
			}
			t.Run(name, func(t *testing.T) {
				mock := newForceDrainNodeGroupMock()
				var runError error
				captureStdout(t, func() {
					_, runError = runConfirmCommand(t, mock, "", []*cobra.Command{command.command}, arguments...)
				})
				if runError != nil {
					t.Fatalf("execute failed: %v", runError)
				}
				calls := command.recorded(mock)
				if len(calls) != 1 {
					t.Fatalf("calls = %d, want 1", len(calls))
				}
				if calls[0].ForceDrain != isForced {
					t.Errorf("ForceDrain = %v, want %v", calls[0].ForceDrain, isForced)
				}
			})
		}
	}
}

func TestClusterScalePassesForceDrainThrough(t *testing.T) {
	for _, isForced := range []bool{false, true} {
		arguments := []string{"cluster", "scale", testClusterID, "1"}
		if isForced {
			arguments = append(arguments, "--force-drain")
		}
		t.Run(strings.Join(arguments[3:], " "), func(t *testing.T) {
			mock := &clusterScaleMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "demo", Kind: "hetzner"}}
			var runError error
			captureStdout(t, func() {
				_, runError = runConfirmCommand(t, mock, "", []*cobra.Command{clusterScaleCmd}, arguments...)
			})
			if runError != nil {
				t.Fatalf("execute failed: %v", runError)
			}
			if len(mock.hetznerCalls) != 1 {
				t.Fatalf("hetzner calls = %d, want 1", len(mock.hetznerCalls))
			}
			if got := mock.hetznerCalls[0].DrainOptions.ForceDrain; got != isForced {
				t.Errorf("ForceDrain = %v, want %v", got, isForced)
			}
		})
	}
}

// TestNodeGroupDeleteForceDrainPromptSaysBudgetsAreBypassed pins that the
// confirmation a forced delete asks for names what the force gives up, and
// that declining it still sends nothing.
func TestNodeGroupDeleteForceDrainPromptSaysBudgetsAreBypassed(t *testing.T) {
	mock := newForceDrainNodeGroupMock()
	output, runError := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{clusterNodeGroupDeleteCmd},
		"cluster", "node-group", "delete", testClusterID, "workers", "--force-drain")
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", runError)
	}
	if !strings.Contains(output, "without honouring their PodDisruptionBudgets") {
		t.Errorf("prompt does not say the budgets are bypassed:\n%s", output)
	}
	if len(mock.deleteDrains) != 0 {
		t.Errorf("expected no delete call on decline, got %d", len(mock.deleteDrains))
	}
}

// TestForceDrainFlagIsOffByDefaultAndNamesPodDisruptionBudgets pins the flag
// on every command that takes it: default false, and help text that says what
// it bypasses rather than only that it forces something.
func TestForceDrainFlagIsOffByDefaultAndNamesPodDisruptionBudgets(t *testing.T) {
	for _, command := range []*cobra.Command{
		clusterScaleCmd,
		clusterNodeGroupScaleCmd,
		clusterNodeGroupUpgradeCmd,
		clusterNodeGroupDeleteCmd,
	} {
		flag := command.Flags().Lookup(forceDrainFlag)
		if flag == nil {
			t.Errorf("%s has no --%s flag", command.CommandPath(), forceDrainFlag)
			continue
		}
		if flag.DefValue != "false" {
			t.Errorf("%s --%s default = %s, want false", command.CommandPath(), forceDrainFlag, flag.DefValue)
		}
		if !strings.Contains(flag.Usage, "Bypass PodDisruptionBudgets") {
			t.Errorf("%s --%s usage does not say it bypasses PodDisruptionBudgets: %q", command.CommandPath(), forceDrainFlag, flag.Usage)
		}
		if !strings.Contains(command.Long, "--force-drain") {
			t.Errorf("%s long help does not explain --force-drain", command.CommandPath())
		}
	}
}
