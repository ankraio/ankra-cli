package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// nodeGroupRecordedMock answers node-group writes the way the platform does
// under wait=true: once the change is recorded, before any node exists, and
// with no operation id.
type nodeGroupRecordedMock struct {
	baseMock
	previousCount int
	submitted     bool
}

func (m *nodeGroupRecordedMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"}, nil
}

func (m *nodeGroupRecordedMock) ScaleHetznerNodeGroup(_ context.Context, _, groupName string, count int, _ client.DrainOptions, _ bool) (*client.ScaleNodeGroupResult, bool, error) {
	if m.submitted {
		return nil, true, nil
	}
	return &client.ScaleNodeGroupResult{GroupName: groupName, PreviousCount: m.previousCount, NewCount: count}, false, nil
}

func (m *nodeGroupRecordedMock) AddHetznerNodeGroup(_ context.Context, _ string, request client.AddNodeGroupRequest, _ bool) (*client.AddNodeGroupResult, bool, error) {
	return &client.AddNodeGroupResult{GroupName: request.Name, Count: request.Count}, false, nil
}

func (m *nodeGroupRecordedMock) DeleteHetznerNodeGroup(_ context.Context, _, groupName string, _ client.DrainOptions, _ bool) (*client.DeleteNodeGroupResult, bool, error) {
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 2}, false, nil
}

func runNodeGroupWrite(t *testing.T, mock APIClient, args ...string) string {
	t.Helper()
	var runError error
	stdout := captureStdout(t, func() {
		_, runError = runConfirmCommand(t, mock, "", []*cobra.Command{
			clusterNodeGroupScaleCmd, clusterNodeGroupAddCmd, clusterNodeGroupDeleteCmd,
			nodeGroupScaleCmd, nodeGroupAddCmd, nodeGroupDeleteCmd,
		}, args...)
	})
	if runError != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), runError)
	}
	return stdout
}

// The platform answers wait=true once a scale, add or delete is recorded,
// before any node exists, and names no operation. The CLI printed "scaled
// from 2 to 3" about two minutes before the new node was Ready, with
// nothing to follow (ankra-v3qdto.6). It now says the nodes are still being
// created or removed and where to watch.
func TestNodeGroupWriteSaysTheNodesAreStillReconciling(t *testing.T) {
	follow := "ankra cluster operations list --cluster " + testClusterID
	for _, testCase := range []struct {
		name    string
		mock    *nodeGroupRecordedMock
		args    []string
		want    []string
		notWant []string
	}{
		{
			name: "scale up", mock: &nodeGroupRecordedMock{previousCount: 2},
			args:    []string{"cluster", "node-group", "scale", testClusterID, "workers", "3", "--wait"},
			want:    []string{"set to 3 node(s), up from 2", "being created", "become Ready", follow},
			notWant: []string{"scaled from"},
		},
		{
			name: "scale down", mock: &nodeGroupRecordedMock{previousCount: 3},
			args: []string{"cluster", "node-group", "scale", testClusterID, "workers", "1", "--wait"},
			want: []string{"set to 1 node(s), down from 3", "drained and removed", follow},
		},
		{
			name: "scale to the same count", mock: &nodeGroupRecordedMock{previousCount: 2},
			args:    []string{"cluster", "node-group", "scale", testClusterID, "workers", "2", "--wait"},
			want:    []string{"already has 2 node(s); nothing to change"},
			notWant: []string{follow},
		},
		{
			name: "add", mock: &nodeGroupRecordedMock{},
			args:    []string{"cluster", "node-group", "add", testClusterID, "--name", "gpu", "--instance-type", "cx32", "--count", "2", "--wait"},
			want:    []string{"Node group 'gpu' recorded with 2 node(s); the nodes are being created", follow},
			notWant: []string{"created with"},
		},
		{
			name: "delete", mock: &nodeGroupRecordedMock{},
			args:    []string{"cluster", "node-group", "delete", testClusterID, "gpu", "--yes", "--wait"},
			want:    []string{"Node group 'gpu' marked for deletion; its 2 node(s) are drained and removed", follow},
			notWant: []string{"node(s) removed."},
		},
		{
			name: "older hetzner scale", mock: &nodeGroupRecordedMock{previousCount: 2},
			args: []string{"cluster", "hetzner", "node-group", "scale", testClusterID, "workers", "3", "--wait"},
			want: []string{"set to 3 node(s), up from 2", follow},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout := runNodeGroupWrite(t, testCase.mock, testCase.args...)
			for _, want := range testCase.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("output lacks %q:\n%s", want, stdout)
				}
			}
			for _, notWant := range testCase.notWant {
				if strings.Contains(stdout, notWant) {
					t.Errorf("output still says %q:\n%s", notWant, stdout)
				}
			}
		})
	}
}

// Without --wait the answer told the user to re-run with --wait "to block
// until completion", which --wait does not do for these writes.
func TestNodeGroupWriteSubmittedSaysWhatWaitWaitsFor(t *testing.T) {
	stdout := runNodeGroupWrite(t, &nodeGroupRecordedMock{submitted: true},
		"cluster", "node-group", "scale", testClusterID, "workers", "3")
	for _, want := range []string{
		"--wait does not wait for the nodes themselves",
		"ankra cluster operations list --cluster " + testClusterID,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "block until completion") {
		t.Errorf("output still promises --wait blocks until completion:\n%s", stdout)
	}

	jsonOut, runError := runConfirmCommand(t, &nodeGroupRecordedMock{submitted: true}, "",
		[]*cobra.Command{clusterNodeGroupScaleCmd}, "cluster", "node-group", "scale", testClusterID, "workers", "3", "-o", "json")
	if runError != nil {
		t.Fatalf("scale -o json: %v", runError)
	}
	var result asyncSubmittedResult
	if decodeError := json.Unmarshal([]byte(jsonOut), &result); decodeError != nil {
		t.Fatalf("stdout must stay parseable JSON, got %q: %v", jsonOut, decodeError)
	}
	if strings.Contains(result.Hint, "block until completion") ||
		!strings.Contains(result.Hint, "ankra cluster operations list --cluster "+testClusterID) {
		t.Errorf("hint = %q, want the operations listing and no completion promise", result.Hint)
	}
}

// The --wait help says what it waits for on every scale, add and delete.
func TestNodeGroupWriteWaitHelpSaysWhatItWaitsFor(t *testing.T) {
	for _, command := range []*cobra.Command{
		clusterNodeGroupScaleCmd, clusterNodeGroupAddCmd, clusterNodeGroupDeleteCmd,
		nodeGroupScaleCmd, nodeGroupAddCmd, nodeGroupDeleteCmd,
		ovhNodeGroupScaleCmd, ovhNodeGroupAddCmd, ovhNodeGroupDeleteCmd,
		upcloudNodeGroupScaleCmd, upcloudNodeGroupAddCmd, upcloudNodeGroupDeleteCmd,
		digitaloceanNodeGroupScaleCmd, digitaloceanNodeGroupAddCmd, digitaloceanNodeGroupDeleteCmd,
	} {
		usage := command.Flags().Lookup("wait").Usage
		if !strings.Contains(usage, "does not wait for nodes") || strings.Contains(usage, "Wait for the operation to finish") {
			t.Errorf("%s --wait help = %q", command.CommandPath(), usage)
		}
	}
}
