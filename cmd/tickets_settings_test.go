package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const ticketSettingsCoreProjectID = "5b3c1a2e-0000-4000-8000-0000000000c1"

type ticketProjectChannelsMock struct {
	baseMock
	list         []client.TicketProjectChannels
	setProjectID string
	setRequest   *client.SetTicketProjectChannelsRequest
	removeCalls  []string
}

func (m *ticketProjectChannelsMock) ListProjects() ([]client.ProjectSummary, error) {
	return []client.ProjectSummary{{ID: ticketSettingsCoreProjectID, Name: "Core", Slug: "core"}}, nil
}

func (m *ticketProjectChannelsMock) ListTicketProjectChannels() (*client.TicketProjectChannelsList, error) {
	return &client.TicketProjectChannelsList{Projects: m.list}, nil
}

func (m *ticketProjectChannelsMock) SetTicketProjectChannels(projectID string, request client.SetTicketProjectChannelsRequest) (*client.SetTicketProjectChannelsResult, error) {
	m.setProjectID = projectID
	m.setRequest = &request
	if request.SlackChannelID == nil && request.TeamsConversationID == nil {
		return &client.SetTicketProjectChannelsResult{ProjectID: projectID}, nil
	}
	return &client.SetTicketProjectChannelsResult{
		ProjectID: projectID,
		Channels: &client.TicketProjectChannelsBinding{
			SlackChannelID:      request.SlackChannelID,
			TeamsConversationID: request.TeamsConversationID,
		},
	}, nil
}

func (m *ticketProjectChannelsMock) RemoveTicketProjectChannels(projectID string) error {
	m.removeCalls = append(m.removeCalls, projectID)
	return nil
}

func ticketsSettingsCommandTree() []*cobra.Command {
	return []*cobra.Command{
		ticketsSettingsProjectsListCmd,
		ticketsSettingsProjectsSetCmd,
		ticketsSettingsProjectsRemoveCmd,
	}
}

func runTicketsSettingsCommand(t *testing.T, mock APIClient, input string, args ...string) (string, error) {
	t.Helper()
	resetTreeFlags(t, ticketsSettingsCommandTree()...)
	return runConfirmCommand(t, mock, input, ticketsSettingsCommandTree(), args...)
}

func TestTicketsSettingsProjectsListRendersTableAndJSON(t *testing.T) {
	mock := &ticketProjectChannelsMock{}
	out, runError := runTicketsSettingsCommand(t, mock, "", "tickets", "settings", "projects", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if !strings.Contains(out, "No project has AI board channels of its own") {
		t.Errorf("expected the empty message, got: %s", out)
	}

	mock.list = []client.TicketProjectChannels{{
		ProjectID: ticketSettingsCoreProjectID, ProjectName: "Core", SlackChannelID: strPtrCmd("C123"),
		UpdatedAt: "2026-11-03T10:00:00Z",
	}}
	out, runError = runTicketsSettingsCommand(t, mock, "", "tickets", "settings", "projects", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	for _, fragment := range []string{"Core", ticketSettingsCoreProjectID, "C123", "organisation channel"} {
		if !strings.Contains(out, fragment) {
			t.Errorf("expected %q in the table, got:\n%s", fragment, out)
		}
	}

	out, runError = runTicketsSettingsCommand(t, mock, "", "tickets", "settings", "projects", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("list -o json failed: %v", runError)
	}
	var decoded client.TicketProjectChannelsList
	if unmarshalError := json.Unmarshal([]byte(out), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON, got %v: %s", unmarshalError, out)
	}
	if len(decoded.Projects) != 1 || decoded.Projects[0].ProjectID != ticketSettingsCoreProjectID {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestTicketsSettingsProjectsSetResolvesTheProjectAndSendsOnlyPassedProviders(t *testing.T) {
	mock := &ticketProjectChannelsMock{}
	out, runError := runTicketsSettingsCommand(t, mock, "",
		"tickets", "settings", "projects", "set", "core", "--slack-channel", "C123")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	if mock.setProjectID != ticketSettingsCoreProjectID {
		t.Errorf("project id = %q, want the resolved %s", mock.setProjectID, ticketSettingsCoreProjectID)
	}
	request := mock.setRequest
	if request == nil || !request.SlackChannelIDSet || request.SlackChannelID == nil || *request.SlackChannelID != "C123" {
		t.Fatalf("slack channel not sent: %+v", request)
	}
	if request.TeamsConversationIDSet {
		t.Errorf("an unpassed provider must be left out so the backend keeps it: %+v", request)
	}
	if !strings.Contains(out, "Slack channel:      C123") || !strings.Contains(out, "Teams conversation: organisation channel") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestTicketsSettingsProjectsSetClearsAProvider(t *testing.T) {
	mock := &ticketProjectChannelsMock{}
	out, runError := runTicketsSettingsCommand(t, mock, "",
		"tickets", "settings", "projects", "set", ticketSettingsCoreProjectID, "--clear-teams")
	if runError != nil {
		t.Fatalf("set --clear-teams failed: %v", runError)
	}
	request := mock.setRequest
	if request == nil || !request.TeamsConversationIDSet || request.TeamsConversationID != nil || request.SlackChannelIDSet {
		t.Fatalf("expected only teams_conversation_id cleared, got %+v", request)
	}
	if !strings.Contains(out, "no AI board channels of its own any more") {
		t.Errorf("expected the fallback message, got:\n%s", out)
	}
}

func TestTicketsSettingsProjectsSetRefusesBadFlagCombinations(t *testing.T) {
	cases := [][]string{
		{"tickets", "settings", "projects", "set", "core"},
		{"tickets", "settings", "projects", "set", "core", "--slack-channel", "  "},
		{"tickets", "settings", "projects", "set", "core", "--slack-channel", "C1", "--clear-slack"},
		{"tickets", "settings", "projects", "set", "core", "--teams-conversation", "19:a@thread.tacv2", "--clear-teams"},
	}
	for _, args := range cases {
		mock := &ticketProjectChannelsMock{}
		_, runError := runTicketsSettingsCommand(t, mock, "", args...)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: expected a usage error, got %v", args, runError)
		}
		if mock.setRequest != nil {
			t.Errorf("%v: a usage error must not reach the client", args)
		}
	}
}

func TestTicketsSettingsProjectsSetUnknownProjectIsNotFound(t *testing.T) {
	mock := &ticketProjectChannelsMock{}
	_, runError := runTicketsSettingsCommand(t, mock, "",
		"tickets", "settings", "projects", "set", "nope", "--slack-channel", "C1")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("expected exit code %d, got %v", exitNotFound, runError)
	}
}

func TestTicketsSettingsProjectsRemoveConfirms(t *testing.T) {
	mock := &ticketProjectChannelsMock{}
	_, runError := runTicketsSettingsCommand(t, mock, "n\n", "tickets", "settings", "projects", "remove", "core")
	if runError == nil || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined prompt must exit %d, got %v", exitCancelled, runError)
	}
	if len(mock.removeCalls) != 0 {
		t.Fatalf("a declined prompt must not remove anything: %v", mock.removeCalls)
	}

	out, runError := runTicketsSettingsCommand(t, mock, "", "tickets", "settings", "projects", "remove", "core", "--yes")
	if runError != nil {
		t.Fatalf("remove --yes failed: %v", runError)
	}
	if len(mock.removeCalls) != 1 || mock.removeCalls[0] != ticketSettingsCoreProjectID {
		t.Errorf("remove calls = %v", mock.removeCalls)
	}
	if !strings.Contains(out, "use the organisation's channels") {
		t.Errorf("unexpected output: %s", out)
	}
}
