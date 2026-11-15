package cmd

// `ankra support connect`: register the Ankra Support MCP server
// (support.ankra.app/mcp) with a coding agent, for Ankra staff and customers.
//
// Support tokens are minted only in the browser, under Agent access after
// Platform sign-in, so this cannot create one the way `ankra mcp connect` does.
// It takes the token the person pastes at a hidden prompt (or pipes in with
// --token-stdin), checks it against the server, and keeps it in one file only
// this user can read. Claude Code asks `ankra support token --headers` for it
// on each request; Cursor and Codex read ANKRA_SUPPORT_MCP_TOKEN.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	supportMCPServerName       = "ankra-support"
	supportDefaultURL          = "https://support.ankra.app"
	supportMCPPath             = "/mcp"
	supportMCPTokenEnvironment = "ANKRA_SUPPORT_MCP_TOKEN"
	supportMCPProtocolVersion  = "2026-07-28"
	supportTokenPrefix         = "asup_"
	supportAccessReadOnly      = "read-only"
	supportAccessReadWrite     = "read/write"
	supportRequestLimit        = 30 * time.Second
	supportResponseLimit       = 1 << 20
)

// supportWriteTools are the tools the server offers only to a token with
// tickets:write, so seeing one of them is what makes a token read/write.
var supportWriteTools = []string{"support_create_ticket", "support_reply", "support_update_ticket"}

// supportVerifiedClientVersions are the client versions checked to speak MCP
// 2026-07-28, the only revision support.ankra.app serves. A version at or
// above one of these is reported as supported; anything else is reported as
// not confirmed, never as unsupported.
var supportVerifiedClientVersions = map[string]string{
	mcpClientClaudeCode: "2.1.325",
	mcpClientCodex:      "0.169.0",
}

// supportStoredToken is the file `ankra support connect` leaves behind.
type supportStoredToken struct {
	Token       string `json:"token"`
	ServerURL   string `json:"server_url"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Access      string `json:"access"`
	Kind        string `json:"kind,omitempty"`
	ConnectedAt string `json:"connected_at"`
}

// supportConnectResult is what -o json prints. It never carries the token.
type supportConnectResult struct {
	Client         string   `json:"client" yaml:"client"`
	ServerURL      string   `json:"server_url" yaml:"server_url"`
	Access         string   `json:"access" yaml:"access"`
	Kind           string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	ExpiresAt      string   `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	Tools          []string `json:"tools" yaml:"tools"`
	TokenFile      string   `json:"token_file" yaml:"token_file"`
	Registered     string   `json:"registered" yaml:"registered"`
	Environment    string   `json:"environment_variable,omitempty" yaml:"environment_variable,omitempty"`
	RevisionCheck  string   `json:"revision_check" yaml:"revision_check"`
	MigratedLegacy bool     `json:"migrated_manual_setup,omitempty" yaml:"migrated_manual_setup,omitempty"`
}

// supportHTTPClient talks to the Support service. A var so tests can point it
// at a fake server.
var supportHTTPClient = &http.Client{Timeout: supportRequestLimit}

// readHiddenSupportToken asks for the token on the terminal with echo off. A
// var so tests can stand in for the terminal.
var readHiddenSupportToken = readHiddenTerminalLine

// supportStdinTerminal reports whether stdin is a terminal, and its
// descriptor. A var so tests can answer both ways.
var supportStdinTerminal = personalAIKeyTerminal

// launchSupportBrowser opens Agent access. A var so tests do not.
var launchSupportBrowser = openBrowser

var supportConnectCmd = &cobra.Command{
	Use:   "connect --client <claude-code|cursor|codex>",
	Short: "Connect Claude Code, Cursor or Codex to the Ankra Support MCP server",
	Long: `Connect a coding agent to the Ankra Support MCP server at
https://support.ankra.app/mcp, for Ankra staff and for customers.

Support tokens are created only in the browser: this opens support.ankra.app,
where you sign in with Ankra Platform, choose Agent access in the sidebar and
create a token (1 to 24 hours, read-only or read/write). Paste it at the hidden
prompt, or pipe it in with --token-stdin. It is never taken as a flag, never
printed, and never written into the agent's config: it is stored in
~/.ankra/support/<client>.json, readable only by you.

The token is checked against the server before anything is saved, and connect
reports whether it is read-only or read/write and when it expires.

  - Claude Code gets a headers helper, 'ankra support token --headers', so it
    reads the token on each request.
  - Cursor and Codex read ANKRA_SUPPORT_MCP_TOKEN, so add this to your shell
    profile:
      export ANKRA_SUPPORT_MCP_TOKEN="$(ankra support token --client cursor)"

Run connect again with a new token when it expires. A token from an earlier
manual setup in ~/.ankra/support/token is picked up the first time.`,
	Example: `  ankra support connect --client claude-code
  ankra support connect --client cursor --no-browser
  pbpaste | ankra support connect --client codex --token-stdin`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runSupportConnect,
}

