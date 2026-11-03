package cmd

// The board's structure from the terminal (ankra-us966.4, workstream W9 of
// the 2026-11-02 ticket-structure design): a ticket's parent and what it is
// to that parent, the tickets under it, its links in both directions, and
// the writes that change them (link, unlink, nest, detach). Close as a
// duplicate naming the keeper is in tickets_close.go.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var ticketsLinksCmd = &cobra.Command{
	Use:   "links <ticket>",
	Short: "List a ticket's links in both directions",
	Long: `List the links a ticket holds and the ticket links other tickets hold on
it, each with the ticket at the other end: its number, title and status.

The relation reads from the ticket you named: "Duplicate of T-42" is a link
this ticket holds, "Duplicate of this ticket T-51" is one T-51 holds on it.
Relations are spawned, evidence, fix, duplicate_of, relates_to and
recurrence_of. The LINK column is the id "ankra tickets unlink" takes.

With -o json the platform's answer is printed as sent: {"links": [...],
"incoming_links": [...]}, each entry with id, ticket_id (the ticket that
holds the link), target_kind, target_id, relation, created_at, direction
("outgoing" or "incoming") and linked_ticket (id, ticket_number, title,
status and resolution of the ticket at the other end, or null for a target
that is not a ticket of the organisation).`,
	Example: `  ankra tickets links T-42
  ankra tickets links T-42 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		links, err := apiClient.ListTicketLinks(ticket.ID)
		if err != nil {
			return fmt.Errorf("the links of T-%d could not be read: %w", ticket.TicketNumber, err)
		}
		if handled, renderError := renderStructured(cmd, links); handled || renderError != nil {
			return renderError
		}
		renderTicketLinkTable(cmd.OutOrStdout(), ticket.TicketNumber, *links)
		return nil
	},
}

var ticketsLinkCmd = &cobra.Command{
	Use:   "link <ticket> <target>",
	Short: "Link a ticket to another ticket or record",
	Long: `Link a ticket to another ticket (by number, T-42, or id) or, with --kind, to
another record. The relation defaults to relates_to; the others are
spawned, evidence, fix, duplicate_of and recurrence_of (an earlier ticket of
the same recurring signal, tickets only). Linking what is already linked
answers with the existing link.

To close a ticket as a duplicate, use "ankra tickets close --duplicate-of",
which closes it and links it to the keeper in one step.`,
	Example: `  ankra tickets link T-50 T-42
  ankra tickets link T-50 T-31 --relation recurrence_of
  ankra tickets link T-50 https://github.com/acme/app/pull/12 --kind merge_request --relation fix`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, _ := cmd.Flags().GetString("kind")
		relation, _ := cmd.Flags().GetString("relation")
		kind = strings.TrimSpace(kind)
		target := strings.TrimSpace(args[1])
		if kind == "" || target == "" {
			return withExitCode(exitUsage, errors.New("name the target and its --kind"))
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		link, err := apiClient.AddTicketLink(ticket.ID, client.TicketLinkRequest{
			TargetKind: kind, TargetID: target, Relation: strings.TrimSpace(relation),
		})
		if err != nil {
			return ticketWriteError(err)
		}
		if handled, renderError := renderStructured(cmd, link); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "T-%d: %s %s (link %s).\n", ticket.TicketNumber,
			strings.ToLower(ticketLinkRelationLabel(*link)), ticketLinkOtherEnd(*link), link.ID)
		return nil
	},
}

var ticketsUnlinkCmd = &cobra.Command{
	Use:   "unlink <ticket> <link-id|ticket>",
	Short: "Remove a link from a ticket",
	Long: `Remove one link from a ticket, named by its link id (see "ankra tickets
links") or by the ticket at its other end. A link another ticket holds on
this one is removed too: the CLI sends the removal to the ticket that holds
it. When two tickets are linked more than once, name the one to remove with
--relation. The removal is recorded on the ticket's timeline.`,
	Example: `  ankra tickets unlink T-50 T-42
  ankra tickets unlink T-50 T-42 --relation relates_to
  ankra tickets unlink T-50 4f1c2d0e-8a6b-4c1e-9b7a-2d4e6f8a0b1c`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		relation, _ := cmd.Flags().GetString("relation")
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		links, err := apiClient.ListTicketLinks(ticket.ID)
		if err != nil {
			return fmt.Errorf("the links of T-%d could not be read, so none was removed: %w", ticket.TicketNumber, err)
		}
		link, err := findTicketLink(*links, ticket.TicketNumber, args[1], strings.TrimSpace(relation))
		if err != nil {
			return err
		}
		if err := apiClient.RemoveTicketLink(link.TicketID, link.ID); err != nil {
			return ticketWriteError(err)
		}
		if handled, renderError := renderStructured(cmd, link); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed from T-%d: %s %s.\n", ticket.TicketNumber,
			strings.ToLower(ticketLinkRelationLabel(*link)), ticketLinkOtherEnd(*link))
		return nil
	},
}

