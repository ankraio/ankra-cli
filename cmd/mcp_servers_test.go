package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// resetMCPServersAddFlags restores the add command's flags to their defaults
// so tests do not inherit values from earlier executions of the shared cobra
// tree. Array flags need SliceValue.Replace: re-setting their textual
// default would append it as a literal element instead of clearing.
func resetMCPServersAddFlags(t *testing.T) {
	t.Helper()
	mcpServersAddCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if sliceValue, isSlice := flag.Value.(pflag.SliceValue); isSlice {
			_ = sliceValue.Replace(nil)
		} else {
			_ = flag.Value.Set(flag.DefValue)
		}
		flag.Changed = false
	})
}

const mcpTestServerID = "11111111-2222-3333-4444-555555555555"

type mcpServersMock struct {
	baseMock
	servers           []client.MCPServerListItem
	catalog           *client.MCPCatalogResult
	createdRequest    *client.CreateMCPServerRequest
	createdSlots      []createdSecretSlot
	createServerError error
	deleteSlotError   error
	deletedSlotIDs    []string
	grantedTool       string
	grantedRole       string
	grantedServerID   string
	deletedServerIDs  []string
}

type createdSecretSlot struct {
	label string
	value string
}

func (m *mcpServersMock) ListMCPServers(ctx context.Context) ([]client.MCPServerListItem, error) {
	return m.servers, nil
}

func (m *mcpServersMock) MCPCatalog(ctx context.Context) (*client.MCPCatalogResult, error) {
	if m.catalog != nil {
		return m.catalog, nil
	}
	return &client.MCPCatalogResult{}, nil
}

func (m *mcpServersMock) CreateSecretSlot(ctx context.Context, label, value string) (*client.SecretSlot, error) {
	m.createdSlots = append(m.createdSlots, createdSecretSlot{label: label, value: value})
	slotID := fmt.Sprintf("slot-%d", len(m.createdSlots))
	return &client.SecretSlot{
		SlotID:   slotID,
		Label:    label,
		Sentinel: fmt.Sprintf("${SECRET_SLOT:%s}", slotID),
	}, nil
}

func (m *mcpServersMock) DeleteSecretSlot(ctx context.Context, slotID string) (*client.SecretSlotDeleteResult, error) {
	if m.deleteSlotError != nil {
		return nil, m.deleteSlotError
	}
	m.deletedSlotIDs = append(m.deletedSlotIDs, slotID)
	return &client.SecretSlotDeleteResult{Deleted: true}, nil
}

