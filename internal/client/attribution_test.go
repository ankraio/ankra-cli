package client

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"testing"
)

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// withInvocation sets the process-wide invocation state for one test and
// restores it afterwards.
func withInvocation(t *testing.T, version string, commandPath string) {
	t.Helper()
	previousVersion, previousCommand := invocationState()
	t.Cleanup(func() {
		invocation.Lock()
		invocation.version, invocation.command = previousVersion, previousCommand
		invocation.Unlock()
	})
	SetVersion(version)
	SetInvocationCommand(commandPath)
}

var sessionDigestPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestDetectAgentReadsClaudeCodeFromTheAIAgentConvention(t *testing.T) {
	agent := detectAgent(environment(map[string]string{
		"AI_AGENT":               "claude-code_2-1-316_agent",
		"CLAUDECODE":             "1",
		"CLAUDE_CODE_SESSION_ID": "5d1c0b6e-0000-4000-8000-000000000000",
	}))
	if agent.name != "claude-code" || agent.version != "2.1.316" {
		t.Fatalf("agent = %+v", agent)
	}
	if !sessionDigestPattern.MatchString(agent.session) {
		t.Fatalf("session must be a 32-character hex digest, got %q", agent.session)
	}
	if agent.session == "5d1c0b6e-0000-4000-8000-000000000000" {
		t.Fatal("the session id itself must never be sent")
	}
}

func TestDetectAgentCases(t *testing.T) {
	cases := []struct {
		name        string
		environment map[string]string
		wantName    string
		wantVersion string
		wantSession bool
	}{
		{name: "nothing set", environment: map[string]string{}, wantName: ""},
		{name: "explicit override wins", wantName: "my-bot", wantVersion: "1.2",
			environment: map[string]string{"ANKRA_AGENT": "my-bot/1.2", "CLAUDECODE": "1"}},
		{name: "bare claude in AI_AGENT is Claude Code", wantName: "claude-code",
			environment: map[string]string{"AI_AGENT": "claude"}},
		{name: "copilot in AI_AGENT", wantName: "copilot",
			environment: map[string]string{"AI_AGENT": "github-copilot-cli"}},
		{name: "claude code marker without AI_AGENT", wantName: "claude-code", wantSession: true,
			environment: map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "abc"}},
		{name: "cursor agent", wantName: "cursor",
			environment: map[string]string{"CURSOR_AGENT": "1"}},
		{name: "cursor agent exec host", wantName: "cursor",
			environment: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": "agent-exec"}},
		{name: "a person in the cursor terminal is not an agent", wantName: "",
			environment: map[string]string{"CURSOR_TRACE_ID": "trace", "CURSOR_EXTENSION_HOST_ROLE": "user"}},
		{name: "a person in a replit shell is not an agent", wantName: "",
			environment: map[string]string{"REPL_ID": "repl"}},
		{name: "codex with its thread", wantName: "codex", wantSession: true,
			environment: map[string]string{"CODEX_THREAD_ID": "thread-1"}},
		{name: "gemini", wantName: "gemini-cli", environment: map[string]string{"GEMINI_CLI": "1"}},
		{name: "opencode", wantName: "opencode", environment: map[string]string{"OPENCODE": "1"}},
		{name: "opencode needs 1", wantName: "", environment: map[string]string{"OPENCODE": "0"}},
		{name: "unsafe override is dropped", wantName: "",
			environment: map[string]string{"ANKRA_AGENT": "evil; rm -rf"}},
		{name: "unsafe version is dropped, name kept", wantName: "my-bot", wantVersion: "",
			environment: map[string]string{"ANKRA_AGENT": "my-bot/1.2 (x)"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			agent := detectAgent(environment(testCase.environment))
			if agent.name != testCase.wantName || agent.version != testCase.wantVersion {
				t.Fatalf("agent = %+v, want name %q version %q", agent, testCase.wantName, testCase.wantVersion)
			}
			if hasSession := agent.session != ""; hasSession != testCase.wantSession {
				t.Fatalf("session = %q, want present %v", agent.session, testCase.wantSession)
			}
		})
	}
}