var ticketsNestCmd = &cobra.Command{
	Use:   "nest <ticket> --root <ticket>",
	Short: "Move a ticket under a root ticket as a symptom",
	Long: `Move an existing ticket under a root ticket as a symptom. The root carries
the work: the symptom gets no agent run or thread of its own, and it is
settled or stands on its own again when the root closes. A ticket a person
owes an answer on keeps its question. Naming a symptom as the root nests
under that symptom's own root. Nesting a ticket that is already a symptom
of the root changes nothing.

The platform refuses, with a sentence saying why, a root that is the ticket
itself or does not exist (exit 2), and a closed ticket or root, a sub-ticket
on either side, a ticket that has sub-tickets of its own, a loop, or held
work (exit 1). A concurrent change is refused the same way: read the ticket
again and retry.`,
	Example: `  ankra tickets nest T-51 --root T-42
  ankra tickets nest T-51 --root T-42 --reason "Same expired certificate"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rootReference, _ := cmd.Flags().GetString("root")
		reason, _ := cmd.Flags().GetString("reason")
		request, err := ticketNestRequest(rootReference, reason)
		if err != nil {
			return err
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		nested, err := apiClient.NestTicket(ticket.ID, request)
		if err != nil {
			return ticketWriteError(err)
		}
		if handled, renderError := renderStructured(cmd, nested); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		root := ticketParentLabel(*nested)
		_, _ = fmt.Fprintf(out, "T-%d is a symptom of %s. The root carries the work.\n", nested.TicketNumber, root)
		if nested.ParentTicketNumber != nil {
			switch {
			case request.RootTicketNumber != nil && *request.RootTicketNumber != *nested.ParentTicketNumber:
				_, _ = fmt.Fprintf(out, "T-%d is itself a symptom, so the ticket sits under its root, T-%d.\n",
					*request.RootTicketNumber, *nested.ParentTicketNumber)
			case request.RootTicketID != nil && nested.ParentTicketID != nil &&
				!strings.EqualFold(*request.RootTicketID, *nested.ParentTicketID):
				_, _ = fmt.Fprintf(out, "The root you named is itself a symptom, so the ticket sits under its root, T-%d.\n",
					*nested.ParentTicketNumber)
			}
		}
		return nil
	},
}

var ticketsDetachCmd = &cobra.Command{
	Use:   "detach <ticket>",
	Short: "Make a symptom a top-level ticket again",
	Long: `Make a symptom a top-level ticket again, with an event on its timeline and
on its root's. Its status and any question it carries stay as they are, and
it is worked like any other ticket from then on. A ticket that sits under no
root is left unchanged. A sub-ticket is refused: it belongs to its parent.`,
	Example: `  ankra tickets detach T-51
  ankra tickets detach T-51 --reason "Different cause after all"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		detached, err := apiClient.DetachTicket(ticket.ID, strings.TrimSpace(reason))
		if err != nil {
			return ticketWriteError(err)
		}
		if handled, renderError := renderStructured(cmd, detached); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		switch {
		case ticket.ParentTicketID == nil && detached.ParentTicketID == nil:
			_, _ = fmt.Fprintf(out, "T-%d sits under no root; nothing changed.\n", detached.TicketNumber)
		case detached.ParentTicketID == nil:
			_, _ = fmt.Fprintf(out, "T-%d is a top-level ticket again.\n", detached.TicketNumber)
		default:
			_, _ = fmt.Fprintf(out, "T-%d is still under %s.\n", detached.TicketNumber, ticketParentLabel(*detached))
		}
		return nil
	},
}

// ticketWriteError keeps the platform's sentence for a refused ticket write
// and classifies it by what would let it through. 400 and 422 mean the
// request named the wrong thing (a root that is the ticket itself or does
// not exist, both roots or neither, a reason that is too long, a
// duplicate_of with another resolution or naming no open ticket): exit 2.
// 404 means the ticket is not in the organisation: exit 3, as every
// command's 404. 409 means the tickets' state refused it (a closed ticket,
// a sub-ticket, a loop, held work, a concurrent change): exit 1. Nothing is
// written in any of them.
func ticketWriteError(err error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(err, &unexpected) &&
		(unexpected.StatusCode == http.StatusBadRequest || unexpected.StatusCode == http.StatusUnprocessableEntity) {
		return withExitCode(exitUsage, err)
	}
	return err
}

