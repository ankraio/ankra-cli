package cmd

// `ankra tickets assign|plan|workspace` and `ankra tickets sync ...`
// (ankra-hq41xb.23): who a ticket goes to, its plan and its review, the AI
// workspace attached to it, and the connections that mirror tickets to
// Jira, Linear or a webhook.
//
// A sync connection's credential is never a flag: it comes from stdin
// (--secret-stdin) or a masked prompt, and it is never printed, including
// in an error. The platform never returns one.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// resolveTicketID resolves a command's leading <ticket> argument to the
// ticket's id.
func resolveTicketID(arguments []string) (string, error) {
	ticket, resolveError := resolveTicketReference(arguments[0])
	if resolveError != nil {
		return "", resolveError
	}
	return ticket.ID, nil
}

// printTicketAfter prints one line about a ticket a write answered with.
func printTicketAfter(describe func(ticket client.Ticket) string) func(out io.Writer, document json.RawMessage) error {
	return func(out io.Writer, document json.RawMessage) error {
		var ticket client.Ticket
		if decodeError := decodeDocument(document, &ticket); decodeError != nil {
			return decodeError
		}
		_, _ = fmt.Fprintf(out, "T-%d %s\n", ticket.TicketNumber, describe(ticket))
		return nil
	}
}

func newTicketsAssignCommand() *cobra.Command {
	assignCommand := newOperationCommand(operationCommand{
		use:   "assign <ticket> (--agent <agent-task-id> | --user <user-id> | --unassign)",
		short: "Assign a ticket to an agent or a member, or unassign it",
		long: `Assign a ticket to an agent (--agent, the agent task's id from 'ankra ai tasks
list') or to an organisation member (--user, the member's user id), never
both. --unassign clears the assignee.`,
		example: `  ankra tickets assign T-42 --agent 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c
  ankra tickets assign T-42 --unassign`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			agentTaskID, _ := command.Flags().GetString("agent")
			userID, _ := command.Flags().GetString("user")
			unassign, _ := command.Flags().GetBool("unassign")
			agentTaskID, userID = strings.TrimSpace(agentTaskID), strings.TrimSpace(userID)
			chosen := 0
			for _, isChosen := range []bool{agentTaskID != "", userID != "", unassign} {
				if isChosen {
					chosen++
				}
			}
			if chosen != 1 {
				return nil, withExitCode(exitUsage, fmt.Errorf("pass exactly one of --agent, --user or --unassign"))
			}
			request := client.TicketAssignRequest{}
			if agentTaskID != "" {
				request.AssigneeAgentTaskID = &agentTaskID
			}
			if userID != "" {
				request.AssigneeUserID = &userID
			}
			ticketID, resolveError := resolveTicketID(arguments)
			if resolveError != nil {
				return nil, resolveError
			}
			document, assignError := apiClient.AssignTicket(command.Context(), ticketID, request)
			return document, ticketWriteError(assignError)
		},
		human: printTicketAfter(func(ticket client.Ticket) string {
			if assignee := ticketAssigneeLabel(ticket); assignee != "-" {
				return "is assigned to " + assignee + "."
			}
			return "is unassigned."
		}),
	})
	assignCommand.Flags().String("agent", "", "The agent task to assign the ticket to (its id)")
	assignCommand.Flags().String("user", "", "The organisation member to assign the ticket to (their user id)")
	assignCommand.Flags().Bool("unassign", false, "Clear the assignee")
	return assignCommand
}

