package cmd

// What the AI ticket lane has learned about the organisation: the recurring
// conditions tickets are filed against, the playbooks that fix them, and the
// platform-level conditions handed to the engineering tracker instead.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type aiConditionSummary struct {
	ID              string  `json:"id"`
	SubjectKey      string  `json:"subject_key"`
	ConditionKind   string  `json:"condition_kind"`
	OccurrenceCount int     `json:"occurrence_count"`
	EpisodeCount    int     `json:"episode_count"`
	NoActionStreak  int     `json:"no_action_streak"`
	IsChronic       bool    `json:"is_chronic"`
	LastOutcome     *string `json:"last_outcome"`
	LastObservedAt  string  `json:"last_observed_at"`
}

func newAIConditionsCommand() *cobra.Command {
	conditionsCommand := &cobra.Command{
		Use:     "conditions",
		Aliases: []string{"condition"},
		Short:   "Read the recurring problems AI tickets are filed against",
		Long: `Read the organisation's conditions: the recurring problems AI tickets are
filed against, each with how often it was observed and how its tickets closed.
A condition closed without action three times in a row is chronic.`,
	}
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List conditions, most recently observed first",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			limit, _ := command.Flags().GetInt("limit")
			if limit < 1 || limit > client.AIConditionListCap {
				return withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", client.AIConditionListCap))
			}
			chronicOnly, _ := command.Flags().GetBool("chronic")
			document, listError := apiClient.ListAIConditions(command.Context(), chronicOnly, limit)
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Conditions []aiConditionSummary `json:"conditions"`
					Counts     struct {
						Total   int `json:"total"`
						Chronic int `json:"chronic"`
						Open    int `json:"open"`
					} `json:"counts"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				if len(listing.Conditions) == 0 {
					_, _ = fmt.Fprintln(out, "No conditions.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"ID", "KIND", "SUBJECT", "SEEN", "CHRONIC", "LAST OUTCOME", "LAST OBSERVED"})
				for _, condition := range listing.Conditions {
					writer.AppendRow(table.Row{condition.ID, condition.ConditionKind, truncateCell(condition.SubjectKey, 50),
						condition.OccurrenceCount, condition.IsChronic, valueOrDash(condition.LastOutcome), condition.LastObservedAt})
				}
				writer.Render()
				_, _ = fmt.Fprintf(out, "%d conditions: %d open, %d chronic.\n",
					listing.Counts.Total, listing.Counts.Open, listing.Counts.Chronic)
				return nil
			})
		},
	}
	listCommand.Flags().Bool("chronic", false, "Only conditions closed without action three times or more")
	listCommand.Flags().Int("limit", 100, fmt.Sprintf("How many conditions to list (1-%d)", client.AIConditionListCap))

	getCommand := &cobra.Command{
		Use:   "get <condition-id>",
		Short: "Show one condition's evidence timeline",
		Long: `Show one condition's evidence timeline: the tickets filed on it, how each
closed and on what evidence, the probes, the playbook runs and the decisions
people took.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			document, getError := apiClient.GetAICondition(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var timeline struct {
					Condition aiConditionSummary `json:"condition"`
					Entries   []struct {
						At           string `json:"at"`
						Kind         string `json:"kind"`
						TicketNumber int    `json:"ticket_number"`
						Detail       string `json:"detail"`
					} `json:"entries"`
				}
				if decodeError := decodeDocument(document, &timeline); decodeError != nil {
					return decodeError
				}
				condition := timeline.Condition
				_, _ = fmt.Fprintf(out, "Condition:    %s\n", condition.ID)
				_, _ = fmt.Fprintf(out, "Kind:         %s\n", condition.ConditionKind)
				_, _ = fmt.Fprintf(out, "Subject:      %s\n", condition.SubjectKey)
				_, _ = fmt.Fprintf(out, "Observed:     %d times over %d episodes, last %s\n",
					condition.OccurrenceCount, condition.EpisodeCount, condition.LastObservedAt)
				_, _ = fmt.Fprintf(out, "Chronic:      %t (%d closed without action in a row)\n",
					condition.IsChronic, condition.NoActionStreak)
				_, _ = fmt.Fprintf(out, "Last outcome: %s\n", valueOrDash(condition.LastOutcome))
				if len(timeline.Entries) == 0 {
					return nil
				}
				_, _ = fmt.Fprintln(out)
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"AT", "WHAT", "TICKET", "DETAIL"})
				for _, entry := range timeline.Entries {
					writer.AppendRow(table.Row{entry.At, entry.Kind, fmt.Sprintf("#%d", entry.TicketNumber),
						truncateCell(strings.Join(strings.Fields(entry.Detail), " "), 80)})
				}
				writer.Render()
				return nil
			})
		},
	}
	registerStructuredOutputFlags(listCommand, getCommand)
	conditionsCommand.AddCommand(listCommand, getCommand)
	return conditionsCommand
}

type aiPlaybookSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Version          int    `json:"version"`
	ConditionKind    string `json:"condition_kind"`
	Status           string `json:"status"`
	RiskClass        string `json:"risk_class"`
	CleanRuns        int    `json:"clean_runs"`
	PlatformTemplate bool   `json:"platform_template"`
}

func printAIPlaybookOutcome(out io.Writer, verb string, document []byte) error {
	var playbook aiPlaybookSummary
	if decodeError := decodeDocument(document, &playbook); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "%s playbook %s (%s), now %s.\n", verb, playbook.Name, playbook.ID, playbook.Status)
	return nil
}

func newAIPlaybooksCommand() *cobra.Command {
	playbooksCommand := &cobra.Command{
		Use:     "playbooks",
		Aliases: []string{"playbook"},
		Short:   "List, adopt and retire the playbooks AI remediation runs",
		Long: `List the organisation's AI playbooks and the platform templates it can adopt,
approve one for use, or retire one. A playbook is the reviewed procedure the
remediation lane runs for one kind of condition. Adopting and retiring need
an organisation admin.`,
	}
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List the organisation's playbooks and the platform templates",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			document, listError := apiClient.ListAIPlaybooks(command.Context())
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Items []aiPlaybookSummary `json:"items"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				if len(listing.Items) == 0 {
					_, _ = fmt.Fprintln(out, "No playbooks.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"ID", "NAME", "VERSION", "CONDITION", "STATUS", "RISK", "CLEAN RUNS", "TEMPLATE"})
				for _, playbook := range listing.Items {
					writer.AppendRow(table.Row{playbook.ID, truncateCell(playbook.Name, 40), playbook.Version,
						playbook.ConditionKind, playbook.Status, playbook.RiskClass, playbook.CleanRuns, playbook.PlatformTemplate})
				}
				writer.Render()
				return nil
			})
		},
	}
	adoptCommand := &cobra.Command{
		Use:   "adopt <playbook-id>",
		Short: "Approve a playbook for use",
		Long: `Approve a playbook for use: a platform template becomes the organisation's
own approved playbook, and one of its candidate playbooks becomes approved.
From then on the remediation lane may run it, so this asks first. A playbook
that cannot be adopted answers 409.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			playbookID := strings.TrimSpace(arguments[0])
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Adopt playbook %s? AI remediation may run it from now on. [y/N]: ", playbookID),
				skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, adoptError := apiClient.AdoptAIPlaybook(command.Context(), playbookID)
			if adoptError != nil {
				return adoptError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				return printAIPlaybookOutcome(out, "Adopted", document)
			})
		},
	}
	retireCommand := &cobra.Command{
		Use:   "retire <playbook-id>",
		Short: "Retire one of the organisation's playbooks",
		Long: `Retire one of the organisation's own playbooks so the remediation lane stops
running it. Platform templates cannot be retired, and a playbook that cannot
be retired answers 409.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			playbookID := strings.TrimSpace(arguments[0])
			reason, _ := command.Flags().GetString("reason")
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Retire playbook %s? AI remediation stops running it. [y/N]: ", playbookID),
				skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, retireError := apiClient.RetireAIPlaybook(command.Context(), playbookID, strings.TrimSpace(reason))
			if retireError != nil {
				return retireError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				return printAIPlaybookOutcome(out, "Retired", document)
			})
		},
	}
	retireCommand.Flags().String("reason", "", "Why the playbook is retired")
	for _, confirming := range []*cobra.Command{adoptCommand, retireCommand} {
		confirming.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	}
	registerStructuredOutputFlags(listCommand, adoptCommand, retireCommand)
	playbooksCommand.AddCommand(listCommand, adoptCommand, retireCommand)
	return playbooksCommand
}

func newAIHandoffsCommand() *cobra.Command {
	handoffsCommand := &cobra.Command{
		Use:     "handoffs",
		Aliases: []string{"handoff"},
		Short:   "Read and acknowledge the conditions handed to engineering",
		Long: `Read the platform-level conditions Ankra handed to the organisation's
engineering tracker instead of the AI ticket board, and record the tracker's
reference once the issue exists there.`,
	}
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List engineering handoffs",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			statuses, _ := command.Flags().GetStringSlice("status")
			document, listError := apiClient.ListAIEngineeringHandoffs(command.Context(), strings.Join(statuses, ","))
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Handoffs []struct {
						ID               string `json:"id"`
						AlertName        string `json:"alert_name"`
						Severity         string `json:"severity"`
						Status           string `json:"status"`
						OccurrenceCount  int    `json:"occurrence_count"`
						LastSeenAt       string `json:"last_seen_at"`
						TrackerReference string `json:"tracker_reference"`
					} `json:"handoffs"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				if len(listing.Handoffs) == 0 {
					_, _ = fmt.Fprintln(out, "No engineering handoffs.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"ID", "ALERT", "SEVERITY", "STATUS", "SEEN", "LAST SEEN", "TRACKER"})
				for _, handoff := range listing.Handoffs {
					writer.AppendRow(table.Row{handoff.ID, truncateCell(handoff.AlertName, 40), handoff.Severity, handoff.Status,
						handoff.OccurrenceCount, handoff.LastSeenAt, orDash(handoff.TrackerReference)})
				}
				writer.Render()
				return nil
			})
		},
	}
	listCommand.Flags().StringSlice("status", nil, "Only handoffs in this status: open, filed or resolved; repeatable")

	acknowledgeCommand := &cobra.Command{
		Use:   "acknowledge <handoff-id>",
		Short: "Record the engineering tracker's reference for a handoff",
		Long: `Record the engineering tracker's reference for a handoff once the issue
exists there. Needs an organisation admin.`,
		Example: "  ankra ai handoffs acknowledge 0b8f6c2e-0000-4000-8000-000000000001 --tracker-reference ENG-482",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			trackerReference, _ := command.Flags().GetString("tracker-reference")
			trackerReference = strings.TrimSpace(trackerReference)
			if trackerReference == "" {
				return withExitCode(exitUsage, errors.New("--tracker-reference is required: the issue's id or URL in your tracker"))
			}
			handoffID := strings.TrimSpace(arguments[0])
			document, acknowledgeError := apiClient.AcknowledgeAIEngineeringHandoff(command.Context(), handoffID, trackerReference)
			if acknowledgeError != nil {
				return acknowledgeError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				_, _ = fmt.Fprintf(out, "Acknowledged handoff %s as %s.\n", handoffID, trackerReference)
				return nil
			})
		},
	}
	acknowledgeCommand.Flags().String("tracker-reference", "", "The issue's id or URL in your engineering tracker (required)")
	registerStructuredOutputFlags(listCommand, acknowledgeCommand)
	handoffsCommand.AddCommand(listCommand, acknowledgeCommand)
	return handoffsCommand
}
