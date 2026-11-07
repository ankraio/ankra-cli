package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	rootTicketID    = "a1b2c3d4-0000-4000-8000-000000000042"
	symptomTicketID = "a1b2c3d4-0000-4000-8000-000000000051"
	holderTicketID  = "a1b2c3d4-0000-4000-8000-000000000050"
)

func rootTicket() client.Ticket {
	return client.Ticket{
		ID: rootTicketID, TicketNumber: 42, Title: "Ingress certificate expired",
		Kind: "incident", Status: "investigating", Priority: "high", PlanStatus: "none",
		Labels: []string{}, UpdatedAt: "2026-11-03T10:00:00Z",
		SubTicketCount: intPointer(4), OpenSubTicketCount: intPointer(3),
		LiveSymptomCount: intPointer(2), SymptomsNeedingPersonCount: intPointer(1),
	}
}

func childTicket(id string, number int64, role string, status string, title string) client.Ticket {
	parentNumber := int64(42)
	return client.Ticket{
		ID: id, TicketNumber: number, Title: title, Kind: "incident", Status: status, Priority: "medium",
		PlanStatus: "none", Labels: []string{}, UpdatedAt: "2026-11-03T10:00:00Z",
		ParentTicketID: stringPointer(rootTicketID), ParentTicketNumber: &parentNumber,
		ParentTicketTitle: stringPointer("Ingress certificate expired"), ParentTicketStatus: stringPointer("investigating"),
		ChildRole: stringPointer(role), SubTicketCount: intPointer(0),
		OpenSubTicketCount: intPointer(0), LiveSymptomCount: intPointer(0), SymptomsNeedingPersonCount: intPointer(0),
	}
}

func ticketLink(id string, holder string, direction string, relation string, other *client.TicketLinkedTicket) client.TicketLink {
	return client.TicketLink{ID: id, TicketID: holder, TargetKind: "ticket", TargetID: rootTicketID,
		Relation: relation, Direction: direction, LinkedTicket: other, CreatedAt: "2026-11-03T09:00:00Z"}
}

func structuredRootMock() *ticketsMock {
	symptom := childTicket(symptomTicketID, 51, "symptom", "blocked", "api pods restart on TLS errors")
	second := childTicket("a1b2c3d4-0000-4000-8000-000000000052", 52, "symptom", "investigating", "Probe fails on /healthz")
	subtask := childTicket("a1b2c3d4-0000-4000-8000-000000000053", 53, "subtask", "triage", "Renew the wildcard certificate")
	return &ticketsMock{
		tickets: []client.Ticket{rootTicket(), symptom, second, subtask},
		links: map[string]*client.TicketLinkList{rootTicketID: {
			Links: []client.TicketLink{ticketLink("11111111-0000-4000-8000-000000000001", rootTicketID, "outgoing",
				"recurrence_of", &client.TicketLinkedTicket{TicketNumber: 31, Title: "Ingress certificate expired",
					Status: "done", Resolution: stringPointer("fixed")})},
			IncomingLinks: []client.TicketLink{ticketLink("11111111-0000-4000-8000-000000000002", holderTicketID, "incoming",
				"duplicate_of", &client.TicketLinkedTicket{ID: holderTicketID, TicketNumber: 50,
					Title: "Certificate warning on prod", Status: "done", Resolution: stringPointer("duplicate")})},
		}},
	}
}

func TestTicketsGetShowsWhatIsUnderARootAndItsLinks(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-42")
	if err != nil {
		t.Fatalf("tickets get failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Under it:  2 open symptoms (1 waiting on a person), 1 open sub-ticket",
		"Under this ticket:",
		"T-51", "api pods restart on TLS errors", "blocked",
		"T-53", "sub-ticket", "Renew the wildcard certificate",
		"Recurrence of", "T-31", "done, fixed",
		"Duplicate of this ticket", "T-50", "Certificate warning on prod",
		"11111111-0000-4000-8000-000000000002",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tickets get output lacks %q:\n%s", want, out)
		}
	}
	var children *client.TicketListFilter
	for index := range mock.listFilters {
		if mock.listFilters[index].ParentTicketID != "" {
			children = &mock.listFilters[index]
		}
	}
	if children == nil || children.ParentTicketID != rootTicketID || !children.IncludeClosed || !children.IncludeReceipts {
		t.Fatalf("the tickets under the root must be read closed and receipts included: %+v", mock.listFilters)
	}
}

