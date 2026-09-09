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

type agentCIMock struct {
	baseMock
	settings    *client.AgentCISettings
	getError    error
	updateError error

	getCalls      int
	updateCalls   int
	lastClusterID string
	lastUpdate    client.AgentCISettingsUpdate
}

func (mock *agentCIMock) GetAgentCISettings(requestContext context.Context,
	clusterID string) (*client.AgentCISettings, error) {
	mock.getCalls++
	mock.lastClusterID = clusterID
	if mock.getError != nil {
		return nil, mock.getError
	}
	return mock.settings, nil
}

func (mock *agentCIMock) UpdateAgentCISettings(requestContext context.Context, clusterID string,
	update client.AgentCISettingsUpdate) (*client.AgentCISettings, error) {
	mock.updateCalls++
	mock.lastClusterID = clusterID
	mock.lastUpdate = update
	if mock.updateError != nil {
		return nil, mock.updateError
	}
	return mock.settings, nil
}

// agentCICommands collects the `cluster agent ci` tree so a test can put its
// flags back: the commands are built once in init and shared by the whole
// package, so a --workers left set would leak into the next test.
func agentCICommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterAgentCmd.Commands() {
		if candidate.Name() == "ci" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster agent ci' command is not registered")
	return nil
}

func runAgentCICommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	return executeCommand(arguments...)
}

func newAgentCISettings() *client.AgentCISettings {
	updatedAt := "2026-09-06T10:00:00Z"
	return &client.AgentCISettings{
		CIWorkerCount:         2,
		CIStorageClass:        "proxmox-csi",
		AgentVersion:          "2.1.1107",
		SupportsPipelineSteps: true,
		ApplyState:            client.AgentCIApplyStateApplied,
		UpdatedAt:             &updatedAt,
	}
}

