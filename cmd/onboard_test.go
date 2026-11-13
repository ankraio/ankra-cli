package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/viper"
)

// onboardFakePlatform answers the two login routes a browser sign-in uses.
type onboardFakePlatform struct {
	server *httptest.Server

	mu          sync.Mutex
	initQueries []url.Values
	polls       int
	userCode    string
	// poll answers the n-th poll (1-based) with a status and a body.
	poll func(n int) (int, any)
}

func newOnboardFakePlatform(t *testing.T, userCode string) *onboardFakePlatform {
	t.Helper()
	platform := &onboardFakePlatform{userCode: userCode}
	platform.poll = func(int) (int, any) {
		return http.StatusOK, tokenExchangeResponse{
			Token: "onboard-token", TokenID: "token-id-1", TokenName: "CLI onboard", ExpiresAt: "2027-02-01T00:00:00Z", Status: "issued",
		}
	}
	platform.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		platform.mu.Lock()
		defer platform.mu.Unlock()
		switch request.URL.Path {
		case "/api/v1/cli/login/init":
			platform.initQueries = append(platform.initQueries, request.URL.Query())
			number := len(platform.initQueries)
			response := map[string]string{
				"auth_url": fmt.Sprintf("https://auth.example.test/authorize?ticket=%d", number),
				"ticket":   fmt.Sprintf("ticket-%d", number),
				"state":    "state",
			}
			if platform.userCode != "" {
				response["user_code"] = platform.userCode
			}
			_ = json.NewEncoder(writer).Encode(response)
		case "/api/v1/cli/login/poll":
			platform.polls++
			status, body := platform.poll(platform.polls)
			writer.WriteHeader(status)
			_ = json.NewEncoder(writer).Encode(body)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(platform.server.Close)
	viper.Set("base-url", platform.server.URL)
	return platform
}

func (platform *onboardFakePlatform) inits() []url.Values {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	return append([]url.Values(nil), platform.initQueries...)
}

// onboardMock is the platform API behind a signed-in onboard.
type onboardMock struct {
	baseMock
	organisations  []client.OrganisationSummary
	switchedTo     []string
	override       string
	clusters       int
	createResult   *client.CreatePlaygroundResult
	createError    error
	creates        int
	statuses       []client.PlaygroundStatus
	statusRequests int
	clustersError  error
}

func (m *onboardMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return m.organisations, nil
}

func (m *onboardMock) SwitchOrganisation(organisationID string) (*client.SwitchOrganisationResponse, error) {
	m.switchedTo = append(m.switchedTo, organisationID)
	return &client.SwitchOrganisationResponse{Success: true}, nil
}

func (m *onboardMock) SetOrganisationOverride(organisationID string) { m.override = organisationID }

func (m *onboardMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	if m.clustersError != nil {
		return nil, m.clustersError
	}
	response := &client.ClusterListResponse{Pagination: client.Pagination{TotalCount: m.clusters}}
	if m.clusters > 0 {
		response.Result = []client.ClusterListItem{{ID: "cluster-1", Name: "prod"}}
	}
	return response, nil
}

func (m *onboardMock) CreatePlayground(string) (*client.CreatePlaygroundResult, error) {
	m.creates++
	return m.createResult, m.createError
}

func (m *onboardMock) GetPlaygroundStatus(clusterID string) (*client.PlaygroundStatus, error) {
	if len(m.statuses) == 0 {
		return nil, errors.New("no status scripted")
	}
	index := m.statusRequests
	if index >= len(m.statuses) {
		index = len(m.statuses) - 1
	}
	m.statusRequests++
	status := m.statuses[index]
	status.ClusterID = clusterID
	return &status, nil
}

func onboardOrg(id string, name string, isCurrent bool) client.OrganisationSummary {
	return client.OrganisationSummary{OrganisationID: id, Name: &name, UserCurrent: isCurrent}
}

var onboardFlagNames = []string{"json", "wait", "signup", "login", "new", "yes", "no-playground", "no-agent-setup"}

// setUpOnboardTest isolates one onboard test: a temporary HOME with no
// login, the mock as the API client, no terminal, no agent, and fast polls.
func setUpOnboardTest(t *testing.T, mock APIClient) string {
	t.Helper()
	withFreshViperAndEnv(t)
	home := withTempHome(t)
	withShortLoginHTTPClient(t)

	originalClient, originalToken, originalBaseURL := apiClient, apiToken, baseURL
	originalTerminal, originalAgent, originalChoose := onboardIsTerminal, onboardAgentName, onboardChoose
	originalBrowser, originalLook, originalMCP := launchBrowser, lookMCPClientCommand, onboardRunMCPConnect
	originalLoginPoll, originalPlaygroundPoll := loginPollInterval, onboardPlaygroundPollInterval
	t.Cleanup(func() {
		apiClient, apiToken, baseURL = originalClient, originalToken, originalBaseURL
		onboardIsTerminal, onboardAgentName, onboardChoose = originalTerminal, originalAgent, originalChoose
		launchBrowser, lookMCPClientCommand, onboardRunMCPConnect = originalBrowser, originalLook, originalMCP
		loginPollInterval, onboardPlaygroundPollInterval = originalLoginPoll, originalPlaygroundPoll
		resetOnboardFlags()
		rootCmd.SetIn(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	apiClient = mock
	onboardIsTerminal = func(any) bool { return false }
	onboardAgentName = func() string { return "" }
	onboardChoose = func(io.Reader, io.Writer, string, []string, int) (int, error) {
		t.Error("unexpected prompt")
		return 0, errCancelled
	}
	launchBrowser = func(string) error { return nil }
	lookMCPClientCommand = func(string) (string, error) { return "", errors.New("not found") }
	onboardRunMCPConnect = func(io.Writer, io.Writer) error {
		t.Error("unexpected MCP connect")
		return nil
	}
	loginPollInterval = 5 * time.Millisecond
	onboardPlaygroundPollInterval = 5 * time.Millisecond
	return home
}

func resetOnboardFlags() {
	for _, name := range onboardFlagNames {
		_ = onboardCmd.Flags().Set(name, "false")
	}
}

// executeOnboard runs `ankra onboard` with separate stdout and stderr, so a
// test can hold stdout to being NDJSON and nothing else.
func executeOnboard(t *testing.T, stdin string, arguments ...string) (string, string, error) {
	t.Helper()
	resetOnboardFlags()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(append([]string{"onboard"}, arguments...))
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

// parseOnboardEvents reads stdout as NDJSON and holds every line to the
// event contract: "event" first, an instruction always.
func parseOnboardEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			t.Fatalf("empty line in the NDJSON stream:\n%s", stdout)
		}
		if !strings.HasPrefix(line, `{"event":`) {
			t.Fatalf("every line must be a JSON object starting with \"event\", got %q", line)
		}
		var event map[string]any
		if decodeError := json.Unmarshal([]byte(line), &event); decodeError != nil {
			t.Fatalf("line is not JSON: %v\n%s", decodeError, line)
		}
		if instruction, _ := event["instruction"].(string); strings.TrimSpace(instruction) == "" {
			t.Fatalf("event %v has no instruction", event["event"])
		}
		if nextCommand, present := event["next_command"]; present {
			if text, _ := nextCommand.(string); text == "" {
				t.Fatalf("event %v carries an empty next_command", event["event"])
			}
		}
		events = append(events, event)
	}
	return events
}

func onboardEventNames(events []map[string]any) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, fmt.Sprint(event["event"]))
	}
	return names
}

