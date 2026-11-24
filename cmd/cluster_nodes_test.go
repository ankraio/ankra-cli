package cmd

// Tests for `ankra cluster <provider> nodes restart`: the command wires the
// provider-specific client method and surfaces both the scheduled-operation
// confirmation and the usecase's error message unchanged.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type nodeRestartMock struct {
	restartResult *client.RestartNodeResult
	restartError  error
	restartCalls  []string
}

func (m *nodeRestartMock) RestartHetznerClusterNode(clusterID, nodeID string) (*client.RestartNodeResult, error) {
	m.restartCalls = append(m.restartCalls, clusterID+":"+nodeID)
	if m.restartError != nil {
		return nil, m.restartError
	}
	return m.restartResult, nil
}

func TestClusterNodesRestartCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &nodeRestartMock{
		restartResult: &client.RestartNodeResult{OperationID: "op-1", NodeID: "node-1", JobName: "hetzner_restart_server"},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "nodes", "restart", testClusterID, "node-1")
	})

	if len(mock.restartCalls) != 1 || mock.restartCalls[0] != testClusterID+":node-1" {
		t.Fatalf("expected one restart call for %s:node-1, got %v", testClusterID, mock.restartCalls)
	}
	if !strings.Contains(stdoutOutput, "Restart scheduled for node node-1") {
		t.Errorf("expected restart confirmation, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "op-1") {
		t.Errorf("expected operation id in output, got: %s", stdoutOutput)
	}
}

func TestClusterNodesRestartCommandSurfacesError(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &nodeRestartMock{
		restartError: errors.New("Node must be in 'up' state to restart (current state: down)"),
	}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "hetzner", "nodes", "restart", testClusterID, "node-1")
	if commandError == nil || !strings.Contains(commandError.Error(), "must be in 'up' state") {
		t.Fatalf("expected the invalid-state error, got %v", commandError)
	}
}

func TestClusterNodesRestartCommandRequiresBothArgs(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &nodeRestartMock{})

	if _, err := executeCommand("cluster", "hetzner", "nodes", "restart", testClusterID); err == nil {
		t.Fatal("expected an error when node_id is missing")
	}
}

type scalewayNodesMock struct {
	restartResult *client.RestartNodeResult
	restartCalls  []string
	listResult    *client.NodeListResult
	listCalls     []string
}

func (m *scalewayNodesMock) RestartScalewayClusterNode(clusterID, nodeID string) (*client.RestartNodeResult, error) {
	m.restartCalls = append(m.restartCalls, clusterID+":"+nodeID)
	return m.restartResult, nil
}

func (m *scalewayNodesMock) ListScalewayClusterNodes(clusterID string) (*client.NodeListResult, error) {
	m.listCalls = append(m.listCalls, clusterID)
	return m.listResult, nil
}

func TestClusterNodesRestartCommandDispatchesScaleway(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &scalewayNodesMock{
		restartResult: &client.RestartNodeResult{OperationID: "op-9", NodeID: "node-9", JobName: "scaleway_restart_server"},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "nodes", "restart", testClusterID, "node-9")
	})

	if len(mock.restartCalls) != 1 || mock.restartCalls[0] != testClusterID+":node-9" {
		t.Fatalf("expected one Scaleway restart call for %s:node-9, got %v", testClusterID, mock.restartCalls)
	}
	if !strings.Contains(stdoutOutput, "scaleway_restart_server") {
		t.Errorf("expected the Scaleway job name in output, got: %s", stdoutOutput)
	}
}

func TestClusterNodesListCommandDispatchesScaleway(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &scalewayNodesMock{
		listResult: &client.NodeListResult{Nodes: []client.NodeSummary{{ID: "node-9", Name: "worker-1", State: "up"}}},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "nodes", "list", testClusterID)
	})

	if len(mock.listCalls) != 1 || mock.listCalls[0] != testClusterID {
		t.Fatalf("expected one Scaleway list call for %s, got %v", testClusterID, mock.listCalls)
	}
	if !strings.Contains(stdoutOutput, "worker-1") {
		t.Errorf("expected the node name in output, got: %s", stdoutOutput)
	}
}

func TestClusterNodesRestartSurfaceMatchesProviderSupport(t *testing.T) {
	expectations := map[string]bool{
		"hetzner":      true,
		"ovh":          true,
		"upcloud":      true,
		"digitalocean": true,
		"scaleway":     true,
		"aws":          true,
		"proxmox":      true,
		"morpheus":     false,
	}
	for providerName, shouldHaveRestart := range expectations {
		nodesCmd := findSubcommandPath(t, "cluster", providerName, "nodes")
		hasRestart := false
		for _, child := range nodesCmd.Commands() {
			if child.Name() == "restart" {
				hasRestart = true
			}
		}
		if hasRestart != shouldHaveRestart {
			t.Errorf("cluster %s nodes restart offered=%v, want %v (the platform has no Morpheus restart lane)", providerName, hasRestart, shouldHaveRestart)
		}
	}
}

