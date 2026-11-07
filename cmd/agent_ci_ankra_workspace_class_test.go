package cmd

// Tests for `ankra cluster agent ci set --ankra-workspace-storage-class` and the
// line `get` prints (ankra-q573dh.79.23): the flag is sent only when named, a
// write naming only it is not reported as a step payload or an agent re-render,
// an inherited class names what it resolves to, and a platform that predates the
// setting is never read as "inherits" or as having stored it.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func agentCIAnkraWorkspaceClassSettings(storageClass string) *client.AgentCISettings {
	settings := newAgentCISettings()
	settings.AnkraWorkspaceStorageClass = &storageClass
	return settings
}

func TestClusterAgentCISetSendsOnlyTheAnkraWorkspaceClass(t *testing.T) {
	mock := &agentCIMock{settings: agentCIAnkraWorkspaceClassSettings("ankra-ci-disposable")}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--ankra-workspace-storage-class", "ankra-ci-disposable")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.AnkraWorkspaceStorageClass == nil || *update.AnkraWorkspaceStorageClass != "ankra-ci-disposable" {
		t.Fatalf("workspace class sent = %v, want ankra-ci-disposable", update.AnkraWorkspaceStorageClass)
	}
	if update.NamesChartSetting() || update.NamesStepPayloadSetting() {
		t.Errorf("a workspace-class-only write sent other settings: %+v", update)
	}
	for _, expected := range []string{
		"Ankra Workspace class: ankra-ci-disposable",
		agentCIAnkraWorkspaceStorageClassSentence,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
	// The write published nothing to the agent and rides no step, so either
	// outcome sentence would describe something that did not happen.
	for _, forbidden := range []string{"re-rendering", agentCIPayloadOnlySentence} {
		if strings.Contains(output, forbidden) {
			t.Errorf("a workspace-class-only write is reported with %q:\n%s", forbidden, output)
		}
	}
}

// An empty value is how the class goes back to inheriting, so it has to reach
// the platform rather than be read as the flag not being named.
func TestClusterAgentCISetSendsAnEmptyAnkraWorkspaceClass(t *testing.T) {
	mock := &agentCIMock{settings: agentCIAnkraWorkspaceClassSettings("")}
	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--ankra-workspace-storage-class", ""); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.AnkraWorkspaceStorageClass == nil || *mock.lastUpdate.AnkraWorkspaceStorageClass != "" {
		t.Fatalf("workspace class sent = %v, want an explicit empty value", mock.lastUpdate.AnkraWorkspaceStorageClass)
	}
}

// Moving the storage class must not restate the workspace class, or the step
// that pins the workspaces first would be undone by the step that follows it.
func TestClusterAgentCISetLeavesTheAnkraWorkspaceClassOutWhenNotNamed(t *testing.T) {
	mock := &agentCIMock{settings: agentCIAnkraWorkspaceClassSettings("ankra-ci-disposable")}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--storage-class", "ankra-ci-local")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if mock.lastUpdate.AnkraWorkspaceStorageClass != nil {
		t.Errorf("workspace class sent = %q, want none", *mock.lastUpdate.AnkraWorkspaceStorageClass)
	}
	if mock.lastUpdate.CIStorageClass == nil || *mock.lastUpdate.CIStorageClass != "ankra-ci-local" {
		t.Errorf("storage class sent = %v", mock.lastUpdate.CIStorageClass)
	}
	if strings.Contains(output, agentCIAnkraWorkspaceStorageClassSentence) {
		t.Errorf("a write that did not name the workspace class explains it:\n%s", output)
	}
	if !strings.Contains(output, "The agent is re-rendering its release") {
		t.Errorf("a write naming the storage class keeps the apply state sentence:\n%s", output)
	}
}

func TestClusterAgentCISetKeepsThePayloadSentenceBesideTheAnkraWorkspaceClass(t *testing.T) {
	mock := &agentCIMock{settings: agentCIAnkraWorkspaceClassSettings("ankra-ci-disposable")}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--ankra-workspace-storage-class", "ankra-ci-disposable", "--node-group", "pipelines")
	if runError != nil {
		t.Fatalf("set: %v", runError)
	}
	for _, expected := range []string{agentCIPayloadOnlySentence, agentCIAnkraWorkspaceStorageClassSentence} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
}