func TestTicketsGetShowsTheRootOfASymptom(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-51")
	if err != nil {
		t.Fatalf("tickets get failed: %v\n%s", err, out)
	}
	want := `Parent:    T-42 "Ingress certificate expired" (investigating). This ticket is a symptom: the root carries the work.`
	if !strings.Contains(out, want) {
		t.Fatalf("tickets get lacks the parent line %q:\n%s", want, out)
	}
	if strings.Contains(out, "Under this ticket") || strings.Contains(out, "Under it:") {
		t.Fatalf("a ticket the platform counts no children under shows none:\n%s", out)
	}
	for _, filter := range mock.listFilters {
		if filter.ParentTicketID != "" {
			t.Fatalf("a ticket with sub_ticket_count 0 must not be read for children: %+v", mock.listFilters)
		}
	}
}

// A read that failed is not a ticket with no links or no children: the
// sections say they were not read, and the command exits 1.
func TestTicketsGetSaysWhatItCouldNotRead(t *testing.T) {
	mock := structuredRootMock()
	mock.linksError = errors.New("reading ticket links: unexpected status: 502 Bad Gateway")
	mock.childrenError = errors.New("listing tickets: unexpected status: 502 Bad Gateway")

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-42")
	if err == nil {
		t.Fatalf("a partial read must fail:\n%s", out)
	}
	if got := exitCodeFor(err); got != exitError {
		t.Errorf("exit code = %d, want %d", got, exitError)
	}
	for _, want := range []string{
		"Under this ticket: could not be read (listing tickets: unexpected status: 502 Bad Gateway)",
		"Links: could not be read (reading ticket links: unexpected status: 502 Bad Gateway)",
		"Ingress certificate expired",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tickets get lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "has no links") || !strings.Contains(err.Error(), "the tickets under it and its links could not be read") {
		t.Fatalf("an unread section must not read as empty: err=%v\n%s", err, out)
	}
}

func TestTicketsLinksSaysWhenThePlatformSentNoIncomingList(t *testing.T) {
	mock := structuredRootMock()
	mock.links[rootTicketID].IncomingLinks = nil

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsLinksCmd}, "tickets", "links", "T-42")
	if err != nil {
		t.Fatalf("tickets links failed: %v", err)
	}
	if !strings.Contains(out, "Recurrence of") ||
		!strings.Contains(out, "Links other tickets hold on this one were not reported by the platform.") {
		t.Fatalf("an absent incoming list must be said, not read as none:\n%s", out)
	}
}

func TestTicketsLinksJSONCarriesBothDirections(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsLinksCmd},
		"tickets", "links", "T-42", "-o", "json")
	if err != nil {
		t.Fatalf("tickets links -o json failed: %v", err)
	}
	for _, want := range []string{`"links"`, `"incoming_links"`, `"direction": "incoming"`, `"relation": "duplicate_of"`, `"ticket_number": 50`} {
		if !strings.Contains(out, want) {
			t.Fatalf("tickets links -o json lacks %s:\n%s", want, out)
		}
	}
}

func TestTicketsLinkSendsTheTargetAndRelation(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsLinkCmd},
		"tickets", "link", "T-51", "T-42", "--relation", "duplicate_of")
	if err != nil {
		t.Fatalf("tickets link failed: %v", err)
	}
	if len(mock.addedLinks) != 1 || mock.addedLinks[0] != (client.TicketLinkRequest{
		TargetKind: "ticket", TargetID: "T-42", Relation: "duplicate_of"}) {
		t.Fatalf("link sent = %+v", mock.addedLinks)
	}
	if !strings.Contains(out, `T-51: duplicate of T-42 "Ingress certificate expired"`) {
		t.Fatalf("link confirmation missing: %s", out)
	}
}

