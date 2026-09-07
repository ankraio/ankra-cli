package cmd

// `ankra cluster agent ci get|set` (ankra-vn0bd, item 5 of the agent CI
// settings contract): the CLI surface over GET/PUT
// /api/v1/org/clusters/{cluster_id}/agent/ci-settings, which size the
// cluster agent's own pipeline-step scheduler.
//
// Before these routes existed the agent's `ci_worker_count` chart value
// could only be raised with a hand-run `helm upgrade --set`, and the next
// platform-driven agent install or upgrade rendered the chart default (0)
// back over it - so a cluster stopped running pipeline steps for no reason
// anyone could see in Ankra. The setting now lives on the platform, every
// generated Helm command carries it, and this pair of verbs is how it is
// read and written.
//
// The cluster is resolved exactly like the sibling `cluster agent
// status|upgrade` verbs, through the shared --cluster override.

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The sentences the platform's three apply states are reported with. They
// are frozen by the agent CI settings contract and shared by both verbs, so
// the state a `set` reports and the state a later `get` reports cannot drift
// into two vocabularies for the same thing.
const (
	agentCIAppliedSentence = "The agent is re-rendering its release with %d pipeline-step workers; " +
		"it advertises the capability on its next check-in."
	agentCIPendingUpgradeSentence = "The agent on this cluster predates chart values; " +
		"the setting is stored and takes effect with the next agent upgrade."
	agentCIAgentOfflineSentence = "The agent on this cluster is offline; " +
		"the setting is stored and applies when it reconnects and is upgraded."
)

// agentCIApplyStateSentence renders one apply state. An apply state this
// build does not know is a newer platform's, not a broken response, so it is
// reported as itself rather than swallowed.
func agentCIApplyStateSentence(settings *client.AgentCISettings) string {
	switch settings.ApplyState {
	case client.AgentCIApplyStateApplied:
		return fmt.Sprintf(agentCIAppliedSentence, settings.CIWorkerCount)
	case client.AgentCIApplyStatePendingUpgrade:
		return agentCIPendingUpgradeSentence
	case client.AgentCIApplyStateAgentOffline:
		return agentCIAgentOfflineSentence
	case "":
		return ""
	default:
		return fmt.Sprintf("The platform reports apply state %q, which this CLI version does not recognise; "+
			"the setting is stored.", settings.ApplyState)
	}
}

func newClusterAgentCICommand() *cobra.Command {
	ciCommand := &cobra.Command{
		Use:   "ci",
		Short: "Read and set the cluster agent's pipeline-step CI settings",
		Long: `The cluster agent runs Ankra Pipelines steps itself, and these settings
size that: how many steps it runs at once, and the storage class its step
workspaces are carved from.

Both are stored on the platform, so every install or upgrade command Ankra
generates for this cluster carries them - unlike a hand-run
'helm upgrade --set ci_worker_count=...', which the next agent upgrade
renders away again.`,
	}
	ciCommand.AddCommand(newClusterAgentCIGetCommand(), newClusterAgentCISetCommand())
	return ciCommand
}

func newClusterAgentCIGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the cluster agent's pipeline-step CI settings",
		Long: `Show the stored worker count and storage class, the agent version that has
to honour them, whether that agent currently advertises it can run pipeline
steps, and how the last write was applied.

An agent that has not yet re-rendered its release reports pipeline steps as
not advertised even with a non-zero worker count stored: the capability is
what the agent reported on its last check-in, not what the settings ask for.`,
		Example: `  ankra cluster agent ci get
  ankra cluster agent ci get --cluster edge-01 -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentCIGet(command)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runClusterAgentCIGet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	settings, getError := apiClient.GetAgentCISettings(command.Context(), cluster.ID)
	if getError != nil {
		// Returned as it came: the platform owns this surface's refusals
		// (an RBAC 403 naming agents.manage, a 404 for a cluster outside
		// the organisation, a 422 naming the value it rejected) and
		// rewording them here would only put a second, staler vocabulary
		// in front of the user.
		return getError
	}
	if settings == nil {
		return fmt.Errorf("the platform returned no agent CI settings for cluster %q", cluster.Name)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, settings)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Agent CI settings for cluster '%s':\n\n", cluster.Name)
	printAgentCISettings(out, settings)
	return nil
}

func newClusterAgentCISetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Set the cluster agent's pipeline-step CI settings",
		Long: `Store how many pipeline steps this cluster's agent runs at once, and
