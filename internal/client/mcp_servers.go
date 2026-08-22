package client

// Organisation-scoped MCP tool servers: external Model Context Protocol
// endpoints agent runs can call, plus the curated adapter catalog and the
// per-tool role grants that gate what a run may invoke. Secret header values
// never travel in plaintext: the CLI first stores them in a secret slot
// (POST /api/v1/org/secret-slots) and registers the server with the returned
// "${SECRET_SLOT:<id>}" sentinel instead - the backend refuses plaintext
// under sensitive-looking env keys.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// MCPAdapterCredential describes one credential header a curated adapter
// expects, including the exact value form (e.g. "Bearer <token>",
// "Sentry-Bearer <token>") in Description.
type MCPAdapterCredential struct {
	Header      string `json:"header"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Description string `json:"description"`
}

// MCPAdapter is one curated catalog entry: a known MCP server product with
// its transport, URL placeholder, credential expectations, and the tool
// names recommended as a starting allow-list.
type MCPAdapter struct {
	Key              string                 `json:"key"`
	DisplayName      string                 `json:"display_name"`
	Description      string                 `json:"description"`
	DocsURL          string                 `json:"docs_url"`
	Transport        string                 `json:"transport"`
	URLPlaceholder   string                 `json:"url_placeholder"`
	Credentials      []MCPAdapterCredential `json:"credentials"`
	ReadToolPatterns []string               `json:"read_tool_patterns"`
	RecommendedTools []string               `json:"recommended_tools"`
}

// MCPCatalogResult wraps the curated adapter listing.
type MCPCatalogResult struct {
	Adapters []MCPAdapter `json:"adapters"`
}

// MCPServerListItem is the compact per-server row the listing endpoint
// returns.
type MCPServerListItem struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Transport       string  `json:"transport"`
	Enabled         bool    `json:"enabled"`
	PermissionTier  string  `json:"permission_tier"`
	AdapterKey      *string `json:"adapter_key,omitempty"`
	LastConnectedAt *string `json:"last_connected_at,omitempty"`
	LastError       *string `json:"last_error,omitempty"`
	ToolGrantsCount int     `json:"tool_grants_count"`
}

// MCPServer is the full server object. EnvKeys lists the configured env/header
// names only - values (secret-slot sentinels included) are never echoed back.
type MCPServer struct {
	ID               string   `json:"id"`
	OrganisationID   string   `json:"organisation_id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Transport        string   `json:"transport"`
	URL              string   `json:"url"`
	EnvKeys          []string `json:"env_keys,omitempty"`
	AllowedTools     []string `json:"allowed_tools,omitempty"`
	Enabled          bool     `json:"enabled"`
	PermissionTier   string   `json:"permission_tier"`
	AdapterKey       *string  `json:"adapter_key,omitempty"`
	ClusterAllowList []string `json:"cluster_allow_list,omitempty"`
	CreatedByUserID  string   `json:"created_by_user_id,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	LastConnectedAt  *string  `json:"last_connected_at,omitempty"`
	LastError        *string  `json:"last_error,omitempty"`
}

// CreateMCPServerRequest is the wire shape for registering a server. Env
// values that carry credentials must be secret-slot sentinels - the backend
// refuses plaintext under sensitive-looking keys.
type CreateMCPServerRequest struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Transport        string            `json:"transport"`
	URL              string            `json:"url"`
	Env              map[string]string `json:"env,omitempty"`
	AllowedTools     []string          `json:"allowed_tools,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	PermissionTier   string            `json:"permission_tier,omitempty"`
	AdapterKey       string            `json:"adapter_key,omitempty"`
	ClusterAllowList []string          `json:"cluster_allow_list,omitempty"`
}

// MCPServerUpdate carries a partial update; only non-nil members are sent, so
// an untouched field keeps its server-side value. A pointer to a nil slice
// (AllowedTools, ClusterAllowList) or to an empty string (AdapterKey) sends
// an explicit JSON null, which clears the field.
type MCPServerUpdate struct {
	Description      *string
	URL              *string
	Env              map[string]string
	AllowedTools     *[]string
	Enabled          *bool
	PermissionTier   *string
	AdapterKey       *string
	ClusterAllowList *[]string
}

