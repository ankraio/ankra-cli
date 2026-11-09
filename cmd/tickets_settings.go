package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var ticketsSettingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Configure the AI board",
}

var ticketsSettingsProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Give a project its own Slack and Teams channels for AI board threads",
	Long: `Give a project its own Slack channel and Teams conversation for AI board
threads.

A ticket of a project with a channel of its own opens its board thread
there, per provider; every other ticket, and a provider the project leaves
unset, uses the organisation's AI board channel. Changing these needs an
organisation admin.

Slack channel ids and Teams conversation ids are listed by
'ankra alerts destinations channels'.

  ankra tickets settings projects list
  ankra tickets settings projects set core --slack-channel C0123ABCD
  ankra tickets settings projects set tools --teams-conversation 19:abc@thread.tacv2
  ankra tickets settings projects set core --clear-teams
  ankra tickets settings projects remove tools`,
}

var ticketsSettingsProjectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the projects that have AI board channels of their own",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, listError := apiClient.ListTicketProjectChannels()
		if listError != nil {
			return fmt.Errorf("listing project AI board channels: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if len(list.Projects) == 0 {
			_, _ = fmt.Fprintln(out, "No project has AI board channels of its own; every ticket uses the organisation's channels.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Project", "Project ID", "Slack channel", "Teams conversation", "Updated"})
		for _, project := range list.Projects {
			writer.AppendRow(table.Row{
				project.ProjectName,
				project.ProjectID,
				channelOrOrganisation(project.SlackChannelID),
				channelOrOrganisation(project.TeamsConversationID),
				project.UpdatedAt,
			})
		}
		writer.Render()
		return nil
	},
}

