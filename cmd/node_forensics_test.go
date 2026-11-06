package cmd

// Node-hang forensics: `cluster ovh nodes console-log` reads a VM's serial
// console, `cluster describe node --kubelet-config` adds the kubelet's live
// configuration, and `cluster operations list <id>` names who started an
// operation.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type nodeConsoleLogMock struct {
	baseMock
	result *client.NodeConsoleLogResult
	err    error
	calls  []string
}

func (m *nodeConsoleLogMock) OvhNodeConsoleLog(ctx context.Context, clusterID, nodeID string) (*client.NodeConsoleLogResult, error) {
	m.calls = append(m.calls, clusterID+":"+nodeID)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return nil, context.Canceled
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

// completedConsoleLog is a finished read, with numbers as float64 the way a
// decoded JSON report carries them.
func completedConsoleLog() *client.NodeConsoleLogResult {
	return &client.NodeConsoleLogResult{
		OperationID: "op-9", NodeID: "node-1", JobName: "ovh_node_console_log", Status: "success", Completed: true,
		Report: map[string]interface{}{
			"action":             "console_log",
			"node":               map[string]interface{}{"name": "worker-1", "ovh_instance_id": "inst-1", "region": "GRA9"},
			"line_limit":         float64(1000),
			"line_count":         float64(4),
			"line_limit_reached": false,
			"console_output":     "line one\nline two\nline three\nkernel: BUG: soft lockup\n",
			"truncated":          false,
		},
	}
}

// resetConsoleLogFlags puts --lines and -o back after a test: the command
// tree is global, so a flag one test sets carries into the next.
func resetConsoleLogFlags(t *testing.T) {
	t.Helper()
	resetCommandFlags(t, findSubcommandPath(t, "cluster", "ovh", "nodes", "console-log"))
}

func TestClusterNodesConsoleLogPrintsTheLogOnStdoutAndTheHeadingOnStderr(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	mock := &nodeConsoleLogMock{result: completedConsoleLog()}
	setMockClient(t, mock)

	var stdoutOutput string
	stderrOutput := captureStderr(t, func() {
		stdoutOutput = captureStdout(t, func() {
			if _, err := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1"); err != nil {
				t.Errorf("nodes console-log: %v", err)
			}
		})
	})

	if len(mock.calls) != 1 || mock.calls[0] != testClusterID+":node-1" {
		t.Fatalf("expected one console read for %s node-1, got %v", testClusterID, mock.calls)
	}
	if stdoutOutput != "line one\nline two\nline three\nkernel: BUG: soft lockup\n" {
		t.Errorf("stdout must be the console text alone, got %q", stdoutOutput)
	}
	if !strings.Contains(stderrOutput, "Console log of worker-1 (instance inst-1, GRA9): the newest 4 lines.") {
		t.Errorf("stderr lacks the heading: %q", stderrOutput)
	}
}

func TestClusterNodesConsoleLogLinesKeepsTheNewestAndSaysSo(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	result := completedConsoleLog()
	result.Report["line_limit_reached"] = true
	result.Report["redacted_values"] = float64(2)
	setMockClient(t, &nodeConsoleLogMock{result: result})

	var stdoutOutput string
	stderrOutput := captureStderr(t, func() {
		stdoutOutput = captureStdout(t, func() {
			if _, err := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1", "--lines", "2"); err != nil {
				t.Errorf("nodes console-log --lines 2: %v", err)
			}
		})
	})

	if stdoutOutput != "line three\nkernel: BUG: soft lockup\n" {
		t.Errorf("--lines 2 must keep the newest two lines, got %q", stdoutOutput)
	}
	for _, expected := range []string{"the newest 2 lines", "Older console output may exist",
		"replaced 2 values that looked like secrets", "Shortened here to the newest 2 (--lines)"} {
		if !strings.Contains(stderrOutput, expected) {
			t.Errorf("stderr lacks %q: %q", expected, stderrOutput)
		}
	}
}