func TestTicketsUnlinkFindsTheLinkInEitherDirection(t *testing.T) {
	mock := structuredRootMock()

	// The link T-50 holds on T-42 is removed through T-50, its holder.
	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd}, "tickets", "unlink", "T-42", "T-50")
	if err != nil {
		t.Fatalf("tickets unlink failed: %v", err)
	}
	if len(mock.removedLinks) != 1 || mock.removedLinks[0] != holderTicketID+"/11111111-0000-4000-8000-000000000002" {
		t.Fatalf("removal must go to the holder: %+v", mock.removedLinks)
	}
	if !strings.Contains(out, `Removed from T-42: duplicate of this ticket T-50 "Certificate warning on prod".`) {
		t.Fatalf("unlink confirmation missing: %s", out)
	}

	resetTreeFlags(t, ticketsUnlinkCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd},
		"tickets", "unlink", "T-42", "11111111-0000-4000-8000-000000000001")
	if err != nil || len(mock.removedLinks) != 2 || mock.removedLinks[1] != rootTicketID+"/11111111-0000-4000-8000-000000000001" {
		t.Fatalf("unlink by link id: err=%v removed=%+v", err, mock.removedLinks)
	}
}

func TestTicketsUnlinkRefusesWhatItCannotPick(t *testing.T) {
	mock := structuredRootMock()
	list := mock.links[rootTicketID]
	list.Links = append(list.Links, ticketLink("11111111-0000-4000-8000-000000000003", rootTicketID, "outgoing",
		"relates_to", &client.TicketLinkedTicket{TicketNumber: 31, Title: "Ingress certificate expired", Status: "done"}))

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd}, "tickets", "unlink", "T-42", "T-31")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "recurrence_of, relates_to") {
		t.Fatalf("two links to one ticket need --relation: %v", err)
	}
	resetTreeFlags(t, ticketsUnlinkCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd}, "tickets", "unlink", "T-42", "T-9")
	if err == nil || exitCodeFor(err) != exitNotFound || !strings.Contains(err.Error(), "T-42 has no link with T-9") {
		t.Fatalf("no link with that ticket is not found: %v", err)
	}
	resetTreeFlags(t, ticketsUnlinkCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd},
		"tickets", "unlink", "T-42", "T-31", "--relation", "relates_to")
	if err != nil || len(mock.removedLinks) != 1 || !strings.HasSuffix(mock.removedLinks[0], "/11111111-0000-4000-8000-000000000003") {
		t.Fatalf("--relation picks the one link: err=%v removed=%+v", err, mock.removedLinks)
	}

	mock.linksError = errors.New("reading ticket links: unexpected status: 503 Service Unavailable")
	resetTreeFlags(t, ticketsUnlinkCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsUnlinkCmd}, "tickets", "unlink", "T-42", "T-50")
	if err == nil || !strings.Contains(err.Error(), "could not be read, so none was removed") || len(mock.removedLinks) != 1 {
		t.Fatalf("an unread link list must not remove anything: %v %+v", err, mock.removedLinks)
	}
}

func TestTicketsNestSendsTheRootAndSaysWhereTheTicketSits(t *testing.T) {
	mock := structuredRootMock()
	nested := childTicket(symptomTicketID, 51, "symptom", "blocked", "api pods restart on TLS errors")
	mock.nestResult = &nested

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsNestCmd},
		"tickets", "nest", "T-51", "--root", "T-43", "--reason", "  Same expired certificate ")
	if err != nil {
		t.Fatalf("tickets nest failed: %v", err)
	}
	if len(mock.nestRequests) != 1 || mock.nestRequests[0].RootTicketID != nil ||
		mock.nestRequests[0].RootTicketNumber == nil || *mock.nestRequests[0].RootTicketNumber != 43 ||
		mock.nestRequests[0].Reason == nil || *mock.nestRequests[0].Reason != "Same expired certificate" {
		t.Fatalf("nest sent = %+v", mock.nestRequests)
	}
	for _, want := range []string{
		`T-51 is a symptom of T-42 "Ingress certificate expired". The root carries the work.`,
		"T-43 is itself a symptom, so the ticket sits under its root, T-42.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("nest output lacks %q:\n%s", want, out)
		}
	}

	resetTreeFlags(t, ticketsNestCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsNestCmd},
		"tickets", "nest", "T-51", "--root", rootTicketID)
	if err != nil || mock.nestRequests[1].RootTicketID == nil || *mock.nestRequests[1].RootTicketID != rootTicketID ||
		mock.nestRequests[1].RootTicketNumber != nil {
		t.Fatalf("a UUID root goes as root_ticket_id: err=%v sent=%+v", err, mock.nestRequests)
	}
}

