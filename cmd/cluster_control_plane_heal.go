package cmd

// `ankra cluster control-plane-heal get|set` (ankra-c550m2): the CLI surface
// over GET/PUT /api/v1/org/clusters/{cluster_id}/control-plane-self-heal, the
// per-cluster switch for Ankra's automatic restart of a NotReady control
// plane (ankra-no2iak).
//
// The switch is on by default and the customer's to turn off. It stops the
// provider restart only; the NotReady card in the product stays. Turning it
// off sends nothing anywhere, so `set` does not ask for confirmation. The
// cluster is resolved like the sibling `cluster logs-ship` verbs, through the
// shared --cluster override or the selected cluster.

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterControlPlaneHealCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "control-plane-heal",
		Short: "Show or change whether Ankra restarts a NotReady control plane",
		Long: `When a control plane node has been NotReady for several minutes while every other
control plane node is Ready and etcd keeps quorum without it, Ankra can restart
that one node through your cloud provider. It never stops, scales or deletes
anything, and it shows each step on the cluster in the product.

The switch is per cluster and on by default. Turning it off stops the restart
only: the NotReady card is still shown. Changing it needs permission to change
cluster settings (clusters.write).`,
		Example: `  ankra cluster control-plane-heal get
  ankra cluster control-plane-heal set --enabled=false --cluster prod`,
	}
	command.AddCommand(newClusterControlPlaneHealGetCommand(), newClusterControlPlaneHealSetCommand())
	return command
}

func newClusterControlPlaneHealGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get",
		Short: "Show the switch and the latest NotReady control plane episode",
		Long: `Show whether Ankra may restart a NotReady control plane of the cluster, and the
latest episode: which node, how it ended or why Ankra is not restarting it yet,
and whether the platform acted or only recorded what it would have done
(shadow).

-o json prints the platform's answer as it is.`,
		Example: `  ankra cluster control-plane-heal get
  ankra cluster control-plane-heal get --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runControlPlaneHealGet(command)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterControlPlaneHealSetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "set --enabled=true|false",
		Short: "Turn the automatic restart of a NotReady control plane on or off",
		Long: `Turn Ankra's automatic restart of a NotReady control plane on or off for the
cluster. --enabled is required, so a change is always explicit. Turning it off
sends nothing anywhere, so this does not ask for confirmation.`,
		Example: `  ankra cluster control-plane-heal set --enabled=false
  ankra cluster control-plane-heal set --enabled=true --cluster prod`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runControlPlaneHealSet(command)
		},
	}
	command.Flags().Bool("enabled", false, "Whether Ankra may restart a NotReady control plane (required)")
	registerStructuredOutputFlags(command)
	return command
}

func runControlPlaneHealGet(command *cobra.Command) error {
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
	state, getError := apiClient.GetControlPlaneHeal(ctx, cluster.ID)
	if getError != nil {
		return controlPlaneHealError(getError, cluster, false)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, state)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Control plane self-heal for cluster '%s':\n\n", cluster.Name)
	printControlPlaneHeal(out, state)
	return nil
}

func runControlPlaneHealSet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	if !command.Flags().Changed("enabled") {
		return withExitCode(exitUsage, errors.New(
			"--enabled=true or --enabled=false is required; see 'ankra cluster control-plane-heal set --help'"))
	}
	enabled, _ := command.Flags().GetBool("enabled")
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	state, setError := apiClient.SetControlPlaneHeal(ctx, cluster.ID, enabled)
	if setError != nil {
		return controlPlaneHealError(setError, cluster, true)
	}
	// A stored state that disagrees with the request is a change that did not
	// take effect: report it and exit non-zero so a script does not read it
	// as the switch having moved.
	var disagreement error
	if state.Enabled != enabled {
		disagreement = withExitCode(exitError, fmt.Errorf(
			"the platform stored control plane self-heal as %s for cluster '%s', not %s as requested; "+
				"run 'ankra cluster control-plane-heal get' to check it",
			controlPlaneHealLabel(state.Enabled), cluster.Name, controlPlaneHealLabel(enabled)))
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, state); encodeError != nil {
			return encodeError
		}
		return disagreement
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Control plane self-heal %s for cluster '%s'.\n\n", controlPlaneHealLabel(state.Enabled), cluster.Name)
	printControlPlaneHeal(out, state)
	return disagreement
}

func controlPlaneHealLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func printControlPlaneHeal(out io.Writer, state *client.ControlPlaneHeal) {
	_, _ = fmt.Fprintf(out, "  Automatic restart: %s\n", controlPlaneHealLabel(state.Enabled))
	incident := state.LatestIncident
	if incident == nil {
		_, _ = fmt.Fprintln(out, "  Latest episode:    none (no NotReady control plane recorded)")
		return
	}
	status := incident.State
	if incident.Outcome != "" {
		status += ", " + incident.Outcome
	}
	_, _ = fmt.Fprintf(out, "  Latest episode:    node %s, %s (%s mode)\n", incident.NodeName, status, incident.Mode)
	_, _ = fmt.Fprintf(out, "  NotReady since:    %s (%s)\n", formatTimeAgo(incident.NotReadySince), incident.NotReadySince)
	_, _ = fmt.Fprintf(out, "  Restart attempts:  %d\n", incident.RestartAttempts)
	if incident.Reason != "" {
		_, _ = fmt.Fprintf(out, "  Reason:            %s\n", incident.Reason)
	}
	if incident.Mode == "shadow" {
		_, _ = fmt.Fprintln(out, "\nAnkra is observing only on this platform: it records what it would do and restarts nothing.")
	}
}

// controlPlaneHealError maps a refusal onto what the user can act on: a 403 on
// the write is a missing clusters.write (exit 7), the platform's own "Cluster
// not found" is a cluster outside the selected organisation (exit 3), and the
// router's own 404 is a platform that predates the switch (exit 1).
func controlPlaneHealError(apiError error, cluster client.ClusterListItem, writing bool) error {
	operation := fmt.Sprintf("reading control plane self-heal for cluster '%s'", cluster.Name)
	if writing {
		operation = fmt.Sprintf("changing control plane self-heal for cluster '%s'", cluster.Name)
	}
	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change cluster settings (clusters.write) to switch control plane "+
					"self-heal for cluster '%s'. Ask an organisation admin for a role that has it", cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		switch unexpected.Detail {
		case "":
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer this switch yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		case routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer the control plane self-heal switch yet (the route answered 404)", operation))
		case hostedLogsClusterNotFoundDetail:
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
					"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
		default:
			return withExitCode(exitError, fmt.Errorf("%s: %w", operation, apiError))
		}
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

func init() {
	clusterCmd.AddCommand(newClusterControlPlaneHealCommand())
}
