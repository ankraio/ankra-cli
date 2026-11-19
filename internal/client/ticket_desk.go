package client

// The AI ticket desk settings, the board summary, the changes made on a
// ticket's behalf and the metadata patch of a ticket (ankra-hq41xb.35),
// under /api/v1/org/ai-tickets. Every answer is returned as the platform
// sent it.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"strings"
)

const (
	ticketDeskSettingsPath = ticketsBasePath + "/settings"
	ticketBoardSummaryPath = ticketsBasePath + "/summary"
)

// GetTicketDeskSettings reads the organisation's ticket desk settings: the
// channels tickets are announced in, the reviewing agent, which insight
// severities are filed on their own, and the channel notification policy.
// An organisation that never saved settings reads the defaults.
// GET /api/v1/org/ai-tickets/settings
func (c *Client) GetTicketDeskSettings(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, ticketDeskSettingsPath, nil)
}

// UpdateTicketDeskSettings sends the desk settings that change. A member
// left out keeps its stored value; a nil value clears slack_channel_id,
// teams_conversation_id or reviewer_agent_task_id. Organisation admins only.
// PUT /api/v1/org/ai-tickets/settings
func (c *Client) UpdateTicketDeskSettings(ctx context.Context, changes map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, ticketDeskSettingsPath, changes)
}

// TicketSummaryFilter narrows the board summary the way a ticket listing is
// narrowed, so the counts describe the same tickets. Every list matches any
// of its values.
type TicketSummaryFilter struct {
	Statuses        []string
	Kinds           []string
	Priorities      []string
	ClusterIDs      []string
	Labels          []string
	Search          string
	NeedsHuman      bool
	IncludeReceipts bool
}

func (filter TicketSummaryFilter) query() neturl.Values {
	query := neturl.Values{}
	for name, values := range map[string][]string{
		"status": filter.Statuses, "kind": filter.Kinds, "priority": filter.Priorities,
		"cluster_id": filter.ClusterIDs, "label": filter.Labels,
	} {
		if len(values) > 0 {
			query.Set(name, joinComma(values))
		}
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query.Set("search", search)
	}
	if filter.NeedsHuman {
		query.Set("needs_human", "true")
	}
	if filter.IncludeReceipts {
		query.Set("include_receipts", "true")
	}
	return query
}

// GetTicketBoardSummary reads the board's counts for top-level tickets:
// per status, open per kind, priority and agent, what waits on a person,
// what is in flight and what closed in the last seven days.
// GET /api/v1/org/ai-tickets/summary
func (c *Client) GetTicketBoardSummary(ctx context.Context, filter TicketSummaryFilter) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, pathWithQuery(ticketBoardSummaryPath, filter.query()), nil)
}

// ListTicketChanges reads the changes applied on a ticket's behalf: each
// linked AI action with what it touched, who approved it, its change
// preview and the execution it produced.
// GET /api/v1/org/ai-tickets/{ticket_id}/changes
func (c *Client) ListTicketChanges(ctx context.Context, ticketID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, ticketsBasePath+"/"+neturl.PathEscape(ticketID)+"/changes", nil)
}

// UpdateTicket patches a ticket's metadata: title, body, priority, labels,
// cluster, staffing and due date. A member left out keeps its value; a nil
// cluster_id or due_at clears it, and labels replaces the whole set.
// PATCH /api/v1/org/ai-tickets/{ticket_id}
func (c *Client) UpdateTicket(ctx context.Context, ticketID string, changes map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPatch, ticketsBasePath+"/"+neturl.PathEscape(ticketID), changes)
}
