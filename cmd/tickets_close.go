package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var ticketsCloseCmd = &cobra.Command{
	Use:   "close <ticket>",
	Short: "Close a ticket with a resolution, or as a duplicate naming the keeper",
	Long: `Close a ticket with a resolution: fixed, no_action, duplicate, wont_fix,
escalated, unverifiable, cleared or expired. A close as fixed can name what
confirmed the fix with --evidence-kind (probe, agent_run, automation_run,
observation, link or human_attestation) and --evidence-reference; without
them you are recorded as the evidence.

--duplicate-of names the open ticket that covers the same cause, by number
(42, T-42) or id, and implies --resolution duplicate. The platform links the
closed ticket to that ticket's canonical keeper and tells the keeper on its
timeline; the CLI then reads the link back and names the keeper. When
nothing was written, the platform says why: another resolution, a reference
that is not a ticket, an unknown number, the ticket itself, another
organisation's ticket, a loop, or a keeper that is closed (exit 2). A ticket
that cannot be closed from its current state is refused with exit 1.`,
	Example: `  ankra tickets close T-50 --duplicate-of T-42
  ankra tickets close T-50 --resolution fixed --note "Renewed the certificate"
  ankra tickets close T-50 --resolution fixed --evidence-kind probe --evidence-reference "https://status.example.com/check/42"
  ankra tickets close T-50 --resolution no_action --note "Expected during the maintenance window"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolution, _ := cmd.Flags().GetString("resolution")
		note, _ := cmd.Flags().GetString("note")
		duplicateOf, _ := cmd.Flags().GetString("duplicate-of")
		evidenceKind, _ := cmd.Flags().GetString("evidence-kind")
		evidenceReference, _ := cmd.Flags().GetString("evidence-reference")
		request, err := ticketCloseRequest(resolution, note, duplicateOf, evidenceKind, evidenceReference)
		if err != nil {
			return err
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		closed, err := apiClient.CloseTicket(ticket.ID, request)
		if err != nil {
			return ticketWriteError(err)
		}
		var keeper *client.TicketLink
		var keeperReadError error
		if request.DuplicateOf != nil {
			keeper, keeperReadError = readDuplicateKeeper(ticket.ID)
		}
		handled, renderError := renderStructured(cmd, closed)
		if renderError != nil {
			return renderError
		}
		if !handled {
			resolved := request.Resolution
			if closed.Resolution != nil && *closed.Resolution != "" {
				resolved = *closed.Resolution
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "T-%d is %s (%s).\n", closed.TicketNumber, closed.Status, resolved)
		}
		if request.DuplicateOf == nil {
			return nil
		}
		return reportDuplicateKeeper(cmd, handled, closed.TicketNumber, *request.DuplicateOf, keeper, keeperReadError)
	},
}

// ticketCloseRequest builds the close body. --duplicate-of implies the
// duplicate resolution and is refused beside any other, as the platform
// would refuse it, before anything is read.
func ticketCloseRequest(resolution, note, duplicateOf, evidenceKind, evidenceReference string) (client.TicketCloseRequest, error) {
	request := client.TicketCloseRequest{Resolution: strings.TrimSpace(resolution)}
	if keeper := strings.TrimSpace(duplicateOf); keeper != "" {
		if request.Resolution == "" {
			request.Resolution = "duplicate"
		}
		if request.Resolution != "duplicate" {
			return request, withExitCode(exitUsage, fmt.Errorf(
				"--duplicate-of goes with --resolution duplicate, not %s", request.Resolution))
		}
		request.DuplicateOf = &keeper
	}
	if request.Resolution == "" {
		return request, withExitCode(exitUsage, errors.New(
			"--resolution is required (fixed, no_action, duplicate, wont_fix, escalated, unverifiable, cleared or expired), or name the keeper with --duplicate-of"))
	}
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		request.Note = &trimmed
	}
	if trimmed := strings.TrimSpace(evidenceKind); trimmed != "" {
		request.EvidenceKind = &trimmed
	}
	if trimmed := strings.TrimSpace(evidenceReference); trimmed != "" {
		request.EvidenceReference = &trimmed
	}
	return request, nil
}

// readDuplicateKeeper reads back the duplicate_of link a close wrote. nil
// with no error means the read succeeded and found none.
func readDuplicateKeeper(ticketID string) (*client.TicketLink, error) {
	links, err := apiClient.ListTicketLinks(ticketID)
	if err != nil {
		return nil, err
	}
	var keeper *client.TicketLink
	for index := range links.Links {
		if links.Links[index].Relation == "duplicate_of" {
			keeper = &links.Links[index]
		}
	}
	return keeper, nil
}

// reportDuplicateKeeper names the keeper the closed ticket now points at.
// The platform resolves the named ticket to its canonical keeper, so the
// two can differ, and that is said. A close that wrote no link (a platform
// that predates duplicate_of ignores the field) fails with the command that
// adds it; a read-back that failed says the link is unconfirmed, never that
// it is missing.
func reportDuplicateKeeper(cmd *cobra.Command, structured bool, ticketNumber int64, named string,
	keeper *client.TicketLink, readError error) error {
	errOut := cmd.ErrOrStderr()
	if readError != nil {
		_, _ = fmt.Fprintf(errOut, "The link to the keeper could not be read back (%v); check it with: ankra tickets links T-%d\n",
			readError, ticketNumber)
		return nil
	}
	if keeper == nil {
		return fmt.Errorf("T-%d was closed as a duplicate, but the platform recorded no link to %s; "+
			"it may predate duplicate_of. Link it with: ankra tickets link T-%d %s --relation duplicate_of",
			ticketNumber, named, ticketNumber, named)
	}
	message := fmt.Sprintf("It is a duplicate of %s, which carries the work.", ticketLinkOtherEnd(*keeper))
	if other := keeper.LinkedTicket; other != nil {
		if number, isNumber := parseTicketNumber(named); isNumber && number != other.TicketNumber {
			message += fmt.Sprintf(" You named T-%d; the platform linked its keeper, T-%d.", number, other.TicketNumber)
		}
	}
	if structured {
		_, _ = fmt.Fprintln(errOut, message)
		return nil
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), message)
	return nil
}

func init() {
	ticketsCloseCmd.Flags().String("resolution", "",
		"fixed, no_action, duplicate, wont_fix, escalated, unverifiable, cleared or expired (duplicate when --duplicate-of is given)")
	ticketsCloseCmd.Flags().String("duplicate-of", "",
		"The open ticket covering the same cause (42, T-42 or its UUID); implies --resolution duplicate")
	ticketsCloseCmd.Flags().String("note", "", "Why the ticket is closed, shown on its timeline")
	ticketsCloseCmd.Flags().String("evidence-kind", "",
		"With fixed: what confirmed the fix (probe, agent_run, automation_run, observation, link, human_attestation)")
	ticketsCloseCmd.Flags().String("evidence-reference", "",
		"With fixed: where that evidence is, such as a run id or URL (up to 1000 characters)")
	registerStructuredOutputFlags(ticketsCloseCmd)
	ticketsCmd.AddCommand(ticketsCloseCmd)
}
