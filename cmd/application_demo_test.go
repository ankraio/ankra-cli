package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// applicationDemoMock records the demo calls the command family makes, so the
// tests can assert the request the flags built rather than the rendering.
type applicationDemoMock struct {
	baseMock
	deployPayload json.RawMessage
	detailPayload json.RawMessage
	listPayload   json.RawMessage
	logsPayload   json.RawMessage

	demoRequest client.DeployApplicationDemoRequest
	demoCalls   int
	logsPod     string
	logsTail    int
	logsCalls   int
	detailCalls int
}

func (mock *applicationDemoMock) DeployApplicationDemo(requestContext context.Context,
	applicationID string, demoRequest client.DeployApplicationDemoRequest) (json.RawMessage, error) {
	mock.demoCalls++
	mock.demoRequest = demoRequest
	return mock.deployPayload, nil
}

func (mock *applicationDemoMock) GetApplicationDemos(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	return mock.listPayload, nil
}

func (mock *applicationDemoMock) GetApplicationDemoDetail(requestContext context.Context,
	applicationID string, workspaceID string) (json.RawMessage, error) {
	mock.detailCalls++
	return mock.detailPayload, nil
}

func (mock *applicationDemoMock) GetApplicationDemoLogs(requestContext context.Context,
	applicationID string, workspaceID string, podName string, tailLines int) (json.RawMessage, error) {
	mock.logsCalls++
	mock.logsPod = podName
	mock.logsTail = tailLines
	return mock.logsPayload, nil
}

const demoDetailFixture = `{
  "demo": {
    "id": "ws-1",
    "status": "ready",
    "branch": "main",
    "namespace": "ankra-demo-br-553ae8f1",
    "preview_url": "https://demo.example",
    "expires_at": "2099-01-01T00:00:00Z",
    "components": [
      {"name": "crm-frontend", "image_tag": "sha-1363b7d", "container_port": 3000, "entry": true},
      {"name": "crm-api", "image_tag": "sha-1363b7d", "container_port": 8090, "ingress_path": "/api"}
    ]
  },
  "inspection": {
    "cluster_reachable": true,
    "steps": [{"label": "Workload", "status": "done", "detail": "Both deployments applied."}],
    "resources": [
      {"kind": "Pod", "name": "crm-api-old", "component": "crm-api", "present": true, "status": "failed"},
      {"kind": "Pod", "name": "crm-api-new", "component": "crm-api", "present": true, "status": "ready"},
      {"kind": "Pod", "name": "demo-front", "component": "crm-frontend", "present": true, "status": "ready"},
      {"kind": "Deployment", "name": "crm-api", "component": "crm-api", "present": true, "status": "ready"}
    ]
  }
}`

