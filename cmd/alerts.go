package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var alertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Manage alert destinations and notification routes",
	Long: `Manage where Ankra delivers alerts and platform notifications.

Destinations are the endpoints notifications are sent to: a webhook URL
(Slack, Microsoft Teams, Discord, PagerDuty, or any custom receiver) or a
Slack/Teams channel the Ankra bot posts to directly. Routes decide which
notifications reach which destination, filtered by kind, severity, cluster,
and source, so the whole alerting setup can live in scripts and CI next to
the rest of your platform configuration.

  ankra alerts destinations list
  ankra alerts destinations create --name ops-slack --url https://hooks.slack.com/services/...
  ankra alerts destinations test <destination-id>
  ankra alerts routes create --destination-id <destination-id> --severity critical
  ankra alerts routes list -o json`,
}

var alertsDestinationsCmd = &cobra.Command{
	Use:   "destinations",
	Short: "Manage alert destinations (webhooks and chat channels)",
	Long: `Manage the organisation's alert destinations.

A destination is either a webhook URL or a channel-based Slack/Teams
destination that carries a channel id instead of a URL (list the channels
the Ankra bot can post to with 'ankra alerts destinations channels').
Webhook URLs are shown masked on every read.`,
}

var alertsDestinationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List alert destinations",
	Long: `List the organisation's alert destinations, 20 per page.

Filter by name with --search and by state with --enabled or --disabled. The
ID column is what routes and the other destination commands take.

  ankra alerts destinations list --search slack
  ankra alerts destinations list --disabled -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")

		list, listError := apiClient.ListAlertDestinations(client.ListAlertDestinationsOptions{
			Page:     page,
			PageSize: pageSize,
			Search:   search,
			Enabled:  enabledFromFlags(cmd),
		})
		if listError != nil {
			return fmt.Errorf("listing alert destinations: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if len(list.Items) == 0 {
			if search != "" {
				_, _ = fmt.Fprintf(out, "No alert destinations found matching %q.\n", search)
			} else {
				_, _ = fmt.Fprintln(out, "No alert destinations found.")
			}
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Name", "ID", "Type", "Target", "Enabled"})
		for _, destination := range list.Items {
			writer.AppendRow(table.Row{
				destination.Name,
				destination.ID,
				alertDestinationType(destination),
				alertDestinationTarget(destination),
				destination.Enabled,
			})
		}
		writer.Render()
		_, _ = fmt.Fprintf(out, "\nPage %d of %d (total %d)\n",
			list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
		return nil
	},
}

var alertsDestinationsGetCmd = &cobra.Command{
	Use:   "get <destination-id>",
	Short: "Show one alert destination",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destination, getError := apiClient.GetAlertDestination(args[0])
		if getError != nil {
			return fmt.Errorf("getting alert destination: %w", getError)
		}
		if rendered, renderError := renderStructured(cmd, destination); rendered || renderError != nil {
			return renderError
		}
		printAlertDestination(cmd.OutOrStdout(), destination)
		return nil
	},
}

var alertsDestinationsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an alert destination",
	Long: `Create an alert destination.

Pass --url for a webhook destination, or --channel-id for a channel-based
Slack or Teams destination (find ids with 'ankra alerts destinations
channels'); a Teams channel also needs --teams-tenant-id. --type records
which receiver the destination targets (slack, teams, discord, pagerduty,
custom; default slack) and selects the default payload format;
--template-file overrides the payload with your own template.

  ankra alerts destinations create --name ops-slack --url https://hooks.slack.com/services/...
  ankra alerts destinations create --name oncall --type pagerduty --url https://events.pagerduty.com/...
  ankra alerts destinations create --name ops-teams --type teams --channel-id 19:abc@thread.tacv2 --teams-tenant-id <tenant>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := mustFlagString(cmd, "name")
		webhookURL := mustFlagString(cmd, "url")
		channelID := mustFlagString(cmd, "channel-id")
		if webhookURL == "" && channelID == "" {
			return withExitCode(exitUsage, errors.New("either --url or --channel-id is required"))
		}
		template, templateError := templateFromFlags(cmd)
		if templateError != nil {
			return templateError
		}
		request := client.CreateAlertDestinationRequest{
			Name:            name,
			URL:             changedStringFlag(cmd, "url"),
			ChannelID:       changedStringFlag(cmd, "channel-id"),
			ChannelName:     changedStringFlag(cmd, "channel-name"),
			TeamsTenantID:   changedStringFlag(cmd, "teams-tenant-id"),
			IntegrationType: changedStringFlag(cmd, "type"),
			Description:     changedStringFlag(cmd, "description"),
			Template:        template,
			Enabled:         enabledFromFlags(cmd),
		}
		destination, createError := apiClient.CreateAlertDestination(request)
		if createError != nil {
			return fmt.Errorf("creating alert destination: %w", createError)
		}
		if rendered, renderError := renderStructured(cmd, destination); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert destination %q created (%s).\n", destination.Name, destination.ID)
		return nil
	},
}

