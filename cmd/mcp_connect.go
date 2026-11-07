package cmd

// `ankra mcp connect`: register the Ankra MCP server with a coding agent, with
// a scoped, expiring token of its own. Writing a bearer token into an agent's
// config by hand is what this replaces, so the token never reaches that config,
// a terminal or a command line. It lives in one file only this user can read,
// and the agent's config names how to fetch it.
//
// Claude Code asks a headers helper each time. Cursor and Codex read an
// environment variable, which the person exports from `ankra mcp token`.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

// The clients `ankra mcp connect` can register with.
const (
	mcpClientClaudeCode = "claude-code"
	mcpClientCursor     = "cursor"
	mcpClientCodex      = "codex"
)

var mcpConnectClients = []string{mcpClientClaudeCode, mcpClientCursor, mcpClientCodex}

const (
	mcpServerName        = "ankra"
	mcpServerPath        = "/api/v1/mcp"
	mcpTokenEnvironment  = "ANKRA_MCP_TOKEN"
	mcpScopeRead         = "mcp:read"
	mcpScopeWrite        = "mcp:write"
	mcpDefaultExpiryDays = 90
	mcpMaximumExpiryDays = 365
)

// mcpStoredToken is the file `ankra mcp connect` leaves behind.
type mcpStoredToken struct {
	TokenID   string   `json:"token_id"`
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	ServerURL string   `json:"server_url"`
	Scopes    []string `json:"scopes"`
}

// mcpConnectResult is what -o json prints. It never carries the token.
type mcpConnectResult struct {
	Client      string   `json:"client" yaml:"client"`
	ServerURL   string   `json:"server_url" yaml:"server_url"`
	TokenID     string   `json:"token_id" yaml:"token_id"`
	ExpiresAt   string   `json:"expires_at" yaml:"expires_at"`
	Scopes      []string `json:"scopes" yaml:"scopes"`
	TokenFile   string   `json:"token_file" yaml:"token_file"`
	Registered  string   `json:"registered" yaml:"registered"`
	Environment string   `json:"environment_variable,omitempty" yaml:"environment_variable,omitempty"`
}

// runMCPClientCommand runs a client's own CLI. A var so tests do not.
var runMCPClientCommand = func(name string, arguments ...string) ([]byte, error) {
	return exec.Command(name, arguments...).CombinedOutput()
}

// lookMCPClientCommand finds a client's CLI. A var so tests choose the answer.
var lookMCPClientCommand = exec.LookPath

// runMCPSkillsInstall installs the skills for a client. A var so tests do not.
var runMCPSkillsInstall = func(out io.Writer, clientName string) error {
	command := exec.Command(currentExecutable(), "skills", "install", "--client", clientName)
	command.Stdout = out
	command.Stderr = out
	return command.Run()
}

// isMCPOutputTerminal reports whether a stream is a terminal. A var so tests
// can answer both ways.
var isMCPOutputTerminal = func(stream any) bool {
	file, isFile := stream.(*os.File)
	return isFile && readline.IsTerminal(int(file.Fd()))
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Connect a coding agent to the Ankra MCP server",
	Long: `Connect a coding agent to the Ankra MCP server.

'ankra mcp connect' is the guided setup. To manage the external MCP servers your
organisation's agent runs may call, see 'ankra org mcp-servers'.`,
}

var mcpConnectCmd = &cobra.Command{
	Use:   "connect --client <claude-code|cursor|codex>",
	Short: "Register the Ankra MCP server with a coding agent, using a scoped, expiring token",
	Long: `Register the Ankra MCP server with Claude Code, Cursor or Codex.

This creates a personal API token that only the MCP server accepts, read-only
unless you pass --write, pinned to the project you are acting in (--project
chooses another) and expiring in 90 days. Running connect again replaces it and
revokes the old one, which is also how to renew it.

The token is never printed. It is stored in ~/.ankra/mcp/<client>.json, readable
only by you, and is not written into the agent's config:

  - Claude Code gets a headers helper, so it fetches the token when it needs it.
  - Cursor and Codex read ANKRA_MCP_TOKEN, so add this to your shell profile:
      export ANKRA_MCP_TOKEN="$(ankra mcp token --client cursor)"

Reads are the place to start; give an agent --write when you want it to act,
for example to open a fix pull request. Writes still ask for confirmation.`,
	Example: `  ankra mcp connect --client claude-code
  ankra mcp connect --client cursor --project platform-team
  ankra mcp connect --client codex --write --expires-days 30 --skills`,
	Args: cobra.NoArgs,
	RunE: runMCPConnect,
}

