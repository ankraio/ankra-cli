package cmd

// `ankra tickets settings get|set`, `ankra tickets summary`,
// `ankra tickets changes` and `ankra tickets update` (ankra-hq41xb.35,
// ankra-us966.4): the organisation's ticket desk settings, the board's
// counts, the changes made on a ticket's behalf, and a ticket's metadata.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
	"ankra/internal/hiddenunicode"
)

type ticketDeskSettings struct {
	SlackChannelID            *string  `json:"slack_channel_id"`
	TeamsConversationID       *string  `json:"teams_conversation_id"`
	ReviewerAgentTaskID       *string  `json:"reviewer_agent_task_id"`
	RequireAgentReview        bool     `json:"require_agent_review"`
	AutoFileInsightSeverities []string `json:"auto_file_insight_severities"`
	NotifyMode                string   `json:"notify_mode"`
	NotifyMinPriority         string   `json:"notify_min_priority"`
	NotifySourceKinds         []string `json:"notify_source_kinds"`
	NotifyEventScope          string   `json:"notify_event_scope"`
	NotifyClusterEnvironments []string `json:"notify_cluster_environments"`
	UpdatedAt                 *string  `json:"updated_at"`
}

func listOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func yesOrNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func printTicketDeskSettings(out io.Writer, document json.RawMessage) error {
	var settings ticketDeskSettings
	if decodeError := decodeDocument(document, &settings); decodeError != nil {
		return decodeError
	}
	updated := "never saved (the defaults)"
	if settings.UpdatedAt != nil {
		updated = *settings.UpdatedAt
	}
	_, _ = fmt.Fprintf(out, "Slack channel:          %s\n", valueOrDash(settings.SlackChannelID))
	_, _ = fmt.Fprintf(out, "Teams conversation:     %s\n", valueOrDash(settings.TeamsConversationID))
	_, _ = fmt.Fprintf(out, "Reviewer agent:         %s\n", valueOrDash(settings.ReviewerAgentTaskID))
	_, _ = fmt.Fprintf(out, "Agent review required:  %s\n", yesOrNo(settings.RequireAgentReview))
	_, _ = fmt.Fprintf(out, "Insights filed:         %s\n", listOrDash(settings.AutoFileInsightSeverities))
	_, _ = fmt.Fprintf(out, "Notify:                 %s\n", orDash(settings.NotifyMode))
	_, _ = fmt.Fprintf(out, "Notify min priority:    %s\n", orDash(settings.NotifyMinPriority))
	_, _ = fmt.Fprintf(out, "Notify sources:         %s\n", listOrDash(settings.NotifySourceKinds))
	_, _ = fmt.Fprintf(out, "Notify events:          %s\n", orDash(settings.NotifyEventScope))
	_, _ = fmt.Fprintf(out, "Notify environments:    %s\n", listOrDash(settings.NotifyClusterEnvironments))
	_, _ = fmt.Fprintf(out, "Updated:                %s\n", updated)
	return nil
}

func newTicketsDeskSettingsGetCommand() *cobra.Command {
	return newOperationCommand(operationCommand{
		use:   "get",
		short: "Show the organisation's ticket desk settings",
		long: `Show where tickets are announced (the Slack channel and Teams conversation),
the reviewing agent and whether its review is required, which insight
severities are filed as tickets on their own, and the channel notification
policy. An organisation that never saved settings shows the defaults.`,
		example:   "  ankra tickets settings get\n  ankra tickets settings get -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			return apiClient.GetTicketDeskSettings(command.Context())
		},
		human: printTicketDeskSettings,
	})
}