var alertsDestinationsUpdateCmd = &cobra.Command{
	Use:   "update <destination-id>",
	Short: "Update an alert destination",
	Long: `Update an alert destination. Only the flags you pass are changed; the
rest keep their current values.

  ankra alerts destinations update <destination-id> --url https://hooks.slack.com/services/new
  ankra alerts destinations update <destination-id> --disabled`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		template, templateError := templateFromFlags(cmd)
		if templateError != nil {
			return templateError
		}
		request := client.UpdateAlertDestinationRequest{
			Name:          changedStringFlag(cmd, "name"),
			URL:           changedStringFlag(cmd, "url"),
			ChannelID:     changedStringFlag(cmd, "channel-id"),
			ChannelName:   changedStringFlag(cmd, "channel-name"),
			TeamsTenantID: changedStringFlag(cmd, "teams-tenant-id"),
			Description:   changedStringFlag(cmd, "description"),
			Template:      template,
			Enabled:       enabledFromFlags(cmd),
		}
		if request == (client.UpdateAlertDestinationRequest{}) {
			return withExitCode(exitUsage, errors.New("nothing to update: pass at least one flag"))
		}
		destination, updateError := apiClient.UpdateAlertDestination(args[0], request)
		if updateError != nil {
			return fmt.Errorf("updating alert destination: %w", updateError)
		}
		if rendered, renderError := renderStructured(cmd, destination); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert destination %q updated (%s).\n", destination.Name, destination.ID)
		return nil
	},
}

var alertsDestinationsDeleteCmd = &cobra.Command{
	Use:   "delete <destination-id>",
	Short: "Delete an alert destination",
	Long: `Delete an alert destination. Routes that pointed at it stop delivering;
remove or re-point them with 'ankra alerts routes'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destinationID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete alert destination %q? [y/N]: ", destinationID), yes); confirmError != nil {
			return confirmError
		}
		if _, deleteError := apiClient.DeleteAlertDestination(destinationID); deleteError != nil {
			return fmt.Errorf("deleting alert destination: %w", deleteError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert destination %s deleted.\n", destinationID)
		return nil
	},
}

var alertsDestinationsTestCmd = &cobra.Command{
	Use:   "test <destination-id>",
	Short: "Send a test notification to a destination",
	Long: `Send a sample notification to a stored destination and report whether
the receiver accepted it. A failed delivery exits non-zero so CI can gate
on it; the details are still printed (or emitted with -o json).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, testError := apiClient.TestAlertDestination(args[0])
		if testError != nil {
			return fmt.Errorf("testing alert destination: %w", testError)
		}
		return reportAlertDestinationTest(cmd, result)
	},
}

