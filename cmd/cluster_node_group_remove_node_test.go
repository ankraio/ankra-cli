package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// removeNodeCall is one RemoveNodeGroupNode request the command sent.
type removeNodeCall struct {
	kind, clusterID, groupName, node string
	drainOptions                     client.DrainOptions
	wait                             bool
}

type nodeGroupRemoveNodeMock struct {
	baseMock
	cluster  client.ClusterListItem
	calls    []removeNodeCall
	result   *client.RemoveNodeGroupNodeResult
	accepted *client.AsyncWriteAcceptedResponse
	err      error
}

func (m *nodeGroupRemoveNodeMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *nodeGroupRemoveNodeMock) RemoveNodeGroupNode(ctx context.Context, kind, clusterID, groupName, node string,
	drainOptions client.DrainOptions, wait bool) (*client.RemoveNodeGroupNodeResult, *client.AsyncWriteAcceptedResponse, error) {
	m.calls = append(m.calls, removeNodeCall{kind: kind, clusterID: clusterID, groupName: groupName, node: node,
		drainOptions: drainOptions, wait: wait})
	return m.result, m.accepted, m.err
}

func newRemoveNodeMock(kind string) *nodeGroupRemoveNodeMock {
	operationID := "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d"
	return &nodeGroupRemoveNodeMock{
		cluster: client.ClusterListItem{ID: testClusterID, Name: "depict-infrastructure", Kind: kind},
		result: &client.RemoveNodeGroupNodeResult{GroupName: "opensearch", NodeName: "depict-opensearch-0",
			NodeID: "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d", PreviousCount: 4, NewCount: 3, OperationID: &operationID},
	}
}

var removeNodeResets = []*cobra.Command{clusterNodeGroupRemoveNodeCmd}

// Removing a node deletes its server, so a declined prompt sends nothing and
// exits 4.
func TestClusterNodeGroupRemoveNodeDeclineSendsNothing(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	output, err := runConfirmCommand(t, mock, "n\n", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0")
	if !errors.Is(err, errCancelled) || exitCodeFor(err) != exitCancelled {
		t.Fatalf("expected errCancelled (exit %d) on decline, got %v (exit %d)", exitCancelled, err, exitCodeFor(err))
	}
	if !strings.Contains(output, `Remove node "depict-opensearch-0" from node group "opensearch"`) ||
		!strings.Contains(output, "deletes its server") {
		t.Errorf("the prompt must name the node, the group and the consequence:\n%s", output)
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no removal on decline, got %v", mock.calls)
	}
}

// The command sends the node exactly as named, to the cluster's own kind,
// guarded unless --force-drain asked otherwise, and reports the operation.
func TestClusterNodeGroupRemoveNodeSendsTheNamedNodeToTheClustersKind(t *testing.T) {
	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "ankracloud", "aws", "proxmox", "morpheus"} {
		mock := newRemoveNodeMock(kind)
		var runError error
		stdout := captureStdout(t, func() {
			_, runError = runConfirmCommand(t, mock, "", removeNodeResets,
				"cluster", "node-group", "remove-node", testClusterID, "opensearch", "hcloud://168428300", "--yes")
		})
		if runError != nil {
			t.Fatalf("%s: remove-node failed: %v", kind, runError)
		}
		want := removeNodeCall{kind: kind, clusterID: testClusterID, groupName: "opensearch", node: "hcloud://168428300"}
		if len(mock.calls) != 1 || mock.calls[0] != want {
			t.Fatalf("%s: calls = %+v, want [%+v]", kind, mock.calls, want)
		}
		if !strings.Contains(stdout, "Node 'depict-opensearch-0' is being removed from node group 'opensearch' (4 -> 3 nodes)") ||
			!strings.Contains(stdout, "ankra cluster operations list 4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d") {
			t.Errorf("%s: output must name the removal and the operation to follow:\n%s", kind, stdout)
		}
	}
}

func TestClusterNodeGroupRemoveNodeForceDrainSaysTheBudgetsAreBypassed(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	output, err := runConfirmCommand(t, mock, "n\n", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--force-drain")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if !strings.Contains(output, "without honouring its PodDisruptionBudgets") {
		t.Errorf("a forced removal's prompt must say the budgets are bypassed:\n%s", output)
	}

	mock = newRemoveNodeMock("hetzner")
	captureStdout(t, func() {
		_, err = runConfirmCommand(t, mock, "", removeNodeResets,
			"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--force-drain", "--yes", "--wait")
	})
	if err != nil {
		t.Fatalf("forced remove-node failed: %v", err)
	}
	if len(mock.calls) != 1 || !mock.calls[0].drainOptions.ForceDrain || !mock.calls[0].wait {
		t.Fatalf("--force-drain --wait must reach the request, got %+v", mock.calls)
	}
}

func TestClusterNodeGroupRemoveNodeJSONIsTheResult(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	output, err := runConfirmCommand(t, mock, "", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--yes", "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("remove-node -o json failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if decoded["node_name"] != "depict-opensearch-0" || decoded["new_count"] != float64(3) ||
		decoded["operation_id"] != "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d" {
		t.Fatalf("JSON = %s", output)
	}
}

// A removal the platform accepted without waiting names the operation it
// created, so a script can follow it.
func TestClusterNodeGroupRemoveNodeAcceptedNamesTheOperation(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.accepted = &client.AsyncWriteAcceptedResponse{Status: "accepted", OperationID: "9d7e1f0e-2a3b-4c5d-8e6f-0a1b2c3d4e5f"}
	output, err := runConfirmCommand(t, mock, "", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("accepted remove-node failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if decoded["submitted"] != true || decoded["operation_id"] != "9d7e1f0e-2a3b-4c5d-8e6f-0a1b2c3d4e5f" {
		t.Fatalf("JSON = %s", output)
	}
}

// A node the cluster does not have is the platform's 404, and exits 3.
func TestClusterNodeGroupRemoveNodeUnknownNodeExitsNotFound(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.err = &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Node 'opensearch-9' not found in this cluster"}
	_, err := runConfirmCommand(t, mock, "", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "opensearch-9", "--yes")
	if err == nil || exitCodeFor(err) != exitNotFound {
		t.Fatalf("expected exit %d for an unknown node, got %v (exit %d)", exitNotFound, err, exitCodeFor(err))
	}
}

// A platform that does not serve the route answers the router's bare 404;
// that is not a missing node, so it is said plainly and exits 1.
func TestClusterNodeGroupRemoveNodeOnAPlatformWithoutTheRouteExitsError(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.err = client.NewUnexpectedResponseError(http.StatusNotFound, "request failed: status 404: 404 page not found")
	_, err := runConfirmCommand(t, mock, "", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err == nil || exitCodeFor(err) != exitError {
		t.Fatalf("expected exit %d for a platform without the route, got %v (exit %d)", exitError, err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "does not serve node removal yet") {
		t.Errorf("the error must say the platform lacks the route, got %v", err)
	}
}

func TestClusterNodeGroupRemoveNodeRefusesAClusterWithoutNodeGroups(t *testing.T) {
	mock := newRemoveNodeMock("imported")
	_, err := runConfirmCommand(t, mock, "", removeNodeResets,
		"cluster", "node-group", "remove-node", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error for a kind without node groups, got %v (exit %d)", err, exitCodeFor(err))
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no removal, got %v", mock.calls)
	}
}
