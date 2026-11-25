package cmd

// Tests for the reserved build AppArmor value ankra-managed (ankra-xf6eot.3):
// it is sent as is, get names the profile the agent loads and on how many nodes
// it is loaded, the nodes it is not loaded on are listed with the agent's reason,
// and an agent or platform that cannot honour it is said out loud, on stderr
// for structured output.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIBuildAppArmorManagedSettings(isManagedSupported *bool,
	status *client.AgentCIBuildAppArmorManagedStatus) *client.AgentCISettings {
	settings := agentCIBuildAppArmorSettings(client.AgentCIBuildAppArmorManaged, true)
	settings.SupportsBuildAppArmorManaged = isManagedSupported
	settings.BuildAppArmorManagedStatus = status
	return settings
}

func agentCIBuildAppArmorManagedBool(value bool) *bool {
	return &value
}

func agentCIBuildAppArmorManagedReport(nodesDesired int, nodesLoaded int,
	nodes ...client.AgentCIBuildAppArmorManagedNode) *client.AgentCIBuildAppArmorManagedStatus {
	return &client.AgentCIBuildAppArmorManagedStatus{
		Profile:        client.AgentCIBuildAppArmorManagedProfile,
		ProfileVersion: "3f2a9c41b7d0",
		NodesDesired:   nodesDesired,
		NodesLoaded:    nodesLoaded,
		Nodes:          nodes,
		ReportedAt:     "2026-11-25T09:00:00Z",
	}
}

func TestClusterAgentCISetSendsAnkraManagedAsTheBuildAppArmorProfile(t *testing.T) {
	mock := &agentCIMock{settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true), nil)}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--build-apparmor-profile", "ankra-managed")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIBuildAppArmorProfile == nil || *update.CIBuildAppArmorProfile != client.AgentCIBuildAppArmorManaged {
		t.Fatalf("profile sent = %v, want ankra-managed", update.CIBuildAppArmorProfile)
	}
	for _, expected := range []string{
		"Build AppArmor:        ankra-managed (ankra-managed-rootless-buildkit)",
		"Managed AppArmor:      advertised",
		"Profile loaded on:     not reported yet",
		agentCIPayloadOnlySentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "does not advertise") {
		t.Errorf("a supported agent is warned about:\n%s", output)
	}
}