func TestClusterNodesConsoleLogJSONCarriesTheTailedAnswer(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	setMockClient(t, &nodeConsoleLogMock{result: completedConsoleLog()})

	// -o json goes through renderStructured to the command's own writer,
	// which executeCommand captures.
	stdoutOutput, executeError := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1", "--lines", "1", "-o", "json")
	if executeError != nil {
		t.Fatalf("nodes console-log -o json: %v", executeError)
	}

	var decoded client.NodeConsoleLogResult
	if decodeError := json.Unmarshal([]byte(stdoutOutput), &decoded); decodeError != nil {
		t.Fatalf("-o json must be the result document alone: %v\n%s", decodeError, stdoutOutput)
	}
	if decoded.Report["console_output"] != "kernel: BUG: soft lockup\n" || decoded.Report["tailed_to_lines"] != float64(1) {
		t.Errorf("report = %+v, want the newest line and tailed_to_lines 1", decoded.Report)
	}
}

func TestClusterNodesConsoleLogFailedReadExitsNonZero(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	setMockClient(t, &nodeConsoleLogMock{result: &client.NodeConsoleLogResult{
		OperationID: "op-10", NodeID: "node-1", JobName: "ovh_node_console_log", Status: "failed", Completed: true,
		ErrorExcerpt: "the cluster's published OpenStack credential was refused by Keystone (401)"}})

	_, executeError := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1")
	if executeError == nil || !strings.Contains(executeError.Error(), "refused by Keystone (401)") {
		t.Fatalf("a failed read must exit non-zero with its reason, got %v", executeError)
	}
}

func TestClusterNodesConsoleLogStillRunningPointsAtThePoller(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	setMockClient(t, &nodeConsoleLogMock{result: &client.NodeConsoleLogResult{
		OperationID: "op-11", NodeID: "node-1", JobName: "ovh_node_console_log", Status: "running", Attached: true}})

	stdoutOutput := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1"); err != nil {
			t.Errorf("a read still running is not a failure: %v", err)
		}
	})
	for _, expected := range []string{"attached to the read already in flight", "still running", "ankra cluster operations list op-11"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

func TestClusterNodesConsoleLogRefusesNegativeLines(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetConsoleLogFlags(t)
	mock := &nodeConsoleLogMock{result: completedConsoleLog()}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "ovh", "nodes", "console-log", testClusterID, "node-1", "--lines", "-3")
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Fatalf("--lines -3 must exit %d, got %d (%v)", exitUsage, got, executeError)
	}
	if len(mock.calls) != 0 {
		t.Error("the flag check must run before the platform is asked")
	}
}

func TestClusterNodesConsoleLogIsRegisteredForOVHOnly(t *testing.T) {
	findSubcommandPath(t, "cluster", "ovh", "nodes", "console-log")
	for _, provider := range []string{"hetzner", "upcloud", "digitalocean", "scaleway", "aws", "ankracloud", "proxmox", "morpheus"} {
		providerCmd, _, findError := rootCmd.Find([]string{"cluster", provider, "nodes"})
		if findError != nil || providerCmd.Name() != "nodes" {
			continue
		}
		for _, child := range providerCmd.Commands() {
			if child.Name() == "console-log" {
				t.Errorf("%s nodes registers console-log, which the platform serves for OVH only", provider)
			}
		}
	}
}

func TestTailLinesKeepsTheNewestLines(t *testing.T) {
	for _, testCase := range []struct {
		text      string
		count     int
		want      string
		shortened bool
	}{
		{text: "a\nb\nc\n", count: 2, want: "b\nc\n", shortened: true},
		{text: "a\nb\nc", count: 1, want: "c", shortened: true},
		{text: "a\nb\n", count: 2, want: "a\nb\n", shortened: false},
		{text: "", count: 5, want: "", shortened: false},
	} {
		got, shortened := tailLines(testCase.text, testCase.count)
		if got != testCase.want || shortened != testCase.shortened {
			t.Errorf("tailLines(%q, %d) = %q, %v; want %q, %v", testCase.text, testCase.count, got, shortened, testCase.want, testCase.shortened)
		}
	}
}