var supportTokenCmd = &cobra.Command{
	Use:   "token --client <client>",
	Short: "Print the stored Support MCP token (or --headers JSON) into a pipe, and refuse a terminal",
	Long: `Print the token 'ankra support connect' stored for a client, for
export ANKRA_SUPPORT_MCP_TOKEN="$(ankra support token --client cursor)".

--headers prints {"Authorization":"Bearer <token>"} instead, which is what
Claude Code's headers helper reads. It refuses to print to a terminal, so the
token stays out of your scrollback, and fails with exit 6 once the token has
expired.`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runSupportToken,
}

var supportDisconnectCmd = &cobra.Command{
	Use:   "disconnect --client <client>",
	Short: "Remove the stored Support MCP token and the client's registration",
	Long: `Remove the Support MCP token stored for a client, and the ankra-support
server from that client's config.

This cannot revoke the token on the server: revoke it under Agent access on
support.ankra.app, or let it expire.`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runSupportDisconnect,
}

func init() {
	for _, command := range []*cobra.Command{supportConnectCmd, supportTokenCmd, supportDisconnectCmd} {
		command.Flags().String("client", "", "Client: "+strings.Join(mcpConnectClients, ", ")+" (required)")
	}
	supportConnectCmd.Flags().Bool("token-stdin", false, "Read the token from stdin instead of a hidden prompt")
	supportConnectCmd.Flags().Bool("no-browser", false, "Print the Agent access address instead of opening it")
	supportConnectCmd.Flags().String("support-url", supportDefaultURL, "Address of the Support service")
	registerStructuredOutputFlags(supportConnectCmd)
	supportTokenCmd.Flags().Bool("headers", false, `Print {"Authorization":"Bearer <token>"} for a headers helper`)
	supportDisconnectCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	supportCmd.AddCommand(supportConnectCmd, supportTokenCmd, supportDisconnectCmd)
}

