package cmd

// Tests for `ankra cluster <provider> nodes restart`: the command wires the
// provider-specific client method and surfaces both the scheduled-operation
// confirmation and the usecase's error message unchanged.

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type nodeRestartMock struct {
	baseMock

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
		_, _ = executeCommand("cluster", "hetzner", "nodes", "restart", "cluster-1", "node-1")
	})

	if len(mock.restartCalls) != 1 || mock.restartCalls[0] != "cluster-1:node-1" {
		t.Fatalf("expected one restart call for cluster-1:node-1, got %v", mock.restartCalls)
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

	_, commandError := executeCommand("cluster", "hetzner", "nodes", "restart", "cluster-1", "node-1")
	if commandError == nil || !strings.Contains(commandError.Error(), "must be in 'up' state") {
		t.Fatalf("expected the invalid-state error, got %v", commandError)
	}
}

func TestClusterNodesRestartCommandRequiresBothArgs(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &nodeRestartMock{})

	if _, err := executeCommand("cluster", "hetzner", "nodes", "restart", "cluster-1"); err == nil {
		t.Fatal("expected an error when node_id is missing")
	}
}

func TestClusterNodesRestartSurfaceMatchesProviderSupport(t *testing.T) {
	expectations := map[string]bool{
		"hetzner":      true,
		"ovh":          true,
		"upcloud":      true,
		"digitalocean": true,
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