// payload builds the partial-update JSON body, translating the clear
// sentinels (nil slice, empty adapter key) into explicit nulls.
func (u MCPServerUpdate) payload() map[string]any {
	body := map[string]any{}
	if u.Description != nil {
		body["description"] = *u.Description
	}
	if u.URL != nil {
		body["url"] = *u.URL
	}
	if u.Env != nil {
		body["env"] = u.Env
	}
	if u.AllowedTools != nil {
		if *u.AllowedTools == nil {
			body["allowed_tools"] = nil
		} else {
			body["allowed_tools"] = *u.AllowedTools
		}
	}
	if u.Enabled != nil {
		body["enabled"] = *u.Enabled
	}
	if u.PermissionTier != nil {
		body["permission_tier"] = *u.PermissionTier
	}
	if u.AdapterKey != nil {
		if *u.AdapterKey == "" {
			body["adapter_key"] = nil
		} else {
			body["adapter_key"] = *u.AdapterKey
		}
	}
	if u.ClusterAllowList != nil {
		if *u.ClusterAllowList == nil {
			body["cluster_allow_list"] = nil
		} else {
			body["cluster_allow_list"] = *u.ClusterAllowList
		}
	}
	return body
}

// MCPServerActionResult is the acknowledgement shape shared by delete,
// enable, and disable: the server id and the state it ended in.
type MCPServerActionResult struct {
	ServerID string `json:"server_id"`
	Status   string `json:"status"`
}

// MCPToolGrant is one per-tool role grant on a server.
type MCPToolGrant struct {
	ID             string `json:"id"`
	OrgMCPServerID string `json:"org_mcp_server_id"`
	ToolName       string `json:"tool_name"`
	AllowedToRole  string `json:"allowed_to_role"`
	CreatedAt      string `json:"created_at"`
}

// mcpGrantRequest is the wire shape for creating a tool grant.
type mcpGrantRequest struct {
	ToolName      string `json:"tool_name"`
	AllowedToRole string `json:"allowed_to_role,omitempty"`
}

// MCPToolGrantRevokeResult acknowledges a grant revocation.
type MCPToolGrantRevokeResult struct {
	Status string `json:"status"`
}

