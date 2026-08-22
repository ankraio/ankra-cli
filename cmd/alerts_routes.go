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
			routeRow := route
			writer.AppendRow(table.Row{
				route.ID,
				route.Priority,
				renderRouteKindFilter(&routeRow),
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
		kinds, kindsNegated := routeKindFilterFlags(cmd)
		request := client.CreateNotificationRouteRequest{
			DestinationID: mustFlagString(cmd, "destination-id"),
			Kind:          changedStringFlag(cmd, "kind"),
			Kinds:         kinds,
			KindsNegated:  kindsNegated,
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
		kinds, kindsNegated := routeKindFilterFlags(cmd)
		request := client.UpdateNotificationRouteRequest{
			DestinationID: changedStringFlag(cmd, "destination-id"),
			Kind:          changedStringFlag(cmd, "kind"),
			Kinds:         kinds,
			KindsNegated:  kindsNegated,
			Severity:      changedStringFlag(cmd, "severity"),
			ClusterID:     changedStringFlag(cmd, "cluster-id"),
			SourceID:      changedStringFlag(cmd, "source-id"),
			Priority:      changedIntFlag(cmd, "priority"),
			StopOnMatch:   changedBoolFlag(cmd, "stop-on-match"),
			Mode:          changedStringFlag(cmd, "mode"),
			Enabled:       enabledFromFlags(cmd),
		}
		if isEmptyRouteUpdate(request) {
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

var alertsRoutesPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Show which destinations a notification would reach",
	Long: `Resolve a hypothetical notification against the organisation's routing
rules and, with --alert-id, that alert's own destinations, and print the
destinations it would reach with the reason each one matched.

Nothing is delivered and nothing is changed - this is a dry run.

Pass --alert-id whenever you preview an alert firing. A destination reached
by both the alert's own list and a routing rule is delivered to once, and
without the alert id the preview cannot tell you which rule that affects.

  ankra alerts routes preview --kind alert_trigger_fired --severity critical
  ankra alerts routes preview --kind alert_trigger_fired --severity critical --alert-id <alert-id>
  ankra alerts routes preview --kind gitops_sync_failed --severity warning --cluster-id <cluster-id> -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if validationError := validatePreviewFlags(cmd); validationError != nil {
			return validationError
		}
		preview, previewError := apiClient.PreviewNotificationRoutes(client.PreviewNotificationRoutesRequest{
			Kind:      mustFlagString(cmd, "kind"),
			Severity:  mustFlagString(cmd, "severity"),
			ClusterID: changedStringFlag(cmd, "cluster-id"),
			SourceID:  changedStringFlag(cmd, "source-id"),
			AlertID:   changedStringFlag(cmd, "alert-id"),
		})
		if previewError != nil {
			return fmt.Errorf("previewing notification routes: %w", previewError)
		}
		if rendered, renderError := renderStructured(cmd, preview); rendered || renderError != nil {
			return renderError
		}
		printNotificationRoutePreview(cmd.OutOrStdout(), preview)
		return nil
	},
}

// validatePreviewFlags rejects the enum values the backend would refuse, so
// a typo exits with the usage code instead of a 422 body.
func validatePreviewFlags(cmd *cobra.Command) error {
	if kind := mustFlagString(cmd, "kind"); kind == "" {
		return withExitCode(exitUsage, errors.New("--kind is required"))
	}
	severity := mustFlagString(cmd, "severity")
	if severity != "critical" && severity != "warning" && severity != "info" {
		return withExitCode(exitUsage,
			fmt.Errorf("unsupported --severity %q (use critical, warning, or info)", severity))
	}
	return nil
}

func printNotificationRoutePreview(out io.Writer, preview *client.NotificationRoutePreview) {
	if !preview.Routable {
		_, _ = fmt.Fprintf(out, "%s\n\n", preview.RoutableReason)
	}
	if len(preview.Deliveries) == 0 {
		_, _ = fmt.Fprintln(out, "This notification would not be delivered to any destination.")
	} else {
		_, _ = fmt.Fprintln(out, "This notification would be delivered to:")
		renderPreviewDeliveries(out, preview.Deliveries)
	}
	if preview.HomeDestinationUsed {
		_, _ = fmt.Fprintln(out, "\nNo routing rule matched, so this fell back to the organisation home channel.")
	}
	if len(preview.Suppressed) > 0 {
		_, _ = fmt.Fprintln(out, "\nNot delivered:")
		renderPreviewDeliveries(out, preview.Suppressed)
	}
	if len(preview.RouteEvaluations) > 0 {
		_, _ = fmt.Fprintln(out, "\nRule evaluation:")
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Rule", "Priority", "Mode", "Matched", "Outcome", "Reason"})
		for _, evaluation := range preview.RouteEvaluations {
			writer.AppendRow(table.Row{
				evaluation.RouteID,
				evaluation.Priority,
				evaluation.Mode,
				evaluation.Matched,
				evaluation.Outcome,
				evaluation.Reason,
			})
		}
		writer.Render()
	}
}

func renderPreviewDeliveries(out io.Writer, deliveries []client.NotificationRoutePreviewDelivery) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Destination", "Via", "Rule", "Reason"})
	for _, delivery := range deliveries {
		writer.AppendRow(table.Row{
			previewDestinationLabel(delivery),
			delivery.Via,
			valueOrAny(delivery.RouteID),
			delivery.Reason,
		})
	}
	writer.Render()
}

// previewDestinationLabel prefers the destination name; a route whose
// destination has been removed has no name to show.
func previewDestinationLabel(delivery client.NotificationRoutePreviewDelivery) string {
	if delivery.DestinationName != "" {
		return delivery.DestinationName
	}
	return delivery.DestinationID
}

// validateRouteFilterFlags rejects enum values the backend would refuse
// with a validation error, so a typo exits with the usage code and a plain
// message instead of a 422 body.
func validateRouteFilterFlags(cmd *cobra.Command) error {
	setKindFlags := []string{}
	for _, name := range []string{"kind", "kinds", "exclude-kinds"} {
		if cmd.Flags().Changed(name) {
			setKindFlags = append(setKindFlags, "--"+name)
		}
	}
	if len(setKindFlags) > 1 {
		return withExitCode(exitUsage, fmt.Errorf("%s are mutually exclusive: a route has one kind filter",
			strings.Join(setKindFlags, " and ")))
	}
	if mode := mustFlagString(cmd, "mode"); cmd.Flags().Changed("mode") && mode != "include" && mode != "exclude" {
		return withExitCode(exitUsage, fmt.Errorf("unsupported --mode %q (use include or exclude)", mode))
	}
	if severity := mustFlagString(cmd, "severity"); cmd.Flags().Changed("severity") &&
		severity != "critical" && severity != "warning" && severity != "info" {
		return withExitCode(exitUsage, fmt.Errorf("unsupported --severity %q (use critical, warning, or info)", severity))
	}
	return nil
}

// routeKindFilterFlags resolves --kinds / --exclude-kinds into the wire pair.
// A nil list leaves both members out of the body, so the backend keeps the
// route's current kind filter on update and applies no filter on create.
func routeKindFilterFlags(cmd *cobra.Command) ([]string, *bool) {
	if cmd.Flags().Changed("exclude-kinds") {
		negated := true
		return splitCommaList(mustFlagString(cmd, "exclude-kinds")), &negated
	}
	if cmd.Flags().Changed("kinds") {
		negated := false
		return splitCommaList(mustFlagString(cmd, "kinds")), &negated
	}
	return nil, nil
}

// splitCommaList parses a comma-separated flag value, dropping the empty
// entries a trailing or doubled comma leaves behind.
func splitCommaList(value string) []string {
	entries := []string{}
	for _, entry := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// isEmptyRouteUpdate reports whether an update carries no change at all.
// The struct cannot be compared with == because it holds a slice.
func isEmptyRouteUpdate(request client.UpdateNotificationRouteRequest) bool {
	return request.DestinationID == nil && request.Kind == nil && request.Kinds == nil &&
		request.KindsNegated == nil && request.Severity == nil && request.ClusterID == nil &&
		request.SourceID == nil && request.Priority == nil && request.StopOnMatch == nil &&
		request.Mode == nil && request.Enabled == nil
}

// renderRouteKindFilter renders a route's kind filter for humans: the list,
// the negated list, or "any" when the route carries no kind filter.
func renderRouteKindFilter(route *client.NotificationRoute) string {
	if len(route.Kinds) > 0 {
		joined := strings.Join(route.Kinds, ", ")
		if route.KindsNegated {
			return "all except " + joined
		}
		return joined
	}
	return valueOrAny(route.Kind)
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
	_, _ = fmt.Fprintf(out, "Kind:          %s\n", renderRouteKindFilter(route))
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
	cmd.Flags().String("kinds", "", "Only notifications of these kinds (comma-separated)")
	cmd.Flags().String("exclude-kinds", "", "Every kind EXCEPT these (comma-separated)")
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

	alertsRoutesPreviewCmd.Flags().String("kind", "", "Notification kind to resolve (for example alert_trigger_fired)")
	alertsRoutesPreviewCmd.Flags().String("severity", "critical", "Notification severity: critical, warning, or info")
	alertsRoutesPreviewCmd.Flags().String("cluster-id", "", "Resolve as a notification about this cluster")
	alertsRoutesPreviewCmd.Flags().String("source-id", "", "Resolve as a notification from this source id")
	alertsRoutesPreviewCmd.Flags().String("alert-id", "", "Resolve as a firing of this alert, including its own destinations")
	_ = alertsRoutesPreviewCmd.MarkFlagRequired("kind")

	registerStructuredOutputFlags(
		alertsRoutesListCmd,
		alertsRoutesCreateCmd,
		alertsRoutesUpdateCmd,
		alertsRoutesTestCmd,
		alertsRoutesPreviewCmd,
	)

	alertsRoutesCmd.AddCommand(alertsRoutesListCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesCreateCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesUpdateCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesDeleteCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesTestCmd)
	alertsRoutesCmd.AddCommand(alertsRoutesPreviewCmd)
	alertsCmd.AddCommand(alertsRoutesCmd)
}
