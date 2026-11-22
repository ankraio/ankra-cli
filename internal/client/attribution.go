package client

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
)

// The headers that tell the platform which software made a request: the
// CLI's version, the coding agent driving it, and the command that was run.
// Most CLI traffic now comes from coding agents, and without these the
// platform could see a token but not who or what was using it. They carry
// names only: never an argument, a flag value, a path or the agent's own
// session id.
const (
	agentHeader        = "X-Ankra-Agent"
	agentSessionHeader = "X-Ankra-Agent-Session"
	cliCommandHeader   = "X-Ankra-CLI-Command"
)

// userAgentProduct is the product token the platform recognises the CLI by.
const userAgentProduct = "ankra-cli"

// Environment variables a user sets to control attribution. ANKRA_AGENT
// names the agent explicitly ("<name>" or "<name>/<version>") for one the CLI
// does not detect. DO_NOT_TRACK (the cross-tool convention) or
// ANKRA_TELEMETRY=off sends only the User-Agent: no agent, session or
// command.
const (
	envAnkraAgent     = "ANKRA_AGENT"
	envAnkraTelemetry = "ANKRA_TELEMETRY"
	envDoNotTrack     = "DO_NOT_TRACK"
)

var invocation = struct {
	sync.RWMutex
	version string
	command string
	// organisationLevel marks a command that answers for the organisation by
	// design, so it never warns that a selected project was not applied.
	organisationLevel bool
}{version: "dev"}

// SetVersion records the CLI version the User-Agent names.
func SetVersion(version string) {
	if strings.TrimSpace(version) == "" {
		return
	}
	invocation.Lock()
	defer invocation.Unlock()
	invocation.version = strings.TrimSpace(version)
}

// SetInvocationCommand records the command this invocation runs, as cobra's
// command path ("ankra cluster get pods"). Only the path is kept, never the
// arguments, and the root command's own name is dropped.
func SetInvocationCommand(commandPath string) {
	fields := strings.Fields(commandPath)
	if len(fields) > 0 && fields[0] == "ankra" {
		fields = fields[1:]
	}
	invocation.Lock()
	defer invocation.Unlock()
	invocation.command = strings.Join(fields, " ")
}

// SetInvocationOrganisationLevel records that this invocation's command belongs
// to the organisation by design, and so never warns that a selected project was
// not applied.
func SetInvocationOrganisationLevel(isOrganisationLevel bool) {
	invocation.Lock()
	defer invocation.Unlock()
	invocation.organisationLevel = isOrganisationLevel
}

func invocationIsOrganisationLevel() bool {
	invocation.RLock()
	defer invocation.RUnlock()
	return invocation.organisationLevel
}

func invocationState() (string, string) {
	invocation.RLock()
	defer invocation.RUnlock()
	return invocation.version, invocation.command
}

