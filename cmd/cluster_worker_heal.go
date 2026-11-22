package cmd

// `ankra cluster worker-heal get|set` (ankra-hq41xb.15): the CLI surface over
// GET/PUT /api/v1/org/clusters/{cluster_id}/worker-self-heal, the per-cluster
// switch for Ankra's automatic restart of a NotReady worker node. It is the
// worker twin of `ankra cluster control-plane-heal` and resolves the cluster
// the same way, through the shared --cluster override or the selected
// cluster.

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterWorkerHealCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "worker-heal",
		Short: "Show or change whether Ankra restarts a NotReady worker node",
		Long: `When a worker node has been NotReady for about ten minutes while every control
plane is Ready, Ankra can restart that one node through your cloud provider: one
worker of a cluster at a time and at most once per node per day, only on a
cluster Ankra provisioned. It never stops, deprovisions or scales anything, and
when the restart does not bring the node back the cluster's notifications name
the node.

The switch is per cluster and on by default. Turning it off leaves a NotReady
worker for you to handle. Changing it needs permission to change cluster
settings (clusters.write).`,
		Example: `  ankra cluster worker-heal get
  ankra cluster worker-heal set --enabled=false --cluster prod`,
	}
	command.AddCommand(newClusterWorkerHealGetCommand(), newClusterWorkerHealSetCommand())
	return command
}

func newClusterWorkerHealGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get",
		Short: "Show the switch and the latest NotReady worker episode",
		Long: `Show whether Ankra may restart a NotReady worker of the cluster, and the latest
episode: which node, how it ended or why Ankra is not restarting it yet, and
whether the platform acted or only recorded what it would have done (shadow).

-o json prints the platform's answer as it is.`,
		Example: `  ankra cluster worker-heal get
  ankra cluster worker-heal get --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runWorkerHealGet(command)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterWorkerHealSetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "set --enabled=true|false",
		Short: "Turn the automatic restart of a NotReady worker on or off",
		Long: `Turn Ankra's automatic restart of a NotReady worker on or off for the cluster.
--enabled is required, so a change is always explicit. Turning it off sends
nothing anywhere, so this does not ask for confirmation. Every change is
recorded in the organisation's audit log.`,
		Example: `  ankra cluster worker-heal set --enabled=false
  ankra cluster worker-heal set --enabled=true --cluster prod`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runWorkerHealSet(command)
		},
	}
	command.Flags().Bool("enabled", false, "Whether Ankra may restart a NotReady worker (required)")
	registerStructuredOutputFlags(command)
	return command
}

func runWorkerHealGet(command *cobra.Command) error {
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
	state, getError := apiClient.GetWorkerSelfHeal(ctx, cluster.ID)
	if getError != nil {
		return clusterSettingRouteError("reading worker self-heal", cluster, getError)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, state)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Worker self-heal for cluster '%s':\n\n", cluster.Name)
	printWorkerHeal(out, state)
	return nil
}

func runWorkerHealSet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	if !command.Flags().Changed("enabled") {
		return withExitCode(exitUsage, errors.New(
			"--enabled=true or --enabled=false is required; see 'ankra cluster worker-heal set --help'"))
	}
	enabled, _ := command.Flags().GetBool("enabled")
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	state, setError := apiClient.SetWorkerSelfHeal(ctx, cluster.ID, enabled)
	if setError != nil {
		return clusterSettingRouteError("changing worker self-heal", cluster, setError)
	}
	var disagreement error
	if state.Enabled != enabled {
		disagreement = withExitCode(exitError, fmt.Errorf(
			"the platform stored worker self-heal as %s for cluster '%s', not %s as requested; "+
				"run 'ankra cluster worker-heal get' to check it",
			controlPlaneHealLabel(state.Enabled), cluster.Name, controlPlaneHealLabel(enabled)))
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, state); encodeError != nil {
			return encodeError
		}
		return disagreement
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Worker self-heal %s for cluster '%s'.\n\n", controlPlaneHealLabel(state.Enabled), cluster.Name)
	printWorkerHeal(out, state)
	return disagreement
}

func printWorkerHeal(out io.Writer, state *client.WorkerSelfHeal) {
	_, _ = fmt.Fprintf(out, "  Automatic restart: %s\n", controlPlaneHealLabel(state.Enabled))
	incident := state.LatestIncident
	if incident == nil {
		_, _ = fmt.Fprintln(out, "  Latest episode:    none (no NotReady worker recorded)")
		return
	}
	status := incident.State
	if incident.Outcome != "" {
		status += ", " + incident.Outcome
	}
	_, _ = fmt.Fprintf(out, "  Latest episode:    node %s, %s (%s mode)\n", incident.NodeName, status, incident.Mode)
	_, _ = fmt.Fprintf(out, "  NotReady since:    %s (%s)\n", formatTimeAgo(incident.NotReadySince), incident.NotReadySince)
	if incident.LastRestartAt != nil {
		_, _ = fmt.Fprintf(out, "  Last restart:      %s\n", *incident.LastRestartAt)
	}
	if incident.Reason != "" {
		_, _ = fmt.Fprintf(out, "  Reason:            %s\n", incident.Reason)
	}
	if incident.Mode == "shadow" {
		_, _ = fmt.Fprintln(out, "\nAnkra is observing only on this platform: it records what it would do and restarts nothing.")
	}
}

// clusterSettingRouteError names the operation and the cluster on a refusal
// from a per-cluster settings route. The router's own 404 is a platform that
// predates the route, which is not "the cluster does not exist" and so exits
// 1; every other answer keeps the exit code its status maps to (403 exits 7,
// the platform's own not-found exits 3).
func clusterSettingRouteError(operation string, cluster client.ClusterListItem, apiError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(apiError, &unexpected) && unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == routeAbsentDetail {
		return withExitCode(exitError, fmt.Errorf(
			"%s for cluster '%s': this Ankra platform does not serve this route yet (it answered 404)", operation, cluster.Name))
	}
	return fmt.Errorf("%s for cluster '%s': %w", operation, cluster.Name, apiError)
}

func init() {
	clusterCmd.AddCommand(newClusterWorkerHealCommand())
}