func newTicketsPlanCommand() *cobra.Command {
	planCommand := &cobra.Command{
		Use:   "plan",
		Short: "Draft a ticket's plan, or review the plan it carries",
		Long: `A ticket can carry a plan: what will be done about it, written before the
work starts. 'draft' records a plan you wrote; 'review' approves the plan on
the ticket or asks for changes. 'ankra tickets get' shows a ticket's plan
status.`,
	}

	draftCommand := newOperationCommand(operationCommand{
		use:   "draft <ticket> --file <path>",
		short: "Record a plan you wrote on a ticket",
		long: `Record a plan on the ticket from a file, or from stdin with '--file -'. An
open ticket only: a closed one answers not found.`,
		example: `  ankra tickets plan draft T-42 --file plan.md
  printf '1. Renew the certificate\n2. Roll the ingress\n' | ankra tickets plan draft T-42 --file -`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			planPath, pathError := requiredTextFlag(command, "file", "the file holding the plan, or - for stdin")
			if pathError != nil {
				return nil, pathError
			}
			planDocument, readError := readDocumentFile(command, planPath)
			if readError != nil {
				return nil, readError
			}
			if strings.TrimSpace(string(planDocument)) == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("the plan is empty"))
			}
			ticketID, resolveError := resolveTicketID(arguments)
			if resolveError != nil {
				return nil, resolveError
			}
			document, draftError := apiClient.DraftTicketPlan(command.Context(), ticketID, string(planDocument))
			return document, ticketWriteError(draftError)
		},
		human: printTicketAfter(func(ticket client.Ticket) string {
			return fmt.Sprintf("carries the plan (plan %s, ticket %s).", orDash(ticket.PlanStatus), ticket.Status)
		}),
	})
	draftCommand.Flags().String("file", "", "The file holding the plan, or - for stdin (required)")

	reviewCommand := newOperationCommand(operationCommand{
		use:   "review <ticket> --verdict approve|request_changes",
		short: "Approve a ticket's plan, or ask for changes",
		long: `Review the plan on the ticket: --verdict approve lets the work start,
request_changes sends it back with your --rationale. --expected-updated-at
takes the ticket's updated_at as you read it ('ankra tickets get -o json');
the platform then refuses the review (exit 1) if the ticket changed since.`,
		example: `  ankra tickets plan review T-42 --verdict approve
  ankra tickets plan review T-42 --verdict request_changes --rationale "Roll the staging ingress first"`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			verdict, _ := command.Flags().GetString("verdict")
			rationale, _ := command.Flags().GetString("rationale")
			expectedUpdatedAt, _ := command.Flags().GetString("expected-updated-at")
			request := client.TicketPlanReviewRequest{Verdict: strings.TrimSpace(verdict), Rationale: strings.TrimSpace(rationale),
				ExpectedUpdatedAt: strings.TrimSpace(expectedUpdatedAt)}
			if request.Verdict != "approve" && request.Verdict != "request_changes" {
				return nil, withExitCode(exitUsage, fmt.Errorf("--verdict must be approve or request_changes, got %q", verdict))
			}
			ticketID, resolveError := resolveTicketID(arguments)
			if resolveError != nil {
				return nil, resolveError
			}
			document, reviewError := apiClient.ReviewTicketPlan(command.Context(), ticketID, request)
			return document, ticketWriteError(reviewError)
		},
		human: printTicketAfter(func(ticket client.Ticket) string {
			return fmt.Sprintf("plan is %s; the ticket is %s.", orDash(ticket.PlanStatus), ticket.Status)
		}),
	})
	reviewCommand.Flags().String("verdict", "", "approve or request_changes (required)")
	reviewCommand.Flags().String("rationale", "", "Why; shown on the ticket's timeline")
	reviewCommand.Flags().String("expected-updated-at", "", "Refuse the review if the ticket's updated_at is no longer this")

	planCommand.AddCommand(draftCommand, reviewCommand)
	return planCommand
}