// ticketDeskSettingsChangesFromFlags builds the PUT body from the flags that
// were passed: a member left out keeps its stored value, and a --clear-*
// flag sends null.
func ticketDeskSettingsChangesFromFlags(command *cobra.Command) (map[string]any, error) {
	changes := map[string]any{}
	for _, nullable := range []struct{ flag, clearFlag, member, meaning string }{
		{"slack-channel", "clear-slack", "slack_channel_id", "a Slack channel id"},
		{"teams-conversation", "clear-teams", "teams_conversation_id", "a Teams conversation id"},
		{"reviewer-agent", "clear-reviewer", "reviewer_agent_task_id", "the reviewing agent task's id"},
	} {
		if command.Flags().Changed(nullable.flag) {
			value := strings.TrimSpace(mustFlagString(command, nullable.flag))
			if value == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("--%s needs %s (use --%s to clear it)", nullable.flag, nullable.meaning, nullable.clearFlag))
			}
			changes[nullable.member] = value
		} else if cleared, _ := command.Flags().GetBool(nullable.clearFlag); cleared {
			changes[nullable.member] = nil
		}
	}
	if command.Flags().Changed("require-agent-review") {
		required, _ := command.Flags().GetBool("require-agent-review")
		changes["require_agent_review"] = required
	}
	for _, text := range []struct{ flag, member string }{
		{"notify-mode", "notify_mode"},
		{"notify-min-priority", "notify_min_priority"},
		{"notify-events", "notify_event_scope"},
	} {
		if command.Flags().Changed(text.flag) {
			value := strings.TrimSpace(mustFlagString(command, text.flag))
			if value == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("--%s cannot be blank", text.flag))
			}
			changes[text.member] = value
		}
	}
	for _, list := range []struct{ flag, member string }{
		{"insight-severities", "auto_file_insight_severities"},
		{"notify-sources", "notify_source_kinds"},
		{"notify-environments", "notify_cluster_environments"},
	} {
		if command.Flags().Changed(list.flag) {
			values, _ := command.Flags().GetStringSlice(list.flag)
			trimmed := []string{}
			for _, value := range values {
				if value = strings.TrimSpace(value); value != "" {
					trimmed = append(trimmed, value)
				}
			}
			changes[list.member] = trimmed
		}
	}
	if len(changes) == 0 {
		return nil, withExitCode(exitUsage, fmt.Errorf("nothing to change: pass at least one setting (see --help)"))
	}
	return changes, nil
}

func newTicketsDeskSettingsSetCommand() *cobra.Command {
	setCommand := newOperationCommand(operationCommand{
		use:   "set",
		short: "Change the organisation's ticket desk settings",
		long: `Change the ticket desk settings you pass and keep the rest. A list flag
replaces the whole list; pass it empty ('') to clear it. The --clear-* flags
drop a channel or the reviewing agent. Needs an organisation admin.

Slack channel ids and Teams conversation ids are listed by 'ankra alerts
destinations channels'; agent task ids by 'ankra ai tasks list'. A project can
announce its tickets in channels of its own: 'ankra tickets settings projects'.

It asks first; --yes skips the question and is required when there is no
terminal to ask on.`,
		example: `  ankra tickets settings set --slack-channel C0123ABCD --yes
  ankra tickets settings set --reviewer-agent 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c --require-agent-review
  ankra tickets settings set --insight-severities critical,warning
  ankra tickets settings set --notify-mode filtered --notify-min-priority high --notify-environments production --yes`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			changes, changesError := ticketDeskSettingsChangesFromFlags(command)
			if changesError != nil {
				return nil, changesError
			}
			if confirmError := confirmOrganisationChange(command,
				"Changing the ticket desk settings changes where tickets are announced, how they are reviewed and which insights are filed, for the whole organisation."); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.UpdateTicketDeskSettings(command.Context(), changes)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			_, _ = fmt.Fprintln(out, "Ticket desk settings updated.")
			return printTicketDeskSettings(out, document)
		},
	})
	flags := setCommand.Flags()
	flags.String("slack-channel", "", "Slack channel id tickets are announced in (for example C0123ABCD)")
	flags.Bool("clear-slack", false, "Announce tickets in no Slack channel")
	flags.String("teams-conversation", "", "Teams conversation id tickets are announced in (19:...@thread.tacv2)")
	flags.Bool("clear-teams", false, "Announce tickets in no Teams conversation")
	flags.String("reviewer-agent", "", "The agent task that reviews tickets (its id)")
	flags.Bool("clear-reviewer", false, "Remove the reviewing agent")
	flags.Bool("require-agent-review", false, "Require the reviewing agent's review (--require-agent-review=false turns it off)")
	flags.StringSlice("insight-severities", nil, "Insight severities filed as tickets on their own: critical, warning, info ('' for none)")
	flags.String("notify-mode", "", "Channel notifications: all, filtered or none")
	flags.String("notify-min-priority", "", "With filtered notifications, the lowest priority announced: urgent, high, medium or low")
	flags.StringSlice("notify-sources", nil, "With filtered notifications, the sources announced: insight, alert, automation, chat_remediation, agent, user")
	flags.String("notify-events", "", "Which ticket events are posted: all, important or anchor_only")
	flags.StringSlice("notify-environments", nil, "Only tickets on clusters of these environments are announced ('' for every environment)")
	flags.BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	setCommand.MarkFlagsMutuallyExclusive("slack-channel", "clear-slack")
	setCommand.MarkFlagsMutuallyExclusive("teams-conversation", "clear-teams")
	setCommand.MarkFlagsMutuallyExclusive("reviewer-agent", "clear-reviewer")
	return setCommand
}