func runSupportConnect(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	clientName, clientError := mcpClientFromFlags(command)
	if clientError != nil {
		return clientError
	}
	supportURL, urlError := supportURLFromFlags(command)
	if urlError != nil {
		return urlError
	}
	if clientName == mcpClientClaudeCode {
		if _, lookError := lookMCPClientCommand("claude"); lookError != nil {
			return errors.New("the 'claude' command was not found on PATH: install Claude Code, then run this again")
		}
	}
	progress := command.ErrOrStderr()
	serverURL := supportURL + supportMCPPath

	token, legacyCheck, tokenError := supportTokenForConnect(command, clientName, supportURL, serverURL)
	if tokenError != nil {
		return tokenError
	}
	isLegacy := legacyCheck != nil
	check := legacyCheck
	if !isLegacy {
		var checkError error
		if check, checkError = checkSupportToken(supportURL, serverURL, token); checkError != nil {
			return checkError
		}
	}

	stored := supportStoredToken{
		Token: token, ServerURL: serverURL, ExpiresAt: check.ExpiresAt, Access: check.Access, Kind: check.Kind,
		ConnectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	previousContent, previousExists := readSupportStoredTokenFile(clientName)
	tokenFile, storeError := writeSupportStoredToken(clientName, stored)
	if storeError == nil {
		storeError = registerSupportMCPClient(clientName, serverURL)
	}
	if storeError != nil {
		restoreSupportStoredTokenFile(clientName, previousContent, previousExists)
		if clientName == mcpClientClaudeCode {
			// Claude Code cannot add a server under a name it already has, so the
			// earlier registration was removed first and cannot be put back.
			return fmt.Errorf("connecting %s failed and the token file was put back, but any earlier %s registration in Claude Code "+
				"was removed before the new one could be added: run connect again once this is fixed: %w",
				clientName, supportMCPServerName, storeError)
		}
		return fmt.Errorf("connecting %s (nothing was changed): %w", clientName, storeError)
	}
	if isLegacy && clientName == mcpClientClaudeCode {
		removeSupportManualSetup(progress)
	}

	result := supportConnectResult{
		Client: clientName, ServerURL: serverURL, Access: check.Access, Kind: check.Kind, ExpiresAt: check.ExpiresAt,
		Tools: check.Tools, TokenFile: tokenFile, Registered: supportRegistrationPlace(clientName),
		RevisionCheck: supportClientRevisionCheck(clientName), MigratedLegacy: isLegacy,
	}
	if clientName != mcpClientClaudeCode {
		result.Environment = supportMCPTokenEnvironment
	}
	if format != outputDefault {
		_, renderError := renderStructured(command, result)
		return renderError
	}
	printSupportConnected(command.OutOrStdout(), result)
	return nil
}

// supportURLFromFlags answers the Support address without a trailing slash.
// It must be https, so the token never crosses the network in the clear; a
// loopback address may be http, for a service run locally.
func supportURLFromFlags(command *cobra.Command) (string, error) {
	raw, _ := command.Flags().GetString("support-url")
	parsed, parseError := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if parseError != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", withExitCode(exitUsage, fmt.Errorf("--support-url must be an address such as %s", supportDefaultURL))
	}
	isLoopback := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopback) {
		return "", withExitCode(exitUsage, errors.New("--support-url must use https, so the token is never sent in the clear"))
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// supportTokenForConnect takes the token from stdin (--token-stdin), from an
// earlier manual setup the first time a client connects, or from a hidden
// prompt after opening Agent access. For a token from the manual setup, which
// is checked before it is chosen, it also answers that check.
func supportTokenForConnect(command *cobra.Command, clientName string, supportURL string, serverURL string) (string, *supportTokenCheck, error) {
	progress := command.ErrOrStderr()
	fromStdin, _ := command.Flags().GetBool("token-stdin")
	descriptor, isTerminal := supportStdinTerminal(command.InOrStdin())
	if fromStdin {
		if isTerminal {
			return "", nil, withExitCode(exitUsage, errors.New(
				"--token-stdin reads a token piped into the command, but stdin is a terminal, where the token would show as you type it; "+
					"leave --token-stdin out to be asked for it with the input hidden"))
		}
		line, readError := readLineWithin(command.InOrStdin(), secretStdinWait)
		if errors.Is(readError, errStdinSilent) {
			return "", nil, withExitCode(exitUsage, fmt.Errorf(
				"--token-stdin was passed, but stdin sent nothing within %s; pipe the token into the command", secretStdinWait))
		}
		if readError != nil && !errors.Is(readError, io.EOF) {
			return "", nil, fmt.Errorf("reading the token from stdin: %w", readError)
		}
		token, shapeError := checkSupportTokenShape(line)
		return token, nil, shapeError
	}

	if legacy := readSupportLegacyToken(clientName); legacy != "" {
		legacyCheck, checkError := checkSupportToken(supportURL, serverURL, legacy)
		if checkError == nil {
			_, _ = fmt.Fprintf(progress, "Using the token from your earlier manual setup (%s).\n", supportLegacyTokenPath())
			return legacy, legacyCheck, nil
		}
		_, _ = fmt.Fprintf(progress, "The token in %s from your earlier manual setup cannot be used (%v), so a new one is needed.\n",
			supportLegacyTokenPath(), checkError)
	}

	if !isTerminal {
		return "", nil, withExitCode(exitUsage, errors.New(
			"stdin is not a terminal, so the token cannot be asked for: pipe it in with --token-stdin. The token is never taken as a flag"))
	}
	agentAccess := supportURL + "/"
	_, _ = fmt.Fprintf(progress, "Create a token at %s: sign in with Ankra Platform, choose Agent access in the sidebar,\n"+
		"and create a token (1 to 24 hours; read/write lets the agent reply and update tickets).\n", agentAccess)
	if noBrowser, _ := command.Flags().GetBool("no-browser"); !noBrowser {
		if openError := launchSupportBrowser(agentAccess); openError != nil {
			_, _ = fmt.Fprintf(progress, "Could not open a browser (%v): open the address above yourself.\n", openError)
		}
	}
	line, promptError := readHiddenSupportToken(descriptor, progress, "Paste the Support token")
	if promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return "", nil, errCancelled
		}
		return "", nil, fmt.Errorf("reading the token: %w", promptError)
	}
	token, shapeError := checkSupportTokenShape(line)
	return token, nil, shapeError
}