var alertsDestinationsTestURLCmd = &cobra.Command{
	Use:   "test-url",
	Short: "Send a test notification to a webhook URL before storing it",
	Long: `Send a sample notification to an ad-hoc webhook URL, optionally with a
custom payload template, without creating a destination. A failed delivery
exits non-zero.

  ankra alerts destinations test-url --url https://hooks.slack.com/services/...
  ankra alerts destinations test-url --url https://example.com/hook --template-file payload.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		template, templateError := templateFromFlags(cmd)
		if templateError != nil {
			return templateError
		}
		result, testError := apiClient.TestAlertDestinationURL(client.TestAlertDestinationURLRequest{
			URL:      mustFlagString(cmd, "url"),
			Template: template,
		})
		if testError != nil {
			return fmt.Errorf("testing webhook url: %w", testError)
		}
		return reportAlertDestinationTest(cmd, result)
	},
}

var alertsDestinationsChannelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "List the Slack and Teams channels available for channel-based destinations",
	Long: `List the channels the Ankra bot can post to, for use with
'ankra alerts destinations create --channel-id'. Both providers are shown
unless --provider narrows it to one.

A provider whose workspace or tenant is not connected to the organisation
reads "not connected"; one whose bot service is not configured on this
platform reads "not available". Neither is an error.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		switch provider {
		case "", "slack", "teams":
		default:
			return withExitCode(exitUsage, fmt.Errorf("unsupported --provider %q (use slack or teams)", provider))
		}

		var result alertChannelsResult
		notices := make([]string, 0, 2)
		if provider != "teams" {
			slack, slackError := apiClient.ListSlackChannels()
			state, classifyError := channelPickerState(slackError)
			if classifyError != nil {
				return fmt.Errorf("listing Slack channels: %w", classifyError)
			}
			if state != "" {
				notices = append(notices, "Slack: "+state)
			}
			result.Slack = slack
		}
		if provider != "slack" {
			teams, teamsError := apiClient.ListTeamsChannels()
			state, classifyError := channelPickerState(teamsError)
			if classifyError != nil {
				return fmt.Errorf("listing Teams channels: %w", classifyError)
			}
			if state != "" {
				notices = append(notices, "Teams: "+state)
			}
			result.Teams = teams
		}

		rendered, renderError := renderStructured(cmd, result)
		if renderError != nil {
			return renderError
		}
		if rendered {
			for _, notice := range notices {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), notice)
			}
			return nil
		}
		out := cmd.OutOrStdout()
		if result.Slack != nil {
			printSlackChannels(out, result.Slack)
		}
		if result.Teams != nil {
			printTeamsChannels(out, result.Teams)
		}
		for _, notice := range notices {
			_, _ = fmt.Fprintln(out, notice)
		}
		return nil
	},
}

// alertChannelsResult is the structured shape of `destinations channels`.
// A provider that was not requested, is not connected, or is not available
// answers null; the reason is written to stderr so stdout stays parseable.
type alertChannelsResult struct {
	Slack *client.SlackChannelList `json:"slack" yaml:"slack"`
	Teams *client.TeamsChannelList `json:"teams" yaml:"teams"`
}

