package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// agentCIRunWorkspacesSettings is a settings read that carries the
// platform's answer about where a new run's workspace is kept.
func agentCIRunWorkspacesSettings(verdict string, effective string, isSupported bool) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.CIStorageClass = ""
	if verdict == client.AgentCINodeLocalStorageStorageClassSet {
		settings.CIStorageClass = effective
	}
	settings.CINodeLocalStorage = &verdict
	settings.CIStorageClassEffective = &effective
	settings.SupportsNodeLocalStorage = &isSupported
	return settings
}

// Every verdict the platform gives reads in plain words on one line, and the
// two that are not a StorageClass are followed by what they mean and the way
// out. A platform that sends no answer prints no line.
func TestClusterAgentCIGetSaysWhereARunWorkspaceIsKept(t *testing.T) {
	const upgrade = "Upgrade the agent (ankra cluster agent upgrade)"
	const setClass = "ankra cluster agent ci set --storage-class <class>"
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"on the nodes' disks": {
			settings: agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageInEffect, "ankra-ci-node-local", true),
			expected: []string{
				"Run workspaces:        the disk of each run's node (StorageClass ankra-ci-node-local)",
				"Node-local storage:    advertised",
				"keeps each run's workspace as a directory on the disk of the node the run is placed on",
				setClass,
			},
			forbidden: []string{upgrade},
		},
		"no volume and an agent that cannot use the nodes' disks": {
			settings: agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageAgentUnsupported, "", false),
			expected: []string{
				"Run workspaces:        no volume: nothing on the cluster creates one",
				"Node-local storage:    not advertised",
				"a run's workspace gets no volume and the run fails",
				upgrade, setClass,
			},
		},
		"the class set for pipelines": {
			settings:  agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageStorageClassSet, "ceph-rbd", true),
			expected:  []string{"Run workspaces:        StorageClass ceph-rbd"},
			forbidden: []string{upgrade, setClass},
		},
		"the cluster's default class": {
			settings:  agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageDefaultStorageClass, "", false),
			expected:  []string{"Run workspaces:        the cluster's default StorageClass"},
			forbidden: []string{upgrade, setClass},
		},
		"StorageClasses not read yet": {
			settings: agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageUnknown, "", true),
			expected: []string{
				"Run workspaces:        not known: the cluster's StorageClasses have not been read yet"},
			forbidden: []string{upgrade, setClass},
		},
		"a verdict this build does not know": {
			settings:  agentCIRunWorkspacesSettings("held_for_review", "", true),
			expected:  []string{"Run workspaces:        held_for_review"},
			forbidden: []string{upgrade, setClass},
		},
		"no answer from the platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Run workspaces", "Node-local storage", upgrade, setClass},
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

// The platform may answer the capability without the verdict (it could not
// read the cluster's StorageClasses): the capability line is still printed,
// and nothing is said about where workspaces are kept.
func TestClusterAgentCIGetPrintsTheCapabilityWithoutAVerdict(t *testing.T) {
	settings := newAgentCISettings()
	isSupported := true
	settings.SupportsNodeLocalStorage = &isSupported
	output, runError := runAgentCICommand(t, &agentCIMock{settings: settings}, "cluster", "agent", "ci", "get")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	if !strings.Contains(output, "Node-local storage:    advertised") || strings.Contains(output, "Run workspaces") {
		t.Errorf("output:\n%s", output)
	}
}

func TestClusterAgentCIGetStructuredOutputCarriesTheRunWorkspaceAnswer(t *testing.T) {
	output, runError := runAgentCICommand(t,
		&agentCIMock{settings: agentCIRunWorkspacesSettings(client.AgentCINodeLocalStorageInEffect,
			"ankra-ci-node-local", true)},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if decoded["ci_node_local_storage"] != "in_effect" ||
		decoded["ci_storage_class_effective"] != "ankra-ci-node-local" ||
		decoded["supports_node_local_storage"] != true {
		t.Errorf("decoded = %v", decoded)
	}
	older, olderError := runAgentCICommand(t, &agentCIMock{settings: newAgentCISettings()},
		"cluster", "agent", "ci", "get", "-o", "json")
	if olderError != nil {
		t.Fatalf("get: %v", olderError)
	}
	for _, key := range []string{"ci_node_local_storage", "ci_storage_class_effective", "supports_node_local_storage"} {
		if strings.Contains(older, key) {
			t.Errorf("a platform that sends no answer still prints %q:\n%s", key, older)
		}
	}
}
