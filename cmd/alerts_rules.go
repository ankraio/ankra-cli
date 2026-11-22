package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"ankra/internal/client"
)

// newAlertsRulesCommand builds `ankra alerts rules`: the organisation's
// alert definitions, the rules Ankra evaluates and opens incidents from.
func newAlertsRulesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "rules",
		Aliases: []string{"rule"},
		Short:   "Manage alert definitions: list, get, create, update, delete, enable, disable",
		Long: `Manage the organisation's alert definitions: the rules Ankra evaluates
against your clusters and opens incidents from.

Reading needs alerts.read. Creating, editing and deleting a definition needs
alerts.manage; muting one (disable) and re-enabling it needs alerts.write,
the same as acknowledging or snoozing a firing.

  ankra alerts rules list --enabled
  ankra alerts rules create -f alert.yaml
  ankra alerts rules disable <alert-id>
  ankra alerts rules delete <alert-id> --yes`,
	}
	command.AddCommand(newAlertsRulesListCommand(), newAlertsRulesGetCommand(), newAlertsRulesCreateCommand(),
		newAlertsRulesUpdateCommand(), newAlertsRulesDeleteCommand(),
		newAlertsRulesSetEnabledCommand(true), newAlertsRulesSetEnabledCommand(false))
	return command
}

func newAlertsRulesListCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List alert definitions",
		Long: `List the organisation's alert definitions, 20 per page.

Filter by name with --search, by state with --enabled or --disabled, and by
the rules' --severity, --resource-type or --cluster-id.

  ankra alerts rules list --search disk
  ankra alerts rules list --disabled -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			page, _ := cmd.Flags().GetInt("page")
			pageSize, _ := cmd.Flags().GetInt("page-size")
			search, _ := cmd.Flags().GetString("search")
			severity, _ := cmd.Flags().GetString("severity")
			resourceType, _ := cmd.Flags().GetString("resource-type")
			clusterID, _ := cmd.Flags().GetString("cluster-id")
			list, listError := apiClient.ListAlertRules(client.ListAlertRulesOptions{
				Page:         page,
				PageSize:     pageSize,
				Search:       search,
				Enabled:      enabledFromFlags(cmd),
				Severity:     severity,
				ResourceType: resourceType,
				ClusterID:    clusterID,
			})
			if listError != nil {
				return fmt.Errorf("listing alert definitions: %w", listError)
			}
			if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
				return renderError
			}
			out := cmd.OutOrStdout()
			if len(list.Items) == 0 {
				_, _ = fmt.Fprintln(out, "No alert definitions found.")
				return nil
			}
			writer := table.NewWriter()
			writer.SetOutputMirror(out)
			writer.SetStyle(table.StyleRounded)
			writer.AppendHeader(table.Row{"Name", "ID", "Severity", "Enabled", "Status", "Rules", "Last triggered"})
			for _, alertRule := range list.Items {
				writer.AppendRow(table.Row{alertRule.Name, alertRule.ID, alertRule.Severity, alertRule.Enabled,
					alertRule.Status, alertRule.RuleCount, stringValue(alertRule.LastTriggeredAt)})
			}
			writer.Render()
			_, _ = fmt.Fprintf(out, "\nPage %d of %d (total %d)\n",
				list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
			return nil
		},
	}
	command.Flags().Int("page", 1, "Page number")
	command.Flags().Int("page-size", 20, "Alert definitions per page (at most 100)")
	command.Flags().String("search", "", "Only definitions whose name contains this text")
	command.Flags().Bool("enabled", false, "Only enabled definitions")
	command.Flags().Bool("disabled", false, "Only muted definitions")
	command.Flags().String("severity", "", "Only definitions with a rule of this severity")
	command.Flags().String("resource-type", "", "Only definitions with a rule on this resource type")
	command.Flags().String("cluster-id", "", "Only definitions whose rules apply to this cluster")
	command.MarkFlagsMutuallyExclusive("enabled", "disabled")
	registerStructuredOutputFlags(command)
	return command
}

func newAlertsRulesGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get <alert-id>",
		Short: "Show one alert definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			detail, getError := apiClient.GetAlertRule(args[0])
			if getError != nil {
				return fmt.Errorf("getting alert definition: %w", getError)
			}
			if rendered, renderError := renderStructured(cmd, detail); rendered || renderError != nil {
				return renderError
			}
			printAlertRule(cmd.OutOrStdout(), &detail.Item)
			if len(detail.Tickets) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Tickets:     %d\n", len(detail.Tickets))
			}
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newAlertsRulesCreateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "create -f <file>",
		Short: "Create an alert definition from a YAML or JSON file",
		Long: `Create an alert definition. The file carries the platform's create body,
