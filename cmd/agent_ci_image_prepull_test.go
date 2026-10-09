package cmd

// Tests for `ankra cluster agent ci set --image-prepull` and the image
// pre-pull lines `get` prints (ankra-q573dh.7): the mode is validated before
// anything is sent, a write naming only it is reported as applying to the next
// dispatched step rather than as a release re-render, and a platform that
// predates the setting is never read as "off".

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIImagePrepullSettings(mode string, isSupported bool) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.CIImagePrepull = &mode
	settings.SupportsImagePrepull = &isSupported
	return settings
}

func TestClusterAgentCISetSendsOnlyTheImagePrepull(t *testing.T) {
	mock := &agentCIMock{settings: agentCIImagePrepullSettings(client.AgentCIImagePrepullOn, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--image-prepull", " ON ")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIImagePrepull == nil || *update.CIImagePrepull != client.AgentCIImagePrepullOn {
		t.Fatalf("image pre-pull sent = %v, want on", update.CIImagePrepull)
	}
	if update.CIWorkerCount != nil || update.CIStorageClass != nil || update.CIPlacement != nil ||
		update.ClearPlacement || update.CIRunReservation != nil {
		t.Errorf("an image-pre-pull-only write sent other settings: %+v", update)
	}
	for _, expected := range []string{
		"Image pre-pull:        on",
		"Pre-pull support:      advertised",
		agentCIPayloadOnlySentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "re-rendering") {
		t.Errorf("a payload-only write is reported as a release re-render:\n%s", output)
	}
}

func TestClusterAgentCISetRefusesAnUnknownImagePrepull(t *testing.T) {
	for _, value := range []string{"", "shadow", "true", "1"} {
		t.Run(value, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--image-prepull", value)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", runError)
			}
			if !strings.Contains(runError.Error(), "off, on") {
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
// ci_image_prepull. The command must not exit 0 as if the mode were set.
func TestClusterAgentCISetFailsWhenThePlatformDidNotStoreTheImagePrepull(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--workers", "2", "--image-prepull", "on"})
	runError := rootCmd.Execute()
	if runError == nil || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v, want a runtime error", runError)
	}
	if !strings.Contains(runError.Error(), "did not store --image-prepull on") {
		t.Errorf("error = %v", runError)
	}
	if !strings.Contains(stdout.String(), "Pipeline-step workers: 2") {
		t.Errorf("what was stored is still shown:\n%s", stdout.String())
	}
}

func TestClusterAgentCIGetPrintsTheImagePrepull(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"on and supported": {
			settings:  agentCIImagePrepullSettings(client.AgentCIImagePrepullOn, true),
			expected:  []string{"Image pre-pull:        on", "Pre-pull support:      advertised"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"on without agent support": {
			settings: agentCIImagePrepullSettings(client.AgentCIImagePrepullOn, false),
			expected: []string{"Image pre-pull:        on", "Pre-pull support:      not advertised",
				"Image pre-pull is on, but this cluster's agent does not advertise it"},
		},
		"off": {
			settings:  agentCIImagePrepullSettings(client.AgentCIImagePrepullOff, false),
			expected:  []string{"Image pre-pull:        off"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"older platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Image pre-pull", "Pre-pull support"},
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
		})
	}
}

func TestClusterAgentCIGetStructuredOutputCarriesTheImagePrepull(t *testing.T) {
	output, runError := runAgentCICommand(t,
		&agentCIMock{settings: agentCIImagePrepullSettings(client.AgentCIImagePrepullOn, false)},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded["ci_image_prepull"] != "on" || decoded["supports_image_prepull"] != false {
		t.Errorf("decoded = %v", decoded)
	}
}
