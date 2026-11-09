package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// clusterFlagCommandMock is a Hetzner cluster named "demo" (testClusterID)
// among others, and records which cluster each command acted on.
type clusterFlagCommandMock struct {
	baseMock
	kind       string
	scaleError error
	calls      []string
}

func (m *clusterFlagCommandMock) record(call string) {
	m.calls = append(m.calls, call)
}

func (m *clusterFlagCommandMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     []client.ClusterListItem{{ID: testClusterID, Name: "demo"}, {ID: otherClusterID, Name: "other"}},
		Pagination: client.Pagination{TotalPages: 1, Page: page, PageSize: pageSize},
	}, nil
}

func (m *clusterFlagCommandMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	kind := m.kind
	if kind == "" {
		kind = "hetzner"
	}
	return client.ClusterListItem{ID: clusterID, Name: "demo", Kind: kind}, nil
}

func (m *clusterFlagCommandMock) GetHetznerClusterSSHKeys(clusterID string) (*client.ClusterSSHKeysResult, error) {
	m.record("ssh-keys get " + clusterID)
	return &client.ClusterSSHKeysResult{}, nil
}

func (m *clusterFlagCommandMock) UpdateHetznerClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*client.UpdateClusterSSHKeysResult, error) {
	m.record("ssh-keys set " + clusterID + " " + strings.Join(sshKeyCredentialIDs, ","))
	return &client.UpdateClusterSSHKeysResult{SSHKeyCredentialIDs: sshKeyCredentialIDs}, nil
}

func (m *clusterFlagCommandMock) ResyncHetznerClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.record("ssh-keys resync " + clusterID)
	return &client.ResyncSSHKeysResult{}, nil
}

func (m *clusterFlagCommandMock) GetHetznerWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	m.record("workers " + clusterID)
	return &client.WorkerCountResult{WorkerCount: 3, Min: 1, Max: 5}, nil
}

func (m *clusterFlagCommandMock) ScaleHetznerNodeGroup(_ context.Context, clusterID, groupName string, count int, _ client.DrainOptions, _ bool) (*client.ScaleNodeGroupResult, bool, error) {
	m.record(fmt.Sprintf("node-group scale %s %s %d", clusterID, groupName, count))
	if m.scaleError != nil {
		return nil, false, m.scaleError
	}
	return &client.ScaleNodeGroupResult{GroupName: groupName, PreviousCount: 1, NewCount: count}, false, nil
}

func (m *clusterFlagCommandMock) LeaveClusterMesh(meshID string, clusterID string) error {
	m.record("mesh leave " + meshID + " " + clusterID)
	return nil
}

func (m *clusterFlagCommandMock) UpdateHetznerBastionAllowedIPs(_ context.Context, clusterID string, allowedIPs []string) (*client.UpdateBastionAllowedIPsResult, error) {
	m.record("allowed-ips " + clusterID + " " + strings.Join(allowedIPs, ","))
	return &client.UpdateBastionAllowedIPsResult{}, nil
}

// clusterTreeCommands is every command under `ankra cluster`, so a test can
// put all their flags back to their defaults.
func clusterTreeCommands() []*cobra.Command {
	var commands []*cobra.Command
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		commands = append(commands, command)
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(clusterCmd)
	return commands
}

// runClusterFlagCommand runs one command line with testClusterID selected
// (`ankra cluster select`) and nothing on stdin.
func runClusterFlagCommand(t *testing.T, mock APIClient, args ...string) error {
	t.Helper()
	withTempHome(t)
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	commands := clusterTreeCommands()
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	rootCmd.SetIn(strings.NewReader(""))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand(args...)
	})
	return runError
}