// sortedCounts renders a count per key as "key n, key n", keys in order.
func sortedCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func newTicketsSummaryCommand() *cobra.Command {
	summaryCommand := newOperationCommand(operationCommand{
		use:   "summary",
		short: "Count the board's top-level tickets by status, kind, priority and agent",
		long: `Count the board's top-level tickets: per status, open per kind, priority and
agent, what waits on a person, what is in flight, and what was resolved in the
last seven days. The filters narrow the counts the way they narrow 'ankra
tickets list', so both describe the same tickets.`,
		example: `  ankra tickets summary
  ankra tickets summary --kind incident --priority urgent,high
  ankra tickets summary --cluster prod-eu --label cert -o json`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			filter := client.TicketSummaryFilter{}
			filter.Statuses, _ = command.Flags().GetStringSlice("status")
			filter.Kinds, _ = command.Flags().GetStringSlice("kind")
			filter.Priorities, _ = command.Flags().GetStringSlice("priority")
			filter.Labels, _ = command.Flags().GetStringSlice("label")
			filter.Search, _ = command.Flags().GetString("search")
			filter.NeedsHuman, _ = command.Flags().GetBool("needs-human")
			filter.IncludeReceipts, _ = command.Flags().GetBool("include-receipts")
			clusterReferences, _ := command.Flags().GetStringSlice("cluster")
			for _, reference := range clusterReferences {
				if reference = strings.TrimSpace(reference); reference == "" {
					continue
				}
				clusterID, resolveError := resolveClusterArg(reference)
				if resolveError != nil {
					return nil, resolveError
				}
				filter.ClusterIDs = append(filter.ClusterIDs, clusterID)
			}
			return apiClient.GetTicketBoardSummary(command.Context(), filter)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var summary struct {
				StatusCounts                  map[string]int `json:"status_counts"`
				NeedsHuman                    int            `json:"needs_human"`
				InFlight                      int            `json:"in_flight"`
				DoneLastSevenDays             int            `json:"done_last_7_days"`
				OpenByKind                    map[string]int `json:"open_by_kind"`
				OpenByPriority                map[string]int `json:"open_by_priority"`
				OpenAssignedToViewer          int            `json:"open_assigned_to_viewer"`
				OpenByAgent                   map[string]int `json:"open_by_agent"`
				AutonomousResolutionsLastWeek int            `json:"autonomous_resolutions_last_7_days"`
				FixedResolutionsLastWeek      int            `json:"fixed_resolutions_last_7_days"`
				HumanMinutesPerResolution     float64        `json:"human_minutes_per_resolution_last_7_days"`
				HumanGateWaiting              int            `json:"human_gate_waiting"`
			}
			if decodeError := decodeDocument(document, &summary); decodeError != nil {
				return decodeError
			}
			_, _ = fmt.Fprintf(out, "Needs a person:       %d (%d at a human gate)\n", summary.NeedsHuman, summary.HumanGateWaiting)
			_, _ = fmt.Fprintf(out, "In flight:            %d\n", summary.InFlight)
			_, _ = fmt.Fprintf(out, "Assigned to you:      %d open\n", summary.OpenAssignedToViewer)
			_, _ = fmt.Fprintf(out, "By status:            %s\n", sortedCounts(summary.StatusCounts))
			_, _ = fmt.Fprintf(out, "Open by kind:         %s\n", sortedCounts(summary.OpenByKind))
			_, _ = fmt.Fprintf(out, "Open by priority:     %s\n", sortedCounts(summary.OpenByPriority))
			_, _ = fmt.Fprintf(out, "Open by agent:        %s\n", sortedCounts(summary.OpenByAgent))
			_, _ = fmt.Fprintf(out, "Last 7 days:          %d done, %d resolved by agents on their own, %d fixed, %.1f human minutes per resolution\n",
				summary.DoneLastSevenDays, summary.AutonomousResolutionsLastWeek, summary.FixedResolutionsLastWeek, summary.HumanMinutesPerResolution)
			return nil
		},
	})
	flags := summaryCommand.Flags()
	flags.StringSlice("status", nil, "Only tickets in these statuses (repeat or comma-separate)")
	flags.StringSlice("kind", nil, "Only tickets of these kinds: incident, insight, gap, automation, request, chore")
	flags.StringSlice("priority", nil, "Only tickets of these priorities: urgent, high, medium, low")
	flags.StringSlice("cluster", nil, "Only tickets about these clusters (name or id)")
	flags.StringSlice("label", nil, "Only tickets carrying one of these labels")
	flags.String("search", "", "Match on the title")
	flags.Bool("needs-human", false, "Only tickets waiting on a person")
	flags.Bool("include-receipts", false, "Count the chat-fix receipts too (writes a person made through the AI chat or MCP)")
	return summaryCommand
}