// SecretSlot is a stored secret value referenced by its sentinel. The value
// itself is write-only: only the label, a hint, and access metadata come
// back.
type SecretSlot struct {
	SlotID         string  `json:"slot_id"`
	Label          string  `json:"label"`
	Hint           string  `json:"hint"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
	LastAccessedAt *string `json:"last_accessed_at,omitempty"`
	AccessCount    int     `json:"access_count"`
	Sentinel       string  `json:"sentinel"`
}

// SecretSlotListResult wraps the secret-slot listing.
type SecretSlotListResult struct {
	Slots []SecretSlot `json:"slots"`
}

// SecretSlotDeleteResult acknowledges a slot deletion.
type SecretSlotDeleteResult struct {
	Deleted bool `json:"deleted"`
}

// createSecretSlotRequest is the wire shape for storing a secret value.
type createSecretSlotRequest struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ErrMCPServerNotFound is returned when the backend reports 404 for a server
// id. Callers use errors.Is to map it to a friendly not-found exit.
var ErrMCPServerNotFound = errors.New("MCP server not found")

func (c *Client) mcpServersURL(suffix string) string {
	return c.BaseURL + "/api/v1/org/mcp-servers" + suffix
}

// MCPCatalog lists the curated MCP adapters with their credential
// expectations and recommended tool allow-lists.
func (c *Client) MCPCatalog(ctx context.Context) (*MCPCatalogResult, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL("/catalog"), nil)
	if requestError != nil {
		return nil, requestError
	}
	var catalog MCPCatalogResult
	if decodeError := decodeMCPResponse(body, &catalog, false); decodeError != nil {
		return nil, decodeError
	}
	return &catalog, nil
}

// ListMCPServers lists the organisation's registered MCP servers.
func (c *Client) ListMCPServers(ctx context.Context) ([]MCPServerListItem, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL(""), nil)
	if requestError != nil {
		return nil, requestError
	}
	var servers []MCPServerListItem
	if decodeError := decodeMCPResponse(body, &servers, false); decodeError != nil {
		return nil, decodeError
	}
	return servers, nil
}

// GetMCPServer reads one server by id.
func (c *Client) GetMCPServer(ctx context.Context, serverID string) (*MCPServer, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL("/"+neturl.PathEscape(serverID)), nil)
	if requestError != nil {
		return nil, requestError
	}
	var server MCPServer
	if decodeError := decodeMCPResponse(body, &server, false); decodeError != nil {
		return nil, decodeError
	}
	return &server, nil
}

// CreateMCPServer registers a new server. The backend returns 409 on a
// duplicate name and 422 for validation refusals (SSRF-refused URL, unknown
// adapter key, plaintext secret env values); both surface their detail text.
func (c *Client) CreateMCPServer(ctx context.Context, request CreateMCPServerRequest) (*MCPServer, error) {
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doMCPRequest(ctx, http.MethodPost, c.mcpServersURL(""), encoded)
	if requestError != nil {
		return nil, requestError
	}
	var server MCPServer
	if decodeError := decodeMCPResponse(body, &server, false); decodeError != nil {
		return nil, decodeError
	}
	return &server, nil
}

// UpdateMCPServer applies a partial update to a server.
func (c *Client) UpdateMCPServer(ctx context.Context, serverID string, update MCPServerUpdate) (*MCPServer, error) {
	encoded, marshalError := json.Marshal(update.payload())
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doMCPRequest(ctx, http.MethodPut, c.mcpServersURL("/"+neturl.PathEscape(serverID)), encoded)
	if requestError != nil {
		return nil, requestError
	}
	var server MCPServer
	if decodeError := decodeMCPResponse(body, &server, false); decodeError != nil {
		return nil, decodeError
	}
	return &server, nil
}

// DeleteMCPServer removes a server (its grants go with it).
func (c *Client) DeleteMCPServer(ctx context.Context, serverID string) (*MCPServerActionResult, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodDelete, c.mcpServersURL("/"+neturl.PathEscape(serverID)), nil)
	if requestError != nil {
		return nil, requestError
	}
	var result MCPServerActionResult
	if decodeError := decodeMCPResponse(body, &result, true); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// SetMCPServerEnabled flips a server on or off without touching its
// configuration.
func (c *Client) SetMCPServerEnabled(ctx context.Context, serverID string, enabled bool) (*MCPServerActionResult, error) {
	action := "/disable"
	if enabled {
		action = "/enable"
	}
	body, requestError := c.doMCPRequest(ctx, http.MethodPost, c.mcpServersURL("/"+neturl.PathEscape(serverID)+action), nil)
	if requestError != nil {
		return nil, requestError
	}
	var result MCPServerActionResult
	if decodeError := decodeMCPResponse(body, &result, true); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// GetMCPServerHealth probes a server's reachability and allowed tool
// inventory. The document shape is backend-defined, so it comes back raw for
// the caller to pass through or inspect.
func (c *Client) GetMCPServerHealth(ctx context.Context, serverID string) (json.RawMessage, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL("/"+neturl.PathEscape(serverID)+"/health"), nil)
	if requestError != nil {
		return nil, requestError
	}
	return json.RawMessage(body), nil
}

// ListMCPServerTools reads a server's tool inventory. Like the health probe,
// the document shape is backend-defined and returned raw.
func (c *Client) ListMCPServerTools(ctx context.Context, serverID string) (json.RawMessage, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL("/"+neturl.PathEscape(serverID)+"/tools"), nil)
	if requestError != nil {
		return nil, requestError
	}
	return json.RawMessage(body), nil
}

// ListMCPToolGrants lists the per-tool role grants on a server.
func (c *Client) ListMCPToolGrants(ctx context.Context, serverID string) ([]MCPToolGrant, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.mcpServersURL("/"+neturl.PathEscape(serverID)+"/grants"), nil)
	if requestError != nil {
		return nil, requestError
	}
	var grants []MCPToolGrant
	if decodeError := decodeMCPResponse(body, &grants, false); decodeError != nil {
		return nil, decodeError
	}
	return grants, nil
}

// GrantMCPTool grants one tool to a role on a server. An empty role lets the
// backend apply its default. 409 (grant already exists) surfaces its detail.
func (c *Client) GrantMCPTool(ctx context.Context, serverID, toolName, role string) (*MCPToolGrant, error) {
	encoded, marshalError := json.Marshal(mcpGrantRequest{ToolName: toolName, AllowedToRole: role})
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doMCPRequest(ctx, http.MethodPost, c.mcpServersURL("/"+neturl.PathEscape(serverID)+"/grants"), encoded)
	if requestError != nil {
		return nil, requestError
	}
	var grant MCPToolGrant
	if decodeError := decodeMCPResponse(body, &grant, false); decodeError != nil {
		return nil, decodeError
	}
	return &grant, nil
}

// RevokeMCPToolGrant removes the grant of one tool to one role.
func (c *Client) RevokeMCPToolGrant(ctx context.Context, serverID, toolName, role string) (*MCPToolGrantRevokeResult, error) {
	url := c.mcpServersURL("/" + neturl.PathEscape(serverID) + "/grants/" + neturl.PathEscape(toolName) + "/" + neturl.PathEscape(role))
	body, requestError := c.doMCPRequest(ctx, http.MethodDelete, url, nil)
	if requestError != nil {
		return nil, requestError
	}
	var result MCPToolGrantRevokeResult
	if decodeError := decodeMCPResponse(body, &result, true); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// CreateSecretSlot stores a secret value server-side and returns the slot,
// whose Sentinel ("${SECRET_SLOT:<id>}") is what an MCP server env value
// should carry instead of the plaintext.
func (c *Client) CreateSecretSlot(ctx context.Context, label, value string) (*SecretSlot, error) {
	encoded, marshalError := json.Marshal(createSecretSlotRequest{Label: label, Value: value})
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doMCPRequest(ctx, http.MethodPost, c.BaseURL+"/api/v1/org/secret-slots", encoded)
	if requestError != nil {
		return nil, requestError
	}
	var slot SecretSlot
	if decodeError := decodeMCPResponse(body, &slot, false); decodeError != nil {
		return nil, decodeError
	}
	return &slot, nil
}

// ListSecretSlots lists the organisation's secret slots (labels and access
// metadata only; values never come back).
func (c *Client) ListSecretSlots(ctx context.Context) (*SecretSlotListResult, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodGet, c.BaseURL+"/api/v1/org/secret-slots", nil)
	if requestError != nil {
		return nil, requestError
	}
	var slots SecretSlotListResult
	if decodeError := decodeMCPResponse(body, &slots, false); decodeError != nil {
		return nil, decodeError
	}
	return &slots, nil
}

// DeleteSecretSlot removes a secret slot. Servers still referencing its
// sentinel will fail their next connection, so re-point them first.
func (c *Client) DeleteSecretSlot(ctx context.Context, slotID string) (*SecretSlotDeleteResult, error) {
	body, requestError := c.doMCPRequest(ctx, http.MethodDelete, c.BaseURL+"/api/v1/org/secret-slots/"+neturl.PathEscape(slotID), nil)
	if requestError != nil {
		return nil, requestError
	}
	var result SecretSlotDeleteResult
	if decodeError := decodeMCPResponse(body, &result, true); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// doMCPRequest sends an authenticated JSON request to the MCP server and
// secret-slot routes. 404 maps to ErrMCPServerNotFound; RBAC 403s map to
// PermissionDeniedError; other refusals (409 duplicate name or grant, 422
// validation such as an SSRF-refused URL or a plaintext secret env value)
// surface the backend's detail - those texts are user-actionable.
func (c *Client) doMCPRequest(ctx context.Context, method, url string, payload []byte) ([]byte, error) {
	var bodyReader *bytes.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	var request *http.Request
	var requestError error
	if bodyReader != nil {
		request, requestError = http.NewRequestWithContext(ctx, method, url, bodyReader)
	} else {
		request, requestError = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return responseBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrMCPServerNotFound
	default:
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		if detail := mcpDetailFromBody(responseBody); detail != "" {
			return nil, errors.New(detail)
		}
		return nil, newUnexpectedResponseError("MCP server request failed",
			response.StatusCode, redactedBodyForError(responseBody, 500))
	}
}

// decodeMCPResponse unmarshals a success-response body into target.
//
// allowEmpty says whether an empty body - a 204, or a 200 with no content -
// counts as success with target left zero-valued. Only the acknowledgement
// calls (delete, enable/disable, revoke, secret-slot delete) pass true: for
// them the action already happened and the body only restates it. Reads keep
// failing loudly, because an empty answer decoded as a zero-valued server or
// a nil list would silently turn "no answer" into a negative one.
func decodeMCPResponse(body []byte, target any, allowEmpty bool) error {
	if len(bytes.TrimSpace(body)) == 0 {
		if allowEmpty {
			return nil
		}
		return errors.New("parse response: the server returned an empty body")
	}
	if decodeError := json.Unmarshal(body, target); decodeError != nil {
		return fmt.Errorf("parse response: %w", decodeError)
	}
	return nil
}

// mcpDetailFromBody extracts a human-readable message from a FastAPI error
// body: either the plain {"detail": "..."} string, or the pydantic 422 shape
// {"detail": [{"loc": [...], "msg": "..."}]} flattened to "field: msg" lines.
func mcpDetailFromBody(body []byte) string {
	var stringEnvelope struct {
		Detail string `json:"detail"`
	}
	if unmarshalError := json.Unmarshal(body, &stringEnvelope); unmarshalError == nil && stringEnvelope.Detail != "" {
		return stringEnvelope.Detail
	}

	var validationEnvelope struct {
		Detail []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		} `json:"detail"`
	}
	if unmarshalError := json.Unmarshal(body, &validationEnvelope); unmarshalError != nil || len(validationEnvelope.Detail) == 0 {
		return ""
	}
	messages := make([]string, 0, len(validationEnvelope.Detail))
	for _, item := range validationEnvelope.Detail {
		var path string
		for _, member := range item.Loc {
			if member == "body" {
				continue
			}
			if path != "" {
				path += "."
			}
			path += fmt.Sprintf("%v", member)
		}
		if path != "" {
			messages = append(messages, path+": "+item.Msg)
		} else {
			messages = append(messages, item.Msg)
		}
	}
	joined := ""
	for index, message := range messages {
		if index > 0 {
			joined += "; "
		}
		joined += message
	}
	return joined
}
