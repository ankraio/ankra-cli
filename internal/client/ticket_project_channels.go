package client

import (
	"encoding/json"
	"net/http"
	neturl "net/url"
)

const ticketProjectChannelsBasePath = "/api/v1/org/ai-tickets/settings/projects"

// TicketProjectChannels is one project that binds AI board channels of its
// own. A ticket of that project opens its board thread in these channels,
// per provider; a provider left nil falls back to the organisation's
// channel.
type TicketProjectChannels struct {
	ProjectID           string  `json:"project_id" yaml:"project_id"`
	ProjectName         string  `json:"project_name" yaml:"project_name"`
	SlackChannelID      *string `json:"slack_channel_id" yaml:"slack_channel_id"`
	TeamsConversationID *string `json:"teams_conversation_id" yaml:"teams_conversation_id"`
	UpdatedAt           string  `json:"updated_at" yaml:"updated_at"`
}

// TicketProjectChannelsList is the GET .../ai-tickets/settings/projects body.
// It lists only projects that bind channels of their own.
type TicketProjectChannelsList struct {
	Projects []TicketProjectChannels `json:"projects" yaml:"projects"`
}

// SetTicketProjectChannelsRequest is the PUT body. Each provider is
// tri-state: not Set keeps the project's current channel, Set with a nil
// value clears it (that provider falls back to the organisation's channel),
// and Set with a value binds that channel. The backend needs at least one
// member.
type SetTicketProjectChannelsRequest struct {
	SlackChannelID         *string
	SlackChannelIDSet      bool
	TeamsConversationID    *string
	TeamsConversationIDSet bool
}

// MarshalJSON writes only the members that were set, a cleared one as null.
func (request SetTicketProjectChannelsRequest) MarshalJSON() ([]byte, error) {
	body := map[string]any{}
	if request.SlackChannelIDSet {
		body["slack_channel_id"] = request.SlackChannelID
	}
	if request.TeamsConversationIDSet {
		body["teams_conversation_id"] = request.TeamsConversationID
	}
	return json.Marshal(body)
}

// TicketProjectChannelsBinding is the channels half of the PUT answer.
type TicketProjectChannelsBinding struct {
	SlackChannelID      *string `json:"slack_channel_id" yaml:"slack_channel_id"`
	TeamsConversationID *string `json:"teams_conversation_id" yaml:"teams_conversation_id"`
	UpdatedAt           string  `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// SetTicketProjectChannelsResult is the PUT answer. Channels is nil when the
// project binds no channel of its own any more.
type SetTicketProjectChannelsResult struct {
	ProjectID string                        `json:"project_id" yaml:"project_id"`
	Channels  *TicketProjectChannelsBinding `json:"channels" yaml:"channels"`
}

func (c *Client) ticketProjectChannelsURL(projectID string) string {
	return c.BaseURL + ticketProjectChannelsBasePath + "/" + neturl.PathEscape(projectID)
}

// ListTicketProjectChannels lists the projects that bind AI board channels
// of their own.
func (c *Client) ListTicketProjectChannels() (*TicketProjectChannelsList, error) {
	var list TicketProjectChannelsList
	if listError := c.sendJSON(http.MethodGet, c.BaseURL+ticketProjectChannelsBasePath, nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// SetTicketProjectChannels binds or clears a project's AI board channels.
// Organisation admins only (403 otherwise); 404 for a project outside the
// organisation; 422 for a body with no member.
func (c *Client) SetTicketProjectChannels(projectID string, request SetTicketProjectChannelsRequest) (*SetTicketProjectChannelsResult, error) {
	var result SetTicketProjectChannelsResult
	if setError := c.sendJSON(http.MethodPut, c.ticketProjectChannelsURL(projectID), request, &result); setError != nil {
		return nil, setError
	}
	return &result, nil
}

// RemoveTicketProjectChannels drops every channel the project binds, so its
// tickets use the organisation's channels again. 204 on success, 404 when
// the project binds none.
func (c *Client) RemoveTicketProjectChannels(projectID string) error {
	return c.sendJSON(http.MethodDelete, c.ticketProjectChannelsURL(projectID), nil, nil)
}