// channelPickerState turns the channel picker's two "no picker" answers
// into a human state: 404 means no workspace or tenant is connected to the
// organisation, 503 means the bot service is not configured on this
// platform. Any other error is passed back for the caller to return.
func channelPickerState(listError error) (string, error) {
	if listError == nil {
		return "", nil
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(listError, &unexpected) {
		switch unexpected.StatusCode {
		case http.StatusNotFound:
			return fmt.Sprintf("not connected (%s)", unexpected.Error()), nil
		case http.StatusServiceUnavailable:
			return fmt.Sprintf("not available (%s)", unexpected.Error()), nil
		}
	}
	return "", listError
}

func printSlackChannels(out io.Writer, list *client.SlackChannelList) {
	heading := "Slack"
	if list.TeamName != nil && *list.TeamName != "" {
		heading += " (workspace " + *list.TeamName + ")"
	}
	_, _ = fmt.Fprintf(out, "%s:\n", heading)
	if len(list.Channels) == 0 {
		_, _ = fmt.Fprintln(out, "  No channels the Ankra bot can post to.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Name", "ID", "Private"})
	for _, channel := range list.Channels {
		writer.AppendRow(table.Row{channel.Name, channel.ID, channel.IsPrivate})
	}
	writer.Render()
}

func printTeamsChannels(out io.Writer, list *client.TeamsChannelList) {
	_, _ = fmt.Fprintln(out, "Teams:")
	if len(list.Channels) == 0 {
		_, _ = fmt.Fprintln(out, "  No channels the Ankra bot is installed in.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Name", "ID", "Team", "Tenant"})
	for _, channel := range list.Channels {
		writer.AppendRow(table.Row{channel.Name, channel.ID, channel.TeamName, channel.TenantID})
	}
	writer.Render()
}

// alertDestinationType names the delivery mechanism, since the read shape
// carries no receiver type: a destination with a channel id posts through
// the Ankra bot, everything else is a webhook.
func alertDestinationType(destination client.AlertDestination) string {
	if destination.ChannelID != nil && *destination.ChannelID != "" {
		return "channel"
	}
	return "webhook"
}

// alertDestinationTarget is the human label of where a destination
// delivers: the channel name (or id) for channel destinations, the masked
// URL for webhooks.
func alertDestinationTarget(destination client.AlertDestination) string {
	if destination.ChannelName != nil && *destination.ChannelName != "" {
		return *destination.ChannelName
	}
	if destination.ChannelID != nil && *destination.ChannelID != "" {
		return *destination.ChannelID
	}
	if destination.URL != nil && *destination.URL != "" {
		return *destination.URL
	}
	return "-"
}

func printAlertDestination(out io.Writer, destination *client.AlertDestination) {
	_, _ = fmt.Fprintf(out, "Name:        %s\n", destination.Name)
	_, _ = fmt.Fprintf(out, "ID:          %s\n", destination.ID)
	_, _ = fmt.Fprintf(out, "Type:        %s\n", alertDestinationType(*destination))
	_, _ = fmt.Fprintf(out, "Target:      %s\n", alertDestinationTarget(*destination))
	if destination.Description != nil && *destination.Description != "" {
		_, _ = fmt.Fprintf(out, "Description: %s\n", *destination.Description)
	}
	if destination.Template != nil && *destination.Template != "" {
		_, _ = fmt.Fprintln(out, "Template:    custom (use -o json to view)")
	}
	_, _ = fmt.Fprintf(out, "Enabled:     %t\n", destination.Enabled)
	_, _ = fmt.Fprintf(out, "Created:     %s (%s)\n", destination.CreatedAt, formatTimeAgo(destination.CreatedAt))
	_, _ = fmt.Fprintf(out, "Updated:     %s (%s)\n", destination.UpdatedAt, formatTimeAgo(destination.UpdatedAt))
}

// reportAlertDestinationTest renders a test outcome and turns a failed
// delivery into a non-zero exit, so a pipeline can gate on the receiver
// being reachable. With -o the structured result is still written first.
func reportAlertDestinationTest(cmd *cobra.Command, result *client.AlertDestinationTestResult) error {
	rendered, renderError := renderStructured(cmd, result)
	if renderError != nil {
		return renderError
	}
	summary := alertDestinationTestSummary(result)
	if !result.Success {
		return fmt.Errorf("test delivery failed (%s)", summary)
	}
	if !rendered {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Test delivery succeeded (%s).\n", summary)
	}
	return nil
}

func alertDestinationTestSummary(result *client.AlertDestinationTestResult) string {
	parts := make([]string, 0, 3)
	if result.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("HTTP %d", *result.StatusCode))
	}
	if result.ResponseTimeMS != nil {
		parts = append(parts, fmt.Sprintf("%.0f ms", *result.ResponseTimeMS))
	}
	if result.Error != nil && *result.Error != "" {
		parts = append(parts, *result.Error)
	}
	if len(parts) == 0 {
		return "no response"
	}
	return strings.Join(parts, ", ")
}

// templateFromFlags reads --template-file into the payload template member;
// nil when the flag was not passed so the backend keeps its default.
func templateFromFlags(cmd *cobra.Command) (*string, error) {
	path := mustFlagString(cmd, "template-file")
	if path == "" {
		return nil, nil
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil, fmt.Errorf("reading template file %q: %w", path, readError)
	}
	template := string(content)
	return &template, nil
}

// enabledFromFlags resolves the --enabled/--disabled pair to a tri-state:
// nil when neither was passed, so lists stay unfiltered and updates leave
// the current state alone. Commands that expose only --disabled resolve the
// same way.
func enabledFromFlags(cmd *cobra.Command) *bool {
	if cmd.Flags().Changed("enabled") {
		enabled := mustFlagBool(cmd, "enabled")
		return &enabled
	}
	if cmd.Flags().Changed("disabled") {
		enabled := !mustFlagBool(cmd, "disabled")
		return &enabled
	}
	return nil
}

// changedStringFlag returns the flag's value only when it was passed, so
// request members the user did not mention are left off the wire.
func changedStringFlag(cmd *cobra.Command, name string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value := mustFlagString(cmd, name)
	return &value
}

