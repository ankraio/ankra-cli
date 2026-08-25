package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// TicketDecisionOption is one alternative an agent offered when it blocked
// a ticket on a choice.
type TicketDecisionOption struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Recommended bool   `json:"recommended"`
}

// TicketDecisionRequest is the choice a blocked ticket waits on: the
// agent's question and the options it offered. The person picks one or
// answers with something else in their own words.
type TicketDecisionRequest struct {
	Prompt  string                 `json:"prompt"`
	Options []TicketDecisionOption `json:"options"`
}

// Ticket mirrors one AI board ticket from /api/v1/org/ai-tickets. Only the
// fields the CLI renders are declared; the rest of the wire shape is
// ignored on decode.
type Ticket struct {
	ID                string                 `json:"id"`
	TicketNumber      int64                  `json:"ticket_number"`
	Title             string                 `json:"title"`
	Body              string                 `json:"body"`
	Kind              string                 `json:"kind"`
	Status            string                 `json:"status"`
	Priority          string                 `json:"priority"`
	AssigneeAgentName *string                `json:"assignee_agent_name"`
	AssigneeUserEmail *string                `json:"assignee_user_email"`
	AssigneeUserName  *string                `json:"assignee_user_name"`
	ClusterName       *string                `json:"cluster_name"`
	PlanStatus        string                 `json:"plan_status"`
	Labels            []string               `json:"labels"`
	Resolution        *string                `json:"resolution"`
	CreatedAt         string                 `json:"created_at"`
	UpdatedAt         string                 `json:"updated_at"`
	ClosedAt          *string                `json:"closed_at"`
	DecisionRequest   *TicketDecisionRequest `json:"decision_request"`
}

// TicketListResponse is the GET /api/v1/org/ai-tickets body.
type TicketListResponse struct {
	Tickets    []Ticket `json:"tickets"`
	TotalCount int      `json:"total_count"`
}

// TicketEvent is one timeline entry on a ticket.
type TicketEvent struct {
	ID              string                 `json:"id"`
	Sequence        int64                  `json:"sequence"`
	EventType       string                 `json:"event_type"`
	AuthorKind      string                 `json:"author_kind"`
	AuthorAgentName *string                `json:"author_agent_name"`
	AuthorUserName  *string                `json:"author_user_name"`
	Body            string                 `json:"body"`
	Payload         map[string]interface{} `json:"payload"`
	CreatedAt       string                 `json:"created_at"`
}

// TicketEventListResponse is the GET /api/v1/org/ai-tickets/{id}/events body.
type TicketEventListResponse struct {
	Events []TicketEvent `json:"events"`
}

// TicketListFilter narrows a ticket listing.
type TicketListFilter struct {
	Statuses      []string
	NeedsHuman    bool
	IncludeClosed bool
	Search        string
	Limit         int
	Offset        int
}

// TicketDecision is the answer to a ticket's pending decision request: the
// key of an offered option, or the answer in the person's own words when no
// option fits (a note beside a chosen option is allowed too).
type TicketDecision struct {
	OptionKey *string `json:"option_key"`
	Answer    *string `json:"answer"`
}

const ticketsBasePath = "/api/v1/org/ai-tickets"

// ListTickets lists the organisation's AI board tickets.
func (c *Client) ListTickets(filter TicketListFilter) (*TicketListResponse, error) {
	query := url.Values{}
	if len(filter.Statuses) > 0 {
		query.Set("status", joinComma(filter.Statuses))
	}
	if filter.NeedsHuman {
		query.Set("needs_human", "true")
	}
	if filter.IncludeClosed {
		query.Set("include_closed", "true")
	}
	if filter.Search != "" {
		query.Set("search", filter.Search)
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Offset > 0 {
		query.Set("offset", strconv.Itoa(filter.Offset))
	}
	requestURL := c.BaseURL + ticketsBasePath
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var response TicketListResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, fmt.Errorf("listing tickets: %w", err)
	}
	return &response, nil
}

// GetTicket fetches one ticket by id.
func (c *Client) GetTicket(ticketID string) (*Ticket, error) {
	var ticket Ticket
	if err := c.getJSON(c.BaseURL+ticketsBasePath+"/"+url.PathEscape(ticketID), &ticket); err != nil {
		return nil, fmt.Errorf("getting ticket: %w", err)
	}
	return &ticket, nil
}

// ListTicketEvents reads a ticket's timeline, oldest first.
func (c *Client) ListTicketEvents(ticketID string, limit int) (*TicketEventListResponse, error) {
	requestURL := c.BaseURL + ticketsBasePath + "/" + url.PathEscape(ticketID) + "/events"
	if limit > 0 {
		requestURL += "?limit=" + strconv.Itoa(limit)
	}
	var response TicketEventListResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, fmt.Errorf("listing ticket events: %w", err)
	}
	return &response, nil
}

// CommentOnTicket posts a comment on the ticket as the caller.
func (c *Client) CommentOnTicket(ticketID string, body string) (*TicketEvent, error) {
	var event TicketEvent
	requestURL := c.BaseURL + ticketsBasePath + "/" + url.PathEscape(ticketID) + "/comments"
	if err := c.sendJSON(http.MethodPost, requestURL, map[string]string{"body": body}, &event); err != nil {
		return nil, fmt.Errorf("commenting on ticket: %w", err)
	}
	return &event, nil
}

// TransitionTicket moves the ticket to a new status with an optional note.
func (c *Client) TransitionTicket(ticketID string, status string, note string) (*Ticket, error) {
	payload := map[string]interface{}{"status": status}
	if note != "" {
		payload["note"] = note
	}
	var ticket Ticket
	requestURL := c.BaseURL + ticketsBasePath + "/" + url.PathEscape(ticketID) + "/transition"
	if err := c.sendJSON(http.MethodPost, requestURL, payload, &ticket); err != nil {
		return nil, fmt.Errorf("moving ticket: %w", err)
	}
	return &ticket, nil
}

// DecideTicket answers the choice a blocked ticket waits on; the agent
// resumes from the recorded answer.
func (c *Client) DecideTicket(ticketID string, decision TicketDecision) (*Ticket, error) {
	var ticket Ticket
	requestURL := c.BaseURL + ticketsBasePath + "/" + url.PathEscape(ticketID) + "/decision"
	if err := c.sendJSON(http.MethodPost, requestURL, decision, &ticket); err != nil {
		return nil, fmt.Errorf("recording the decision: %w", err)
	}
	return &ticket, nil
}

func joinComma(values []string) string {
	joined := ""
	for index, value := range values {
		if index > 0 {
			joined += ","
		}
		joined += value
	}
	return joined
}
