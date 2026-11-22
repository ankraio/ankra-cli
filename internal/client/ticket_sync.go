package client

// Ticket assignment, plans and workspaces, and the outbound ticket sync
// connections that mirror tickets to Jira, Linear or a webhook
// (ankra-hq41xb.23), under /api/v1/org/ai-tickets.
//
// A sync connection's credential is stored by the platform and never
// returned. A write carries it in the request body alone; this package
// never prints it or puts it into an error.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
)

const (
	ticketSyncConnections  = ticketsBasePath + "/sync/connections"
	ticketSyncDestinations = ticketsBasePath + "/sync/destinations"
)

// TicketAssignRequest names the agent task or the member a ticket goes to,
// never both. Both nil unassigns the ticket.
type TicketAssignRequest struct {
	AssigneeAgentTaskID *string `json:"assignee_agent_task_id"`
	AssigneeUserID      *string `json:"assignee_user_id"`
}

// AssignTicket assigns a ticket to an agent or a member, or unassigns it.
// POST /api/v1/org/ai-tickets/{ticket_id}/assign
func (c *Client) AssignTicket(ctx context.Context, ticketID string, request TicketAssignRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketsBasePath+"/"+neturl.PathEscape(ticketID)+"/assign", request)
}

// DraftTicketPlan records a plan written by the caller on the ticket.
// POST /api/v1/org/ai-tickets/{ticket_id}/plan
func (c *Client) DraftTicketPlan(ctx context.Context, ticketID string, planDocument string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketsBasePath+"/"+neturl.PathEscape(ticketID)+"/plan",
		map[string]string{"plan_document": planDocument})
}

// TicketPlanReviewRequest is a review of a ticket's plan. Verdict is approve
// or request_changes; ExpectedUpdatedAt, when set, makes the platform refuse
// the review (409) if the ticket changed since it was read.
type TicketPlanReviewRequest struct {
	Verdict           string `json:"verdict"`
	Rationale         string `json:"rationale,omitempty"`
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty"`
}

// ReviewTicketPlan submits a review of the ticket's plan.
// POST /api/v1/org/ai-tickets/{ticket_id}/plan/review
func (c *Client) ReviewTicketPlan(ctx context.Context, ticketID string, request TicketPlanReviewRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketsBasePath+"/"+neturl.PathEscape(ticketID)+"/plan/review", request)
}

// SetTicketWorkspace attaches an AI workspace to the ticket, or detaches
// the current one when workspaceID is nil.
// POST /api/v1/org/ai-tickets/{ticket_id}/workspace
func (c *Client) SetTicketWorkspace(ctx context.Context, ticketID string, workspaceID *string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketsBasePath+"/"+neturl.PathEscape(ticketID)+"/workspace",
		map[string]*string{"workspace_id": workspaceID})
}

// TicketSyncConnectionRequest creates or updates a sync connection. On an
// update a member left out keeps its stored value. Config holds the
// provider's non-secret settings (Jira: base_url, project_key, issue_type;
// Linear: team_id; webhook: url). Credential is write-only (Jira: email and
// api_token; Linear: api_key; webhook: secret).
type TicketSyncConnectionRequest struct {
	Name            string            `json:"name,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	Config          map[string]string `json:"config,omitempty"`
	Credential      map[string]string `json:"credential,omitempty"`
	Enabled         *bool             `json:"enabled,omitempty"`
	SyncKinds       *[]string         `json:"sync_kinds,omitempty"`
	SyncMinPriority string            `json:"sync_min_priority,omitempty"`
	SyncEventScope  string            `json:"sync_event_scope,omitempty"`
}

// ListTicketSyncConnections reads the organisation's sync connections.
// Stored credentials are never returned.
// GET /api/v1/org/ai-tickets/sync/connections
func (c *Client) ListTicketSyncConnections(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, ticketSyncConnections, nil)
}

// GetTicketSyncConnection reads one sync connection, without its credential.
// GET /api/v1/org/ai-tickets/sync/connections/{connection_id}
func (c *Client) GetTicketSyncConnection(ctx context.Context, connectionID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, ticketSyncConnections+"/"+neturl.PathEscape(connectionID), nil)
}

// CreateTicketSyncConnection creates a sync connection.
// POST /api/v1/org/ai-tickets/sync/connections
func (c *Client) CreateTicketSyncConnection(ctx context.Context, request TicketSyncConnectionRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketSyncConnections, request)
}

// UpdateTicketSyncConnection updates a sync connection; a new credential
// rotates the stored one.
// PUT /api/v1/org/ai-tickets/sync/connections/{connection_id}
func (c *Client) UpdateTicketSyncConnection(ctx context.Context, connectionID string, request TicketSyncConnectionRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, ticketSyncConnections+"/"+neturl.PathEscape(connectionID), request)
}

// DeleteTicketSyncConnection deletes a sync connection.
// DELETE /api/v1/org/ai-tickets/sync/connections/{connection_id}
func (c *Client) DeleteTicketSyncConnection(ctx context.Context, connectionID string) error {
	return c.sendJSONContext(ctx, http.MethodDelete, c.BaseURL+ticketSyncConnections+"/"+neturl.PathEscape(connectionID), nil, nil)
}

// TicketSyncDestinationsRequest asks what a tracker credential can reach:
// either a new Credential (write-only) or the ConnectionID of a saved
// connection whose stored credential is used.
type TicketSyncDestinationsRequest struct {
	Provider     string            `json:"provider"`
	Credential   map[string]string `json:"credential,omitempty"`
	ConnectionID string            `json:"connection_id,omitempty"`
}

// ListTicketSyncDestinations lists the destinations a tracker credential
// can file into. Nothing is written.
// POST /api/v1/org/ai-tickets/sync/destinations
func (c *Client) ListTicketSyncDestinations(ctx context.Context, request TicketSyncDestinationsRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, ticketSyncDestinations, request)
}