// parseTicketNumber reads a ticket number written as 8, T-8 or #8.
func parseTicketNumber(reference string) (int64, bool) {
	numberText := strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(reference)), "T-"), "#")
	number, parseError := strconv.ParseInt(numberText, 10, 64)
	return number, parseError == nil && number >= 1
}

// ticketNestRequest builds the nest body from --root: an id goes as
// root_ticket_id and a number as root_ticket_number, so the platform
// resolves the root itself and refuses one that does not exist.
func ticketNestRequest(rootReference string, reason string) (client.TicketNestRequest, error) {
	request := client.TicketNestRequest{}
	root := strings.TrimSpace(rootReference)
	switch {
	case root == "":
		return request, withExitCode(exitUsage, errors.New("--root is required: the ticket to nest under"))
	case ticketUUIDPattern.MatchString(root):
		request.RootTicketID = &root
	default:
		number, isNumber := parseTicketNumber(root)
		if !isNumber {
			return request, withExitCode(exitUsage, fmt.Errorf(
				"'%s' is not a ticket reference; use the root's number (42, T-42) or its UUID", rootReference))
		}
		request.RootTicketNumber = &number
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		request.Reason = &trimmed
	}
	return request, nil
}

// findTicketLink picks the link "unlink" removes: the one with that link id,
// or the one whose other end is that ticket, narrowed by relation. Both
// directions are searched, because a link another ticket holds on this one
// is removed through its holder.
func findTicketLink(links client.TicketLinkList, ticketNumber int64, reference string, relation string) (*client.TicketLink, error) {
	all := append(append([]client.TicketLink{}, links.Links...), links.IncomingLinks...)
	wanted := strings.TrimSpace(reference)
	for index := range all {
		if strings.EqualFold(all[index].ID, wanted) {
			return &all[index], nil
		}
	}
	number, isNumber := parseTicketNumber(wanted)
	isUUID := ticketUUIDPattern.MatchString(wanted)
	if !isNumber && !isUUID {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"'%s' is not a link id or a ticket reference; see the LINK column of: ankra tickets links T-%d",
			reference, ticketNumber))
	}
	matches := []client.TicketLink{}
	for _, link := range all {
		other := link.LinkedTicket
		if other == nil {
			continue
		}
		if (isNumber && other.TicketNumber == number) || (isUUID && strings.EqualFold(other.ID, wanted)) {
			if relation == "" || link.Relation == relation {
				matches = append(matches, link)
			}
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		if relation != "" {
			return nil, withExitCode(exitNotFound, fmt.Errorf("ticket T-%d has no %s link with %s", ticketNumber, relation, reference))
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf("ticket T-%d has no link with %s", ticketNumber, reference))
	}
	relations := make([]string, 0, len(matches))
	for _, match := range matches {
		relations = append(relations, match.Relation)
	}
	return nil, withExitCode(exitUsage, fmt.Errorf(
		"ticket T-%d is linked to %s %d times (%s); name the one to remove with --relation or by its link id",
		ticketNumber, reference, len(matches), strings.Join(relations, ", ")))
}

// ticketLinkRelationLabel words a link as it reads from the ticket being
// viewed, the same words the board uses. Only duplicate_of, recurrence_of
// and relates_to get wording of their own: spawned, evidence and fix are
// written in both senses by different writers, so they keep the relation's
// own word, and from the other end say only that the other ticket links
// here with it.
func ticketLinkRelationLabel(link client.TicketLink) string {
	if link.Direction == "incoming" {
		switch link.Relation {
		case "duplicate_of":
			return "Duplicate of this ticket"
		case "recurrence_of":
			return "Came back as"
		case "relates_to":
			return "Related to"
		default:
			return "Links here as " + strings.ToLower(relationWords(link.Relation))
		}
	}
	switch link.Relation {
	case "duplicate_of":
		return "Duplicate of"
	case "recurrence_of":
		return "Recurrence of"
	case "relates_to":
		return "Related to"
	default:
		return relationWords(link.Relation)
	}
}