func TestApplyClientAttributionSetsEveryHeader(t *testing.T) {
	withInvocation(t, "0.31.0", "ankra cluster get pods")
	header := http.Header{}
	applyClientAttribution(header, environment(map[string]string{
		"AI_AGENT":               "claude-code_2-1-316_agent",
		"CLAUDE_CODE_SESSION_ID": "session",
	}))
	if got, want := header.Get("User-Agent"), "ankra-cli/0.31.0 ("+runtime.GOOS+"; "+runtime.GOARCH+")"; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
	if header.Get(agentHeader) != "claude-code/2.1.316" {
		t.Fatalf("%s = %q", agentHeader, header.Get(agentHeader))
	}
	if !sessionDigestPattern.MatchString(header.Get(agentSessionHeader)) {
		t.Fatalf("%s = %q", agentSessionHeader, header.Get(agentSessionHeader))
	}
	if header.Get(cliCommandHeader) != "cluster get pods" {
		t.Fatalf("%s = %q", cliCommandHeader, header.Get(cliCommandHeader))
	}
}

func TestApplyClientAttributionHonoursTheOptOut(t *testing.T) {
	withInvocation(t, "0.31.0", "ankra cluster get pods")
	for _, optOut := range []map[string]string{
		{"DO_NOT_TRACK": "1", "CLAUDECODE": "1"},
		{"DO_NOT_TRACK": "true", "CLAUDECODE": "1"},
		{"ANKRA_TELEMETRY": "off", "CLAUDECODE": "1"},
		{"ANKRA_TELEMETRY": "0", "CLAUDECODE": "1"},
	} {
		header := http.Header{}
		applyClientAttribution(header, environment(optOut))
		if header.Get("User-Agent") == "" {
			t.Fatalf("%v: the User-Agent is always sent", optOut)
		}
		for _, name := range []string{agentHeader, agentSessionHeader, cliCommandHeader} {
			if header.Get(name) != "" {
				t.Fatalf("%v: %s must not be sent after an opt-out, got %q", optOut, name, header.Get(name))
			}
		}
	}
}

func TestApplyClientAttributionWithoutAnAgentSendsTheCommandOnly(t *testing.T) {
	withInvocation(t, "0.31.0", "ankra org current")
	header := http.Header{}
	applyClientAttribution(header, environment(map[string]string{}))
	if header.Get(agentHeader) != "" || header.Get(agentSessionHeader) != "" {
		t.Fatalf("no agent must be named: %v", header)
	}
	if header.Get(cliCommandHeader) != "org current" {
		t.Fatalf("%s = %q", cliCommandHeader, header.Get(cliCommandHeader))
	}
}

func TestSetInvocationCommandKeepsThePathOnly(t *testing.T) {
	withInvocation(t, "0.31.0", "ankra   cluster  get   pods")
	if _, command := invocationState(); command != "cluster get pods" {
		t.Fatalf("command = %q", command)
	}
	SetInvocationCommand("ankra")
	if _, command := invocationState(); command != "" {
		t.Fatalf("the root command alone names no command, got %q", command)
	}
}

func TestSetVersionIgnoresAnEmptyVersion(t *testing.T) {
	withInvocation(t, "0.31.0", "")
	SetVersion("  ")
	if version, _ := invocationState(); version != "0.31.0" {
		t.Fatalf("version = %q", version)
	}
}

func TestAgentSessionDigestOfNoSessionIsEmpty(t *testing.T) {
	if agentSessionDigest("  ") != "" {
		t.Fatal("no session must digest to nothing")
	}
	if agentSessionDigest("a") == agentSessionDigest("b") {
		t.Fatal("different sessions must digest differently")
	}
}

// TestClientRequestsCarryTheAttribution drives a real request through New's
// shared transport, which is what every API call rides.
func TestClientRequestsCarryTheAttribution(t *testing.T) {
	withInvocation(t, "0.31.0", "ankra cluster kube-token")
	previousLookup := environmentLookup
	environmentLookup = environment(map[string]string{"CODEX_THREAD_ID": "thread-7"})
	t.Cleanup(func() { environmentLookup = previousLookup })

	var received http.Header
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = request.Header.Clone()
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	apiClient := New("token", server.URL)
	apiClient.SetOrganisationOverride("org-1")
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/clusters", nil)
	response, requestError := apiClient.HTTP.Do(request)
	if requestError != nil {
		t.Fatal(requestError)
	}
	_ = response.Body.Close()

	if received.Get(agentHeader) != "codex" || received.Get(cliCommandHeader) != "cluster kube-token" ||
		!sessionDigestPattern.MatchString(received.Get(agentSessionHeader)) {
		t.Fatalf("attribution headers = %v", received)
	}
	if received.Get(orgOverrideHeader) != "org-1" {
		t.Fatalf("the organisation override must still be sent: %v", received)
	}
	if request.Header.Get(agentHeader) != "" {
		t.Fatal("the caller's request must not be modified")
	}
}
