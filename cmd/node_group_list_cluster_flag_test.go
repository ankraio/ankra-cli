package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// otherClusterID is a second id-shaped cluster, so a test can tell the
// cluster --cluster named from the one the argument or the selection named.
const otherClusterID = "9d3e2c1b-0a4f-4e5d-8c7b-6a5f4e3d2c1b"

// nodeGroupListClusterMock records which cluster each provider's node-group
// list was asked for.
type nodeGroupListClusterMock struct {
	baseMock
	listed []string
}

func (m *nodeGroupListClusterMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"}, nil
}

func (m *nodeGroupListClusterMock) record(clusterID string) (*client.NodeGroupListResult, error) {
	m.listed = append(m.listed, clusterID)
	return &client.NodeGroupListResult{}, nil
}

func (m *nodeGroupListClusterMock) ListHetznerNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.record(clusterID)
}

func (m *nodeGroupListClusterMock) ListOvhNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.record(clusterID)
}

func (m *nodeGroupListClusterMock) ListUpcloudNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.record(clusterID)
}

func (m *nodeGroupListClusterMock) ListDigitaloceanNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.record(clusterID)
}

var nodeGroupListCommands = []struct {
	name    string
	command *cobra.Command
	path    []string
}{
	{name: "cluster node-group list", command: clusterNodeGroupListCmd, path: []string{"cluster", "node-group", "list"}},
	{name: "cluster hetzner node-group list", command: nodeGroupListCmd, path: []string{"cluster", "hetzner", "node-group", "list"}},
	{name: "cluster ovh node-group list", command: ovhNodeGroupListCmd, path: []string{"cluster", "ovh", "node-group", "list"}},
	{name: "cluster upcloud node-group list", command: upcloudNodeGroupListCmd, path: []string{"cluster", "upcloud", "node-group", "list"}},
	{name: "cluster digitalocean node-group list", command: digitaloceanNodeGroupListCmd, path: []string{"cluster", "digitalocean", "node-group", "list"}},
}

// runNodeGroupList runs one node-group list with the selected cluster set to
// testClusterID, or to nothing when selected is false.
func runNodeGroupList(t *testing.T, command *cobra.Command, selected bool, args ...string) (*nodeGroupListClusterMock, error) {
	t.Helper()
	if selected {
		writeSelectedClusterJSON(t)
	} else {
		withTempHome(t)
	}
	resetTreeFlags(t, command)
	t.Cleanup(func() { resetTreeFlags(t, command) })
	mock := &nodeGroupListClusterMock{}
	setMockClient(t, mock)
	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand(args...)
	})
	return mock, runError
}

// node-group list demanded its <cluster> argument and never read --cluster,
// so `node-group list --cluster <id>` failed with "accepts 1 arg(s),
// received 0", and an argument beside --cluster silently won
// (ankra-v3qdto.6). The cluster is now the argument, else --cluster, else the
// selected cluster, on the provider-detecting list and on the four older
// per-provider ones.
func TestNodeGroupListHonoursTheClusterFlag(t *testing.T) {
	for _, testCase := range nodeGroupListCommands {
		t.Run(testCase.name, func(t *testing.T) {
			mock, runError := runNodeGroupList(t, testCase.command, true,
				append(append([]string{}, testCase.path...), "--cluster", otherClusterID)...)
			if runError != nil {
				t.Fatalf("list --cluster: %v", runError)
			}
			if len(mock.listed) != 1 || mock.listed[0] != otherClusterID {
				t.Fatalf("listed %v, want the --cluster cluster %s, not the selected one", mock.listed, otherClusterID)
			}
		})
	}
}

func TestNodeGroupListFallsBackToTheSelectedCluster(t *testing.T) {
	for _, testCase := range nodeGroupListCommands {
		t.Run(testCase.name, func(t *testing.T) {
			mock, runError := runNodeGroupList(t, testCase.command, true, testCase.path...)
			if runError != nil {
				t.Fatalf("list: %v", runError)
			}
			if len(mock.listed) != 1 || mock.listed[0] != testClusterID {
				t.Fatalf("listed %v, want the selected cluster %s", mock.listed, testClusterID)
			}
		})
	}
}

