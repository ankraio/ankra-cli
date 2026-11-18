package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The alerts of one application (bead ankra-5uowtk.26.3.1): the CLI side of
// the routes under /api/v1/org/applications/{application_id}/alerts. An
// application alert has one condition on the application's own metrics, on
// one environment, and sends to 1 to 5 of the organisation's alert
// destinations. The platform stores it as an organisation alert named
// "Application · Environment · name", so it also shows under `ankra alerts
// rules`, where it can be turned off and on; these commands are the ones that
// may change or delete it.

const applicationAlertConditionHelp = `The alert fires when the metric reads above the threshold for
--hold-minutes minutes in a row (1 to 60). The metrics are the ones the
application's charts draw for the environment:

  failed_share_pct  the share of requests that failed, in percent (0 to 100)
  p95_ms            the 95th percentile response time, in milliseconds
  restarts          container restarts in the last 15 minutes

--send-to takes 1 to 5 alert destination ids, comma-separated or repeated.
'ankra alerts destinations list' shows them. --severity is critical, warning
or info.`

// applicationAlertConditionType is the one condition type the platform takes.
const applicationAlertConditionType = "app_metric"

func newApplicationAlertsCommand() *cobra.Command {
	alertsCommand := &cobra.Command{
		Use:     "alerts",
		Aliases: []string{"alert"},
		Short:   "List, create, change and delete the alerts of one application",
		Long: `List, create, change and delete the alerts of one application.

An application alert watches one environment of the application and has one
condition on the application's own metrics. It also shows under 'ankra alerts
rules', where you can turn it off and on. An organisation alert rule that
watches one of the environment's clusters is listed here too, but it is
changed under 'ankra alerts rules'.`,
		Example: `  ankra application alerts list shop
  ankra application alerts create shop --name "Errors" --environment production \
    --metric failed_share_pct --threshold 5 --hold-minutes 10 --send-to <destination-id>`,
	}
	alertsCommand.AddCommand(
		newApplicationAlertsListCommand(),
		newApplicationAlertsCreateCommand(),
		newApplicationAlertsUpdateCommand(),
		newApplicationAlertsDeleteCommand(),
	)
	return alertsCommand
}

func newApplicationAlertsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list <application>",
		Short: "List an application's alerts and what each one is doing now",
		Long: `List an application's alerts and what each one is doing now.

The application's own alerts come first, oldest first, at most 100. With
--environment the list holds the alerts on that environment and the
organisation alert rules that watch one of its clusters.

STATE is what the platform reports for each alert:

  firing since <time>  the alert is telling someone now
  ok                   the rule was read on its last run and is not firing
  unknown (<reason>)   the platform could not read the rule on its last run
  not evaluated yet    no run has read the rule yet
  off                  the alert or its rule is turned off`,
		Example: `  ankra application alerts list shop
  ankra application alerts list shop --environment production
  ankra application alerts list shop -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			environmentID := ""
			if environmentReference, _ := command.Flags().GetString("environment"); strings.TrimSpace(environmentReference) != "" {
				resolvedID, environmentError := resolveApplicationAlertEnvironment(command, applicationID, environmentReference)
				if environmentError != nil {
					return environmentError
				}
				environmentID = resolvedID
			}
			list, listError := apiClient.ListApplicationAlerts(command.Context(), applicationID, environmentID)
			if listError != nil {
				return fmt.Errorf("reading the alerts of application %s: %w", strings.TrimSpace(arguments[0]), listError)
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, list)
				return renderError
			}
			return writeApplicationAlertList(command.OutOrStdout(), list, environmentID != "")
		},
	}
	listCommand.Flags().String("environment", "", "Only this environment, by name or id ('ankra application environments' lists them)")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationAlertsCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create <application>",
		Short: "Create an alert on one environment of an application",
		Long: `Create an alert on one environment of an application.

An environment with nothing running on it yet is accepted, so you can set an
alert before the first deploy. It reads unknown until something runs there.
The platform refuses the alert when the organisation has no alert destination
yet, or when the application already has 50 alerts.