in YAML or JSON: name, description, enabled, rules (each with name,
resource_type, conditions, severity and the clusters it applies to) and the
integration_names of the destinations to notify. Pass -f - to read stdin.

  name: node-disk-pressure
  enabled: true
  integration_names: [ops-slack]
  rules:
    - name: disk pressure
      resource_type: node
      all_clusters: true
      severity: critical
      conditions:
        - condition_type: status
          condition_operator: equals
          condition_value: DiskPressure

  ankra alerts rules create -f alert.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			definition, readError := readAlertRuleDocument(cmd)
			if readError != nil {
				return readError
			}
			if name, _ := definition["name"].(string); strings.TrimSpace(name) == "" {
				return withExitCode(exitUsage, errors.New("the alert definition needs a name"))
			}
			alertRule, createError := apiClient.CreateAlertRule(definition)
			if createError != nil {
				return fmt.Errorf("creating alert definition: %w", createError)
			}
			if rendered, renderError := renderStructured(cmd, alertRule); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert definition %q created (%s).\n", alertRule.Name, alertRule.ID)
			return nil
		},
	}
	command.Flags().StringP("file", "f", "", "YAML or JSON file with the alert definition (- reads stdin)")
	_ = command.MarkFlagRequired("file")
	registerStructuredOutputFlags(command)
	return command
}

func newAlertsRulesUpdateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "update <alert-id> -f <file>",
		Short: "Change an alert definition from a YAML or JSON file",
		Long: `Change an alert definition. The file names only the members to change
(name, description, enabled, rules, integration_names); the rest are kept.
A rules member replaces the definition's whole rule set.

  ankra alerts rules update <alert-id> -f changes.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changes, readError := readAlertRuleDocument(cmd)
			if readError != nil {
				return readError
			}
			if len(changes) == 0 {
				return withExitCode(exitUsage, errors.New("nothing to change: the file names no member"))
			}
			alertRule, updateError := apiClient.UpdateAlertRule(args[0], changes)
			if updateError != nil {
				return fmt.Errorf("updating alert definition: %w", updateError)
			}
			if rendered, renderError := renderStructured(cmd, alertRule); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert definition %q updated (%s).\n", alertRule.Name, alertRule.ID)
			return nil
		},
	}
	command.Flags().StringP("file", "f", "", "YAML or JSON file with the members to change (- reads stdin)")
	_ = command.MarkFlagRequired("file")
	registerStructuredOutputFlags(command)
	return command
}

func newAlertsRulesDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete <alert-id>",
		Short: "Delete an alert definition and its rules",
		Long: `Delete an alert definition and its rules. Its incident history stops being
evaluated; to stop it notifying while keeping it, use 'ankra alerts rules
disable' instead.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
				fmt.Sprintf("Delete alert definition %q and its rules? [y/N]: ", args[0]), yes); confirmError != nil {
				return confirmError
			}
			result, deleteError := apiClient.DeleteAlertRule(args[0])
			if deleteError != nil {
				return fmt.Errorf("deleting alert definition: %w", deleteError)
			}
			if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert definition %s deleted.\n", args[0])
			return nil
		},
	}
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(command)
	return command
}

