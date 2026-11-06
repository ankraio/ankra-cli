package cmd

import (
	"bytes"
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
	lookups  int
	calls    []removeNodeCall
	result   *client.RemoveNodeGroupNodeResult
	accepted *client.AsyncWriteAcceptedResponse
	err      error
	// nodes is the cluster's node list (nil: the read fails), and
	// kubernetesNodes the providerID each Kubernetes node reports, by name
	// (nil: the read fails).
	nodes           []client.NodeSummary
	kubernetesNodes map[string]string
}

func (m *nodeGroupRemoveNodeMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	m.lookups++
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *nodeGroupRemoveNodeMock) ListHetznerClusterNodes(string) (*client.NodeListResult, error) {
	if m.nodes == nil {
		return nil, errors.New("listing nodes: status 503")
	}
	return &client.NodeListResult{Nodes: m.nodes}, nil
}

func (m *nodeGroupRemoveNodeMock) GetResources(_ string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	if m.kubernetesNodes == nil || len(request.ResourceRequests) != 1 || request.ResourceRequests[0].Kind != "Node" {
		return nil, errors.New("the agent did not answer")
	}
	var items []interface{}
	for name, providerID := range m.kubernetesNodes {
		items = append(items, map[string]interface{}{
			"metadata": map[string]interface{}{"name": name},
			"spec":     map[string]interface{}{"providerID": providerID},
		})
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{{Status: "success", Kind: "Node", Items: items}}}, nil
}

func (m *nodeGroupRemoveNodeMock) RemoveNodeGroupNode(ctx context.Context, kind, clusterID, groupName, node string,
	drainOptions client.DrainOptions, wait bool) (*client.RemoveNodeGroupNodeResult, *client.AsyncWriteAcceptedResponse, error) {
	m.calls = append(m.calls, removeNodeCall{kind: kind, clusterID: clusterID, groupName: groupName, node: node,
		drainOptions: drainOptions, wait: wait})
	return m.result, m.accepted, m.err
}

const (
	removeNodeOperationID = "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d"
	removeNodeServerID    = "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d"
)

func removeNodeStringPointer(value string) *string { return &value }

func newRemoveNodeMock(kind string) *nodeGroupRemoveNodeMock {
	operationID := removeNodeOperationID
	return &nodeGroupRemoveNodeMock{
		cluster: client.ClusterListItem{ID: testClusterID, Name: "depict-infrastructure", Kind: kind},
		result: &client.RemoveNodeGroupNodeResult{GroupName: "opensearch", NodeName: "depict-opensearch-0",
			NodeID: removeNodeServerID, PreviousCount: 4, NewCount: 3, OperationID: &operationID},
		nodes: []client.NodeSummary{
			{ID: removeNodeServerID, Name: "depict-opensearch-0", Role: removeNodeStringPointer("worker"),
				NodeGroup: removeNodeStringPointer("opensearch")},
			{ID: "0a9b8c7d-6e5f-4a3b-8c2d-1e0f9a8b7c6d", Name: "depict-opensearch-1", Role: removeNodeStringPointer("worker"),
				NodeGroup: removeNodeStringPointer("opensearch")},
			{ID: "11111111-2222-4333-8444-555555555555", Name: "depict-opensearch-0", IsDeleted: true},
		},
		kubernetesNodes: map[string]string{"depict-opensearch-0": "hcloud://168428300", "depict-opensearch-1": "hcloud://168428301"},
	}
}

var removeNodeResets = []*cobra.Command{clusterNodeGroupRemoveNodeCmd}

// runRemoveNode runs a command line and returns stdout and stderr apart, so
// a test can tell output from the prompt.
func runRemoveNode(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "node-group", "remove-node"}, args...))
	resetTreeFlags(t, removeNodeResets...)
	t.Cleanup(func() { resetTreeFlags(t, removeNodeResets...) })
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

