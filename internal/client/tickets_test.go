package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const ticketsClientTestID = "a1b2c3d4-0000-4000-8000-000000000042"

func readRequestJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, readError := io.ReadAll(r.Body)
	if readError != nil {
		t.Fatalf("read body: %v", readError)
	}
	body := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if decodeError := json.Unmarshal(raw, &body); decodeError != nil {
			t.Fatalf("body %q is not JSON: %v", raw, decodeError)
		}
	}
	return body
}

func TestListTickets_SendsTheStructureAndReceiptFilters(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/api/v1/org/ai-tickets" || query.Get("parent_ticket_id") != ticketsClientTestID ||
			query.Get("top_level_only") != "" || query.Get("include_receipts") != "true" || query.Get("include_closed") != "true" {
			t.Errorf("request = %s", r.URL.String())
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"tickets": []any{}, "total_count": 0})
	}
	_, listError := newTestClient(t, handler).ListTickets(TicketListFilter{
		ParentTicketID: ticketsClientTestID, IncludeReceipts: true, IncludeClosed: true,
	})
	if listError != nil {
		t.Fatalf("ListTickets: %v", listError)
	}

	topLevel := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("top_level_only") != "true" || r.URL.Query().Get("include_receipts") != "" {
			t.Errorf("request = %s", r.URL.String())
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"tickets": []any{}, "total_count": 0})
	}
	if _, listError := newTestClient(t, topLevel).ListTickets(TicketListFilter{TopLevelOnly: true}); listError != nil {
		t.Fatalf("ListTickets top-level: %v", listError)
	}
}

func TestGetTicket_DecodesTheStructureFields(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"id": ticketsClientTestID, "ticket_number": 51, "title": "api pods restart", "status": "blocked",
			"parent_ticket_id": "a1b2c3d4-0000-4000-8000-000000000001", "parent_ticket_number": 42,
			"parent_ticket_title": "Ingress certificate expired", "parent_ticket_status": "investigating",
			"child_role": "symptom", "sub_ticket_count": 0, "open_sub_ticket_count": 0,
			"live_symptom_count": 0, "symptoms_needing_person_count": 0,
		})
	}
	ticket, getError := newTestClient(t, handler).GetTicket(ticketsClientTestID)
	if getError != nil {
		t.Fatalf("GetTicket: %v", getError)
	}
	if ticket.ParentTicketNumber == nil || *ticket.ParentTicketNumber != 42 || ticket.ChildRole == nil ||
		*ticket.ChildRole != "symptom" || ticket.ParentTicketTitle == nil || ticket.ParentTicketStatus == nil ||
		ticket.SubTicketCount == nil || *ticket.SubTicketCount != 0 || ticket.LiveSymptomCount == nil {
		t.Fatalf("ticket = %+v", ticket)
	}

	// A platform that does not send a count leaves it unknown, not zero.
	older := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": ticketsClientTestID, "ticket_number": 51})
	}
	ticket, getError = newTestClient(t, older).GetTicket(ticketsClientTestID)
	if getError != nil || ticket.SubTicketCount != nil || ticket.LiveSymptomCount != nil || ticket.ChildRole != nil {
		t.Fatalf("absent counts must stay nil: %+v %v", ticket, getError)
	}
}

func TestListTicketLinks_KeepsAnAbsentIncomingListApartFromAnEmptyOne(t *testing.T) {
	both := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/ai-tickets/"+ticketsClientTestID+"/links" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"links": []any{}, "incoming_links": []any{
			map[string]any{"id": "l1", "ticket_id": "other", "target_kind": "ticket", "target_id": ticketsClientTestID,
				"relation": "duplicate_of", "direction": "incoming",
				"linked_ticket": map[string]any{"id": "other", "ticket_number": 50, "title": "Cert warning", "status": "done", "resolution": "duplicate"}},
		}})
	}
	links, listError := newTestClient(t, both).ListTicketLinks(ticketsClientTestID)
	if listError != nil || links.Links == nil || len(links.IncomingLinks) != 1 ||
		links.IncomingLinks[0].LinkedTicket == nil || links.IncomingLinks[0].LinkedTicket.TicketNumber != 50 {
		t.Fatalf("links = %+v, %v", links, listError)
	}
	if links.RawJSON() == nil {
		t.Fatal("the platform's document must be kept for -o json")
	}

	older := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"links": []any{}})
	}
	links, listError = newTestClient(t, older).ListTicketLinks(ticketsClientTestID)
	if listError != nil || links.IncomingLinks != nil {
		t.Fatalf("an absent incoming list must stay nil: %+v %v", links, listError)
	}

	// An answer with no list at all is not a ticket with no links.
	empty := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{})
	}
	if _, listError = newTestClient(t, empty).ListTicketLinks(ticketsClientTestID); listError == nil {
		t.Fatal("an answer without links must be an error")
	}
}