func newTicketsWorkspaceCommand() *cobra.Command {
	workspaceCommand := newOperationCommand(operationCommand{
		use:   "workspace <ticket> (--workspace <workspace-id> | --detach)",
		short: "Attach an AI workspace to a ticket, or detach the current one",
		example: `  ankra tickets workspace T-42 --workspace 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c
  ankra tickets workspace T-42 --detach`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			workspaceID, _ := command.Flags().GetString("workspace")
			detach, _ := command.Flags().GetBool("detach")
			workspaceID = strings.TrimSpace(workspaceID)
			if (workspaceID == "") != detach {
				return nil, withExitCode(exitUsage, fmt.Errorf("pass exactly one of --workspace or --detach"))
			}
			ticketID, resolveError := resolveTicketID(arguments)
			if resolveError != nil {
				return nil, resolveError
			}
			var attached *string
			if !detach {
				attached = &workspaceID
			}
			document, workspaceError := apiClient.SetTicketWorkspace(command.Context(), ticketID, attached)
			return document, ticketWriteError(workspaceError)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var ticket struct {
				TicketNumber int64   `json:"ticket_number"`
				WorkspaceID  *string `json:"workspace_id"`
			}
			if decodeError := decodeDocument(document, &ticket); decodeError != nil {
				return decodeError
			}
			if ticket.WorkspaceID == nil || *ticket.WorkspaceID == "" {
				_, _ = fmt.Fprintf(out, "T-%d has no workspace attached.\n", ticket.TicketNumber)
				return nil
			}
			_, _ = fmt.Fprintf(out, "T-%d has workspace %s attached.\n", ticket.TicketNumber, *ticket.WorkspaceID)
			return nil
		},
	})
	workspaceCommand.Flags().String("workspace", "", "The AI workspace to attach (its id)")
	workspaceCommand.Flags().Bool("detach", false, "Detach the workspace the ticket has")
	return workspaceCommand
}

type ticketSyncConnection struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Provider             string         `json:"provider"`
	Config               map[string]any `json:"config"`
	Enabled              bool           `json:"enabled"`
	CredentialConfigured bool           `json:"credential_configured"`
	SyncKinds            []string       `json:"sync_kinds"`
	SyncMinPriority      string         `json:"sync_min_priority"`
	SyncEventScope       string         `json:"sync_event_scope"`
	LastSyncedAt         *string        `json:"last_synced_at"`
	LastError            *string        `json:"last_error"`
}

func (connection ticketSyncConnection) target() string {
	parts := []string{}
	for _, key := range []string{"base_url", "project_key", "issue_type", "team_name", "team_key", "team_id", "url"} {
		if value, isText := connection.Config[key].(string); isText && value != "" {
			parts = append(parts, value)
		}
	}
	return orDash(strings.Join(parts, " "))
}

func (connection ticketSyncConnection) state() string {
	switch {
	case !connection.Enabled:
		return "disabled"
	case connection.LastError != nil && *connection.LastError != "":
		return "failing"
	}
	return "enabled"
}

func printTicketSyncConnection(out io.Writer, document json.RawMessage) error {
	var connection ticketSyncConnection
	if decodeError := decodeDocument(document, &connection); decodeError != nil {
		return decodeError
	}
	credential := "not held"
	if connection.CredentialConfigured {
		credential = "held by the platform (never shown)"
	}
	kinds := "all kinds"
	if len(connection.SyncKinds) > 0 {
		kinds = strings.Join(connection.SyncKinds, ", ")
	}
	_, _ = fmt.Fprintf(out, "Sync connection: %s (%s)\n", connection.Name, connection.ID)
	_, _ = fmt.Fprintf(out, "  Provider:    %s\n", connection.Provider)
	_, _ = fmt.Fprintf(out, "  Target:      %s\n", connection.target())
	_, _ = fmt.Fprintf(out, "  State:       %s\n", connection.state())
	_, _ = fmt.Fprintf(out, "  Credential:  %s\n", credential)
	_, _ = fmt.Fprintf(out, "  Syncs:       %s at %s priority or above, events %s\n", kinds,
		orDash(connection.SyncMinPriority), orDash(connection.SyncEventScope))
	_, _ = fmt.Fprintf(out, "  Last synced: %s\n", valueOrDash(connection.LastSyncedAt))
	if connection.LastError != nil && *connection.LastError != "" {
		_, _ = fmt.Fprintf(out, "  Last error:  %s\n", *connection.LastError)
	}
	return nil
}

// ticketSyncConfigFlags maps each provider's non-secret settings to the
// flag that carries it.
var ticketSyncConfigFlags = map[string]map[string]string{
	"jira":    {"base_url": "base-url", "project_key": "project-key", "issue_type": "issue-type"},
	"linear":  {"team_id": "team-id"},
	"webhook": {"url": "url"},
}