` + applicationAlertConditionHelp,
		Example: `  ankra application alerts create shop --name "Errors" --environment production \
    --metric failed_share_pct --threshold 5 --hold-minutes 10 \
    --send-to 0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e --severity critical`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			flags := command.Flags()
			name, _ := flags.GetString("name")
			environmentReference, _ := flags.GetString("environment")
			metric, _ := flags.GetString("metric")
			threshold, _ := flags.GetFloat64("threshold")
			holdMinutes, _ := flags.GetInt("hold-minutes")
			sendTo, _ := flags.GetStringSlice("send-to")
			severity, _ := flags.GetString("severity")

			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			environmentID, environmentError := resolveApplicationAlertEnvironment(command, applicationID, environmentReference)
			if environmentError != nil {
				return environmentError
			}
			created, createError := apiClient.CreateApplicationAlert(command.Context(), applicationID, client.ApplicationAlertRequest{
				Name:          name,
				EnvironmentID: environmentID,
				Condition: client.ApplicationAlertCondition{
					ConditionType: applicationAlertConditionType,
					Metric:        strings.TrimSpace(metric),
					Threshold:     threshold,
					HoldMinutes:   holdMinutes,
				},
				SendTo:   trimmedValues(sendTo),
				Severity: strings.TrimSpace(severity),
			})
			if createError != nil {
				return fmt.Errorf("creating the alert: %w", createError)
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, created)
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Alert %q created.\n", created.Name)
			return writeApplicationAlertTable(command.OutOrStdout(), []client.ApplicationAlert{*created})
		},
	}
	flags := createCommand.Flags()
	flags.String("name", "", "The alert's name on the application page (1 to 100 characters)")
	flags.String("environment", "", "The environment it watches, by name or id")
	flags.String("metric", "", "failed_share_pct, p95_ms or restarts")
	flags.Float64("threshold", 0, "It fires when the metric reads above this")
	flags.Int("hold-minutes", 0, "How many minutes in a row the reading stays above the threshold before it fires (1 to 60)")
	flags.StringSlice("send-to", nil, "Alert destination ids to send to (1 to 5)")
	flags.String("severity", "", "critical, warning or info (the platform stores warning when left out)")
	for _, required := range []string{"name", "environment", "metric", "threshold", "hold-minutes", "send-to"} {
		_ = createCommand.MarkFlagRequired(required)
	}
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

func newApplicationAlertsUpdateCommand() *cobra.Command {
	updateCommand := &cobra.Command{
		Use:   "update <application> <alert-id>",
		Short: "Change one of an application's own alerts",
		Long: `Change one of an application's own alerts.

Pass only what changes. The command reads the alert first and keeps every
field you leave out. The alert's id is in 'ankra application alerts list'.
An organisation alert rule is changed under 'ankra alerts rules' instead, and
so is turning an alert off or on.

` + applicationAlertConditionHelp + `

--send-to replaces the whole list of destinations.`,
		Example: `  ankra application alerts update shop 0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e --threshold 2
  ankra application alerts update shop 0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e --metric p95_ms --threshold 800`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			flags := command.Flags()
			changed := false
			for _, name := range []string{"name", "environment", "metric", "threshold", "hold-minutes", "send-to", "severity"} {
				changed = changed || flags.Changed(name)
			}
			if !changed {
				return withExitCode(exitUsage, errors.New(
					"nothing to change: pass --name, --environment, --metric, --threshold, --hold-minutes, --send-to or --severity"))
			}
			alertID := strings.TrimSpace(arguments[1])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			current, findError := findOwnApplicationAlert(command, applicationID, strings.TrimSpace(arguments[0]), alertID)
			if findError != nil {
				return findError
			}

			alertRequest := client.ApplicationAlertRequest{Name: current.Name}
			if current.EnvironmentID != nil {
				alertRequest.EnvironmentID = *current.EnvironmentID
			}
			if current.Condition != nil {
				alertRequest.Condition = *current.Condition
			}
			alertRequest.Condition.ConditionType = applicationAlertConditionType
			for _, destination := range current.SendTo {
				alertRequest.SendTo = append(alertRequest.SendTo, destination.IntegrationID)
			}
			if flags.Changed("name") {
				alertRequest.Name, _ = flags.GetString("name")
			}
			if flags.Changed("environment") {
				environmentReference, _ := flags.GetString("environment")
				environmentID, environmentError := resolveApplicationAlertEnvironment(command, applicationID, environmentReference)
				if environmentError != nil {
					return environmentError
				}
				alertRequest.EnvironmentID = environmentID
			}
			if flags.Changed("metric") {
				metric, _ := flags.GetString("metric")
				alertRequest.Condition.Metric = strings.TrimSpace(metric)
			}
			if flags.Changed("threshold") {
				alertRequest.Condition.Threshold, _ = flags.GetFloat64("threshold")
			}
			if flags.Changed("hold-minutes") {
				alertRequest.Condition.HoldMinutes, _ = flags.GetInt("hold-minutes")
			}
			if flags.Changed("send-to") {
				sendTo, _ := flags.GetStringSlice("send-to")
				alertRequest.SendTo = trimmedValues(sendTo)
			}
			if flags.Changed("severity") {
				severity, _ := flags.GetString("severity")
				alertRequest.Severity = strings.TrimSpace(severity)
			}

			updated, updateError := apiClient.UpdateApplicationAlert(command.Context(), applicationID, alertID, alertRequest)
			if updateError != nil {
				return fmt.Errorf("changing the alert: %w", updateError)
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, updated)
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Alert %q changed.\n", updated.Name)
			return writeApplicationAlertTable(command.OutOrStdout(), []client.ApplicationAlert{*updated})
		},
	}
	flags := updateCommand.Flags()
	flags.String("name", "", "A new name (1 to 100 characters)")
	flags.String("environment", "", "Another environment to watch, by name or id")
	flags.String("metric", "", "failed_share_pct, p95_ms or restarts")
	flags.Float64("threshold", 0, "It fires when the metric reads above this")
	flags.Int("hold-minutes", 0, "How many minutes in a row the reading stays above the threshold before it fires (1 to 60)")
	flags.StringSlice("send-to", nil, "Alert destination ids to send to (1 to 5); replaces the list")
	flags.String("severity", "", "critical, warning or info")
	registerStructuredOutputFlags(updateCommand)
	return updateCommand
}

func newApplicationAlertsDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:   "delete <application> <alert-id>",
		Short: "Delete one of an application's own alerts",
		Long: `Delete one of an application's own alerts, with its rule.

