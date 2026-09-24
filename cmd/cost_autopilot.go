package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// The widest the autopilot tables' text cells grow before they wrap, so each
// table fits a 100-column terminal.
const (
	costAutopilotTierTextWidthMax = 30
	costAutopilotClusterWidthMax  = 24
)

var costAutopilotCmd = &cobra.Command{
	Use:   "autopilot",
	Short: "How much Ankra may do about cost unasked: the tier per environment kind, quiet hours, and per-cluster overrides",
	Long: `The cost autopilot decides how much Ankra may do about cost without asking -
the Autopilot view in the portal.

Every environment kind (production, staging, development, preview, unknown)
defaults to a tier: hands-off (every change waits for a person), scheduled,
managed or ephemeral, each described by 'ankra cost autopilot get'. A cluster
takes its environment's tier unless a person overrides it. Quiet hours are a
daily window when the autopilot starts no change of its own, and its
pre-notices go to a notification route or to the organisation's default
routing.

An organisation that never set a policy is hands-off for every kind. Reading
the policy is open to every member; changing it needs billing.manage and
clusters.write (at the cluster, for an override).`,
}

var costAutopilotGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the policy (tier per environment kind, quiet hours, pre-notice route), the tiers and every cluster's tier",
	Args:  cobra.NoArgs,
	Example: `  ankra cost autopilot get
  ankra cost autopilot get -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		policy, err := apiClient.GetCostAutopilot()
		if err != nil {
			return costAutopilotError(err, "reading the cost autopilot", http.MethodGet, false)
		}
		if rendered, err := renderStructured(cmd, policy); rendered || err != nil {
			return err
		}
		renderCostAutopilot(cmd.OutOrStdout(), policy)
		return nil
	},
}

var costAutopilotSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the parts of the policy you pass; everything else keeps its value",
	Long: `Change the organisation's autopilot policy. Only what you pass is sent, so
every other part keeps its value: --default changes only the environment
kinds it names, and the quiet hours and the pre-notice route change only when
you pass them or clear them.

--default KIND=TIER may repeat. The kinds are production, staging,
development, preview and unknown; the tiers are hands-off, scheduled, managed
and ephemeral. --quiet-hours takes a daily window as START-END in 24-hour
HH:MM (an end before the start runs past midnight) and needs --timezone, an
IANA zone.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost autopilot set --default development=managed --default preview=ephemeral
  ankra cost autopilot set --quiet-hours 22:00-07:00 --timezone Europe/Stockholm
  ankra cost autopilot set --clear-quiet-hours
  ankra cost autopilot set --notification-route 5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3
  ankra cost autopilot set --clear-notification-route`,
	RunE: func(cmd *cobra.Command, args []string) error {
		update, updateError := costAutopilotUpdateFromFlags(cmd)
		if updateError != nil {
			return updateError
		}
		policy, err := apiClient.UpdateCostAutopilot(update)
		if err != nil {
			return costAutopilotError(err, "changing the cost autopilot", http.MethodPut, false)
		}
		if rendered, err := renderStructured(cmd, policy); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Autopilot policy updated.")
		renderCostAutopilot(out, policy)
		return nil
	},
}

var costAutopilotOverrideCmd = &cobra.Command{
	Use:   "override <cluster>",
	Short: "Put one cluster in a tier whatever its environment, with the reason a card shows",
	Long: `Put one cluster in a tier whatever its environment kind. --reason is the
