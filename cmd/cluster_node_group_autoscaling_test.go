package cmd

// Tests for `ankra cluster node-group autoscaling get|set`: provider
// detection through GetClusterByID, the request payload, and both the
// enabled and disabled rendering paths.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type autoscalingMock struct {
	baseMock

	clusterKind string
	getResult   *client.NodeGroupAutoscalingResult
	getError    error
	getCalls    int
	// nodeCount is the group's node count in the node-group listing.
	nodeCount int
	listError error
	listCalls int
	// saves makes a write record its bounds as the platform does
	// (upsertNodeGroupSettings, on a disable too), so the next read answers
	// them.
	saves bool

	setCalls []client.NodeGroupAutoscalingRequest
}

func (m *autoscalingMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Kind: m.clusterKind}, nil
}

func (m *autoscalingMock) GetHetznerNodeGroupAutoscaling(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error) {
	m.getCalls++
	return m.getResult, m.getError
}

func (m *autoscalingMock) ListHetznerNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	m.listCalls++
	if m.listError != nil {
		return nil, m.listError
	}
	return &client.NodeGroupListResult{NodeGroups: []client.NodeGroupInfo{{Name: "default", Count: m.nodeCount}}}, nil
}

func (m *autoscalingMock) GetOvhNodeGroupAutoscaling(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error) {
	return m.getResult, nil
}

func (m *autoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	if m.saves {
		m.getResult = recordedAutoscaling(req.Enabled, req.MinCount, req.MaxCount)
	}
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
	}, false, nil
}

func TestClusterNodeGroupAutoscalingGetCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{
		clusterKind: "hetzner",
		getResult:   &client.NodeGroupAutoscalingResult{GroupName: "default", Enabled: true, MinCount: 1, MaxCount: 5},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "get", testClusterID, "default")
	})

	if !strings.Contains(stdoutOutput, "autoscaling: enabled") {
		t.Errorf("expected enabled state in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Min: 1") || !strings.Contains(stdoutOutput, "Max: 5") {
		t.Errorf("expected bounds in output, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingSetCommandSendsBounds(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner"}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default",
			"--enabled", "--min", "2", "--max", "6", "--wait")
	})

	if len(mock.setCalls) != 1 {
		t.Fatalf("expected one autoscaling update call, got %d", len(mock.setCalls))
	}
	sentRequest := mock.setCalls[0]
	if !sentRequest.Enabled || sentRequest.MinCount != 2 || sentRequest.MaxCount != 6 {
		t.Fatalf("request drifted: %+v", sentRequest)
	}
	if !strings.Contains(stdoutOutput, "autoscaling enabled (min 2, max 6)") {
		t.Errorf("expected enable confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingSetCommandDisables(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner"}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default",
			"--enabled=false", "--min", "1", "--max", "5", "--wait")
	})

	if len(mock.setCalls) != 1 || mock.setCalls[0].Enabled {
		t.Fatalf("expected a disable call, got %+v", mock.setCalls)
	}
	if !strings.Contains(stdoutOutput, "autoscaling disabled") {
		t.Errorf("expected disable confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupAutoscalingRefusesUnsupportedKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "imported"}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "node-group", "autoscaling", "get", testClusterID, "default")
	if commandError == nil || !strings.Contains(commandError.Error(), "does not support node groups") {
		t.Fatalf("expected the unsupported-kind refusal, got %v", commandError)
	}
}

// recordedAutoscaling is what the platform's autoscaling read answers.
func recordedAutoscaling(enabled bool, minCount, maxCount int) *client.NodeGroupAutoscalingResult {
	return &client.NodeGroupAutoscalingResult{GroupName: "default", Enabled: enabled, MinCount: minCount, MaxCount: maxCount}
}

// The platform saves its default (min 1, max 100) for a bound the body
// leaves out, so sending only the flags given raised a group capped at 5 to
// 100 on a plain --enabled and reset both bounds on --enabled=false
// (ankra-v3qdto.6 review). Both bounds are sent: the flag when given, else
// on a disable exactly what the group reads as now, else on an enable the
// bound recorded for the group, or min 1 / max 5 when the group has none
// recorded (the platform reads such a group as disabled, 1, 100). An enable
// without --max lists the group to check the max does not scale it down.
func TestClusterNodeGroupAutoscalingSetKeepsTheRecordedBoundsItIsNotGiven(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		recorded  *client.NodeGroupAutoscalingResult
		nodeCount int
		args      []string
		wantBody  string
		wantReads int
		wantLists int
		wantNote  string
	}{
		{name: "omitted max keeps the recorded max", recorded: recordedAutoscaling(true, 2, 7), nodeCount: 7,
			args: []string{"--enabled", "--min", "1"}, wantBody: `{"enabled":true,"min_count":1,"max_count":7}`, wantReads: 1, wantLists: 1,
			wantNote: "--max not given: keeping max 7, recorded for the group."},
		{name: "omitted max with nothing recorded sends 5", recorded: recordedAutoscaling(false, 1, 100), nodeCount: 3,
			args: []string{"--enabled", "--min", "1"}, wantBody: `{"enabled":true,"min_count":1,"max_count":5}`, wantReads: 1, wantLists: 1,
			wantNote: "--max not given: using max 5, the CLI default: the group reads as disabled with min 1 and max 100, " +
				"the answer for a group with no bounds recorded and for one that recorded exactly those; pass --max to set another."},
		{name: "plain enable with nothing recorded sends 1 and 5", recorded: recordedAutoscaling(false, 1, 100), nodeCount: 5,
			args: []string{"--enabled"}, wantBody: `{"enabled":true,"min_count":1,"max_count":5}`, wantReads: 1, wantLists: 1},
		{name: "plain disable with nothing recorded sends what the group reads, without a listing", recorded: recordedAutoscaling(false, 1, 100), nodeCount: 8,
			args: []string{"--enabled=false"}, wantBody: `{"enabled":false,"min_count":1,"max_count":100}`, wantReads: 1,
			wantNote: "--max not given: keeping max 100, what the group reads as now."},
		{name: "a disable with only --max keeps the min as the group reads", recorded: recordedAutoscaling(false, 1, 100),
			args: []string{"--enabled=false", "--max", "9"}, wantBody: `{"enabled":false,"min_count":1,"max_count":9}`, wantReads: 1,
			wantNote: "--min not given: keeping min 1, what the group reads as now."},
		{name: "a disable with only --min on nothing recorded takes max 5, not 100", recorded: recordedAutoscaling(false, 1, 100),
			args: []string{"--enabled=false", "--min", "2"}, wantBody: `{"enabled":false,"min_count":2,"max_count":5}`, wantReads: 1,
			wantNote: "--max not given: using max 5, the CLI default: the group reads as disabled with min 1 and max 100, " +
				"the answer for a group with no bounds recorded and for one that recorded exactly those; pass --max to set another."},
		{name: "a disable with only --min keeps a recorded max", recorded: recordedAutoscaling(true, 1, 9),
			args: []string{"--enabled=false", "--min", "2"}, wantBody: `{"enabled":false,"min_count":2,"max_count":9}`, wantReads: 1,
			wantNote: "--max not given: keeping max 9, recorded for the group."},
		{name: "plain disable keeps both recorded bounds", recorded: recordedAutoscaling(true, 0, 9),
			args: []string{"--enabled=false"}, wantBody: `{"enabled":false,"min_count":0,"max_count":9}`, wantReads: 1},
		{name: "a disabled group keeps the bounds it recorded", recorded: recordedAutoscaling(false, 2, 7),
			args: []string{"--enabled", "--max", "8"}, wantBody: `{"enabled":true,"min_count":2,"max_count":8}`, wantReads: 1},
		{name: "explicit flags win without a read", recorded: recordedAutoscaling(true, 2, 7),
			args: []string{"--enabled", "--min", "3", "--max", "4"}, wantBody: `{"enabled":true,"min_count":3,"max_count":4}`},
		{name: "explicit scale to zero", recorded: recordedAutoscaling(true, 2, 7),
			args: []string{"--enabled", "--min", "0", "--max", "4"}, wantBody: `{"enabled":true,"min_count":0,"max_count":4}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
			writeSelectedClusterJSON(t)
			mock := &autoscalingMock{clusterKind: "hetzner", getResult: testCase.recorded, nodeCount: testCase.nodeCount}
			setMockClient(t, mock)

			var stderr string
			var runError error
			captureStdout(t, func() {
				arguments := append([]string{"cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--wait"}, testCase.args...)
				stderr, runError = executeCommand(arguments...)
			})
			if runError != nil {
				t.Fatalf("set: %v", runError)
			}
			if mock.getCalls != testCase.wantReads || mock.listCalls != testCase.wantLists {
				t.Errorf("autoscaling reads = %d, listings = %d, want %d and %d", mock.getCalls, mock.listCalls,
					testCase.wantReads, testCase.wantLists)
			}
			if len(mock.setCalls) != 1 {
				t.Fatalf("autoscaling update calls = %d, want 1", len(mock.setCalls))
			}
			body, marshalError := json.Marshal(mock.setCalls[0])
			if marshalError != nil {
				t.Fatalf("marshal: %v", marshalError)
			}
			if string(body) != testCase.wantBody {
				t.Errorf("body = %s, want %s", body, testCase.wantBody)
			}
			if testCase.wantNote != "" && !strings.Contains(stderr, testCase.wantNote) {
				t.Errorf("stderr lacks %q: %s", testCase.wantNote, stderr)
			}
		})
	}
}

// When a bound is left out and the current bounds cannot be read, nothing
// is sent: a guessed bound could scale the group. The refusal names the way
// out, which is also the one on a stopped cluster, whose bounds read the
// platform answers "Node group 'g' not found" (ankra-v3qdto.65), and keeps
// the read's own exit class (a 401 exits 6, a 403 exits 7...).
func TestClusterNodeGroupAutoscalingSetRefusesWhenTheRecordedBoundsCannotBeRead(t *testing.T) {
	stoppedClusterRead := client.NewUnexpectedResponseError(409, "Node group 'default' not found")
	for _, testCase := range []struct {
		name        string
		mock        *autoscalingMock
		args        []string
		wantOmitted string
	}{
		{name: "read fails", mock: &autoscalingMock{clusterKind: "hetzner", getError: client.ErrUnauthorized},
			args: []string{"--enabled", "--min", "1"}, wantOmitted: "(--max)"},
		{name: "empty read", mock: &autoscalingMock{clusterKind: "hetzner"},
			args: []string{"--enabled", "--min", "1"}, wantOmitted: "(--max)"},
		{name: "a plain disable on a stopped cluster", mock: &autoscalingMock{clusterKind: "hetzner", getError: stoppedClusterRead},
			args: []string{"--enabled=false"}, wantOmitted: "(--min and --max)"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
			writeSelectedClusterJSON(t)
			setMockClient(t, testCase.mock)

			var runError error
			captureStdout(t, func() {
				arguments := append([]string{"cluster", "node-group", "autoscaling", "set", testClusterID, "default"}, testCase.args...)
				_, runError = executeCommand(arguments...)
			})
			if runError == nil ||
				!strings.Contains(runError.Error(), "could not read the group's current autoscaling bounds (if the cluster is stopped, pass both --min and --max)") ||
				!strings.Contains(runError.Error(), testCase.wantOmitted) {
				t.Fatalf("want a refusal naming the stopped-cluster way out and %s, got %v", testCase.wantOmitted, runError)
			}
			if len(testCase.mock.setCalls) != 0 {
				t.Fatalf("nothing may be sent when the current bounds are unknown, sent %+v", testCase.mock.setCalls)
			}
			if testCase.mock.getError != nil && exitCodeFor(runError) != exitCodeFor(testCase.mock.getError) {
				t.Errorf("exit = %d, want the read's own %d", exitCodeFor(runError), exitCodeFor(testCase.mock.getError))
			}
		})
	}
}

// A bound given beside one kept that it contradicts is refused before
// anything is sent, naming where the kept bound comes from.
func TestClusterNodeGroupAutoscalingSetRefusesAMinAboveTheKeptMax(t *testing.T) {
	resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner", getResult: recordedAutoscaling(false, 1, 100)}
	setMockClient(t, mock)

	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--enabled", "--min", "8")
	})
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--min 8 is above the max of 5") {
		t.Fatalf("want exit 2 naming the kept max, got %v (exit %d)", runError, exitCodeFor(runError))
	}
	if len(mock.setCalls) != 0 {
		t.Fatalf("nothing may be sent, sent %+v", mock.setCalls)
	}
}

// A max the user did not type, the CLI default or the one recorded for the
// group, is refused rather than letting an enable scale a bigger group
// down: a plain --enabled on an 8-node group used to clamp it to 5
// (ankra-v3qdto.6). The refusal is a preflight block with nothing changed,
// so it exits 8. An unreadable node count refuses too, keeping the
// listing's error class.
func TestClusterNodeGroupAutoscalingSetRefusesAnUntypedMaxThatScalesTheGroupDown(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		mock     *autoscalingMock
		wantExit int
		want     string
	}{
		{name: "more nodes than the default max", mock: &autoscalingMock{clusterKind: "hetzner",
			getResult: recordedAutoscaling(false, 1, 100), nodeCount: 8},
			wantExit: exitBlocked, want: "--max was left out, so the CLI default max of 5 (the group reads as recording no bounds) would apply, " +
				"and the group has 8 nodes: enabling would scale it down to 5; pass --max (--max 8 keeps its size)"},
		{name: "more nodes than the recorded max", mock: &autoscalingMock{clusterKind: "hetzner",
			getResult: recordedAutoscaling(false, 2, 7), nodeCount: 12},
			wantExit: exitBlocked, want: "--max was left out, so the max of 7 recorded for the group would apply, " +
				"and the group has 12 nodes: enabling would scale it down to 7; pass --max (--max 12 keeps its size)"},
		{name: "node count unreadable", mock: &autoscalingMock{clusterKind: "hetzner",
			getResult: recordedAutoscaling(false, 1, 100), listError: client.ErrUnauthorized},
			wantExit: exitAuth, want: "whether that scales the group down could not be checked"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
			writeSelectedClusterJSON(t)
			setMockClient(t, testCase.mock)

			var runError error
			captureStdout(t, func() {
				_, runError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--enabled")
			})
			if runError == nil || exitCodeFor(runError) != testCase.wantExit || !strings.Contains(runError.Error(), testCase.want) {
				t.Fatalf("want exit %d saying %q, got %v (exit %d)", testCase.wantExit, testCase.want, runError, exitCodeFor(runError))
			}
			if len(testCase.mock.setCalls) != 0 {
				t.Fatalf("nothing may be sent, sent %+v", testCase.mock.setCalls)
			}
		})
	}

	// A --max the user typed is sent whatever the node count, without a
	// listing: scaling the group down to it is what the user asked for.
	resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner", getResult: recordedAutoscaling(false, 2, 7), nodeCount: 12}
	setMockClient(t, mock)
	captureStdout(t, func() {
		if _, runError := executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default",
			"--enabled", "--max", "3", "--wait"); runError != nil {
			t.Fatalf("set with a typed max: %v", runError)
		}
	})
	if len(mock.setCalls) != 1 || mock.setCalls[0].MaxCount != 3 || mock.listCalls != 0 {
		t.Fatalf("a typed max must be sent without a listing, sent %+v after %d listings", mock.setCalls, mock.listCalls)
	}
}

// A plain disable wrote the CLI default max 5 for a group with no bounds
// recorded, and the platform saved it; the next plain enable then read max 5
// as recorded and scaled an 8-node group down to 5 unguarded. The disable now
// sends what the group reads, and the enable is refused whatever the max's
// source. A group an older CLI left disabled at 1/5 is refused the same way.
// A disable that sets --min alone on a group with nothing recorded sends the
// CLI default max 5, not the read 100: saved beside --min 2, a 100 would read
// as recorded, and the next plain enable would keep it and let the autoscaler
// grow the group to 100. With 5 the enable is refused like the others.
func TestClusterNodeGroupAutoscalingSetDisableThenEnableNeverScalesTheGroupDown(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		recorded    *client.NodeGroupAutoscalingResult
		disableArgs []string
		wantDisable client.NodeGroupAutoscalingRequest
		want        string
	}{
		{name: "no bounds recorded", recorded: recordedAutoscaling(false, 1, 100),
			wantDisable: client.NodeGroupAutoscalingRequest{MinCount: 1, MaxCount: 100},
			want:        "the CLI default max of 5 (the group reads as recording no bounds) would apply, and the group has 8 nodes"},
		{name: "1 and 5 an older CLI recorded", recorded: recordedAutoscaling(false, 1, 5),
			wantDisable: client.NodeGroupAutoscalingRequest{MinCount: 1, MaxCount: 5},
			want:        "the max of 5 recorded for the group would apply, and the group has 8 nodes"},
		{name: "a disable with --min alone on no bounds recorded", recorded: recordedAutoscaling(false, 1, 100),
			disableArgs: []string{"--min", "2"},
			wantDisable: client.NodeGroupAutoscalingRequest{MinCount: 2, MaxCount: 5},
			want:        "the max of 5 recorded for the group would apply, and the group has 8 nodes"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
			writeSelectedClusterJSON(t)
			mock := &autoscalingMock{clusterKind: "hetzner", getResult: testCase.recorded, nodeCount: 8, saves: true}
			setMockClient(t, mock)

			captureStdout(t, func() {
				arguments := append([]string{"cluster", "node-group", "autoscaling", "set", testClusterID, "default",
					"--enabled=false", "--wait"}, testCase.disableArgs...)
				if _, runError := executeCommand(arguments...); runError != nil {
					t.Fatalf("disable: %v", runError)
				}
			})
			if len(mock.setCalls) != 1 || mock.setCalls[0] != testCase.wantDisable {
				t.Fatalf("the disable sent %+v, want %+v", mock.setCalls, testCase.wantDisable)
			}

			resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
			var runError error
			captureStdout(t, func() {
				_, runError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--enabled", "--wait")
			})
			if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), testCase.want) ||
				!strings.Contains(runError.Error(), "(--max 8 keeps its size)") {
				t.Fatalf("want exit 8 saying %q, got %v (exit %d)", testCase.want, runError, exitCodeFor(runError))
			}
			if len(mock.setCalls) != 1 {
				t.Fatalf("the enable must send nothing, sent %+v", mock.setCalls[1:])
			}
		})
	}
}

// The confirmation echoes the bounds that were sent.
func TestClusterNodeGroupAutoscalingSetEchoesTheBoundsSent(t *testing.T) {
	resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAutoscalingSetCmd) })
	writeSelectedClusterJSON(t)
	mock := &autoscalingMock{clusterKind: "hetzner", getResult: recordedAutoscaling(false, 1, 100), nodeCount: 2}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "default", "--enabled", "--wait")
	})
	if !strings.Contains(stdoutOutput, "autoscaling enabled (min 1, max 5)") {
		t.Errorf("expected the bounds sent in the confirmation, got: %s", stdoutOutput)
	}
}

// scalingAutoscalingMock answers an autoscaling write that moves the group
// to its new bounds, and serves the operation doing it.
type scalingAutoscalingMock struct {
	autoscalingMock

	submitted       bool
	operationStatus string
	operationReads  int
}

func (m *scalingAutoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	nodeCount, targetCount, operationID := 5, 3, "6f1c2d3e-0000-4000-8000-000000000042"
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
		NodeCount: &nodeCount, TargetCount: &targetCount, OperationID: &operationID,
		Message: "Node group 'ci-large' will scale down from 5 to 3 nodes as running pipeline steps finish.",
	}, m.submitted, nil
}

func (m *scalingAutoscalingMock) GetExecution(executionID string) (client.ExecutionDetail, error) {
	m.operationReads++
	status := "running"
	if m.operationReads > 1 {
		status = m.operationStatus
	}
	excerpt := "drain refused by a PodDisruptionBudget"
	return client.ExecutionDetail{Execution: client.ExecutionSummary{ID: executionID, Status: status, ErrorExcerpt: &excerpt}}, nil
}

func fastNodeGroupOperationPolling(t *testing.T) {
	t.Helper()
	previousInterval := nodeGroupOperationPollInterval
	nodeGroupOperationPollInterval = time.Millisecond
	t.Cleanup(func() { nodeGroupOperationPollInterval = previousInterval })
}

// The 2026-10-22 ci-large request: a --max below the group's node count says
// the group will scale down, names the operation, and --wait waits for it.
func TestClusterNodeGroupAutoscalingSetSaysTheGroupScalesDownAndWaitsForTheOperation(t *testing.T) {
	writeSelectedClusterJSON(t)
	fastNodeGroupOperationPolling(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, operationStatus: "success"}
	setMockClient(t, mock)

	var commandError error
	stdoutOutput := captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if commandError != nil {
		t.Fatalf("set failed: %v", commandError)
	}
	for _, expected := range []string{
		"autoscaling enabled (min 1, max 3)",
		"will scale down from 5 to 3 nodes as running pipeline steps finish",
		"Operation: 6f1c2d3e-0000-4000-8000-000000000042",
		"Operation 6f1c2d3e-0000-4000-8000-000000000042 finished: success.",
	} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in output, got: %s", expected, stdoutOutput)
		}
	}
	if mock.operationReads < 2 {
		t.Fatalf("--wait must poll the operation until it ends, read it %d times", mock.operationReads)
	}
}

func TestClusterNodeGroupAutoscalingSetWaitFailsWhenTheOperationFails(t *testing.T) {
	writeSelectedClusterJSON(t)
	fastNodeGroupOperationPolling(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, operationStatus: "failed"}
	setMockClient(t, mock)

	var commandError error
	captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if commandError == nil || !strings.Contains(commandError.Error(), "ended failed: drain refused by a PodDisruptionBudget") {
		t.Fatalf("a failed operation must fail the command with its reason, got %v", commandError)
	}
}

// Without --wait, a platform that finished the write inside its verdict
// window still says what happens, rather than only "submitted".
func TestClusterNodeGroupAutoscalingSetSubmittedSaysWhatHappens(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &scalingAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}, submitted: true}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait=false")
	})

	if !strings.Contains(stdoutOutput, "will scale down from 5 to 3 nodes") ||
		!strings.Contains(stdoutOutput, "Operation: 6f1c2d3e-0000-4000-8000-000000000042") {
		t.Errorf("expected the outcome and the operation, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Re-run the same command with --wait") {
		t.Errorf("an answer that says what happens must not read as a bare submission, got: %s", stdoutOutput)
	}
	if mock.operationReads != 0 {
		t.Fatalf("without --wait the operation must not be polled, read it %d times", mock.operationReads)
	}
}

// inBoundsAutoscalingMock answers a write that leaves the node count alone,
// with the platform's message about it.
type inBoundsAutoscalingMock struct {
	autoscalingMock
}

func (m *inBoundsAutoscalingMock) UpdateHetznerNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	m.setCalls = append(m.setCalls, req)
	nodeCount := 2
	return &client.NodeGroupAutoscalingResult{
		GroupName: groupName, Enabled: req.Enabled, MinCount: req.MinCount, MaxCount: req.MaxCount,
		NodeCount: &nodeCount, TargetCount: &nodeCount,
		Message: "Node group 'ci-large' autoscaling enabled (min 1, max 3); its node count of 2 is within the bounds.",
	}, false, nil
}

// The platform's message is printed whenever it sends one, not only when the
// node count moves.
func TestClusterNodeGroupAutoscalingSetPrintsThePlatformMessageWhenNothingMoves(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &inBoundsAutoscalingMock{autoscalingMock: autoscalingMock{clusterKind: "hetzner"}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "autoscaling", "set", testClusterID, "ci-large",
			"--enabled", "--min", "1", "--max", "3", "--wait")
	})

	if !strings.Contains(stdoutOutput, "its node count of 2 is within the bounds") {
		t.Errorf("expected the platform's message, got: %s", stdoutOutput)
	}
}
