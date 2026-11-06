package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type slackMock struct {
	baseMock
	statuses    []*client.SlackStatus
	statusCalls int
	statusError error
	installURL  string
	installErr  error
	installs    int
}

func (mock *slackMock) GetSlackStatus(context.Context) (*client.SlackStatus, error) {
	if mock.statusError != nil && mock.statusCalls > 0 {
		mock.statusCalls++
		return nil, mock.statusError
	}
	index := min(mock.statusCalls, len(mock.statuses)-1)
	mock.statusCalls++
	return mock.statuses[index], nil
}

func (mock *slackMock) GetSlackInstallURL(context.Context) (*client.SlackInstallURL, error) {
	mock.installs++
	if mock.installErr != nil {
		return nil, mock.installErr
	}
	return &client.SlackInstallURL{InstallURL: mock.installURL}, nil
}

const slackTestURL = "https://slack.com/oauth/v2/authorize?client_id=x&state=y"

func slackReset(t *testing.T) {
	t.Helper()
	previous := slackPollInterval
	slackPollInterval = time.Millisecond
	t.Cleanup(func() {
		slackPollInterval = previous
		_ = slackConnectCmd.Flags().Set("no-wait", "false")
		_ = slackConnectCmd.Flags().Set("timeout", slackDefaultConnectTimeout.String())
		_ = slackConnectCmd.Flags().Set("output", "")
		_ = slackStatusCmd.Flags().Set("output", "")
	})
}

func slackWorkspaceAt(teamID string, name string, installedAt time.Time) client.SlackWorkspace {
	return client.SlackWorkspace{TeamID: teamID, TeamName: &name, InstalledAt: installedAt}
}

func TestSlackConnectPrintsTheURLAndWaitsForTheInstall(t *testing.T) {
	slackReset(t)
	now := time.Now()
	mock := &slackMock{installURL: slackTestURL, statuses: []*client.SlackStatus{
		{Configured: true},
		{Configured: true},
		{Configured: true, Workspaces: []client.SlackWorkspace{slackWorkspaceAt("T1", "Acme", now)}},
	}}
	setMockClient(t, mock)
	output, executeError := executeCommand("slack", "connect")
	if executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	for _, want := range []string{slackTestURL, "Connected Acme"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q: %s", want, output)
		}
	}
	if mock.installs != 1 {
		t.Errorf("installs = %d", mock.installs)
	}
}

func TestSlackConnectDoesNotMistakeAnExistingWorkspaceForTheInstall(t *testing.T) {
	slackReset(t)
	then := time.Now().Add(-time.Hour)
	existing := slackWorkspaceAt("T0", "Old", then)
	reinstalled := slackWorkspaceAt("T0", "Old", then.Add(time.Minute))
	mock := &slackMock{installURL: slackTestURL, statuses: []*client.SlackStatus{
		{Workspaces: []client.SlackWorkspace{existing}},
		{Workspaces: []client.SlackWorkspace{existing}},
		{Workspaces: []client.SlackWorkspace{reinstalled}},
	}}
	setMockClient(t, mock)
	if _, executeError := executeCommand("slack", "connect"); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	if mock.statusCalls != 3 {
		t.Errorf("status reads = %d, want it to wait past the unchanged workspace", mock.statusCalls)
	}
}

func TestSlackConnectTimesOutWithTheWaitCode(t *testing.T) {
	slackReset(t)
	mock := &slackMock{installURL: slackTestURL, statuses: []*client.SlackStatus{{Configured: true}}}
	setMockClient(t, mock)
	output, executeError := executeCommand("slack", "connect", "--timeout", "10ms")
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Fatalf("exit = %d (%v), want %d", exitCodeFor(executeError), executeError, exitWaitTimeout)
	}
	if !strings.Contains(output, slackTestURL) {
		t.Errorf("the URL must have been printed: %s", output)
	}
}

func TestSlackConnectNoWaitReturnsAtOnce(t *testing.T) {
	slackReset(t)
	mock := &slackMock{installURL: slackTestURL, statuses: []*client.SlackStatus{{Configured: true}}}
	setMockClient(t, mock)
	output, executeError := executeCommand("slack", "connect", "--no-wait")
	if executeError != nil || !strings.Contains(output, slackTestURL) || mock.statusCalls != 1 {
		t.Errorf("error = %v, status reads = %d, output = %s", executeError, mock.statusCalls, output)
	}
}

func TestSlackConnectRefusalNamesItself(t *testing.T) {
	slackReset(t)
	mock := &slackMock{statuses: []*client.SlackStatus{{Configured: true}},
		installErr: client.NewUnexpectedResponseError(403, "Only organisation admins can install Slack integrations.")}
	setMockClient(t, mock)
	_, executeError := executeCommand("slack", "connect")
	if exitCodeFor(executeError) != exitForbidden {
		t.Errorf("exit = %d (%v), want %d", exitCodeFor(executeError), executeError, exitForbidden)
	}
}

func TestSlackConnectOnAnOlderPlatformSaysSo(t *testing.T) {
	slackReset(t)
	mock := &slackMock{statuses: []*client.SlackStatus{{Configured: true}},
		installErr: client.NewUnexpectedResponseError(404, "Not Found")}
	setMockClient(t, mock)
	_, executeError := executeCommand("slack", "connect")
	if executeError == nil || !strings.Contains(executeError.Error(), "from the portal") {
		t.Errorf("got %v", executeError)
	}
}

func TestSlackConnectWaitSurvivesAPassingFailureButNotARefusal(t *testing.T) {
	if !slackPollErrorIsRetryable(errors.New("connection reset")) {
		t.Error("a network failure must not end the wait")
	}
	if !slackPollErrorIsRetryable(client.NewUnexpectedResponseError(502, "bad gateway")) {
		t.Error("a 5xx must not end the wait")
	}
	if slackPollErrorIsRetryable(client.NewUnexpectedResponseError(403, "no")) {
		t.Error("a refusal must end the wait")
	}
}

func TestSlackConnectStructuredIsOneDocument(t *testing.T) {
	slackReset(t)
	mock := &slackMock{installURL: slackTestURL, statuses: []*client.SlackStatus{{Configured: true}}}
	setMockClient(t, mock)
	stdout := &strings.Builder{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"slack", "connect", "--no-wait", "-o", "json"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("connect failed: %v", executeError)
	}
	var document slackConnectResult
	if decodeError := json.Unmarshal([]byte(stdout.String()), &document); decodeError != nil {
		t.Fatalf("not one document: %v: %s", decodeError, stdout)
	}
	if document.InstallURL != slackTestURL || document.Connected {
		t.Errorf("document = %+v", document)
	}
}

func TestSlackStatusSaysWhenNothingIsConnected(t *testing.T) {
	slackReset(t)
	setMockClient(t, &slackMock{statuses: []*client.SlackStatus{{Configured: true}}})
	output, executeError := executeCommand("slack", "status")
	if executeError != nil || !strings.Contains(output, "ankra slack connect") {
		t.Errorf("error = %v, output = %s", executeError, output)
	}
}
