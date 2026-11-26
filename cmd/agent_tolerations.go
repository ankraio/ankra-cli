package cmd

// `ankra cluster agent tolerations get|set|clear` (ankra-o4jy76.2): the CLI
// surface over GET/PUT /api/v1/org/clusters/{cluster_id}/agent/tolerations,
// the extra tolerations Ankra holds for the cluster agent's own pod.
//
// A toleration set by hand on the Deployment or with `helm upgrade --set` is
// rendered away by the next agent upgrade, and --set on the chart's
// tolerations list replaces the ones Ankra's install put there. A held list
// is added to them on every install, upgrade and re-render Ankra generates.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterAgentTolerationsCommand() *cobra.Command {
	tolerationsCommand := &cobra.Command{
		Use:   "tolerations",
		Short: "Read and set the extra taints the cluster agent tolerates",
		Long: `The cluster agent runs as one pod in the cluster, and Kubernetes keeps it off
every node whose taints it does not tolerate. These tolerations let it run on
nodes you have tainted, for example the only nodes with a public IP.

They are stored on the platform and added to the tolerations the agent's Helm
release already lists, in every install, upgrade and re-render Ankra generates
for this cluster - unlike one set by hand, which the next agent upgrade
renders away again. A toleration lets the agent run on a node; it does not
make it prefer one.`,
	}
	tolerationsCommand.AddCommand(newClusterAgentTolerationsGetCommand(),
		newClusterAgentTolerationsSetCommand(), newClusterAgentTolerationsClearCommand())
	return tolerationsCommand
}

func newClusterAgentTolerationsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the extra taints the cluster agent tolerates",
		Long: `Show the tolerations Ankra stores for this cluster's agent, when they last
changed, and how the platform applied them. The tolerations the agent's own
Helm release lists are not part of this and are not shown.`,
		Example: `  ankra cluster agent tolerations get
  ankra cluster agent tolerations get --cluster edge-01 -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentTolerationsGet(command)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runClusterAgentTolerationsGet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	tolerations, getError := apiClient.GetAgentTolerations(command.Context(), cluster.ID)
	if getError != nil {
		// Returned as it came: the platform owns this surface's refusals.
		return getError
	}
	return printAgentTolerations(command, format, cluster, tolerations, "Agent tolerations for cluster '%s':")
}

func newClusterAgentTolerationsSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Set the extra taints the cluster agent tolerates",
		Long: `Store the taints this cluster's agent tolerates, as key[=value]:Effect with
Effect NoSchedule, PreferNoSchedule or NoExecute. Without a value the
toleration matches any value of the key. Repeat --toleration for more than
one: the list you pass replaces the stored one as a whole.

Tolerating a NoExecute taint also means the agent is not evicted from a node
that carries it, for as long as the taint is there. Keep it to taints of your
own that mark a dedicated node, not one your tooling sets when a node is
failing. Refused: the taints Kubernetes puts on a node that cannot run pods
(node.kubernetes.io/... and node.cloudprovider.kubernetes.io/...), and the
ones the Cluster Autoscaler, Karpenter, Cilium and Ankra put on a node they
are removing, preparing or keeping work off (ToBeDeletedByClusterAutoscaler,
DeletionCandidateOfClusterAutoscaler, karpenter.sh/..., node.cilium.io/...,
ankra.io/... and ankra.cloud/...).

The write always stores the list. Whether it reaches the agent now depends on
the agent: an online agent new enough to accept it re-renders its release and
restarts, an older one from the first upgrade or re-render after it has been
upgraded to a release that accepts it (the upgrade itself is run by the older
release), and an offline one when it reconnects and is upgraded. The command prints the platform's sentence
saying which happened.

The platform validates the list and its refusal is printed as it came.`,
		Example: `  ankra cluster agent tolerations set --toleration purpose=db:NoSchedule
  ankra cluster agent tolerations set --toleration dedicated:NoSchedule --toleration dedicated:NoExecute
  ankra cluster agent tolerations set --toleration purpose=db:NoSchedule --cluster edge-01`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentTolerationsSet(command)
		},
	}
	setCommand.Flags().StringArray("toleration", nil,
		"Taint the agent tolerates, as key[=value]:Effect with Effect NoSchedule, PreferNoSchedule "+
			"or NoExecute; repeat it for more than one")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func runClusterAgentTolerationsSet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	values, _ := command.Flags().GetStringArray("toleration")
	// An empty list clears the stored one, so it has to be asked for by name.
	if len(values) == 0 {
		return withExitCode(exitUsage, errors.New("nothing to set: pass at least one --toleration key[=value]:Effect, "+
			"or run 'ankra cluster agent tolerations clear' to remove the stored ones"))
	}
	tolerations := make([]client.AgentToleration, 0, len(values))
	for _, value := range values {
		parsed, parseError := parseAgentCIToleration(value)
		if parseError != nil {
			return withExitCode(exitUsage, parseError)
		}
		tolerations = append(tolerations, client.AgentToleration(parsed))
	}

	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	stored, replaceError := apiClient.ReplaceAgentTolerations(command.Context(), cluster.ID, tolerations)
	if replaceError != nil {
		return replaceError
	}
	return printAgentTolerations(command, format, cluster, stored, "Agent tolerations updated for cluster '%s'.")
}

func newClusterAgentTolerationsClearCommand() *cobra.Command {
	clearCommand := &cobra.Command{
		Use:   "clear",
		Short: "Remove the extra taints the cluster agent tolerates",
		Long: `Remove every toleration Ankra stores for this cluster's agent. The tolerations