func TestClusterAgentCIGetPrintsTheManagedBuildAppArmorProfile(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"loaded on some nodes": {
			settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true),
				agentCIBuildAppArmorManagedReport(3, 1,
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-1", Loaded: true},
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-2", Message: "apparmor_parser not found"},
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-3"})),
			expected: []string{
				"Build AppArmor:        ankra-managed (ankra-managed-rootless-buildkit 3f2a9c41b7d0)",
				"AppArmor support:      advertised",
				"Managed AppArmor:      advertised",
				"Profile loaded on:     1 of 3 nodes (last build's report, ",
				"\n" + agentCIBuildAppArmorManagedNodeIndent + "ci-2: apparmor_parser not found\n",
				"\n" + agentCIBuildAppArmorManagedNodeIndent + "ci-3: not confirmed loaded (no reason reported)\n",
			},
			forbidden: []string{"ci-1:", "more not confirmed loaded", "profile loaded on no node", "does not advertise"},
		},
		"loaded on every node": {
			settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true),
				agentCIBuildAppArmorManagedReport(2, 2,
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-1", Loaded: true},
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-2", Loaded: true})),
			expected:  []string{"Profile loaded on:     2 of 2 nodes"},
			forbidden: []string{"ci-1:", "ci-2:", "profile loaded on no node"},
		},
		"node list capped by the agent": {
			settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true),
				agentCIBuildAppArmorManagedReport(70, 0,
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-1", Message: "loader pod pending"})),
			expected: []string{
				"Profile loaded on:     0 of 70 nodes",
				agentCIBuildAppArmorManagedNodeIndent + "ci-1: loader pod pending",
				agentCIBuildAppArmorManagedNodeIndent + "and 69 more not confirmed loaded",
				"found the managed AppArmor profile loaded on no node: until it is loaded on a node builds can run " +
					"on, builds wait",
				"the next build reports again",
			},
		},
		"last build found no node builds can run on": {
			settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true),
				agentCIBuildAppArmorManagedReport(0, 0)),
			expected: []string{
				"Profile loaded on:     unknown: the last build found no node builds can run on (last build's report, ",
			},
			forbidden: []string{"0 of 0 nodes", "profile loaded on no node", "more not confirmed loaded"},
		},
		"report without a time": {
			settings: func() *client.AgentCISettings {
				report := agentCIBuildAppArmorManagedReport(1, 1,
					client.AgentCIBuildAppArmorManagedNode{Name: "ci-1", Loaded: true})
				report.ReportedAt = ""
				return agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true), report)
			}(),
			expected: []string{"Profile loaded on:     1 of 1 nodes (last build's report)\n"},
		},
		"agent without managed support": {
			settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(false), nil),
			expected: []string{
				"Managed AppArmor:      not advertised",
				"this cluster's agent does not advertise managed AppArmor support",
			},
			forbidden: []string{"A build AppArmor profile is named"},
		},
		"agent without the build profile capability": {
			settings: func() *client.AgentCISettings {
				settings := agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(false), nil)
				isSupported := false
				settings.SupportsBuildAppArmorProfile = &isSupported
				return settings
			}(),
			expected:  []string{"AppArmor support:      not advertised", "does not advertise managed AppArmor support"},
			forbidden: []string{"A build AppArmor profile is named"},
		},
		"platform older than managed support": {
			settings: agentCIBuildAppArmorManagedSettings(nil, nil),
			expected: []string{
				"Managed AppArmor:      not reported",
				"the platform does not report managed AppArmor support",
			},
		},
		"neither capability reported": {
			settings: func() *client.AgentCISettings {
				settings := agentCIBuildAppArmorManagedSettings(nil, nil)
				settings.SupportsBuildAppArmorProfile = nil
				return settings
			}(),
			expected: []string{
				"AppArmor support:      not reported",
				"Managed AppArmor:      not reported",
				"managed AppArmor support is not confirmed: neither the platform nor an agent reports it",
			},
			forbidden: []string{"the platform does not report managed AppArmor support:"},
		},
		"operator-installed profile": {
			settings:  agentCIBuildAppArmorSettings("ankra-rootless-buildkit", true),
			expected:  []string{"Build AppArmor:        ankra-rootless-buildkit\n"},
			forbidden: []string{"Managed AppArmor", "Profile loaded on"},
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

func TestClusterAgentCIGetStructuredOutputCarriesTheManagedBuildAppArmorStatus(t *testing.T) {
	settings := agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true),
		agentCIBuildAppArmorManagedReport(2, 1,
			client.AgentCIBuildAppArmorManagedNode{Name: "ci-1", Loaded: true},
			client.AgentCIBuildAppArmorManagedNode{Name: "ci-2", Message: "not enforced"}))
	output, runError := runAgentCICommand(t, &agentCIMock{settings: settings},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded struct {
		Profile         string                                    `json:"ci_build_apparmor_profile"`
		SupportsManaged *bool                                     `json:"supports_build_apparmor_managed"`
		ManagedStatus   *client.AgentCIBuildAppArmorManagedStatus `json:"build_apparmor_managed_status"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded.Profile != client.AgentCIBuildAppArmorManaged || decoded.SupportsManaged == nil ||
		!*decoded.SupportsManaged {
		t.Errorf("decoded = %+v", decoded)
	}
	status := decoded.ManagedStatus
	if status == nil || status.ProfileVersion != "3f2a9c41b7d0" || status.NodesDesired != 2 ||
		status.NodesLoaded != 1 || len(status.Nodes) != 2 || status.Nodes[1].Message != "not enforced" ||
		status.ReportedAt != "2026-11-25T09:00:00Z" {
		t.Errorf("status = %+v", status)
	}
}

// Structured output keeps stdout parseable, so the warning that the agent
// cannot honour ankra-managed goes to stderr.
func TestClusterAgentCISetStructuredWarnsOnStderrWhenTheAgentCannotManageTheProfile(t *testing.T) {
	mock := &agentCIMock{settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(false), nil)}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--build-apparmor-profile", "ankra-managed",
		"-o", "json"})
	if runError := rootCmd.Execute(); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal(stdout.Bytes(), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, stdout.String())
	}
	if decoded["supports_build_apparmor_managed"] != false {
		t.Errorf("decoded = %v", decoded)
	}
	if !strings.Contains(stderr.String(), "Warning: The build AppArmor profile is ankra-managed, but this "+
		"cluster's agent does not advertise managed AppArmor support") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestClusterAgentCISetStructuredIsQuietWhenTheAgentManagesTheProfile(t *testing.T) {
	mock := &agentCIMock{settings: agentCIBuildAppArmorManagedSettings(agentCIBuildAppArmorManagedBool(true), nil)}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--build-apparmor-profile", "ankra-managed",
		"-o", "json"})
	if runError := rootCmd.Execute(); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}