func findSubcommandPath(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	current := rootCmd
	for _, name := range path {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == name {
				next = child
				break
			}
		}
		if next == nil {
			t.Fatalf("command path %v: %q not found", path, name)
		}
		current = next
	}
	return current
}

// ankra-lk328: on 2026-11-24 OVH lost EU-WEST-PAR-A and the nodes list could
// not say which machines, or whether the bastion, ran there. The table now
// shows each node's zone and public IP; a zone only the node group asked for
// is starred and explained, and a node with no known zone shows "-" rather
// than a guess.
func TestClusterNodesListShowsTheZoneAndPublicIP(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &scalewayNodesMock{listResult: &client.NodeListResult{Nodes: []client.NodeSummary{
		{ID: "node-1", Name: "bastion", State: "up", AvailabilityZone: strPtrCmd("eu-west-par-a"),
			AvailabilityZoneSource: strPtrCmd("provider"), PublicIP: strPtrCmd("57.130.20.200")},
		{ID: "node-2", Name: "db-par-a-0", State: "creating", AvailabilityZone: strPtrCmd("eu-west-par-a"),
			AvailabilityZoneSource: strPtrCmd("requested"), PrivateIP: strPtrCmd("10.0.0.12")},
		{ID: "node-3", Name: "cp-0", State: "up", PrivateIP: strPtrCmd("10.0.0.10")},
	}}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "nodes", "list", testClusterID)
	})

	lines := strings.Split(stdoutOutput, "\n")
	zoneColumn, publicIPColumn := strings.Index(lines[0], "ZONE "), strings.Index(lines[0], "PUBLIC_IP ")
	if zoneColumn < 0 || publicIPColumn < 0 {
		t.Fatalf("the header must carry ZONE and PUBLIC_IP, got: %s", lines[0])
	}
	cell := func(node string, column int) string {
		t.Helper()
		row := rowOf(t, lines, node)
		return strings.Fields(row[column:])[0]
	}
	for _, want := range []struct{ node, zone, publicIP string }{
		{"bastion", "eu-west-par-a", "57.130.20.200"},
		{"db-par-a-0", "eu-west-par-a*", "-"},
		{"cp-0", "-", "-"},
	} {
		if zone := cell(want.node, zoneColumn); zone != want.zone {
			t.Errorf("%s ZONE = %q, want %q", want.node, zone, want.zone)
		}
		if publicIP := cell(want.node, publicIPColumn); publicIP != want.publicIP {
			t.Errorf("%s PUBLIC_IP = %q, want %q", want.node, publicIP, want.publicIP)
		}
	}
	if !strings.Contains(stdoutOutput, "* the zone the node group asked for") {
		t.Errorf("a starred zone must be explained under the table, got: %s", stdoutOutput)
	}
}

func TestClusterNodesListOmitsTheFootnoteWhenNoZoneIsOnlyRequested(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &scalewayNodesMock{listResult: &client.NodeListResult{Nodes: []client.NodeSummary{
		{ID: "node-1", Name: "worker-1", State: "up", AvailabilityZone: strPtrCmd("fr-par-2"), AvailabilityZoneSource: strPtrCmd("location")},
	}}})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "nodes", "list", testClusterID)
	})

	if strings.Contains(stdoutOutput, "*") {
		t.Errorf("no zone is only requested, so nothing is starred or explained, got: %s", stdoutOutput)
	}
}

func TestClusterNodesListJSONPassesTheNodeFieldsThrough(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &scalewayNodesMock{listResult: &client.NodeListResult{Nodes: []client.NodeSummary{
		{ID: "node-1", Name: "bastion", State: "up", AvailabilityZone: strPtrCmd("eu-west-par-a"),
			AvailabilityZoneSource: strPtrCmd("provider"), PublicIP: strPtrCmd("57.130.20.200"),
			KubernetesVersion: strPtrCmd("v1.33.4"), ProviderStatus: strPtrCmd("ACTIVE")},
	}}})
	listCommand, _, findError := rootCmd.Find([]string{"cluster", "scaleway", "nodes", "list"})
	if findError != nil {
		t.Fatalf("finding the list command: %v", findError)
	}
	t.Cleanup(func() { _ = listCommand.Flags().Set("output", "") })

	output, runError := executeCommand("cluster", "scaleway", "nodes", "list", testClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, output)
	}
	if len(decoded.Nodes) != 1 {
		t.Fatalf("expected one node, got %s", output)
	}
	node := decoded.Nodes[0]
	for key, want := range map[string]any{
		"availability_zone": "eu-west-par-a", "availability_zone_source": "provider", "public_ip": "57.130.20.200",
		"kubernetes_version": "v1.33.4", "provider_status": "ACTIVE", "provider_power_state": nil,
	} {
		value, present := node[key]
		if !present || value != want {
			t.Errorf("%s = %v (present %v), want %v", key, value, present, want)
		}
	}
}

// rowOf is the table row that names node.
func rowOf(t *testing.T, lines []string, node string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, " "+node+" ") {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", node, strings.Join(lines, "\n"))
	return ""
}