// Every refusal keeps the platform's sentence and exits by what would let
// it through: 2 for a request naming the wrong thing, 3 for a ticket not in
// the organisation, 1 for state.
func TestTicketsNestRefusalsKeepTheSentenceAndTheExitCode(t *testing.T) {
	for _, refusal := range []struct {
		status   int
		sentence string
		exitCode int
	}{
		{409, "T-42 is closed, so nothing can be nested under it.", exitError},
		{422, "A ticket cannot be nested under itself.", exitUsage},
		{400, "Name the root with exactly one of root_ticket_id or root_ticket_number.", exitUsage},
		{404, "Ticket not found.", exitNotFound},
	} {
		mock := structuredRootMock()
		mock.writeError = client.NewUnexpectedResponseError(refusal.status, refusal.sentence)
		resetTreeFlags(t, ticketsNestCmd)
		_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsNestCmd},
			"tickets", "nest", "T-51", "--root", "T-42")
		if err == nil || !strings.Contains(err.Error(), refusal.sentence) {
			t.Fatalf("%d: the platform's sentence must reach the user, got %v", refusal.status, err)
		}
		if got := exitCodeFor(err); got != refusal.exitCode {
			t.Errorf("%d: exit code = %d, want %d", refusal.status, got, refusal.exitCode)
		}
	}

	mock := structuredRootMock()
	resetTreeFlags(t, ticketsNestCmd)
	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsNestCmd}, "tickets", "nest", "T-51")
	if err == nil || exitCodeFor(err) != exitUsage || len(mock.nestRequests) != 0 {
		t.Fatalf("a nest without --root is refused before any call: %v %+v", err, mock.nestRequests)
	}
}

func TestTicketsDetachSaysWhetherAnythingChanged(t *testing.T) {
	mock := structuredRootMock()
	standing := childTicket(symptomTicketID, 51, "symptom", "blocked", "api pods restart on TLS errors")
	standing.ParentTicketID, standing.ParentTicketNumber, standing.ChildRole = nil, nil, nil
	mock.detachResult = &standing

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDetachCmd},
		"tickets", "detach", "T-51", "--reason", "Different cause")
	if err != nil || !strings.Contains(out, "T-51 is a top-level ticket again.") ||
		len(mock.detachReasons) != 1 || mock.detachReasons[0] != "Different cause" {
		t.Fatalf("detach: err=%v reasons=%+v out=%s", err, mock.detachReasons, out)
	}

	topLevel := rootTicket()
	mock.detachResult = &topLevel
	resetTreeFlags(t, ticketsDetachCmd)
	out, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDetachCmd}, "tickets", "detach", "T-42")
	if err != nil || !strings.Contains(out, "T-42 sits under no root; nothing changed.") {
		t.Fatalf("detaching a top-level ticket changes nothing: err=%v out=%s", err, out)
	}

	mock.writeError = client.NewUnexpectedResponseError(409, "T-53 is a sub-ticket; it stays with its parent.")
	resetTreeFlags(t, ticketsDetachCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsDetachCmd}, "tickets", "detach", "T-53")
	if err == nil || exitCodeFor(err) != exitError || !strings.Contains(err.Error(), "it stays with its parent") {
		t.Fatalf("a refused detach keeps the sentence: %v", err)
	}
}

