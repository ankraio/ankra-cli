package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type agentCIQuarantineMock struct {
	settings  *client.AgentCISettings
	nodes     []interface{}
	readError error
	requests  []client.GetResourcesRequest
}

func (mock *agentCIQuarantineMock) GetAgentCISettings(requestContext context.Context,
	clusterID string) (*client.AgentCISettings, error) {
	return mock.settings, nil
}

func (mock *agentCIQuarantineMock) GetResources(clusterID string,
	request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	mock.requests = append(mock.requests, request)
	if mock.readError != nil {
		return nil, mock.readError
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{{
		Status: "success", Kind: "Node", Items: mock.nodes,
	}}}, nil
}

func quarantinedNodeItem(name string, reason string, annotations map[string]interface{}) interface{} {
	return map[string]interface{}{"metadata": map[string]interface{}{
		"name": name, "labels": map[string]interface{}{ciQuarantinedNodeLabel: reason}, "annotations": annotations,
	}}
}

func TestClusterAgentCIGetNamesTheQuarantinedNodes(t *testing.T) {
	mock := &agentCIQuarantineMock{settings: newAgentCISettings(), nodes: []interface{}{
		quarantinedNodeItem("ci-large-6", "sandbox", map[string]interface{}{
			ciQuarantinedAtAnnotation:    "2026-11-27T10:05:00Z",
			ciQuarantineDetailAnnotation: "2 pods did not start here in 30m0s, the newest: sandbox",
			ciStartFailuresAnnotation:    "[]",
		}),
		quarantinedNodeItem("ci-large-2", "mount", map[string]interface{}{
			ciReplacementRequestedAnnotation: "2026-11-27T10:30:00Z", ciStartFailuresAnnotation: "[]",
		}),
		quarantinedNodeItem("ci-large-3", "stuck", map[string]interface{}{}),
	}}
	output, runError := runAgentCICommand(t, mock, "cluster", "agent", "ci", "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	for _, want := range []string{
		"Quarantined CI nodes: 3 (pipeline steps are kept off them and retried elsewhere)",
		"  ci-large-2: mount; being replaced",
		"  ci-large-3: stuck; pods start there again",
		"  ci-large-6: sandbox, since 2026-11-27T10:05:00Z (2 pods did not start here in 30m0s, the newest: sandbox)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	if len(mock.requests) != 1 || mock.requests[0].ResourceRequests[0].LabelSelector != ciQuarantinedNodeLabel {
		t.Fatalf("requests = %+v, want one node listing by the quarantine label", mock.requests)
	}
}

// None is said only when the read answered; a failed read says so, and a
// cluster with pipeline steps off is not read at all.
func TestClusterAgentCIGetSaysNoneOnlyWhenTheNodesWereRead(t *testing.T) {
	none := &agentCIQuarantineMock{settings: newAgentCISettings()}
	output, _ := runAgentCICommand(t, none, "cluster", "agent", "ci", "get")
	if !strings.Contains(output, "Quarantined CI nodes: none") {
		t.Errorf("output missing none:\n%s", output)
	}
	failed := &agentCIQuarantineMock{settings: newAgentCISettings(), readError: errors.New("agent offline")}
	output, runError := runAgentCICommand(t, failed, "cluster", "agent", "ci", "get")
	if runError != nil || !strings.Contains(output, "Quarantined CI nodes: could not be read (agent offline)") ||
		strings.Contains(output, "Quarantined CI nodes: none") {
		t.Errorf("err = %v output:\n%s", runError, output)
	}
	off := newAgentCISettings()
	off.CIWorkerCount = 0
	idle := &agentCIQuarantineMock{settings: off}
	output, _ = runAgentCICommand(t, idle, "cluster", "agent", "ci", "get")
	if len(idle.requests) != 0 || strings.Contains(output, "Quarantined") {
		t.Errorf("a cluster with pipeline steps off must not be read; requests %d output:\n%s", len(idle.requests), output)
	}
}

func TestAnAgentThatCannotListItsNodesIsNotReadAsNone(t *testing.T) {
	message := "forbidden"
	setMockClient(t, &errorListingMock{message: message})
	if _, readError := readAgentCIQuarantinedNodes("cluster"); readError == nil || readError.Error() != message {
		t.Fatalf("error = %v, want the agent's error", readError)
	}
}

type errorListingMock struct {
	message string
}

func (mock *errorListingMock) GetResources(clusterID string,
	request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{{
		Status: "error", ErrorResponse: &mock.message,
	}}}, nil
}
