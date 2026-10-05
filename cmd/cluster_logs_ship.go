package cmd

// `ankra cluster logs-ship status|enable|disable` (ankra-t5jf5.34.11.7): the
// CLI surface over GET/PUT /api/v1/org/clusters/{cluster_id}/hosted-logs, the
// per-cluster "Ship logs to Ankra" switch.
//
// Hosted log shipping is opt-in per cluster and off by default: customer log
// content leaves a cluster only after a member with clusters.write turns it
// on. Because turning it on sends data out of the cluster, `enable` says what
// is sent and asks first; `disable` sends nothing, so it does not ask.
//
// The cluster is resolved exactly like the sibling `cluster agent ...` verbs,
// through the shared --cluster override or the selected cluster.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// hostedLogsRequestTimeout bounds one read or write of the switch.
const hostedLogsRequestTimeout = 30 * time.Second

// hostedLogsEnableExplanation is what `enable` prints before it asks: what
// is sent, where it goes, how long it is kept, who can read it, and how to
// stop it.
func hostedLogsEnableExplanation(clusterName string) string {
	return fmt.Sprintf("Shipping logs to Ankra sends the log lines of every running container in cluster '%s' "+
		"to Ankra's hosted log store, where they are kept for 7 days and can be read by the members of your "+
		"organisation. Turn it off any time with 'ankra cluster logs-ship disable'.", clusterName)
}

// The sentences the two conditions that keep a stored switch from taking
// effect are reported with, shared by every verb so `status` and a write
// cannot describe the same state two ways.
const (
	hostedLogsUnavailableSentence = "Ankra has not turned hosted logging on for this platform yet: " +
		"the switch is stored, but nothing ships until it does."
	hostedLogsAgentTooOldSentence = "This cluster's agent is too old to follow the switch. " +
		"Upgrade the agent with 'ankra cluster agent upgrade' so it does."
)

func newClusterLogsShipCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "logs-ship",
		Short: "Turn shipping this cluster's logs to Ankra on or off",
		Long: `Ankra can keep a cluster's container logs in its hosted log store, so they can
be read from Ankra with no logging stack of your own to run or connect.

Shipping is opt-in per cluster and off by default: no log content leaves the
cluster until a member with permission to change cluster settings
(clusters.write) turns it on, here or in the cluster's settings in the portal.
While it is on, the cluster's agent sends the log lines of every running
container to Ankra's hosted log store, where they are kept for 7 days and can
be read by the members of your organisation. 'disable' stops it at any time.

The agent picks the switch up at its next check-in. An agent too old to follow
it does not, and 'status' says so.`,
		Example: `  ankra cluster logs-ship status
  ankra cluster logs-ship enable --cluster prod
  ankra cluster logs-ship enable --cluster prod --yes
  ankra cluster logs-ship disable --cluster prod`,
	}
	command.AddCommand(
		newClusterLogsShipStatusCommand(),
		newClusterLogsShipSwitchCommand(true),
		newClusterLogsShipSwitchCommand(false),
	)
	return command
}

func newClusterLogsShipStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show whether the cluster ships its logs to Ankra",
		Long: `Show whether the cluster ships its logs to Ankra's hosted log store, whether
hosted logging is available on this platform yet, whether the cluster's agent
is new enough to follow the switch, and when the switch was last changed.

-o json prints the platform's answer as it is.`,
		Example: `  ankra cluster logs-ship status
  ankra cluster logs-ship status --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterLogsShipStatus(command)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterLogsShipSwitchCommand(enabled bool) *cobra.Command {
	command := &cobra.Command{
		Use:   "disable",
		Short: "Stop shipping the cluster's logs to Ankra",
		Long: `Turn hosted log shipping off for the cluster. The agent stops sending log lines