func findOnboardEvent(t *testing.T, events []map[string]any, name string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["event"] == name {
			return event
		}
	}
	t.Fatalf("no %s event in %v", name, onboardEventNames(events))
	return nil
}

func readPendingLogin(t *testing.T, home string) (onboardPendingLogin, os.FileInfo) {
	t.Helper()
	path := filepath.Join(home, ".ankra", "onboard-login.json")
	info, statError := os.Stat(path)
	if statError != nil {
		t.Fatalf("pending sign-in file: %v", statError)
	}
	content, _ := os.ReadFile(path)
	var pending onboardPendingLogin
	if decodeError := json.Unmarshal(content, &pending); decodeError != nil {
		t.Fatalf("pending sign-in file: %v", decodeError)
	}
	return pending, info
}

func pendingLoginExists(home string) bool {
	_, statError := os.Stat(filepath.Join(home, ".ankra", "onboard-login.json"))
	return statError == nil
}

func TestOnboardSignedInSkipsTheSignIn(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}, clusters: 2}
	home := setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test", "token_name": "CLI laptop"})
	// A sign-in link left from an earlier run is moot once signed in.
	if saveError := saveOnboardPendingLogin(&onboardPendingLogin{
		BaseURL: "https://platform.example.test", Ticket: "t", CodeVerifier: "v", AuthURL: "https://a", ExpiresAt: time.Now().Add(time.Minute),
	}); saveError != nil {
		t.Fatal(saveError)
	}

	stdout, stderr, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
	if runError != nil {
		t.Fatalf("onboard: %v\nstderr: %s", runError, stderr)
	}
	events := parseOnboardEvents(t, stdout)
	if got, want := strings.Join(onboardEventNames(events), ","), "onboarding_start,signed_in,organisation,clusters,done"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	signedIn := findOnboardEvent(t, events, "signed_in")
	if signedIn["already_signed_in"] != true || signedIn["token_name"] != "CLI laptop" || signedIn["token_source"] != "config" {
		t.Fatalf("signed_in = %v", signedIn)
	}
	if count := findOnboardEvent(t, events, "clusters")["count"]; count != float64(2) {
		t.Fatalf("clusters count = %v, want 2", count)
	}
	if mock.override != "org-1" {
		t.Fatalf("the run must be scoped to the organisation, override = %q", mock.override)
	}
	if pendingLoginExists(home) {
		t.Fatal("the stale pending sign-in must be removed once signed in")
	}
	done := findOnboardEvent(t, events, "done")
	if done["portal_url"] != "https://platform.example.test/next" {
		t.Fatalf("portal_url = %v", done["portal_url"])
	}
}