// ticketSyncConfigFromFlags reads the settings flags of provider and refuses
// one that belongs to another provider.
func ticketSyncConfigFromFlags(command *cobra.Command, provider string) (map[string]string, error) {
	ownFlags, isProvider := ticketSyncConfigFlags[provider]
	if !isProvider {
		return nil, withExitCode(exitUsage, fmt.Errorf("--provider must be jira, linear or webhook, got %q", provider))
	}
	config := map[string]string{}
	for key, flagName := range ownFlags {
		if value, _ := command.Flags().GetString(flagName); strings.TrimSpace(value) != "" {
			config[key] = strings.TrimSpace(value)
		}
	}
	foreign := []string{}
	for otherProvider, otherFlags := range ticketSyncConfigFlags {
		if otherProvider == provider {
			continue
		}
		for _, flagName := range otherFlags {
			if command.Flags().Changed(flagName) {
				foreign = append(foreign, "--"+flagName)
			}
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return nil, withExitCode(exitUsage, fmt.Errorf("%s does not belong to a %s connection", strings.Join(foreign, ", "), provider))
	}
	return config, nil
}

// readTicketSyncCredential reads the provider's secret from stdin or a
// masked prompt and builds the credential the platform stores. It returns
// the secret too, so the caller can keep it out of an error.
func readTicketSyncCredential(command *cobra.Command, provider string) (map[string]string, string, error) {
	label, secretKey := "API key", "api_key"
	credential := map[string]string{}
	switch provider {
	case "jira":
		label, secretKey = "API token", "api_token"
		email, emailError := requiredTextFlag(command, "email", "the Atlassian account the Jira API token belongs to")
		if emailError != nil {
			return nil, "", emailError
		}
		credential["email"] = email
	case "webhook":
		label, secretKey = "signing secret", "secret"
	}
	if provider != "jira" && command.Flags().Changed("email") {
		return nil, "", withExitCode(exitUsage, fmt.Errorf("--email does not belong to a %s connection", provider))
	}
	secret, secretError := readSecretFromStdinOrPrompt(command, "secret-stdin", label)
	if secretError != nil {
		return nil, "", secretError
	}
	credential[secretKey] = secret
	return credential, secret, nil
}

func registerTicketSyncFilterFlags(command *cobra.Command) {
	command.Flags().StringSlice("kind", nil, "Only sync tickets of this kind, repeatable: incident, insight, gap, automation, request, chore")
	command.Flags().String("min-priority", "", "Only sync tickets at this priority or above: urgent, high, medium or low")
	command.Flags().String("event-scope", "", "all relays the ticket's timeline; anchor_only creates the issue and stays quiet after")
	command.Flags().String("base-url", "", "Jira: the site's URL, such as https://example.atlassian.net")
	command.Flags().String("project-key", "", "Jira: the project issues are created in")
	command.Flags().String("issue-type", "", "Jira: the issue type to create")
	command.Flags().String("email", "", "Jira: the Atlassian account the API token belongs to")
	command.Flags().String("team-id", "", "Linear: the team issues are created in ('ankra tickets sync destinations')")
	command.Flags().String("url", "", "Webhook: the https endpoint events are posted to")
	command.Flags().Bool("secret-stdin", false, "Read the Jira API token, Linear API key or webhook signing secret from stdin")
}

func applyTicketSyncFilterFlags(command *cobra.Command, request *client.TicketSyncConnectionRequest) {
	if command.Flags().Changed("kind") {
		kinds, _ := command.Flags().GetStringSlice("kind")
		request.SyncKinds = &kinds
	}
	minimumPriority, _ := command.Flags().GetString("min-priority")
	eventScope, _ := command.Flags().GetString("event-scope")
	request.SyncMinPriority, request.SyncEventScope = strings.TrimSpace(minimumPriority), strings.TrimSpace(eventScope)
}

func newTicketsSyncConnectionsCommand() *cobra.Command {
	connectionsCommand := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection"},
		Short:   "Manage the connections that mirror tickets to Jira, Linear or a webhook",
	}

	listCommand := newOperationCommand(operationCommand{
		use:       "list",
		aliases:   []string{"ls"},
		short:     "List the organisation's ticket sync connections",
		long:      "List the organisation's ticket sync connections. Stored credentials are never returned.",
		example:   "  ankra tickets sync connections list\n  ankra tickets sync connections list -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			return apiClient.ListTicketSyncConnections(command.Context())
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Connections []ticketSyncConnection `json:"connections"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Connections) == 0 {
				_, _ = fmt.Fprintln(out, "No ticket sync connections. Create one with 'ankra tickets sync connections create'.")
				return nil
			}
			rows := make([]table.Row, 0, len(listing.Connections))
			for _, connection := range listing.Connections {
				rows = append(rows, table.Row{connection.ID, connection.Name, connection.Provider, connection.target(),
					connection.state(), valueOrDash(connection.LastSyncedAt)})
			}
			renderRoundedTable(out, table.Row{"ID", "Name", "Provider", "Target", "State", "Last synced"}, rows)
			return nil
		},
	})

	getCommand := newOperationCommand(operationCommand{
		use:       "get <connection-id>",
		short:     "Show one ticket sync connection, without its credential",
		example:   "  ankra tickets sync connections get 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c",
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			connectionID, argumentError := requiredArgument(arguments, 0, "the connection id")
			if argumentError != nil {
				return nil, argumentError
			}
			return apiClient.GetTicketSyncConnection(command.Context(), connectionID)
		},
		human: printTicketSyncConnection,
	})

	createCommand := newOperationCommand(operationCommand{
		use:   "create --name <name> --provider jira|linear|webhook",
		short: "Create a ticket sync connection",
		long: `Create a connection that mirrors tickets to an external tracker. What it
needs depends on --provider:

  jira     --base-url, --project-key, --email and an optional --issue-type.
           The secret is the Atlassian API token of --email.
  linear   --team-id ('ankra tickets sync destinations' lists the teams a key
           can file into). The secret is the Linear API key.
  webhook  --url (https). The secret is the signing secret events are signed
           with; --unsigned creates the connection without one.

The secret is never taken as an argument. Pipe it on stdin with --secret-stdin,
or run in a terminal to be asked for it with the input hidden. The platform
stores it in its secret store and never returns it. Organisation admins only.`,
		example: `  cat linear-key.txt | ankra tickets sync connections create --name linear --provider linear \
    --team-id 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --secret-stdin
  ankra tickets sync connections create --name jira --provider jira --base-url https://example.atlassian.net \
    --project-key OPS --email ops@example.com --kind incident --min-priority high`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			name, nameError := requiredTextFlag(command, "name", "the connection's name")
			if nameError != nil {
				return nil, nameError
			}
			provider, _ := command.Flags().GetString("provider")
			request := client.TicketSyncConnectionRequest{Name: name, Provider: strings.TrimSpace(provider)}
			config, configError := ticketSyncConfigFromFlags(command, request.Provider)
			if configError != nil {
				return nil, configError
			}
			request.Config = config
			applyTicketSyncFilterFlags(command, &request)
			if disabled, _ := command.Flags().GetBool("disabled"); disabled {
				enabled := false
				request.Enabled = &enabled
			}
			unsigned, _ := command.Flags().GetBool("unsigned")
			if unsigned && request.Provider != "webhook" {
				return nil, withExitCode(exitUsage, fmt.Errorf("--unsigned belongs to a webhook connection; a %s connection needs its credential", request.Provider))
			}
			secret := ""
			if !unsigned {
				credential, readSecret, credentialError := readTicketSyncCredential(command, request.Provider)
				if credentialError != nil {
					return nil, credentialError
				}
				request.Credential, secret = credential, readSecret
			}
			document, createError := apiClient.CreateTicketSyncConnection(command.Context(), request)
			return document, redactSecret(ticketWriteError(createError), secret)
		},
		human: printTicketSyncConnection,
	})
	createCommand.Flags().String("name", "", "Connection name (required)")
	createCommand.Flags().String("provider", "", "jira, linear or webhook (required)")
	createCommand.Flags().Bool("disabled", false, "Create the connection switched off")
	createCommand.Flags().Bool("unsigned", false, "Webhook: create the connection without a signing secret")
	registerTicketSyncFilterFlags(createCommand)

	updateCommand := newOperationCommand(operationCommand{
		use:   "update <connection-id>",
		short: "Change a ticket sync connection, or rotate its credential",
		long: `Change a connection: only what you pass changes. --rotate-secret reads a new
credential from stdin (--secret-stdin) or a hidden prompt and replaces the
stored one; for Jira it needs --email too. An update also clears the
connection's failure state and retries delivery at once. Organisation admins
only.`,
		example: `  ankra tickets sync connections update 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --disable
  cat new-key.txt | ankra tickets sync connections update <connection-id> --rotate-secret --secret-stdin`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			connectionID, argumentError := requiredArgument(arguments, 0, "the connection id")
			if argumentError != nil {
				return nil, argumentError
			}
			enable, _ := command.Flags().GetBool("enable")
			disable, _ := command.Flags().GetBool("disable")
			if enable && disable {
				return nil, withExitCode(exitUsage, fmt.Errorf("pass --enable or --disable, not both"))
			}
			name, _ := command.Flags().GetString("name")
			request := client.TicketSyncConnectionRequest{Name: strings.TrimSpace(name)}
			applyTicketSyncFilterFlags(command, &request)
			if enable || disable {
				request.Enabled = &enable
			}
			currentDocument, readError := apiClient.GetTicketSyncConnection(command.Context(), connectionID)
			if readError != nil {
				return nil, readError
			}
			var current ticketSyncConnection
			if decodeError := decodeDocument(currentDocument, &current); decodeError != nil {
				return nil, decodeError
			}
			changedConfig, configError := ticketSyncConfigFromFlags(command, current.Provider)
			if configError != nil {
				return nil, configError
			}
			if len(changedConfig) > 0 {
				request.Config = map[string]string{}
				for key, value := range current.Config {
					if text, isText := value.(string); isText {
						request.Config[key] = text
					}
				}
				for key, value := range changedConfig {
					request.Config[key] = value
				}
			}
			secret := ""
			if rotate, _ := command.Flags().GetBool("rotate-secret"); rotate {
				credential, readSecret, credentialError := readTicketSyncCredential(command, current.Provider)
				if credentialError != nil {
					return nil, credentialError
				}
				request.Credential, secret = credential, readSecret
			} else if command.Flags().Changed("email") || command.Flags().Changed("secret-stdin") {
				return nil, withExitCode(exitUsage, fmt.Errorf("--email and --secret-stdin go with --rotate-secret"))
			}
			if request.Name == "" && request.Config == nil && request.Credential == nil && request.Enabled == nil &&
				request.SyncKinds == nil && request.SyncMinPriority == "" && request.SyncEventScope == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("nothing to change: pass at least one flag"))
			}
			document, updateError := apiClient.UpdateTicketSyncConnection(command.Context(), connectionID, request)
			return document, redactSecret(ticketWriteError(updateError), secret)
		},
		human: printTicketSyncConnection,
	})
	updateCommand.Flags().String("name", "", "A new name")
	updateCommand.Flags().Bool("enable", false, "Switch the connection on")
	updateCommand.Flags().Bool("disable", false, "Switch the connection off")
	updateCommand.Flags().Bool("rotate-secret", false, "Replace the stored credential with one read from stdin or a hidden prompt")
	registerTicketSyncFilterFlags(updateCommand)

	deleteCommand := newOperationCommand(operationCommand{
		use:   "delete <connection-id>",
		short: "Delete a ticket sync connection",
		long: `Delete a connection and its stored credential. Tickets stop syncing to its
tracker; issues already created there stay. Organisation admins only. It asks
first; --yes skips the question and is required when there is no terminal to
ask on.`,
		example:   "  ankra tickets sync connections delete 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --yes",
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			connectionID, argumentError := requiredArgument(arguments, 0, "the connection id")
			if argumentError != nil {
				return nil, argumentError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Deleting sync connection %s stops tickets syncing to its tracker and deletes its credential. It cannot be undone.",
				connectionID)); confirmError != nil {
				return nil, confirmError
			}
			if deleteError := apiClient.DeleteTicketSyncConnection(command.Context(), connectionID); deleteError != nil {
				return nil, deleteError
			}
			return acknowledgementDocument(map[string]any{"id": connectionID, "deleted": true})
		},
		human: func(out io.Writer, _ json.RawMessage) error {
			_, _ = fmt.Fprintln(out, "Sync connection deleted.")
			return nil
		},
	})
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	connectionsCommand.AddCommand(listCommand, getCommand, createCommand, updateCommand, deleteCommand)
	return connectionsCommand
}

func newTicketsSyncDestinationsCommand() *cobra.Command {
	destinationsCommand := newOperationCommand(operationCommand{
		use:   "destinations --provider <provider> [--connection <connection-id>]",
		short: "List where a tracker credential can file tickets",
		long: `List the destinations a tracker credential can reach, such as the Linear
teams it can file into, so a connection can target one. With --connection the
saved connection's stored credential is used; without it a credential is read
from stdin (--secret-stdin) or a hidden prompt, used for this one lookup and
not stored. Nothing is written. Organisation admins only.`,
		example: `  ankra tickets sync destinations --provider linear --connection 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c
  cat linear-key.txt | ankra tickets sync destinations --provider linear --secret-stdin`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			provider, providerError := requiredTextFlag(command, "provider", "jira, linear or webhook")
			if providerError != nil {
				return nil, providerError
			}
			if _, isProvider := ticketSyncConfigFlags[provider]; !isProvider {
				return nil, withExitCode(exitUsage, fmt.Errorf("--provider must be jira, linear or webhook, got %q", provider))
			}
			connectionID, _ := command.Flags().GetString("connection")
			request := client.TicketSyncDestinationsRequest{Provider: provider, ConnectionID: strings.TrimSpace(connectionID)}
			secret := ""
			if request.ConnectionID == "" {
				credential, readSecret, credentialError := readTicketSyncCredential(command, provider)
				if credentialError != nil {
					return nil, credentialError
				}
				request.Credential, secret = credential, readSecret
			} else if command.Flags().Changed("email") || command.Flags().Changed("secret-stdin") {
				return nil, withExitCode(exitUsage, fmt.Errorf("--connection uses the stored credential; leave --email and --secret-stdin off"))
			}
			document, listError := apiClient.ListTicketSyncDestinations(command.Context(), request)
			return document, redactSecret(ticketWriteError(listError), secret)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Destinations []struct {
					ID   string `json:"id"`
					Key  string `json:"key"`
					Name string `json:"name"`
				} `json:"destinations"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Destinations) == 0 {
				_, _ = fmt.Fprintln(out, "The credential reaches no destinations.")
				return nil
			}
			rows := make([]table.Row, 0, len(listing.Destinations))
			for _, destination := range listing.Destinations {
				rows = append(rows, table.Row{destination.ID, destination.Key, destination.Name})
			}
			renderRoundedTable(out, table.Row{"ID", "Key", "Name"}, rows)
			return nil
		},
	})
	destinationsCommand.Flags().String("provider", "", "jira, linear or webhook (required)")
	destinationsCommand.Flags().String("connection", "", "Use this saved connection's stored credential (its id)")
	destinationsCommand.Flags().String("email", "", "Jira: the Atlassian account the API token belongs to")
	destinationsCommand.Flags().Bool("secret-stdin", false, "Read the credential from stdin")
	return destinationsCommand
}

func newTicketsSyncCommand() *cobra.Command {
	syncCommand := &cobra.Command{
		Use:   "sync",
		Short: "Mirror tickets to Jira, Linear or a webhook",
		Long: `Ticket sync mirrors the AI board outbound: each connection creates one issue
per ticket in Jira or Linear, or posts signed events to a webhook, and relays
the ticket's timeline after. A connection filters what it syncs by kind and
priority. Delivery is automatic and at least once; nothing flows back from the
tracker, and tickets closed before a connection existed are not back-filled.`,
	}
	syncCommand.AddCommand(newTicketsSyncConnectionsCommand(), newTicketsSyncDestinationsCommand())
	return syncCommand
}

func init() {
	ticketsCmd.AddCommand(newTicketsAssignCommand(), newTicketsPlanCommand(), newTicketsWorkspaceCommand(), newTicketsSyncCommand())
}
