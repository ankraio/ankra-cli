package cmd

// Tests for `ankra cluster agent ci set --run-reservation` and the run
// reservation lines `get` prints (ankra-q573dh.3.3): the mode is validated
// before anything is sent, a write naming only it is reported as applying to
// the next dispatched step rather than as a release re-render, and a
// platform that predates the setting is never read as "off".

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIRunReservationSettings(mode string, isSupported bool) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.CIRunReservation = &mode
	settings.SupportsRunReservation = &isSupported
	return settings
}

func TestClusterAgentCISetSendsOnlyTheRunReservation(t *testing.T) {
	mock := &agentCIMock{settings: agentCIRunReservationSettings(client.AgentCIRunReservationShadow, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--run-reservation", "shadow")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIRunReservation == nil || *update.CIRunReservation != client.AgentCIRunReservationShadow {
		t.Fatalf("run reservation sent = %v, want shadow", update.CIRunReservation)
	}
	if update.CIWorkerCount != nil || update.CIStorageClass != nil || update.CIPlacement != nil || update.ClearPlacement {
		t.Errorf("a run-reservation-only write sent other settings: %+v", update)
	}
	for _, expected := range []string{
		"Run reservation:       shadow",
		"Reservation support:   advertised",
		agentCIPayloadOnlySentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
	// The write published nothing to the agent, so the apply state's
	// "re-rendering its release" would describe something that did not
	// happen.
	if strings.Contains(output, "re-rendering") {
		t.Errorf("a payload-only write is reported as a release re-render:\n%s", output)
	}
}

func TestClusterAgentCISetNormalisesTheRunReservationAndKeepsTheApplyStateForChartSettings(t *testing.T) {
	mock := &agentCIMock{settings: agentCIRunReservationSettings(client.AgentCIRunReservationOn, true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--workers", "2", "--run-reservation", " ON ")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.CIRunReservation == nil || *mock.lastUpdate.CIRunReservation != client.AgentCIRunReservationOn {
		t.Fatalf("run reservation sent = %v, want on", mock.lastUpdate.CIRunReservation)
	}
	if !strings.Contains(output, "The agent is re-rendering its release with 2 pipeline-step workers") {
		t.Errorf("a write naming the worker count keeps the apply state sentence:\n%s", output)
	}
}

func TestClusterAgentCISetRefusesAnUnknownRunReservation(t *testing.T) {
	for _, value := range []string{"", "maybe", "true", "1"} {
		t.Run(value, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--run-reservation", value)
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
// ci_run_reservation. The command must not exit 0 as if the mode were set.
func TestClusterAgentCISetFailsWhenThePlatformDidNotStoreTheRunReservation(t *testing.T) {
	for _, format := range []string{"", "json"} {
		t.Run("format="+format, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			arguments := []string{"cluster", "agent", "ci", "set", "--workers", "2", "--run-reservation", "on"}
			if format != "" {
				arguments = append(arguments, "-o", format)
			}
			// stdout and stderr apart: the error is cobra's to print on
			// stderr, and stdout must stay one parseable document.
			writeSelectedClusterJSON(t)
			setMockClient(t, mock)
			t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SetArgs(arguments)
			runError := rootCmd.Execute()
			output := stdout.String()
			if runError == nil || exitCodeFor(runError) != exitError {
				t.Fatalf("error = %v, want a runtime error", runError)
			}
			if !strings.Contains(runError.Error(), "did not store --run-reservation on") {
				t.Errorf("error = %v", runError)
			}
			if format == "json" {
				var decoded client.AgentCISettings
				if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
					t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
				}
			} else if !strings.Contains(output, "Pipeline-step workers: 2") {
				t.Errorf("what was stored is still shown:\n%s", output)
			}
		})
	}
}

func TestClusterAgentCIGetPrintsTheRunReservation(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"on and supported": {
			settings:  agentCIRunReservationSettings(client.AgentCIRunReservationOn, true),
			expected:  []string{"Run reservation:       on", "Reservation support:   advertised"},
			forbidden: []string{"nothing is held"},
		},
		"on without agent support": {
			settings: agentCIRunReservationSettings(client.AgentCIRunReservationOn, false),
			expected: []string{"Run reservation:       on", "Reservation support:   not advertised",
				"Run reservation is on, but this cluster's agent does not advertise it"},
		},
		"off": {
			settings:  agentCIRunReservationSettings(client.AgentCIRunReservationOff, false),
			expected:  []string{"Run reservation:       off", "Reservation support:   not advertised"},
			forbidden: []string{"nothing is held"},
		},
		"older platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Run reservation", "Reservation support"},
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

func TestClusterAgentCIGetStructuredOutputCarriesTheRunReservation(t *testing.T) {
	output, runError := runAgentCICommand(t,
		&agentCIMock{settings: agentCIRunReservationSettings(client.AgentCIRunReservationShadow, false)},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded["ci_run_reservation"] != "shadow" || decoded["supports_run_reservation"] != false {
		t.Errorf("decoded = %v", decoded)
	}
}