func TestOnboardWithoutATerminalSavesThePendingSignIn(t *testing.T) {
	home := setUpOnboardTest(t, &onboardMock{})
	platform := newOnboardFakePlatform(t, "WXYZ-1234")

	stdout, stderr, runError := executeOnboard(t, "", "--json")
	if runError != nil {
		t.Fatalf("a link waiting for the user is not a failure: %v\nstderr: %s", runError, stderr)
	}
	events := parseOnboardEvents(t, stdout)
	if got, want := strings.Join(onboardEventNames(events), ","), "onboarding_start,login_url"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	link := findOnboardEvent(t, events, "login_url")
	if link["url"] != "https://auth.example.test/authorize?ticket=1" || link["user_code"] != "WXYZ-1234" || link["reused"] != false {
		t.Fatalf("login_url = %v", link)
	}
	if link["next_command"] != "ankra onboard --wait --json" {
		t.Fatalf("next_command = %v", link["next_command"])
	}
	if instruction := link["instruction"].(string); !strings.Contains(instruction, "must match WXYZ-1234") ||
		!strings.Contains(instruction, "never replaces this link") {
		t.Fatalf("instruction = %q", instruction)
	}
	if _, parseError := time.Parse(time.RFC3339, fmt.Sprint(link["expires_at"])); parseError != nil {
		t.Fatalf("expires_at = %v: %v", link["expires_at"], parseError)
	}

	pending, info := readPendingLogin(t, home)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("the pending sign-in carries the code verifier and must be 0600, got %#o", info.Mode().Perm())
	}
	if pending.Ticket != "ticket-1" || pending.CodeVerifier == "" || pending.UserCode != "WXYZ-1234" ||
		pending.BaseURL != platform.server.URL || pending.MachineID == "" {
		t.Fatalf("pending = %+v", pending)
	}
	if lifetime := pending.ExpiresAt.Sub(pending.CreatedAt); lifetime != onboardPendingLoginLifetime {
		t.Fatalf("pending lifetime = %s, want %s", lifetime, onboardPendingLoginLifetime)
	}

	inits := platform.inits()
	if len(inits) != 1 {
		t.Fatalf("init requests = %d, want 1", len(inits))
	}
	query := inits[0]
	if query.Get("source") != "onboard" || query.Get("user_code") != "1" || query.Get("code_challenge") == "" {
		t.Fatalf("init query = %v", query)
	}
	if query.Has("screen_hint") {
		t.Fatalf("without --signup the page must offer both sign-in and sign-up, got screen_hint=%q", query.Get("screen_hint"))
	}
}

func TestOnboardRerunReusesThePendingLink(t *testing.T) {
	setUpOnboardTest(t, &onboardMock{})
	platform := newOnboardFakePlatform(t, "WXYZ-1234")

	if _, _, runError := executeOnboard(t, "", "--json"); runError != nil {
		t.Fatal(runError)
	}
	stdout, _, runError := executeOnboard(t, "", "--json")
	if runError != nil {
		t.Fatal(runError)
	}
	link := findOnboardEvent(t, parseOnboardEvents(t, stdout), "login_url")
	if link["reused"] != true || link["url"] != "https://auth.example.test/authorize?ticket=1" {
		t.Fatalf("the rerun must show the same link, got %v", link)
	}
	if inits := platform.inits(); len(inits) != 1 {
		t.Fatalf("the rerun must not start a second sign-in, init requests = %d", len(inits))
	}
}

