package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const mcpTestToken = "ankra_secret_value_never_printed"

type mcpConnectMock struct {
	baseMock
	created []client.CreateAPITokenRequest
	revoked []string
}

func (mock *mcpConnectMock) CreateAPIToken(request client.CreateAPITokenRequest) (*client.CreateAPITokenResponse, error) {
	mock.created = append(mock.created, request)
	return &client.CreateAPITokenResponse{ID: "token-new", Token: mcpTestToken}, nil
}

func (mock *mcpConnectMock) RevokeAPIToken(tokenID string) (*client.RevokeAPITokenResponse, error) {
	mock.revoked = append(mock.revoked, tokenID)
	return &client.RevokeAPITokenResponse{Success: true}, nil
}

type mcpClientCommands struct {
	calls   [][]string
	failAdd bool
}

func stubMCPEnvironment(t *testing.T, isTerminal bool) (*mcpClientCommands, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	commands := &mcpClientCommands{}
	previousRun, previousLook, previousSkills, previousTerminal := runMCPClientCommand, lookMCPClientCommand, runMCPSkillsInstall, isMCPOutputTerminal
	runMCPClientCommand = func(name string, arguments ...string) ([]byte, error) {
		commands.calls = append(commands.calls, append([]string{name}, arguments...))
		if commands.failAdd && len(arguments) > 1 && arguments[1] == "add-json" {
			return []byte("nope"), errors.New("exit 1")
		}
		return nil, nil
	}
	lookMCPClientCommand = func(string) (string, error) { return "/bin/true", nil }
	runMCPSkillsInstall = func(io.Writer, string) error { return nil }
	isMCPOutputTerminal = func(any) bool { return isTerminal }
	t.Cleanup(func() {
		runMCPClientCommand, lookMCPClientCommand, runMCPSkillsInstall, isMCPOutputTerminal = previousRun, previousLook, previousSkills, previousTerminal
		for _, command := range []string{"client", "output"} {
			_ = mcpConnectCmd.Flags().Set(command, "")
		}
		_ = mcpConnectCmd.Flags().Set("write", "false")
		_ = mcpConnectCmd.Flags().Set("skills", "false")
		_ = mcpConnectCmd.Flags().Set("expires-days", "90")
		_ = mcpHeadersCmd.Flags().Set("client", "")
		_ = mcpTokenCmd.Flags().Set("client", "")
	})
	return commands, home
}

func assertNoMCPSecret(t *testing.T, where string, content string) {
	t.Helper()
	if strings.Contains(content, mcpTestToken) {
		t.Errorf("the token leaked into %s: %s", where, content)
	}
}

