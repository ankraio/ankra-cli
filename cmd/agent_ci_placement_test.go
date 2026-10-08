package cmd

// Tests for the cluster-level CI placement flags on `ankra cluster agent ci
// set` and the placement block `get` prints (PLA-902): a node group or a raw
// selector and tolerations put every pipeline pod of the cluster on chosen
// nodes, and the worker count is no longer required to change only that.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestClusterAgentCISetSendsANodeGroupPlacementWithoutWorkers(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set",
		"--node-group", "pipelines"); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	update := mock.lastUpdate
	if update.CIWorkerCount != nil || update.CIStorageClass != nil {
		t.Errorf("a placement-only write sent workers=%v storage=%v", update.CIWorkerCount, update.CIStorageClass)
	}
	if update.CIPlacement == nil || update.CIPlacement.NodeGroup != "pipelines" || update.ClearPlacement {
		t.Fatalf("placement sent = %+v clear=%t", update.CIPlacement, update.ClearPlacement)
	}
}

func TestClusterAgentCISetParsesSelectorsTolerationsAndMode(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--workers", "4",
		"--node-selector", "pool=ci", "--node-selector", "disk=ssd",
		"--toleration", "smartoptics.dev/pipelines=true:NoSchedule", "--toleration", "dedicated:NoExecute",
		"--placement", "preferred"); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	placement := mock.lastUpdate.CIPlacement
	if placement == nil || placement.Mode != client.AgentCIPlacementModePreferred ||
		placement.NodeSelector["pool"] != "ci" || placement.NodeSelector["disk"] != "ssd" {
		t.Fatalf("placement = %+v", placement)
	}
	want := []client.AgentCIPlacementToleration{
		{Key: "smartoptics.dev/pipelines", Operator: "Equal", Value: "true", Effect: "NoSchedule"},
		{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"},
	}
	if len(placement.Tolerations) != 2 || placement.Tolerations[0] != want[0] || placement.Tolerations[1] != want[1] {
		t.Fatalf("tolerations = %+v, want %+v", placement.Tolerations, want)
	}
	if mock.lastUpdate.CIWorkerCount == nil || *mock.lastUpdate.CIWorkerCount != 4 {
		t.Errorf("the worker count still travels beside the placement, got %v", mock.lastUpdate.CIWorkerCount)
	}
}

func TestClusterAgentCISetClearsThePlacement(t *testing.T) {
	mock := &agentCIMock{settings: newAgentCISettings()}
	if _, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "set", "--clear-placement"); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	if !mock.lastUpdate.ClearPlacement || mock.lastUpdate.CIPlacement != nil {
		t.Fatalf("update = %+v, want a clear and no placement", mock.lastUpdate)
	}
}

func TestClusterAgentCISetRefusesMalformedPlacementFlags(t *testing.T) {
	for name, arguments := range map[string][]string{
		"selector without value":    {"--node-selector", "pool"},
		"toleration without effect": {"--toleration", "dedicated=ci"},
		"toleration without key":    {"--toleration", "=ci:NoSchedule"},
		"unknown toleration effect": {"--toleration", "dedicated=ci:Sometimes"},
		"unknown mode":              {"--node-group", "pipelines", "--placement", "sometimes"},
		"clear with a group":        {"--clear-placement", "--node-group", "pipelines"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &agentCIMock{settings: newAgentCISettings()}
			_, runError := runAgentCICommand(t, mock,
				append([]string{"cluster", "agent", "ci", "set"}, arguments...)...)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", runError)
			}
			if mock.updateCalls != 0 {
				t.Errorf("a malformed flag must send nothing, sent %d", mock.updateCalls)
			}
		})
	}
}