var mcpHeadersCmd = &cobra.Command{
	Use:         "headers --client <client>",
	Short:       "Print the MCP request headers as JSON (for a client's headers helper, refuses a terminal)",
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	Hidden:      true,
	RunE: func(command *cobra.Command, _ []string) error {
		stored, readError := readMCPTokenForOutput(command)
		if readError != nil {
			return readError
		}
		return json.NewEncoder(command.OutOrStdout()).Encode(map[string]string{"Authorization": "Bearer " + stored.Token})
	},
}

var mcpTokenCmd = &cobra.Command{
	Use:   "token --client <client>",
	Short: "Print the stored MCP token into a pipe or $(...), and refuse a terminal",
	Long: `Print the token 'ankra mcp connect' stored for a client, for use in
export ANKRA_MCP_TOKEN="$(ankra mcp token --client cursor)". It refuses to print
to a terminal, so the token stays out of your scrollback.`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE: func(command *cobra.Command, _ []string) error {
		stored, readError := readMCPTokenForOutput(command)
		if readError != nil {
			return readError
		}
		_, writeError := fmt.Fprintln(command.OutOrStdout(), stored.Token)
		return writeError
	},
}

func init() {
	for _, command := range []*cobra.Command{mcpConnectCmd, mcpHeadersCmd, mcpTokenCmd} {
		command.Flags().String("client", "", "Client: "+strings.Join(mcpConnectClients, ", ")+" (required)")
	}
	mcpConnectCmd.Flags().Bool("write", false, "Also allow the mutating tools (mcp:write)")
	mcpConnectCmd.Flags().Int("expires-days", mcpDefaultExpiryDays, "Days until the token expires")
	mcpConnectCmd.Flags().Bool("skills", false, "Also install the Ankra skills for this client, without asking")
	registerStructuredOutputFlags(mcpConnectCmd)
	mcpCmd.AddCommand(mcpConnectCmd, mcpHeadersCmd, mcpTokenCmd)
	rootCmd.AddCommand(mcpCmd)
}

func mcpClientFromFlags(command *cobra.Command) (string, error) {
	clientName, _ := command.Flags().GetString("client")
	clientName = strings.TrimSpace(clientName)
	if !slices.Contains(mcpConnectClients, clientName) {
		return "", withExitCode(exitUsage, fmt.Errorf("--client must be one of: %s", strings.Join(mcpConnectClients, ", ")))
	}
	return clientName, nil
}

var mcpTokenNamePart = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

