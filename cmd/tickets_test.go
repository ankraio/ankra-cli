package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type ticketsMock struct {
	baseMock
	tickets       []client.Ticket
	events        []client.TicketEvent
	listFilters   []client.TicketListFilter
	comments      []string
	transitions   []string
	decisions     []client.TicketDecision
	decisionError error
}

func (m *ticketsMock) ListTickets(filter client.TicketListFilter) (*client.TicketListResponse, error) {
	m.listFilters = append(m.listFilters, filter)
	if filter.Offset >= len(m.tickets) {
		return &client.TicketListResponse{Tickets: []client.Ticket{}, TotalCount: len(m.tickets)}, nil
	}
	return &client.TicketListResponse{Tickets: m.tickets[filter.Offset:], TotalCount: len(m.tickets)}, nil
}

func (m *ticketsMock) GetTicket(ticketID string) (*client.Ticket, error) {
	for index := range m.tickets {
		if m.tickets[index].ID == ticketID {
			return &m.tickets[index], nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, "Ticket not found.")
}

func (m *ticketsMock) ListTicketEvents(ticketID string, limit int) (*client.TicketEventListResponse, error) {
	return &client.TicketEventListResponse{Events: m.events}, nil
}

func (m *ticketsMock) CommentOnTicket(ticketID string, body string) (*client.TicketEvent, error) {
	m.comments = append(m.comments, body)
	return &client.TicketEvent{Sequence: 12, EventType: "comment", Body: body}, nil
}

func (m *ticketsMock) TransitionTicket(ticketID string, status string, note string) (*client.Ticket, error) {
	m.transitions = append(m.transitions, status)
	moved := m.tickets[0]
	moved.Status = status
	return &moved, nil
}

func (m *ticketsMock) DecideTicket(ticketID string, decision client.TicketDecision) (*client.Ticket, error) {
	m.decisions = append(m.decisions, decision)
	if m.decisionError != nil {
		return nil, m.decisionError
	}
	decided := m.tickets[0]
	decided.DecisionRequest = nil
	return &decided, nil
}

const ticketsTestID = "c5bde475-eda1-4067-9c08-83c2aff6cdb0"

func blockedTicketWithChoice() client.Ticket {
	agent := "DevOps"
	return client.Ticket{
		ID: ticketsTestID, TicketNumber: 8,
		Title: "launch-site-demo publishes to the wrong registry",
		Body:  "Two ways out.",
		Kind:  "chore", Status: "blocked", Priority: "medium",
		AssigneeAgentName: &agent, PlanStatus: "none", Labels: []string{},
		CreatedAt: "2026-08-25T09:12:00Z", UpdatedAt: "2026-08-25T09:40:00Z",
		DecisionRequest: &client.TicketDecisionRequest{
			Prompt: "Which registry should launch-site-demo publish to?",
			Options: []client.TicketDecisionOption{
				{Key: "a", Title: "Re-point the declaration", Summary: "No runtime change.", Recommended: true},
				{Key: "b", Title: "Move publishing to Harbor", Summary: "Rewrites the workflow."},
			},
		},
	}
}

func TestTicketsListShowsWhatEachTicketWaitsOn(t *testing.T) {
	investigating := blockedTicketWithChoice()
	investigating.TicketNumber = 9
	investigating.ID = "d0ce0e4b-ebf6-493c-a608-17b9d29420b3"
	investigating.Status = "investigating"
	investigating.DecisionRequest = nil
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice(), investigating}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsListCmd},
		"tickets", "list", "--needs-human")
	if err != nil {
		t.Fatalf("tickets list failed: %v", err)
	}
	if !strings.Contains(out, "T-8") || !strings.Contains(out, "your decision") {
		t.Fatalf("listing must flag the ticket waiting on a decision: %s", out)
	}
	if !strings.Contains(out, "T-9") || strings.Contains(out, "T-9") && !strings.Contains(out, "DevOps (agent)") {
		t.Fatalf("listing missing the second ticket or its assignee: %s", out)
	}
	if len(mock.listFilters) != 1 || !mock.listFilters[0].NeedsHuman {
		t.Fatalf("--needs-human must reach the API filter: %+v", mock.listFilters)
	}
}

func TestTicketsGetRendersTheDecisionWithItsOptions(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-8")
	if err != nil {
		t.Fatalf("tickets get failed: %v", err)
	}
	for _, want := range []string{
		"Your decision is needed: Which registry should launch-site-demo publish to?",
		"* [a] Re-point the declaration",
		"  [b] Move publishing to Harbor",
		"No runtime change.",
		"Something else",
		"ankra tickets decide T-8 --option <key>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tickets get output lacks %q:\n%s", want, out)
		}
	}
	// A number resolves through the listing, closed tickets included.
	if len(mock.listFilters) == 0 || !mock.listFilters[0].IncludeClosed {
		t.Fatalf("a ticket number must be resolved through the closed-inclusive listing: %+v", mock.listFilters)
	}
}

func TestTicketsGetByUUIDDoesNotWalkTheListing(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", ticketsTestID)
	if err != nil {
		t.Fatalf("tickets get failed: %v", err)
	}
	if !strings.Contains(out, "T-8") || len(mock.listFilters) != 0 {
		t.Fatalf("a UUID must be fetched directly: listFilters=%+v out=%s", mock.listFilters, out)
	}
}