// An explicit --signup or --login the saved link was not started with gets a
// link that opens the page asked for; --wait keeps the link the person has.
func TestOnboardExplicitAccountChoiceReplacesADifferentPendingLink(t *testing.T) {
	home := setUpOnboardTest(t, &onboardMock{})
	platform := newOnboardFakePlatform(t, "")

	if _, _, runError := executeOnboard(t, "", "--json", "--signup"); runError != nil {
		t.Fatal(runError)
	}
	if pending, _ := readPendingLogin(t, home); !pending.Signup {
		t.Fatal("the pending sign-in must remember it was started for a new account")
	}
	if _, _, runError := executeOnboard(t, "", "--json", "--signup"); runError != nil {
		t.Fatal(runError)
	}
	if inits := platform.inits(); len(inits) != 1 {
		t.Fatalf("the same choice must reuse the link, init requests = %d", len(inits))
	}
	stdout, _, runError := executeOnboard(t, "", "--json", "--login")
	if runError != nil {
		t.Fatal(runError)
	}
	link := findOnboardEvent(t, parseOnboardEvents(t, stdout), "login_url")
	if link["reused"] != false || link["url"] != "https://auth.example.test/authorize?ticket=2" {
		t.Fatalf("--login after a sign-up link must hand out a new link, got %v", link)
	}
	inits := platform.inits()
	if len(inits) != 2 || inits[1].Has("screen_hint") {
		t.Fatalf("the --login link must not ask for the sign-up screen, init queries = %v", inits)
	}
	if pending, _ := readPendingLogin(t, home); pending.Signup || pending.Ticket != "ticket-2" {
		t.Fatalf("the file must hold the sign-in link, got %+v", pending)
	}
	if _, _, runError := executeOnboard(t, "", "--json"); runError != nil {
		t.Fatal(runError)
	}
	if inits := platform.inits(); len(inits) != 2 {
		t.Fatalf("without a choice any pending link is reused, init requests = %d", len(inits))
	}
}

func TestOnboardNewReplacesThePendingLink(t *testing.T) {
	home := setUpOnboardTest(t, &onboardMock{})
	platform := newOnboardFakePlatform(t, "")

	if _, _, runError := executeOnboard(t, "", "--json"); runError != nil {
		t.Fatal(runError)
	}
	stdout, _, runError := executeOnboard(t, "", "--json", "--new")
	if runError != nil {
		t.Fatal(runError)
	}
	link := findOnboardEvent(t, parseOnboardEvents(t, stdout), "login_url")
	if link["reused"] != false || link["url"] != "https://auth.example.test/authorize?ticket=2" {
		t.Fatalf("--new must hand out a new link, got %v", link)
	}
	if inits := platform.inits(); len(inits) != 2 {
		t.Fatalf("init requests = %d, want 2", len(inits))
	}
	if pending, _ := readPendingLogin(t, home); pending.Ticket != "ticket-2" {
		t.Fatalf("the file must hold the new sign-in, ticket = %q", pending.Ticket)
	}
}

func TestOnboardWaitCompletesTheSignIn(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}, clusters: 1}
	home := setUpOnboardTest(t, mock)
	platform := newOnboardFakePlatform(t, "WXYZ-1234")
	platform.poll = func(n int) (int, any) {
		if n == 1 {
			return http.StatusOK, tokenExchangeResponse{Status: "awaiting_approval"}
		}
		return http.StatusOK, tokenExchangeResponse{
			Token: "onboard-token", TokenID: "token-id-1", TokenName: "CLI onboard", ExpiresAt: "2027-02-01T00:00:00Z", Status: "issued",
		}
	}

	if _, _, runError := executeOnboard(t, "", "--json"); runError != nil {
		t.Fatal(runError)
	}
	stdout, stderr, runError := executeOnboard(t, "", "--json", "--wait", "--no-agent-setup")
	if runError != nil {
		t.Fatalf("onboard --wait: %v\nstderr: %s", runError, stderr)
	}
	events := parseOnboardEvents(t, stdout)
	if got, want := strings.Join(onboardEventNames(events), ","), "onboarding_start,signed_in,organisation,clusters,done"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	signedIn := findOnboardEvent(t, events, "signed_in")
	if signedIn["already_signed_in"] != false || signedIn["token_name"] != "CLI onboard" {
		t.Fatalf("signed_in = %v", signedIn)
	}
	if !strings.Contains(stderr, "Approve the sign-in in your browser") {
		t.Fatalf("the waiting hint belongs on stderr in JSON mode, stderr = %q", stderr)
	}
	if inits := platform.inits(); len(inits) != 1 {
		t.Fatalf("--wait must collect the saved sign-in, not start another (init requests = %d)", len(inits))
	}

	configPath := filepath.Join(home, ".ankra.yaml")
	content, readError := os.ReadFile(configPath)
	if readError != nil {
		t.Fatal(readError)
	}
	if !strings.Contains(string(content), "token: onboard-token") || !strings.Contains(string(content), "token_id: token-id-1") {
		t.Fatalf("credentials not saved like ankra login does:\n%s", content)
	}
	if info, _ := os.Stat(configPath); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %#o, want 0600", info.Mode().Perm())
	}
	if pendingLoginExists(home) {
		t.Fatal("the pending sign-in must be removed once collected")
	}
}

