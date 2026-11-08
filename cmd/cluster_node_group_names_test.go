package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	for groupName, wantInError := range groupNamesNoGroupCanHave {
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

// groupNamesNoGroupCanHave maps each name the node-group commands refuse to
// what the refusal says.
var groupNamesNoGroupCanHave = map[string]string{
	"":          "node group name is blank",
	"   ":       "node group name is blank",
	" workers":  "has spaces around it",
	"workers\t": "has spaces around it",
	".":         "is a path step, not a name",
	"..":        "is a path step, not a name",
	"%2e%2e":    `decodes to "..", a path step`,
	"%2E.":      `decodes to "..", a path step`,
	"a/b":       "contains '/'",
	"a%2Fb":     "contains '/'",
}

// A real group name passes the check: the command goes on to look up the
// cluster (where the mock's unimplemented write stops it). "..." and "a..b"
// are not path steps: the edge leaves them alone, so the check does too.
func TestNodeGroupCommandsAcceptAnOrdinaryGroupName(t *testing.T) {
	for _, groupName := range []string{"gpu-workers", "...", "a..b"} {
		for command, args := range nodeGroupCommandsNaming(groupName) {
			mock := &nodeGroupNameGuardMock{}
			_, _ = runConfirmCommand(t, mock, "n\n", nodeGroupNameResets, args...)
			if mock.lookups == 0 {
				t.Errorf("%s %q: an ordinary group name was refused before the cluster lookup", command, groupName)
			}
		}
	}
}

var providerNodeGroupResets = []*cobra.Command{
	nodeGroupAddCmd, nodeGroupScaleCmd, nodeGroupUpgradeCmd, nodeGroupDeleteCmd,
	ovhNodeGroupAddCmd, ovhNodeGroupScaleCmd, ovhNodeGroupUpgradeCmd, ovhNodeGroupDeleteCmd,
	ovhNodeGroupLabelsCmd, ovhNodeGroupTaintsCmd,
	upcloudNodeGroupAddCmd, upcloudNodeGroupScaleCmd, upcloudNodeGroupUpgradeCmd, upcloudNodeGroupDeleteCmd,
	digitaloceanNodeGroupAddCmd, digitaloceanNodeGroupScaleCmd, digitaloceanNodeGroupUpgradeCmd,
	digitaloceanNodeGroupDeleteCmd,
}

// The provider node-group commands (deprecated forwards of `cluster
// node-group`) check the name the same way, before any prompt: `hetzner
// node-group add --name ..` would otherwise create a group no later command
// can address, since the name travels in the body where the request check
// does not look.
func TestProviderNodeGroupCommandsRefuseAGroupNameNoGroupCanHave(t *testing.T) {
	for _, provider := range []string{"hetzner", "ovh", "upcloud", "digitalocean"} {
		lines := map[string][]string{
			"add":     {"cluster", provider, "node-group", "add", testClusterID, "--name", "..", "--instance-type", "x"},
			"scale":   {"cluster", provider, "node-group", "scale", testClusterID, "..", "2"},
			"upgrade": {"cluster", provider, "node-group", "upgrade", testClusterID, "..", "x"},
			"delete":  {"cluster", provider, "node-group", "delete", testClusterID, ".."},
		}
		if provider == "ovh" {
			lines["labels"] = []string{"cluster", "ovh", "node-group", "labels", testClusterID, "..", "--clear"}
			lines["taints"] = []string{"cluster", "ovh", "node-group", "taints", testClusterID, "..", "--clear"}
		}
		for command, args := range lines {
			mock := &nodeGroupNameGuardMock{}
			output, err := runConfirmCommand(t, mock, "y\n", providerNodeGroupResets, args...)
			if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "is a path step") {
				t.Errorf("%s %s: got %v (exit %d), want the usage error for a path step", provider, command, err,
					exitCodeFor(err))
			}
			if mock.lookups != 0 || strings.Contains(output, "[y/N]") {
				t.Errorf("%s %s: the name was checked too late (lookups=%d, output %q)", provider, command,
					mock.lookups, output)
			}
		}
	}
}

// An -o value the command cannot write is refused with the arguments, before
// the cluster is looked up, a prompt is shown or a write is sent: a write
// whose answer cannot be printed must not be sent at all. Every node-group
// command that names a group shares the check, the provider ones too.
func TestNodeGroupCommandsRefuseAnUnknownOutputFormatBeforeAnything(t *testing.T) {
	lines := nodeGroupCommandsNaming("workers")
	for _, provider := range []string{"hetzner", "ovh", "upcloud", "digitalocean"} {
		lines[provider+" add"] = []string{"cluster", provider, "node-group", "add", testClusterID, "--name", "workers",
			"--instance-type", "x"}
		lines[provider+" scale"] = []string{"cluster", provider, "node-group", "scale", testClusterID, "workers", "2"}
		lines[provider+" delete"] = []string{"cluster", provider, "node-group", "delete", testClusterID, "workers"}
	}
	resets := append(append([]*cobra.Command{}, nodeGroupNameResets...), providerNodeGroupResets...)
	for command, args := range lines {
		mock := &nodeGroupNameGuardMock{}
		output, err := runConfirmCommand(t, mock, "y\n", resets, append(args, "-o", "xml")...)
		if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), `invalid -o format "xml"`) {
			t.Errorf("%s -o xml: got %v (exit %d), want the usage error", command, err, exitCodeFor(err))
		}
		if mock.lookups != 0 || strings.Contains(output, "[y/N]") {
			t.Errorf("%s -o xml: checked too late (lookups=%d, output %q)", command, mock.lookups, output)
		}
	}
}

// A request the client refused to send is not a dropped connection: no retry
// loop tries it again, so it ends as the usage error it is.
func TestARefusedRequestIsNeverRetried(t *testing.T) {
	refused := fmt.Errorf("reading the run: %w", &url.Error{Op: "Get", URL: "https://platform.example/x/..",
		Err: &client.UnsafeRequestPathError{Method: "GET", Path: "/x/..", Segment: ".."}})
	if isTransientWorkspaceError(refused) {
		t.Error("isTransientWorkspaceError retries a refused request")
	}
	if _, retry := pipelineLogStreamOpenRetryReason(refused); retry {
		t.Error("pipelineLogStreamOpenRetryReason retries a refused request")
	}
	if pipelineRunReadIsWorthRetrying(&pipelineRunWait{parentContext: context.Background()}, refused) {
		t.Error("pipelineRunReadIsWorthRetrying retries a refused request")
	}
	if retryableTailError(refused) {
		t.Error("retryableTailError retries a refused request")
	}
	if exitCodeFor(refused) != exitUsage {
		t.Errorf("a refused request exits %d, want %d", exitCodeFor(refused), exitUsage)
	}
	dropped := &url.Error{Op: "Get", URL: "https://platform.example/x", Err: errors.New("connection reset")}
	if !isTransientWorkspaceError(dropped) {
		t.Error("a dropped connection must still be retried")
	}
	if _, retry := pipelineLogStreamOpenRetryReason(dropped); !retry {
		t.Error("a dropped log stream must still be retried")
	}
}
