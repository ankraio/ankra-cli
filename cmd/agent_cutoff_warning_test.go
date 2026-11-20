package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// The warnings and prompts below come from support ticket 1331 (bead
// ankra-p83bfg.3): a cluster record was deleted while its agent was connected
// and the agent was refused for 13.7 days with nobody told, and generating a
// new agent token cuts a running agent off the same way (ankra-uwaqu).

func checkInAgo(age time.Duration) *string {
	value := time.Now().UTC().Add(-age).Format(time.RFC3339)
	return &value
}

func TestRunningAgentPresence(t *testing.T) {
	online, offline := true, false
	for name, testCase := range map[string]struct {
		agent *client.AgentInfo
		want  string
	}{
		"no agent":                                {agent: nil, want: ""},
		"never checked in":                        {agent: &client.AgentInfo{}, want: ""},
		"connected":                               {agent: &client.AgentInfo{IsOnline: &online, CheckedInAt: checkInAgo(20 * time.Second)}, want: "is connected (last check-in "},
		"connected with no check-in time":         {agent: &client.AgentInfo{IsOnline: &online}, want: "is connected"},
		"offline but checked in 5 minutes ago":    {agent: &client.AgentInfo{IsOnline: &offline, CheckedInAt: checkInAgo(5 * time.Minute)}, want: "checked in 5 minutes ago"},
		"offline and checked in 20 minutes ago":   {agent: &client.AgentInfo{IsOnline: &offline, CheckedInAt: checkInAgo(20 * time.Minute)}, want: ""},
		"older platform, fresh check-in":          {agent: &client.AgentInfo{CheckedInAt: checkInAgo(30 * time.Second)}, want: "is connected (last check-in "},
		"older platform, check-in 10 minutes ago": {agent: &client.AgentInfo{CheckedInAt: checkInAgo(10 * time.Minute)}, want: "checked in 10 minutes ago"},
	} {
		got := runningAgentPresence(testCase.agent)
		if (testCase.want == "" && got != "") || (testCase.want != "" && !strings.HasPrefix(got, testCase.want)) {
			t.Errorf("%s: want %q, got %q", name, testCase.want, got)
		}
	}
}

// agentCutOffMock answers the agent read both commands make before they cut
// an agent off, and counts the deletes and token generations they send.
type agentCutOffMock struct {
	agent       *client.AgentInfo
	agentError  error
	token       *client.AgentToken
	deletes     int
	generations int
}

func (m *agentCutOffMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: testClusterID, Name: name, Kind: "imported"}, nil
}

func (m *agentCutOffMock) DeleteCluster(ctx context.Context, name string) error {
	m.deletes++
	return nil
}

func (m *agentCutOffMock) GetClusterAgent(clusterID string) (*client.AgentInfo, error) {
	if m.agentError != nil {
		return nil, m.agentError
	}
	return m.agent, nil
}

func (m *agentCutOffMock) GenerateAgentToken(ctx context.Context, clusterID string) (*client.AgentToken, error) {
	m.generations++
	if m.token != nil {
		return m.token, nil
	}
	return &client.AgentToken{Token: "ank_cai_new", ClusterID: clusterID, Command: "helm upgrade --install ..."}, nil
}

func connectedAgent() *client.AgentInfo {
	online := true
	return &client.AgentInfo{IsOnline: &online, CheckedInAt: checkInAgo(20 * time.Second)}
}

// runDeleteClusterWithInput runs `ankra delete cluster games` with input on
// stdin and returns stdout and stderr apart.
func runDeleteClusterWithInput(t *testing.T, mock *agentCutOffMock, force bool, input string) (string, string, error) {
	t.Helper()
	originalForce := forceDelete
	forceDelete = force
	t.Cleanup(func() { forceDelete = originalForce })
	setMockClient(t, mock)

	reader, writer, pipeError := os.Pipe()
	if pipeError != nil {
		t.Fatal(pipeError)
	}
	_, _ = writer.WriteString(input)
	_ = writer.Close()
	originalStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = originalStdin; _ = reader.Close() })

	stderr := new(bytes.Buffer)
	deleteClusterCmd.SetErr(stderr)
	t.Cleanup(func() { deleteClusterCmd.SetErr(nil) })

	var runError error
	stdout := captureStdout(t, func() {
		runError = deleteClusterCmd.RunE(deleteClusterCmd, []string{"games"})
	})
	return stdout, stderr.String(), runError
}

const deleteCutOffWarning = "Deleting the cluster cuts that agent off: it keeps calling Ankra, and is refused, " +
	"until it is uninstalled from the cluster."