func runMCPConnect(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	clientName, clientError := mcpClientFromFlags(command)
	if clientError != nil {
		return clientError
	}
	expiryDays, _ := command.Flags().GetInt("expires-days")
	if expiryDays < 1 || expiryDays > mcpMaximumExpiryDays {
		return withExitCode(exitUsage, fmt.Errorf("--expires-days must be between 1 and %d", mcpMaximumExpiryDays))
	}
	scopes := []string{mcpScopeRead}
	if allowWrite, _ := command.Flags().GetBool("write"); allowWrite {
		scopes = append(scopes, mcpScopeWrite)
	}
	projectID, projectError := tokenProjectPin(command)
	if projectError != nil {
		return projectError
	}
	// Checked before a token exists, so a machine without the client's own CLI
	// is not left with a token nothing uses.
	if clientName == mcpClientClaudeCode {
		if _, lookError := lookMCPClientCommand("claude"); lookError != nil {
			return errors.New("the 'claude' command was not found on PATH: install Claude Code, then run this again")
		}
	}

	serverURL := strings.TrimRight(baseURL, "/") + mcpServerPath
	previousContent, previousExists := readMCPStoredTokenFile(clientName)
	previous, previousError := readMCPStoredToken(clientName)
	if previousError != nil && !errors.Is(previousError, os.ErrNotExist) {
		_, _ = fmt.Fprintf(command.ErrOrStderr(),
			"note: the previous token file could not be read (%v), so its token is not revoked; find it with 'ankra tokens list'\n",
			previousError)
	}

	expiresAt := time.Now().UTC().AddDate(0, 0, expiryDays).Format(time.RFC3339)
	created, createError := apiClient.CreateAPIToken(client.CreateAPITokenRequest{
		Name:      mcpTokenName(clientName),
		ExpiresAt: &expiresAt,
		Scopes:    scopes,
		ProjectID: projectID,
	})
	if createError != nil {
		return fmt.Errorf("creating the MCP token: %w", createError)
	}
	// The platform's own answer wins, in case it holds a token to a shorter life.
	if created.ExpiresAt != "" {
		expiresAt = created.ExpiresAt
	}
	stored := mcpStoredToken{TokenID: created.ID, Token: created.Token, ExpiresAt: expiresAt, ServerURL: serverURL, Scopes: scopes}
	tokenFile, storeError := writeMCPStoredToken(clientName, stored)
	if storeError == nil {
		storeError = registerMCPClient(clientName, serverURL)
	}
	if storeError != nil {
		// A token nothing can use must not stay valid, and the client's working
		// token, if it had one, must stay the one on disk.
		_, _ = apiClient.RevokeAPIToken(created.ID)
		restoreMCPStoredTokenFile(clientName, previousContent, previousExists)
		return fmt.Errorf("connecting %s (the new token was revoked): %w", clientName, storeError)
	}
	progress := command.ErrOrStderr()
	if previous != nil && previous.TokenID != "" {
		if _, revokeError := apiClient.RevokeAPIToken(previous.TokenID); revokeError != nil {
			_, _ = fmt.Fprintf(progress, "note: could not revoke the previous token %s: %v; revoke it with 'ankra tokens revoke %s'\n",
				previous.TokenID, revokeError, previous.TokenID)
		}
	}

	result := mcpConnectResult{
		Client: clientName, ServerURL: serverURL, TokenID: created.ID, ExpiresAt: expiresAt, Scopes: scopes,
		TokenFile: tokenFile, Registered: mcpRegistrationPlace(clientName),
	}
	if clientName != mcpClientClaudeCode {
		result.Environment = mcpTokenEnvironment
	}
	if format != outputDefault {
		_, renderError := renderStructured(command, result)
		return renderError
	}
	printMCPConnected(command.OutOrStdout(), result)
	return offerMCPSkills(command, clientName)
}

// mcpTokenName names the token so 'ankra tokens list' says what it is for.
func mcpTokenName(clientName string) string {
	host, hostError := os.Hostname()
	if hostError != nil || host == "" {
		return "mcp-" + clientName
	}
	return "mcp-" + clientName + "-" + strings.Trim(mcpTokenNamePart.ReplaceAllString(host, "-"), "-")
}

func printMCPConnected(out io.Writer, result mcpConnectResult) {
	_, _ = fmt.Fprintf(out, "Connected %s to %s.\n", result.Client, result.ServerURL)
	_, _ = fmt.Fprintf(out, "  Registered in: %s\n", result.Registered)
	_, _ = fmt.Fprintf(out, "  Token %s, scopes %s, expires %s. Run connect again to renew it.\n",
		result.TokenID, strings.Join(result.Scopes, ","), result.ExpiresAt)
	_, _ = fmt.Fprintf(out, "  Stored in %s, readable only by you. It was not printed or written to the client's config.\n", result.TokenFile)
	if result.Environment != "" {
		_, _ = fmt.Fprintf(out, "\n%s reads the token from %s. Add this to your shell profile, then restart it:\n  export %s=\"$(ankra mcp token --client %s)\"\n",
			result.Client, result.Environment, result.Environment, result.Client)
	}
}

