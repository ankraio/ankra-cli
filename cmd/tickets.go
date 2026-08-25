package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var ticketsCmd = &cobra.Command{
	Use:   "tickets",
	Short: "Work the AI board: list tickets, read one, comment, move it, answer a decision",
	Long: `Work the organisation's AI board from the terminal.

The AI board is where Ankra's agents track incidents, insights and requests
as tickets. These commands list the board, show one ticket with its
timeline, comment on it, move it through its lifecycle, and answer the
decision a blocked ticket is waiting on - the same choice the ticket page
renders as options plus "something else".

A ticket is referenced by its number (8, T-8 or #8) or its UUID.`,
}

var ticketsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the board's tickets, newest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		statuses, _ := cmd.Flags().GetStringSlice("status")
		needsHuman, _ := cmd.Flags().GetBool("needs-human")
		includeClosed, _ := cmd.Flags().GetBool("include-closed")
		search, _ := cmd.Flags().GetString("search")
		limit, _ := cmd.Flags().GetInt("limit")

		response, err := apiClient.ListTickets(client.TicketListFilter{
			Statuses: statuses, NeedsHuman: needsHuman, IncludeClosed: includeClosed,
			Search: search, Limit: limit,
		})
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, response); handled || renderError != nil {
			return renderError
		}
		if len(response.Tickets) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No tickets found.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(cmd.OutOrStdout())
		writer.AppendHeader(table.Row{"TICKET", "TITLE", "STATUS", "PRIORITY", "ASSIGNEE", "WAITING ON"})
		for _, ticket := range response.Tickets {
			writer.AppendRow(table.Row{
				"T-" + strconv.FormatInt(ticket.TicketNumber, 10),
				truncateCell(ticket.Title, 60),
				ticket.Status, ticket.Priority,
				ticketAssigneeLabel(ticket), ticketWaitingLabel(ticket),
			})
		}
		writer.Render()
		if response.TotalCount > len(response.Tickets) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Showing %d of %d tickets; raise --limit to see more.\n",
				len(response.Tickets), response.TotalCount)
		}
		return nil
	},
}

var ticketsGetCmd = &cobra.Command{
	Use:   "get <ticket>",
	Short: "Show one ticket, including the decision it is waiting on",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, ticket); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Ticket:    T-%d  %s\n", ticket.TicketNumber, ticket.Title)
		_, _ = fmt.Fprintf(out, "ID:        %s\n", ticket.ID)
		_, _ = fmt.Fprintf(out, "Status:    %s (%s, %s)\n", ticket.Status, ticket.Kind, ticket.Priority)
		_, _ = fmt.Fprintf(out, "Assignee:  %s\n", ticketAssigneeLabel(*ticket))
		if ticket.ClusterName != nil {
			_, _ = fmt.Fprintf(out, "Cluster:   %s\n", *ticket.ClusterName)
		}
		if ticket.PlanStatus != "" && ticket.PlanStatus != "none" {
			_, _ = fmt.Fprintf(out, "Plan:      %s\n", ticket.PlanStatus)
		}
		if len(ticket.Labels) > 0 {
			_, _ = fmt.Fprintf(out, "Labels:    %s\n", strings.Join(ticket.Labels, ", "))
		}
		if ticket.Resolution != nil {
			_, _ = fmt.Fprintf(out, "Resolved:  %s\n", *ticket.Resolution)
		}
		_, _ = fmt.Fprintf(out, "Updated:   %s\n", ticket.UpdatedAt)
		if strings.TrimSpace(ticket.Body) != "" {
			_, _ = fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(ticket.Body))
		}
		renderTicketDecision(cmd, *ticket)
		return nil
	},
}

var ticketsEventsCmd = &cobra.Command{
	Use:   "events <ticket>",
	Short: "Show a ticket's timeline, oldest first",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		response, err := apiClient.ListTicketEvents(ticket.ID, limit)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, response); handled || renderError != nil {
			return renderError
		}
		if len(response.Events) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No events yet.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(cmd.OutOrStdout())
		writer.AppendHeader(table.Row{"#", "AT", "EVENT", "AUTHOR", "BODY"})
		for _, event := range response.Events {
			writer.AppendRow(table.Row{event.Sequence, event.CreatedAt, event.EventType,
				ticketEventAuthorLabel(event), truncateCell(strings.ReplaceAll(event.Body, "\n", " "), 80)})
		}
		writer.Render()
		return nil
	},
}