func TestOnboardWaitOnAnExpiredLinkExitsWithTheWaitCode(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		body   any
	}{
		{name: "the platform says the ticket is gone", status: http.StatusGone, body: map[string]string{"detail": "Your login session expired."}},
		{name: "still pending when the link runs out", status: http.StatusOK, body: tokenExchangeResponse{Status: "pending"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := setUpOnboardTest(t, &onboardMock{})
			platform := newOnboardFakePlatform(t, "")
			platform.poll = func(int) (int, any) { return testCase.status, testCase.body }
			if saveError := saveOnboardPendingLogin(&onboardPendingLogin{
				BaseURL: platform.server.URL, MachineID: "m", Ticket: "old-ticket", CodeVerifier: "v",
				AuthURL: "https://auth.example.test/old", CreatedAt: time.Now().Add(-11 * time.Minute), ExpiresAt: time.Now().Add(-time.Minute),
			}); saveError != nil {
				t.Fatal(saveError)
			}

			stdout, _, runError := executeOnboard(t, "", "--json", "--wait")
			if exitCodeFor(runError) != exitWaitTimeout {
				t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitWaitTimeout)
			}
			events := parseOnboardEvents(t, stdout)
			failure := events[len(events)-1]
			if failure["event"] != "error" || failure["exit_code"] != float64(exitWaitTimeout) || failure["next_command"] != "ankra onboard --json" {
				t.Fatalf("last event = %v", failure)
			}
			if !strings.Contains(fmt.Sprint(failure["message"]), "expired") {
				t.Fatalf("message = %v", failure["message"])
			}
			if pendingLoginExists(home) {
				t.Fatal("an expired sign-in must be removed")
			}
			if len(platform.inits()) != 0 {
				t.Fatal("an expired link must not silently start a new sign-in while the agent waits")
			}
		})
	}
}

func TestOnboardWithoutAUserCode(t *testing.T) {
	setUpOnboardTest(t, &onboardMock{})
	newOnboardFakePlatform(t, "")

	stdout, _, runError := executeOnboard(t, "", "--json")
	if runError != nil {
		t.Fatal(runError)
	}
	link := findOnboardEvent(t, parseOnboardEvents(t, stdout), "login_url")
	if _, present := link["user_code"]; present {
		t.Fatalf("an older platform sends no code, so none is shown: %v", link)
	}
	if strings.Contains(link["instruction"].(string), "must match") {
		t.Fatalf("instruction must not mention a code: %q", link["instruction"])
	}

	human, _, runError := executeOnboard(t, "")
	if runError != nil {
		t.Fatal(runError)
	}
	if strings.Contains(human, "Your code") || !strings.Contains(human, "ankra onboard --wait") ||
		!strings.Contains(human, "https://auth.example.test/authorize?ticket=1") {
		t.Fatalf("human output without a terminal:\n%s", human)
	}
}

func TestOnboardInteractiveSignupAsksForTheSignupScreen(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}, clusters: 1}
	setUpOnboardTest(t, mock)
	platform := newOnboardFakePlatform(t, "ABCD-EFGH")
	onboardIsTerminal = func(any) bool { return true }
	var asked []string
	onboardChoose = func(_ io.Reader, _ io.Writer, label string, items []string, defaultIndex int) (int, error) {
		asked = append(asked, label)
		if defaultIndex != 0 || !strings.Contains(items[0], "create a free account") {
			t.Errorf("account question items = %v, default %d", items, defaultIndex)
		}
		return 0, nil
	}
	var opened string
	launchBrowser = func(link string) error { opened = link; return nil }

	stdout, stderr, runError := executeOnboard(t, "", "--no-agent-setup")
	if runError != nil {
		t.Fatalf("onboard: %v\nstderr: %s", runError, stderr)
	}
	if len(asked) != 1 || asked[0] != "Do you have an Ankra account?" {
		t.Fatalf("questions = %v", asked)
	}
	query := platform.inits()[0]
	if query.Get("screen_hint") != "signup" || query.Get("source") != "onboard" || query.Get("user_code") != "1" {
		t.Fatalf("init query = %v", query)
	}
	if opened != "https://auth.example.test/authorize?ticket=1" {
		t.Fatalf("browser opened %q", opened)
	}
	for _, want := range []string{onboardTagline, "Your code: ABCD-EFGH (the browser shows the same code)", "Signed in to", "Organisation: Acme", "Next steps:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "—") {
		t.Fatal("no em dashes in onboard output")
	}
}