func TestNodeGroupListArgumentStillWins(t *testing.T) {
	for _, testCase := range nodeGroupListCommands {
		t.Run(testCase.name, func(t *testing.T) {
			mock, runError := runNodeGroupList(t, testCase.command, true,
				append(append([]string{}, testCase.path...), otherClusterID)...)
			if runError != nil {
				t.Fatalf("list <cluster>: %v", runError)
			}
			if len(mock.listed) != 1 || mock.listed[0] != otherClusterID {
				t.Fatalf("listed %v, want the argument's cluster %s", mock.listed, otherClusterID)
			}
		})
	}
}

// Naming two different clusters is refused rather than one of them being
// quietly ignored, and naming none with nothing selected says how to name
// one; neither lists anything.
func TestNodeGroupListRefusesAmbiguousOrMissingCluster(t *testing.T) {
	for _, testCase := range nodeGroupListCommands {
		t.Run(testCase.name+" both", func(t *testing.T) {
			mock, runError := runNodeGroupList(t, testCase.command, true,
				append(append([]string{}, testCase.path...), testClusterID, "--cluster", otherClusterID)...)
			if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "name different clusters") {
				t.Fatalf("error = %v, want the different-clusters usage error", runError)
			}
			if len(mock.listed) != 0 {
				t.Fatalf("listed %v, want nothing", mock.listed)
			}
		})
		t.Run(testCase.name+" none", func(t *testing.T) {
			mock, runError := runNodeGroupList(t, testCase.command, false, testCase.path...)
			if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--cluster") {
				t.Fatalf("error = %v, want the no-cluster usage error naming --cluster", runError)
			}
			if len(mock.listed) != 0 {
				t.Fatalf("listed %v, want nothing", mock.listed)
			}
		})
	}
}

// The five lists go through withClusterArgument as optional-cluster
// commands that may use the selection: --cluster alone reaches the resolver,
// and a different --cluster is refused with the same words the resolver
// uses.
func TestNodeGroupListUnderTheClusterArgumentResolver(t *testing.T) {
	for _, testCase := range nodeGroupListCommands {
		t.Run(testCase.name, func(t *testing.T) {
			if kind := clusterPositionalKind(testCase.command); kind != "[" {
				t.Fatalf("Use %q: cluster positional kind %q, want an optional one", testCase.command.Use, kind)
			}
			if access := testCase.command.Annotations[clusterArgumentAnnotation]; access != string(clusterSelectionAllowed) {
				t.Fatalf("registered as %q, want %q", access, clusterSelectionAllowed)
			}
			mock, runError := runNodeGroupList(t, testCase.command, true,
				append(append([]string{}, testCase.path...), "--cluster", otherClusterID)...)
			if runError != nil {
				t.Fatalf("list --cluster: %v", runError)
			}
			if len(mock.listed) != 1 || mock.listed[0] != otherClusterID {
				t.Fatalf("listed %v, want the --cluster cluster %s", mock.listed, otherClusterID)
			}

			mock, runError = runNodeGroupList(t, testCase.command, true,
				append(append([]string{}, testCase.path...), testClusterID, "--cluster", otherClusterID)...)
			if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "name different clusters") {
				t.Fatalf("error = %v, want the different-clusters usage error", runError)
			}
			if len(mock.listed) != 0 {
				t.Fatalf("listed %v, want nothing", mock.listed)
			}
		})
	}
}

// wrongKindListMock is an OVH cluster that the Hetzner node-group route
// refuses, the way the platform answers a kind mismatch.
type wrongKindListMock struct {
	baseMock
}

func (m *wrongKindListMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Name: "ovh-prod", Kind: "ovh"}, nil
}

func (m *wrongKindListMock) ListHetznerNodeGroups(string) (*client.NodeGroupListResult, error) {
	return nil, client.NewUnexpectedResponseError(409, "cluster is not a hetzner cluster")
}

// A cluster named with --cluster gets the same wrong-kind refusal (exit 2,
// naming the cluster's kind) as one named by the argument.
func TestProviderNodeGroupListWithClusterFlagKeepsTheKindRefusal(t *testing.T) {
	wrapProviderKindRefusals(rootCmd)
	withTempHome(t)
	resetTreeFlags(t, nodeGroupListCmd)
	t.Cleanup(func() { resetTreeFlags(t, nodeGroupListCmd) })
	setMockClient(t, &wrongKindListMock{})
	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand("cluster", "hetzner", "node-group", "list", "--cluster", otherClusterID)
	})
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "its kind is ovh") {
		t.Fatalf("error = %v, want the wrong-kind usage error", runError)
	}
}
