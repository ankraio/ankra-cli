package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMCPCatalog_ListsTheCuratedAdapters(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/mcp-servers/catalog" {
			t.Errorf("path = %s, want /api/v1/org/mcp-servers/catalog", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"adapters": []map[string]any{{
				"key":             "sentry",
				"display_name":    "Sentry",
				"description":     "Issue triage",
				"docs_url":        "https://docs.sentry.io/mcp",
				"transport":       "http",
				"url_placeholder": "https://mcp.sentry.dev/mcp",
				"credentials": []map[string]any{{
					"header":      "Authorization",
					"label":       "Sentry token",
					"secret":      true,
					"description": "Sentry-Bearer <token>",
				}},
				"read_tool_patterns": []string{"get_*", "list_*"},
				"recommended_tools":  []string{"get_issue", "list_issues"},
			}},
		})
	}
	testClient := newTestClient(t, handler)

	catalog, catalogError := testClient.MCPCatalog(context.Background())
	if catalogError != nil {
		t.Fatalf("MCPCatalog: %v", catalogError)
	}
	if len(catalog.Adapters) != 1 {
		t.Fatalf("adapters = %+v, want one", catalog.Adapters)
	}
	adapter := catalog.Adapters[0]
	if adapter.Key != "sentry" || adapter.URLPlaceholder != "https://mcp.sentry.dev/mcp" {
		t.Errorf("adapter = %+v", adapter)
	}
	if len(adapter.Credentials) != 1 || adapter.Credentials[0].Header != "Authorization" || !adapter.Credentials[0].Secret {
		t.Errorf("credentials = %+v", adapter.Credentials)
	}
	if len(adapter.RecommendedTools) != 2 || adapter.RecommendedTools[0] != "get_issue" {
		t.Errorf("recommended tools = %+v", adapter.RecommendedTools)
	}
}

func TestListMCPServers_ReadsTheListing(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/mcp-servers" {
			t.Errorf("path = %s, want /api/v1/org/mcp-servers", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []map[string]any{{
			"id":                "11111111-2222-3333-4444-555555555555",
			"name":              "sentry",
			"transport":         "http",
			"enabled":           true,
			"permission_tier":   "read_only",
			"adapter_key":       "sentry",
			"last_error":        "connect timeout",
			"tool_grants_count": 3,
		}})
	}
	testClient := newTestClient(t, handler)

	servers, listError := testClient.ListMCPServers(context.Background())
	if listError != nil {
		t.Fatalf("ListMCPServers: %v", listError)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %+v, want one", servers)
	}
	server := servers[0]
	if server.Name != "sentry" || !server.Enabled || server.ToolGrantsCount != 3 {
		t.Errorf("server = %+v", server)
	}
	if server.AdapterKey == nil || *server.AdapterKey != "sentry" {
		t.Errorf("adapter key = %v, want sentry", server.AdapterKey)
	}
	if server.LastError == nil || *server.LastError != "connect timeout" {
		t.Errorf("last error = %v, want connect timeout", server.LastError)
	}
}

func TestCreateMCPServer_SendsTheFullRegistration(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/mcp-servers" {
			t.Errorf("path = %s, want /api/v1/org/mcp-servers", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &receivedBody); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"id":              "11111111-2222-3333-4444-555555555555",
			"organisation_id": "org-1",
			"name":            "sentry",
			"transport":       "http",
			"url":             "https://mcp.sentry.dev/mcp",
			"env_keys":        []string{"Authorization"},
			"allowed_tools":   []string{"get_issue"},
			"enabled":         true,
			"permission_tier": "read_only",
			"adapter_key":     "sentry",
			"created_at":      "2026-08-22T10:00:00Z",
			"updated_at":      "2026-08-22T10:00:00Z",
		})
	}
	testClient := newTestClient(t, handler)

	server, createError := testClient.CreateMCPServer(context.Background(), CreateMCPServerRequest{
		Name:           "sentry",
		Transport:      "http",
		URL:            "https://mcp.sentry.dev/mcp",
		Env:            map[string]string{"Authorization": "${SECRET_SLOT:slot-1}"},
		AllowedTools:   []string{"get_issue"},
		PermissionTier: "read_only",
		AdapterKey:     "sentry",
	})
	if createError != nil {
		t.Fatalf("CreateMCPServer: %v", createError)
	}
	if server.ID != "11111111-2222-3333-4444-555555555555" || server.Name != "sentry" {
		t.Errorf("server = %+v", server)
	}
	if receivedBody["name"] != "sentry" || receivedBody["adapter_key"] != "sentry" {
		t.Errorf("body = %v, want name and adapter_key", receivedBody)
	}
	env, isObject := receivedBody["env"].(map[string]any)
	if !isObject || env["Authorization"] != "${SECRET_SLOT:slot-1}" {
		t.Errorf("env = %v, want the secret-slot sentinel, never plaintext", receivedBody["env"])
	}
	if _, present := receivedBody["enabled"]; present {
		t.Errorf("enabled = %v, want omitted when unset so the backend default applies", receivedBody["enabled"])
	}
}