func TestAgentCISettingsUpdateEncodesOnlyNamedMembersAndNullForAClear(t *testing.T) {
	workerCount := 2
	for name, testCase := range map[string]struct {
		update client.AgentCISettingsUpdate
		want   string
	}{
		"workers only":  {client.AgentCISettingsUpdate{CIWorkerCount: &workerCount}, `{"ci_worker_count":2}`},
		"clear":         {client.AgentCISettingsUpdate{ClearPlacement: true}, `{"ci_placement":null}`},
		"node group":    {client.AgentCISettingsUpdate{CIPlacement: &client.AgentCIPlacement{NodeGroup: "pipelines"}}, `{"ci_placement":{"node_group":"pipelines"}}`},
		"nothing named": {client.AgentCISettingsUpdate{}, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, encodeError := json.Marshal(testCase.update)
			if encodeError != nil {
				t.Fatal(encodeError)
			}
			if string(encoded) != testCase.want {
				t.Fatalf("encoded %s, want %s", encoded, testCase.want)
			}
		})
	}
}

func TestClusterAgentCIGetPrintsThePlacementBlock(t *testing.T) {
	settings := newAgentCISettings()
	admitting := 2
	settings.CIPlacement = &client.AgentCIPlacement{
		NodeGroup:    "pipelines",
		NodeSelector: map[string]string{"ankra.cloud/node-group": "pipelines"},
		Tolerations: []client.AgentCIPlacementToleration{
			{Key: "smartoptics.dev/pipelines", Operator: "Equal", Value: "true", Effect: "NoSchedule"},
			{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"},
		},
		Mode: client.AgentCIPlacementModeRequired,
	}
	settings.PlacementAdmittingNodes = &admitting
	output, runError := runAgentCICommand(t, &agentCIMock{settings: settings}, "cluster", "agent", "ci", "get")
	if runError != nil {
		t.Fatalf("get: %v", runError)
	}
	for _, expected := range []string{
		"Node group:          pipelines",
		"Node selector:       ankra.cloud/node-group=pipelines",
		"Tolerations:         smartoptics.dev/pipelines=true:NoSchedule, dedicated:NoExecute",
		"Mode:                required",
		"Admitting nodes:     2 Ready",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}

	noPlacement, noPlacementError := runAgentCICommand(t, &agentCIMock{settings: newAgentCISettings()},
		"cluster", "agent", "ci", "get")
	if noPlacementError != nil || !strings.Contains(noPlacement, "Placement:             none") {
		t.Errorf("a cluster with no placement says so (%v):\n%s", noPlacementError, noPlacement)
	}
}

type nodeGroupAddPlacementMock struct {
	baseMock
	gotRequest client.AddNodeGroupRequest
}

func (m *nodeGroupAddPlacementMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Kind: "upcloud"}, nil
}

func (m *nodeGroupAddPlacementMock) AddUpcloudNodeGroup(_ context.Context, _ string, request client.AddNodeGroupRequest,
	_ bool) (*client.AddNodeGroupResult, bool, error) {
	m.gotRequest = request
	return &client.AddNodeGroupResult{GroupName: request.Name, Count: request.Count}, false, nil
}

func TestClusterNodeGroupAddSendsLabelsAndTaints(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &nodeGroupAddPlacementMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAddCmd) })
	_ = captureStdout(t, func() {
		if _, runError := executeCommand("cluster", "node-group", "add", testClusterID, "--name", "pipelines",
			"--instance-type", "4xCPU-8GB", "--labels", "team=ci,tier=build",
			"--taints", "smartoptics.dev/pipelines=true:NoSchedule,dedicated", "--wait"); runError != nil {
			t.Fatalf("add: %v", runError)
		}
	})
	request := mock.gotRequest
	if request.Labels["team"] != "ci" || request.Labels["tier"] != "build" {
		t.Errorf("labels = %v", request.Labels)
	}
	if len(request.Taints) != 2 || request.Taints[0].Key != "smartoptics.dev/pipelines" ||
		request.Taints[0].Value != "true" || request.Taints[1].Effect != "NoSchedule" {
		t.Errorf("taints = %+v", request.Taints)
	}
}