func TestMCPConnectClaudeCodeKeepsTheTokenOutOfEverythingButItsFile(t *testing.T) {
	commands, home := stubMCPEnvironment(t, false)
	mock := &mcpConnectMock{}
	setMockClient(t, mock)

	output, executeError := executeCommand("mcp", "connect", "--client", "claude-code")
	if executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	assertNoMCPSecret(t, "the output", output)
	for _, call := range commands.calls {
		assertNoMCPSecret(t, "a command line", strings.Join(call, " "))
	}
	lastCall := commands.calls[len(commands.calls)-1]
	if lastCall[2] != "add-json" || !strings.Contains(lastCall[len(lastCall)-1], "headersHelper") ||
		!strings.Contains(lastCall[len(lastCall)-1], "mcp headers --client claude-code") {
		t.Errorf("claude was not given the helper: %v", lastCall)
	}

	if len(mock.created) != 1 {
		t.Fatalf("created = %+v", mock.created)
	}
	created := mock.created[0]
	if strings.Join(created.Scopes, ",") != mcpScopeRead {
		t.Errorf("scopes = %v, want read only by default", created.Scopes)
	}
	if created.ExpiresAt == nil {
		t.Fatal("the token must expire")
	}
	expiresAt, parseError := time.Parse(time.RFC3339, *created.ExpiresAt)
	if parseError != nil || expiresAt.Before(time.Now().AddDate(0, 0, 89)) || expiresAt.After(time.Now().AddDate(0, 0, 91)) {
		t.Errorf("expires_at = %s, want 90 days out", *created.ExpiresAt)
	}

	path := filepath.Join(home, ".ankra", "mcp", "claude-code.json")
	info, statError := os.Stat(path)
	if statError != nil {
		t.Fatalf("token file: %v", statError)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %o, want 600", info.Mode().Perm())
	}
	directoryInfo, _ := os.Stat(filepath.Dir(path))
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Errorf("token directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
	if content, _ := os.ReadFile(path); !strings.Contains(string(content), mcpTestToken) {
		t.Errorf("the token must be in its file: %s", content)
	}
}

func TestMCPConnectWriteAddsTheWriteScope(t *testing.T) {
	stubMCPEnvironment(t, false)
	mock := &mcpConnectMock{}
	setMockClient(t, mock)
	if _, executeError := executeCommand("mcp", "connect", "--client", "claude-code", "--write", "--expires-days", "7"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	if strings.Join(mock.created[0].Scopes, ",") != mcpScopeRead+","+mcpScopeWrite {
		t.Errorf("scopes = %v", mock.created[0].Scopes)
	}
}

func TestMCPConnectCursorMergesIntoItsConfig(t *testing.T) {
	_, home := stubMCPEnvironment(t, false)
	setMockClient(t, &mcpConnectMock{})
	path := filepath.Join(home, ".cursor", "mcp.json")
	if makeError := os.MkdirAll(filepath.Dir(path), 0o755); makeError != nil {
		t.Fatal(makeError)
	}
	existing := `{"mcpServers":{"other":{"url":"https://example.test"}},"theme":"dark"}`
	if writeError := os.WriteFile(path, []byte(existing), 0o644); writeError != nil {
		t.Fatal(writeError)
	}

	output, executeError := executeCommand("mcp", "connect", "--client", "cursor")
	if executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	assertNoMCPSecret(t, "the output", output)
	if !strings.Contains(output, `export ANKRA_MCP_TOKEN="$(ankra mcp token --client cursor)"`) {
		t.Errorf("the output must say how to export the token: %s", output)
	}
	content, _ := os.ReadFile(path)
	assertNoMCPSecret(t, "the config", string(content))
	var document struct {
		Theme      string                    `json:"theme"`
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if decodeError := json.Unmarshal(content, &document); decodeError != nil {
		t.Fatalf("config is not JSON: %v", decodeError)
	}
	if document.Theme != "dark" || document.MCPServers["other"] == nil {
		t.Errorf("the rest of the config was lost: %s", content)
	}
	if !strings.Contains(string(content), "${env:ANKRA_MCP_TOKEN}") {
		t.Errorf("the config must name the variable: %s", content)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("config mode = %o, want its old 644 kept", info.Mode().Perm())
	}
}

func TestMCPConnectRefusesAnUnreadableCursorConfigAndRevokes(t *testing.T) {
	_, home := stubMCPEnvironment(t, false)
	mock := &mcpConnectMock{}
	setMockClient(t, mock)
	path := filepath.Join(home, ".cursor", "mcp.json")
	if makeError := os.MkdirAll(filepath.Dir(path), 0o755); makeError != nil {
		t.Fatal(makeError)
	}
	if writeError := os.WriteFile(path, []byte("{not json"), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	_, executeError := executeCommand("mcp", "connect", "--client", "cursor")
	if executeError == nil || !strings.Contains(executeError.Error(), "left alone") {
		t.Fatalf("got %v", executeError)
	}
	if len(mock.revoked) != 1 || mock.revoked[0] != "token-new" {
		t.Errorf("the unused token must be revoked: %v", mock.revoked)
	}
	if content, _ := os.ReadFile(path); string(content) != "{not json" {
		t.Errorf("the config was changed: %s", content)
	}
	if _, statError := os.Stat(filepath.Join(home, ".ankra", "mcp", "cursor.json")); !os.IsNotExist(statError) {
		t.Errorf("a revoked token must not stay on disk: %v", statError)
	}
}

func TestRegisterMCPCodexKeepsTheRestOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# my settings\nmodel = \"o3\"\n\n[mcp_servers.ankra]\nurl = \"https://old\"\n\n[profiles.fast]\nmodel = \"x\"\n"
	if writeError := os.WriteFile(path, []byte(original), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if registerError := registerMCPCodex(path, "https://platform.test/api/v1/mcp"); registerError != nil {
		t.Fatal(registerError)
	}
	content, _ := os.ReadFile(path)
	for _, want := range []string{"# my settings", "[profiles.fast]", `url = "https://platform.test/api/v1/mcp"`, `bearer_token_env_var = "ANKRA_MCP_TOKEN"`} {
		if !strings.Contains(string(content), want) {
			t.Errorf("config must contain %q:\n%s", want, content)
		}
	}
	if strings.Contains(string(content), "https://old") || strings.Count(string(content), "[mcp_servers.ankra]") != 1 {
		t.Errorf("the old section must be replaced:\n%s", content)
	}

	freshPath := filepath.Join(t.TempDir(), "config.toml")
	if registerError := registerMCPCodex(freshPath, "https://platform.test/api/v1/mcp"); registerError != nil {
		t.Fatal(registerError)
	}
	if content, _ := os.ReadFile(freshPath); !strings.HasPrefix(string(content), "[mcp_servers.ankra]") {
		t.Errorf("a fresh file holds only the section:\n%s", content)
	}
}

func TestMCPConnectAgainRevokesThePreviousToken(t *testing.T) {
	stubMCPEnvironment(t, false)
	mock := &mcpConnectMock{}
	setMockClient(t, mock)
	if _, storeError := writeMCPStoredToken("claude-code", mcpStoredToken{TokenID: "token-old", Token: "old"}); storeError != nil {
		t.Fatal(storeError)
	}
	if _, executeError := executeCommand("mcp", "connect", "--client", "claude-code"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	if len(mock.revoked) != 1 || mock.revoked[0] != "token-old" {
		t.Errorf("revoked = %v", mock.revoked)
	}
}

func TestMCPConnectFailureRevokesTheNewTokenAndKeepsTheOld(t *testing.T) {
	commands, _ := stubMCPEnvironment(t, false)
	commands.failAdd = true
	mock := &mcpConnectMock{}
	setMockClient(t, mock)
	if _, storeError := writeMCPStoredToken("claude-code", mcpStoredToken{TokenID: "token-old", Token: "old"}); storeError != nil {
		t.Fatal(storeError)
	}
	_, executeError := executeCommand("mcp", "connect", "--client", "claude-code")
	if executeError == nil || !strings.Contains(executeError.Error(), "was revoked") {
		t.Fatalf("got %v", executeError)
	}
	if len(mock.revoked) != 1 || mock.revoked[0] != "token-new" {
		t.Errorf("revoked = %v, want only the new token", mock.revoked)
	}
	stored, readError := readMCPStoredToken("claude-code")
	if readError != nil || stored.TokenID != "token-old" {
		t.Errorf("stored = %+v, %v, want the old token back", stored, readError)
	}
}

func TestMCPConnectWithoutClaudeCreatesNoToken(t *testing.T) {
	stubMCPEnvironment(t, false)
	lookMCPClientCommand = func(string) (string, error) { return "", errors.New("not found") }
	mock := &mcpConnectMock{}
	setMockClient(t, mock)
	_, executeError := executeCommand("mcp", "connect", "--client", "claude-code")
	if executeError == nil || len(mock.created) != 0 {
		t.Errorf("error = %v, created = %+v", executeError, mock.created)
	}
}

func TestMCPConnectRefusesBadFlags(t *testing.T) {
	cases := [][]string{
		{"mcp", "connect"},
		{"mcp", "connect", "--client", "vim"},
		{"mcp", "connect", "--client", "cursor", "--expires-days", "0"},
		{"mcp", "connect", "--client", "cursor", "--expires-days", "9999"},
	}
	for _, arguments := range cases {
		stubMCPEnvironment(t, false)
		mock := &mcpConnectMock{}
		setMockClient(t, mock)
		_, executeError := executeCommand(arguments...)
		if exitCodeFor(executeError) != exitUsage || len(mock.created) != 0 {
			t.Errorf("%v: exit %d (%v), created %+v", arguments, exitCodeFor(executeError), executeError, mock.created)
		}
		_ = mcpConnectCmd.Flags().Set("client", "")
		_ = mcpConnectCmd.Flags().Set("expires-days", "90")
	}
}

func TestMCPTokenRefusesATerminal(t *testing.T) {
	stubMCPEnvironment(t, true)
	if _, storeError := writeMCPStoredToken("cursor", mcpStoredToken{Token: mcpTestToken}); storeError != nil {
		t.Fatal(storeError)
	}
	for _, verb := range []string{"token", "headers"} {
		output, executeError := executeCommand("mcp", verb, "--client", "cursor")
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%s: exit %d (%v), want %d", verb, exitCodeFor(executeError), executeError, exitUsage)
		}
		assertNoMCPSecret(t, verb+" on a terminal", output)
	}
}

func TestMCPTokenPrintsIntoAPipe(t *testing.T) {
	stubMCPEnvironment(t, false)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, storeError := writeMCPStoredToken("claude-code", mcpStoredToken{Token: mcpTestToken, ExpiresAt: expires}); storeError != nil {
		t.Fatal(storeError)
	}
	output, executeError := executeCommand("mcp", "headers", "--client", "claude-code")
	if executeError != nil {
		t.Fatalf("headers failed: %v", executeError)
	}
	var headers map[string]string
	if decodeError := json.Unmarshal([]byte(output), &headers); decodeError != nil {
		t.Fatalf("headers are not JSON: %v: %s", decodeError, output)
	}
	if headers["Authorization"] != "Bearer "+mcpTestToken {
		t.Errorf("headers = %v", headers)
	}
}

func TestMCPTokenSaysWhenItCannotAnswer(t *testing.T) {
	stubMCPEnvironment(t, false)
	_, missingError := executeCommand("mcp", "token", "--client", "codex")
	if exitCodeFor(missingError) != exitNotFound || !strings.Contains(missingError.Error(), "mcp connect --client codex") {
		t.Errorf("missing: exit %d (%v)", exitCodeFor(missingError), missingError)
	}
	if _, storeError := writeMCPStoredToken("codex", mcpStoredToken{Token: mcpTestToken, ExpiresAt: "2020-01-01T00:00:00Z"}); storeError != nil {
		t.Fatal(storeError)
	}
	output, expiredError := executeCommand("mcp", "token", "--client", "codex")
	if exitCodeFor(expiredError) != exitAuth || !strings.Contains(expiredError.Error(), "expired") {
		t.Errorf("expired: exit %d (%v)", exitCodeFor(expiredError), expiredError)
	}
	assertNoMCPSecret(t, "an expired answer", output)
}

func TestMCPConnectStructuredOutputCarriesNoToken(t *testing.T) {
	stubMCPEnvironment(t, false)
	setMockClient(t, &mcpConnectMock{})
	output, executeError := executeCommand("mcp", "connect", "--client", "codex", "-o", "json")
	if executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	assertNoMCPSecret(t, "the document", output)
	var result mcpConnectResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("not one document: %v: %s", decodeError, output)
	}
	if result.TokenID != "token-new" || result.Environment != mcpTokenEnvironment {
		t.Errorf("result = %+v", result)
	}
}

func TestMCPTokenNameNamesTheClient(t *testing.T) {
	if name := mcpTokenName("cursor"); !strings.HasPrefix(name, "mcp-cursor") || strings.ContainsAny(name, " .") {
		t.Errorf("name = %q", name)
	}
}

func TestMCPTokenRefusesAnExpiryItCannotRead(t *testing.T) {
	stubMCPEnvironment(t, false)
	if _, storeError := writeMCPStoredToken("codex", mcpStoredToken{Token: mcpTestToken, ExpiresAt: "next tuesday"}); storeError != nil {
		t.Fatal(storeError)
	}
	output, executeError := executeCommand("mcp", "token", "--client", "codex")
	if exitCodeFor(executeError) != exitAuth || !strings.Contains(executeError.Error(), "cannot be read") {
		t.Errorf("unreadable expiry: exit %d (%v), want %d", exitCodeFor(executeError), executeError, exitAuth)
	}
	assertNoMCPSecret(t, "an unreadable expiry", output)
}

func TestRegisterMCPCursorLeavesANonObjectServersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{"mcpServers": ["kept"]}`
	if writeError := os.WriteFile(path, []byte(original), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if registerError := registerMCPCursor(path, "https://platform.test/api/v1/mcp"); registerError == nil {
		t.Fatal("a non-object mcpServers must be refused")
	}
	if content, _ := os.ReadFile(path); string(content) != original {
		t.Errorf("the config must be left alone:\n%s", content)
	}
}

func TestRegisterMCPCodexReplacesAHeaderWithATrailingComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[mcp_servers.ankra] # the platform\nurl = \"https://old\"\n"
	if writeError := os.WriteFile(path, []byte(original), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if registerError := registerMCPCodex(path, "https://platform.test/api/v1/mcp"); registerError != nil {
		t.Fatal(registerError)
	}
	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "https://old") || strings.Count(string(content), "[mcp_servers.ankra]") != 1 {
		t.Errorf("the commented section must be replaced, not duplicated:\n%s", content)
	}
}