func TestClusterAgentCIGetPrintsTheSettingsAndTheApplyState(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}

	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.getCalls != 1 || mock.lastClusterID != testClusterID {
		t.Fatalf("expected one read of the selected cluster, got %d for %q", mock.getCalls, mock.lastClusterID)
	}
	for _, want := range []string{
		"Agent CI settings for cluster 'test-cluster'",
		"Pipeline-step workers: 2",
		"Storage class:         proxmox-csi",
		"Agent version:         2.1.1107",
		"Pipeline steps:        advertised",
		"The agent is re-rendering its release with 2 pipeline-step workers; " +
			"it advertises the capability on its next check-in.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// The two "not set" states have to read as defaults rather than as failures:
// an empty storage class is the cluster's own default class, and an agent
// that has not re-rendered its release yet simply has not advertised the
// capability.
func TestClusterAgentCIGetNamesTheDefaultsAndTheMissingCapability(t *testing.T) {
	mock := &agentCIMock{settings: &client.AgentCISettings{
		CIWorkerCount: 0,
		AgentVersion:  "2.1.900",
		ApplyState:    client.AgentCIApplyStatePendingUpgrade,
	}}

	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	for _, want := range []string{
		"Storage class:         cluster default",
		"Pipeline steps:        not advertised",
		"Last changed:          never",
		"The agent on this cluster predates chart values; " +
			"the setting is stored and takes effect with the next agent upgrade.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterAgentCIGetStructuredOutputStaysParseable(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}

	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded client.AgentCISettings
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded.CIWorkerCount != 2 || decoded.ApplyState != client.AgentCIApplyStateApplied {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestClusterAgentCISetSendsTheWorkerCountAndReportsTheApplyState(t *testing.T) {
	cases := []struct {
		name         string
		applyState   string
		workerCount  int
		wantSentence string
	}{
		{
			name:        "applied",
			applyState:  client.AgentCIApplyStateApplied,
			workerCount: 4,
			wantSentence: "The agent is re-rendering its release with 4 pipeline-step workers; " +
				"it advertises the capability on its next check-in.",
		},
		{
			name:        "pending upgrade",
			applyState:  client.AgentCIApplyStatePendingUpgrade,
			workerCount: 4,
			wantSentence: "The agent on this cluster predates chart values; " +
				"the setting is stored and takes effect with the next agent upgrade.",
		},
		{
			name:        "agent offline",
			applyState:  client.AgentCIApplyStateAgentOffline,
			workerCount: 4,
			wantSentence: "The agent on this cluster is offline; " +
				"the setting is stored and applies when it reconnects and is upgraded.",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &agentCIMock{settings: &client.AgentCISettings{
				CIWorkerCount: testCase.workerCount,
				AgentVersion:  "2.1.1107",
				ApplyState:    testCase.applyState,
			}}

			output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "4")
			if runError != nil {
				t.Fatalf("expected success, got %v", runError)
			}
			if mock.updateCalls != 1 {
				t.Fatalf("expected one update, got %d", mock.updateCalls)
			}
			if mock.lastUpdate.CIWorkerCount == nil || *mock.lastUpdate.CIWorkerCount != 4 {
				t.Errorf("worker count sent = %v, want 4", mock.lastUpdate.CIWorkerCount)
			}
			if !strings.Contains(output, "Agent CI settings updated for cluster 'test-cluster'.") {
				t.Errorf("output missing the confirmation:\n%s", output)
			}
			if !strings.Contains(output, testCase.wantSentence) {
				t.Errorf("output missing %q:\n%s", testCase.wantSentence, output)
			}
		})
	}
}

// A worker-count change must not carry a storage class the caller never
// named: the platform would take it as a write and reset one someone set
// earlier.
func TestClusterAgentCISetOnlySendsTheStorageClassWhenNamed(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "2"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.lastUpdate.CIStorageClass != nil {
		t.Errorf("storage class sent as %q when the flag was not passed", *mock.lastUpdate.CIStorageClass)
	}

	named := &agentCIMock{settings: newAgentCISettings()}
	if _, runError := runAgentCICommand(t, named, "cluster", "agent", "ci", "set",
		"--workers", "2", "--storage-class", "proxmox-csi"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if named.lastUpdate.CIStorageClass == nil || *named.lastUpdate.CIStorageClass != "proxmox-csi" {
		t.Errorf("storage class sent = %v, want proxmox-csi", named.lastUpdate.CIStorageClass)
	}
}

// Zero workers is a real setting (it disables the agent's pipeline-step
// scheduler), so it has to reach the platform rather than reading as
// "nothing to send".
func TestClusterAgentCISetSendsZeroWorkers(t *testing.T) {
	mock := &agentCIMock{settings: &client.AgentCISettings{ApplyState: client.AgentCIApplyStateApplied}}

	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "0"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.lastUpdate.CIWorkerCount == nil || *mock.lastUpdate.CIWorkerCount != 0 {
		t.Errorf("worker count sent = %v, want an explicit 0", mock.lastUpdate.CIWorkerCount)
	}
}

func TestClusterAgentCISetWithoutWorkersIsAUsageError(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}

	_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set")
	if runError == nil {
		t.Fatal("expected --workers to be required")
	}
	if exitCode := exitCodeFor(runError); exitCode != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCode, exitUsage)
	}
	if !strings.Contains(runError.Error(), "workers") {
		t.Errorf("error should name the missing flag, got %v", runError)
	}
	if mock.updateCalls != 0 {
		t.Errorf("expected no update without --workers, got %d", mock.updateCalls)
	}
}

func TestClusterAgentCIRefusalsReachTheUserUnchanged(t *testing.T) {
	t.Run("403 keeps the permission and exits 7", func(t *testing.T) {
		mock := &agentCIMock{updateError: &client.PermissionDeniedError{Permission: "agents.manage"}}

		_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "2")
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
		mock := &agentCIMock{getError: client.NewUnexpectedResponseError(404, "cluster not found in this organisation")}

		_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "get")
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
		mock := &agentCIMock{updateError: errors.New("ci_worker_count must be between 0 and 32")}

		_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "99")
		if runError == nil {
			t.Fatal("expected the validation refusal to fail the command")
		}
		if runError.Error() != "ci_worker_count must be between 0 and 32" {
			t.Errorf("error = %q, want the server's wording unchanged", runError.Error())
		}
	})
}

// An apply state a newer platform introduces is reported as itself rather
// than disappearing, so the operator can see that something was stored even
// when this build has no sentence for it.
func TestAgentCIApplyStateSentenceHandlesUnknownAndAbsentStates(t *testing.T) {
	if sentence := agentCIApplyStateSentence(&client.AgentCISettings{ApplyState: ""}); sentence != "" {
		t.Errorf("absent apply state should print nothing, got %q", sentence)
	}
	sentence := agentCIApplyStateSentence(&client.AgentCISettings{ApplyState: "queued_behind_upgrade"})
	if !strings.Contains(sentence, "queued_behind_upgrade") {
		t.Errorf("an unknown apply state should be reported as itself, got %q", sentence)
	}
}