func (m *mcpServersMock) CreateMCPServer(ctx context.Context, request client.CreateMCPServerRequest) (*client.MCPServer, error) {
	m.createdRequest = &request
	if m.createServerError != nil {
		return nil, m.createServerError
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return &client.MCPServer{
		ID:             mcpTestServerID,
		Name:           request.Name,
		Transport:      request.Transport,
		URL:            request.URL,
		AllowedTools:   request.AllowedTools,
		Enabled:        enabled,
		PermissionTier: request.PermissionTier,
		CreatedAt:      "2026-08-22T10:00:00Z",
		UpdatedAt:      "2026-08-22T10:00:00Z",
	}, nil
}

func (m *mcpServersMock) GrantMCPTool(ctx context.Context, serverID, toolName, role string) (*client.MCPToolGrant, error) {
	m.grantedServerID = serverID
	m.grantedTool = toolName
	m.grantedRole = role
	return &client.MCPToolGrant{
		ID:             "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		OrgMCPServerID: serverID,
		ToolName:       toolName,
		AllowedToRole:  role,
		CreatedAt:      "2026-08-22T10:00:00Z",
	}, nil
}

func (m *mcpServersMock) DeleteMCPServer(ctx context.Context, serverID string) (*client.MCPServerActionResult, error) {
	m.deletedServerIDs = append(m.deletedServerIDs, serverID)
	return &client.MCPServerActionResult{ServerID: serverID, Status: "deleted"}, nil
}

func mcpServerListFixture(name string, adapterKey string, lastError string) client.MCPServerListItem {
	item := client.MCPServerListItem{
		ID:              mcpTestServerID,
		Name:            name,
		Transport:       "http",
		Enabled:         true,
		PermissionTier:  "read_only",
		ToolGrantsCount: 2,
	}
	if adapterKey != "" {
		item.AdapterKey = &adapterKey
	}
	if lastError != "" {
		item.LastError = &lastError
	}
	return item
}

func TestOrgMCPServersListCommand(t *testing.T) {
	mock := &mcpServersMock{servers: []client.MCPServerListItem{
		mcpServerListFixture("sentry", "sentry", "connect timeout"),
	}}
	setMockClient(t, mock)

	output, executeError := executeCommand("org", "mcp-servers", "list")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if !strings.Contains(output, "sentry") {
		t.Errorf("expected server name in output, got: %s", output)
	}
	if !strings.Contains(output, "read_only") {
		t.Errorf("expected permission tier in output, got: %s", output)
	}
	if !strings.Contains(output, "connect timeout") {
		t.Errorf("expected last error in output, got: %s", output)
	}
}

func TestOrgMCPServersListEmptyCommand(t *testing.T) {
	mock := &mcpServersMock{}
	setMockClient(t, mock)

	output, executeError := executeCommand("org", "mcp-servers", "list")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if !strings.Contains(output, "No MCP servers") {
		t.Errorf("expected empty-state message, got: %s", output)
	}
}

func TestOrgMCPServersAddSecretHeaderAndSeedingCommand(t *testing.T) {
	mock := &mcpServersMock{catalog: &client.MCPCatalogResult{Adapters: []client.MCPAdapter{{
		Key:              "sentry",
		DisplayName:      "Sentry",
		Transport:        "http",
		URLPlaceholder:   "https://mcp.sentry.dev/mcp",
		RecommendedTools: []string{"get_issue", "list_issues"},
	}}}}
	setMockClient(t, mock)
	resetMCPServersAddFlags(t)

	output, executeError := executeCommand("org", "mcp-servers", "add", "sentry",
		"--url", "https://mcp.sentry.dev/mcp",
		"--adapter", "sentry",
		"--tier", "read_only",
		"--secret-header", "Authorization=Sentry-Bearer tok-123")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}

	if len(mock.createdSlots) != 1 {
		t.Fatalf("expected one secret slot to be created, got: %v", mock.createdSlots)
	}
	if mock.createdSlots[0].label != "sentry Authorization" {
		t.Errorf("slot label = %q, want \"sentry Authorization\"", mock.createdSlots[0].label)
	}
	if mock.createdSlots[0].value != "Sentry-Bearer tok-123" {
		t.Errorf("slot value = %q, want the raw header value", mock.createdSlots[0].value)
	}

	if mock.createdRequest == nil {
		t.Fatal("expected a create request to be sent")
	}
	if mock.createdRequest.AdapterKey != "sentry" {
		t.Errorf("adapter_key = %q, want sentry", mock.createdRequest.AdapterKey)
	}
	if mock.createdRequest.Env["Authorization"] != "${SECRET_SLOT:slot-1}" {
		t.Errorf("env = %v, want the secret-slot sentinel, never the plaintext value", mock.createdRequest.Env)
	}
	if len(mock.createdRequest.AllowedTools) != 2 ||
		mock.createdRequest.AllowedTools[0] != "get_issue" ||
		mock.createdRequest.AllowedTools[1] != "list_issues" {
		t.Errorf("allowed_tools = %v, want the adapter's recommended tools seeded", mock.createdRequest.AllowedTools)
	}
	if strings.Contains(output, "Sentry-Bearer tok-123") {
		t.Errorf("the secret value must never be echoed, got: %s", output)
	}
	if !strings.Contains(output, "registered") {
		t.Errorf("expected registration confirmation, got: %s", output)
	}
}

func TestOrgMCPServersAddExplicitAllowedToolsSkipSeedingCommand(t *testing.T) {
	mock := &mcpServersMock{catalog: &client.MCPCatalogResult{Adapters: []client.MCPAdapter{{
		Key:              "sentry",
		RecommendedTools: []string{"get_issue", "list_issues"},
	}}}}
	setMockClient(t, mock)
	resetMCPServersAddFlags(t)

	_, executeError := executeCommand("org", "mcp-servers", "add", "sentry-custom",
		"--url", "https://mcp.sentry.dev/mcp",
		"--adapter", "sentry",
		"--allowed-tools", "search_events")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if mock.createdRequest == nil {
		t.Fatal("expected a create request to be sent")
	}
	if len(mock.createdRequest.AllowedTools) != 1 || mock.createdRequest.AllowedTools[0] != "search_events" {
		t.Errorf("allowed_tools = %v, want only the explicit list", mock.createdRequest.AllowedTools)
	}
}

func TestOrgMCPServersGrantCommand(t *testing.T) {
	mock := &mcpServersMock{servers: []client.MCPServerListItem{
		mcpServerListFixture("sentry", "sentry", ""),
	}}
	setMockClient(t, mock)

	output, executeError := executeCommand("org", "mcp-servers", "grant", "sentry", "get_issue", "--role", "member")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if mock.grantedServerID != mcpTestServerID {
		t.Errorf("granted server id = %q, want the resolved id %q", mock.grantedServerID, mcpTestServerID)
	}
	if mock.grantedTool != "get_issue" || mock.grantedRole != "member" {
		t.Errorf("granted tool/role = %q/%q, want get_issue/member", mock.grantedTool, mock.grantedRole)
	}
	if !strings.Contains(output, "Granted tool") {
		t.Errorf("expected grant confirmation, got: %s", output)
	}
}