func TestCreateMCPServer_SurfacesTheDuplicateNameDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"detail": "An MCP server named 'sentry' already exists."})
	}
	testClient := newTestClient(t, handler)

	_, createError := testClient.CreateMCPServer(context.Background(), CreateMCPServerRequest{
		Name: "sentry", Transport: "http", URL: "https://mcp.sentry.dev/mcp"})
	if createError == nil || createError.Error() != "An MCP server named 'sentry' already exists." {
		t.Fatalf("error = %v, want the backend detail verbatim", createError)
	}
}

func TestCreateMCPServer_FlattensPydanticValidationErrors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]any{
			"detail": []map[string]any{{
				"loc":  []any{"body", "url"},
				"msg":  "URL resolves to a private address",
				"type": "value_error",
			}},
		})
	}
	testClient := newTestClient(t, handler)

	_, createError := testClient.CreateMCPServer(context.Background(), CreateMCPServerRequest{
		Name: "internal", Transport: "http", URL: "https://mcp.example.internal/mcp"})
	if createError == nil || !strings.Contains(createError.Error(), "url: URL resolves to a private address") {
		t.Fatalf("error = %v, want the flattened field message", createError)
	}
}

func TestGetMCPServer_MapsMissingServersToTheTypedError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]any{"detail": "MCP server not found"})
	}
	testClient := newTestClient(t, handler)

	_, getError := testClient.GetMCPServer(context.Background(), "11111111-2222-3333-4444-555555555555")
	if !errors.Is(getError, ErrMCPServerNotFound) {
		t.Fatalf("error = %v, want ErrMCPServerNotFound", getError)
	}
}

func TestUpdateMCPServer_SendsAnExplicitNullToClearTheAllowList(t *testing.T) {
	var receivedRaw string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		receivedRaw = string(raw)
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "name": "sentry",
			"transport": "http", "url": "https://mcp.sentry.dev/mcp",
			"enabled": true, "permission_tier": "read_only",
			"created_at": "2026-08-22T10:00:00Z", "updated_at": "2026-08-22T10:05:00Z",
		})
	}
	testClient := newTestClient(t, handler)

	var cleared []string
	_, updateError := testClient.UpdateMCPServer(context.Background(),
		"11111111-2222-3333-4444-555555555555", MCPServerUpdate{AllowedTools: &cleared})
	if updateError != nil {
		t.Fatalf("UpdateMCPServer: %v", updateError)
	}
	if !strings.Contains(receivedRaw, `"allowed_tools":null`) {
		t.Errorf("body = %s, want an explicit null so the backend clears the allow-list", receivedRaw)
	}
	if strings.Contains(receivedRaw, "description") || strings.Contains(receivedRaw, "url") {
		t.Errorf("body = %s, want untouched members omitted", receivedRaw)
	}
}

func TestSetMCPServerEnabled_PostsToTheEnableRoute(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/api/v1/org/mcp-servers/11111111-2222-3333-4444-555555555555/enable"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"server_id": "11111111-2222-3333-4444-555555555555", "status": "enabled"})
	}
	testClient := newTestClient(t, handler)

	result, enableError := testClient.SetMCPServerEnabled(context.Background(),
		"11111111-2222-3333-4444-555555555555", true)
	if enableError != nil {
		t.Fatalf("SetMCPServerEnabled: %v", enableError)
	}
	if result.Status != "enabled" {
		t.Errorf("status = %s, want enabled", result.Status)
	}
}

func TestSetMCPServerEnabled_PostsToTheDisableRoute(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/v1/org/mcp-servers/11111111-2222-3333-4444-555555555555/disable"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"server_id": "11111111-2222-3333-4444-555555555555", "status": "disabled"})
	}
	testClient := newTestClient(t, handler)

	result, disableError := testClient.SetMCPServerEnabled(context.Background(),
		"11111111-2222-3333-4444-555555555555", false)
	if disableError != nil {
		t.Fatalf("SetMCPServerEnabled: %v", disableError)
	}
	if result.Status != "disabled" {
		t.Errorf("status = %s, want disabled", result.Status)
	}
}

func TestGrantMCPTool_SendsToolAndRole(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/api/v1/org/mcp-servers/11111111-2222-3333-4444-555555555555/grants"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &receivedBody); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"id":                "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			"org_mcp_server_id": "11111111-2222-3333-4444-555555555555",
			"tool_name":         "get_issue",
			"allowed_to_role":   "member",
			"created_at":        "2026-08-22T10:00:00Z",
		})
	}
	testClient := newTestClient(t, handler)

	grant, grantError := testClient.GrantMCPTool(context.Background(),
		"11111111-2222-3333-4444-555555555555", "get_issue", "member")
	if grantError != nil {
		t.Fatalf("GrantMCPTool: %v", grantError)
	}
	if grant.ToolName != "get_issue" || grant.AllowedToRole != "member" {
		t.Errorf("grant = %+v", grant)
	}
	if receivedBody["tool_name"] != "get_issue" || receivedBody["allowed_to_role"] != "member" {
		t.Errorf("body = %v", receivedBody)
	}
}

