package cmd

// ankra-mz2awi item 2: `ankra cluster <provider> nodes diagnose` is the CLI
// twin of the assistant's read-only ssh_diagnose_node, modelled on
// `bastion diagnose`.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type nodeDiagnoseMock struct {
	baseMock
	result *client.NodeDiagnoseResult
	err    error
	calls  []string
}

func (m *nodeDiagnoseMock) DiagnoseClusterNode(ctx context.Context, provider, clusterID, nodeID string) (*client.NodeDiagnoseResult, error) {
	m.calls = append(m.calls, provider+":"+clusterID+":"+nodeID)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return nil, context.Canceled
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func completedNodeDiagnosis() *client.NodeDiagnoseResult {
	return &client.NodeDiagnoseResult{
		OperationID: "op-7", StepID: "step-7", NodeID: "node-1", JobName: "hetzner_node_diagnose",
		Status: "success", Completed: true,
		Report: map[string]interface{}{"action": "diagnose", "diagnosis": map[string]interface{}{
			"final_outcome": "diagnose_only", "symptoms_detected": []interface{}{"kubelet_not_running"}}},
	}
}

func TestClusterNodesDiagnoseDispatchesThroughTheProvidersRoute(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &nodeDiagnoseMock{result: completedNodeDiagnosis()}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "hetzner", "nodes", "diagnose", testClusterID, "node-1"); err != nil {
			t.Errorf("nodes diagnose: %v", err)
		}
	})

	if len(mock.calls) != 1 || mock.calls[0] != "hetzner:"+testClusterID+":node-1" {
		t.Fatalf("expected one diagnose call for hetzner %s node-1, got %v", testClusterID, mock.calls)
	}
	for _, expected := range []string{"Node diagnosis dispatched (hetzner_node_diagnose)", "op-7", "kubelet_not_running", "diagnose_only"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

func TestClusterNodesDiagnoseStillRunningPointsAtThePoller(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &nodeDiagnoseMock{result: &client.NodeDiagnoseResult{
		OperationID: "op-8", NodeID: "node-1", JobName: "ovh_node_diagnose", Status: "running", Attached: true}})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "nodes", "diagnose", testClusterID, "node-1")
	})

	for _, expected := range []string{"attached to the run already in flight", "still running", "ankra cluster operations list op-8"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

func TestClusterNodesDiagnoseJSONIsTheResult(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &nodeDiagnoseMock{result: completedNodeDiagnosis()})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "nodes", "diagnose", testClusterID, "node-1", "-o", "json")
	})

	var decoded client.NodeDiagnoseResult
	if err := json.Unmarshal([]byte(stdoutOutput), &decoded); err != nil {
		t.Fatalf("-o json must be the result document: %v: %s", err, stdoutOutput)
	}
	if decoded.OperationID != "op-7" || !decoded.Completed || decoded.Report["action"] != "diagnose" {
		t.Errorf("decoded = %+v", decoded)
	}
}

// A job that finished without a report (the node unreachable over SSH)
// must not exit 0: a script would read it as a diagnosis.
func TestClusterNodesDiagnoseFailedJobExitsNonZero(t *testing.T) {
	failed := &client.NodeDiagnoseResult{OperationID: "op-9", NodeID: "node-1", JobName: "hetzner_node_diagnose",
		Status: "failed", Completed: true, ErrorExcerpt: "ssh: connect to host 10.0.1.4 port 22: connection timed out"}
	for _, args := range [][]string{
		{"cluster", "hetzner", "nodes", "diagnose", testClusterID, "node-1"},
		{"cluster", "hetzner", "nodes", "diagnose", testClusterID, "node-1", "-o", "json"},
	} {
		writeSelectedClusterJSON(t)
		setMockClient(t, &nodeDiagnoseMock{result: failed})
		var commandError error
		stdoutOutput := captureStdout(t, func() {
			_, commandError = executeCommand(args...)
		})
		if code := exitCodeFor(commandError); code != exitError {
			t.Errorf("%v: a failed diagnosis must exit %d, got %d (%v)", args, exitError, code, commandError)
		}
		if commandError == nil || !strings.Contains(commandError.Error(), "connection timed out") ||
			!strings.Contains(commandError.Error(), "op-9") {
			t.Errorf("%v: the error must carry the excerpt and the operation, got %v", args, commandError)
		}
		if !strings.Contains(stdoutOutput, "op-9") {
			t.Errorf("%v: the operation handle must still be printed, got: %s", args, stdoutOutput)
		}
	}
}

func TestClusterNodesDiagnoseClientTimeoutExitsWaitTimeout(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &nodeDiagnoseMock{err: context.DeadlineExceeded})

	_, commandError := executeCommand("cluster", "hetzner", "nodes", "diagnose", testClusterID, "node-1")

	if code := exitCodeFor(commandError); code != exitWaitTimeout {
		t.Fatalf("a client budget that runs out must exit %d (the job keeps running), got %d (%v)",
			exitWaitTimeout, code, commandError)
	}
}

// The diagnosis rides the platform's node SSH lane, which Proxmox and
// Morpheus do not have, so neither offers the verb - exactly the providers
// without cloud-init-log.
func TestClusterNodesDiagnoseSurfaceMatchesTheNodeSSHLane(t *testing.T) {
	expectations := map[string]bool{
		"hetzner": true, "ovh": true, "upcloud": true, "digitalocean": true, "scaleway": true,
		"aws": true, "ankracloud": true, "proxmox": false, "morpheus": false,
	}
	for providerName, shouldHaveDiagnose := range expectations {
		nodesCmd := findSubcommandPath(t, "cluster", providerName, "nodes")
		offered := map[string]bool{}
		for _, child := range nodesCmd.Commands() {
			offered[child.Name()] = true
		}
		if offered["diagnose"] != shouldHaveDiagnose || offered["diagnose"] != offered["cloud-init-log"] {
			t.Errorf("cluster %s nodes: diagnose offered=%v cloud-init-log offered=%v, want both %v",
				providerName, offered["diagnose"], offered["cloud-init-log"], shouldHaveDiagnose)
		}
	}
}