// relationWords turns a relation the CLI has no wording for into words, so
// a relation added later still reads rather than vanishing.
func relationWords(relation string) string {
	words := strings.TrimSpace(strings.ReplaceAll(relation, "_", " "))
	if words == "" {
		return "Linked"
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// ticketLinkOtherEnd names what is at the other end of a link: the ticket
// with its number and title, or the record's kind and id.
func ticketLinkOtherEnd(link client.TicketLink) string {
	if other := link.LinkedTicket; other != nil {
		return fmt.Sprintf("T-%d %q", other.TicketNumber, other.Title)
	}
	if link.TargetKind == "ticket" {
		return "ticket " + ticketLinkOtherID(link) + " (not a ticket of this organisation)"
	}
	return link.TargetKind + " " + link.TargetID
}

// ticketLinkOtherID is the id at the other end: the target of a link the
// ticket holds, the holder of one it receives.
func ticketLinkOtherID(link client.TicketLink) string {
	if link.Direction == "incoming" {
		return link.TicketID
	}
	return link.TargetID
}

func linkedTicketState(other client.TicketLinkedTicket) string {
	if other.Resolution != nil && *other.Resolution != "" {
		return other.Status + ", " + *other.Resolution
	}
	return other.Status
}

// renderTicketLinkTable prints both directions of a ticket's links. A
// platform that did not send the incoming list is said to have not
// reported it, so its absence never reads as "no other ticket links here".
func renderTicketLinkTable(out io.Writer, ticketNumber int64, links client.TicketLinkList) {
	all := append(append([]client.TicketLink{}, links.Links...), links.IncomingLinks...)
	if len(all) == 0 {
		if links.IncomingLinks == nil {
			_, _ = fmt.Fprintf(out, "T-%d holds no links. Links other tickets hold on it were not reported by the platform.\n", ticketNumber)
			return
		}
		_, _ = fmt.Fprintf(out, "T-%d has no links.\n", ticketNumber)
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.AppendHeader(table.Row{"RELATION", "WITH", "TITLE", "STATUS", "LINK"})
	for _, link := range all {
		with, title, state := link.TargetKind, link.TargetID, ""
		if other := link.LinkedTicket; other != nil {
			with = "T-" + strconv.FormatInt(other.TicketNumber, 10)
			title = other.Title
			state = linkedTicketState(*other)
		} else if link.TargetKind == "ticket" {
			title = ticketLinkOtherID(link) + " (not a ticket of this organisation)"
		}
		writer.AppendRow(table.Row{ticketLinkRelationLabel(link), with, truncateCell(title, 50), state, link.ID})
	}
	writer.Render()
	if links.IncomingLinks == nil {
		_, _ = fmt.Fprintln(out, "Links other tickets hold on this one were not reported by the platform.")
	}
}

// ticketParentLabel names a ticket's parent by number and title, falling
// back to its id when the platform sent no number.
func ticketParentLabel(ticket client.Ticket) string {
	label := "its root"
	switch {
	case ticket.ParentTicketNumber != nil:
		label = "T-" + strconv.FormatInt(*ticket.ParentTicketNumber, 10)
	case ticket.ParentTicketID != nil:
		label = *ticket.ParentTicketID
	}
	if ticket.ParentTicketTitle != nil && *ticket.ParentTicketTitle != "" {
		label += fmt.Sprintf(" %q", *ticket.ParentTicketTitle)
	}
	return label
}

// ticketChildRoleLabel is the role column of a child: what it is to the
// ticket above it.
func ticketChildRoleLabel(ticket client.Ticket) string {
	if ticket.ChildRole == nil {
		return "-"
	}
	if *ticket.ChildRole == "subtask" {
		return "sub-ticket"
	}
	return *ticket.ChildRole
}

// renderTicketParentLine prints where a ticket sits: under which ticket,
// and whether that ticket carries its work. Nothing is printed for a
// top-level ticket.
func renderTicketParentLine(out io.Writer, ticket client.Ticket) {
	if ticket.ParentTicketID == nil && ticket.ParentTicketNumber == nil {
		return
	}
	parent := ticketParentLabel(ticket)
	if ticket.ParentTicketStatus != nil && *ticket.ParentTicketStatus != "" {
		parent += " (" + *ticket.ParentTicketStatus + ")"
	}
	switch {
	case ticket.ChildRole != nil && *ticket.ChildRole == "symptom":
		_, _ = fmt.Fprintf(out, "Parent:    %s. This ticket is a symptom: the root carries the work.\n", parent)
	case ticket.ChildRole != nil && *ticket.ChildRole == "subtask":
		_, _ = fmt.Fprintf(out, "Parent:    %s. This ticket is a sub-ticket of it.\n", parent)
	default:
		_, _ = fmt.Fprintf(out, "Parent:    %s\n", parent)
	}
}

// renderTicketChildCounts prints the platform's counts of what is open
// under a ticket. A count the platform did not send is left out rather
// than shown as zero.
func renderTicketChildCounts(out io.Writer, ticket client.Ticket) {
	parts := []string{}
	if ticket.LiveSymptomCount != nil && *ticket.LiveSymptomCount > 0 {
		part := countedNoun(*ticket.LiveSymptomCount, "open symptom", "open symptoms")
		if ticket.SymptomsNeedingPersonCount != nil && *ticket.SymptomsNeedingPersonCount > 0 {
			part += fmt.Sprintf(" (%d waiting on a person)", *ticket.SymptomsNeedingPersonCount)
		}
		parts = append(parts, part)
	}
	// open_sub_ticket_count counts every open child, symptoms included.
	if ticket.OpenSubTicketCount != nil && ticket.LiveSymptomCount != nil {
		if openSubTickets := *ticket.OpenSubTicketCount - *ticket.LiveSymptomCount; openSubTickets > 0 {
			parts = append(parts, countedNoun(openSubTickets, "open sub-ticket", "open sub-tickets"))
		}
	}
	if len(parts) > 0 {
		_, _ = fmt.Fprintf(out, "Under it:  %s\n", strings.Join(parts, ", "))
	}
}

func countedNoun(count int, one string, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return strconv.Itoa(count) + " " + many
}

// renderTicketChildren lists every ticket directly under this one, closed
// and receipts included, and reports false when the list could not be
// read: the section then says so instead of showing nothing. A ticket the
// platform counts no children under is not read again.
func renderTicketChildren(out io.Writer, ticket client.Ticket) bool {
	if ticket.SubTicketCount != nil && *ticket.SubTicketCount == 0 {
		return true
	}
	page, err := apiClient.ListTickets(client.TicketListFilter{
		ParentTicketID: ticket.ID, IncludeClosed: true, IncludeReceipts: true, Limit: 200,
	})
	if err == nil && page.Tickets == nil {
		err = errors.New("the platform's answer carried no tickets list")
	}
	if err != nil {
		_, _ = fmt.Fprintf(out, "\nUnder this ticket: could not be read (%v)\n", err)
		return false
	}
	if len(page.Tickets) == 0 {
		return true
	}
	_, _ = fmt.Fprintln(out, "\nUnder this ticket:")
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.AppendHeader(table.Row{"TICKET", "ROLE", "STATUS", "TITLE"})
	for _, child := range page.Tickets {
		writer.AppendRow(table.Row{"T-" + strconv.FormatInt(child.TicketNumber, 10),
			ticketChildRoleLabel(child), child.Status, truncateCell(child.Title, 60)})
	}
	writer.Render()
	if page.TotalCount > len(page.Tickets) {
		_, _ = fmt.Fprintf(out, "Showing %d of %d; list them with: ankra tickets list --parent T-%d --include-closed\n",
			len(page.Tickets), page.TotalCount, ticket.TicketNumber)
	}
	return true
}

// renderTicketLinksSection is the links part of "tickets get"; false means
// they could not be read, which the section says.
func renderTicketLinksSection(out io.Writer, ticket client.Ticket) bool {
	links, err := apiClient.ListTicketLinks(ticket.ID)
	if err != nil {
		_, _ = fmt.Fprintf(out, "\nLinks: could not be read (%v)\n", err)
		return false
	}
	_, _ = fmt.Fprintln(out)
	if len(links.Links)+len(links.IncomingLinks) > 0 {
		_, _ = fmt.Fprintln(out, "Links:")
	}
	renderTicketLinkTable(out, ticket.TicketNumber, *links)
	return true
}

func init() {
	ticketsLinkCmd.Flags().String("relation", "",
		"spawned, evidence, fix, duplicate_of, relates_to (the default) or recurrence_of")
	ticketsLinkCmd.Flags().String("kind", "ticket",
		"What the target is: ticket, or another record kind such as merge_request, agent_run or session")
	ticketsUnlinkCmd.Flags().String("relation", "", "Remove only the link with this relation")
	ticketsNestCmd.Flags().String("root", "", "The root ticket to nest under: its number (42, T-42) or UUID")
	ticketsNestCmd.Flags().String("reason", "", "Why this ticket is a symptom of the root, shown on both timelines (up to 1000 characters)")
	ticketsDetachCmd.Flags().String("reason", "", "Why the ticket stands on its own again, shown on both timelines (up to 1000 characters)")
	registerStructuredOutputFlags(ticketsLinksCmd, ticketsLinkCmd, ticketsUnlinkCmd, ticketsNestCmd, ticketsDetachCmd)

	ticketsCmd.AddCommand(ticketsLinksCmd)
	ticketsCmd.AddCommand(ticketsLinkCmd)
	ticketsCmd.AddCommand(ticketsUnlinkCmd)
	ticketsCmd.AddCommand(ticketsNestCmd)
	ticketsCmd.AddCommand(ticketsDetachCmd)
}
