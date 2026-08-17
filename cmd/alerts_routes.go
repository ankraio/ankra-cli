package cmd

import (
	"errors"
	"fmt"
	"io"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var alertsRoutesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage notification routes (which notifications reach which destination)",
	Long: `Manage the organisation's notification routes.

A route sends notifications matching every filter it sets (kind, severity,
cluster, source) to one destination; a route with no filters matches
everything. Routes are evaluated in ascending priority, mode "exclude"
withholds matches instead of delivering them, and --stop-on-match ends the
walk at that route so lower-priority routes never see the notification.

  ankra alerts routes create --destination-id <destination-id> --severity critical
  ankra alerts routes create --destination-id <destination-id> --kind execution_failed --cluster-id <cluster-id> --priority 10
  ankra alerts routes update <route-id> --disabled
  ankra alerts routes test <route-id>`,
}

var alertsRoutesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List notification routes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, listError := apiClient.ListNotificationRoutes()
		if listError != nil {
			return fmt.Errorf("listing notification routes: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if len(list.Items) == 0 {
			_, _ = fmt.Fprintln(out, "No notification routes found.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"ID", "Priority", "Kind", "Severity", "Cluster", "Destination", "Mode", "Enabled"})
		for _, route := range list.Items {
			writer.AppendRow(table.Row{
				route.ID,
				route.Priority,
				valueOrAny(route.Kind),
				valueOrAny(route.Severity),
				valueOrAny(route.ClusterID),
				route.DestinationID,
				route.Mode,
				route.Enabled,
			})
		}
		writer.Render()
		return nil
	},
}

var alertsRoutesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a notification route",
	Long: `Create a notification route to a destination. Every filter is optional;
a route with none matches every notification.

Kinds include execution_failed, resource_deployment_failed,
resource_health_degraded, alert_trigger_fired, gitops_sync_failed,
agent_offline, security_new_severe_cves, and the other platform
notification kinds; severities are critical, warning, and info.

  ankra alerts routes create --destination-id <destination-id> --severity critical
  ankra alerts routes create --destination-id <destination-id> --kind gitops_sync_failed --mode exclude --stop-on-match`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if validationError := validateRouteFilterFlags(cmd); validationError != nil {
			return validationError
		}
		request := client.CreateNotificationRouteRequest{
			DestinationID: mustFlagString(cmd, "destination-id"),
			Kind:          changedStringFlag(cmd, "kind"),
			Severity:      changedStringFlag(cmd, "severity"),
			ClusterID:     changedStringFlag(cmd, "cluster-id"),
			SourceID:      changedStringFlag(cmd, "source-id"),
			Priority:      changedIntFlag(cmd, "priority"),
			StopOnMatch:   changedBoolFlag(cmd, "stop-on-match"),
			Mode:          changedStringFlag(cmd, "mode"),
			Enabled:       enabledFromFlags(cmd),
		}
		route, createError := apiClient.CreateNotificationRoute(request)
		if createError != nil {
			return fmt.Errorf("creating notification route: %w", createError)
		}
		if rendered, renderError := renderStructured(cmd, route); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Notification route %s created.\n", route.ID)
		printNotificationRoute(cmd.OutOrStdout(), route)
		return nil
	},
}

var alertsRoutesUpdateCmd = &cobra.Command{
	Use:   "update <route-id>",
	Short: "Update a notification route",
	Long: `Update a notification route. Only the flags you pass are changed; the
rest keep their current values.

  ankra alerts routes update <route-id> --priority 5 --stop-on-match
  ankra alerts routes update <route-id> --destination-id <other-destination-id>
  ankra alerts routes update <route-id> --disabled`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if validationError := validateRouteFilterFlags(cmd); validationError != nil {
			return validationError
		}
		request := client.UpdateNotificationRouteRequest{
			DestinationID: changedStringFlag(cmd, "destination-id"),
			Kind:          changedStringFlag(cmd, "kind"),
			Severity:      changedStringFlag(cmd, "severity"),
			ClusterID:     changedStringFlag(cmd, "cluster-id"),
			SourceID:      changedStringFlag(cmd, "source-id"),
			Priority:      changedIntFlag(cmd, "priority"),
			StopOnMatch:   changedBoolFlag(cmd, "stop-on-match"),
			Mode:          changedStringFlag(cmd, "mode"),
			Enabled:       enabledFromFlags(cmd),
		}
		if request == (client.UpdateNotificationRouteRequest{}) {
			return withExitCode(exitUsage, errors.New("nothing to update: pass at least one flag"))
		}
		route, updateError := apiClient.UpdateNotificationRoute(args[0], request)
		if updateError != nil {
			return fmt.Errorf("updating notification route: %w", updateError)
		}
		if rendered, renderError := renderStructured(cmd, route); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Notification route %s updated.\n", route.ID)
		printNotificationRoute(cmd.OutOrStdout(), route)
		return nil
	},
}

