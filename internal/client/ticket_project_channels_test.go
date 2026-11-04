package client

import (
	"errors"
	"net/http"
	"testing"
)

func TestListTicketProjectChannels_DecodesProjects(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"projects": []map[string]interface{}{{
			"project_id": "project-core", "project_name": "Core", "slack_channel_id": "C123",
			"teams_conversation_id": nil, "updated_at": "2026-11-03T10:00:00Z",
		}},
	})
	testClient := newTestClient(t, handler)

	list, listError := testClient.ListTicketProjectChannels()
	if listError != nil {
		t.Fatalf("ListTicketProjectChannels: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/ai-tickets/settings/projects")
	if len(list.Projects) != 1 {
		t.Fatalf("projects = %+v", list.Projects)
	}
	project := list.Projects[0]
	if project.ProjectID != "project-core" || project.ProjectName != "Core" ||
		project.SlackChannelID == nil || *project.SlackChannelID != "C123" || project.TeamsConversationID != nil {
		t.Errorf("project = %+v", project)
	}
}

func TestSetTicketProjectChannels_SendsOnlySetMembersAndNullForCleared(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"project_id": "project-core",
		"channels":   map[string]interface{}{"slack_channel_id": "C123", "teams_conversation_id": nil},
	})
	testClient := newTestClient(t, handler)

	result, setError := testClient.SetTicketProjectChannels("project-core", SetTicketProjectChannelsRequest{
		SlackChannelID: strPtr("C123"), SlackChannelIDSet: true,
	})
	if setError != nil {
		t.Fatalf("SetTicketProjectChannels: %v", setError)
	}
	recorder.assert(t, http.MethodPut, "/api/v1/org/ai-tickets/settings/projects/project-core")
	body := recorder.decodedBody(t)
	if body["slack_channel_id"] != "C123" {
		t.Errorf("body = %v", body)
	}
	if _, present := body["teams_conversation_id"]; present {
		t.Errorf("an unset provider must be absent so the backend keeps it, body = %v", body)
	}
	if result.Channels == nil || result.Channels.SlackChannelID == nil || *result.Channels.SlackChannelID != "C123" {
		t.Errorf("result = %+v", result)
	}

	recorder, handler = recordAlertsRequest(t, http.StatusOK, map[string]interface{}{"project_id": "project-core", "channels": nil})
	testClient = newTestClient(t, handler)
	result, setError = testClient.SetTicketProjectChannels("project-core", SetTicketProjectChannelsRequest{TeamsConversationIDSet: true})
	if setError != nil {
		t.Fatalf("SetTicketProjectChannels clear: %v", setError)
	}
	body = recorder.decodedBody(t)
	value, present := body["teams_conversation_id"]
	if !present || value != nil {
		t.Errorf("a cleared provider must be sent as null, body = %v", body)
	}
	if _, present := body["slack_channel_id"]; present {
		t.Errorf("slack_channel_id must be absent, body = %v", body)
	}
	if result.Channels != nil {
		t.Errorf("a null channels member means the project binds none, got %+v", result.Channels)
	}
}

func TestSetTicketProjectChannels_ForbiddenKeepsStatus(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusForbidden, map[string]string{"detail": "Organisation admins only"})
	testClient := newTestClient(t, handler)

	_, setError := testClient.SetTicketProjectChannels("project-core", SetTicketProjectChannelsRequest{
		SlackChannelID: strPtr("C123"), SlackChannelIDSet: true,
	})
	if setError == nil {
		t.Fatal("expected a 403 to surface as an error")
	}
}

func TestRemoveTicketProjectChannels_Accepts204AndSurfaces404(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusNoContent, nil)
	testClient := newTestClient(t, handler)
	if removeError := testClient.RemoveTicketProjectChannels("project-core"); removeError != nil {
		t.Fatalf("RemoveTicketProjectChannels: %v", removeError)
	}
	recorder.assert(t, http.MethodDelete, "/api/v1/org/ai-tickets/settings/projects/project-core")

	_, handler = recordAlertsRequest(t, http.StatusNotFound, map[string]string{"detail": "Project binds no channels"})
	testClient = newTestClient(t, handler)
	removeError := testClient.RemoveTicketProjectChannels("project-tools")
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 UnexpectedResponseError, got %v", removeError)
	}
}