sentence a card shows for it (3 to 500 characters). The override replaces
the cluster's previous one and lasts until --expires-at (an RFC 3339 time) or
for --expires-in (a duration such as 72h), or with no expiry when neither is
given. 'ankra cost autopilot clear' returns the cluster to its environment's
tier.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra cost autopilot override staging-1 --tier managed --reason "Load test week, keep it lean"
  ankra cost autopilot override prod-eu --tier hands-off --reason "Launch freeze" --expires-in 72h
  ankra cost autopilot override prod-eu --tier scheduled --reason "Quarter close" --expires-at 2026-10-01T00:00:00Z`,
	RunE: func(cmd *cobra.Command, args []string) error {
		request, requestError := costAutopilotOverrideFromFlags(cmd)
		if requestError != nil {
			return requestError
		}
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}
		cluster, err := apiClient.SetCostAutopilotOverride(clusterID, request)
		if err != nil {
			return costAutopilotError(err, fmt.Sprintf("overriding the autopilot tier of %s", args[0]), http.MethodPut, true)
		}
		if rendered, err := renderStructured(cmd, cluster); rendered || err != nil {
			return err
		}
		renderCostAutopilotClusterChange(cmd.OutOrStdout(), cluster)
		return nil
	},
}

var costAutopilotClearCmd = &cobra.Command{
	Use:   "clear <cluster>",
	Short: "Remove a cluster's override, returning it to its environment's tier",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost autopilot clear staging-1
  ankra cost autopilot clear staging-1 --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		// The prompt goes to stderr so -o json keeps stdout parseable.
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Remove the autopilot override on %s and return it to its environment's tier? [y/N]: ", args[0]), yes); confirmError != nil {
			return confirmError
		}
		cluster, err := apiClient.ClearCostAutopilotOverride(clusterID)
		if err != nil {
			return costAutopilotError(err, fmt.Sprintf("clearing the autopilot override of %s", args[0]), http.MethodDelete, true)
		}
		if rendered, err := renderStructured(cmd, cluster); rendered || err != nil {
			return err
		}
		renderCostAutopilotClusterChange(cmd.OutOrStdout(), cluster)
		return nil
	},
}

func init() {
	costAutopilotSetCmd.Flags().StringArray("default", nil, "KIND=TIER: the tier an environment kind defaults to (repeatable; kinds you do not name keep theirs)")
	costAutopilotSetCmd.Flags().String("quiet-hours", "", "A daily window START-END in 24-hour HH:MM, e.g. 22:00-07:00 (needs --timezone)")
	costAutopilotSetCmd.Flags().String("timezone", "", "The IANA timezone of --quiet-hours, e.g. Europe/Stockholm")
	costAutopilotSetCmd.Flags().Bool("clear-quiet-hours", false, "Remove the quiet hours")
	costAutopilotSetCmd.Flags().String("notification-route", "", "The id of the notification route the autopilot's pre-notices go to")
	costAutopilotSetCmd.Flags().Bool("clear-notification-route", false, "Send the pre-notices through the organisation's default routing")
	costAutopilotSetCmd.MarkFlagsRequiredTogether("quiet-hours", "timezone")
	costAutopilotSetCmd.MarkFlagsMutuallyExclusive("quiet-hours", "clear-quiet-hours")
	costAutopilotSetCmd.MarkFlagsMutuallyExclusive("timezone", "clear-quiet-hours")
	costAutopilotSetCmd.MarkFlagsMutuallyExclusive("notification-route", "clear-notification-route")
	costAutopilotOverrideCmd.Flags().String("tier", "", "The tier: hands-off, scheduled, managed or ephemeral")
	costAutopilotOverrideCmd.Flags().String("reason", "", "Why, as a card shows it (3 to 500 characters)")
	costAutopilotOverrideCmd.Flags().String("expires-at", "", "When the override stops applying, as an RFC 3339 time (no expiry when omitted)")
	costAutopilotOverrideCmd.Flags().Duration("expires-in", 0, "How long the override applies from now, e.g. 72h (no expiry when omitted)")
	costAutopilotOverrideCmd.MarkFlagsMutuallyExclusive("expires-at", "expires-in")
	_ = costAutopilotOverrideCmd.MarkFlagRequired("tier")
	_ = costAutopilotOverrideCmd.MarkFlagRequired("reason")
	costAutopilotClearCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(costAutopilotGetCmd, costAutopilotSetCmd, costAutopilotOverrideCmd, costAutopilotClearCmd)
	costAutopilotCmd.AddCommand(costAutopilotGetCmd)
	costAutopilotCmd.AddCommand(costAutopilotSetCmd)
	costAutopilotCmd.AddCommand(costAutopilotOverrideCmd)
	costAutopilotCmd.AddCommand(costAutopilotClearCmd)
	costCmd.AddCommand(costAutopilotCmd)
}

// costAutopilotError maps what an autopilot route answers into what the user
// reads. A 404 or 405 is the route missing on a platform that predates the
// autopilot, except the cluster routes' own 404, which says the cluster is
// not a live one of the organisation (exit 3). The writes' permission
// refusal arrives in the RBAC shape and keeps its exit 7 and the permission
// it names.
func costAutopilotError(routeError error, operation string, method string, clusterRoute bool) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) &&
		(unexpected.StatusCode == http.StatusNotFound || unexpected.StatusCode == http.StatusMethodNotAllowed) {
		if clusterRoute && unexpected.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(unexpected.Detail), "cluster") {
			return fmt.Errorf("%s: %w (it is not a live cluster of this organisation)", operation, routeError)
		}
		route := "/api/v1/org/cloud-cost/autopilot"
		if clusterRoute {
			route += "/clusters/{cluster_id}"
		}
		return withExitCode(exitError, fmt.Errorf(
			"this platform does not serve the cost autopilot: %s %s is not registered, so this platform predates it", method, route))
	}
	return fmt.Errorf("%s: %w", operation, routeError)
}

// costAutopilotUpdateFromFlags builds the partial policy write from the flags
// given, and only those.
func costAutopilotUpdateFromFlags(cmd *cobra.Command) (client.CostAutopilotPolicyUpdate, error) {
	var update client.CostAutopilotPolicyUpdate
	flags := cmd.Flags()
	if flags.Changed("default") {
		entries, _ := flags.GetStringArray("default")
		update.Defaults = map[string]string{}
		for _, entry := range entries {
			kind, tier, found := strings.Cut(entry, "=")
			kind = strings.ToLower(strings.TrimSpace(kind))
			tier = strings.ToLower(strings.TrimSpace(tier))
			if !found || kind == "" || tier == "" {
				return update, withExitCode(exitUsage, fmt.Errorf("--default %q is not KIND=TIER, e.g. development=managed", entry))
			}
			if _, repeated := update.Defaults[kind]; repeated {
				return update, withExitCode(exitUsage, fmt.Errorf("--default names %s more than once", kind))
			}
			update.Defaults[kind] = tier
		}
	}
	if flags.Changed("quiet-hours") {
		window, _ := flags.GetString("quiet-hours")
		start, end, found := strings.Cut(strings.TrimSpace(window), "-")
		if !found || strings.TrimSpace(start) == "" || strings.TrimSpace(end) == "" {
			return update, withExitCode(exitUsage, fmt.Errorf("--quiet-hours %q is not START-END, e.g. 22:00-07:00", window))
		}
		timezone, _ := flags.GetString("timezone")
		update.QuietHours = &client.CostAutopilotQuietHours{Start: strings.TrimSpace(start), End: strings.TrimSpace(end),
			Timezone: strings.TrimSpace(timezone)}
	}
	update.ClearQuietHours, _ = flags.GetBool("clear-quiet-hours")
	if flags.Changed("notification-route") {
		routeID, _ := flags.GetString("notification-route")
		routeID = strings.TrimSpace(routeID)
		update.NotificationRouteID = &routeID
	}
	update.ClearNotificationRoute, _ = flags.GetBool("clear-notification-route")
	if len(update.Body()) == 0 {
		return update, withExitCode(exitUsage, errors.New(
			"pass at least one of --default, --quiet-hours, --clear-quiet-hours, --notification-route or --clear-notification-route"))
	}
	return update, nil
}

// costAutopilotOverrideFromFlags builds the override request. The expiry is
// sent only when --expires-at or --expires-in was given.
func costAutopilotOverrideFromFlags(cmd *cobra.Command) (client.CostAutopilotOverrideRequest, error) {
	flags := cmd.Flags()
	tier, _ := flags.GetString("tier")
	reason, _ := flags.GetString("reason")
	request := client.CostAutopilotOverrideRequest{Tier: strings.ToLower(strings.TrimSpace(tier)), Reason: strings.TrimSpace(reason)}
	if flags.Changed("expires-at") {
		raw, _ := flags.GetString("expires-at")
		expiresAt, parseError := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if parseError != nil {
			return request, withExitCode(exitUsage, fmt.Errorf("--expires-at %q is not an RFC 3339 time, e.g. 2026-10-01T00:00:00Z", raw))
		}
		formatted := expiresAt.UTC().Format(time.RFC3339)
		request.ExpiresAt = &formatted
	}
	if flags.Changed("expires-in") {
		duration, _ := flags.GetDuration("expires-in")
		if duration <= 0 {
			return request, withExitCode(exitUsage, errors.New("--expires-in must be a positive duration, e.g. 72h"))
		}
		formatted := time.Now().Add(duration).UTC().Format(time.RFC3339)
		request.ExpiresAt = &formatted
	}
	return request, nil
}

// costAutopilotKinds is the order the environment kinds are shown in: the
// platform's own list, then any kind the defaults carry that it did not name.
func costAutopilotKinds(policy *client.CostAutopilotPolicy) []string {
	kinds := append([]string{}, policy.EnvironmentKinds...)
	listed := map[string]bool{}
	for _, kind := range kinds {
		listed[kind] = true
	}
	extra := []string{}
	for kind := range policy.Defaults {
		if !listed[kind] {
			extra = append(extra, kind)
		}
	}
	sort.Strings(extra)
	return append(kinds, extra...)
}

func costAutopilotTierOrUnset(tier string) string {
	if tier == "" {
		return "—"
	}
	return tier
}

// costAutopilotEnvironment shows a cluster's environment label with the kind
// it maps to when the two differ.
func costAutopilotEnvironment(cluster client.CostAutopilotCluster) string {
	label := strings.TrimSpace(cluster.Environment)
	switch {
	case label == "":
		return "none (" + cluster.EnvironmentKind + ")"
	case strings.EqualFold(label, cluster.EnvironmentKind):
		return label
	default:
		return label + " (" + cluster.EnvironmentKind + ")"
	}
}

// costAutopilotSource says what set a cluster's tier.
func costAutopilotSource(cluster client.CostAutopilotCluster) string {
	if cluster.Source != "override" {
		return "its environment"
	}
	if cluster.Override != nil && cluster.Override.ExpiresAt != nil && *cluster.Override.ExpiresAt != "" {
		return "override until " + *cluster.Override.ExpiresAt
	}
	return "override, no expiry"
}

// costAutopilotClusterNotes is what goes under a cluster's row: the sentence
// a card shows, then the override's own reason and who set it.
func costAutopilotClusterNotes(cluster client.CostAutopilotCluster) []string {
	notes := []string{}
	if reason := strings.TrimSpace(cluster.Reason); reason != "" {
		notes = append(notes, reason)
	}
	if cluster.Override != nil {
		override := fmt.Sprintf("Override %q set by %s", cluster.Override.Reason, cluster.Override.SetBy)
		if cluster.Override.SetAt != "" {
			override += " at " + cluster.Override.SetAt
		}
		notes = append(notes, override+".")
	}
	return notes
}

func renderCostAutopilot(out io.Writer, policy *client.CostAutopilotPolicy) {
	if policy.IsSet {
		line := "Cost autopilot policy"
		if policy.UpdatedBy != nil && *policy.UpdatedBy != "" {
			line += ", last changed by " + *policy.UpdatedBy
		}
		if policy.UpdatedAt != nil && *policy.UpdatedAt != "" {
			line += " at " + *policy.UpdatedAt
		}
		_, _ = fmt.Fprintln(out, line)
	} else {
		_, _ = fmt.Fprintln(out, "Cost autopilot policy: never set, so every environment kind is hands-off (every change waits for a person).")
	}
	if policy.QuietHours != nil {
		_, _ = fmt.Fprintf(out, "  Quiet hours: %s to %s %s (the autopilot starts no change of its own then)\n",
			policy.QuietHours.Start, policy.QuietHours.End, policy.QuietHours.Timezone)
	} else {
		_, _ = fmt.Fprintln(out, "  Quiet hours: none")
	}
	if policy.NotificationRouteID != nil && *policy.NotificationRouteID != "" {
		_, _ = fmt.Fprintf(out, "  Pre-notices go to notification route %s\n", *policy.NotificationRouteID)
	} else {
		_, _ = fmt.Fprintln(out, "  Pre-notices go through the organisation's default routing")
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Tier by environment kind:")
	kindWriter := newCostTable(out)
	kindWriter.AppendHeader(table.Row{"Environment kind", "Tier", "Recommended"})
	for _, kind := range costAutopilotKinds(policy) {
		kindWriter.AppendRow(table.Row{kind, costAutopilotTierOrUnset(policy.Defaults[kind]),
			costAutopilotTierOrUnset(policy.RecommendedDefaults[kind])})
	}
	kindWriter.Render()

	if len(policy.Tiers) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Tiers:")
		tierWriter := newCostTable(out)
		tierWriter.AppendHeader(table.Row{"Tier", "Does on its own", "Proposes", "Rollback window"})
		for _, tier := range policy.Tiers {
			name := tier.Tier
			if tier.Name != "" && !strings.EqualFold(tier.Name, tier.Tier) {
				name += "\n(" + tier.Name + ")"
			}
			tierWriter.AppendRow(table.Row{
				name,
				text.WrapSoft(tier.Does, costAutopilotTierTextWidthMax),
				text.WrapSoft(tier.Proposes, costAutopilotTierTextWidthMax),
				text.WrapSoft(tier.RollbackWindow, 16),
			})
		}
		tierWriter.Render()
	}

	_, _ = fmt.Fprintln(out)
	if len(policy.Clusters) == 0 {
		_, _ = fmt.Fprintln(out, "No live cluster yet.")
		return
	}
	_, _ = fmt.Fprintf(out, "Clusters (%d):\n", len(policy.Clusters))
	renderCostAutopilotClusters(out, policy.Clusters)
}

func renderCostAutopilotClusters(out io.Writer, clusters []client.CostAutopilotCluster) {
	header := table.Row{"Cluster", "Environment", "Tier", "Set by"}
	rows := make([]table.Row, 0, len(clusters))
	for _, cluster := range clusters {
		rows = append(rows, table.Row{
			text.WrapSoft(cluster.ClusterName, costAutopilotClusterWidthMax),
			text.WrapSoft(costAutopilotEnvironment(cluster), costAutopilotClusterWidthMax),
			cluster.Tier,
			costAutopilotSource(cluster),
		})
	}
	// A note spans the whole row under the cluster it explains, wrapped to
	// the table's own width so it never widens it (see renderCloudLedger).
	noteWidth := len(header) - 1
	for column := range header {
		width := text.LongestLineLen(fmt.Sprint(header[column]))
		for _, row := range rows {
			width = max(width, text.LongestLineLen(fmt.Sprint(row[column])))
		}
		noteWidth += width
	}
	writer := newCostTable(out)
	writer.AppendHeader(header)
	for index, cluster := range clusters {
		writer.AppendRow(rows[index])
		for _, note := range costAutopilotClusterNotes(cluster) {
			wrapped := wrapCostLedgerNote(note, noteWidth)
			writer.AppendRow(table.Row{wrapped, wrapped, wrapped, wrapped},
				table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
		}
	}
	writer.Render()
}

// renderCostAutopilotClusterChange confirms an override set or cleared with
// the cluster's tier as it now stands.
func renderCostAutopilotClusterChange(out io.Writer, cluster *client.CostAutopilotCluster) {
	if cluster.Source == "override" {
		expiry := "no expiry"
		if cluster.Override != nil && cluster.Override.ExpiresAt != nil && *cluster.Override.ExpiresAt != "" {
			expiry = "until " + *cluster.Override.ExpiresAt
		}
		_, _ = fmt.Fprintf(out, "%s is now %s by override (%s).\n", cluster.ClusterName, cluster.Tier, expiry)
	} else {
		_, _ = fmt.Fprintf(out, "%s takes its environment's tier: %s.\n", cluster.ClusterName, cluster.Tier)
	}
	renderCostAutopilotClusters(out, []client.CostAutopilotCluster{*cluster})
}
