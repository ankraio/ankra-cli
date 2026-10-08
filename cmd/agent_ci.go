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
	"sort"
	"strings"

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
	// agentCIAppliedLiveSentence is the applied state for a write the agent
	// takes live: the platform stored the count and did not re-render the
	// release, so saying "re-rendering" would tell an operator to expect an
	// agent restart - and cancelled long steps - that never happens.
	agentCIAppliedLiveSentence = "Applied live, no agent restart: the agent runs %d pipeline-step " +
		"workers from its next job pull."
)

// agentCIApplyStateSentence renders one apply state. An apply state this
// build does not know is a newer platform's, not a broken response, so it is
// reported as itself rather than swallowed. An applied write the platform
// marks applies_live reached the agent without a re-render; a platform that
// does not say keeps the re-render sentence, which is what it did.
func agentCIApplyStateSentence(settings *client.AgentCISettings) string {
	switch settings.ApplyState {
	case client.AgentCIApplyStateApplied:
		if settings.AppliesLive != nil && *settings.AppliesLive {
			return fmt.Sprintf(agentCIAppliedLiveSentence, settings.CIWorkerCount)
		}
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

The worker count is the agent's CI slots: each slot is one pipeline step the
agent watches at a time, not a node. The nodes those steps run on come from
the cluster's node group autoscaler ('ankra cluster node-group autoscaling
get'), so a cluster whose steps wait for a slot needs more workers, and one
whose step pods sit Pending needs more nodes.

Both are stored on the platform, so every install or upgrade command Ankra
generates for this cluster carries them - unlike a hand-run
'helm upgrade --set ci_worker_count=...', which the next agent upgrade
renders away again.

The placement says which nodes every pipeline pod runs on - step pods, their
cache and artifact helpers, and image builds - so CI can live on a dedicated,
tainted node group without editing any pipeline's runs_on.`,
	}
	ciCommand.AddCommand(newClusterAgentCIGetCommand(), newClusterAgentCISetCommand())
	return ciCommand
}

func newClusterAgentCIGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the cluster agent's pipeline-step CI settings",
		Long: `Show the stored worker count, storage class and placement, the agent version
that has to honour them, whether that agent currently advertises it can run
pipeline steps, and how the last write was applied. The placement block names
the node group, node selector, tolerations and mode, and how many Ready nodes
it admits right now.

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
		Long: `Store how many pipeline steps this cluster's agent runs at once, the storage
class its step workspaces are carved from, and where its pipeline pods run.

--workers 0 disables the agent's pipeline-step scheduler; the cluster keeps
its agent and everything else it does. --storage-class is only sent when you
pass it, so setting the worker count alone keeps the storage class already
stored; pass an empty value to fall back to the cluster's default class.

--workers is the agent's CI slots: how many pipeline steps it watches at
once. A slot is not a node; the nodes steps run on come from the cluster's
node group autoscaler. Agents that support live resize accept up to 128
workers, older agents up to 32.

The write always stores the values. Whether they reach the agent now depends
on the agent: an agent that supports live resize takes a new worker count on
its next job pull without restarting, an online agent new enough to accept
chart values re-renders its release immediately, an older one picks them up
at its next upgrade, and an offline one when it reconnects. The command says
which happened.

