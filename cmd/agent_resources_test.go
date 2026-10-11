package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type agentResourcesMock struct {
	baseMock
	resources   *client.AgentResources
	getError    error
	updateError error

	getCalls      int
	updateCalls   int
	lastClusterID string
	lastUpdate    client.AgentResourcesUpdate
}

func (mock *agentResourcesMock) GetAgentResources(requestContext context.Context,
	clusterID string) (*client.AgentResources, error) {
	mock.getCalls++
	mock.lastClusterID = clusterID
	if mock.getError != nil {
		return nil, mock.getError
	}
	return mock.resources, nil
}

func (mock *agentResourcesMock) UpdateAgentResources(requestContext context.Context, clusterID string,
	update client.AgentResourcesUpdate) (*client.AgentResources, error) {
	mock.updateCalls++
	mock.lastClusterID = clusterID
	mock.lastUpdate = update
	if mock.updateError != nil {
		return nil, mock.updateError
	}
	return mock.resources, nil
}

// agentResourcesCommands collects the `cluster agent resources` tree so a
// test can put its flags back: the commands are built once in init and shared
// by the whole package, so a --memory-limit left set would leak into the next
// test (and, through Changed, be sent by it).
func agentResourcesCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterAgentCmd.Commands() {
		if candidate.Name() == "resources" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster agent resources' command is not registered")
	return nil
}

func runAgentResourcesCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentResourcesCommands(t)...) })
	return executeCommand(arguments...)
}

const agentResourcesAppliedMessage = "The agent is re-rendering its release with the new resources."

func newAgentResources() *client.AgentResources {
	memoryLimit := "2Gi"
	cpuRequest := "250m"
	updatedAt := "2026-10-11T10:00:00Z"
	return &client.AgentResources{
		MemoryLimit: &memoryLimit,
		CPURequest:  &cpuRequest,
		UpdatedAt:   &updatedAt,
		ApplyState:  client.AgentResourcesApplyStateApplied,
		Message:     agentResourcesAppliedMessage,
	}
}