// kubeletConfigMock serves the node and its events from debugVerbsMock and
// the kubelet configuration from its own fields.
type kubeletConfigMock struct {
	*debugVerbsMock
	config *client.NodeKubeletConfig
	err    error
	nodes  []string
}

func (m *kubeletConfigMock) GetNodeKubeletConfig(clusterID, nodeName string) (*client.NodeKubeletConfig, error) {
	m.nodes = append(m.nodes, nodeName)
	if m.err != nil {
		return nil, m.err
	}
	return m.config, nil
}

func newKubeletConfigMock() *kubeletConfigMock {
	verbs := newDebugVerbsMock()
	verbs.responses["Node"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "worker-1"},
		})},
	}
	verbs.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	return &kubeletConfigMock{debugVerbsMock: verbs, config: &client.NodeKubeletConfig{
		NodeName:      "worker-1",
		KubeletConfig: map[string]interface{}{"maxPods": float64(110), "evictionHard": map[string]interface{}{"memory.available": "100Mi"}},
	}}
}

func TestDescribeNodeKubeletConfigAddsTheLiveConfiguration(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newKubeletConfigMock()
	setMockClient(t, mock)

	output := stripANSICodes(captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "node", "worker-1", "--kubelet-config"); executeError != nil {
			t.Fatalf("describe node --kubelet-config: %v", executeError)
		}
	}))
	if len(mock.nodes) != 1 || mock.nodes[0] != "worker-1" {
		t.Fatalf("kubelet configuration reads = %v, want one for worker-1", mock.nodes)
	}
	for _, expected := range []string{"Node/worker-1", "Kubelet configuration (live, as the kubelet reports it on /configz):", "maxPods: 110", "memory.available: 100Mi"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestDescribeNodeKubeletConfigReportsAnUnreadableKubelet(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newKubeletConfigMock()
	mock.err = errors.New("the apiserver could not reach the kubelet of worker-1")
	setMockClient(t, mock)

	output := stripANSICodes(captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "node", "worker-1", "--kubelet-config"); executeError != nil {
			t.Fatalf("an unreadable kubelet must not fail the describe: %v", executeError)
		}
	}))
	if !strings.Contains(output, "Kubelet configuration: could not be read: the apiserver could not reach the kubelet of worker-1") {
		t.Errorf("output must carry the reason in place of the configuration:\n%s", output)
	}
}

func TestDescribeNodeKubeletConfigJSONSaysWhyItIsMissing(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newKubeletConfigMock()
	mock.err = errors.New("kubelet does not serve /configz")
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "node", "worker-1", "--kubelet-config", "-o", "json"); executeError != nil {
			t.Fatalf("describe -o json: %v", executeError)
		}
	})
	var decoded describeResult
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("decoding: %v\n%s", decodeError, output)
	}
	if decoded.KubeletConfig != nil || decoded.KubeletConfigError != "kubelet does not serve /configz" {
		t.Errorf("result = %+v, want no configuration and the reason", decoded)
	}
}

func TestDescribeKubeletConfigRefusesAnotherKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newKubeletConfigMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "describe", "pod", "web-1", "-n", "default", "--kubelet-config")
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Fatalf("--kubelet-config on a pod must exit %d, got %d (%v)", exitUsage, got, executeError)
	}
	if len(mock.requests) != 0 || len(mock.nodes) != 0 {
		t.Error("the kind check must run before any API call")
	}
}

func TestExecutionInitiatorNamesWhatThePlatformSays(t *testing.T) {
	member := "alex@example.com"
	userID := "11111111-1111-1111-1111-111111111111"
	for _, testCase := range []struct {
		execution client.ExecutionSummary
		want      string
	}{
		{execution: client.ExecutionSummary{UserID: &userID, InitiatedBy: &member}, want: member},
		{execution: client.ExecutionSummary{UserID: &userID}, want: "user " + userID},
		{execution: client.ExecutionSummary{}, want: "not recorded (an automated run or an older operation)"},
	} {
		if got := executionInitiator(testCase.execution); got != testCase.want {
			t.Errorf("executionInitiator(%+v) = %q, want %q", testCase.execution, got, testCase.want)
		}
	}
}