var ticketsSettingsProjectsSetCmd = &cobra.Command{
	Use:   "set <project>",
	Short: "Bind or clear a project's AI board channels",
	Long: `Bind or clear a project's AI board channels. <project> is the project's id,
slug or name. Only the providers you pass change; --clear-slack and
--clear-teams drop that provider's channel so the project falls back to the
organisation's.

  ankra tickets settings projects set core --slack-channel C0123ABCD --teams-conversation 19:abc@thread.tacv2
  ankra tickets settings projects set core --clear-slack`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		request, requestError := ticketProjectChannelsRequestFromFlags(cmd)
		if requestError != nil {
			return requestError
		}
		projectID, resolveError := resolveProjectReferenceToID(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, setError := apiClient.SetTicketProjectChannels(projectID, request)
		if setError != nil {
			return fmt.Errorf("setting the AI board channels of project %s: %w", args[0], setError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		printTicketProjectChannelsResult(cmd.OutOrStdout(), args[0], result)
		return nil
	},
}

var ticketsSettingsProjectsRemoveCmd = &cobra.Command{
	Use:   "remove <project>",
	Short: "Drop a project's own AI board channels",
	Long: `Drop every AI board channel a project binds, so its tickets open their
threads in the organisation's channels again. <project> is the project's id,
slug or name.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, resolveError := resolveProjectReferenceToID(args[0])
		if resolveError != nil {
			return resolveError
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Drop the AI board channels of project %q? [y/N]: ", args[0]), yes); confirmError != nil {
			return confirmError
		}
		if removeError := apiClient.RemoveTicketProjectChannels(projectID); removeError != nil {
			return fmt.Errorf("removing the AI board channels of project %s: %w", args[0], removeError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Project %s has no AI board channels of its own any more; its tickets use the organisation's channels.\n", args[0])
		return nil
	},
}

// ticketProjectChannelsRequestFromFlags builds the PUT body from the flags,
// refusing a combination the backend would refuse or that contradicts
// itself, so it exits with the usage code instead of a 422 body.
func ticketProjectChannelsRequestFromFlags(cmd *cobra.Command) (client.SetTicketProjectChannelsRequest, error) {
	var request client.SetTicketProjectChannelsRequest
	clearSlack, _ := cmd.Flags().GetBool("clear-slack")
	clearTeams, _ := cmd.Flags().GetBool("clear-teams")
	if cmd.Flags().Changed("slack-channel") {
		slackChannelID := strings.TrimSpace(mustFlagString(cmd, "slack-channel"))
		if slackChannelID == "" {
			return request, withExitCode(exitUsage, errors.New("--slack-channel needs a channel id (use --clear-slack to drop the channel)"))
		}
		request.SlackChannelID = &slackChannelID
		request.SlackChannelIDSet = true
	} else if clearSlack {
		request.SlackChannelIDSet = true
	}
	if cmd.Flags().Changed("teams-conversation") {
		teamsConversationID := strings.TrimSpace(mustFlagString(cmd, "teams-conversation"))
		if teamsConversationID == "" {
			return request, withExitCode(exitUsage, errors.New("--teams-conversation needs a conversation id (use --clear-teams to drop the conversation)"))
		}
		request.TeamsConversationID = &teamsConversationID
		request.TeamsConversationIDSet = true
	} else if clearTeams {
		request.TeamsConversationIDSet = true
	}
	if !request.SlackChannelIDSet && !request.TeamsConversationIDSet {
		return request, withExitCode(exitUsage,
			errors.New("nothing to change: pass --slack-channel, --teams-conversation, --clear-slack or --clear-teams"))
	}
	return request, nil
}

func printTicketProjectChannelsResult(out io.Writer, projectReference string, result *client.SetTicketProjectChannelsResult) {
	if result.Channels == nil {
		_, _ = fmt.Fprintf(out,
			"Project %s has no AI board channels of its own any more; its tickets use the organisation's channels.\n", projectReference)
		return
	}
	_, _ = fmt.Fprintf(out, "AI board channels of project %s updated.\n", projectReference)
	_, _ = fmt.Fprintf(out, "Slack channel:      %s\n", channelOrOrganisation(result.Channels.SlackChannelID))
	_, _ = fmt.Fprintf(out, "Teams conversation: %s\n", channelOrOrganisation(result.Channels.TeamsConversationID))
}

// channelOrOrganisation renders a provider the project leaves unset as the
// fallback it gets.
func channelOrOrganisation(channelID *string) string {
	if channelID == nil || *channelID == "" {
		return "organisation channel"
	}
	return *channelID
}

func init() {
	ticketsSettingsProjectsSetCmd.Flags().String("slack-channel", "", "Slack channel id for this project's AI board threads (for example C0123ABCD)")
	ticketsSettingsProjectsSetCmd.Flags().String("teams-conversation", "", "Teams conversation id for this project's AI board threads (19:...@thread.tacv2)")
	ticketsSettingsProjectsSetCmd.Flags().Bool("clear-slack", false, "Drop the project's Slack channel (falls back to the organisation's)")
	ticketsSettingsProjectsSetCmd.Flags().Bool("clear-teams", false, "Drop the project's Teams conversation (falls back to the organisation's)")
	ticketsSettingsProjectsSetCmd.MarkFlagsMutuallyExclusive("slack-channel", "clear-slack")
	ticketsSettingsProjectsSetCmd.MarkFlagsMutuallyExclusive("teams-conversation", "clear-teams")

	ticketsSettingsProjectsRemoveCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(ticketsSettingsProjectsListCmd, ticketsSettingsProjectsSetCmd)

	ticketsSettingsProjectsCmd.AddCommand(ticketsSettingsProjectsListCmd)
	ticketsSettingsProjectsCmd.AddCommand(ticketsSettingsProjectsSetCmd)
	ticketsSettingsProjectsCmd.AddCommand(ticketsSettingsProjectsRemoveCmd)
	ticketsSettingsCmd.AddCommand(ticketsSettingsProjectsCmd)
	ticketsCmd.AddCommand(ticketsSettingsCmd)
}