func TestClusterAgentResourcesGetPrintsTheSizingAndTheMessage(t *testing.T) {
	mock := &agentResourcesMock{resources: newAgentResources()}

	output, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.getCalls != 1 || mock.lastClusterID != testClusterID {
		t.Fatalf("expected one read of the selected cluster, got %d for %q", mock.getCalls, mock.lastClusterID)
	}
	for _, want := range []string{
		"Agent resources for cluster 'test-cluster'",
		"Memory limit:    2Gi",
		"Memory request:  chart default",
		"CPU limit:       chart default",
		"CPU request:     250m",
		"Apply state:     applied",
		"\n" + agentResourcesAppliedMessage + "\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// A cluster that stores nothing reads as defaults rather than as a failure.
func TestClusterAgentResourcesGetNamesTheDefaults(t *testing.T) {
	mock := &agentResourcesMock{resources: &client.AgentResources{
		ApplyState: client.AgentResourcesApplyStateNotSet,
		Message:    "No resources are stored; the agent runs with the chart's defaults.",
	}}

	output, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	for _, want := range []string{
		"Memory limit:    chart default",
		"CPU request:     chart default",
		"Last changed:    never",
		"Apply state:     not_set",
		"No resources are stored; the agent runs with the chart's defaults.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterAgentResourcesGetStructuredOutputStaysParseable(t *testing.T) {
	mock := &agentResourcesMock{resources: newAgentResources()}

	output, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded["memory_limit"] != "2Gi" || decoded["apply_state"] != client.AgentResourcesApplyStateApplied {
		t.Errorf("decoded = %v", decoded)
	}
	// A member the cluster does not set stays null, so a script can tell
	// "chart default" from a stored value.
	if value, present := decoded["memory_request"]; !present || value != nil {
		t.Errorf("memory_request = %v (present %v), want null", value, present)
	}
}

func TestClusterAgentResourcesSetSendsOnlyTheNamedFlags(t *testing.T) {
	mock := &agentResourcesMock{resources: newAgentResources()}

	output, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set",
		"--memory-limit", "2Gi", "--cpu-request", "250m")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.updateCalls != 1 || mock.lastClusterID != testClusterID {
		t.Fatalf("expected one update of the selected cluster, got %d for %q", mock.updateCalls, mock.lastClusterID)
	}
	update := mock.lastUpdate
	if update.MemoryLimit == nil || *update.MemoryLimit != "2Gi" {
		t.Errorf("memory limit sent = %v, want 2Gi", update.MemoryLimit)
	}
	if update.CPURequest == nil || *update.CPURequest != "250m" {
		t.Errorf("cpu request sent = %v, want 250m", update.CPURequest)
	}
	if update.MemoryRequest != nil || update.CPULimit != nil {
		t.Errorf("unnamed members were sent: %+v", update)
	}
	for _, want := range []string{
		"Agent resources updated for cluster 'test-cluster'.",
		"Memory limit:    2Gi",
		"\n" + agentResourcesAppliedMessage + "\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// --memory-limit "" is how a member goes back to the chart default, so the
// flag being named is what decides the send, not its value being non-empty.
func TestClusterAgentResourcesSetSendsAnEmptyValueToClear(t *testing.T) {
	mock := &agentResourcesMock{resources: &client.AgentResources{ApplyState: client.AgentResourcesApplyStateNotSet}}

	if _, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set",
		"--memory-limit", ""); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.updateCalls != 1 {
		t.Fatalf("expected one update, got %d", mock.updateCalls)
	}
	if mock.lastUpdate.MemoryLimit == nil || *mock.lastUpdate.MemoryLimit != "" {
		t.Errorf("memory limit sent = %v, want an explicit empty string", mock.lastUpdate.MemoryLimit)
	}
	if mock.lastUpdate.CPULimit != nil || mock.lastUpdate.CPURequest != nil || mock.lastUpdate.MemoryRequest != nil {
		t.Errorf("unnamed members were sent: %+v", mock.lastUpdate)
	}
}

func TestClusterAgentResourcesSetWithoutAFlagIsAUsageError(t *testing.T) {
	mock := &agentResourcesMock{resources: newAgentResources()}

	_, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set")
	if runError == nil {
		t.Fatal("expected a set with no sizing flag to be refused")
	}
	if exitCode := exitCodeFor(runError); exitCode != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCode, exitUsage)
	}
	for _, flagName := range agentResourcesFlags {
		if !strings.Contains(runError.Error(), "--"+flagName) {
			t.Errorf("error should name --%s, got %v", flagName, runError)
		}
	}
	if mock.updateCalls != 0 {
		t.Errorf("expected no update without a sizing flag, got %d", mock.updateCalls)
	}
}

func TestClusterAgentResourcesSetStructuredOutputStaysParseable(t *testing.T) {
	mock := &agentResourcesMock{resources: newAgentResources()}

	output, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set",
		"--memory-limit", "2Gi", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded client.AgentResources
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded.Message != agentResourcesAppliedMessage {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestClusterAgentResourcesRefusalsReachTheUserUnchanged(t *testing.T) {
	t.Run("403 keeps the permission and exits 7", func(t *testing.T) {
		mock := &agentResourcesMock{updateError: &client.PermissionDeniedError{Permission: "agents.manage"}}

		_, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set",
			"--memory-limit", "2Gi")
		if runError == nil {
			t.Fatal("expected the RBAC refusal to fail the command")
		}
		if exitCode := exitCodeFor(runError); exitCode != exitForbidden {
			t.Errorf("exit code = %d, want %d (RBAC)", exitCode, exitForbidden)
		}
		if !strings.Contains(runError.Error(), `"agents.manage"`) {
			t.Errorf("error should name the permission, got %v", runError)
		}
	})

	t.Run("404 exits 3", func(t *testing.T) {
		mock := &agentResourcesMock{getError: client.NewUnexpectedResponseError(404,
			"cluster not found in this organisation")}

		_, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "get")
		if runError == nil {
			t.Fatal("expected the not-found refusal to fail the command")
		}
		if exitCode := exitCodeFor(runError); exitCode != exitNotFound {
			t.Errorf("exit code = %d, want %d (not found)", exitCode, exitNotFound)
		}
		if runError.Error() != "cluster not found in this organisation" {
			t.Errorf("error = %q, want the server's wording unchanged", runError.Error())
		}
	})

	t.Run("422 keeps the validation wording", func(t *testing.T) {
		mock := &agentResourcesMock{updateError: errors.New("memory_limit \"lots\" is not a Kubernetes quantity")}

		_, runError := runAgentResourcesCommand(t, mock, "cluster", "agent", "resources", "set",
			"--memory-limit", "lots")
		if runError == nil {
			t.Fatal("expected the validation refusal to fail the command")
		}
		if runError.Error() != "memory_limit \"lots\" is not a Kubernetes quantity" {
			t.Errorf("error = %q, want the server's wording unchanged", runError.Error())
		}
	})
}
