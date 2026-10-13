package cmd

// Tests for `ankra cluster agent ci set --build-apparmor-profile` and the
// lines `get` prints (ankra-hbxi9.27.1.3): the name is validated before
// anything is sent, an empty value is sent as an explicit clear, a write naming
// only it applies to the next dispatched build rather than re-rendering the
// release, and a platform that predates the setting is never read as "none".

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIBuildAppArmorSettings(profile string, isSupported bool) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.CIBuildAppArmorProfile = &profile
	settings.SupportsBuildAppArmorProfile = &isSupported
	return settings
}

func TestClusterAgentCISetSendsOnlyTheBuildAppArmorProfile(t *testing.T) {
	mock := &agentCIMock{settings: agentCIBuildAppArmorSettings("ankra-rootless-buildkit", true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--build-apparmor-profile", " ankra-rootless-buildkit ")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIBuildAppArmorProfile == nil || *update.CIBuildAppArmorProfile != "ankra-rootless-buildkit" {
		t.Fatalf("profile sent = %v, want ankra-rootless-buildkit", update.CIBuildAppArmorProfile)
	}
	if update.CIWorkerCount != nil || update.CIStorageClass != nil || update.CIPlacement != nil ||
		update.ClearPlacement || update.CIRunReservation != nil || update.CIImagePrepull != nil {
		t.Errorf("a profile-only write sent other settings: %+v", update)
	}
	for _, expected := range []string{
		"Build AppArmor:        ankra-rootless-buildkit",
		"AppArmor support:      advertised",
		agentCIPayloadOnlySentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
}

func TestClusterAgentCISetSendsAnEmptyBuildAppArmorProfileAsAClear(t *testing.T) {
	mock := &agentCIMock{settings: agentCIBuildAppArmorSettings("", true)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--build-apparmor-profile", "")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.CIBuildAppArmorProfile == nil || *mock.lastUpdate.CIBuildAppArmorProfile != "" {
		t.Fatalf("an empty profile must be sent as a clear, got %v", mock.lastUpdate.CIBuildAppArmorProfile)
	}
	body, marshalError := mock.lastUpdate.MarshalJSON()
	if marshalError != nil || !strings.Contains(string(body), `"ci_build_apparmor_profile":""`) {
		t.Fatalf("body = %s (%v), want the member sent empty", body, marshalError)
	}
	if !strings.Contains(output, "Build AppArmor:        (runtime default)") {
		t.Errorf("a cleared profile is not shown as the runtime default:\n%s", output)
	}
}

func TestClusterAgentCISetRefusesAMalformedBuildAppArmorProfile(t *testing.T) {
	for _, value := range []string{"unconfined", "Ankra", "a..b", "a.-b", "-x", "localhost/x", "has space"} {
		t.Run(value, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			_, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
				"--build-apparmor-profile", value)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", runError)
			}
			if mock.updateCalls != 0 {
				t.Errorf("a malformed profile must send nothing, sent %d", mock.updateCalls)
			}
		})
	}
}

// A platform older than the setting drops a member it does not know when
// another one is named, stores the rest, and answers without
// ci_build_apparmor_profile. The command must not exit 0 as if it were set.
func TestClusterAgentCISetFailsWhenThePlatformDidNotStoreTheBuildAppArmorProfile(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--workers", "2",
		"--build-apparmor-profile", "ankra-rootless-buildkit"})
	runError := rootCmd.Execute()
	if runError == nil || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v, want a runtime error", runError)
	}
	if !strings.Contains(runError.Error(), `did not store --build-apparmor-profile "ankra-rootless-buildkit"`) {
		t.Errorf("error = %v", runError)
	}
}

func TestClusterAgentCIGetPrintsTheBuildAppArmorProfile(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"named and supported": {
			settings:  agentCIBuildAppArmorSettings("ankra-rootless-buildkit", true),
			expected:  []string{"Build AppArmor:        ankra-rootless-buildkit", "AppArmor support:      advertised"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"named without agent support": {
			settings: agentCIBuildAppArmorSettings("ankra-rootless-buildkit", false),
			expected: []string{"AppArmor support:      not advertised",
				"A build AppArmor profile is named, but this cluster's agent does not advertise it"},
		},
		"named with support not reported": {
			settings: func() *client.AgentCISettings {
				settings := newAgentCISettings()
				profile := "ankra-rootless-buildkit"
				settings.CIBuildAppArmorProfile = &profile
				return settings
			}(),
			expected:  []string{"Build AppArmor:        ankra-rootless-buildkit", "AppArmor support:      not reported"},
			forbidden: []string{"AppArmor support:      advertised", "AppArmor support:      not advertised"},
		},
		"none": {
			settings:  agentCIBuildAppArmorSettings("", false),
			expected:  []string{"Build AppArmor:        (runtime default)"},
			forbidden: []string{"until the agent is upgraded"},
		},
		"older platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Build AppArmor", "AppArmor support"},
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