its own Helm release lists stay.

An online agent re-renders its release without them and restarts. If every
node that has room for it carries a taint it then no longer tolerates, the
agent stays Pending and the cluster shows as offline, so the command asks
first. --yes skips the question, and a run with no terminal needs it.`,
		Example: `  ankra cluster agent tolerations clear
  ankra cluster agent tolerations clear --cluster edge-01 --yes`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentTolerationsClear(command)
		},
	}
	clearCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clearCommand)
	return clearCommand
}

func runClusterAgentTolerationsClear(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	if confirmError := confirmAgentTolerationsClear(command, cluster); confirmError != nil {
		return confirmError
	}
	stored, replaceError := apiClient.ReplaceAgentTolerations(command.Context(), cluster.ID, nil)
	if replaceError != nil {
		return replaceError
	}
	return printAgentTolerations(command, format, cluster, stored, "Agent tolerations cleared for cluster '%s'.")
}

// confirmAgentTolerationsClear asks before the agent loses tolerations it may
// need to run at all. Everything goes to stderr so -o json|yaml stays
// parseable.
func confirmAgentTolerationsClear(command *cobra.Command, cluster client.ClusterListItem) error {
	if yes, _ := command.Flags().GetBool("yes"); yes {
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"clearing the agent tolerations of cluster '%s' can leave the agent with no node to run on and needs "+
				"confirmation, but there is no terminal to ask on; nothing was changed. Re-run with --yes to confirm",
			cluster.Name))
	}
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "The Ankra agent in cluster '%s' will no longer tolerate the taints stored for it. If no "+
		"other node has room for it, it stays Pending and the cluster shows as offline.\n", cluster.Name)
	if promptError := confirmPrompt(command.InOrStdin(), errOut, "Clear the agent tolerations? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// printAgentTolerations renders the platform's answer in the requested format:
// the heading, the list, then the platform's sentence for the apply state on
// its own line, as it came.
func printAgentTolerations(command *cobra.Command, format outputFormat, cluster client.ClusterListItem,
	tolerations *client.AgentTolerations, heading string) error {
	if tolerations == nil {
		return fmt.Errorf("the platform returned no agent tolerations for cluster %q", cluster.Name)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, tolerations)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, heading+"\n\n", cluster.Name)
	printAgentTolerationsList(out, tolerations.Tolerations)
	if tolerations.UpdatedAt != nil && *tolerations.UpdatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Last changed:    %s\n", formatTimeAgo(*tolerations.UpdatedAt))
	} else {
		_, _ = fmt.Fprintln(out, "  Last changed:    never")
	}
	applyState := tolerations.ApplyState
	if applyState == "" {
		applyState = "unknown"
	}
	_, _ = fmt.Fprintf(out, "  Apply state:     %s\n", applyState)
	if tolerations.Message != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", tolerations.Message)
	}
	return nil
}

func printAgentTolerationsList(out io.Writer, tolerations []client.AgentToleration) {
	if len(tolerations) == 0 {
		_, _ = fmt.Fprintln(out, "  Tolerations:     none")
		return
	}
	for index, toleration := range tolerations {
		label := "               "
		if index == 0 {
			label = "Tolerations:   "
		}
		_, _ = fmt.Fprintf(out, "  %s  %s\n", label, agentTolerationLabel(toleration))
	}
}

// agentTolerationLabel writes a toleration the way --toleration takes it.
func agentTolerationLabel(toleration client.AgentToleration) string {
	taint := toleration.Key
	if !strings.EqualFold(toleration.Operator, "Exists") {
		taint += "=" + toleration.Value
	}
	return taint + ":" + toleration.Effect
}

func init() {
	clusterAgentCmd.AddCommand(newClusterAgentTolerationsCommand())
}