// A platform older than the setting drops a member it does not know when another
// one is named, stores the rest, and answers without it. Exiting 0 would tell an
// operator the workspaces are pinned, and the storage class flip that follows
// would then replace every one of them.
func TestClusterAgentCISetFailsWhenThePlatformDidNotStoreTheAnkraWorkspaceClass(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentCICommands(t)...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "agent", "ci", "set", "--workers", "2",
		"--ankra-workspace-storage-class", "ankra-ci-disposable"})
	runError := rootCmd.Execute()
	if runError == nil || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v, want a runtime error", runError)
	}
	if !strings.Contains(runError.Error(), `did not store --ankra-workspace-storage-class "ankra-ci-disposable"`) {
		t.Errorf("error = %v", runError)
	}
	if !strings.Contains(stdout.String(), "Pipeline-step workers: 2") {
		t.Errorf("what was stored is still shown:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), agentCIAnkraWorkspaceStorageClassSentence) {
		t.Errorf("a class that was not stored is explained as if it were:\n%s", stdout.String())
	}
}

// The structured answer still has to print before the failure, so a script gets
// what was stored, on stdout, and the exit code says the class was not.
func TestClusterAgentCISetStructuredFailsWhenThePlatformDidNotStoreTheAnkraWorkspaceClass(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "2",
		"--ankra-workspace-storage-class", "ankra-ci-disposable", "-o", "json")
	if runError == nil || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v, want a runtime error", runError)
	}
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON document in the output:\n%s", output)
	}
	var decoded map[string]any
	if decodeError := json.NewDecoder(strings.NewReader(output[jsonStart:])).Decode(&decoded); decodeError != nil {
		t.Fatalf("stdout does not carry parseable JSON (%v):\n%s", decodeError, output)
	}
	if _, isPresent := decoded["ankra_workspace_storage_class"]; isPresent {
		t.Errorf("a class the platform did not report appears in the document: %v", decoded)
	}
}

func TestClusterAgentCIGetPrintsTheAnkraWorkspaceClass(t *testing.T) {
	inheritsTheClusterDefault := agentCIAnkraWorkspaceClassSettings("")
	inheritsTheClusterDefault.CIStorageClass = ""
	for name, testCase := range map[string]struct {
		settings  *client.AgentCISettings
		expected  []string
		forbidden []string
	}{
		"named": {
			settings:  agentCIAnkraWorkspaceClassSettings("ankra-ci-disposable"),
			expected:  []string{"Ankra Workspace class: ankra-ci-disposable"},
			forbidden: []string{"inherited"},
		},
		"inherits a named storage class": {
			settings: agentCIAnkraWorkspaceClassSettings(""),
			expected: []string{"Ankra Workspace class: proxmox-csi (inherited from the storage class)"},
		},
		"inherits the cluster default": {
			settings: inheritsTheClusterDefault,
			expected: []string{"Ankra Workspace class: cluster default (inherited from the storage class)"},
		},
		"older platform": {
			settings:  newAgentCISettings(),
			forbidden: []string{"Ankra Workspace class", "inherited"},
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
			// A read describes the stored class; what a change of it does is a
			// statement about a write.
			if strings.Contains(output, agentCIAnkraWorkspaceStorageClassSentence) {
				t.Errorf("a read carries the write's sentence:\n%s", output)
			}
		})
	}
}

func TestClusterAgentCIGetStructuredOutputCarriesTheAnkraWorkspaceClass(t *testing.T) {
	output, runError := runAgentCICommand(t, &agentCIMock{settings: agentCIAnkraWorkspaceClassSettings("")},
		"cluster", "agent", "ci", "get", "-o", "json")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not parseable JSON (%v):\n%s", decodeError, output)
	}
	if value, isPresent := decoded["ankra_workspace_storage_class"]; !isPresent || value != "" {
		t.Errorf("an inheriting cluster has to say so in the document: %v", decoded)
	}
}

// The help is where an operator reads what --storage-class is about to do to
// the workspaces, so the two consequences are pinned.
func TestClusterAgentCISetHelpSaysWhatTheStorageClassReaches(t *testing.T) {
	for _, candidate := range agentCICommands(t) {
		if candidate.Name() != "set" {
			continue
		}
		usage := candidate.Flags().Lookup("storage-class").Usage
		for _, expected := range []string{"at the next agent re-render", "are replaced on next use"} {
			if !strings.Contains(usage, expected) {
				t.Errorf("--storage-class help is missing %q: %s", expected, usage)
			}
		}
		if candidate.Flags().Lookup("ankra-workspace-storage-class") == nil {
			t.Error("--ankra-workspace-storage-class is not registered")
		}
		return
	}
	t.Fatal("the 'cluster agent ci set' command is not registered")
}