var ticketsCommentCmd = &cobra.Command{
	Use:   "comment <ticket>",
	Short: "Post a comment on a ticket",
	Long: `Post a comment on a ticket as yourself. A comment on a blocked ticket
hands it back to its agent; to answer a choice the agent offered, use
"ankra tickets decide" instead so the answer is recorded as the decision.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, _ := cmd.Flags().GetString("body")
		if strings.TrimSpace(body) == "" {
			return withExitCode(exitUsage, fmt.Errorf("--body is required"))
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		event, err := apiClient.CommentOnTicket(ticket.ID, strings.TrimSpace(body))
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, event); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Commented on T-%d (event #%d).\n", ticket.TicketNumber, event.Sequence)
		return nil
	},
}

var ticketsTransitionCmd = &cobra.Command{
	Use:   "transition <ticket>",
	Short: "Move a ticket to another status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetString("status")
		note, _ := cmd.Flags().GetString("note")
		if strings.TrimSpace(status) == "" {
			return withExitCode(exitUsage, fmt.Errorf("--status is required"))
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		moved, err := apiClient.TransitionTicket(ticket.ID, strings.TrimSpace(status), strings.TrimSpace(note))
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, moved); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "T-%d is now %s.\n", moved.TicketNumber, moved.Status)
		return nil
	},
}

var ticketsDecideCmd = &cobra.Command{
	Use:   "decide <ticket>",
	Short: "Answer the choice a blocked ticket is waiting on",
	Long: `Answer the decision a blocked ticket is waiting on. Pick one of the
options the agent offered with --option <key> (see "ankra tickets get"),
or give the decision in your own words with --answer. Both together record
the option with your note beside it. The answer lands on the timeline as a
"Decision:" comment and the agent resumes from it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		optionKey, _ := cmd.Flags().GetString("option")
		answer, _ := cmd.Flags().GetString("answer")
		optionKey = strings.TrimSpace(optionKey)
		answer = strings.TrimSpace(answer)
		if optionKey == "" && answer == "" {
			return withExitCode(exitUsage, fmt.Errorf("give --option <key> or --answer <text> (or both)"))
		}
		ticket, err := resolveTicketReference(args[0])
		if err != nil {
			return err
		}
		if ticket.Status != "blocked" || ticket.DecisionRequest == nil {
			return withExitCode(exitUsage, fmt.Errorf(
				"ticket T-%d is not waiting on a decision (status %s); use \"ankra tickets comment\" to talk to the agent",
				ticket.TicketNumber, ticket.Status))
		}
		if optionKey != "" && findDecisionOption(*ticket.DecisionRequest, optionKey) == nil {
			return withExitCode(exitUsage, fmt.Errorf("'%s' is not one of the offered options (%s)",
				optionKey, strings.Join(decisionOptionKeys(*ticket.DecisionRequest), ", ")))
		}
		decision := client.TicketDecision{}
		if optionKey != "" {
			decision.OptionKey = &optionKey
		}
		if answer != "" {
			decision.Answer = &answer
		}
		decided, err := apiClient.DecideTicket(ticket.ID, decision)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, decided); handled || renderError != nil {
			return renderError
		}
		if optionKey != "" {
			option := findDecisionOption(*ticket.DecisionRequest, optionKey)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Decision recorded on T-%d: %s (option '%s'). The agent resumes with it.\n",
				ticket.TicketNumber, option.Title, option.Key)
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Decision recorded on T-%d: something else - %s. The agent resumes with it.\n",
			ticket.TicketNumber, answer)
		return nil
	},
}

var ticketUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveTicketReference accepts a ticket UUID or a ticket number (8, T-8,
// #8) and returns the ticket. Numbers are resolved by walking the listing,
// closed tickets included, because the API addresses tickets by id.
func resolveTicketReference(reference string) (*client.Ticket, error) {
	trimmed := strings.TrimSpace(reference)
	if ticketUUIDPattern.MatchString(trimmed) {
		return apiClient.GetTicket(trimmed)
	}
	numberText := strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(trimmed), "T-"), "#")
	number, parseError := strconv.ParseInt(numberText, 10, 64)
	if parseError != nil || number < 1 {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"'%s' is not a ticket reference; use the ticket number (8, T-8) or its UUID", reference))
	}
	const pageSize = 200
	for offset := 0; offset < pageSize*10; offset += pageSize {
		page, listError := apiClient.ListTickets(client.TicketListFilter{
			IncludeClosed: true, Limit: pageSize, Offset: offset,
		})
		if listError != nil {
			return nil, listError
		}
		for index := range page.Tickets {
			if page.Tickets[index].TicketNumber == number {
				return &page.Tickets[index], nil
			}
		}
		if len(page.Tickets) < pageSize {
			break
		}
	}
	return nil, withExitCode(exitNotFound, fmt.Errorf("ticket T-%d not found", number))
}

