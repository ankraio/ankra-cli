package cmd

// Tests for `ankra cluster agent ci set --step-workspaces` and the step
// workspaces lines `get` prints (ankra-q573dh.79.17): the mode is validated
// before anything is sent, a write naming only it is reported as applying with
// no release re-render and only to runs that have not started, and a platform
// that predates the setting is never read as "off".

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIStepWorkspacesSettings(mode string, isSupported bool) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.CIStepWorkspaces = &mode
	settings.SupportsStepWorkspaces = &isSupported
	return settings
}

func TestClusterAgentCISetSendsOnlyTheStepWorkspaces(t *testing.T) {
	mock := &agentCIMock{settings: agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesShadow, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--step-workspaces", "shadow")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIStepWorkspaces == nil || *update.CIStepWorkspaces != client.AgentCIStepWorkspacesShadow {
		t.Fatalf("step workspaces sent = %v, want shadow", update.CIStepWorkspaces)
	}
	if update.CIWorkerCount != nil || update.CIStorageClass != nil || update.CIPlacement != nil ||
		update.ClearPlacement || update.CIRunReservation != nil || update.CIImagePrepull != nil ||
		update.CIBuildAppArmorProfile != nil {
		t.Errorf("a step-workspaces-only write sent other settings: %+v", update)
	}
	for _, expected := range []string{
		"Step workspaces:       shadow",
		"Workspaces support:    advertised",
		agentCIPayloadOnlySentence,
		agentCIStepWorkspacesFirstStepSentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
	// The write published nothing to the agent, so the apply state's
	// "re-rendering its release" would describe something that did not happen.
	if strings.Contains(output, "re-rendering") {
		t.Errorf("a step-workspaces-only write is reported as a release re-render:\n%s", output)
	}
}

func TestClusterAgentCISetNormalisesTheStepWorkspacesAndKeepsTheApplyStateForChartSettings(t *testing.T) {
	mock := &agentCIMock{settings: agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesOn, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--workers", "2", "--step-workspaces", " ON ")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.CIStepWorkspaces == nil || *mock.lastUpdate.CIStepWorkspaces != client.AgentCIStepWorkspacesOn {
		t.Fatalf("step workspaces sent = %v, want on", mock.lastUpdate.CIStepWorkspaces)
	}
	if !strings.Contains(output, "The agent is re-rendering its release with 2 pipeline-step workers") {
		t.Errorf("a write naming the worker count keeps the apply state sentence:\n%s", output)
	}
}

// A write that names something else says nothing about when a run takes its
// workspace mode, which only a write of the mode has anything to do with.
func TestClusterAgentCISetLeavesOutTheFirstStepSentenceWhenTheModeIsNotNamed(t *testing.T) {
	mock := &agentCIMock{settings: agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesOff, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "2")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.CIStepWorkspaces != nil {
		t.Errorf("step workspaces sent = %v, want none", *mock.lastUpdate.CIStepWorkspaces)
	}
	if strings.Contains(output, agentCIStepWorkspacesFirstStepSentence) {
		t.Errorf("output carries the first-step sentence:\n%s", output)
	}
}

func TestClusterAgentCISetRefusesAnUnknownStepWorkspaces(t *testing.T) {
	for _, value := range []string{"", "per_step", "true", "1"} {
		t.Run(value, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--step-workspaces", value)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", runError)
			}
			if !strings.Contains(runError.Error(), "off, shadow, on") {
				t.Errorf("the refusal names the modes: %v", runError)
			}
			if mock.updateCalls != 0 {
				t.Errorf("an unknown mode must send nothing, sent %d", mock.updateCalls)
			}
		})
	}
}

// A platform older than the setting drops a member it does not know when
// another one is named, stores the rest, and answers without
// ci_step_workspaces. The command must not exit 0 as if the mode were set, and
// must not explain when a mode that was never stored takes effect.
func TestClusterAgentCISetFailsWhenThePlatformDidNotStoreTheStepWorkspaces(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--workers", "2", "--step-workspaces", "on"})
	runError := rootCmd.Execute()
	if runError == nil || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v, want a runtime error", runError)
	}
	if !strings.Contains(runError.Error(), "did not store --step-workspaces on") {
		t.Errorf("error = %v", runError)
	}
	if !strings.Contains(stdout.String(), "Pipeline-step workers: 2") {
		t.Errorf("what was stored is still shown:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), agentCIStepWorkspacesFirstStepSentence) {
		t.Errorf("a mode that was not stored is explained as if it were:\n%s", stdout.String())
	}
}

func TestClusterAgentCIGetPrintsTheStepWorkspaces(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"on and supported": {
			settings:  agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesOn, true),
			expected:  []string{"Step workspaces:       on", "Workspaces support:    advertised"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"on without agent support": {
			settings: agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesOn, false),
			expected: []string{"Step workspaces:       on", "Workspaces support:    not advertised",
				"Step workspaces are on, but this cluster's agent does not advertise them"},
		},
		"shadow without agent support": {
			settings:  agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesShadow, false),
			expected:  []string{"Step workspaces:       shadow", "Workspaces support:    not advertised"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"off": {
			settings:  agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesOff, false),
			expected:  []string{"Step workspaces:       off", "Workspaces support:    not advertised"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"older platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Step workspaces", "Workspaces support"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			output, runError := runAgentCICommand(t, &agentCIMock{settings: testCase.settings},
				"cluster", "agent", "ci", "get")
			if runError != nil {
				t.Fatalf("get: %v", runError)
			}
			for _, expected := range testCase.expected {
				if !strings.Contains(output, expected) {
					t.Errorf("output is missing %q:\n%s", expected, output)
				}
			}
			for _, forbidden := range testCase.forbidden {
				if strings.Contains(output, forbidden) {
					t.Errorf("output carries %q:\n%s", forbidden, output)
				}
			}
			// A read describes the stored mode; when a run takes it is a
			// statement about a write.
			if strings.Contains(output, agentCIStepWorkspacesFirstStepSentence) {
				t.Errorf("a read carries the first-step sentence:\n%s", output)
			}
		})
	}
}

func TestClusterAgentCIGetStructuredOutputCarriesTheStepWorkspaces(t *testing.T) {
	output, runError := runAgentCICommand(t,
		&agentCIMock{settings: agentCIStepWorkspacesSettings(client.AgentCIStepWorkspacesShadow, false)},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded["ci_step_workspaces"] != "shadow" || decoded["supports_step_workspaces"] != false {
		t.Errorf("decoded = %v", decoded)
	}
}