func TestTicketsGetOffersNoDecisionOnAPlainBlock(t *testing.T) {
	plain := blockedTicketWithChoice()
	plain.DecisionRequest = nil
	mock := &ticketsMock{tickets: []client.Ticket{plain}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "8")
	if err != nil {
		t.Fatalf("tickets get failed: %v", err)
	}
	if strings.Contains(out, "Your decision is needed") {
		t.Fatalf("a blocked ticket without a request has nothing to choose from: %s", out)
	}
}

func TestTicketsGetUnknownNumberUsesExitNotFound(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-404")
	if err == nil {
		t.Fatal("expected an error for a missing ticket")
	}
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("expected exit code %d, got %d", exitNotFound, got)
	}
}

func TestTicketsDecideSendsTheChosenOption(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDecideCmd},
		"tickets", "decide", "T-8", "--option", "A", "--answer", "Before the launch.")
	if err != nil {
		t.Fatalf("tickets decide failed: %v", err)
	}
	if len(mock.decisions) != 1 || mock.decisions[0].OptionKey == nil || *mock.decisions[0].OptionKey != "A" ||
		mock.decisions[0].Answer == nil || *mock.decisions[0].Answer != "Before the launch." {
		t.Fatalf("decision sent = %+v", mock.decisions)
	}
	if !strings.Contains(out, "Decision recorded on T-8: Re-point the declaration (option 'a')") {
		t.Fatalf("confirmation missing: %s", out)
	}
}

func TestTicketsDecideSendsSomethingElse(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDecideCmd},
		"tickets", "decide", "8", "--answer", "Publish to both for a week, then decide.")
	if err != nil {
		t.Fatalf("tickets decide failed: %v", err)
	}
	if len(mock.decisions) != 1 || mock.decisions[0].OptionKey != nil ||
		*mock.decisions[0].Answer != "Publish to both for a week, then decide." {
		t.Fatalf("decision sent = %+v", mock.decisions)
	}
	if !strings.Contains(out, "something else - Publish to both") {
		t.Fatalf("confirmation missing: %s", out)
	}
}

func TestTicketsDecideRefusesLocallyWhatTheServerWould(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDecideCmd},
		"tickets", "decide", "T-8", "--option", "c")
	if err == nil || !strings.Contains(err.Error(), "not one of the offered options (a, b)") {
		t.Fatalf("an unknown option must be refused before the call, got %v", err)
	}
	resetTreeFlags(t, ticketsDecideCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDecideCmd}, "tickets", "decide", "T-8")
	if err == nil || !strings.Contains(err.Error(), "--option") {
		t.Fatalf("an empty decision must be refused, got %v", err)
	}
	plain := blockedTicketWithChoice()
	plain.Status = "investigating"
	plain.DecisionRequest = nil
	mock = &ticketsMock{tickets: []client.Ticket{plain}}
	resetTreeFlags(t, ticketsDecideCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDecideCmd},
		"tickets", "decide", "T-8", "--option", "a")
	if err == nil || !strings.Contains(err.Error(), "not waiting on a decision") {
		t.Fatalf("a ticket without a pending choice must be refused, got %v", err)
	}
	if len(mock.decisions) != 0 {
		t.Fatalf("no decision must be sent for a refused call: %+v", mock.decisions)
	}
}

func TestTicketsCommentAndTransition(t *testing.T) {
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCommentCmd},
		"tickets", "comment", "T-8", "--body", "  Looking into it.  ")
	if err != nil {
		t.Fatalf("tickets comment failed: %v", err)
	}
	if len(mock.comments) != 1 || mock.comments[0] != "Looking into it." || !strings.Contains(out, "event #12") {
		t.Fatalf("comment = %+v out=%s", mock.comments, out)
	}

	out, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsTransitionCmd},
		"tickets", "transition", "T-8", "--status", "investigating", "--note", "Resuming.")
	if err != nil {
		t.Fatalf("tickets transition failed: %v", err)
	}
	if len(mock.transitions) != 1 || mock.transitions[0] != "investigating" || !strings.Contains(out, "T-8 is now investigating") {
		t.Fatalf("transition = %+v out=%s", mock.transitions, out)
	}
}

func TestTicketsEventsListsTheTimeline(t *testing.T) {
	author := "DevOps"
	mock := &ticketsMock{
		tickets: []client.Ticket{blockedTicketWithChoice()},
		events: []client.TicketEvent{{Sequence: 7, EventType: "status_change", AuthorKind: "agent",
			AuthorAgentName: &author, Body: "Investigation conclusive.", CreatedAt: "2026-08-25T12:21:03Z"}},
	}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsEventsCmd}, "tickets", "events", "T-8")
	if err != nil {
		t.Fatalf("tickets events failed: %v", err)
	}
	if !strings.Contains(out, "status_change") || !strings.Contains(out, "DevOps") ||
		!strings.Contains(out, "Investigation conclusive.") {
		t.Fatalf("timeline missing fields: %s", out)
	}
}