func ticketAssigneeLabel(ticket client.Ticket) string {
	if ticket.AssigneeAgentName != nil && *ticket.AssigneeAgentName != "" {
		return *ticket.AssigneeAgentName + " (agent)"
	}
	if ticket.AssigneeUserName != nil && *ticket.AssigneeUserName != "" {
		return *ticket.AssigneeUserName
	}
	if ticket.AssigneeUserEmail != nil && *ticket.AssigneeUserEmail != "" {
		return *ticket.AssigneeUserEmail
	}
	return "-"
}

// ticketWaitingLabel names what a parked ticket waits on, so the listing
// tells a decision apart from a plain block and from a plan approval.
func ticketWaitingLabel(ticket client.Ticket) string {
	switch {
	case ticket.Status == "blocked" && ticket.DecisionRequest != nil:
		return "your decision"
	case ticket.Status == "blocked":
		return "a human"
	case ticket.Status == "awaiting_approval":
		return "your approval"
	case ticket.Status == "awaiting_review":
		return "review"
	}
	return ""
}

func ticketEventAuthorLabel(event client.TicketEvent) string {
	if event.AuthorAgentName != nil && *event.AuthorAgentName != "" {
		return *event.AuthorAgentName
	}
	if event.AuthorUserName != nil && *event.AuthorUserName != "" {
		return *event.AuthorUserName
	}
	return event.AuthorKind
}

// renderTicketDecision prints the pending choice with the command that
// answers it. Nothing is printed for a ticket that waits on no decision.
func renderTicketDecision(cmd *cobra.Command, ticket client.Ticket) {
	if ticket.Status != "blocked" || ticket.DecisionRequest == nil {
		return
	}
	out := cmd.OutOrStdout()
	request := *ticket.DecisionRequest
	_, _ = fmt.Fprintf(out, "\nYour decision is needed: %s\n", request.Prompt)
	for _, option := range request.Options {
		marker := "  "
		if option.Recommended {
			marker = "* "
		}
		_, _ = fmt.Fprintf(out, "%s[%s] %s\n", marker, option.Key, option.Title)
		if option.Summary != "" {
			_, _ = fmt.Fprintf(out, "      %s\n", option.Summary)
		}
	}
	_, _ = fmt.Fprintf(out, "  [ ] Something else - answer in your own words\n")
	_, _ = fmt.Fprintf(out, "\nAnswer with: ankra tickets decide T-%d --option <key>   or   --answer \"...\"\n",
		ticket.TicketNumber)
	if len(request.Options) > 0 {
		_, _ = fmt.Fprintln(out, "(* = the agent's recommendation)")
	}
}

func findDecisionOption(request client.TicketDecisionRequest, key string) *client.TicketDecisionOption {
	wanted := strings.ToLower(strings.TrimSpace(key))
	for index := range request.Options {
		if strings.ToLower(request.Options[index].Key) == wanted {
			return &request.Options[index]
		}
	}
	return nil
}

func decisionOptionKeys(request client.TicketDecisionRequest) []string {
	keys := make([]string, 0, len(request.Options))
	for _, option := range request.Options {
		keys = append(keys, option.Key)
	}
	return keys
}

func init() {
	ticketsListCmd.Flags().StringSlice("status", nil,
		"Only these statuses (comma-separated: triage, investigating, planning, awaiting_review, awaiting_approval, executing, verifying, blocked, done, cancelled)")
	ticketsListCmd.Flags().Bool("needs-human", false, "Only tickets waiting on a person")
	ticketsListCmd.Flags().Bool("include-closed", false, "Include done and cancelled tickets")
	ticketsListCmd.Flags().String("search", "", "Match on the title")
	ticketsListCmd.Flags().Int("limit", 50, "Maximum tickets to list (up to 200)")
	ticketsEventsCmd.Flags().Int("limit", 200, "Maximum events to show")
	ticketsCommentCmd.Flags().String("body", "", "The comment (markdown)")
	ticketsTransitionCmd.Flags().String("status", "", "The status to move to")
	ticketsTransitionCmd.Flags().String("note", "", "Why the ticket is moving")
	ticketsDecideCmd.Flags().String("option", "", "Key of the offered option to choose")
	ticketsDecideCmd.Flags().String("answer", "", "The decision in your own words, or a note beside --option")
	registerStructuredOutputFlags(ticketsListCmd, ticketsGetCmd, ticketsEventsCmd,
		ticketsCommentCmd, ticketsTransitionCmd, ticketsDecideCmd)

	ticketsCmd.AddCommand(ticketsListCmd)
	ticketsCmd.AddCommand(ticketsGetCmd)
	ticketsCmd.AddCommand(ticketsEventsCmd)
	ticketsCmd.AddCommand(ticketsCommentCmd)
	ticketsCmd.AddCommand(ticketsTransitionCmd)
	ticketsCmd.AddCommand(ticketsDecideCmd)
	rootCmd.AddCommand(ticketsCmd)
}