// checkSupportTokenShape refuses what cannot be a Support agent token without
// ever repeating it, so a Platform token pasted by mistake goes nowhere.
func checkSupportTokenShape(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	switch {
	case token == "":
		return "", withExitCode(exitUsage, errors.New("no token was given; nothing was changed"))
	case !strings.HasPrefix(token, supportTokenPrefix):
		return "", withExitCode(exitUsage, fmt.Errorf(
			"that is not a Support agent token (they start with %s; create one under Agent access on support.ankra.app); nothing was changed",
			supportTokenPrefix))
	case strings.IndexFunc(token, func(character rune) bool { return character <= ' ' || character == '"' || character == '\\' }) >= 0:
		return "", withExitCode(exitUsage, errors.New("what was given holds spaces or quotes, so it is not a single token; nothing was changed"))
	}
	return token, nil
}

// supportTokenCheck is what the server says about a token.
type supportTokenCheck struct {
	Access    string
	Kind      string
	ExpiresAt string
	Tools     []string
}

// checkSupportToken asks the server, as the token, whether it speaks MCP
// 2026-07-28 (server/discover) and which tools the token is offered
// (tools/list), which is what decides read-only or read/write. The token's
// expiry and whether it is a staff token come from /v1/me; when that cannot be
// read the expiry is left unknown rather than guessed.
func checkSupportToken(supportURL string, serverURL string, token string) (*supportTokenCheck, error) {
	discovered, discoverError := callSupportMCP(serverURL, token, "server/discover")
	if discoverError != nil {
		return nil, discoverError
	}
	var discovery struct {
		SupportedVersions []string `json:"supportedVersions"`
	}
	if decodeError := json.Unmarshal(discovered, &discovery); decodeError != nil {
		return nil, fmt.Errorf("the Support server's server/discover answer could not be read: %w", decodeError)
	}
	if !slices.Contains(discovery.SupportedVersions, supportMCPProtocolVersion) {
		return nil, fmt.Errorf("the Support server does not offer MCP %s (it offers %s), which this CLI expects; nothing was changed",
			supportMCPProtocolVersion, strings.Join(discovery.SupportedVersions, ", "))
	}
	listed, listError := callSupportMCP(serverURL, token, "tools/list")
	if listError != nil {
		return nil, listError
	}
	var catalogue struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if decodeError := json.Unmarshal(listed, &catalogue); decodeError != nil {
		return nil, fmt.Errorf("the Support server's tools/list answer could not be read: %w", decodeError)
	}
	check := &supportTokenCheck{Access: supportAccessReadOnly, Tools: []string{}}
	for _, tool := range catalogue.Tools {
		check.Tools = append(check.Tools, tool.Name)
		if slices.Contains(supportWriteTools, tool.Name) {
			check.Access = supportAccessReadWrite
		}
	}
	slices.Sort(check.Tools)
	check.Kind, check.ExpiresAt = readSupportTokenIdentity(supportURL, token)
	return check, nil
}

// callSupportMCP sends one MCP 2026-07-28 request: no initialize handshake or
// session, the revision and method in both the headers and params._meta, and
// a JSON or event-stream answer. It answers the JSON-RPC result.
func callSupportMCP(serverURL string, token string, method string) (json.RawMessage, error) {
	body, encodeError := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params": map[string]any{"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    supportMCPProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}},
	})
	if encodeError != nil {
		return nil, encodeError
	}
	request, requestError := http.NewRequest(http.MethodPost, serverURL, bytes.NewReader(body))
	if requestError != nil {
		return nil, requestError
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", supportMCPProtocolVersion)
	request.Header.Set("Mcp-Method", method)
	response, sendError := supportHTTPClient.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("could not reach the Support server at %s: %w; nothing was changed", serverURL, redactSupportError(sendError, token))
	}
	defer func() { _ = response.Body.Close() }()
	content, readError := io.ReadAll(io.LimitReader(response.Body, supportResponseLimit))
	if readError != nil {
		return nil, fmt.Errorf("reading the Support server's answer: %w", readError)
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return nil, withExitCode(exitAuth, errors.New(
			"the Support server refused the token: it has expired, was revoked, or was not copied whole. Create a new one under Agent access; nothing was changed"))
	case response.StatusCode == http.StatusForbidden:
		return nil, withExitCode(exitForbidden, errors.New(
			"the Support server accepted the token but it does not allow reading tickets; create one with read access under Agent access; nothing was changed"))
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the Support server answered %s to %s: %s; nothing was changed",
			response.Status, method, strings.TrimSpace(string(content)))
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		content = lastSupportEventData(content)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if decodeError := json.Unmarshal(content, &envelope); decodeError != nil {
		return nil, fmt.Errorf("the Support server's answer to %s is not JSON-RPC: %w", method, decodeError)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("the Support server refused %s: %s (%d); nothing was changed", method, envelope.Error.Message, envelope.Error.Code)
	}
	return envelope.Result, nil
}