at its next check-in. Nothing is sent by turning it off, so this does not ask
for confirmation.`,
		Example: `  ankra cluster logs-ship disable
  ankra cluster logs-ship disable --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterLogsShipSwitch(command, enabled)
		},
	}
	if enabled {
		command.Use = "enable"
		command.Short = "Ship the cluster's logs to Ankra's hosted log store"
		command.Long = `Turn hosted log shipping on for the cluster. The log lines of every running
container in the cluster are sent to Ankra's hosted log store, where they are
kept for 7 days and can be read by the members of your organisation. Turn it
off any time with 'ankra cluster logs-ship disable'.

Because log content leaves the cluster, enable says what is sent and asks for
confirmation first. --yes skips the question; without a terminal to ask on
(a script, CI) it is required.

If Ankra has not turned hosted logging on for the platform yet, the switch is
stored and nothing ships until it does.`
		command.Example = `  ankra cluster logs-ship enable
  ankra cluster logs-ship enable --cluster prod --yes`
		command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	}
	registerStructuredOutputFlags(command)
	return command
}

// hostedLogsContext bounds one request by the command's own context, so an
// interrupted command stops the request instead of waiting it out.
func hostedLogsContext(command *cobra.Command) (context.Context, context.CancelFunc) {
	parent := command.Context()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, hostedLogsRequestTimeout)
}

func runClusterLogsShipStatus(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}

	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	state, getError := apiClient.GetClusterHostedLogs(ctx, cluster.ID)
	if getError != nil {
		return hostedLogsError(getError, cluster, false)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, state)
	}

	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Hosted log shipping for cluster '%s':\n\n", cluster.Name)
	printHostedLogsState(out, state)
	if !state.ShippingEnabled {
		_, _ = fmt.Fprintln(out, "\nTurn it on with 'ankra cluster logs-ship enable'.")
	}
	return nil
}

func runClusterLogsShipSwitch(command *cobra.Command, enabled bool) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	if enabled {
		if confirmError := confirmHostedLogShipping(command, cluster); confirmError != nil {
			return confirmError
		}
	}

	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	state, setError := apiClient.SetClusterHostedLogShipping(ctx, cluster.ID, enabled)
	if setError != nil {
		return hostedLogsError(setError, cluster, true)
	}
	// The route answers the state it stored. One that disagrees with the
	// request is a change that did not take effect, so it is reported AND
	// exits non-zero: a script running `enable --yes` must not read it as
	// the switch having moved.
	disagreement := hostedLogsDisagreement(cluster, enabled, state.ShippingEnabled)
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, state); encodeError != nil {
			return encodeError
		}
		return disagreement
	}

	out := command.OutOrStdout()
	if state.ShippingEnabled {
		_, _ = fmt.Fprintf(out, "Hosted log shipping enabled for cluster '%s'.\n\n", cluster.Name)
	} else {
		_, _ = fmt.Fprintf(out, "Hosted log shipping disabled for cluster '%s'.\n\n", cluster.Name)
	}
	printHostedLogsState(out, state)
	switch {
	case disagreement != nil:
		return disagreement
	case !state.Available || !state.AgentSupportsSwitch:
		// printHostedLogsState has already said why nothing changes yet.
	case enabled:
		_, _ = fmt.Fprintln(out, "\nThe agent starts sending log lines at its next check-in. "+
			"Turn it off any time with 'ankra cluster logs-ship disable'.")
	default:
		_, _ = fmt.Fprintln(out, "\nThe agent stops sending log lines at its next check-in.")
	}
	return nil
}

// confirmHostedLogShipping says what turning shipping on sends and asks for
// it to be confirmed. --yes skips the question; without a terminal to ask
// on, --yes is required, because a script that never saw the explanation
// must not send a cluster's logs out by accident. Everything goes to stderr
// so -o json|yaml stays parseable.
func confirmHostedLogShipping(command *cobra.Command, cluster client.ClusterListItem) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintln(errOut, hostedLogsEnableExplanation(cluster.Name))
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"enabling hosted log shipping sends the container logs of cluster '%s' to Ankra and needs "+
				"confirmation, but there is no terminal to ask on; re-run with --yes to confirm", cluster.Name))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		"\nShip this cluster's logs to Ankra? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// hostedLogsDisagreement is the error for a write whose stored state is not