// `ankra cluster ssh-keys get --cluster <id>` failed with "accepts 1 arg(s),
// received 0" although every cluster command documents --cluster as its
// target (ankra-v3qdto.19). --cluster now stands in for the cluster argument,
// and a command with further arguments takes them without it.
func TestClusterCommandsTakeTheClusterFromTheFlag(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{"cluster", "ssh-keys", "get", "--cluster", otherClusterID}, "ssh-keys get " + otherClusterID},
		{[]string{"cluster", "ssh-keys", "set", "--cluster", otherClusterID, "--ssh-key-credential-ids", "key-1"},
			"ssh-keys set " + otherClusterID + " key-1"},
		{[]string{"cluster", "ssh-keys", "resync", "--cluster", otherClusterID}, "ssh-keys resync " + otherClusterID},
		{[]string{"cluster", "hetzner", "workers", "--cluster", otherClusterID}, "workers " + otherClusterID},
		{[]string{"cluster", "node-group", "scale", "--cluster", otherClusterID, "pool-a", "3"},
			"node-group scale " + otherClusterID + " pool-a 3"},
		{[]string{"cluster", "hetzner", "node-group", "scale", "pool-a", "3", "--cluster", otherClusterID},
			"node-group scale " + otherClusterID + " pool-a 3"},
		{[]string{"cluster", "mesh", "leave", "mesh-1", "--cluster", otherClusterID}, "mesh leave mesh-1 " + otherClusterID},
		// An address where the cluster goes is the allowlist's first entry.
		{[]string{"cluster", "hetzner", "bastion", "allowed-ips", "--cluster", otherClusterID, "203.0.113.7", "198.51.100.0/24"},
			"allowed-ips " + otherClusterID + " 203.0.113.7,198.51.100.0/24"},
		{[]string{"cluster", "hetzner", "bastion", "allowed-ips", otherClusterID, "203.0.113.7", "--cluster", otherClusterID},
			"allowed-ips " + otherClusterID + " 203.0.113.7"},
		// The same cluster in both places, spelled the same way.
		{[]string{"cluster", "node-group", "scale", otherClusterID, "pool-a", "3", "--cluster", otherClusterID},
			"node-group scale " + otherClusterID + " pool-a 3"},
		{[]string{"cluster", "ssh-keys", "get", otherClusterID, "--cluster", otherClusterID}, "ssh-keys get " + otherClusterID},
	} {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			mock := &clusterFlagCommandMock{}
			if runError := runClusterFlagCommand(t, mock, testCase.args...); runError != nil {
				t.Fatalf("run: %v", runError)
			}
			if len(mock.calls) != 1 || mock.calls[0] != testCase.want {
				t.Fatalf("calls %v, want [%s]", mock.calls, testCase.want)
			}
		})
	}
}

// A command whose usage shows the cluster as required never acts on the
// cluster `ankra cluster select` saved, whether it reads or writes: the
// selection is one file per machine, shared by every shell and agent
// session on it, so a selection made elsewhere could aim a stop or a
// deprovision at the wrong cluster. With no cluster named it is a usage
// error naming --cluster, and nothing is looked up or sent.
func TestClusterCommandsRefuseTheSelectedCluster(t *testing.T) {
	for _, args := range [][]string{
		{"cluster", "ssh-keys", "get"},
		{"cluster", "hetzner", "workers"},
		{"cluster", "ssh-keys", "set", "--ssh-key-credential-ids", "key-1"},
		{"cluster", "ssh-keys", "resync"},
		{"cluster", "node-group", "scale", "pool-a", "3"},
		{"cluster", "hetzner", "node-group", "delete", "pool-a"},
		{"cluster", "hetzner", "stop"},
		{"cluster", "upgrade", "v1.33.1"},
		{"cluster", "mesh", "leave", "mesh-1"},
		// These three fell back to the selection before; deprovisioning,
		// moving and provisioning are the last things to do to a cluster
		// nobody named.
		{"cluster", "deprovision", "--yes"},
		{"cluster", "move", "--organisation", "acme-prod", "--yes"},
		{"cluster", "provision"},
		// The environment decides alert silencing and whether Ankra AI may
		// remediate on its own, so a relabel is never aimed at a selection
		// another session made either.
		{"cluster", "update", "--environment", "development"},
		// The domain read falls back to the selection; creating or tearing
		// down the zone does not.
		{"cluster", "domain", "--enable"},
		{"cluster", "domain", "--remove"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := &clusterFlagCommandMock{}
			runError := runClusterFlagCommand(t, mock, args...)
			if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--cluster") {
				t.Fatalf("error = %v (exit %d), want a usage error (exit 2) naming --cluster", runError, exitCodeFor(runError))
			}
			if len(mock.calls) != 0 {
				t.Fatalf("calls %v, want none", mock.calls)
			}
		})
	}
}