func TestDeleteClusterWarnsBeforeThePromptWhenTheAgentIsConnected(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	_, stderr, runError := runDeleteClusterWithInput(t, mock, false, "n\n")
	if mock.deletes != 0 || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined prompt must exit 4 with nothing deleted, got %v (exit %d, %d deletes)",
			runError, exitCodeFor(runError), mock.deletes)
	}
	warning := strings.Index(stderr, `The Ankra agent in cluster "games" is connected (last check-in `)
	cutOff := strings.Index(stderr, deleteCutOffWarning)
	prompt := strings.Index(stderr, `Are you sure you want to delete cluster "games"?`)
	if warning < 0 || cutOff < 0 || prompt < 0 || warning > cutOff || cutOff > prompt {
		t.Fatalf("want the agent warning, then the cut-off sentence, then the prompt, on stderr; got %q", stderr)
	}
}

func TestDeleteClusterWithAConnectedAgentDeletesOnYes(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	stdout, stderr, runError := runDeleteClusterWithInput(t, mock, false, "y\n")
	if runError != nil || mock.deletes != 1 || !strings.Contains(stdout, `Cluster "games" deleted successfully.`) {
		t.Fatalf("want one delete after yes, got %v (%d deletes), stdout %q", runError, mock.deletes, stdout)
	}
	if strings.Contains(stdout, deleteCutOffWarning) || !strings.Contains(stderr, deleteCutOffWarning) {
		t.Fatalf("the warning belongs on stderr only; stdout %q, stderr %q", stdout, stderr)
	}
}

// --force keeps today's rule, no question asked, but the warning still lands
// in the run's log.
func TestDeleteClusterWithForceStillPrintsTheAgentWarning(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	_, stderr, runError := runDeleteClusterWithInput(t, mock, true, "")
	if runError != nil || mock.deletes != 1 {
		t.Fatalf("--force must delete without asking, got %v (%d deletes)", runError, mock.deletes)
	}
	if !strings.Contains(stderr, deleteCutOffWarning) || strings.Contains(stderr, "Are you sure") {
		t.Fatalf("want the warning and no prompt on stderr, got %q", stderr)
	}
}

// A platform without the agent route, a cluster with no agent record, or an
// agent that has been gone for a while: the delete asks exactly as before.
func TestDeleteClusterSaysNothingAboutAnAgentItCannotSeeRunning(t *testing.T) {
	offline := false
	for name, mock := range map[string]*agentCutOffMock{
		"no agent record":        {agentError: client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found")},
		"the agent is long gone": {agent: &client.AgentInfo{IsOnline: &offline, CheckedInAt: checkInAgo(3 * time.Hour)}},
	} {
		_, stderr, runError := runDeleteClusterWithInput(t, mock, false, "y\n")
		if runError != nil || mock.deletes != 1 {
			t.Fatalf("%s: want the delete after yes, got %v (%d deletes)", name, runError, mock.deletes)
		}
		if strings.Contains(stderr, "Ankra agent") || !strings.Contains(stderr, "Are you sure") {
			t.Fatalf("%s: want only today's prompt, got %q", name, stderr)
		}
	}
}

// runAgentTokenGenerate runs `ankra cluster agent token --generate` with
// input on stdin and returns stdout and stderr apart. A prompt is answered
// only when interactive is set.
func runAgentTokenGenerate(t *testing.T, mock *agentCutOffMock, interactive bool, input string, arguments ...string) (string, string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return interactive }
	t.Cleanup(func() { promptIsInteractive = previous })

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "agent", "token", "--generate"}, arguments...))
	resetTreeFlags(t, clusterAgentTokenCmd)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, clusterAgentTokenCmd)
	})
	var runError error
	printed := captureStdout(t, func() {
		runError = rootCmd.Execute()
	})
	return printed + stdout.String(), stderr.String(), runError
}

const tokenRotationWarning = "A new token replaces the token that agent uses."

func TestAgentTokenGenerateAsksWhenTheAgentIsConnected(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	_, stderr, runError := runAgentTokenGenerate(t, mock, true, "n\n")
	if mock.generations != 0 || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined prompt must exit 4 with nothing generated, got %v (exit %d, %d generations)",
			runError, exitCodeFor(runError), mock.generations)
	}
	for _, want := range []string{
		"The Ankra agent in cluster 'test-cluster' is connected (last check-in ",
		tokenRotationWarning,
		"Generate a new agent token? [y/N]: ",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr must carry %q, got %q", want, stderr)
		}
	}
}

func TestAgentTokenGenerateWithoutATerminalNeedsYes(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	_, _, runError := runAgentTokenGenerate(t, mock, false, "")
	if mock.generations != 0 || exitCodeFor(runError) != exitUsage ||
		!strings.Contains(runError.Error(), "nothing was generated. Re-run with --yes to confirm") {
		t.Fatalf("want exit 2 asking for --yes with nothing generated, got %v (exit %d, %d generations)",
			runError, exitCodeFor(runError), mock.generations)
	}
}