Placement puts every pipeline pod of the cluster - step pods, their cache and
artifact helpers, and image builds - on chosen nodes. --node-group names one
of the cluster's Ankra node groups: the platform selects its nodes by the
ankra.cloud/node-group label (or the labels the group declares) and tolerates
every taint the group carries, so a dedicated, tainted pool needs nothing
else. --node-selector and --toleration add to it, or stand alone on a cluster
without Ankra node groups. --placement required (the default) keeps pods on
those nodes; preferred favours them and lets pods run elsewhere when they have
no room, and still carries the tolerations. The placement is replaced whole by
any placement flag, applies to the next step dispatched with no agent restart,
and is never held to the organisation's runs_on allow-lists. --clear-placement
removes it. --workers is optional when only the storage class or the placement
changes.`,
		Example: `  ankra cluster agent ci set --workers 2
  ankra cluster agent ci set --workers 4 --storage-class proxmox-csi
  ankra cluster agent ci set --workers 0 --cluster edge-01
  ankra cluster agent ci set --node-group pipelines
  ankra cluster agent ci set --node-selector pool=ci --toleration dedicated=ci:NoSchedule --placement preferred
  ankra cluster agent ci set --clear-placement`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentCISet(command)
		},
	}
	setCommand.Flags().Int("workers", 0,
		"CI slots: pipeline steps the agent watches at once, not nodes (nodes come from the node group "+
			"autoscaler); up to 128 on agents that support live resize, 32 otherwise; 0 disables the scheduler")
	setCommand.Flags().String("storage-class", "",
		"Storage class for pipeline step workspaces; pass empty to use the cluster default (omit the flag to keep the stored value)")
	setCommand.Flags().String("node-group", "",
		"Run every pipeline pod on this Ankra node group: selects its nodes and tolerates its taints")
	setCommand.Flags().StringArray("node-selector", nil,
		"Node label every pipeline pod selects, as key=value (repeatable)")
	setCommand.Flags().StringArray("toleration", nil,
		"Taint every pipeline pod tolerates, as key[=value]:Effect with Effect NoSchedule, PreferNoSchedule "+
			"or NoExecute (repeatable)")
	setCommand.Flags().String("placement", "",
		"Placement mode: required (default) keeps pipeline pods on the selected nodes, preferred favours them")
	setCommand.Flags().Bool("clear-placement", false, "Remove the cluster's CI placement")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

// agentCIPlacementFlags are the flags that write the placement block.
var agentCIPlacementFlags = []string{"node-group", "node-selector", "toleration", "placement"}

// agentCIPlacementFromFlags builds the placement a set names, nil when it
// names no placement flag. It refuses malformed values as usage errors before
// anything is sent; what the values mean - a node group the cluster has, a
// label key Kubernetes accepts - is the platform's to judge.
func agentCIPlacementFromFlags(command *cobra.Command) (*client.AgentCIPlacement, bool, error) {
	isClearing := mustFlagBool(command, "clear-placement")
	isNamed := false
	for _, flagName := range agentCIPlacementFlags {
		if command.Flags().Changed(flagName) {
			isNamed = true
		}
	}
	if isClearing && isNamed {
		return nil, false, withExitCode(exitUsage, fmt.Errorf(
			"--clear-placement removes the placement; it cannot be combined with --%s",
			strings.Join(agentCIPlacementFlags, ", --")))
	}
	if !isNamed {
		return nil, isClearing, nil
	}
	placement := &client.AgentCIPlacement{
		NodeGroup: strings.TrimSpace(mustFlagString(command, "node-group")),
		Mode:      strings.TrimSpace(mustFlagString(command, "placement")),
	}
	switch placement.Mode {
	case "", client.AgentCIPlacementModeRequired, client.AgentCIPlacementModePreferred:
	default:
		return nil, false, withExitCode(exitUsage, fmt.Errorf(
			"--placement must be %q or %q, got %q", client.AgentCIPlacementModeRequired,
			client.AgentCIPlacementModePreferred, placement.Mode))
	}
	selectorValues, _ := command.Flags().GetStringArray("node-selector")
	for _, selectorValue := range selectorValues {
		key, value, hasValue := strings.Cut(selectorValue, "=")
		key = strings.TrimSpace(key)
		if !hasValue || key == "" {
			return nil, false, withExitCode(exitUsage, fmt.Errorf(
				"--node-selector takes key=value, got %q", selectorValue))
		}
		if placement.NodeSelector == nil {
			placement.NodeSelector = map[string]string{}
		}
		placement.NodeSelector[key] = strings.TrimSpace(value)
	}
	tolerationValues, _ := command.Flags().GetStringArray("toleration")
	for _, tolerationValue := range tolerationValues {
		toleration, parseError := parseAgentCIToleration(tolerationValue)
		if parseError != nil {
			return nil, false, withExitCode(exitUsage, parseError)
		}
		placement.Tolerations = append(placement.Tolerations, toleration)
	}
	return placement, false, nil
}

// parseAgentCIToleration reads key[=value]:Effect. A toleration naming a
// value matches that value (Equal); one without matches any value of the key
// (Exists).
func parseAgentCIToleration(text string) (client.AgentCIPlacementToleration, error) {
	separator := strings.LastIndex(text, ":")
	if separator <= 0 || separator == len(text)-1 {
		return client.AgentCIPlacementToleration{}, fmt.Errorf(
			"--toleration takes key[=value]:Effect, for example dedicated=ci:NoSchedule, got %q", text)
	}
	keyAndValue, effect := text[:separator], strings.TrimSpace(text[separator+1:])
	key, value, hasValue := strings.Cut(keyAndValue, "=")
	key = strings.TrimSpace(key)
	if key == "" {
		return client.AgentCIPlacementToleration{}, fmt.Errorf(
			"--toleration names no taint key, got %q", text)
	}
	toleration := client.AgentCIPlacementToleration{Key: key, Operator: "Exists", Effect: effect}
	if hasValue {
		toleration.Operator = "Equal"
		toleration.Value = strings.TrimSpace(value)
	}
	return toleration, nil
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

	// Each setting is only sent when its flag is named, which is what keeps a
	// worker-count change from resetting a storage class or a placement
	// someone set earlier.
	placement, isClearingPlacement, placementError := agentCIPlacementFromFlags(command)
	if placementError != nil {
		return placementError
	}
	update := client.AgentCISettingsUpdate{
		CIWorkerCount:  changedIntFlag(command, "workers"),
		CIStorageClass: changedStringFlag(command, "storage-class"),
		CIPlacement:    placement,
		ClearPlacement: isClearingPlacement,
	}
	if update.CIWorkerCount == nil && update.CIStorageClass == nil && placement == nil && !isClearingPlacement {
		return withExitCode(exitUsage, fmt.Errorf(`required flag(s) "workers" not set: pass --workers, `+
			`--storage-class, or a placement flag (--node-group, --node-selector, --toleration, --placement, `+
			`--clear-placement)`))
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
	printAgentCIPlacement(out, settings)
	if sentence := agentCIApplyStateSentence(settings); sentence != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", sentence)
	}
}

// printAgentCIPlacement renders the placement block. A cluster with none
// says so, because "any node" is an answer an operator is looking for.
func printAgentCIPlacement(out io.Writer, settings *client.AgentCISettings) {
	placement := settings.CIPlacement
	if placement == nil {
		_, _ = fmt.Fprintln(out, "  Placement:             none (pipeline pods run on any node)")
		return
	}
	_, _ = fmt.Fprintln(out, "\n  Placement:")
	nodeGroup := placement.NodeGroup
	if nodeGroup == "" {
		nodeGroup = "none"
	}
	mode := placement.Mode
	if mode == "" {
		mode = client.AgentCIPlacementModeRequired
	}
	_, _ = fmt.Fprintf(out, "    Node group:          %s\n", nodeGroup)
	_, _ = fmt.Fprintf(out, "    Node selector:       %s\n", agentCISelectorLabel(placement.NodeSelector))
	_, _ = fmt.Fprintf(out, "    Tolerations:         %s\n", agentCITolerationsLabel(placement.Tolerations))
	_, _ = fmt.Fprintf(out, "    Mode:                %s\n", mode)
	if settings.PlacementAdmittingNodes != nil {
		_, _ = fmt.Fprintf(out, "    Admitting nodes:     %d Ready\n", *settings.PlacementAdmittingNodes)
	}
}

func agentCISelectorLabel(selector map[string]string) string {
	if len(selector) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+selector[key])
	}
	return strings.Join(pairs, ", ")
}

func agentCITolerationsLabel(tolerations []client.AgentCIPlacementToleration) string {
	if len(tolerations) == 0 {
		return "none"
	}
	rendered := make([]string, 0, len(tolerations))
	for _, toleration := range tolerations {
		taint := toleration.Key
		if toleration.Operator != "Exists" && toleration.Value != "" {
			taint += "=" + toleration.Value
		}
		rendered = append(rendered, taint+":"+toleration.Effect)
	}
	return strings.Join(rendered, ", ")
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