// The argument and --cluster naming two different clusters is refused
// rather than the argument silently winning, whether both are ids or one is
// a name. The comparison is of the text, so a name beside the same
// cluster's id is refused too (ankra-oznf7k.10): the safe side.
func TestClusterCommandsRefuseTwoDifferentClusters(t *testing.T) {
	for _, args := range [][]string{
		{"cluster", "ssh-keys", "get", testClusterID, "--cluster", otherClusterID},
		{"cluster", "ssh-keys", "get", "demo", "--cluster", otherClusterID},
		{"cluster", "ssh-keys", "get", "demo", "--cluster", testClusterID},
		{"cluster", "node-group", "scale", testClusterID, "pool-a", "3", "--cluster", otherClusterID},
		{"cluster", "mesh", "leave", "mesh-1", testClusterID, "--cluster", otherClusterID},
		// Commands ending in a list: the count cannot tell, the value where
		// the cluster goes does.
		{"cluster", "mesh", "up", "mesh-1", testClusterID, "--cluster", otherClusterID},
		{"cluster", "mesh", "readiness", testClusterID, otherClusterID, "--cluster", otherClusterID},
		{"cluster", "hetzner", "bastion", "allowed-ips", testClusterID, "203.0.113.7", "--cluster", otherClusterID},
		// Commands that read the argument and --cluster themselves.
		{"cluster", "select", testClusterID, "--cluster", otherClusterID},
		{"cluster", "update", "demo", "--environment", "staging", "--cluster", otherClusterID},
		{"cluster", "reconcile", "demo", "--cluster", otherClusterID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := &clusterFlagCommandMock{}
			runError := runClusterFlagCommand(t, mock, args...)
			if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "name different clusters") {
				t.Fatalf("error = %v (exit %d), want the different-clusters usage error (exit 2)", runError, exitCodeFor(runError))
			}
			if len(mock.calls) != 0 {
				t.Fatalf("calls %v, want none", mock.calls)
			}
		})
	}
}

// The same cluster as the argument and with --cluster is the argument as
// given, counted by the command's own check, as before --cluster could
// stand in (#744): `node-group delete prod --cluster prod` lacks the group.
// It does not delete a group called prod on the cluster prod.
func TestClusterArgumentRepeatedAsTheFlagKeepsTheCommandsCount(t *testing.T) {
	withTempHome(t)
	writeSelectedClusterJSON(t)
	for _, testCase := range []struct {
		path []string
		want string
	}{
		{[]string{"node-group", "delete"}, "accepts 2 arg(s), received 1"},
		{[]string{"hetzner", "node-group", "delete"}, "accepts 2 arg(s), received 1"},
		{[]string{"node-group", "scale"}, "accepts 3 arg(s), received 1"},
		{[]string{"hetzner", "node-group", "scale"}, "accepts 3 arg(s), received 1"},
		{[]string{"managed", "node-pool", "delete"}, "accepts 2 arg(s), received 1"},
		{[]string{"hetzner", "nodes", "restart"}, "accepts 2 arg(s), received 1"},
	} {
		t.Run(strings.Join(testCase.path, " "), func(t *testing.T) {
			command, _, findError := clusterCmd.Find(testCase.path)
			if findError != nil || command.Name() != testCase.path[len(testCase.path)-1] {
				t.Fatalf("finding %v: %v", testCase.path, findError)
			}
			err := censusArgs(t, command, "prod", []string{"prod"})
			if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), testCase.want) ||
				strings.Contains(err.Error(), "counting --cluster") {
				t.Fatalf("--cluster prod beside prod: got %v (exit %d), want the command's own count error %q",
					err, exitCodeFor(err), testCase.want)
			}
		})
	}
}

// A provider command pointed at a cluster of another kind with --cluster
// gets the same exit-2 refusal naming the kind as one pointed at it with the
// argument: the refusal names the cluster --cluster named, not the node
// group that is now the first argument.
func TestProviderCommandWithClusterFlagNamesTheClusterInTheKindRefusal(t *testing.T) {
	wrapProviderKindRefusals(rootCmd)
	mock := &clusterFlagCommandMock{kind: "ovh",
		scaleError: client.NewUnexpectedResponseError(409, "cluster is not a hetzner cluster")}
	runError := runClusterFlagCommand(t, mock,
		"cluster", "hetzner", "node-group", "scale", "--cluster", otherClusterID, "pool-a", "3")
	want := otherClusterID + " is not a Hetzner cluster (its kind is ovh)"
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), want) {
		t.Fatalf("error = %v (exit %d), want the wrong-kind usage error %q", runError, exitCodeFor(runError), want)
	}
}