The alert stops telling anyone, and it cannot be undone. The command names
the alert and asks before it deletes it; --yes skips the question. To stop an
alert for a while and keep it, turn it off with 'ankra alerts rules disable
<alert-id>' instead.`,
		Example: `  ankra application alerts delete shop 0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra application alerts delete shop 0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			alertID := strings.TrimSpace(arguments[1])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			alertName := alertID
			yes, _ := command.Flags().GetBool("yes")
			if !yes {
				current, findError := findOwnApplicationAlert(command, applicationID, applicationReference, alertID)
				if findError != nil {
					return findError
				}
				alertName = current.Name
				if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
					fmt.Sprintf("Delete alert %q (%s) from application %q? It stops telling anyone and cannot be undone. [y/N]: ",
						current.Name, current.Says, applicationReference), yes); confirmError != nil {
					return confirmError
				}
			}
			result, deleteError := apiClient.DeleteApplicationAlert(command.Context(), applicationID, alertID)
			if deleteError != nil {
				return fmt.Errorf("deleting the alert: %w", deleteError)
			}
			if !result.Success {
				reason := strings.TrimSpace(result.Message)
				if reason == "" {
					reason = "the platform did not say why"
				}
				return fmt.Errorf("deleting the alert: the platform did not delete it: %s", reason)
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, result)
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Alert %q deleted.\n", alertName)
			return nil
		},
	}
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

// resolveApplicationAlertEnvironment turns an --environment value into an
// environment id: an id is taken as it is, a name is looked up among the
// application's environments the way 'application environments set' does.
func resolveApplicationAlertEnvironment(command *cobra.Command, applicationID string, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", withExitCode(exitUsage, errors.New("--environment is empty: pass its name or id"))
	}
	if looksLikeUUID(reference) {
		return reference, nil
	}
	payload, readError := apiClient.GetApplicationAutoDeploy(command.Context(), applicationID)
	if readError != nil {
		return "", fmt.Errorf("reading the application's environments to find %q: %w", reference, readError)
	}
	environments, decodeError := decodeApplicationEnvironments(payload)
	if decodeError != nil {
		return "", decodeError
	}
	environment, matchError := matchApplicationEnvironment(environments, reference)
	if matchError != nil {
		return "", matchError
	}
	return environment.ID, nil
}

// findOwnApplicationAlert reads the application's alerts and returns the one
// with alertID, refusing an alert that is not there and an organisation rule
// the application did not make. An application has at most 50 own alerts and
// the list carries them first, so an own alert is always on it.
func findOwnApplicationAlert(command *cobra.Command, applicationID string, applicationReference string, alertID string) (client.ApplicationAlert, error) {
	list, listError := apiClient.ListApplicationAlerts(command.Context(), applicationID, "")
	if listError != nil {
		return client.ApplicationAlert{}, fmt.Errorf("reading the alerts of application %s: %w", applicationReference, listError)
	}
	for _, alert := range list.Alerts {
		if !strings.EqualFold(alert.AlertID, alertID) {
			continue
		}
		if !alert.Own {
			return client.ApplicationAlert{}, withExitCode(exitUsage, fmt.Errorf(
				"alert %q is an organisation alert rule, not one this application made: change it with 'ankra alerts rules' instead",
				alert.Name))
		}
		return alert, nil
	}
	return client.ApplicationAlert{}, withExitCode(exitNotFound, fmt.Errorf(
		"application %s has no alert %s; 'ankra application alerts list %s' lists them",
		applicationReference, alertID, applicationReference))
}

// writeApplicationAlertList prints the table and what the platform said about
// the list as a whole: that it was cut at its limit, and, for one environment,
// whether organisation rules also watch it.
func writeApplicationAlertList(output io.Writer, list *client.ApplicationAlertList, forEnvironment bool) error {
	if len(list.Alerts) == 0 {
		place := "this application"
		if forEnvironment {
			place = "this environment"
		}
		_, _ = fmt.Fprintf(output, "No alerts on %s.\n", place)
	} else if tableError := writeApplicationAlertTable(output, list.Alerts); tableError != nil {
		return tableError
	}
	if list.Truncated {
		_, _ = fmt.Fprintf(output, "Showing the first %d alerts. The application has more.\n", list.Limit)
	}
	if covered := list.CoveredByOrganisationRules; covered != nil {
		if *covered {
			_, _ = fmt.Fprintln(output, "Organisation alert rules also watch this environment's clusters.")
		} else {
			_, _ = fmt.Fprintln(output, "No organisation alert rule watches this environment's clusters.")
		}
	}
	return nil
}

func writeApplicationAlertTable(output io.Writer, alerts []client.ApplicationAlert) error {
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "NAME\tCONDITION\tSEVERITY\tSTATE\tSENDS TO\tENVIRONMENT\tALERT ID")
	for _, alert := range alerts {
		environment := "-"
		switch {
		case !alert.Own:
			environment = "organisation rule"
		case alert.EnvironmentID != nil && *alert.EnvironmentID != "":
			environment = *alert.EnvironmentID
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			valueOrDashString(alert.Name), valueOrDashString(alert.Says), valueOrDashString(alert.Severity),
			applicationAlertState(alert), applicationAlertDestinations(alert.SendTo), environment, alert.AlertID)
	}
	return writer.Flush()
}

// applicationAlertState is the STATE cell, made only of what the platform
// reported: an open last firing, a rule that is off, or how the evaluator
// last read the rule.
func applicationAlertState(alert client.ApplicationAlert) string {
	parts := []string{}
	if !alert.Enabled {
		parts = append(parts, "off")
	}
	if alert.LastFiring.IsOpen() {
		firing := "firing since " + applicationAlertMoment(alert.LastFiring.TriggeredAt)
		if acknowledger := alert.LastFiring.AcknowledgedBy; acknowledger != nil {
			if acknowledger.Name != nil && strings.TrimSpace(*acknowledger.Name) != "" {
				firing += ", acknowledged by " + strings.TrimSpace(*acknowledger.Name)
			} else {
				firing += ", acknowledged"
			}
		}
		return strings.Join(append(parts, firing), ", ")
	}
	if !alert.Enabled {
		return strings.Join(parts, ", ")
	}
	switch alert.Evaluation {
	case "ok":
		return "ok"
	case "not_evaluated":
		return "not evaluated yet"
	case "unknown":
		reason := stringValue(alert.UnknownReason)
		if reason == "" {
			return "unknown"
		}
		return "unknown (" + applicationAlertUnknownReason(reason) + ")"
	case "":
		return "-"
	default:
		return alert.Evaluation
	}
}

// applicationAlertUnknownReason says in words the reasons the platform
// documents; any other reason is shown as the platform sent it.
func applicationAlertUnknownReason(reason string) string {
	switch reason {
	case "no_installations":
		return "nothing of the application runs on the environment"
	case "application_not_found":
		return "the application is gone"
	case "environment_not_found":
		return "the environment is gone"
	default:
		return reason
	}
}

func applicationAlertDestinations(destinations []client.ApplicationAlertDestination) string {
	labels := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		label := strings.TrimSpace(destination.Label)
		if label == "" {
			label = destination.IntegrationID
		}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "-"
	}
	return strings.Join(labels, ", ")
}

// applicationAlertMoment prints a platform timestamp as its UTC minute, or
// as sent when it does not parse.
func applicationAlertMoment(timestamp string) string {
	moment, parseError := time.Parse(time.RFC3339, timestamp)
	if parseError != nil {
		return timestamp
	}
	return moment.UTC().Format("2006-01-02 15:04 UTC")
}

// decodeApplicationEnvironments reads the environments of an auto-deploy
// payload.
func decodeApplicationEnvironments(payload []byte) ([]applicationAutoDeployEnvironment, error) {
	var state applicationAutoDeployState
	if decodeError := json.Unmarshal(payload, &state); decodeError != nil {
		return nil, fmt.Errorf("parsing the application's environments: %w", decodeError)
	}
	return state.Environments, nil
}

// trimmedValues drops blanks and surrounding spaces from a list flag.
func trimmedValues(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}
