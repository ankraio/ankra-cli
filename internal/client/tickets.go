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

	// Structure: the ticket's parent, what it is to that parent, and what
	// stands under it. A ticket with no parent carries null parent fields
	// and a null child_role. The counts are pointers so a platform that does
	// not send one reads as unknown rather than as zero.
	ParentTicketID     *string `json:"parent_ticket_id"`
	ParentTicketNumber *int64  `json:"parent_ticket_number"`
	ParentTicketTitle  *string `json:"parent_ticket_title"`
	ParentTicketStatus *string `json:"parent_ticket_status"`
	// ChildRole is "symptom" (the root carries the work) or "subtask"
	// (worked like any ticket), and null on a ticket with no parent.
	ChildRole *string `json:"child_role"`
	// SubTicketCount and OpenSubTicketCount count every child, symptoms
	// included; LiveSymptomCount counts the open symptoms alone, and
	// SymptomsNeedingPersonCount how many of those a person owes an answer on.
	SubTicketCount             *int `json:"sub_ticket_count"`
	OpenSubTicketCount         *int `json:"open_sub_ticket_count"`
	LiveSymptomCount           *int `json:"live_symptom_count"`
	SymptomsNeedingPersonCount *int `json:"symptoms_needing_person_count"`
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
	// IncludeReceipts asks for the chat-fix receipts too (the records of
	// writes a person made through the AI chat or MCP), which the board
	// leaves out unless asked.
	IncludeReceipts bool
	// ParentTicketID lists only the tickets directly under this one;
	// TopLevelOnly lists only tickets that sit under no other.
	ParentTicketID string
	TopLevelOnly   bool
	Search         string
	Limit          int
	Offset         int
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
	if filter.IncludeReceipts {
		query.Set("include_receipts", "true")
	}
	if filter.ParentTicketID != "" {
		query.Set("parent_ticket_id", filter.ParentTicketID)
	}
	if filter.TopLevelOnly {
		query.Set("top_level_only", "true")
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

// TicketLinkedTicket names the ticket at the other end of a ticket link:
// the target of a link this ticket holds, the holder of one it receives.
type TicketLinkedTicket struct {
	ID           string  `json:"id"`
	TicketNumber int64   `json:"ticket_number"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Resolution   *string `json:"resolution"`
}

// TicketLink is one link on a ticket, as stored: TicketID is the ticket
// that holds it, so an incoming link's TicketID is the other ticket and its
// TargetID is the ticket being read. LinkedTicket is null for a target that
// is not a ticket, and for a ticket target outside the organisation.
type TicketLink struct {
	ID           string              `json:"id"`
	TicketID     string              `json:"ticket_id"`
	TargetKind   string              `json:"target_kind"`
	TargetID     string              `json:"target_id"`
	Relation     string              `json:"relation"`
	CreatedAt    string              `json:"created_at"`
	Direction    string              `json:"direction"`
	LinkedTicket *TicketLinkedTicket `json:"linked_ticket"`
}

// TicketLinkList is the GET /api/v1/org/ai-tickets/{id}/links body: the
// links the ticket holds and the ticket links other tickets hold on it.
// IncomingLinks is nil, not empty, when the platform did not send the
// field at all, so a platform that predates it is not read as "no ticket
// links here".
type TicketLinkList struct {
	RawDocument   `json:"-" yaml:"-"`
	Links         []TicketLink `json:"links"`
	IncomingLinks []TicketLink `json:"incoming_links"`
}

// TicketLinkRequest is the add-link body. TargetID is a ticket's number
// (T-42 or 42) or id for target kind "ticket"; Relation empty means the
// platform's default, relates_to.
type TicketLinkRequest struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	Relation   string `json:"relation,omitempty"`
}

// TicketNestRequest moves a ticket under a root as a symptom. Exactly one
// of RootTicketID and RootTicketNumber is set.
type TicketNestRequest struct {
	RootTicketID     *string `json:"root_ticket_id,omitempty"`
	RootTicketNumber *int64  `json:"root_ticket_number,omitempty"`
	Reason           *string `json:"reason,omitempty"`
}

// TicketCloseRequest closes a ticket. DuplicateOf names the ticket that
// covers the same cause, by number or id, and goes only with resolution
// "duplicate"; the platform links the closed ticket to that ticket's
// canonical keeper.
type TicketCloseRequest struct {
	Resolution        string  `json:"resolution"`
	Note              *string `json:"note,omitempty"`
	EvidenceKind      *string `json:"evidence_kind,omitempty"`
	EvidenceReference *string `json:"evidence_reference,omitempty"`
	DuplicateOf       *string `json:"duplicate_of,omitempty"`
}

// TicketCreateRequest files a ticket, or a sub-ticket under a parent.
// ClusterID and Staffing apply to a top-level ticket only: the sub-ticket
// route does not read them.
type TicketCreateRequest struct {
	Title     string   `json:"title"`
	Body      *string  `json:"body,omitempty"`
	Kind      *string  `json:"kind,omitempty"`
	Priority  *string  `json:"priority,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	ClusterID *string  `json:"cluster_id,omitempty"`
	Staffing  *string  `json:"staffing,omitempty"`
}

func ticketPath(c *Client, ticketID string, suffix string) string {
	return c.BaseURL + ticketsBasePath + "/" + url.PathEscape(ticketID) + suffix
}

// ListTicketLinks reads the links a ticket holds and the ticket links other
// tickets hold on it.
func (c *Client) ListTicketLinks(ticketID string) (*TicketLinkList, error) {
	var response TicketLinkList
	if err := c.sendJSON(http.MethodGet, ticketPath(c, ticketID, "/links"), nil, &response); err != nil {
		return nil, fmt.Errorf("reading ticket links: %w", err)
	}
	// An answer without the list is not a ticket with no links.
	if response.Links == nil {
		return nil, fmt.Errorf("reading ticket links: the platform's answer carried no links list")
	}
	return &response, nil
}

// AddTicketLink links a record to the ticket. The platform answers with the
// link, the existing one when it was already there.
func (c *Client) AddTicketLink(ticketID string, link TicketLinkRequest) (*TicketLink, error) {
	var response TicketLink
	if err := c.sendJSON(http.MethodPost, ticketPath(c, ticketID, "/links"), link, &response); err != nil {
		return nil, fmt.Errorf("linking ticket: %w", err)
	}
	return &response, nil
}

// RemoveTicketLink removes one link. holderTicketID is the ticket that
// holds the link (its ticket_id), which for an incoming link is the other
// ticket: the platform finds a link only through its holder.
func (c *Client) RemoveTicketLink(holderTicketID string, linkID string) error {
	requestURL := ticketPath(c, holderTicketID, "/links/"+url.PathEscape(linkID))
	if err := c.sendJSON(http.MethodDelete, requestURL, nil, nil); err != nil {
		return fmt.Errorf("removing ticket link: %w", err)
	}
	return nil
}

// NestTicket moves a ticket under a root ticket as a symptom and returns the
// ticket as it now stands.
func (c *Client) NestTicket(ticketID string, request TicketNestRequest) (*Ticket, error) {
	var ticket Ticket
	if err := c.sendJSON(http.MethodPost, ticketPath(c, ticketID, "/nest"), request, &ticket); err != nil {
		return nil, fmt.Errorf("nesting ticket: %w", err)
	}
	return &ticket, nil
}

// DetachTicket makes a symptom a top-level ticket again. A ticket under no
// root comes back unchanged.
func (c *Client) DetachTicket(ticketID string, reason string) (*Ticket, error) {
	payload := map[string]string{}
	if reason != "" {
		payload["reason"] = reason
	}
	var ticket Ticket
	if err := c.sendJSON(http.MethodPost, ticketPath(c, ticketID, "/detach"), payload, &ticket); err != nil {
		return nil, fmt.Errorf("detaching ticket: %w", err)
	}
	return &ticket, nil
}

// CloseTicket closes a ticket with a resolution.
func (c *Client) CloseTicket(ticketID string, request TicketCloseRequest) (*Ticket, error) {
	var ticket Ticket
	if err := c.sendJSON(http.MethodPost, ticketPath(c, ticketID, "/close"), request, &ticket); err != nil {
		return nil, fmt.Errorf("closing ticket: %w", err)
	}
	return &ticket, nil
}

// CreateTicket files a top-level ticket on the board.
func (c *Client) CreateTicket(request TicketCreateRequest) (*Ticket, error) {
	var ticket Ticket
	if err := c.sendJSON(http.MethodPost, c.BaseURL+ticketsBasePath, request, &ticket); err != nil {
		return nil, fmt.Errorf("filing ticket: %w", err)
	}
	return &ticket, nil
}

// CreateSubTicket files a sub-ticket under an open parent ticket.
func (c *Client) CreateSubTicket(parentTicketID string, request TicketCreateRequest) (*Ticket, error) {
	var ticket Ticket
	if err := c.sendJSON(http.MethodPost, ticketPath(c, parentTicketID, "/sub-tickets"), request, &ticket); err != nil {
		return nil, fmt.Errorf("filing sub-ticket: %w", err)
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