func TestOnboardLoginFlagSkipsTheAccountQuestion(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}, clusters: 1}
	setUpOnboardTest(t, mock)
	platform := newOnboardFakePlatform(t, "")
	onboardIsTerminal = func(any) bool { return true }

	if _, stderr, runError := executeOnboard(t, "", "--login", "--no-agent-setup"); runError != nil {
		t.Fatalf("onboard --login: %v\nstderr: %s", runError, stderr)
	}
	if query := platform.inits()[0]; query.Has("screen_hint") || query.Get("source") != "onboard" {
		t.Fatalf("--login opens the sign-in screen, init query = %v", query)
	}
}

func TestOnboardSignupAndLoginTogetherIsAUsageError(t *testing.T) {
	setUpOnboardTest(t, &onboardMock{})
	_, _, runError := executeOnboard(t, "", "--signup", "--login")
	if exitCodeFor(runError) != exitUsage {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitUsage)
	}
}

func TestOnboardUnderACodingAgentNeverPrompts(t *testing.T) {
	setUpOnboardTest(t, &onboardMock{})
	newOnboardFakePlatform(t, "")
	onboardIsTerminal = func(any) bool { return true }
	onboardAgentName = func() string { return "claude-code" }

	stdout, stderr, runError := executeOnboard(t, "")
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(stdout, "ankra onboard --wait") || !strings.Contains(stderr, "ankra onboard --json") {
		t.Fatalf("under an agent onboard prints the link and the JSON hint:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestOnboardOrganisationPickerSwitchesToTheChoice(t *testing.T) {
	mock := &onboardMock{
		organisations: []client.OrganisationSummary{onboardOrg("org-a", "Alpha", true), onboardOrg("org-b", "Beta", false)},
		clusters:      1,
	}
	home := setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})
	onboardIsTerminal = func(any) bool { return true }
	onboardChoose = func(_ io.Reader, _ io.Writer, label string, items []string, defaultIndex int) (int, error) {
		if label != "Which organisation do you want to work in?" || len(items) != 2 || defaultIndex != 0 {
			t.Errorf("picker = %q %v default %d", label, items, defaultIndex)
		}
		return 1, nil
	}

	if _, stderr, runError := executeOnboard(t, "", "--no-agent-setup"); runError != nil {
		t.Fatalf("onboard: %v\nstderr: %s", runError, stderr)
	}
	if strings.Join(mock.switchedTo, ",") != "org-b" || mock.override != "org-b" {
		t.Fatalf("switched to %v, override %q; want org-b", mock.switchedTo, mock.override)
	}
	content, readError := os.ReadFile(filepath.Join(home, ".ankra", "organisation.json"))
	if readError != nil || !strings.Contains(string(content), `"organisation_id":"org-b"`) {
		t.Fatalf("local selection = %s (%v)", content, readError)
	}
}

func TestOnboardOrganisationsInJSON(t *testing.T) {
	t.Run("several continue in the current one", func(t *testing.T) {
		mock := &onboardMock{
			organisations: []client.OrganisationSummary{onboardOrg("org-a", "Alpha", false), onboardOrg("org-b", "Beta", true)},
			clusters:      1,
		}
		setUpOnboardTest(t, mock)
		writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

		stdout, _, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
		if runError != nil {
			t.Fatal(runError)
		}
		event := findOnboardEvent(t, parseOnboardEvents(t, stdout), "organisation")
		chosen := event["organisation"].(map[string]any)
		listed := event["organisations"].([]any)
		if chosen["organisation_id"] != "org-b" || len(listed) != 2 {
			t.Fatalf("organisation event = %v", event)
		}
		if listed[0].(map[string]any)["current"] != false || listed[1].(map[string]any)["current"] != true {
			t.Fatalf("current flags = %v", listed)
		}
		if !strings.Contains(event["instruction"].(string), "ankra org switch <organisation_id>") {
			t.Fatalf("instruction = %q", event["instruction"])
		}
		if len(mock.switchedTo) != 0 || mock.override != "org-b" {
			t.Fatalf("an agent run must not switch, switched %v override %q", mock.switchedTo, mock.override)
		}
	})
	t.Run("none points at org create", func(t *testing.T) {
		mock := &onboardMock{organisations: []client.OrganisationSummary{}}
		setUpOnboardTest(t, mock)
		writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

		stdout, _, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
		if runError != nil {
			t.Fatal(runError)
		}
		events := parseOnboardEvents(t, stdout)
		event := findOnboardEvent(t, events, "organisation")
		if listed := event["organisations"].([]any); len(listed) != 0 {
			t.Fatalf("organisations = %v", listed)
		}
		if !strings.Contains(event["instruction"].(string), "ankra org create <name>") {
			t.Fatalf("instruction = %q", event["instruction"])
		}
		for _, name := range onboardEventNames(events) {
			if name == "clusters" || name == "playground_offer" {
				t.Fatal("without an organisation there is no cluster step")
			}
		}
	})
}

