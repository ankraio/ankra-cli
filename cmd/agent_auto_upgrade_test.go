package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type agentAutoUpgradeCall struct {
	clusterID string
	enabled   bool
}

type agentAutoUpgradeMock struct {
	baseMock
	calls       []agentAutoUpgradeCall
	updateError error
}

func (mock *agentAutoUpgradeMock) SetClusterAgentAutoUpgrade(_ context.Context, clusterID string,
	enabled bool) (*client.AgentSettingsResult, error) {
	mock.calls = append(mock.calls, agentAutoUpgradeCall{clusterID: clusterID, enabled: enabled})
	if mock.updateError != nil {
		return nil, mock.updateError
	}
	return &client.AgentSettingsResult{Success: true, Message: "Cluster agent settings updated successfully"}, nil
}

func runAgentAutoUpgradeCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	return executeCommand(arguments...)
}

func TestClusterAgentAutoUpgradeDisableSendsTheOptOut(t *testing.T) {
	mock := &agentAutoUpgradeMock{}

	output, runError := runAgentAutoUpgradeCommand(t, mock, "cluster", "agent", "auto-upgrade", "disable")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.calls) != 1 || mock.calls[0].clusterID != testClusterID || mock.calls[0].enabled {
		t.Fatalf("expected one opt-out write for the selected cluster, got %+v", mock.calls)
	}
	for _, want := range []string{
		"Automatic agent upgrades disabled for cluster 'test-cluster'",
		"The fleet rollout skips this agent until you run 'ankra cluster agent auto-upgrade enable'",
		"'ankra cluster agent upgrade' still applies the latest release on demand",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterAgentAutoUpgradeEnablePutsTheAgentBack(t *testing.T) {
	mock := &agentAutoUpgradeMock{}

	output, runError := runAgentAutoUpgradeCommand(t, mock, "cluster", "agent", "auto-upgrade", "enable")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.calls) != 1 || mock.calls[0].clusterID != testClusterID || !mock.calls[0].enabled {
		t.Fatalf("expected one opt-in write for the selected cluster, got %+v", mock.calls)
	}
	if !strings.Contains(output, "Automatic agent upgrades enabled for cluster 'test-cluster'") {
		t.Errorf("output missing the confirmation:\n%s", output)
	}
}

func TestClusterAgentAutoUpgradeStructuredOutputStaysParseable(t *testing.T) {
	mock := &agentAutoUpgradeMock{}

	output, runError := runAgentAutoUpgradeCommand(t, mock, "cluster", "agent", "auto-upgrade", "disable", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded agentAutoUpgradeOutput
	if decodeError := json.Unmarshal([]byte(strings.TrimSpace(output)), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, output)
	}
	if decoded.ClusterID != testClusterID || decoded.ClusterName != "test-cluster" || decoded.AutoUpgradeEnabled {
		t.Fatalf("unexpected structured output %+v", decoded)
	}
}

// A platform refusal reaches the user with the command's context and the
// backend detail unchanged, and nothing prints as if it had worked.
func TestClusterAgentAutoUpgradeRefusalReachesTheUser(t *testing.T) {
	mock := &agentAutoUpgradeMock{updateError: errors.New("Cluster agent not found")}

	output, runError := runAgentAutoUpgradeCommand(t, mock, "cluster", "agent", "auto-upgrade", "disable")
	if runError == nil || !strings.Contains(runError.Error(), "updating agent auto-upgrade: Cluster agent not found") {
		t.Fatalf("expected the refusal, got %v", runError)
	}
	if strings.Contains(output, "disabled for cluster") {
		t.Fatalf("a refused switch printed success:\n%s", output)
	}
}

func TestClusterAgentAutoUpgradeSwitchesTakeNoArguments(t *testing.T) {
	mock := &agentAutoUpgradeMock{}

	_, runError := runAgentAutoUpgradeCommand(t, mock, "cluster", "agent", "auto-upgrade", "disable", "prod")
	if runError == nil {
		t.Fatal("expected a usage error for a positional argument")
	}
	if len(mock.calls) != 0 {
		t.Fatalf("a usage error still wrote the flag: %+v", mock.calls)
	}
}
