package client

import (
	"fmt"
	"net/http"
)

// BoardIdentity is the organisation's board agent identity: the service
// principal the AI board's platform-created agents act as. Provisioned is
// false for an organisation that never opted in - which is also the state
// in which every designated board worker escalates instead of working.
type BoardIdentity struct {
	Provisioned bool    `json:"provisioned"`
	Subject     string  `json:"subject,omitempty"`
	RoleSlug    string  `json:"role_slug,omitempty"`
	AnkraUserID *string `json:"ankra_user_id,omitempty"`
}

// AIPauseState is the organisation AI kill switch. PausedBy and Reason are
// present only while it is engaged.
type AIPauseState struct {
	Paused   bool    `json:"paused"`
	PausedAt *string `json:"paused_at"`
	PausedBy *string `json:"paused_by"`
	Reason   *string `json:"reason"`
}

// AIPauseOutcome is what engaging the switch reports: the state plus what
// the sweep cancelled on the way in.
type AIPauseOutcome struct {
	AIPauseState
	CancelledSessions int `json:"cancelled_sessions"`
	CancelledRuns     int `json:"cancelled_agent_runs"`
}

const (
	boardIdentityPath = "/api/v1/org/ai-tasks/board-identity"
	aiPausePath       = "/api/v1/org/ai-settings/pause"
)

// GetBoardIdentity reads the organisation's board agent identity.
func (c *Client) GetBoardIdentity() (*BoardIdentity, error) {
	var identity BoardIdentity
	if err := c.getJSON(c.BaseURL+boardIdentityPath, &identity); err != nil {
		return nil, fmt.Errorf("reading the board identity: %w", err)
	}
	return &identity, nil
}

// ProvisionBoardIdentity mints the board identity, or returns the existing
// one. An empty roleSlug takes the platform default (operator).
func (c *Client) ProvisionBoardIdentity(roleSlug string) (*BoardIdentity, error) {
	payload := map[string]any{}
	if roleSlug != "" {
		payload["role_slug"] = roleSlug
	}
	var identity BoardIdentity
	if err := c.sendJSON(http.MethodPost, c.BaseURL+boardIdentityPath, payload, &identity); err != nil {
		return nil, fmt.Errorf("provisioning the board identity: %w", err)
	}
	return &identity, nil
}

// RevokeBoardIdentity stands the board identity down: the principal keeps
// its history, and loses every grant above the viewer floor.
func (c *Client) RevokeBoardIdentity() (*BoardIdentity, error) {
	var identity BoardIdentity
	if err := c.sendJSON(http.MethodDelete, c.BaseURL+boardIdentityPath, nil, &identity); err != nil {
		return nil, fmt.Errorf("standing the board identity down: %w", err)
	}
	return &identity, nil
}

// GetAIPauseState reads the organisation AI kill switch.
func (c *Client) GetAIPauseState() (*AIPauseState, error) {
	var state AIPauseState
	if err := c.getJSON(c.BaseURL+aiPausePath, &state); err != nil {
		return nil, fmt.Errorf("reading the AI pause state: %w", err)
	}
	return &state, nil
}

// SetAIPause engages or releases the kill switch. Engaging cancels the
// organisation's running AI sessions and agent runs.
func (c *Client) SetAIPause(paused bool, reason string) (*AIPauseOutcome, error) {
	payload := map[string]any{"paused": paused}
	if reason != "" {
		payload["reason"] = reason
	}
	var outcome AIPauseOutcome
	if err := c.sendJSON(http.MethodPut, c.BaseURL+aiPausePath, payload, &outcome); err != nil {
		return nil, fmt.Errorf("setting the AI pause state: %w", err)
	}
	return &outcome, nil
}