var alertsRoutesDeleteCmd = &cobra.Command{
	Use:   "delete <route-id>",
	Short: "Delete a notification route",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		routeID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete notification route %q? [y/N]: ", routeID), yes); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeleteNotificationRoute(routeID); deleteError != nil {
			return fmt.Errorf("deleting notification route: %w", deleteError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Notification route %s deleted.\n", routeID)
		return nil
	},
}

var alertsRoutesTestCmd = &cobra.Command{
	Use:   "test <route-id>",
	Short: "Queue a sample notification through a route",
	Long: `Queue a sample notification through a route's destination. Delivery is
asynchronous: the command returns the delivery id once the sample is
queued, not once the receiver has accepted it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, testError := apiClient.TestNotificationRoute(args[0])
		if testError != nil {
			return fmt.Errorf("testing notification route: %w", testError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Test notification queued (delivery %s).\n", result.DeliveryID)
		return nil
	},
}

// validateRouteFilterFlags rejects enum values the backend would refuse
// with a validation error, so a typo exits with the usage code and a plain
// message instead of a 422 body.
func validateRouteFilterFlags(cmd *cobra.Command) error {
	if mode := mustFlagString(cmd, "mode"); cmd.Flags().Changed("mode") && mode != "include" && mode != "exclude" {
		return withExitCode(exitUsage, fmt.Errorf("unsupported --mode %q (use include or exclude)", mode))
	}
	if severity := mustFlagString(cmd, "severity"); cmd.Flags().Changed("severity") &&
		severity != "critical" && severity != "warning" && severity != "info" {
		return withExitCode(exitUsage, fmt.Errorf("unsupported --severity %q (use critical, warning, or info)", severity))
	}
	return nil
}

// valueOrAny renders a nullable route filter: an unset filter matches any
// value, which is what the table should say.
func valueOrAny(value *string) string {
	if value == nil || *value == "" {
		return "any"
	}
	return *value
}

func printNotificationRoute(out io.Writer, route *client.NotificationRoute) {
	_, _ = fmt.Fprintf(out, "ID:            %s\n", route.ID)
	_, _ = fmt.Fprintf(out, "Destination:   %s\n", route.DestinationID)
	_, _ = fmt.Fprintf(out, "Priority:      %d\n", route.Priority)
	_, _ = fmt.Fprintf(out, "Kind:          %s\n", valueOrAny(route.Kind))
	_, _ = fmt.Fprintf(out, "Severity:      %s\n", valueOrAny(route.Severity))
	_, _ = fmt.Fprintf(out, "Cluster:       %s\n", valueOrAny(route.ClusterID))
	_, _ = fmt.Fprintf(out, "Source:        %s\n", valueOrAny(route.SourceID))
	_, _ = fmt.Fprintf(out, "Mode:          %s\n", route.Mode)
	_, _ = fmt.Fprintf(out, "Stop on match: %t\n", route.StopOnMatch)
	_, _ = fmt.Fprintf(out, "Enabled:       %t\n", route.Enabled)
}

// registerRouteFilterFlags adds the route shape flags shared by create and
// update. Defaults are left zero on purpose: only flags the user passes are
// sent, so create picks up the backend defaults (priority 100, mode include)
// and update leaves unmentioned members alone.
func registerRouteFilterFlags(cmd *cobra.Command) {
	cmd.Flags().String("kind", "", "Only notifications of this kind (for example execution_failed)")
	cmd.Flags().String("severity", "", "Only notifications of this severity: critical, warning, or info")
	cmd.Flags().String("cluster-id", "", "Only notifications about this cluster (id)")
	cmd.Flags().String("source-id", "", "Only notifications from this source id")
	cmd.Flags().Int("priority", 0, "Evaluation order, lowest first (default 100)")
	cmd.Flags().Bool("stop-on-match", false, "Stop evaluating lower-priority routes once this one matches")
	cmd.Flags().String("mode", "", "include delivers matches, exclude withholds them (default include)")
}

func init() {
	alertsRoutesCreateCmd.Flags().String("destination-id", "", "Destination to deliver to (see 'ankra alerts destinations list')")
	registerRouteFilterFlags(alertsRoutesCreateCmd)
	alertsRoutesCreateCmd.Flags().Bool("disabled", false, "Create the route disabled")
	_ = alertsRoutesCreateCmd.MarkFlagRequired("destination-id")

	alertsRoutesUpdateCmd.Flags().String("destination-id", "", "Re-point the route at another destination")
	registerRouteFilterFlags(alertsRoutesUpdateCmd)
	alertsRoutesUpdateCmd.Flags().Bool("enabled", false, "Enable the route")
	alertsRoutesUpdateCmd.Flags().Bool("disabled", false, "Disable the route")
	alertsRoutesUpdateCmd.MarkFlagsMutuallyExclusive("enabled", "disabled")

	alertsRoutesDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(
		alertsRoutesListCmd,
		alertsRoutesCreateCmd,
		alertsRoutesUpdateCmd,
		alertsRoutesTestCmd,
	)

	alertsRoutesCmd.AddCommand(alertsRoutesListCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesCreateCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesUpdateCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesDeleteCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesTestCmd)
	alertsCmd.AddCommand(alertsRoutesCmd)
}
