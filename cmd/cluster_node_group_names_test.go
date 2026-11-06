package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// nodeGroupNameGuardMock answers the cluster lookup every node-group command
// starts with, so a name check that ran too late shows up as a lookup.
type nodeGroupNameGuardMock struct {
	baseMock
	lookups int
}

func (m *nodeGroupNameGuardMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	m.lookups++
	return client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"}, nil
}

var nodeGroupNameResets = []*cobra.Command{
	clusterNodeGroupAddCmd, clusterNodeGroupScaleCmd, clusterNodeGroupUpgradeCmd, clusterNodeGroupDeleteCmd,
	clusterNodeGroupAutoscalingGetCmd, clusterNodeGroupAutoscalingSetCmd, clusterNodeGroupLabelsCmd,
	clusterNodeGroupTaintsCmd,
}

// nodeGroupCommandsNaming builds every node-group command line that names a
// group, around the given name.
func nodeGroupCommandsNaming(groupName string) map[string][]string {
	return map[string][]string{
		"add":             {"cluster", "node-group", "add", testClusterID, "--name", groupName, "--instance-type", "cx22"},
		"scale":           {"cluster", "node-group", "scale", testClusterID, groupName, "2"},
		"upgrade":         {"cluster", "node-group", "upgrade", testClusterID, groupName, "cx32"},
		"delete":          {"cluster", "node-group", "delete", testClusterID, groupName},
		"autoscaling get": {"cluster", "node-group", "autoscaling", "get", testClusterID, groupName},
		"autoscaling set": {"cluster", "node-group", "autoscaling", "set", testClusterID, groupName, "--min", "1", "--max", "2"},
		"labels":          {"cluster", "node-group", "labels", testClusterID, groupName, "--labels", "tier=db"},
		"taints":          {"cluster", "node-group", "taints", testClusterID, groupName, "--clear"},
	}
}

// ankra-6pi023: a group name is pasted into the request path, and `..` there
// reached the cluster's own DELETE. Every node-group command refuses a name
// no group can have as a usage error, before the cluster is looked up or a
// prompt is shown, and says which argument is wrong.
func TestNodeGroupCommandsRefuseAGroupNameNoGroupCanHave(t *testing.T) {
	for groupName, wantInError := range map[string]string{
		"":       "node group name is blank",
		"   ":    "node group name is blank",
		".":      "made only of dots",
		"..":     "made only of dots",
		"...":    "made only of dots",
		"%2e%2e": `decodes to ".."`,
		"%2E.":   `decodes to ".."`,
		"a/b":    "contains '/'",
		"a%2Fb":  "contains '/'",
	} {
		for command, args := range nodeGroupCommandsNaming(groupName) {
			mock := &nodeGroupNameGuardMock{}
			output, err := runConfirmCommand(t, mock, "y\n", nodeGroupNameResets, args...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Errorf("%s %q: got %v (exit %d), want a usage error (exit %d)", command, groupName, err,
					exitCodeFor(err), exitUsage)
				continue
			}
			if !strings.Contains(err.Error(), wantInError) {
				t.Errorf("%s %q: error %q does not say %q", command, groupName, err.Error(), wantInError)
			}
			if mock.lookups != 0 || strings.Contains(output, "[y/N]") {
				t.Errorf("%s %q: the name was checked too late (lookups=%d, output %q)", command, groupName,
					mock.lookups, output)
			}
		}
	}
}

// A real group name passes the check: the command goes on to look up the
// cluster (where the mock's unimplemented write stops it).
func TestNodeGroupCommandsAcceptAnOrdinaryGroupName(t *testing.T) {
	for command, args := range nodeGroupCommandsNaming("gpu-workers") {
		mock := &nodeGroupNameGuardMock{}
		_, _ = runConfirmCommand(t, mock, "n\n", nodeGroupNameResets, args...)
		if mock.lookups == 0 {
			t.Errorf("%s: an ordinary group name was refused before the cluster lookup", command)
		}
	}
}
