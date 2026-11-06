package client

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// SlackInstallURL is GET /api/v1/org/slack/install-url: the address a person
// opens to add Ankra to a Slack workspace.
type SlackInstallURL struct {
	InstallURL string `json:"install_url"`
}

// SlackWorkspace is one Slack workspace connected to the organisation.
type SlackWorkspace struct {
	TeamID      string    `json:"team_id" yaml:"team_id"`
	TeamName    *string   `json:"team_name" yaml:"team_name"`
	InstalledAt time.Time `json:"installed_at" yaml:"installed_at"`
}

// SlackStatus is GET /api/v1/org/slack/status.
type SlackStatus struct {
	Configured bool             `json:"configured" yaml:"configured"`
	Workspaces []SlackWorkspace `json:"workspaces" yaml:"workspaces"`
}

// GetSlackInstallURL starts a Slack install and answers where to finish it.
func (client *Client) GetSlackInstallURL(requestContext context.Context) (*SlackInstallURL, error) {
	payload, requestError := client.applicationResourceRequest(requestContext, http.MethodGet, "/api/v1/org/slack/install-url", nil, nil)
	if requestError != nil {
		return nil, requestError
	}
	var result SlackInstallURL
	if decodeError := json.Unmarshal(payload, &result); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// GetSlackStatus reads the Slack workspaces connected to the organisation.
func (client *Client) GetSlackStatus(requestContext context.Context) (*SlackStatus, error) {
	payload, requestError := client.applicationResourceRequest(requestContext, http.MethodGet, "/api/v1/org/slack/status", nil, nil)
	if requestError != nil {
		return nil, requestError
	}
	var result SlackStatus
	if decodeError := json.Unmarshal(payload, &result); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}
