package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetApplicationDemoLogsSendsTailLinesAndPod(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/app-1/demos/ws-1/logs" {
			t.Errorf("path = %s", request.URL.Path)
		}
		// The endpoint reads tail_lines; anything else leaves the tail at
		// the backend default, which is what `tail` silently did.
		if got := request.URL.Query().Get("tail_lines"); got != "500" {
			t.Errorf("tail_lines = %q, want 500", got)
		}
		if got := request.URL.Query().Get("pod"); got != "crm-api-abc" {
			t.Errorf("pod = %q, want crm-api-abc", got)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"lines": []string{"ok"}})
	})

	if _, logsError := testClient.GetApplicationDemoLogs(context.Background(),
		"app-1", "ws-1", "crm-api-abc", 500); logsError != nil {
		t.Fatalf("GetApplicationDemoLogs error = %v", logsError)
	}
}

func TestGetApplicationDemoLogsOmitsUnsetSelectors(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty", request.URL.RawQuery)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"lines": []string{}})
	})

	if _, logsError := testClient.GetApplicationDemoLogs(context.Background(),
		"app-1", "ws-1", "", 0); logsError != nil {
		t.Fatalf("GetApplicationDemoLogs error = %v", logsError)
	}
}

func TestDeployApplicationDemoSendsComponents(t *testing.T) {
	imageTag := "sha-1363b7d"
	containerPort := 8090
	ingressPath := "/api"
	entryComponent := "crm-frontend"

	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		components, isList := body["components"].([]any)
		if !isList || len(components) != 2 {
			t.Fatalf("components = %v, want two entries", body["components"])
		}
		second, isObject := components[1].(map[string]any)
		if !isObject {
			t.Fatalf("components[1] = %v", components[1])
		}
		if second["name"] != "crm-api" || second["image_tag"] != imageTag ||
			second["container_port"] != float64(containerPort) || second["ingress_path"] != ingressPath {
			t.Errorf("components[1] = %v", second)
		}
		first, isObject := components[0].(map[string]any)
		if !isObject {
			t.Fatalf("components[0] = %v", components[0])
		}
		// Overrides are omitted rather than sent as zero values: the
		// backend resolves an absent port from the component record.
		if _, sent := first["container_port"]; sent {
			t.Errorf("components[0] carries an unset container_port: %v", first)
		}
		if body["entry_component"] != entryComponent {
			t.Errorf("entry_component = %v, want %s", body["entry_component"], entryComponent)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"workspace_id": "ws-1"})
	})

	if _, deployError := testClient.DeployApplicationDemo(context.Background(), "app-1",
		DeployApplicationDemoRequest{
			Components: []DeployApplicationDemoComponent{
				{Name: "crm-frontend"},
				{Name: "crm-api", ImageTag: &imageTag, ContainerPort: &containerPort, IngressPath: &ingressPath},
			},
			EntryComponent: &entryComponent,
		}); deployError != nil {
		t.Fatalf("DeployApplicationDemo error = %v", deployError)
	}
}

func TestDeployApplicationDemoOmitsComponentsWhenUnset(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		// An absent components key is how the backend is told to deploy
		// every recorded component; an empty list would not be.
		if _, sent := body["components"]; sent {
			t.Errorf("components was sent for an unnarrowed launch: %v", body)
		}
		if _, sent := body["entry_component"]; sent {
			t.Errorf("entry_component was sent unset: %v", body)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"workspace_id": "ws-1"})
	})

	if _, deployError := testClient.DeployApplicationDemo(context.Background(), "app-1",
		DeployApplicationDemoRequest{}); deployError != nil {
		t.Fatalf("DeployApplicationDemo error = %v", deployError)
	}
}