func TestAddAndRemoveTicketLink(t *testing.T) {
	add := func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/ai-tickets/"+ticketsClientTestID+"/links" ||
			body["target_kind"] != "ticket" || body["target_id"] != "T-42" || body["relation"] != "duplicate_of" {
			t.Errorf("request = %s %s %v", r.Method, r.URL.Path, body)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{"id": "l1", "relation": "duplicate_of", "direction": "outgoing"})
	}
	link, addError := newTestClient(t, add).AddTicketLink(ticketsClientTestID,
		TicketLinkRequest{TargetKind: "ticket", TargetID: "T-42", Relation: "duplicate_of"})
	if addError != nil || link.ID != "l1" {
		t.Fatalf("AddTicketLink = %+v, %v", link, addError)
	}

	defaultRelation := func(w http.ResponseWriter, r *http.Request) {
		if _, sent := readRequestJSON(t, r)["relation"]; sent {
			t.Error("an empty relation must be left to the platform's default")
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": "l1"})
	}
	if _, addError = newTestClient(t, defaultRelation).AddTicketLink(ticketsClientTestID,
		TicketLinkRequest{TargetKind: "ticket", TargetID: "T-42"}); addError != nil {
		t.Fatalf("AddTicketLink default relation: %v", addError)
	}

	remove := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/org/ai-tickets/holder-id/links/link-id" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	if removeError := newTestClient(t, remove).RemoveTicketLink("holder-id", "link-id"); removeError != nil {
		t.Fatalf("RemoveTicketLink: %v", removeError)
	}
}

func TestNestAndDetachTicket_SendTheBodyThePlatformReads(t *testing.T) {
	nest := func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		_, idSent := body["root_ticket_id"]
		if r.URL.Path != "/api/v1/org/ai-tickets/"+ticketsClientTestID+"/nest" || idSent ||
			body["root_ticket_number"] != float64(42) || body["reason"] != "Same cert" {
			t.Errorf("request = %s %v", r.URL.Path, body)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": ticketsClientTestID, "child_role": "symptom"})
	}
	number := int64(42)
	reason := "Same cert"
	nested, nestError := newTestClient(t, nest).NestTicket(ticketsClientTestID,
		TicketNestRequest{RootTicketNumber: &number, Reason: &reason})
	if nestError != nil || nested.ChildRole == nil || *nested.ChildRole != "symptom" {
		t.Fatalf("NestTicket = %+v, %v", nested, nestError)
	}

	detach := func(w http.ResponseWriter, r *http.Request) {
		if body := readRequestJSON(t, r); r.URL.Path != "/api/v1/org/ai-tickets/"+ticketsClientTestID+"/detach" || len(body) != 0 {
			t.Errorf("request = %s %v", r.URL.Path, body)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": ticketsClientTestID})
	}
	if _, detachError := newTestClient(t, detach).DetachTicket(ticketsClientTestID, ""); detachError != nil {
		t.Fatalf("DetachTicket: %v", detachError)
	}
}

// A refusal carries the platform's own sentence and status, which the
// command classifies into an exit code.
func TestNestTicket_RefusalKeepsTheSentence(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": "T-42 is closed, so nothing can be nested under it."})
	}
	number := int64(42)
	_, nestError := newTestClient(t, handler).NestTicket(ticketsClientTestID, TicketNestRequest{RootTicketNumber: &number})
	var unexpected *UnexpectedResponseError
	if !errors.As(nestError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
		!strings.Contains(nestError.Error(), "T-42 is closed, so nothing can be nested under it.") {
		t.Fatalf("nest refusal = %v", nestError)
	}
}

func TestCloseTicket_SendsDuplicateOfOnlyWhenGiven(t *testing.T) {
	withKeeper := func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		if r.URL.Path != "/api/v1/org/ai-tickets/"+ticketsClientTestID+"/close" ||
			body["resolution"] != "duplicate" || body["duplicate_of"] != "T-42" {
			t.Errorf("request = %s %v", r.URL.Path, body)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": ticketsClientTestID, "status": "done", "resolution": "duplicate"})
	}
	keeper := "T-42"
	if _, closeError := newTestClient(t, withKeeper).CloseTicket(ticketsClientTestID,
		TicketCloseRequest{Resolution: "duplicate", DuplicateOf: &keeper}); closeError != nil {
		t.Fatalf("CloseTicket: %v", closeError)
	}

	plain := func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		for _, absent := range []string{"duplicate_of", "note", "evidence_kind", "evidence_reference"} {
			if _, sent := body[absent]; sent {
				t.Errorf("%s must not be sent when not given: %v", absent, body)
			}
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": ticketsClientTestID, "status": "done"})
	}
	if _, closeError := newTestClient(t, plain).CloseTicket(ticketsClientTestID,
		TicketCloseRequest{Resolution: "fixed"}); closeError != nil {
		t.Fatalf("CloseTicket: %v", closeError)
	}
}

func TestCreateTicketAndSubTicket_UseTheirRoutes(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		switch r.URL.Path {
		case "/api/v1/org/ai-tickets":
			if body["title"] != "Rotate the token" || body["staffing"] != "manual" {
				t.Errorf("create body = %v", body)
			}
		case "/api/v1/org/ai-tickets/" + ticketsClientTestID + "/sub-tickets":
			if _, sent := body["staffing"]; sent || body["title"] != "Renew the certificate" {
				t.Errorf("sub-ticket body = %v", body)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{"id": "new", "ticket_number": 60})
	}
	testClient := newTestClient(t, handler)
	staffing := "manual"
	if _, createError := testClient.CreateTicket(TicketCreateRequest{Title: "Rotate the token", Staffing: &staffing}); createError != nil {
		t.Fatalf("CreateTicket: %v", createError)
	}
	if _, createError := testClient.CreateSubTicket(ticketsClientTestID, TicketCreateRequest{Title: "Renew the certificate"}); createError != nil {
		t.Fatalf("CreateSubTicket: %v", createError)
	}
}