func TestOnboardOffersAPlaygroundWithoutClusters(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

	stdout, _, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
	if runError != nil {
		t.Fatal(runError)
	}
	offer := findOnboardEvent(t, parseOnboardEvents(t, stdout), "playground_offer")
	if offer["next_command"] != "ankra cluster playground create" || !strings.Contains(offer["instruction"].(string), "Ask the user") {
		t.Fatalf("playground_offer = %v", offer)
	}
	if mock.creates != 0 {
		t.Fatal("without --yes an agent run only offers the playground")
	}
}

// A cluster list that fails does not stop onboarding, and a --json caller
// still gets the step.
func TestOnboardClusterCheckFailureIsAnEvent(t *testing.T) {
	mock := &onboardMock{
		organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)},
		clustersError: errors.New("connection reset"),
	}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

	stdout, _, runError := executeOnboard(t, "", "--json", "--no-agent-setup")
	if runError != nil {
		t.Fatal(runError)
	}
	events := parseOnboardEvents(t, stdout)
	clusters := findOnboardEvent(t, events, "clusters")
	if clusters["error"] != "connection reset" || clusters["next_command"] != "ankra cluster list" {
		t.Fatalf("clusters = %v", clusters)
	}
	findOnboardEvent(t, events, "done")
	if mock.creates != 0 {
		t.Fatal("no playground may be created when the clusters could not be checked")
	}
}

func TestOnboardPlaygroundReusesTheExistingOne(t *testing.T) {
	trialEnd := "2026-11-18T10:00:00Z"
	mock := &onboardMock{
		organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)},
		createError:   &client.PlaygroundExistsError{ClusterID: "pg-1", Detail: "This organisation already has a playground."},
		statuses: []client.PlaygroundStatus{
			{Phase: "provisioning"},
			{Phase: "ready", IsTrial: true, TrialExpiresAt: &trialEnd},
		},
	}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

	stdout, stderr, runError := executeOnboard(t, "", "--json", "--yes", "--no-agent-setup")
	if runError != nil {
		t.Fatalf("onboard: %v\nstderr: %s", runError, stderr)
	}
	var statuses []map[string]any
	for _, event := range parseOnboardEvents(t, stdout) {
		if event["event"] == "playground_status" {
			statuses = append(statuses, event)
		}
	}
	if len(statuses) != 2 {
		t.Fatalf("playground_status events = %v", statuses)
	}
	if statuses[0]["phase"] != "provisioning" || statuses[0]["cluster_id"] != "pg-1" {
		t.Fatalf("progress = %v", statuses[0])
	}
	ready := statuses[1]
	if ready["phase"] != "ready" || ready["trial_expires_at"] != trialEnd || !strings.Contains(ready["instruction"].(string), trialEnd) {
		t.Fatalf("ready = %v", ready)
	}
	if mock.creates != 1 {
		t.Fatalf("creates = %d, want 1", mock.creates)
	}
}

func TestOnboardPlaygroundPoolFull(t *testing.T) {
	mock := &onboardMock{
		organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)},
		createError:   client.NewUnexpectedResponseError(http.StatusServiceUnavailable, "The playground host is at capacity."),
	}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})

	stdout, _, runError := executeOnboard(t, "", "--json", "--yes", "--no-agent-setup")
	if runError != nil {
		t.Fatalf("a refused optional offer does not fail onboarding: %v", runError)
	}
	events := parseOnboardEvents(t, stdout)
	status := findOnboardEvent(t, events, "playground_status")
	if status["phase"] != "not_created" || status["message"] != "the playground pool is full right now, try again later" ||
		status["next_command"] != "ankra cluster playground create" {
		t.Fatalf("playground_status = %v", status)
	}
	findOnboardEvent(t, events, "done")

	human, _, _ := executeOnboard(t, "", "--yes", "--no-agent-setup")
	if !strings.Contains(human, "the playground pool is full right now, try again later") {
		t.Fatalf("human output:\n%s", human)
	}
}