// the one requested, or nil when they agree.
func hostedLogsDisagreement(cluster client.ClusterListItem, requested bool, stored bool) error {
	if requested == stored {
		return nil
	}
	return withExitCode(exitError, fmt.Errorf(
		"the platform stored hosted log shipping as %s for cluster '%s', not %s as requested; "+
			"run 'ankra cluster logs-ship status' to check it",
		hostedLogsShippingLabel(stored), cluster.Name, hostedLogsShippingLabel(requested)))
}

// hostedLogsError maps a refusal from the switch routes onto what the user
// can act on. A 403 on the write is a missing clusters.write and exits 7; a
// 404 carrying the platform's own detail is a cluster outside the selected
// organisation and exits 3; the router's own 404 (detail "Not Found") is a
// platform that predates the switch, which is not "the cluster does not
// exist" and so exits 1. A 404 with no detail at all says neither, so it is
// reported as unknown rather than guessed at. Anything else keeps the
// platform's detail.
func hostedLogsError(apiError error, cluster client.ClusterListItem, writing bool) error {
	operation := fmt.Sprintf("reading hosted log shipping for cluster '%s'", cluster.Name)
	if writing {
		operation = fmt.Sprintf("changing hosted log shipping for cluster '%s'", cluster.Name)
	}

	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change cluster settings (clusters.write) to switch hosted log shipping "+
					"for cluster '%s'. Ask an organisation admin for a role that has it", cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		if unexpected.Detail == "" {
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer hosted log shipping yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		}
		if unexpected.Detail == routeAbsentDetail {
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer hosted log shipping yet (the route answered 404). "+
					"Check the selected organisation with 'ankra org current' and try again once the platform has it",
				operation))
		}
		return withExitCode(exitNotFound, fmt.Errorf(
			"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
				"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

func printHostedLogsState(out io.Writer, state *client.ClusterHostedLogs) {
	_, _ = fmt.Fprintf(out, "  Shipping:       %s\n", hostedLogsShippingLabel(state.ShippingEnabled))
	_, _ = fmt.Fprintf(out, "  Hosted logging: %s\n", hostedLogsAvailabilityLabel(state.Available))
	_, _ = fmt.Fprintf(out, "  Agent:          %s\n", hostedLogsAgentLabel(state.AgentSupportsSwitch))
	_, _ = fmt.Fprintf(out, "  Last changed:   %s\n", hostedLogsChangedLabel(state.ChangedAt))

	var notes []string
	if !state.Available {
		notes = append(notes, hostedLogsUnavailableSentence)
	}
	if !state.AgentSupportsSwitch {
		notes = append(notes, hostedLogsAgentTooOldSentence)
	}
	for _, note := range notes {
		_, _ = fmt.Fprintf(out, "\n%s\n", note)
	}
}

func hostedLogsShippingLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func hostedLogsAvailabilityLabel(available bool) string {
	if available {
		return "available on this platform"
	}
	return "not yet available on this platform"
}

// hostedLogsAgentLabel reports what the agent advertised at its last
// identify, which is a fact about the running agent, not about the switch.
func hostedLogsAgentLabel(supportsSwitch bool) string {
	if supportsSwitch {
		return "follows the switch"
	}
	return "too old to follow the switch (upgrade the agent)"
}

func hostedLogsChangedLabel(changedAt *string) string {
	if changedAt == nil || *changedAt == "" {
		return "never"
	}
	return fmt.Sprintf("%s (%s)", formatTimeAgo(*changedAt), *changedAt)
}

func init() {
	clusterCmd.AddCommand(newClusterLogsShipCommand())
}