func TestApplicationDemoDeployMapsComponentFlags(t *testing.T) {
	mockClient := &applicationDemoMock{deployPayload: json.RawMessage(`{"workspace_id":"ws-1"}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"demo", "deploy", testApplicationID,
		"--branch", "main",
		"--component", "crm-frontend",
		"--component", "crm-api",
		"--component-tag", "crm-api=sha-1363b7d",
		"--component-port", "crm-api=8090",
		"--component-path", "crm-api=/api",
		"--entry-component", "crm-frontend",
	)
	if executeError != nil {
		t.Fatalf("demo deploy error = %v", executeError)
	}
	request := mockClient.demoRequest
	if len(request.Components) != 2 {
		t.Fatalf("components = %+v, want two", request.Components)
	}
	if request.Components[0].Name != "crm-frontend" || request.Components[1].Name != "crm-api" {
		t.Errorf("components are not in flag order: %+v", request.Components)
	}
	if request.Components[0].ImageTag != nil || request.Components[0].ContainerPort != nil ||
		request.Components[0].IngressPath != nil {
		t.Errorf("crm-frontend picked up overrides meant for crm-api: %+v", request.Components[0])
	}
	api := request.Components[1]
	if api.ImageTag == nil || *api.ImageTag != "sha-1363b7d" {
		t.Errorf("crm-api image tag = %v", api.ImageTag)
	}
	if api.ContainerPort == nil || *api.ContainerPort != 8090 {
		t.Errorf("crm-api container port = %v", api.ContainerPort)
	}
	if api.IngressPath == nil || *api.IngressPath != "/api" {
		t.Errorf("crm-api ingress path = %v", api.IngressPath)
	}
	if request.EntryComponent == nil || *request.EntryComponent != "crm-frontend" {
		t.Errorf("entry component = %v", request.EntryComponent)
	}
}

func TestApplicationDemoDeployWithoutComponentFlagsSendsNoSelection(t *testing.T) {
	mockClient := &applicationDemoMock{deployPayload: json.RawMessage(`{"workspace_id":"ws-1"}`)}
	if _, executeError := runApplicationCommand(t, mockClient,
		"demo", "deploy", testApplicationID, "--branch", "main"); executeError != nil {
		t.Fatalf("demo deploy error = %v", executeError)
	}
	if mockClient.demoRequest.Components != nil {
		t.Errorf("components = %+v, want nil so the backend deploys every component",
			mockClient.demoRequest.Components)
	}
	if mockClient.demoRequest.EntryComponent != nil {
		t.Errorf("entry component = %v, want nil", mockClient.demoRequest.EntryComponent)
	}
}

func TestApplicationDemoDeployRejectsBadComponentFlags(t *testing.T) {
	for name, arguments := range map[string][]string{
		"override for an unselected component": {
			"--component", "crm-frontend", "--component-port", "crm-api=8090"},
		"override without any selection":   {"--component-tag", "crm-api=sha-1"},
		"override missing the equals sign": {"--component", "crm-api", "--component-tag", "crm-api"},
		"override with an empty name":      {"--component", "crm-api", "--component-port", "=8090"},
		"non-numeric port":                 {"--component", "crm-api", "--component-port", "crm-api=web"},
		"component listed twice":           {"--component", "crm-api", "--component", "crm-api"},
		"empty component name":             {"--component", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			mockClient := &applicationDemoMock{}
			_, executeError := runApplicationCommand(t, mockClient,
				append([]string{"demo", "deploy", testApplicationID, "--branch", "main"}, arguments...)...)
			if exitCodeFor(executeError) != exitUsage {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
			}
			if mockClient.demoCalls != 0 {
				t.Errorf("DeployApplicationDemo calls = %d, want 0", mockClient.demoCalls)
			}
		})
	}
}

func TestApplicationDemoLogsResolvesComponentToReadyPod(t *testing.T) {
	mockClient := &applicationDemoMock{
		detailPayload: json.RawMessage(demoDetailFixture),
		logsPayload:   json.RawMessage(`{"lines":["listening on 8090"]}`),
	}
	if _, executeError := runApplicationCommand(t, mockClient,
		"demo", "logs", testApplicationID, "ws-1", "--component", "crm-api", "--tail", "500"); executeError != nil {
		t.Fatalf("demo logs error = %v", executeError)
	}
	if mockClient.logsPod != "crm-api-new" {
		t.Errorf("pod = %q, want the ready pod crm-api-new", mockClient.logsPod)
	}
	if mockClient.logsTail != 500 {
		t.Errorf("tail = %d, want 500", mockClient.logsTail)
	}
}

func TestApplicationDemoLogsWithoutComponentSkipsDetailLookup(t *testing.T) {
	mockClient := &applicationDemoMock{logsPayload: json.RawMessage(`{"lines":[]}`)}
	if _, executeError := runApplicationCommand(t, mockClient,
		"demo", "logs", testApplicationID, "ws-1"); executeError != nil {
		t.Fatalf("demo logs error = %v", executeError)
	}
	if mockClient.detailCalls != 0 {
		t.Errorf("detail calls = %d, want 0 without a component selector", mockClient.detailCalls)
	}
	if mockClient.logsPod != "" {
		t.Errorf("pod = %q, want empty so the backend picks the demo's pod", mockClient.logsPod)
	}
}

func TestApplicationDemoLogsRejectsUnknownComponent(t *testing.T) {
	mockClient := &applicationDemoMock{detailPayload: json.RawMessage(demoDetailFixture)}
	output, executeError := runApplicationCommand(t, mockClient,
		"demo", "logs", testApplicationID, "ws-1", "--component", "billing")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitNotFound, executeError)
	}
	if mockClient.logsCalls != 0 {
		t.Errorf("GetApplicationDemoLogs calls = %d, want 0", mockClient.logsCalls)
	}
	if !strings.Contains(output+executeError.Error(), "crm-api") {
		t.Errorf("error does not name the demo's components: %v", executeError)
	}
}

func TestApplicationDemoLogsRejectsComponentWithPod(t *testing.T) {
	mockClient := &applicationDemoMock{detailPayload: json.RawMessage(demoDetailFixture)}
	_, executeError := runApplicationCommand(t, mockClient,
		"demo", "logs", testApplicationID, "ws-1", "--component", "crm-api", "--pod", "crm-api-new")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if mockClient.logsCalls != 0 {
		t.Errorf("GetApplicationDemoLogs calls = %d, want 0", mockClient.logsCalls)
	}
}

func TestApplicationDemoListRendersComponents(t *testing.T) {
	mockClient := &applicationDemoMock{listPayload: json.RawMessage(`{
	  "demos": [{
	    "id": "ws-1",
	    "status": "ready",
	    "branch": "main",
	    "preview_url": "https://demo.example",
	    "expires_at": "2099-01-01T00:00:00Z",
	    "components": [
	      {"name": "crm-frontend", "entry": true},
	      {"name": "crm-api", "ingress_path": "/api"}
	    ]
	  }]
	}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "list", testApplicationID)
	if executeError != nil {
		t.Fatalf("demo list error = %v", executeError)
	}
	for _, expected := range []string{"ws-1", "ready", "main", "crm-frontend*", "crm-api", "entry component"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
}

func TestApplicationDemoListEmpty(t *testing.T) {
	mockClient := &applicationDemoMock{listPayload: json.RawMessage(`{"demos":[]}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "list", testApplicationID)
	if executeError != nil {
		t.Fatalf("demo list error = %v", executeError)
	}
	if !strings.Contains(output, "No active demo workspaces.") {
		t.Errorf("output = %q", output)
	}
}

func TestApplicationDemoListFallsBackWhenPayloadCarriesNoDemosKey(t *testing.T) {
	// "No active demo workspaces." is a claim about the application, so a
	// payload that never carried the list must print raw instead.
	mockClient := &applicationDemoMock{listPayload: json.RawMessage(`{"detail":"staging cluster missing"}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "list", testApplicationID)
	if executeError != nil {
		t.Fatalf("demo list error = %v", executeError)
	}
	if strings.Contains(output, "No active demo workspaces.") {
		t.Errorf("a payload without a demos key was summarised as empty:\n%s", output)
	}
	if !strings.Contains(output, "staging cluster missing") {
		t.Errorf("output = %q", output)
	}
}

func TestApplicationDemoListStructuredOutputStaysRaw(t *testing.T) {
	mockClient := &applicationDemoMock{listPayload: json.RawMessage(`{"demos":[],"default_container_port":3000}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "list", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("demo list error = %v", executeError)
	}
	if !strings.Contains(output, "\"default_container_port\": 3000") {
		t.Errorf("-o json dropped payload fields the summary does not render:\n%s", output)
	}
}

func TestApplicationDemoDetailRendersComponentsAndSteps(t *testing.T) {
	mockClient := &applicationDemoMock{detailPayload: json.RawMessage(demoDetailFixture)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "detail", testApplicationID, "ws-1")
	if executeError != nil {
		t.Fatalf("demo detail error = %v", executeError)
	}
	for _, expected := range []string{
		"Demo ws-1", "ready", "ankra-demo-br-553ae8f1", "https://demo.example",
		"crm-frontend", "crm-api", "8090", "/api", "sha-1363b7d", "crm-api-new",
		"Steps:", "Workload",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
}

func TestApplicationDemoDetailFallsBackToRawJSON(t *testing.T) {
	// A payload this rendering cannot decode must still print: a wire shape
	// the CLI has not caught up with is not a reason to fail a read.
	mockClient := &applicationDemoMock{detailPayload: json.RawMessage(`{"demo":"not-an-object"}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "detail", testApplicationID, "ws-1")
	if executeError != nil {
		t.Fatalf("demo detail error = %v", executeError)
	}
	if !strings.Contains(output, "not-an-object") {
		t.Errorf("output = %q", output)
	}
}

func TestApplicationDemoDetailRendersLegacySingleComponent(t *testing.T) {
	mockClient := &applicationDemoMock{detailPayload: json.RawMessage(`{
	  "demo": {"id":"ws-9","status":"ready","pr_number":42,"component":"website",
	           "image_tag":"sha-abc","container_port":3000,"expires_at":"2099-01-01T00:00:00Z"},
	  "inspection": {"cluster_reachable": true, "steps": [], "resources": []}
	}`)}
	output, executeError := runApplicationCommand(t, mockClient, "demo", "detail", testApplicationID, "ws-9")
	if executeError != nil {
		t.Fatalf("demo detail error = %v", executeError)
	}
	for _, expected := range []string{"PR #42", "website", "sha-abc", "3000"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output is missing %q:\n%s", expected, output)
		}
	}
}