func changedIntFlag(cmd *cobra.Command, name string) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value := mustFlagInt(cmd, name)
	return &value
}

func changedBoolFlag(cmd *cobra.Command, name string) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value := mustFlagBool(cmd, name)
	return &value
}

func init() {
	alertsDestinationsListCmd.Flags().String("search", "", "Filter destinations by name")
	alertsDestinationsListCmd.Flags().Bool("enabled", false, "Only enabled destinations")
	alertsDestinationsListCmd.Flags().Bool("disabled", false, "Only disabled destinations")
	alertsDestinationsListCmd.Flags().Int("page", 1, "Page number")
	alertsDestinationsListCmd.Flags().Int("page-size", 20, "Destinations per page (max 100)")
	alertsDestinationsListCmd.MarkFlagsMutuallyExclusive("enabled", "disabled")

	alertsDestinationsCreateCmd.Flags().String("name", "", "Destination name, unique within the organisation")
	alertsDestinationsCreateCmd.Flags().String("url", "", "Webhook URL to deliver to")
	alertsDestinationsCreateCmd.Flags().String("channel-id", "", "Slack or Teams channel id for a channel-based destination (instead of --url)")
	alertsDestinationsCreateCmd.Flags().String("channel-name", "", "Display name of the channel behind --channel-id")
	alertsDestinationsCreateCmd.Flags().String("teams-tenant-id", "", "Microsoft Teams tenant id (required with --channel-id for --type teams)")
	alertsDestinationsCreateCmd.Flags().String("type", "", "Receiver type: slack, teams, discord, pagerduty, or custom (default slack)")
	alertsDestinationsCreateCmd.Flags().String("description", "", "Free-text description")
	alertsDestinationsCreateCmd.Flags().String("template-file", "", "File holding a custom payload template")
	alertsDestinationsCreateCmd.Flags().Bool("disabled", false, "Create the destination disabled")
	_ = alertsDestinationsCreateCmd.MarkFlagRequired("name")

	alertsDestinationsUpdateCmd.Flags().String("name", "", "New destination name")
	alertsDestinationsUpdateCmd.Flags().String("url", "", "New webhook URL")
	alertsDestinationsUpdateCmd.Flags().String("channel-id", "", "New Slack or Teams channel id")
	alertsDestinationsUpdateCmd.Flags().String("channel-name", "", "New display name of the channel")
	alertsDestinationsUpdateCmd.Flags().String("teams-tenant-id", "", "New Microsoft Teams tenant id")
	alertsDestinationsUpdateCmd.Flags().String("description", "", "New description")
	alertsDestinationsUpdateCmd.Flags().String("template-file", "", "File holding the new payload template")
	alertsDestinationsUpdateCmd.Flags().Bool("enabled", false, "Enable the destination")
	alertsDestinationsUpdateCmd.Flags().Bool("disabled", false, "Disable the destination")
	alertsDestinationsUpdateCmd.MarkFlagsMutuallyExclusive("enabled", "disabled")

	alertsDestinationsDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	alertsDestinationsTestURLCmd.Flags().String("url", "", "Webhook URL to send the test notification to")
	alertsDestinationsTestURLCmd.Flags().String("template-file", "", "File holding a custom payload template")
	_ = alertsDestinationsTestURLCmd.MarkFlagRequired("url")

	alertsDestinationsChannelsCmd.Flags().String("provider", "", "Only one provider: slack or teams (default both)")

	registerStructuredOutputFlags(
		alertsDestinationsListCmd,
		alertsDestinationsGetCmd,
		alertsDestinationsCreateCmd,
		alertsDestinationsUpdateCmd,
		alertsDestinationsTestCmd,
		alertsDestinationsTestURLCmd,
		alertsDestinationsChannelsCmd,
	)

	alertsDestinationsCmd.AddCommand(alertsDestinationsListCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsGetCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsCreateCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsUpdateCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsDeleteCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsTestCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsTestURLCmd)
	alertsDestinationsCmd.AddCommand(alertsDestinationsChannelsCmd)
	alertsCmd.AddCommand(alertsDestinationsCmd)
	rootCmd.AddCommand(alertsCmd)
}