// offerMCPSkills is the skills install the guided setup offers.
func offerMCPSkills(command *cobra.Command, clientName string) error {
	out := command.OutOrStdout()
	installSkills, _ := command.Flags().GetBool("skills")
	if !installSkills {
		if !isMCPOutputTerminal(command.InOrStdin()) {
			_, _ = fmt.Fprintf(out, "\nTo teach it how Ankra works: ankra skills install --client %s\n", clientName)
			return nil
		}
		if confirmPrompt(command.InOrStdin(), out, "\nInstall the Ankra skills for it too? [y/N] ", false) != nil {
			return nil
		}
	}
	if installError := runMCPSkillsInstall(out, clientName); installError != nil {
		return fmt.Errorf("the MCP server is connected, but installing the skills failed: %w; retry with 'ankra skills install --client %s'",
			installError, clientName)
	}
	return nil
}

// mcpTokenFile is where a client's token lives.
func mcpTokenFile(clientName string) (string, error) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return "", homeError
	}
	return filepath.Join(home, ".ankra", "mcp", clientName+".json"), nil
}

// writeMCPStoredToken writes the file so it is never readable by anyone else,
// not even between creating and renaming it.
func writeMCPStoredToken(clientName string, stored mcpStoredToken) (string, error) {
	path, pathError := mcpTokenFile(clientName)
	if pathError != nil {
		return "", pathError
	}
	directory := filepath.Dir(path)
	if makeError := os.MkdirAll(directory, 0o700); makeError != nil {
		return "", makeError
	}
	if permissionError := os.Chmod(directory, 0o700); permissionError != nil {
		return "", permissionError
	}
	encoded, encodeError := json.Marshal(stored)
	if encodeError != nil {
		return "", encodeError
	}
	temporary, createError := os.CreateTemp(directory, clientName+".*.tmp")
	if createError != nil {
		return "", createError
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, writeError := temporary.Write(encoded); writeError != nil {
		_ = temporary.Close()
		return "", writeError
	}
	if closeError := temporary.Close(); closeError != nil {
		return "", closeError
	}
	if renameError := os.Rename(temporary.Name(), path); renameError != nil {
		return "", renameError
	}
	return path, nil
}

// readMCPStoredTokenFile answers the token file's bytes as they are, readable
// or not, so a failed connect can put back exactly what it replaced.
func readMCPStoredTokenFile(clientName string) ([]byte, bool) {
	path, pathError := mcpTokenFile(clientName)
	if pathError != nil {
		return nil, false
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil, false
	}
	return content, true
}

// restoreMCPStoredTokenFile puts back what a failed connect replaced: the
// previous file's bytes, or no file when there was none.
func restoreMCPStoredTokenFile(clientName string, previousContent []byte, previousExists bool) {
	path, pathError := mcpTokenFile(clientName)
	if pathError != nil {
		return
	}
	if !previousExists {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, previousContent, 0o600)
}

func readMCPStoredToken(clientName string) (*mcpStoredToken, error) {
	path, pathError := mcpTokenFile(clientName)
	if pathError != nil {
		return nil, pathError
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil, readError
	}
	var stored mcpStoredToken
	if decodeError := json.Unmarshal(content, &stored); decodeError != nil {
		return nil, fmt.Errorf("reading %s: %w", path, decodeError)
	}
	return &stored, nil
}

// readMCPTokenForOutput answers the stored token for a command that is about to
// print it, refusing a terminal and a token that has expired.
func readMCPTokenForOutput(command *cobra.Command) (*mcpStoredToken, error) {
	clientName, clientError := mcpClientFromFlags(command)
	if clientError != nil {
		return nil, clientError
	}
	if isMCPOutputTerminal(command.OutOrStdout()) {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"this prints a token, so it refuses a terminal; use it in a pipe or $(...), or a client's headers helper"))
	}
	stored, readError := readMCPStoredToken(clientName)
	if errors.Is(readError, os.ErrNotExist) {
		return nil, withExitCode(exitNotFound, fmt.Errorf("no MCP token is stored for %s: run 'ankra mcp connect --client %s'", clientName, clientName))
	}
	if readError != nil {
		return nil, readError
	}
	expiresAt, parseError := time.Parse(time.RFC3339, stored.ExpiresAt)
	if parseError != nil {
		return nil, withExitCode(exitAuth, fmt.Errorf("the stored MCP token for %s has an expiry that cannot be read (%q): run 'ankra mcp connect --client %s' to renew it",
			clientName, stored.ExpiresAt, clientName))
	}
	if !time.Now().Before(expiresAt) {
		return nil, withExitCode(exitAuth, fmt.Errorf("the MCP token for %s expired on %s: run 'ankra mcp connect --client %s' to renew it",
			clientName, stored.ExpiresAt, clientName))
	}
	return stored, nil
}