// lastSupportEventData answers the data of the last event in an event stream,
// which is where a single-response stream carries the JSON-RPC response.
func lastSupportEventData(stream []byte) []byte {
	var last, current []byte
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), supportResponseLimit)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			if len(current) > 0 {
				current = append(current, '\n')
			}
			current = append(current, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
		case line == "" && len(current) > 0:
			last, current = current, nil
		}
	}
	if len(current) > 0 {
		last = current
	}
	return last
}

// redactSupportError keeps the token out of an error from the transport, which
// some proxies echo back.
func redactSupportError(failure error, token string) error {
	return errors.New(strings.ReplaceAll(failure.Error(), token, "[token]"))
}

// readSupportTokenIdentity reads the token's actor from /v1/me: staff or
// customer, and when it expires. Either is "" when it cannot be read.
func readSupportTokenIdentity(supportURL string, token string) (string, string) {
	request, requestError := http.NewRequest(http.MethodGet, supportURL+"/v1/me", nil)
	if requestError != nil {
		return "", ""
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, sendError := supportHTTPClient.Do(request)
	if sendError != nil {
		return "", ""
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", ""
	}
	var me struct {
		Actor struct {
			Kind      string    `json:"kind"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"actor"`
	}
	if decodeError := json.NewDecoder(io.LimitReader(response.Body, supportResponseLimit)).Decode(&me); decodeError != nil {
		return "", ""
	}
	expiresAt := ""
	if !me.Actor.ExpiresAt.IsZero() {
		expiresAt = me.Actor.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return me.Actor.Kind, expiresAt
}

func printSupportConnected(out io.Writer, result supportConnectResult) {
	_, _ = fmt.Fprintf(out, "Connected %s to %s as %s.\n", result.Client, result.ServerURL, supportMCPServerName)
	who := "token"
	if result.Kind != "" {
		who = result.Kind + " token"
	}
	_, _ = fmt.Fprintf(out, "  Access: %s %s, tools %s.\n", result.Access, who, strings.Join(result.Tools, ", "))
	if result.ExpiresAt != "" {
		_, _ = fmt.Fprintf(out, "  Expires %s. Run connect again with a new token to renew it.\n", result.ExpiresAt)
	} else {
		_, _ = fmt.Fprintln(out, "  Its expiry could not be read; it lasts at most 24 hours. Run connect again with a new token to renew it.")
	}
	_, _ = fmt.Fprintf(out, "  Registered in: %s\n", result.Registered)
	_, _ = fmt.Fprintf(out, "  Stored in %s, readable only by you. It was not printed or written to the client's config.\n", result.TokenFile)
	_, _ = fmt.Fprintf(out, "  %s\n", result.RevisionCheck)
	if result.MigratedLegacy {
		_, _ = fmt.Fprintf(out, "  Took the token from your earlier manual setup in %s.\n", filepath.Dir(supportLegacyTokenPath()))
	}
	if result.Environment != "" {
		_, _ = fmt.Fprintf(out, "\n%s reads the token from %s. Add this to your shell profile, then restart it:\n  export %s=\"$(ankra support token --client %s)\"\n",
			result.Client, result.Environment, result.Environment, result.Client)
	}
}

// --- the client's MCP revision ---

var supportVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// supportClientRevisionCheck says whether the installed client is known to
// speak MCP 2026-07-28. It reads the client's own --version and compares it
// with the versions checked by hand; a client it cannot ask is said to be
// unchecked, never assumed either way.
func supportClientRevisionCheck(clientName string) string {
	if clientName == mcpClientCursor {
		return "Cursor's MCP revision cannot be checked from here: the server speaks only MCP " + supportMCPProtocolVersion +
			", so update Cursor if it shows the server as failing."
	}
	executable, label := "claude", "Claude Code"
	if clientName == mcpClientCodex {
		executable, label = "codex", "Codex"
	}
	output, runError := runMCPClientCommand(executable, "--version")
	installed := supportVersionPattern.FindString(string(output))
	if runError != nil || installed == "" {
		return fmt.Sprintf("%s's version could not be read, so whether it speaks MCP %s was not checked.", label, supportMCPProtocolVersion)
	}
	verified := supportVerifiedClientVersions[clientName]
	if compareSupportVersions(installed, verified) >= 0 {
		return fmt.Sprintf("%s %s speaks MCP %s.", label, installed, supportMCPProtocolVersion)
	}
	return fmt.Sprintf("%s %s could not be confirmed to speak MCP %s (checked from %s): update it if the server does not connect.",
		label, installed, supportMCPProtocolVersion, verified)
}

// compareSupportVersions compares two dotted versions number by number.
func compareSupportVersions(left string, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		var leftNumber, rightNumber int
		if index < len(leftParts) {
			leftNumber, _ = strconv.Atoi(leftParts[index])
		}
		if index < len(rightParts) {
			rightNumber, _ = strconv.Atoi(rightParts[index])
		}
		if leftNumber != rightNumber {
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		}
	}
	return 0
}

// --- the token file ---

func supportDirectory() (string, error) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return "", homeError
	}
	return filepath.Join(home, ".ankra", "support"), nil
}