// userAgent is "ankra-cli/<version> (<os>; <arch>)".
func userAgent(version string) string {
	return userAgentProduct + "/" + version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

// applyClientAttribution sets the attribution headers on a request bound for
// the Ankra API. The User-Agent is always sent; the rest only while
// attribution is not switched off.
func applyClientAttribution(header http.Header, lookupEnvironment func(string) string) {
	version, command := invocationState()
	header.Set("User-Agent", userAgent(version))
	if attributionDisabled(lookupEnvironment) {
		return
	}
	agent := detectAgent(lookupEnvironment)
	if agent.name != "" {
		declared := agent.name
		if agent.version != "" {
			declared += "/" + agent.version
		}
		header.Set(agentHeader, declared)
	}
	if agent.session != "" {
		header.Set(agentSessionHeader, agent.session)
	}
	if command != "" {
		header.Set(cliCommandHeader, command)
	}
}

// attributionDisabled reports whether the user opted out of agent, session
// and command attribution.
func attributionDisabled(lookupEnvironment func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(lookupEnvironment(envDoNotTrack))) {
	case "1", "true", "yes":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(lookupEnvironment(envAnkraTelemetry))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// detectedAgent is the coding agent the CLI runs under.
type detectedAgent struct {
	name    string
	version string
	// session is a digest of the agent's session id (agentSessionDigest).
	session string
}

// agentMarkers are the environment variables coding agents set in the shells
// they run commands in, after @vercel/detect-agent, tried in order. Two of
// its markers are left out on purpose: CURSOR_TRACE_ID and REPL_ID are set in
// a person's own Cursor or Replit terminal too, and would name a human an
// agent.
var agentMarkers = []struct {
	variables []string
	matches   func(value string) bool
	agent     string
}{
	{[]string{"CLAUDECODE", "CLAUDE_CODE"}, isSet, "claude-code"},
	{[]string{"CURSOR_AGENT"}, isSet, "cursor"},
	{[]string{"CURSOR_EXTENSION_HOST_ROLE"}, equals("agent-exec"), "cursor"},
	{[]string{"GEMINI_CLI"}, isSet, "gemini-cli"},
	{[]string{"CODEX_SANDBOX", "CODEX_CI", "CODEX_THREAD_ID"}, isSet, "codex"},
	{[]string{"ANTIGRAVITY_AGENT"}, isSet, "antigravity"},
	{[]string{"AUGMENT_AGENT"}, isSet, "augment"},
	{[]string{"OPENCODE"}, equals("1"), "opencode"},
	{[]string{"OPENCODE_CLIENT"}, isSet, "opencode"},
	{[]string{"COPILOT_MODEL", "COPILOT_ALLOW_ALL"}, isSet, "copilot"},
}

// agentSessionVariables name each agent's own session id, which the CLI
// digests (agentSessionDigest) so the platform can group one session's
// requests without the id itself leaving the machine.
var agentSessionVariables = map[string]string{
	"claude-code": "CLAUDE_CODE_SESSION_ID",
	"codex":       "CODEX_THREAD_ID",
}

func isSet(value string) bool { return strings.TrimSpace(value) != "" }

func equals(expected string) func(string) bool {
	return func(value string) bool { return strings.TrimSpace(value) == expected }
}

// DetectedAgentName names the coding agent this process runs under (Claude
// Code, Codex, Cursor, ...), from the same markers the request attribution
// reads, or "" when none is detected. ANKRA_AGENT names one the CLI does not
// detect. Unlike the attribution headers it is not switched off by
// DO_NOT_TRACK: callers use it to decide how to behave, not to report.
func DetectedAgentName() string {
	return detectAgent(os.Getenv).name
}

// detectAgent names the agent from, in order: ANKRA_AGENT, the AI_AGENT
// convention ("claude-code_2-1-316_agent", "github-copilot"), then the
// agents' own markers. The version comes from whichever source carries one.
func detectAgent(lookupEnvironment func(string) string) detectedAgent {
	var agent detectedAgent
	if declared := strings.TrimSpace(lookupEnvironment(envAnkraAgent)); declared != "" {
		agent.name, agent.version, _ = strings.Cut(declared, "/")
	} else if conventional := strings.TrimSpace(lookupEnvironment("AI_AGENT")); conventional != "" {
		agent.name, agent.version = parseAIAgent(conventional)
	} else {
		for _, marker := range agentMarkers {
			if markerMatches(lookupEnvironment, marker.variables, marker.matches) {
				agent.name = marker.agent
				break
			}
		}
	}
	agent.name = sanitiseAgentField(agent.name)
	agent.version = sanitiseAgentField(agent.version)
	if agent.name == "" {
		return detectedAgent{}
	}
	if sessionVariable, hasSession := agentSessionVariables[agent.name]; hasSession {
		agent.session = agentSessionDigest(lookupEnvironment(sessionVariable))
	}
	return agent
}

func markerMatches(lookupEnvironment func(string) string, variables []string, matches func(string) bool) bool {
	for _, variable := range variables {
		if matches(lookupEnvironment(variable)) {
			return true
		}
	}
	return false
}

// parseAIAgent reads the AI_AGENT convention. Claude Code writes
// "<name>_<version with dashes>_agent"; most agents write a bare name.
// "claude" alone is Claude Code too: it is the value @vercel/detect-agent
// reports for it, and no other Claude product sets the variable in a shell.
func parseAIAgent(value string) (string, string) {
	trimmed := strings.TrimSuffix(value, "_agent")
	name, version, _ := strings.Cut(trimmed, "_")
	version = strings.ReplaceAll(version, "-", ".")
	switch name {
	case "claude":
		name = "claude-code"
	case "github-copilot", "github-copilot-cli":
		name = "copilot"
	}
	return name, version
}

// sanitiseAgentField keeps a header-safe name or version: letters, digits
// and . _ - + only, at most 64 characters, or nothing.
func sanitiseAgentField(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 64 {
		return ""
	}
	for _, character := range trimmed {
		isAllowed := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._-+", character)
		if !isAllowed {
			return ""
		}
	}
	return trimmed
}

// agentSessionDigest is the first 32 hex characters of a SHA-256 over the
// agent's session id, namespaced so it matches no other system's digest of
// the same id. Empty for no session.
func agentSessionDigest(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("ankra-agent-session:" + trimmed))
	return hex.EncodeToString(digest[:])[:32]
}

// requestDestination is where a request goes, relative to the client's API
// host.
type requestDestination int

const (
	// destinationUnknown: the transport has no readable base URL, so it
	// cannot tell. Attribution is for the API only and is left off; the
	// path check is applied, so it fails closed.
	destinationUnknown requestDestination = iota
	// destinationAPI: the client's own API host, the only host the
	// attribution headers are for and the one whose edge rewrites paths.
	destinationAPI
	// destinationOtherHost: somewhere else, such as a presigned storage URL.
	destinationOtherHost
)

// destination says where a request goes, comparing host and port with the
// base URL's. It parses the base URL once, so RoundTrip calls it once per
// request and reuses the answer.
func (t *scopeOverrideTransport) destination(requestURL *url.URL) requestDestination {
	if t.baseURL == nil || requestURL == nil {
		return destinationUnknown
	}
	apiURL, parseError := url.Parse(*t.baseURL)
	if parseError != nil || apiURL.Host == "" {
		return destinationUnknown
	}
	if hostAndPort(apiURL) == hostAndPort(requestURL) {
		return destinationAPI
	}
	return destinationOtherHost
}

// hostAndPort is a URL's lower-cased host with its port, the scheme's
// default port when the URL names none.
func hostAndPort(address *url.URL) string {
	port := address.Port()
	if port == "" {
		switch strings.ToLower(address.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(address.Hostname()), port)
}

func (t *scopeOverrideTransport) environment() func(string) string {
	if t.lookupEnvironment != nil {
		return t.lookupEnvironment
	}
	return os.Getenv
}