func TestTicketsCloseAsDuplicateNamesTheKeeperThePlatformLinked(t *testing.T) {
	mock := structuredRootMock()
	mock.tickets[0] = childTicket(holderTicketID, 50, "symptom", "investigating", "Certificate warning on prod")
	keeper := ticketLink("22222222-0000-4000-8000-000000000001", holderTicketID, "outgoing", "duplicate_of",
		&client.TicketLinkedTicket{ID: rootTicketID, TicketNumber: 42, Title: "Ingress certificate expired", Status: "investigating"})
	mock.closeLinksKeeper = &keeper

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCloseCmd},
		"tickets", "close", "T-50", "--duplicate-of", "T-43", "--note", "Same cert")
	if err != nil {
		t.Fatalf("tickets close failed: %v\n%s", err, out)
	}
	if len(mock.closeRequests) != 1 || mock.closeRequests[0].Resolution != "duplicate" ||
		mock.closeRequests[0].DuplicateOf == nil || *mock.closeRequests[0].DuplicateOf != "T-43" ||
		mock.closeRequests[0].Note == nil || *mock.closeRequests[0].Note != "Same cert" {
		t.Fatalf("close sent = %+v", mock.closeRequests)
	}
	for _, want := range []string{
		"T-50 is done (duplicate).",
		`It is a duplicate of T-42 "Ingress certificate expired", which carries the work.`,
		"You named T-43; the platform linked its keeper, T-42.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("close output lacks %q:\n%s", want, out)
		}
	}
}

// A platform that ignores duplicate_of closes the ticket and writes no link:
// that is said, with the command that adds it, and the command fails.
func TestTicketsCloseAsDuplicateFailsWhenNoLinkWasWritten(t *testing.T) {
	mock := structuredRootMock()

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCloseCmd},
		"tickets", "close", "T-42", "--duplicate-of", "T-31")
	if err == nil || exitCodeFor(err) != exitError ||
		!strings.Contains(err.Error(), "recorded no link to T-31") ||
		!strings.Contains(err.Error(), "ankra tickets link T-42 T-31 --relation duplicate_of") {
		t.Fatalf("a duplicate close with no link must fail and say how to add it: %v", err)
	}
}

func TestTicketsCloseRefusesLocallyAndRelaysThePlatform(t *testing.T) {
	mock := structuredRootMock()

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCloseCmd},
		"tickets", "close", "T-42", "--resolution", "fixed", "--duplicate-of", "T-31")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--duplicate-of goes with --resolution duplicate") {
		t.Fatalf("--duplicate-of beside another resolution is a usage error: %v", err)
	}
	resetTreeFlags(t, ticketsCloseCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCloseCmd}, "tickets", "close", "T-42")
	if err == nil || exitCodeFor(err) != exitUsage || len(mock.closeRequests) != 0 {
		t.Fatalf("a close without a resolution is refused before any call: %v %+v", err, mock.closeRequests)
	}

	mock.writeError = client.NewUnexpectedResponseError(422, "T-31 is closed, so it cannot keep a duplicate.")
	resetTreeFlags(t, ticketsCloseCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCloseCmd},
		"tickets", "close", "T-42", "--duplicate-of", "T-31")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "T-31 is closed, so it cannot keep a duplicate.") {
		t.Fatalf("a refused duplicate keeps the platform's sentence: %v", err)
	}
}

func TestTicketsCreateFilesASubTicketUnderTheParent(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCreateCmd},
		"tickets", "create", "--title", "Renew the wildcard certificate", "--parent", "T-42", "--label", "cert")
	if err != nil {
		t.Fatalf("tickets create --parent failed: %v", err)
	}
	if len(mock.subTicketParents) != 1 || mock.subTicketParents[0] != rootTicketID ||
		mock.createRequests[0].Title != "Renew the wildcard certificate" || len(mock.createRequests[0].Labels) != 1 {
		t.Fatalf("sub-ticket sent = %+v under %+v", mock.createRequests, mock.subTicketParents)
	}
	if !strings.Contains(out, "Filed T-60: Renew the wildcard certificate") ||
		!strings.Contains(out, `It is a sub-ticket of T-42 "Ingress certificate expired".`) {
		t.Fatalf("create confirmation missing: %s", out)
	}

	resetTreeFlags(t, ticketsCreateCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCreateCmd},
		"tickets", "create", "--title", "x", "--parent", "T-42", "--staffing", "manual")
	if err == nil || exitCodeFor(err) != exitUsage || len(mock.createRequests) != 1 {
		t.Fatalf("--staffing with --parent is refused, not dropped: %v", err)
	}

	resetTreeFlags(t, ticketsCreateCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCreateCmd},
		"tickets", "create", "--title", "Rotate the robot token", "--staffing", "manual", "--kind", "chore")
	if err != nil || len(mock.createRequests) != 2 || len(mock.subTicketParents) != 1 ||
		mock.createRequests[1].Staffing == nil || *mock.createRequests[1].Staffing != "manual" {
		t.Fatalf("a top-level create: err=%v sent=%+v", err, mock.createRequests)
	}
}