func newTicketsChangesCommand() *cobra.Command {
	return newOperationCommand(operationCommand{
		use:   "changes <ticket>",
		short: "List the changes made on a ticket's behalf",
		long: `List the changes applied on a ticket's behalf: each AI action linked to it,
the cluster and stack it touched, who approved it and how it ended. -o json
carries each action's change preview and execution.`,
		example:   "  ankra tickets changes T-42\n  ankra tickets changes 42 -o json",
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			ticketID, resolveError := resolveTicketID(arguments)
			if resolveError != nil {
				return nil, resolveError
			}
			return apiClient.ListTicketChanges(command.Context(), ticketID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Changes []struct {
					ActionID    string  `json:"action_id"`
					ToolName    string  `json:"tool_name"`
					Status      string  `json:"status"`
					RiskLevel   string  `json:"risk_level"`
					ClusterName *string `json:"cluster_name"`
					StackName   *string `json:"stack_name"`
					ApprovedBy  *string `json:"approved_by"`
					ProposedAt  string  `json:"proposed_at"`
				} `json:"changes"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Changes) == 0 {
				_, _ = fmt.Fprintln(out, "No changes were made on this ticket's behalf.")
				return nil
			}
			rows := make([]table.Row, 0, len(listing.Changes))
			for _, change := range listing.Changes {
				target := valueOrDash(change.ClusterName)
				if change.StackName != nil && *change.StackName != "" {
					target += " / " + *change.StackName
				}
				rows = append(rows, table.Row{change.ActionID, change.ToolName, change.Status, orDash(change.RiskLevel), target,
					valueOrDash(change.ApprovedBy), change.ProposedAt})
			}
			renderRoundedTable(out, table.Row{"Action", "Tool", "Status", "Risk", "Cluster / stack", "Approved by", "Proposed"}, rows)
			return nil
		},
	})
}

// ticketUpdateChangesFromFlags builds the PATCH body from the flags that
// were passed, with the names of what changes for the confirmation.
func ticketUpdateChangesFromFlags(command *cobra.Command) (map[string]any, []string, int, error) {
	changes := map[string]any{}
	changed := []string{}
	hidden := 0
	for _, text := range []struct{ flag, member string }{
		{"title", "title"},
		{"body", "body"},
		{"priority", "priority"},
		{"staffing", "staffing"},
		{"due", "due_at"},
	} {
		if !command.Flags().Changed(text.flag) {
			continue
		}
		value := strings.TrimSpace(mustFlagString(command, text.flag))
		if text.flag == "title" || text.flag == "body" {
			cleaned, removed := hiddenunicode.Strip(value)
			hidden += removed
			value = strings.TrimSpace(cleaned)
		}
		if value == "" && text.flag != "body" {
			return nil, nil, 0, withExitCode(exitUsage, fmt.Errorf("--%s cannot be blank", text.flag))
		}
		if text.flag == "due" {
			if _, parseError := time.Parse(time.RFC3339, value); parseError != nil {
				return nil, nil, 0, withExitCode(exitUsage, fmt.Errorf("--due takes an RFC 3339 time such as 2026-11-20T17:00:00Z, got %q", value))
			}
		}
		changes[text.member] = value
		changed = append(changed, text.flag)
	}
	if clearDue, _ := command.Flags().GetBool("clear-due"); clearDue {
		changes["due_at"] = nil
		changed = append(changed, "due date")
	}
	if command.Flags().Changed("label") {
		labels, _ := command.Flags().GetStringSlice("label")
		trimmed := []string{}
		for _, label := range labels {
			if label = strings.TrimSpace(label); label != "" {
				trimmed = append(trimmed, label)
			}
		}
		changes["labels"] = trimmed
		changed = append(changed, "labels")
	}
	if command.Flags().Changed("cluster") {
		reference := strings.TrimSpace(mustFlagString(command, "cluster"))
		if reference == "" {
			return nil, nil, 0, withExitCode(exitUsage, fmt.Errorf("--cluster needs a cluster name or id (use --clear-cluster to unset it)"))
		}
		clusterID, resolveError := resolveClusterArg(reference)
		if resolveError != nil {
			return nil, nil, 0, resolveError
		}
		changes["cluster_id"] = clusterID
		changed = append(changed, "cluster")
	} else if clearCluster, _ := command.Flags().GetBool("clear-cluster"); clearCluster {
		changes["cluster_id"] = nil
		changed = append(changed, "cluster")
	}
	if len(changes) == 0 {
		return nil, nil, 0, withExitCode(exitUsage, fmt.Errorf("nothing to change: pass --title, --body, --priority, --label, --cluster, --staffing or --due, or a --clear-* flag"))
	}
	return changes, changed, hidden, nil
}

func newTicketsUpdateCommand() *cobra.Command {
	updateCommand := newOperationCommand(operationCommand{
		use:   "update <ticket>",
		short: "Change a ticket's title, body, priority, labels, cluster, staffing or due date",
		long: `Change a ticket's metadata and keep the rest. --label replaces every label on
the ticket; pass it empty ('') to remove them all. --due takes an RFC 3339
time. Moving a ticket between statuses is 'ankra tickets transition'.

It asks first; --yes skips the question and is required when there is no
terminal to ask on.`,
		example: `  ankra tickets update T-42 --priority urgent --yes
  ankra tickets update 42 --title "Ingress cert expires Friday" --label cert,ingress
  ankra tickets update T-42 --due 2026-11-20T17:00:00Z --staffing manual
  ankra tickets update T-42 --clear-cluster --clear-due --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			changes, changed, hidden, changesError := ticketUpdateChangesFromFlags(command)
			if changesError != nil {
				return nil, changesError
			}
			if hidden > 0 {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "%s\n", hiddenunicode.Notice(hidden))
			}
			ticket, resolveError := resolveTicketReference(arguments[0])
			if resolveError != nil {
				return nil, resolveError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Updating T-%d %q changes its %s.", ticket.TicketNumber, ticket.Title, strings.Join(changed, ", "))); confirmError != nil {
				return nil, confirmError
			}
			document, updateError := apiClient.UpdateTicket(command.Context(), ticket.ID, changes)
			return document, ticketWriteError(updateError)
		},
		human: printTicketAfter(func(ticket client.Ticket) string {
			return fmt.Sprintf("is updated: %s (%s, %s).", ticket.Title, ticket.Priority, ticket.Status)
		}),
	})
	flags := updateCommand.Flags()
	flags.String("title", "", "The new title")
	flags.String("body", "", "The new description (markdown; '' empties it)")
	flags.String("priority", "", "urgent, high, medium or low")
	flags.StringSlice("label", nil, "The ticket's labels, replacing the current ones (repeat or comma-separate; '' for none)")
	flags.String("cluster", "", "The cluster the ticket is about (name or id)")
	flags.Bool("clear-cluster", false, "The ticket is about no cluster")
	flags.String("staffing", "", "auto (the AI team may staff it) or manual (a person owns it)")
	flags.String("due", "", "Due date, an RFC 3339 time such as 2026-11-20T17:00:00Z")
	flags.Bool("clear-due", false, "Remove the due date")
	flags.BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	updateCommand.MarkFlagsMutuallyExclusive("cluster", "clear-cluster")
	updateCommand.MarkFlagsMutuallyExclusive("due", "clear-due")
	return updateCommand
}

func init() {
	ticketsSettingsCmd.AddCommand(newTicketsDeskSettingsGetCommand(), newTicketsDeskSettingsSetCommand())
	ticketsCmd.AddCommand(newTicketsSummaryCommand(), newTicketsChangesCommand(), newTicketsUpdateCommand())
}