func supportTokenFile(clientName string) (string, error) {
	directory, directoryError := supportDirectory()
	if directoryError != nil {
		return "", directoryError
	}
	return filepath.Join(directory, clientName+".json"), nil
}

func writeSupportStoredToken(clientName string, stored supportStoredToken) (string, error) {
	path, pathError := supportTokenFile(clientName)
	if pathError != nil {
		return "", pathError
	}
	encoded, encodeError := json.Marshal(stored)
	if encodeError != nil {
		return "", encodeError
	}
	if writeError := writePrivateFile(path, encoded); writeError != nil {
		return "", writeError
	}
	return path, nil
}

func readSupportStoredTokenFile(clientName string) ([]byte, bool) {
	path, pathError := supportTokenFile(clientName)
	if pathError != nil {
		return nil, false
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil, false
	}
	return content, true
}

// restoreSupportStoredTokenFile puts back what a failed connect replaced.
func restoreSupportStoredTokenFile(clientName string, previousContent []byte, previousExists bool) {
	path, pathError := supportTokenFile(clientName)
	if pathError != nil {
		return
	}
	if !previousExists {
		_ = os.Remove(path)
		return
	}
	_ = writePrivateFile(path, previousContent)
}

func readSupportStoredToken(clientName string) (*supportStoredToken, error) {
	content, isPresent := readSupportStoredTokenFile(clientName)
	if !isPresent {
		return nil, os.ErrNotExist
	}
	var stored supportStoredToken
	if decodeError := json.Unmarshal(content, &stored); decodeError != nil || stored.Token == "" {
		path, _ := supportTokenFile(clientName)
		return nil, fmt.Errorf("%s cannot be read as a Support token file: run 'ankra support connect --client %s'", path, clientName)
	}
	return &stored, nil
}

// --- the earlier manual setup ---

// supportLegacyTokenPath is the plain token file of the manual setup this
// command replaces, read by the hand-written ~/.ankra/support/mcp-headers.sh.
func supportLegacyTokenPath() string {
	directory, directoryError := supportDirectory()
	if directoryError != nil {
		return ""
	}
	return filepath.Join(directory, "token")
}

func supportLegacyHelperPath() string {
	directory, directoryError := supportDirectory()
	if directoryError != nil {
		return ""
	}
	return filepath.Join(directory, "mcp-headers.sh")
}

// readSupportLegacyToken answers the manual setup's token, but only the first
// time a client connects: once the client has a token of its own, running
// connect again means a new token is wanted.
func readSupportLegacyToken(clientName string) string {
	if _, isPresent := readSupportStoredTokenFile(clientName); isPresent {
		return ""
	}
	path := supportLegacyTokenPath()
	if path == "" {
		return ""
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return ""
	}
	token, shapeError := checkSupportTokenShape(string(content))
	if shapeError != nil {
		return ""
	}
	return token
}

// removeSupportManualSetup deletes the manual setup's token file and helper
// script once Claude Code, their only user, has been moved off them. The token
// lives on in the client's own file.
func removeSupportManualSetup(progress io.Writer) {
	for _, path := range []string{supportLegacyTokenPath(), supportLegacyHelperPath()} {
		if path == "" {
			continue
		}
		if removeError := os.Remove(path); removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
			_, _ = fmt.Fprintf(progress, "note: could not remove %s from the earlier manual setup: %v\n", path, removeError)
		}
	}
}

// --- client registration ---

func supportRegistrationPlace(clientName string) string {
	switch clientName {
	case mcpClientClaudeCode:
		return "Claude Code, user scope, as " + supportMCPServerName + " (claude mcp get " + supportMCPServerName + ")"
	case mcpClientCursor:
		return "~/.cursor/mcp.json, as " + supportMCPServerName
	}
	return "~/.codex/config.toml, as [mcp_servers." + supportMCPServerName + "]"
}