optionally the storage class its step workspaces are carved from.

--workers 0 disables the agent's pipeline-step scheduler; the cluster keeps
its agent and everything else it does. --storage-class is only sent when you
pass it, so setting the worker count alone keeps the storage class already
stored; pass an empty value to fall back to the cluster's default class.

The write always stores the values. Whether they reach the agent now depends
on the agent: an online agent new enough to accept chart values re-renders
its release immediately, an older one picks them up at its next upgrade, and
an offline one when it reconnects. The command says which of the three
happened.`,
		Example: `  ankra cluster agent ci set --workers 2
  ankra cluster agent ci set --workers 4 --storage-class proxmox-csi
  ankra cluster agent ci set --workers 0 --cluster edge-01`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentCISet(command)
		},
	}
	setCommand.Flags().Int("workers", 0, "Number of pipeline steps the agent runs at once (0 disables the scheduler)")
	setCommand.Flags().String("storage-class", "",
		"Storage class for pipeline step workspaces; pass empty to use the cluster default (omit the flag to keep the stored value)")
	_ = setCommand.MarkFlagRequired("workers")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func runClusterAgentCISet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}

	// --workers is required, so it is always sent; --storage-class is only
	// sent when named, which is what keeps a worker-count change from
	// resetting a storage class someone set earlier.
	update := client.AgentCISettingsUpdate{
		CIWorkerCount:  changedIntFlag(command, "workers"),
		CIStorageClass: changedStringFlag(command, "storage-class"),
	}

	settings, updateError := apiClient.UpdateAgentCISettings(command.Context(), cluster.ID, update)
	if updateError != nil {
		return updateError
	}
	if settings == nil {
		return fmt.Errorf("the platform returned no agent CI settings for cluster %q", cluster.Name)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, settings)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Agent CI settings updated for cluster '%s'.\n\n", cluster.Name)
	printAgentCISettings(out, settings)
	return nil
}

func printAgentCISettings(out io.Writer, settings *client.AgentCISettings) {
	_, _ = fmt.Fprintf(out, "  Pipeline-step workers: %d\n", settings.CIWorkerCount)
	_, _ = fmt.Fprintf(out, "  Storage class:         %s\n", agentCIStorageClassLabel(settings.CIStorageClass))
	_, _ = fmt.Fprintf(out, "  Agent version:         %s\n", agentCIVersionLabel(settings.AgentVersion))
	_, _ = fmt.Fprintf(out, "  Pipeline steps:        %s\n", agentCICapabilityLabel(settings.SupportsPipelineSteps))
	if settings.UpdatedAt != nil && *settings.UpdatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Last changed:          %s\n", formatTimeAgo(*settings.UpdatedAt))
	} else {
		_, _ = fmt.Fprintln(out, "  Last changed:          never")
	}
	if sentence := agentCIApplyStateSentence(settings); sentence != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", sentence)
	}
}

// agentCIStorageClassLabel says which class step workspaces land on. An
// empty stored value is the cluster's default class, not a missing setting.
func agentCIStorageClassLabel(storageClass string) string {
	if storageClass == "" {
		return "cluster default"
	}
	return storageClass
}

func agentCIVersionLabel(agentVersion string) string {
	if agentVersion == "" {
		return "unknown (the agent has not checked in)"
	}
	return agentVersion
}

// agentCICapabilityLabel reports the capability the agent advertised, which
// is a fact about the running agent rather than about the stored settings.
func agentCICapabilityLabel(supportsPipelineSteps bool) string {
	if supportsPipelineSteps {
		return "advertised"
	}
	return "not advertised"
}

func init() {
	clusterAgentCmd.AddCommand(newClusterAgentCICommand())
}