func TestOnboardInteractivePlaygroundDeclined(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})
	onboardIsTerminal = func(any) bool { return true }

	stdout, _, runError := executeOnboard(t, "n\n", "--no-agent-setup")
	if runError != nil {
		t.Fatal(runError)
	}
	if mock.creates != 0 || !strings.Contains(stdout, "Create a free playground now? [Y/n]") ||
		!strings.Contains(stdout, "ankra cluster playground create") {
		t.Fatalf("creates = %d, output:\n%s", mock.creates, stdout)
	}
}

func TestOnboardInteractivePlaygroundDefaultsToYes(t *testing.T) {
	mock := &onboardMock{
		organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)},
		createResult:  &client.CreatePlaygroundResult{ClusterID: "pg-2", Success: true},
		statuses:      []client.PlaygroundStatus{{Phase: "ready"}},
	}
	setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})
	onboardIsTerminal = func(any) bool { return true }

	stdout, _, runError := executeOnboard(t, "\n", "--no-agent-setup")
	if runError != nil {
		t.Fatal(runError)
	}
	if mock.creates != 1 || !strings.Contains(stdout, "Your playground is ready (cluster pg-2)") {
		t.Fatalf("creates = %d, output:\n%s", mock.creates, stdout)
	}
}

func TestOnboardAgentSetupOffersTheSkills(t *testing.T) {
	mock := &onboardMock{organisations: []client.OrganisationSummary{onboardOrg("org-1", "Acme", true)}, clusters: 1}
	home := setUpOnboardTest(t, mock)
	writeAnkraConfig(t, map[string]string{"token": "saved-token", "base-url": "https://platform.example.test"})
	if mkdirError := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); mkdirError != nil {
		t.Fatal(mkdirError)
	}
	lookMCPClientCommand = func(string) (string, error) { return "/usr/local/bin/claude", nil }

	stdout, _, runError := executeOnboard(t, "", "--json")
	if runError != nil {
		t.Fatal(runError)
	}
	setup := findOnboardEvent(t, parseOnboardEvents(t, stdout), "agent_setup")
	assistants := setup["detected_clients"].([]any)
	if len(assistants) != 1 || assistants[0].(map[string]any)["id"] != "claude-code" || assistants[0].(map[string]any)["skills_installed"] != false {
		t.Fatalf("detected_clients = %v", assistants)
	}
	if setup["next_command"] != "ankra skills install --client claude-code" {
		t.Fatalf("next_command = %v", setup["next_command"])
	}
	commands := setup["commands"].([]any)
	if len(commands) != 2 || commands[1] != "ankra mcp connect --client claude-code" {
		t.Fatalf("commands = %v", commands)
	}
	if _, statError := os.Stat(filepath.Join(home, ".claude", "skills", "ankra-cli")); statError == nil {
		t.Fatal("without --yes nothing is installed")
	}

	// --yes installs the skills, keeps stdout NDJSON, and only offers MCP.
	stdout, stderr, runError := executeOnboard(t, "", "--json", "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	setup = findOnboardEvent(t, parseOnboardEvents(t, stdout), "agent_setup")
	if setup["skills_installed_now"] != true || setup["next_command"] != "ankra mcp connect --client claude-code" {
		t.Fatalf("agent_setup after --yes = %v", setup)
	}
	if _, statError := os.Stat(filepath.Join(home, ".claude", "skills", "ankra-cli", "SKILL.md")); statError != nil {
		t.Fatalf("skills not installed: %v\nstderr: %s", statError, stderr)
	}

	// Run again: nothing is left to do for the skills.
	stdout, _, runError = executeOnboard(t, "", "--json")
	if runError != nil {
		t.Fatal(runError)
	}
	setup = findOnboardEvent(t, parseOnboardEvents(t, stdout), "agent_setup")
	if setup["detected_clients"].([]any)[0].(map[string]any)["skills_installed"] != true ||
		setup["next_command"] != "ankra mcp connect --client claude-code" {
		t.Fatalf("agent_setup on a rerun = %v", setup)
	}
}

func TestOnboardEventsOmitEmptyValues(t *testing.T) {
	var buffer bytes.Buffer
	(&onboardEvents{writer: &buffer}).emit("login_url", "Open <this>.", "",
		onboardValue("url", "https://a.test/?x=1&y=2"), onboardValue("user_code", ""), onboardValue("reused", false))
	if got, want := buffer.String(), `{"event":"login_url","url":"https://a.test/?x=1&y=2","reused":false,"instruction":"Open <this>."}`+"\n"; got != want {
		t.Fatalf("event line =\n%s\nwant\n%s", got, want)
	}
}