func supportClientConfigPath(clientName string) (string, error) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return "", homeError
	}
	if clientName == mcpClientCursor {
		return filepath.Join(home, ".cursor", "mcp.json"), nil
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func registerSupportMCPClient(clientName string, serverURL string) error {
	if clientName == mcpClientClaudeCode {
		return registerSupportMCPClaudeCode(serverURL)
	}
	path, pathError := supportClientConfigPath(clientName)
	if pathError != nil {
		return pathError
	}
	if clientName == mcpClientCursor {
		return registerMCPCursorServer(path, supportMCPServerName, serverURL, supportMCPTokenEnvironment)
	}
	return registerMCPCodexServer(path, supportMCPServerName, serverURL, supportMCPTokenEnvironment)
}

// registerSupportMCPClaudeCode replaces any earlier ankra-support server,
// the hand-made one included, with one whose headers helper is this CLI. The
// JSON carries no secret, so it is safe on a command line.
func registerSupportMCPClaudeCode(serverURL string) error {
	configuration, encodeError := json.Marshal(map[string]any{
		"type":          "http",
		"url":           serverURL,
		"headersHelper": shellQuoteMCP(currentExecutable()) + " support token --client " + mcpClientClaudeCode + " --headers",
	})
	if encodeError != nil {
		return encodeError
	}
	_, _ = runMCPClientCommand("claude", "mcp", "remove", "--scope", "user", supportMCPServerName)
	output, addError := runMCPClientCommand("claude", "mcp", "add-json", "--scope", "user", supportMCPServerName, string(configuration))
	if addError != nil {
		return fmt.Errorf("'claude mcp add-json' failed: %w: %s", addError, strings.TrimSpace(string(output)))
	}
	return nil
}

// supportClaudeNotRegisteredPattern matches what `claude mcp remove` says
// about a server that is not registered, which is a removal already done.
var supportClaudeNotRegisteredPattern = regexp.MustCompile(`(?i)no .*server.* (found|named)|not found|does not exist`)

// unregisterSupportMCPClient removes ankra-support from a client's config. It
// answers what is left for the person to do when it could not check that the
// registration is gone: Claude Code's own CLI is not on PATH to ask.
func unregisterSupportMCPClient(clientName string) (string, error) {
	if clientName == mcpClientClaudeCode {
		if _, lookError := lookMCPClientCommand("claude"); lookError != nil {
			return "The 'claude' command is not on PATH, so the " + supportMCPServerName +
				" registration was not checked; remove it with 'claude mcp remove --scope user " + supportMCPServerName + "'.", nil
		}
		output, removeError := runMCPClientCommand("claude", "mcp", "remove", "--scope", "user", supportMCPServerName)
		if removeError != nil && !supportClaudeNotRegisteredPattern.Match(output) {
			return "", fmt.Errorf("'claude mcp remove' failed: %w: %s", removeError, strings.TrimSpace(string(output)))
		}
		return "", nil
	}
	path, pathError := supportClientConfigPath(clientName)
	if pathError != nil {
		return "", pathError
	}
	if clientName == mcpClientCursor {
		return "", unregisterMCPCursorServer(path, supportMCPServerName)
	}
	if removeError := unregisterMCPCodexServer(path, supportMCPServerName); removeError != nil {
		return "", removeError
	}
	// A table written by hand in another form, a quoted key for one, is not
	// matched; say so rather than report it removed.
	if content, readError := os.ReadFile(path); readError == nil && strings.Contains(string(content), supportMCPServerName) {
		return path + " still names " + supportMCPServerName + " in a form this could not match: remove that table by hand.", nil
	}
	return "", nil
}

// unregisterMCPCursorServer removes one server from Cursor's config, keeping
// everything else. A missing file or server is already done.
func unregisterMCPCursorServer(path string, serverName string) error {
	if _, statError := os.Stat(path); errors.Is(statError, os.ErrNotExist) {
		return nil
	}
	document, servers, readError := readCursorMCPServers(path)
	if readError != nil {
		return readError
	}
	if _, isPresent := servers[serverName]; !isPresent {
		return nil
	}
	delete(servers, serverName)
	document["mcpServers"] = servers
	return writeCursorMCPDocument(path, document)
}

// unregisterMCPCodexServer removes the [mcp_servers.<serverName>] table from
// Codex's config, leaving every other line byte for byte.
func unregisterMCPCodexServer(path string, serverName string) error {
	content, readError := os.ReadFile(path)
	if errors.Is(readError, os.ErrNotExist) {
		return nil
	}
	if readError != nil {
		return readError
	}
	updated, isFound := removeTOMLSection(string(content), "[mcp_servers."+serverName+"]")
	if !isFound {
		return nil
	}
	return writeMCPClientConfig(path, []byte(updated))
}

// removeTOMLSection removes one table, from its header to the next table
// header, and the blank lines that separated it from the table before.
func removeTOMLSection(document string, header string) (string, bool) {
	lines := strings.SplitAfter(document, "\n")
	for start, line := range lines {
		if tomlHeaderOf(line) != header {
			continue
		}
		end := start + 1
		for end < len(lines) && !isTOMLTableHeader(lines[end]) {
			end++
		}
		before := strings.TrimRight(strings.Join(lines[:start], ""), "\n")
		after := strings.Join(lines[end:], "")
		switch {
		case before == "":
			return after, true
		case after == "":
			return before + "\n", true
		}
		return before + "\n\n" + after, true
	}
	return document, false
}

// --- token and disconnect ---

func runSupportToken(command *cobra.Command, _ []string) error {
	clientName, clientError := mcpClientFromFlags(command)
	if clientError != nil {
		return clientError
	}
	if isMCPOutputTerminal(command.OutOrStdout()) {
		return withExitCode(exitUsage, errors.New(
			"this prints a token, so it refuses a terminal; use it in a pipe or $(...), or a client's headers helper"))
	}
	stored, readError := readSupportStoredToken(clientName)
	if errors.Is(readError, os.ErrNotExist) {
		return withExitCode(exitNotFound, fmt.Errorf(
			"no Support MCP token is stored for %s: run 'ankra support connect --client %s'", clientName, clientName))
	}
	if readError != nil {
		return withExitCode(exitAuth, readError)
	}
	if stored.ExpiresAt != "" {
		expiresAt, parseError := time.Parse(time.RFC3339, stored.ExpiresAt)
		if parseError == nil && !time.Now().Before(expiresAt) {
			return withExitCode(exitAuth, fmt.Errorf(
				"the Support MCP token for %s expired at %s: create a new one under Agent access on support.ankra.app and run 'ankra support connect --client %s'",
				clientName, stored.ExpiresAt, clientName))
		}
	}
	if asHeaders, _ := command.Flags().GetBool("headers"); asHeaders {
		return json.NewEncoder(command.OutOrStdout()).Encode(map[string]string{"Authorization": "Bearer " + stored.Token})
	}
	_, writeError := fmt.Fprintln(command.OutOrStdout(), stored.Token)
	return writeError
}

func runSupportDisconnect(command *cobra.Command, _ []string) error {
	clientName, clientError := mcpClientFromFlags(command)
	if clientError != nil {
		return clientError
	}
	yes, _ := command.Flags().GetBool("yes")
	if !yes {
		if _, isTerminal := supportStdinTerminal(command.InOrStdin()); !isTerminal {
			return withExitCode(exitUsage, errors.New(
				"this removes the stored Support token and the client's registration; re-run with --yes to confirm"))
		}
		prompt := fmt.Sprintf("Remove the Support MCP token stored for %s and the %s server from its config? [y/N] ", clientName, supportMCPServerName)
		if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), prompt, false); confirmError != nil {
			return confirmError
		}
	}
	remaining, unregisterError := unregisterSupportMCPClient(clientName)
	if unregisterError != nil {
		return fmt.Errorf("removing %s from %s (the token file was kept, so run disconnect again once it is fixed): %w",
			supportMCPServerName, clientName, unregisterError)
	}
	path, pathError := supportTokenFile(clientName)
	if pathError != nil {
		return pathError
	}
	if removeError := os.Remove(path); removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, removeError)
	}
	out := command.OutOrStdout()
	if remaining != "" {
		_, _ = fmt.Fprintf(out, "Removed %s. %s\n", path, remaining)
	} else {
		_, _ = fmt.Fprintf(out, "Disconnected %s from the Support MCP server: removed %s and its %s registration.\n", clientName, path, supportMCPServerName)
	}
	_, _ = fmt.Fprintln(out, "The token still works until it expires: revoke it under Agent access on support.ankra.app.")
	if clientName != mcpClientClaudeCode {
		_, _ = fmt.Fprintf(out, "Remove the %s line from your shell profile too.\n", supportMCPTokenEnvironment)
	}
	return nil
}