func TestTicketsListUnderAParent(t *testing.T) {
	mock := structuredRootMock()

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsListCmd},
		"tickets", "list", "--parent", "T-42", "--include-closed")
	if err != nil {
		t.Fatalf("tickets list --parent failed: %v", err)
	}
	last := mock.listFilters[len(mock.listFilters)-1]
	if last.ParentTicketID != rootTicketID || !last.IncludeReceipts || !last.IncludeClosed {
		t.Fatalf("list --parent filter = %+v", last)
	}
	if !strings.Contains(out, "ROLE") || !strings.Contains(out, "sub-ticket") || !strings.Contains(out, "T-51") {
		t.Fatalf("list --parent lacks the role column: %s", out)
	}

	resetTreeFlags(t, ticketsListCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsListCmd},
		"tickets", "list", "--parent", "T-42", "--top-level")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("--parent with --top-level is a usage error: %v", err)
	}

	resetTreeFlags(t, ticketsListCmd)
	_, err = runConfirmCommand(t, mock, "", []*cobra.Command{ticketsListCmd}, "tickets", "list", "--top-level")
	if err != nil || !mock.listFilters[len(mock.listFilters)-1].TopLevelOnly {
		t.Fatalf("--top-level must reach the filter: %v %+v", err, mock.listFilters)
	}
}

// ankra-vwlr6g: the board leaves chat-fix receipts out unless asked, so a
// lookup by number that did not ask answered "not found" for every receipt.
func TestTicketReferenceFindsAChatFixReceiptByNumber(t *testing.T) {
	receipt := blockedTicketWithChoice()
	receipt.ID = "b0c1d2e3-0000-4000-8000-000000001298"
	receipt.TicketNumber = 1298
	receipt.Labels = []string{"chat-fix"}
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}, receipts: []client.Ticket{receipt}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsGetCmd}, "tickets", "get", "T-1298")
	if err != nil || !strings.Contains(out, "T-1298") {
		t.Fatalf("a receipt must resolve by number: err=%v out=%s", err, out)
	}
	if first := mock.listFilters[0]; first.Search != "T-1298" || !first.IncludeReceipts || !first.IncludeClosed {
		t.Fatalf("the lookup must search by number with receipts and closed tickets: %+v", first)
	}
}

// A platform that reads the search as text only still resolves the number
// by walking the listing, receipts included.
func TestTicketReferenceWalksTheListingWhenSearchIsTextOnly(t *testing.T) {
	receipt := blockedTicketWithChoice()
	receipt.ID = "b0c1d2e3-0000-4000-8000-000000001290"
	receipt.TicketNumber = 1290
	mock := &ticketsMock{tickets: []client.Ticket{blockedTicketWithChoice()}, receipts: []client.Ticket{receipt},
		textSearchOnly: true}

	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{ticketsCommentCmd},
		"tickets", "comment", "1290", "--body", "Seen.")
	if err != nil || len(mock.comments) != 1 {
		t.Fatalf("the walk must find the receipt: err=%v comments=%+v", err, mock.comments)
	}
	if len(mock.listFilters) < 2 || !mock.listFilters[1].IncludeReceipts || mock.listFilters[1].Search != "" {
		t.Fatalf("the walk must list receipts too: %+v", mock.listFilters)
	}
}