func TestRevokeMCPToolGrant_DeletesTheToolRolePath(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		wantPath := "/api/v1/org/mcp-servers/11111111-2222-3333-4444-555555555555/grants/get_issue/member"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"status": "revoked"})
	}
	testClient := newTestClient(t, handler)

	result, revokeError := testClient.RevokeMCPToolGrant(context.Background(),
		"11111111-2222-3333-4444-555555555555", "get_issue", "member")
	if revokeError != nil {
		t.Fatalf("RevokeMCPToolGrant: %v", revokeError)
	}
	if result.Status != "revoked" {
		t.Errorf("status = %s, want revoked", result.Status)
	}
}

func TestCreateSecretSlot_StoresTheValueAndReturnsTheSentinel(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/secret-slots" {
			t.Errorf("path = %s, want /api/v1/org/secret-slots", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &receivedBody); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"slot_id":      "slot-1",
			"label":        "sentry Authorization",
			"hint":         "Sent…oken",
			"created_at":   "2026-08-22T10:00:00Z",
			"access_count": 0,
			"sentinel":     "${SECRET_SLOT:slot-1}",
		})
	}
	testClient := newTestClient(t, handler)

	slot, createError := testClient.CreateSecretSlot(context.Background(),
		"sentry Authorization", "Sentry-Bearer token")
	if createError != nil {
		t.Fatalf("CreateSecretSlot: %v", createError)
	}
	if slot.Sentinel != "${SECRET_SLOT:slot-1}" || slot.SlotID != "slot-1" {
		t.Errorf("slot = %+v", slot)
	}
	if receivedBody["label"] != "sentry Authorization" || receivedBody["value"] != "Sentry-Bearer token" {
		t.Errorf("body = %v", receivedBody)
	}
}

func TestListSecretSlots_ReadsTheListing(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/secret-slots" {
			t.Errorf("path = %s, want /api/v1/org/secret-slots", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"slots": []map[string]any{{
				"slot_id": "slot-1", "label": "sentry Authorization",
				"hint": "Sent…oken", "created_at": "2026-08-22T10:00:00Z",
				"access_count": 2, "sentinel": "${SECRET_SLOT:slot-1}",
			}},
		})
	}
	testClient := newTestClient(t, handler)

	slots, listError := testClient.ListSecretSlots(context.Background())
	if listError != nil {
		t.Fatalf("ListSecretSlots: %v", listError)
	}
	if len(slots.Slots) != 1 || slots.Slots[0].AccessCount != 2 {
		t.Errorf("slots = %+v", slots.Slots)
	}
}

func TestDeleteSecretSlot_DeletesBySlotID(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/org/secret-slots/slot-1" {
			t.Errorf("path = %s, want /api/v1/org/secret-slots/slot-1", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"deleted": true})
	}
	testClient := newTestClient(t, handler)

	result, deleteError := testClient.DeleteSecretSlot(context.Background(), "slot-1")
	if deleteError != nil {
		t.Fatalf("DeleteSecretSlot: %v", deleteError)
	}
	if !result.Deleted {
		t.Errorf("deleted = false, want true")
	}
}

func TestDeleteMCPServer_AcceptsAnEmpty204Response(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	testClient := newTestClient(t, handler)

	result, deleteError := testClient.DeleteMCPServer(context.Background(),
		"11111111-2222-3333-4444-555555555555")
	if deleteError != nil {
		t.Fatalf("DeleteMCPServer: %v, want an empty 204 to be success", deleteError)
	}
	if result == nil || result.ServerID != "" || result.Status != "" {
		t.Errorf("result = %+v, want a zero-valued acknowledgement for an empty body", result)
	}
}

func TestDeleteSecretSlot_AcceptsAnEmpty204Response(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	testClient := newTestClient(t, handler)

	result, deleteError := testClient.DeleteSecretSlot(context.Background(), "slot-1")
	if deleteError != nil {
		t.Fatalf("DeleteSecretSlot: %v, want an empty 204 to be success", deleteError)
	}
	if result == nil || result.Deleted {
		t.Errorf("result = %+v, want a zero-valued acknowledgement for an empty body", result)
	}
}

func TestGetMCPServer_RefusesAnEmpty200Body(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	testClient := newTestClient(t, handler)

	_, getError := testClient.GetMCPServer(context.Background(),
		"11111111-2222-3333-4444-555555555555")
	if getError == nil || !strings.Contains(getError.Error(), "empty body") {
		t.Fatalf("error = %v, want a loud refusal - an empty read decoded as a zero-valued server would turn no answer into a negative one", getError)
	}
}

func TestListMCPServers_RefusesAnEmpty200Body(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	testClient := newTestClient(t, handler)

	_, listError := testClient.ListMCPServers(context.Background())
	if listError == nil || !strings.Contains(listError.Error(), "empty body") {
		t.Fatalf("error = %v, want a loud refusal instead of a silent nil listing", listError)
	}
}
