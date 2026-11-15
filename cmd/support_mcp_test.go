package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	supportTestReadToken    = "asup_read_secret_never_printed"
	supportTestWriteToken   = "asup_write_secret_never_printed"
	supportTestExpiredToken = "asup_expired_secret_never_printed"
)

var supportTestExpiry = time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeSupportServer answers MCP 2026-07-28 and /v1/me the way support.ankra.app
// does, refusing a request that misses the revision or method headers.
type fakeSupportServer struct {
	server             *httptest.Server
	lock               sync.Mutex
	methods            []string
	answerToolsAsEvent bool
}

// calledMethods answers the MCP methods the server was sent, in order.
func (fake *fakeSupportServer) calledMethods() string {
	fake.lock.Lock()
	defer fake.lock.Unlock()
	return strings.Join(fake.methods, ",")
}

func (fake *fakeSupportServer) answerToolsListAsEventStream() {
	fake.lock.Lock()
	defer fake.lock.Unlock()
	fake.answerToolsAsEvent = true
}

func newFakeSupportServer(t *testing.T) *fakeSupportServer {
	t.Helper()
	fake := &fakeSupportServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if token != supportTestReadToken && token != supportTestWriteToken {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/v1/me" {
			_ = json.NewEncoder(response).Encode(map[string]any{"actor": map[string]any{"kind": "staff", "expires_at": supportTestExpiry}, "csrf": ""})
			return
		}
		var envelope struct {
			Method string `json:"method"`
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		_ = json.NewDecoder(request.Body).Decode(&envelope)
		if request.URL.Path != "/mcp" || request.Header.Get("MCP-Protocol-Version") != supportMCPProtocolVersion ||
			request.Header.Get("Mcp-Method") != envelope.Method ||
			envelope.Params.Meta["io.modelcontextprotocol/protocolVersion"] != supportMCPProtocolVersion {
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		fake.lock.Lock()
		fake.methods = append(fake.methods, envelope.Method)
		asEvent := fake.answerToolsAsEvent && envelope.Method == "tools/list"
		fake.lock.Unlock()
		var result any
		switch envelope.Method {
		case "server/discover":
			result = map[string]any{"resultType": "complete", "supportedVersions": []string{supportMCPProtocolVersion}}
		case "tools/list":
			tools := []map[string]string{{"name": "support_list_tickets"}, {"name": "support_get_ticket"}}
			if token == supportTestWriteToken {
				tools = append(tools, map[string]string{"name": "support_reply"})
			}
			result = map[string]any{"tools": tools}
		}
		encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		if asEvent {
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = response.Write([]byte("event: message\ndata: " + string(encoded) + "\n\n"))
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(encoded)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

type supportTestEnvironment struct {
	home     string
	commands *mcpClientCommands
	server   *fakeSupportServer
	opened   []string
	prompted string
}

func stubSupportEnvironment(t *testing.T, isTerminal bool) *supportTestEnvironment {
	t.Helper()
	commands, home := stubMCPEnvironment(t, false)
	environment := &supportTestEnvironment{home: home, commands: commands, server: newFakeSupportServer(t)}
	previousTerminal, previousPrompt, previousBrowser := supportStdinTerminal, readHiddenSupportToken, launchSupportBrowser
	supportStdinTerminal = func(io.Reader) (int, bool) { return 0, isTerminal }
	readHiddenSupportToken = func(int, io.Writer, string) (string, error) { return environment.prompted, nil }
	launchSupportBrowser = func(address string) error {
		environment.opened = append(environment.opened, address)
		return nil
	}
	t.Cleanup(func() {
		supportStdinTerminal, readHiddenSupportToken, launchSupportBrowser = previousTerminal, previousPrompt, previousBrowser
		for _, command := range []string{"client", "output"} {
			_ = supportConnectCmd.Flags().Set(command, "")
		}
		_ = supportConnectCmd.Flags().Set("support-url", supportDefaultURL)
		_ = supportConnectCmd.Flags().Set("token-stdin", "false")
		_ = supportConnectCmd.Flags().Set("no-browser", "false")
		_ = supportTokenCmd.Flags().Set("client", "")
		_ = supportTokenCmd.Flags().Set("headers", "false")
		_ = supportDisconnectCmd.Flags().Set("client", "")
		_ = supportDisconnectCmd.Flags().Set("yes", "false")
		rootCmd.SetIn(nil)
	})
	return environment
}

func (environment *supportTestEnvironment) connect(t *testing.T, input string, arguments ...string) (string, error) {
	t.Helper()
	rootCmd.SetIn(strings.NewReader(input))
	return executeCommand(append([]string{"support", "connect", "--support-url", environment.server.server.URL}, arguments...)...)
}

func assertNoSupportSecret(t *testing.T, where string, content string) {
	t.Helper()
	for _, token := range []string{supportTestReadToken, supportTestWriteToken, supportTestExpiredToken} {
		if strings.Contains(content, token) {
			t.Errorf("the token leaked into %s: %s", where, content)
		}
	}
}

func TestSupportConnectClaudeCodeKeepsTheTokenOutOfEverythingButItsFile(t *testing.T) {
	environment := stubSupportEnvironment(t, false)

	output, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "claude-code", "--token-stdin")
	if executeError != nil {
		t.Fatalf("connect failed: %v\n%s", executeError, output)
	}
	assertNoSupportSecret(t, "the output", output)
	for _, call := range environment.commands.calls {
		assertNoSupportSecret(t, "a command line", strings.Join(call, " "))
	}
	if !strings.Contains(output, "read-only staff token") || !strings.Contains(output, "2099-01-02T03:04:05Z") {
		t.Errorf("output does not report access and expiry: %s", output)
	}
	if environment.server.calledMethods() != "server/discover,tools/list" {
		t.Errorf("MCP requests = %s", environment.server.calledMethods())
	}

	var added []string
	for _, call := range environment.commands.calls {
		if len(call) > 2 && call[2] == "add-json" {
			added = call
		}
	}
	if len(added) == 0 || added[len(added)-2] != supportMCPServerName ||
		!strings.Contains(added[len(added)-1], "support token --client claude-code --headers") {
		t.Fatalf("claude was not given the headers helper: %v", environment.commands.calls)
	}

	path := filepath.Join(environment.home, ".ankra", "support", "claude-code.json")
	info, statError := os.Stat(path)
	if statError != nil {
		t.Fatalf("token file: %v", statError)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %o, want 600", info.Mode().Perm())
	}
	if directoryInfo, _ := os.Stat(filepath.Dir(path)); directoryInfo.Mode().Perm() != 0o700 {
		t.Errorf("token directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
	stored, readError := readSupportStoredToken("claude-code")
	if readError != nil || stored.Token != supportTestReadToken || stored.ExpiresAt != "2099-01-02T03:04:05Z" ||
		stored.Access != supportAccessReadOnly || stored.Kind != "staff" {
		t.Errorf("stored = %+v (%v)", stored, readError)
	}
}

func TestSupportConnectReportsReadWriteFromTheToolsOffered(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	environment.server.answerToolsListAsEventStream()

	output, executeError := environment.connect(t, supportTestWriteToken+"\n", "--client", "claude-code", "--token-stdin", "-o", "json")
	if executeError != nil {
		t.Fatalf("connect failed: %v\n%s", executeError, output)
	}
	assertNoSupportSecret(t, "the JSON output", output)
	var result supportConnectResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	if result.Access != supportAccessReadWrite || result.Kind != "staff" || !strings.Contains(strings.Join(result.Tools, ","), "support_reply") {
		t.Errorf("result = %+v", result)
	}
}

func TestSupportConnectPromptsWithTheInputHiddenAfterOpeningAgentAccess(t *testing.T) {
	environment := stubSupportEnvironment(t, true)
	environment.prompted = supportTestReadToken

	output, executeError := environment.connect(t, "", "--client", "claude-code")
	if executeError != nil {
		t.Fatalf("connect failed: %v\n%s", executeError, output)
	}
	assertNoSupportSecret(t, "the output", output)
	if len(environment.opened) != 1 || environment.opened[0] != environment.server.server.URL+"/" {
		t.Errorf("opened = %v", environment.opened)
	}
	if !strings.Contains(output, "Agent access") {
		t.Errorf("the prompt does not say where to create the token: %s", output)
	}
}

func TestSupportConnectNoBrowserOnlyPrintsTheAddress(t *testing.T) {
	environment := stubSupportEnvironment(t, true)
	environment.prompted = supportTestReadToken
	if _, executeError := environment.connect(t, "", "--client", "claude-code", "--no-browser"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	if len(environment.opened) != 0 {
		t.Errorf("a browser was opened: %v", environment.opened)
	}
}

func TestSupportConnectRefusesWhatItCannotUseAndChangesNothing(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		isTerminal bool
		arguments  []string
		exitCode   int
	}{
		{"a token the server refuses", supportTestExpiredToken + "\n", false, []string{"--token-stdin"}, exitAuth},
		{"a token that is not a Support token", "ankra_platform_secret_never_printed\n", false, []string{"--token-stdin"}, exitUsage},
		{"no token on stdin", "\n", false, []string{"--token-stdin"}, exitUsage},
		{"no terminal and no --token-stdin", "", false, nil, exitUsage},
		{"--token-stdin on a terminal", "", true, []string{"--token-stdin"}, exitUsage},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			environment := stubSupportEnvironment(t, test.isTerminal)
			output, executeError := environment.connect(t, test.input, append([]string{"--client", "claude-code"}, test.arguments...)...)
			if exitCodeFor(executeError) != test.exitCode {
				t.Fatalf("exit %d (%v), want %d", exitCodeFor(executeError), executeError, test.exitCode)
			}
			assertNoSupportSecret(t, "the output", output)
			assertNoSupportSecret(t, "the error", executeError.Error())
			if strings.Contains(executeError.Error(), "ankra_platform_secret_never_printed") {
				t.Errorf("the pasted value leaked into the error: %v", executeError)
			}
			if _, statError := os.Stat(filepath.Join(environment.home, ".ankra", "support", "claude-code.json")); !errors.Is(statError, os.ErrNotExist) {
				t.Errorf("a token file was written: %v", statError)
			}
			if len(environment.commands.calls) != 0 {
				t.Errorf("claude was called: %v", environment.commands.calls)
			}
		})
	}
}

func TestSupportConnectRefusesAPlainHTTPAddress(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	rootCmd.SetIn(strings.NewReader(supportTestReadToken + "\n"))
	_, executeError := executeCommand("support", "connect", "--client", "cursor", "--token-stdin", "--support-url", "http://support.example.com")
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("exit %d (%v), want %d", exitCodeFor(executeError), executeError, exitUsage)
	}
	if methods := environment.server.calledMethods(); methods != "" {
		t.Errorf("the token was sent: %s", methods)
	}
}

func TestSupportConnectAgainReplacesTheToken(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "cursor", "--token-stdin"); executeError != nil {
		t.Fatalf("first connect failed: %v", executeError)
	}
	if _, executeError := environment.connect(t, supportTestWriteToken+"\n", "--client", "cursor", "--token-stdin"); executeError != nil {
		t.Fatalf("second connect failed: %v", executeError)
	}
	stored, readError := readSupportStoredToken("cursor")
	if readError != nil || stored.Token != supportTestWriteToken || stored.Access != supportAccessReadWrite {
		t.Errorf("stored = %+v (%v)", stored, readError)
	}
}

func TestSupportConnectCursorAndCodexReadTheEnvironmentVariable(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	cursorPath := filepath.Join(environment.home, ".cursor", "mcp.json")
	if makeError := os.MkdirAll(filepath.Dir(cursorPath), 0o755); makeError != nil {
		t.Fatal(makeError)
	}
	if writeError := os.WriteFile(cursorPath, []byte(`{"mcpServers":{"ankra":{"url":"https://platform.ankra.app/api/v1/mcp"}}}`), 0o644); writeError != nil {
		t.Fatal(writeError)
	}

	output, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "cursor", "--token-stdin")
	if executeError != nil {
		t.Fatalf("cursor connect failed: %v", executeError)
	}
	if !strings.Contains(output, `export ANKRA_SUPPORT_MCP_TOKEN="$(ankra support token --client cursor)"`) {
		t.Errorf("output does not give the export line: %s", output)
	}
	cursorContent, _ := os.ReadFile(cursorPath)
	assertNoSupportSecret(t, "Cursor's config", string(cursorContent))
	var cursorConfig struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if decodeError := json.Unmarshal(cursorContent, &cursorConfig); decodeError != nil {
		t.Fatalf("Cursor's config is not JSON: %v", decodeError)
	}
	support := cursorConfig.MCPServers[supportMCPServerName]
	if support.URL != environment.server.server.URL+"/mcp" || support.Headers["Authorization"] != "Bearer ${env:ANKRA_SUPPORT_MCP_TOKEN}" {
		t.Errorf("ankra-support = %+v", support)
	}
	if _, isKept := cursorConfig.MCPServers["ankra"]; !isKept {
		t.Error("the existing ankra server was dropped")
	}

	if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "codex", "--token-stdin"); executeError != nil {
		t.Fatalf("codex connect failed: %v", executeError)
	}
	codexContent, _ := os.ReadFile(filepath.Join(environment.home, ".codex", "config.toml"))
	assertNoSupportSecret(t, "Codex's config", string(codexContent))
	if !strings.Contains(string(codexContent), "[mcp_servers.ankra-support]") ||
		!strings.Contains(string(codexContent), `bearer_token_env_var = "ANKRA_SUPPORT_MCP_TOKEN"`) {
		t.Errorf("Codex's config = %s", codexContent)
	}
}

