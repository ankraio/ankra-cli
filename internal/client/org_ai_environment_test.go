package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestUpdateOrganisationPreviewSettingsSendsTypedValuesAndNulls(t *testing.T) {
	var received map[string]json.RawMessage
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/org/ai-environment" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		raw, _ := io.ReadAll(request.Body)
		if unmarshalError := json.Unmarshal(raw, &received); unmarshalError != nil {
			t.Fatalf("decode body %q: %v", raw, unmarshalError)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{
			"staging_cluster_id": "cluster-1", "staging_cluster_name": "staging", "staging_cluster_online": true,
			"workspace_ttl_hours": 12, "demo_ttl_hours": 48, "default_gateway_mode": "agent",
			"demo_ttl_role_caps": map[string]int{"member": 24},
		})
	})

	settings, updateError := apiClient.UpdateOrganisationPreviewSettings(context.Background(), map[string]any{
		"staging_cluster_id":  nil,
		"workspace_ttl_hours": 12,
		"demo_ttl_role_caps":  map[string]int{"member": 24},
	})
	if updateError != nil {
		t.Fatalf("update: %v", updateError)
	}
	if string(received["staging_cluster_id"]) != "null" || string(received["workspace_ttl_hours"]) != "12" ||
		string(received["demo_ttl_role_caps"]) != `{"member":24}` {
		t.Errorf("body = %v", received)
	}
	if len(received) != 3 {
		t.Errorf("only the given keys may be sent, got %v", received)
	}
	if settings.StagingClusterName == nil || *settings.StagingClusterName != "staging" || !settings.StagingClusterOnline ||
		settings.WorkspaceTTLHours != 12 || settings.DemoTTLHours != 48 || settings.DefaultGatewayMode != "agent" ||
		settings.DemoTTLRoleCaps["member"] != 24 {
		t.Errorf("settings = %+v", settings)
	}
}