// Removing a node deletes its server, so a declined prompt sends nothing and
// exits 4. The prompt, on stderr, names the node the reference resolved to:
// its name, its server id and its providerID.
func TestClusterNodeGroupRemoveNodeDeclineSendsNothing(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	stdout, prompt, err := runRemoveNode(t, mock, "n\n", testClusterID, "opensearch", "depict-opensearch-0")
	if !errors.Is(err, errCancelled) || exitCodeFor(err) != exitCancelled {
		t.Fatalf("expected errCancelled (exit %d) on decline, got %v (exit %d)", exitCancelled, err, exitCodeFor(err))
	}
	for _, want := range []string{
		`Remove node "depict-opensearch-0" from node group "opensearch"`,
		"Server id:   " + removeNodeServerID,
		"Provider id: hcloud://168428300",
		"This drains the node and deletes its server! [y/N]: ",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(stdout, "[y/N]") {
		t.Errorf("the prompt must not be written to stdout:\n%s", stdout)
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no removal on decline, got %v", mock.calls)
	}
}

// A providerID or a server id is resolved to the node it names, so the user
// approves a node rather than an id.
func TestClusterNodeGroupRemoveNodePromptResolvesProviderIDsAndServerIDs(t *testing.T) {
	for _, reference := range []string{"hcloud://168428300", removeNodeServerID, "DEPICT-OPENSEARCH-0"} {
		mock := newRemoveNodeMock("hetzner")
		_, prompt, err := runRemoveNode(t, mock, "n\n", testClusterID, "opensearch", reference)
		if !errors.Is(err, errCancelled) {
			t.Fatalf("%s: expected errCancelled on decline, got %v", reference, err)
		}
		if !strings.Contains(prompt, `Remove node "depict-opensearch-0" from node group "opensearch"`) ||
			!strings.Contains(prompt, "Server id:   "+removeNodeServerID) {
			t.Errorf("%s: the prompt must name the node it resolves to:\n%s", reference, prompt)
		}
	}
}

// A reference the CLI cannot match is said so in the prompt, with the
// reason, and is not refused: the platform resolves it itself.
func TestClusterNodeGroupRemoveNodeUnresolvedReferenceIsSaidAndStillSent(t *testing.T) {
	cases := map[string]func(*nodeGroupRemoveNodeMock){
		"no such node in the records": func(*nodeGroupRemoveNodeMock) {},
		"the node list cannot be read": func(mock *nodeGroupRemoveNodeMock) {
			mock.nodes = nil
		},
	}
	for name, arrange := range cases {
		mock := newRemoveNodeMock("hetzner")
		arrange(mock)
		_, prompt, err := runRemoveNode(t, mock, "y\n", testClusterID, "opensearch", "opensearch-9")
		if err != nil {
			t.Fatalf("%s: an unresolved reference must still be sent once confirmed, got %v", name, err)
		}
		if !strings.Contains(prompt, `Remove the node "opensearch-9" names from node group "opensearch"`) ||
			!strings.Contains(prompt, "The CLI could not tell which node that is") {
			t.Errorf("%s: the prompt must say the reference could not be resolved:\n%s", name, prompt)
		}
		if len(mock.calls) != 1 || mock.calls[0].node != "opensearch-9" {
			t.Errorf("%s: calls = %+v, want the reference sent as typed", name, mock.calls)
		}
	}
}

// The providerID is shown as not known when the cluster's Kubernetes nodes
// cannot be read, rather than leaving the line out or guessing.
func TestClusterNodeGroupRemoveNodePromptSaysWhenTheProviderIDIsNotKnown(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.kubernetesNodes = nil
	_, prompt, _ := runRemoveNode(t, mock, "n\n", testClusterID, "opensearch", "depict-opensearch-0")
	if !strings.Contains(prompt, "Provider id: not known: the cluster's Kubernetes nodes could not be read") {
		t.Errorf("the prompt must say the providerID is not known:\n%s", prompt)
	}
}

// With -o json and no --yes the prompt goes to stderr, so stdout stays one
// JSON document a script can read.
func TestClusterNodeGroupRemoveNodeJSONWithoutYesKeepsThePromptOffStdout(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	stdout, stderr, err := runRemoveNode(t, mock, "y\n", testClusterID, "opensearch", "depict-opensearch-0", "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("remove-node -o json failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["node_name"] != "depict-opensearch-0" || !strings.Contains(stderr, "[y/N]") {
		t.Fatalf("stdout = %s, stderr = %s", stdout, stderr)
	}
}

// The command sends the node exactly as named, to the cluster's own kind,
// guarded unless --force-drain asked otherwise. The output names the node
// that leaves, the counts, and how to follow the drain and the delete.
func TestClusterNodeGroupRemoveNodeSendsTheNamedNodeToTheClustersKind(t *testing.T) {
	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "ankracloud", "aws", "proxmox", "morpheus"} {
		mock := newRemoveNodeMock(kind)
		stdout, _, runError := runRemoveNode(t, mock, "", testClusterID, "opensearch", "hcloud://168428300", "--yes")
		if runError != nil {
			t.Fatalf("%s: remove-node failed: %v", kind, runError)
		}
		want := removeNodeCall{kind: kind, clusterID: testClusterID, groupName: "opensearch", node: "hcloud://168428300"}
		if len(mock.calls) != 1 || mock.calls[0] != want {
			t.Fatalf("%s: calls = %+v, want [%+v]", kind, mock.calls, want)
		}
		for _, said := range []string{
			"Node 'depict-opensearch-0' (server id " + removeNodeServerID + ") is leaving node group 'opensearch' (4 -> 3 nodes)",
			"ankra cluster operations list " + removeNodeOperationID + " --watch",
			"If the drain is refused, the operation fails and names what refused it, the node is kept",
		} {
			if !strings.Contains(stdout, said) {
				t.Errorf("%s: output does not say %q:\n%s", kind, said, stdout)
			}
		}
		if strings.Contains(stdout, "--wait") {
			t.Errorf("%s: output must not suggest re-running with --wait:\n%s", kind, stdout)
		}
	}
}

// A removal the platform created no operation for has two causes the CLI
// cannot tell apart (a stopped cluster, or a node change already running),
// so the message says what each one means rather than asserting one.
func TestClusterNodeGroupRemoveNodeWithoutAnOperationNamesBothCauses(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result.OperationID = nil
	stdout, _, runError := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if runError != nil {
		t.Fatalf("remove-node failed: %v", runError)
	}
	for _, said := range []string{
		"the cluster is stopped: the node was dropped from the cluster's record now, nothing is drained, " +
			"and it is not created again when the cluster starts",
		"another node change on the cluster is still running: the platform drains and deletes this node once it has finished",
		"ankra cluster operations list --cluster " + testClusterID,
	} {
		if !strings.Contains(stdout, said) {
			t.Errorf("output does not say %q:\n%s", said, stdout)
		}
	}
	if strings.Contains(stdout, "--watch") {
		t.Errorf("a removal without an operation must name no operation to follow:\n%s", stdout)
	}
}

func TestClusterNodeGroupRemoveNodeForceDrainSaysTheBudgetsAreBypassed(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	_, prompt, err := runRemoveNode(t, mock, "n\n", testClusterID, "opensearch", "depict-opensearch-0", "--force-drain")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if !strings.Contains(prompt, "without honouring its PodDisruptionBudgets") {
		t.Errorf("a forced removal's prompt must say the budgets are bypassed:\n%s", prompt)
	}

	mock = newRemoveNodeMock("hetzner")
	stdout, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--force-drain", "--yes", "--wait")
	if err != nil {
		t.Fatalf("forced remove-node failed: %v", err)
	}
	if len(mock.calls) != 1 || !mock.calls[0].drainOptions.ForceDrain || !mock.calls[0].wait {
		t.Fatalf("--force-drain --wait must reach the request, got %+v", mock.calls)
	}
	if !strings.Contains(stdout, "without honouring its pods' PodDisruptionBudgets") ||
		strings.Contains(stdout, "If the drain is refused") {
		t.Errorf("a forced removal's output must say the budgets are not honoured, and no drain refusal:\n%s", stdout)
	}
}

func TestClusterNodeGroupRemoveNodeJSONIsTheResult(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	stdout, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes", "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("remove-node -o json failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["node_name"] != "depict-opensearch-0" || decoded["new_count"] != float64(3) ||
		decoded["operation_id"] != removeNodeOperationID {
		t.Fatalf("JSON = %s", stdout)
	}
}

// A removal the platform decided before answering 202 names the node that
// leaves: the human output is the same as with --wait, and the JSON adds the
// node and the counts to the submitted shape.
func TestClusterNodeGroupRemoveNodeAcceptedNamesTheNodeThatLeaves(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.accepted = &client.AsyncWriteAcceptedResponse{Status: "accepted", OperationID: removeNodeOperationID}
	stdout, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err != nil {
		t.Fatalf("accepted remove-node failed: %v", err)
	}
	for _, said := range []string{
		"Node 'depict-opensearch-0' (server id " + removeNodeServerID + ") is leaving node group 'opensearch' (4 -> 3 nodes)",
		"ankra cluster operations list " + removeNodeOperationID + " --watch",
	} {
		if !strings.Contains(stdout, said) {
			t.Errorf("output does not say %q:\n%s", said, stdout)
		}
	}
	if strings.Contains(stdout, "--wait") {
		t.Errorf("output must not suggest re-running with --wait:\n%s", stdout)
	}

	mock = newRemoveNodeMock("hetzner")
	mock.accepted = &client.AsyncWriteAcceptedResponse{Status: "accepted", OperationID: removeNodeOperationID}
	stdout, _, err = runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("accepted remove-node -o json failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["submitted"] != true || decoded["operation_id"] != removeNodeOperationID ||
		decoded["node_name"] != "depict-opensearch-0" || decoded["previous_count"] != float64(4) ||
		decoded["new_count"] != float64(3) || !strings.Contains(decoded["hint"].(string), "--watch") {
		t.Fatalf("JSON = %s", stdout)
	}
}

// A removal the platform was still checking when it answered names no node:
// the output says so and where the outcome will show, and does not suggest
// sending it again.
func TestClusterNodeGroupRemoveNodeAcceptedWhileStillCheckingSaysWhereToLook(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.accepted = &client.AsyncWriteAcceptedResponse{Status: "accepted"}
	stdout, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err != nil {
		t.Fatalf("accepted remove-node failed: %v", err)
	}
	for _, said := range []string{
		"has not named the node that leaves yet",
		"ankra cluster operations list --cluster " + testClusterID,
		"ankra cluster operations list --failed --cluster " + testClusterID,
		"Do not run the removal again",
	} {
		if !strings.Contains(stdout, said) {
			t.Errorf("output does not say %q:\n%s", said, stdout)
		}
	}
	if strings.Contains(stdout, "--wait") {
		t.Errorf("output must not suggest re-running with --wait:\n%s", stdout)
	}

	mock = newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.accepted = &client.AsyncWriteAcceptedResponse{Status: "accepted"}
	stdout, _, err = runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("accepted remove-node -o json failed: %v", err)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["submitted"] != true || decoded["node_name"] != nil || strings.Contains(decoded["hint"].(string), "--wait") {
		t.Fatalf("JSON = %s", stdout)
	}
}

// A refusal is the platform's 409: it exits 1 and says the platform's reason.
func TestClusterNodeGroupRemoveNodeRefusalSaysThePlatformsReason(t *testing.T) {
	const reason = "Node 'depict-opensearch-0' is already being removed from node group 'opensearch'"
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.err = &client.UnexpectedResponseError{StatusCode: http.StatusConflict, Detail: reason}
	_, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err == nil || exitCodeFor(err) != exitError {
		t.Fatalf("expected exit %d for a refusal, got %v (exit %d)", exitError, err, exitCodeFor(err))
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.Detail != reason || !strings.HasPrefix(err.Error(), "removing node from node group: ") {
		t.Errorf("the error must carry the platform's reason, got %v", err)
	}
}

// A node the cluster does not have is the platform's 404, and exits 3.
func TestClusterNodeGroupRemoveNodeUnknownNodeExitsNotFound(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	mock.result = nil
	mock.err = &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: "Node 'opensearch-9' not found in this cluster"}
	_, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "opensearch-9", "--yes")
	if err == nil || exitCodeFor(err) != exitNotFound {
		t.Fatalf("expected exit %d for an unknown node, got %v (exit %d)", exitNotFound, err, exitCodeFor(err))
	}
}

// A platform without the route answers its router's 404: {"detail":
// "Not Found"} from the cluster API (routeAbsentDetail), or a body with no
// detail at all from anything in front of it. Neither is the node being
// gone, so both exit 1 and say the route is missing.
func TestClusterNodeGroupRemoveNodeOnAPlatformWithoutTheRouteExitsError(t *testing.T) {
	for name, routeAbsent := range map[string]error{
		"router 404 with the API's detail": &client.UnexpectedResponseError{StatusCode: http.StatusNotFound, Detail: routeAbsentDetail},
		"bare 404 with no detail":          client.NewUnexpectedResponseError(http.StatusNotFound, "request failed: status 404: 404 page not found"),
	} {
		mock := newRemoveNodeMock("hetzner")
		mock.result = nil
		mock.err = routeAbsent
		_, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
		if err == nil || exitCodeFor(err) != exitError {
			t.Fatalf("%s: expected exit %d for a platform without the route, got %v (exit %d)", name, exitError, err, exitCodeFor(err))
		}
		if !strings.Contains(err.Error(), "does not serve node removal yet") {
			t.Errorf("%s: the error must say the platform lacks the route, got %v", name, err)
		}
	}
}

func TestClusterNodeGroupRemoveNodeRefusesAClusterWithoutNodeGroups(t *testing.T) {
	mock := newRemoveNodeMock("imported")
	_, _, err := runRemoveNode(t, mock, "", testClusterID, "opensearch", "depict-opensearch-0", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error for a kind without node groups, got %v (exit %d)", err, exitCodeFor(err))
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no removal, got %v", mock.calls)
	}
}

// A group or node no node can have is a usage error, said before the
// cluster is looked up or a prompt is shown, rather than a request whose
// 404 reads as "this platform does not serve node removal yet" or, for a
// reference of dots, a request the edge sends to another route.
func TestClusterNodeGroupRemoveNodeRefusesAReferenceNoNodeCanHave(t *testing.T) {
	cases := []struct{ group, node, wantInError string }{
		{"opensearch", "", "the node is blank"},
		{"opensearch", "  ", "the node is blank"},
		{"opensearch", "..", "made only of dots"},
		{"opensearch", ".", "made only of dots"},
		{"opensearch", "%2e%2E", `decodes to ".."`},
		{"opensearch", "a/b", "is not a providerID"},
		{"opensearch", "x://..", "which no providerID has"},
		{"opensearch", "x://%2e%2e", "which no providerID has"},
		{"opensearch", "x://..%2f..", "which no providerID has"},
		{"opensearch", "hcloud://168428300/../..", "which no providerID has"},
		{"opensearch", "aws:///./i-0abc123", "which no providerID has"},
		{"", "depict-opensearch-0", "node group name is blank"},
		{"..", "depict-opensearch-0", "made only of dots"},
		{"a/b", "depict-opensearch-0", "contains '/'"},
	}
	for _, testCase := range cases {
		mock := newRemoveNodeMock("hetzner")
		_, stderr, err := runRemoveNode(t, mock, "y\n", testClusterID, testCase.group, testCase.node)
		if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), testCase.wantInError) {
			t.Errorf("%q %q: got %v (exit %d), want a usage error saying %q", testCase.group, testCase.node, err,
				exitCodeFor(err), testCase.wantInError)
		}
		if mock.lookups != 0 || strings.Contains(stderr, "[y/N]") || len(mock.calls) != 0 {
			t.Errorf("%q %q: checked too late (lookups=%d, calls=%d, stderr %q)", testCase.group, testCase.node,
				mock.lookups, len(mock.calls), stderr)
		}
	}
}