// newAlertsRulesSetEnabledCommand builds `enable` (enabled true) or
// `disable` (false). Disabling mutes the alert: its history is kept, any open
// incident is settled, and nothing new fires until it is enabled again.
func newAlertsRulesSetEnabledCommand(enabled bool) *cobra.Command {
	verb, pastTense, short := "enable", "enabled", "Re-enable a muted alert definition"
	if !enabled {
		verb, pastTense, short = "disable", "disabled", "Mute an alert definition; its history is kept"
	}
	command := &cobra.Command{
		Use:   verb + " <alert-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alertRule, setError := apiClient.SetAlertRuleEnabled(args[0], enabled)
			if setError != nil {
				return fmt.Errorf("setting alert definition %s: %w", pastTense, setError)
			}
			if rendered, renderError := renderStructured(cmd, alertRule); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert definition %q %s.\n", alertRule.Name, pastTense)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

// newAlertsTriggersCommand builds `ankra alerts triggers`: acting on one
// firing of an alert.
func newAlertsTriggersCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "triggers",
		Short: "Acknowledge or snooze one firing of an alert",
		Long: `Act on one firing (trigger) of an alert definition. Both need alerts.write.

  ankra alerts triggers acknowledge <alert-id> <trigger-id> --notes "looking"
  ankra alerts triggers snooze <alert-id> <trigger-id> --minutes 120`,
	}

	acknowledge := &cobra.Command{
		Use:   "acknowledge <alert-id> <trigger-id>",
		Short: "Acknowledge a firing",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			notes, _ := cmd.Flags().GetString("notes")
			result, actionError := apiClient.AcknowledgeAlertTrigger(args[0], args[1], notes)
			if actionError != nil {
				return fmt.Errorf("acknowledging trigger: %w", actionError)
			}
			if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Trigger %s acknowledged.\n", args[1])
			return nil
		},
	}
	acknowledge.Flags().String("notes", "", "Notes recorded on the trigger")

	snooze := &cobra.Command{
		Use:   "snooze <alert-id> <trigger-id> --minutes <n>",
		Short: "Snooze a firing for a number of minutes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			minutes, _ := cmd.Flags().GetInt("minutes")
			if minutes <= 0 {
				return withExitCode(exitUsage, errors.New("--minutes must be a positive number of minutes"))
			}
			notes, _ := cmd.Flags().GetString("notes")
			result, actionError := apiClient.SnoozeAlertTrigger(args[0], args[1], minutes, notes)
			if actionError != nil {
				return fmt.Errorf("snoozing trigger: %w", actionError)
			}
			if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Trigger %s snoozed for %d minutes.\n", args[1], minutes)
			return nil
		},
	}
	snooze.Flags().Int("minutes", 0, "How long to snooze the firing, in minutes")
	snooze.Flags().String("notes", "", "Notes recorded on the trigger")
	_ = snooze.MarkFlagRequired("minutes")

	registerStructuredOutputFlags(acknowledge, snooze)
	command.AddCommand(acknowledge, snooze)
	return command
}

// readAlertRuleDocument reads the -f file (or stdin for -) as a YAML or JSON
// object.
func readAlertRuleDocument(cmd *cobra.Command) (map[string]any, error) {
	path, _ := cmd.Flags().GetString("file")
	var content []byte
	var readError error
	if path == "-" {
		content, readError = io.ReadAll(cmd.InOrStdin())
	} else {
		content, readError = os.ReadFile(path)
	}
	if readError != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("reading %s: %w", path, readError))
	}
	document := map[string]any{}
	if decodeError := yaml.Unmarshal(content, &document); decodeError != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is not a YAML or JSON object: %w", path, decodeError))
	}
	return document, nil
}

func printAlertRule(out io.Writer, alertRule *client.AlertRule) {
	_, _ = fmt.Fprintf(out, "Name:        %s\n", alertRule.Name)
	_, _ = fmt.Fprintf(out, "ID:          %s\n", alertRule.ID)
	if alertRule.Description != nil && *alertRule.Description != "" {
		_, _ = fmt.Fprintf(out, "Description: %s\n", *alertRule.Description)
	}
	_, _ = fmt.Fprintf(out, "Enabled:     %t\n", alertRule.Enabled)
	if alertRule.Severity != "" {
		_, _ = fmt.Fprintf(out, "Severity:    %s\n", alertRule.Severity)
	}
	if alertRule.Status != "" {
		_, _ = fmt.Fprintf(out, "Status:      %s\n", alertRule.Status)
	}
	_, _ = fmt.Fprintf(out, "Rules:       %d\n", alertRule.RuleCount)
	_, _ = fmt.Fprintf(out, "Triggers:    %d\n", alertRule.TriggerCount)
	if alertRule.LastTriggeredAt != nil {
		_, _ = fmt.Fprintf(out, "Last fired:  %s\n", *alertRule.LastTriggeredAt)
	}
}

func init() {
	alertsCmd.AddCommand(newAlertsRulesCommand(), newAlertsTriggersCommand())
}