func TestAgentTokenGenerateOnYesSaysThePreviousTokenIsInvalid(t *testing.T) {
	mock := &agentCutOffMock{agent: connectedAgent()}
	stdout, _, runError := runAgentTokenGenerate(t, mock, true, "y\n")
	if runError != nil || mock.generations != 1 {
		t.Fatalf("want one generation after yes, got %v (%d generations)", runError, mock.generations)
	}
	if !strings.Contains(stdout, "The previous token is now invalid; redeploy the agent with the command above.") {
		t.Fatalf("a platform with no grace window must keep today's line, got %q", stdout)
	}
}

func TestAgentTokenGenerateNamesTheGraceWindow(t *testing.T) {
	until := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	mock := &agentCutOffMock{
		agent: connectedAgent(),
		token: &client.AgentToken{Token: "ank_cai_new", ClusterID: testClusterID, PreviousTokenValidUntil: &until},
	}
	stdout, _, runError := runAgentTokenGenerate(t, mock, false, "", "--yes")
	if runError != nil || mock.generations != 1 {
		t.Fatalf("--yes must generate without asking, got %v (%d generations)", runError, mock.generations)
	}
	want := "An agent still running with the previous token keeps working until the agent with this token connects, " +
		"or until " + until + " ("
	if !strings.Contains(stdout, want) || strings.Contains(stdout, "now invalid") {
		t.Fatalf("want the grace window named, got %q", stdout)
	}
}

// -o json stays parseable: the warning is on stderr and the grace window is
// a key of the document.
func TestAgentTokenGenerateJSONStaysClean(t *testing.T) {
	until := "2026-11-21T10:00:00Z"
	mock := &agentCutOffMock{
		agent: connectedAgent(),
		token: &client.AgentToken{Token: "ank_cai_new", ClusterID: testClusterID, PreviousTokenValidUntil: &until},
	}
	stdout, stderr, runError := runAgentTokenGenerate(t, mock, false, "", "--yes", "-o", "json")
	if runError != nil || mock.generations != 1 {
		t.Fatalf("want one generation, got %v (%d generations)", runError, mock.generations)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil {
		t.Fatalf("stdout must be one JSON document, got %q: %v", stdout, decodeError)
	}
	if document["previous_token_valid_until"] != until {
		t.Fatalf("want previous_token_valid_until %q, got %v", until, document["previous_token_valid_until"])
	}
	if !strings.Contains(stderr, tokenRotationWarning) {
		t.Fatalf("the warning belongs on stderr, got %q", stderr)
	}
}

// An agent that is not running, or that cannot be read, keeps today's
// behaviour: no question, even with no terminal.
func TestAgentTokenGenerateDoesNotAskWhenNoAgentIsRunning(t *testing.T) {
	offline := false
	for name, mock := range map[string]*agentCutOffMock{
		"no agent record":         {agentError: client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found")},
		"the agent never checked": {agent: &client.AgentInfo{IsOnline: &offline}},
	} {
		_, stderr, runError := runAgentTokenGenerate(t, mock, false, "")
		if runError != nil || mock.generations != 1 {
			t.Fatalf("%s: want one generation without asking, got %v (%d generations)", name, runError, mock.generations)
		}
		if strings.Contains(stderr, tokenRotationWarning) {
			t.Fatalf("%s: no warning expected, got %q", name, stderr)
		}
	}
}

// A read that fails for any reason but a 404 is not an answer: both commands
// say on stderr that whether an agent would be cut off is not known, then go
// on as before (delete still asks; --generate does not add a question).
func TestAnUnreadableAgentIsSaidToBeUnknownNotAbsent(t *testing.T) {
	const unknown = "Could not read the Ankra agent of cluster "
	readFailure := func() *agentCutOffMock {
		return &agentCutOffMock{agentError: client.NewUnexpectedResponseError(502, "unexpected status: 502 Bad Gateway")}
	}

	deleteMock := readFailure()
	_, deleteStderr, deleteError := runDeleteClusterWithInput(t, deleteMock, false, "y\n")
	if deleteError != nil || deleteMock.deletes != 1 {
		t.Fatalf("delete: want the delete after yes, got %v (%d deletes)", deleteError, deleteMock.deletes)
	}
	if !strings.Contains(deleteStderr, unknown+"'games', so whether a running agent would be cut off is not known") ||
		!strings.Contains(deleteStderr, "Are you sure") {
		t.Fatalf("delete: want the unknown note and the prompt, got %q", deleteStderr)
	}

	generateMock := readFailure()
	stdout, generateStderr, generateError := runAgentTokenGenerate(t, generateMock, false, "")
	if generateError != nil || generateMock.generations != 1 {
		t.Fatalf("generate: want one generation, got %v (%d generations)", generateError, generateMock.generations)
	}
	if !strings.Contains(generateStderr, unknown+"'test-cluster'") || strings.Contains(stdout, unknown) {
		t.Fatalf("generate: want the unknown note on stderr only; stdout %q, stderr %q", stdout, generateStderr)
	}
}