func TestSupportConnectTakesOverTheManualSetup(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	directory := filepath.Join(environment.home, ".ankra", "support")
	if makeError := os.MkdirAll(directory, 0o755); makeError != nil {
		t.Fatal(makeError)
	}
	legacyToken, legacyHelper := filepath.Join(directory, "token"), filepath.Join(directory, "mcp-headers.sh")
	if writeError := os.WriteFile(legacyToken, []byte(supportTestWriteToken+"\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if writeError := os.WriteFile(legacyHelper, []byte("#!/bin/sh\n"), 0o700); writeError != nil {
		t.Fatal(writeError)
	}

	output, executeError := environment.connect(t, "", "--client", "claude-code")
	if executeError != nil {
		t.Fatalf("connect failed: %v\n%s", executeError, output)
	}
	assertNoSupportSecret(t, "the output", output)
	stored, readError := readSupportStoredToken("claude-code")
	if readError != nil || stored.Token != supportTestWriteToken || stored.Access != supportAccessReadWrite {
		t.Errorf("stored = %+v (%v)", stored, readError)
	}
	for _, path := range []string{legacyToken, legacyHelper} {
		if _, statError := os.Stat(path); !errors.Is(statError, os.ErrNotExist) {
			t.Errorf("%s was left behind: %v", path, statError)
		}
	}
	if len(environment.commands.calls) < 2 || environment.commands.calls[0][2] != "remove" ||
		environment.commands.calls[0][len(environment.commands.calls[0])-1] != supportMCPServerName {
		t.Errorf("the hand-made ankra-support registration was not replaced: %v", environment.commands.calls)
	}
	if environment.server.calledMethods() != "server/discover,tools/list" {
		t.Errorf("the manual setup's token was checked more than once: %s", environment.server.calledMethods())
	}
}

func TestSupportConnectLeavesTheManualSetupForClaudeWhenConnectingCursor(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	directory := filepath.Join(environment.home, ".ankra", "support")
	if makeError := os.MkdirAll(directory, 0o700); makeError != nil {
		t.Fatal(makeError)
	}
	legacyToken := filepath.Join(directory, "token")
	if writeError := os.WriteFile(legacyToken, []byte(supportTestReadToken), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if _, executeError := environment.connect(t, "", "--client", "cursor"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	if _, statError := os.Stat(legacyToken); statError != nil {
		t.Errorf("the token Claude Code still reads was removed: %v", statError)
	}
}

func TestSupportTokenPrintsTheTokenOrTheHeaders(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "claude-code", "--token-stdin"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}

	var headers bytes.Buffer
	rootCmd.SetOut(&headers)
	rootCmd.SetArgs([]string{"support", "token", "--client", "claude-code", "--headers"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("token --headers failed: %v", executeError)
	}
	var decoded map[string]string
	if decodeError := json.Unmarshal(headers.Bytes(), &decoded); decodeError != nil || len(decoded) != 1 ||
		decoded["Authorization"] != "Bearer "+supportTestReadToken {
		t.Errorf("headers = %q (%v)", headers.String(), decodeError)
	}

	_ = supportTokenCmd.Flags().Set("headers", "false")
	output, executeError := executeCommand("support", "token", "--client", "claude-code")
	if executeError != nil || output != supportTestReadToken+"\n" {
		t.Errorf("token = %q (%v)", output, executeError)
	}
}

func TestSupportTokenRefusesATerminal(t *testing.T) {
	stubSupportEnvironment(t, false)
	if _, executeError := writeSupportStoredToken("cursor", supportStoredToken{Token: supportTestReadToken}); executeError != nil {
		t.Fatal(executeError)
	}
	isMCPOutputTerminal = func(any) bool { return true }
	output, executeError := executeCommand("support", "token", "--client", "cursor")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit %d (%v), want %d", exitCodeFor(executeError), executeError, exitUsage)
	}
	assertNoSupportSecret(t, "the terminal", output)
}

func TestSupportTokenMissingOrExpiredSaysToConnect(t *testing.T) {
	stubSupportEnvironment(t, false)
	_, missingError := executeCommand("support", "token", "--client", "codex", "--headers")
	if exitCodeFor(missingError) != exitNotFound || !strings.Contains(missingError.Error(), "ankra support connect --client codex") {
		t.Errorf("missing: exit %d (%v)", exitCodeFor(missingError), missingError)
	}

	expired := supportStoredToken{Token: supportTestExpiredToken, ExpiresAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}
	if _, writeError := writeSupportStoredToken("codex", expired); writeError != nil {
		t.Fatal(writeError)
	}
	output, expiredError := executeCommand("support", "token", "--client", "codex", "--headers")
	if exitCodeFor(expiredError) != exitAuth || !strings.Contains(expiredError.Error(), "expired") ||
		!strings.Contains(expiredError.Error(), "ankra support connect --client codex") {
		t.Errorf("expired: exit %d (%v)", exitCodeFor(expiredError), expiredError)
	}
	assertNoSupportSecret(t, "the output", output)
}

func TestSupportDisconnectRemovesTheTokenAndTheRegistration(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	for _, clientName := range []string{"claude-code", "cursor", "codex"} {
		if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", clientName, "--token-stdin"); executeError != nil {
			t.Fatalf("connect %s failed: %v", clientName, executeError)
		}
	}
	codexPath := filepath.Join(environment.home, ".codex", "config.toml")
	codexBefore, _ := os.ReadFile(codexPath)
	if writeError := os.WriteFile(codexPath, append([]byte("model = \"o4\"\n\n"), codexBefore...), 0o600); writeError != nil {
		t.Fatal(writeError)
	}

	_, refusedError := executeCommand("support", "disconnect", "--client", "cursor")
	if exitCodeFor(refusedError) != exitUsage {
		t.Errorf("without --yes and a terminal: exit %d (%v), want %d", exitCodeFor(refusedError), refusedError, exitUsage)
	}

	for _, clientName := range []string{"claude-code", "cursor", "codex"} {
		environment.commands.calls = nil
		output, executeError := executeCommand("support", "disconnect", "--client", clientName, "--yes")
		if executeError != nil {
			t.Fatalf("disconnect %s failed: %v", clientName, executeError)
		}
		if !strings.Contains(output, "revoke it under Agent access") {
			t.Errorf("%s: output does not say to revoke: %s", clientName, output)
		}
		if _, statError := os.Stat(filepath.Join(environment.home, ".ankra", "support", clientName+".json")); !errors.Is(statError, os.ErrNotExist) {
			t.Errorf("%s: the token file was left: %v", clientName, statError)
		}
		if clientName == "claude-code" && (len(environment.commands.calls) != 1 || environment.commands.calls[0][2] != "remove") {
			t.Errorf("claude was not asked to remove the server: %v", environment.commands.calls)
		}
	}
	cursorContent, _ := os.ReadFile(filepath.Join(environment.home, ".cursor", "mcp.json"))
	if strings.Contains(string(cursorContent), supportMCPServerName) {
		t.Errorf("Cursor still has the server: %s", cursorContent)
	}
	codexContent, _ := os.ReadFile(codexPath)
	if strings.Contains(string(codexContent), supportMCPServerName) || !strings.Contains(string(codexContent), `model = "o4"`) {
		t.Errorf("Codex's config = %s", codexContent)
	}
}

func TestSupportClientRevisionCheckNeverGuesses(t *testing.T) {
	stubSupportEnvironment(t, false)
	cases := []struct {
		clientName string
		version    string
		failure    error
		want       string
	}{
		{"claude-code", "2.1.325 (Claude Code)", nil, "Claude Code 2.1.325 speaks MCP 2026-07-28"},
		{"claude-code", "2.2.0 (Claude Code)", nil, "Claude Code 2.2.0 speaks MCP 2026-07-28"},
		{"claude-code", "2.0.9 (Claude Code)", nil, "could not be confirmed"},
		{"codex", "codex-cli 0.169.0", nil, "Codex 0.169.0 speaks MCP 2026-07-28"},
		{"codex", "", errors.New("not found"), "was not checked"},
		{"cursor", "", nil, "cannot be checked"},
	}
	for _, test := range cases {
		runMCPClientCommand = func(string, ...string) ([]byte, error) { return []byte(test.version), test.failure }
		if got := supportClientRevisionCheck(test.clientName); !strings.Contains(got, test.want) {
			t.Errorf("%s %q: %q, want it to contain %q", test.clientName, test.version, got, test.want)
		}
	}
}

func TestRemoveTOMLSectionKeepsEveryOtherLine(t *testing.T) {
	document := "model = \"o4\"\n\n[mcp_servers.ankra]\nurl = \"a\"\n\n[mcp_servers.ankra-support]\nurl = \"b\"\n\n[profiles.fast]\nmodel = \"x\"\n"
	updated, isFound := removeTOMLSection(document, "[mcp_servers.ankra-support]")
	want := "model = \"o4\"\n\n[mcp_servers.ankra]\nurl = \"a\"\n\n[profiles.fast]\nmodel = \"x\"\n"
	if !isFound || updated != want {
		t.Errorf("updated = %q, want %q", updated, want)
	}
	if _, isFound := removeTOMLSection(document, "[mcp_servers.missing]"); isFound {
		t.Error("a missing table was reported found")
	}
}

func TestSupportDisconnectSaysWhenTheClaudeRegistrationIsNotGone(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	tokenFile := filepath.Join(environment.home, ".ankra", "support", "claude-code.json")
	connect := func() {
		t.Helper()
		if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "claude-code", "--token-stdin"); executeError != nil {
			t.Fatalf("connect failed: %v", executeError)
		}
	}

	connect()
	runMCPClientCommand = func(string, ...string) ([]byte, error) { return []byte("permission denied"), errors.New("exit 1") }
	_, refusedError := executeCommand("support", "disconnect", "--client", "claude-code", "--yes")
	if refusedError == nil || !strings.Contains(refusedError.Error(), "permission denied") {
		t.Errorf("a failed removal was reported as done: %v", refusedError)
	}
	if _, statError := os.Stat(tokenFile); statError != nil {
		t.Errorf("the token file was removed although the registration remains: %v", statError)
	}

	runMCPClientCommand = func(string, ...string) ([]byte, error) {
		return []byte("No MCP server found with name: ankra-support"), errors.New("exit 1")
	}
	if _, executeError := executeCommand("support", "disconnect", "--client", "claude-code", "--yes"); executeError != nil {
		t.Errorf("a registration that is already gone failed disconnect: %v", executeError)
	}

	runMCPClientCommand = func(string, ...string) ([]byte, error) { return nil, nil }
	connect()
	lookMCPClientCommand = func(string) (string, error) { return "", errors.New("not found") }
	output, executeError := executeCommand("support", "disconnect", "--client", "claude-code", "--yes")
	if executeError != nil {
		t.Fatalf("disconnect failed: %v", executeError)
	}
	if !strings.Contains(output, "not on PATH") || !strings.Contains(output, "claude mcp remove --scope user ankra-support") ||
		strings.Contains(output, "Disconnected") {
		t.Errorf("output does not say the registration was not checked: %s", output)
	}
}

