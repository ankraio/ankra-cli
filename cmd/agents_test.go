package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type agentRunsMock struct {
	baseMock
	runs        []client.AgentRun
	cancelCalls int
	cancelError error
}

func (m *agentRunsMock) ListAgentRuns(taskID string, statuses []string, limit int) (*client.AgentRunListResponse, error) {
	return &client.AgentRunListResponse{Runs: m.runs}, nil
}

func (m *agentRunsMock) GetAgentRun(runID string) (*client.AgentRun, error) {
	for index := range m.runs {
		if m.runs[index].ID == runID {
			return &m.runs[index], nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, "Agent run not found.")
}

func (m *agentRunsMock) CancelAgentRun(runID string) (*client.CancelAgentRunResponse, error) {
	m.cancelCalls++
	if m.cancelError != nil {
		return nil, m.cancelError
	}
	return &client.CancelAgentRunResponse{Detail: "Run cancelled.", Status: "cancelled",
		CancelledSessionID: "3a67c807-6f0b-45c8-9c2a-000000000001"}, nil
}

func sampleAgentRun() client.AgentRun {
	outcome := "Investigated the ticket."
	return client.AgentRun{
		ID:        "1f14b8f7-0000-4000-8000-000000000001",
		TaskID:    "2f14b8f7-0000-4000-8000-000000000002",
		TaskName:  "SRE",
		Status:    "running",
		StartedAt: "2026-07-20T15:00:00Z",
		TurnsUsed: 1, OutcomeSummary: &outcome,
	}
}

func TestAgentsRunsListsRuns(t *testing.T) {
	mock := &agentRunsMock{runs: []client.AgentRun{sampleAgentRun()}}
	out, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{agentsRunsCmd}, "agents", "runs")
	if err != nil {
		t.Fatalf("agents runs failed: %v", err)
	}
	if !strings.Contains(out, "SRE") || !strings.Contains(out, "running") {
		t.Fatalf("run table missing fields: %s", out)
	}
}

func TestAgentsRunNotFoundUsesExitNotFound(t *testing.T) {
	mock := &agentRunsMock{}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{agentsRunCmd}, "agents", "run", "1f14b8f7-0000-4000-8000-00000000dead")
	if err == nil {
		t.Fatal("expected an error for a missing run")
	}
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("expected exit code %d, got %d", exitNotFound, got)
	}
}

func TestAgentsCancelDeclinedUsesExitCancelled(t *testing.T) {
	mock := &agentRunsMock{}
	_, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{agentsCancelCmd}, "agents", "cancel", "1f14b8f7-0000-4000-8000-000000000001")
	if err == nil {
		t.Fatal("expected the declined confirmation to error")
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.cancelCalls != 0 {
		t.Errorf("declined confirmation must not cancel, got %d calls", mock.cancelCalls)
	}
}

func TestAgentsCancelConfirmedCancels(t *testing.T) {
	mock := &agentRunsMock{}
	out, err := runConfirmCommand(t, mock, "y\n",
		[]*cobra.Command{agentsCancelCmd}, "agents", "cancel", "1f14b8f7-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatalf("agents cancel failed: %v", err)
	}
	if mock.cancelCalls != 1 {
		t.Fatalf("expected one cancel call, got %d", mock.cancelCalls)
	}
	if !strings.Contains(out, "Run cancelled") {
		t.Fatalf("missing cancel confirmation output: %s", out)
	}
}