func TestOrgMCPServersRemoveWithYesCommand(t *testing.T) {
	mock := &mcpServersMock{servers: []client.MCPServerListItem{
		mcpServerListFixture("sentry", "", ""),
	}}
	setMockClient(t, mock)

	output, executeError := executeCommand("org", "mcp-servers", "remove", "sentry", "--yes")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if len(mock.deletedServerIDs) != 1 || mock.deletedServerIDs[0] != mcpTestServerID {
		t.Errorf("deleted server ids = %v, want the resolved id %q", mock.deletedServerIDs, mcpTestServerID)
	}
	if !strings.Contains(output, "deleted") {
		t.Errorf("expected delete confirmation, got: %s", output)
	}
}

func TestOrgMCPServersResolveUnknownNameCommand(t *testing.T) {
	mock := &mcpServersMock{servers: []client.MCPServerListItem{
		mcpServerListFixture("sentry", "", ""),
	}}
	setMockClient(t, mock)

	output, executeError := executeCommand("org", "mcp-servers", "remove", "linear", "--yes")
	if executeError == nil {
		t.Fatal("expected an error for an unknown server name")
	}
	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d, want exitNotFound", exitCodeFor(executeError))
	}
	if !strings.Contains(executeError.Error(), "Available servers: sentry") {
		t.Errorf("expected the available-servers hint, got: %v", executeError)
	}
	if len(mock.deletedServerIDs) != 0 {
		t.Errorf("nothing should be deleted on a resolve miss, got: %v (output: %s)", mock.deletedServerIDs, output)
	}
}

func TestOrgMCPServersAddFailureCleansUpSecretSlotsCommand(t *testing.T) {
	mock := &mcpServersMock{
		createServerError: errors.New("An MCP server named 'sentry' already exists."),
	}
	setMockClient(t, mock)
	resetMCPServersAddFlags(t)

	_, executeError := executeCommand("org", "mcp-servers", "add", "sentry",
		"--url", "https://mcp.sentry.dev/mcp",
		"--secret-header", "Authorization=Sentry-Bearer tok-123")
	if executeError == nil {
		t.Fatal("expected the registration failure to surface")
	}
	if len(mock.createdSlots) != 1 {
		t.Fatalf("expected one secret slot to have been created, got: %v", mock.createdSlots)
	}
	if len(mock.deletedSlotIDs) != 1 || mock.deletedSlotIDs[0] != "slot-1" {
		t.Errorf("deleted slot ids = %v, want the orphaned slot-1 removed", mock.deletedSlotIDs)
	}
	if !strings.Contains(executeError.Error(), "already exists") {
		t.Errorf("expected the backend refusal in the error, got: %v", executeError)
	}
	if !strings.Contains(executeError.Error(), "removed the 1 secret slot(s) created for this registration") {
		t.Errorf("expected the cleanup note in the error, got: %v", executeError)
	}
}

func TestOrgMCPServersAddFailureNamesUndeletableSlotsCommand(t *testing.T) {
	mock := &mcpServersMock{
		createServerError: errors.New("An MCP server named 'sentry' already exists."),
		deleteSlotError:   errors.New("secret-slot service unavailable"),
	}
	setMockClient(t, mock)
	resetMCPServersAddFlags(t)

	output, executeError := executeCommand("org", "mcp-servers", "add", "sentry",
		"--url", "https://mcp.sentry.dev/mcp",
		"--secret-header", "Authorization=Sentry-Bearer tok-123")
	if executeError == nil {
		t.Fatal("expected the registration failure to surface")
	}
	if !strings.Contains(output, "could not remove secret slot(s) slot-1") {
		t.Errorf("expected the undeletable slot id to be named, got output: %s (error: %v)", output, executeError)
	}
}

func TestOrgMCPServersAddTwoPipedSecretHeadersCommand(t *testing.T) {
	mock := &mcpServersMock{}
	setMockClient(t, mock)
	resetMCPServersAddFlags(t)
	rootCmd.SetIn(strings.NewReader("value-one\nvalue-two\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	_, executeError := executeCommand("org", "mcp-servers", "add", "piped-secrets",
		"--url", "https://mcp.example.com/mcp",
		"--secret-header", "Authorization",
		"--secret-header", "X-Api-Key")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if len(mock.createdSlots) != 2 {
		t.Fatalf("expected both piped secret headers to create slots, got: %v", mock.createdSlots)
	}
	if mock.createdSlots[0].value != "value-one" || mock.createdSlots[1].value != "value-two" {
		t.Errorf("slot values = %+v, want one stdin line per prompted header (a per-prompt reader would drop the second line)", mock.createdSlots)
	}
	if mock.createdRequest == nil {
		t.Fatal("expected a create request to be sent")
	}
	if mock.createdRequest.Env["Authorization"] != "${SECRET_SLOT:slot-1}" ||
		mock.createdRequest.Env["X-Api-Key"] != "${SECRET_SLOT:slot-2}" {
		t.Errorf("env = %v, want both sentinels", mock.createdRequest.Env)
	}
}