func TestRegisterMCPCursorServerAcceptsANullConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if writeError := os.WriteFile(path, []byte("null\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if registerError := registerMCPCursorServer(path, supportMCPServerName, "https://support.ankra.app/mcp", supportMCPTokenEnvironment); registerError != nil {
		t.Fatalf("register failed: %v", registerError)
	}
	if content, _ := os.ReadFile(path); !strings.Contains(string(content), supportMCPServerName) {
		t.Errorf("config = %s", content)
	}
}

func TestLastSupportEventDataJoinsDataLines(t *testing.T) {
	stream := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\ndata: \"id\":1}\n\nevent: message\ndata: {\"id\":2,\ndata: \"result\":{}}\n\n")
	if got, want := string(lastSupportEventData(stream)), "{\"id\":2,\n\"result\":{}}"; got != want {
		t.Errorf("last event data = %q, want %q", got, want)
	}
}

func TestSupportConnectSaysTheClaudeRegistrationWasRemovedWhenTheAddFails(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	environment.commands.failAdd = true
	_, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "claude-code", "--token-stdin")
	if executeError == nil || strings.Contains(executeError.Error(), "nothing was changed") ||
		!strings.Contains(executeError.Error(), "was removed before the new one could be added") {
		t.Errorf("error = %v", executeError)
	}
	if _, statError := os.Stat(filepath.Join(environment.home, ".ankra", "support", "claude-code.json")); !errors.Is(statError, os.ErrNotExist) {
		t.Errorf("the token file was not put back: %v", statError)
	}
}

func TestSupportDisconnectCodexSaysWhenATableCouldNotBeMatched(t *testing.T) {
	environment := stubSupportEnvironment(t, false)
	if _, executeError := environment.connect(t, supportTestReadToken+"\n", "--client", "codex", "--token-stdin"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	codexPath := filepath.Join(environment.home, ".codex", "config.toml")
	if writeError := os.WriteFile(codexPath, []byte("[mcp_servers.\"ankra-support\"]\nurl = \"x\"\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	output, executeError := executeCommand("support", "disconnect", "--client", "codex", "--yes")
	if executeError != nil {
		t.Fatalf("disconnect failed: %v", executeError)
	}
	if !strings.Contains(output, "remove that table by hand") || strings.Contains(output, "Disconnected") {
		t.Errorf("output = %s", output)
	}
}