func mcpRegistrationPlace(clientName string) string {
	switch clientName {
	case mcpClientClaudeCode:
		return "Claude Code, user scope (claude mcp list)"
	case mcpClientCursor:
		return "~/.cursor/mcp.json"
	}
	return "~/.codex/config.toml"
}

func registerMCPClient(clientName string, serverURL string) error {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return homeError
	}
	switch clientName {
	case mcpClientClaudeCode:
		return registerMCPClaudeCode(serverURL)
	case mcpClientCursor:
		return registerMCPCursor(filepath.Join(home, ".cursor", "mcp.json"), serverURL)
	}
	return registerMCPCodex(filepath.Join(home, ".codex", "config.toml"), serverURL)
}

// registerMCPClaudeCode goes through Claude Code's own CLI, whose config file
// is not ours to edit. The JSON names the helper and carries no secret, so it
// is safe on a command line. An earlier registration is replaced.
func registerMCPClaudeCode(serverURL string) error {
	configuration, encodeError := json.Marshal(map[string]any{
		"type":          "http",
		"url":           serverURL,
		"headersHelper": shellQuoteMCP(currentExecutable()) + " mcp headers --client " + mcpClientClaudeCode,
	})
	if encodeError != nil {
		return encodeError
	}
	// A server that is not registered yet makes remove fail, which is fine.
	_, _ = runMCPClientCommand("claude", "mcp", "remove", "--scope", "user", mcpServerName)
	output, addError := runMCPClientCommand("claude", "mcp", "add-json", "--scope", "user", mcpServerName, string(configuration))
	if addError != nil {
		return fmt.Errorf("'claude mcp add-json' failed: %w: %s", addError, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuoteMCP(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// registerMCPCursor merges the server into Cursor's config, keeping everything
// else in it. Cursor expands ${env:NAME} in a header value.
func registerMCPCursor(path string, serverURL string) error {
	document := map[string]any{}
	content, readError := os.ReadFile(path)
	switch {
	case readError == nil && len(bytes.TrimSpace(content)) == 0:
	case readError == nil:
		if decodeError := json.Unmarshal(content, &document); decodeError != nil {
			return fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, decodeError)
		}
	case !errors.Is(readError, os.ErrNotExist):
		return readError
	}
	servers := map[string]any{}
	if existing, isPresent := document["mcpServers"]; isPresent && existing != nil {
		existingServers, isObject := existing.(map[string]any)
		if !isObject {
			return fmt.Errorf("%s has an mcpServers that is not an object, so it was left alone", path)
		}
		servers = existingServers
	}
	servers[mcpServerName] = map[string]any{
		"url":     serverURL,
		"headers": map[string]string{"Authorization": "Bearer ${env:" + mcpTokenEnvironment + "}"},
	}
	document["mcpServers"] = servers
	encoded, encodeError := json.MarshalIndent(document, "", "  ")
	if encodeError != nil {
		return encodeError
	}
	return writeMCPClientConfig(path, append(encoded, '\n'))
}

// registerMCPCodex adds the server to Codex's TOML config. In a table that is
// already there it sets url and bearer_token_env_var and keeps every other key;
// the rest of the file is left byte for byte, comments included, which a
// decode and encode would not do.
func registerMCPCodex(path string, serverURL string) error {
	content, readError := os.ReadFile(path)
	if readError != nil && !errors.Is(readError, os.ErrNotExist) {
		return readError
	}
	header := "[mcp_servers." + mcpServerName + "]"
	keys := []tomlKeyValue{
		{Key: "url", Line: fmt.Sprintf("url = %q\n", serverURL)},
		{Key: "bearer_token_env_var", Line: fmt.Sprintf("bearer_token_env_var = %q\n", mcpTokenEnvironment)},
	}
	updated, isFound := setTOMLSectionKeys(string(content), header, keys)
	if !isFound {
		updated = strings.TrimRight(string(content), "\n")
		if updated != "" {
			updated += "\n\n"
		}
		updated += header + "\n"
		for _, key := range keys {
			updated += key.Line
		}
	}
	return writeMCPClientConfig(path, []byte(updated))
}

// tomlKeyValue is one key of a TOML table and the whole line that sets it.
type tomlKeyValue struct {
	Key  string
	Line string
}

// setTOMLSectionKeys sets the given keys in one table of a TOML document,
// replacing the line of a key that is already set and adding the others under
// the header. Every other line, in the table and outside it, is kept.
func setTOMLSectionKeys(document string, header string, keys []tomlKeyValue) (string, bool) {
	lines := strings.SplitAfter(document, "\n")
	for start, line := range lines {
		if tomlHeaderOf(line) != header {
			continue
		}
		end := start + 1
		for end < len(lines) && !isTOMLTableHeader(lines[end]) {
			end++
		}
		section := append([]string{}, lines[start+1:end]...)
		var missing []string
		for _, key := range keys {
			isSet := false
			for index, sectionLine := range section {
				if tomlKeyOf(sectionLine) == key.Key {
					section[index] = key.Line
					isSet = true
				}
			}
			if !isSet {
				missing = append(missing, key.Line)
			}
		}
		headerLine := lines[start]
		if !strings.HasSuffix(headerLine, "\n") {
			headerLine += "\n"
		}
		rebuilt := strings.Join(lines[:start], "") + headerLine + strings.Join(missing, "") +
			strings.Join(section, "") + strings.Join(lines[end:], "")
		return rebuilt, true
	}
	return document, false
}

// tomlKeyOf answers the key a line sets, or "" for a line that sets none.
func tomlKeyOf(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	equalsAt := strings.Index(trimmed, "=")
	if equalsAt <= 0 {
		return ""
	}
	return strings.TrimSpace(trimmed[:equalsAt])
}

// tomlTableHeaderPattern is a table or array-of-tables header: bare or quoted
// key parts, never a comma, so a line of a multi-line array value that starts
// with "[" does not end the table.
var tomlTableHeaderPattern = regexp.MustCompile(`^\[\[?[A-Za-z0-9_.\-"' ]+\]\]?$`)

func isTOMLTableHeader(line string) bool {
	return tomlTableHeaderPattern.MatchString(tomlHeaderOf(line))
}

// tomlHeaderOf answers a line with any trailing comment removed, so a table
// header followed by "# note" still reads as that header.
func tomlHeaderOf(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return trimmed
	}
	if commentStart := strings.Index(trimmed, "#"); commentStart >= 0 {
		trimmed = strings.TrimSpace(trimmed[:commentStart])
	}
	return trimmed
}

// writeMCPClientConfig replaces a client's config in one step, keeping its mode.
func writeMCPClientConfig(path string, content []byte) error {
	directory := filepath.Dir(path)
	if makeError := os.MkdirAll(directory, 0o755); makeError != nil {
		return makeError
	}
	mode := os.FileMode(0o600)
	if info, statError := os.Stat(path); statError == nil {
		mode = info.Mode().Perm()
	}
	temporary, createError := os.CreateTemp(directory, filepath.Base(path)+".*.tmp")
	if createError != nil {
		return createError
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, writeError := temporary.Write(content); writeError != nil {
		_ = temporary.Close()
		return writeError
	}
	if closeError := temporary.Close(); closeError != nil {
		return closeError
	}
	if chmodError := os.Chmod(temporary.Name(), mode); chmodError != nil {
		return chmodError
	}
	return os.Rename(temporary.Name(), path)
}