// A providerID is the one node reference with a '/', whatever its provider.
func TestClusterNodeGroupRemoveNodeAcceptsProviderIDs(t *testing.T) {
	for _, reference := range []string{"hcloud://168428300", "aws:///eu-west-1a/i-0abc123", "openstack:///3f2a9c",
		"vsphere://42.1f..aa/.vm", "  hcloud://168428300  "} {
		if err := validateNodeReference(reference); err != nil {
			t.Errorf("%s: refused a providerID: %v", reference, err)
		}
	}
}

// What is checked is what is sent: space around the reference is not part of
// it, so the prompt's lookup and the request both use the trimmed form.
func TestClusterNodeGroupRemoveNodeSendsTheReferenceItChecked(t *testing.T) {
	mock := newRemoveNodeMock("hetzner")
	_, stderr, runError := runRemoveNode(t, mock, "y\n", testClusterID, "opensearch", "  hcloud://168428300  ")
	if runError != nil {
		t.Fatalf("remove-node failed: %v", runError)
	}
	want := removeNodeCall{kind: "hetzner", clusterID: testClusterID, groupName: "opensearch", node: "hcloud://168428300"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want [%+v]", mock.calls, want)
	}
	if !strings.Contains(stderr, "depict-opensearch-0") {
		t.Errorf("the prompt did not resolve the trimmed reference: %q", stderr)
	}
}

// --wait waits for the platform's answer, which comes once the node is
// marked and its operation started; the help says so rather than promising
// the drain and the delete.
func TestClusterNodeGroupRemoveNodeWaitSaysWhatItWaitsFor(t *testing.T) {
	usage := clusterNodeGroupRemoveNodeCmd.Flags().Lookup("wait").Usage
	if !strings.Contains(usage, "It does not wait for the drain or the delete") {
		t.Errorf("--wait usage = %q", usage)
	}
	if !strings.Contains(clusterNodeGroupRemoveNodeCmd.Long, "--wait waits for the platform's answer only") {
		t.Error("the help must say what --wait waits for")
	}
}
