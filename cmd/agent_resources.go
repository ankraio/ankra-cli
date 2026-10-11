package cmd

// `ankra cluster agent resources get|set` (ankra-jr4e7h): the CLI surface
// over GET/PUT /api/v1/org/clusters/{cluster_id}/agent/resources, which size
// the cluster agent's own pod - its memory and CPU limits and requests.
//
// Before these routes existed an agent that needed more memory could only be
// given it with a hand-run `helm upgrade --set resources.limits.memory=...`,
// and the next platform-driven agent install or upgrade rendered the chart
// default back over it, so the agent went back to being OOM-killed. The
// sizing now lives on the platform, every generated Helm command carries it,
// and this pair of verbs is how it is read and written.
//
// The cluster is resolved exactly like the sibling `cluster agent
// status|upgrade|ci` verbs, through the shared --cluster override.

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// agentResourcesFlags maps each `set` flag to the member it writes, in the
// order help text and refusals name them.
var agentResourcesFlags = []string{"memory-limit", "memory-request", "cpu-limit", "cpu-request"}

func newClusterAgentResourcesCommand() *cobra.Command {
	resourcesCommand := &cobra.Command{
		Use:   "resources",
		Short: "Read and set the cluster agent's memory and CPU sizing",
		Long: `The cluster agent runs as one pod in the cluster, and these settings size it:
its memory limit and request and its CPU limit and request, as Kubernetes
quantities ("2Gi", "500m").

They are stored on the platform, so every install or upgrade command Ankra
generates for this cluster carries them - unlike a hand-run
'helm upgrade --set resources.limits.memory=...', which the next agent
upgrade renders away again. A member the cluster does not set uses the agent
chart's default.`,
	}
	resourcesCommand.AddCommand(newClusterAgentResourcesGetCommand(), newClusterAgentResourcesSetCommand())
	return resourcesCommand
}

func newClusterAgentResourcesGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the cluster agent's stored memory and CPU sizing",
		Long: `Show the stored memory and CPU limits and requests of the cluster's agent, when
they last changed, and how the platform applied them. A member the cluster
does not set reads "chart default": the agent chart's own value applies.`,
		Example: `  ankra cluster agent resources get
  ankra cluster agent resources get --cluster edge-01 -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentResourcesGet(command)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runClusterAgentResourcesGet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	resources, getError := apiClient.GetAgentResources(command.Context(), cluster.ID)
	if getError != nil {
		// Returned as it came: the platform owns this surface's refusals
		// (an RBAC 403 naming agents.manage, a 404 for a cluster outside the
		// organisation), and rewording them here would only put a second,
		// staler vocabulary in front of the user.
		return getError
	}
	if resources == nil {
		return fmt.Errorf("the platform returned no agent resources for cluster %q", cluster.Name)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, resources)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Agent resources for cluster '%s':\n\n", cluster.Name)
	printAgentResources(out, resources)
	return nil
}

func newClusterAgentResourcesSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Set the cluster agent's memory and CPU sizing",
		Long: `Store the memory and CPU limits and requests of this cluster's agent, as
Kubernetes quantities ("2Gi", "512Mi", "1", "500m").

Only the flags you pass are sent, so setting the memory limit alone keeps
every other member as stored. Pass an empty value ("") to clear a member: the
agent goes back to the chart's default for it. At least one flag is required.

The write always stores the values. Whether they reach the agent now depends
on the agent: an online agent new enough to accept them re-renders its
release immediately, an older one picks them up at its next upgrade, and an
offline one when it reconnects. The command prints the platform's sentence
saying which happened.

The platform validates the values - a malformed quantity, or a request above
its limit - and its refusal is printed as it came.`,
		Example: `  ankra cluster agent resources set --memory-limit 2Gi
  ankra cluster agent resources set --memory-limit 2Gi --memory-request 1Gi --cluster edge-01
  ankra cluster agent resources set --cpu-limit 1 --cpu-request 250m
  ankra cluster agent resources set --memory-limit ""`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentResourcesSet(command)
		},
	}
	setCommand.Flags().String("memory-limit", "",
		"Agent memory limit, a Kubernetes quantity such as 2Gi; pass \"\" to clear it back to the chart default")
	setCommand.Flags().String("memory-request", "",
		"Agent memory request, such as 1Gi; pass \"\" to clear it back to the chart default")
	setCommand.Flags().String("cpu-limit", "",
		"Agent CPU limit, such as 1 or 1500m; pass \"\" to clear it back to the chart default")
	setCommand.Flags().String("cpu-request", "",
		"Agent CPU request, such as 250m; pass \"\" to clear it back to the chart default")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func runClusterAgentResourcesSet(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}

	// Each member is only sent when its flag is named - Changed, not a
	// non-empty value - which is what lets --memory-limit "" clear one member
	// and keeps a memory change from resetting a CPU value someone set
	// earlier.
	update := client.AgentResourcesUpdate{
		MemoryLimit:   changedStringFlag(command, "memory-limit"),
		MemoryRequest: changedStringFlag(command, "memory-request"),
		CPULimit:      changedStringFlag(command, "cpu-limit"),
		CPURequest:    changedStringFlag(command, "cpu-request"),
	}
	// Refused before the cluster is resolved, so a bare `set` costs no
	// round trip: the platform would answer it with a 422 anyway.
	if !update.NamesAny() {
		return withExitCode(exitUsage, fmt.Errorf("nothing to set: pass at least one of --%s",
			strings.Join(agentResourcesFlags, ", --")))
	}

	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	resources, updateError := apiClient.UpdateAgentResources(command.Context(), cluster.ID, update)
	if updateError != nil {
		return updateError
	}
	if resources == nil {
		return fmt.Errorf("the platform returned no agent resources for cluster %q", cluster.Name)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, resources)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Agent resources updated for cluster '%s'.\n\n", cluster.Name)
	printAgentResources(out, resources)
	return nil
}

// printAgentResources renders the sizing block, then the platform's sentence
// for the apply state on its own line, as it came.
func printAgentResources(out io.Writer, resources *client.AgentResources) {
	_, _ = fmt.Fprintf(out, "  Memory limit:    %s\n", agentResourcesQuantityLabel(resources.MemoryLimit))
	_, _ = fmt.Fprintf(out, "  Memory request:  %s\n", agentResourcesQuantityLabel(resources.MemoryRequest))
	_, _ = fmt.Fprintf(out, "  CPU limit:       %s\n", agentResourcesQuantityLabel(resources.CPULimit))
	_, _ = fmt.Fprintf(out, "  CPU request:     %s\n", agentResourcesQuantityLabel(resources.CPURequest))
	if resources.UpdatedAt != nil && *resources.UpdatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Last changed:    %s\n", formatTimeAgo(*resources.UpdatedAt))
	} else {
		_, _ = fmt.Fprintln(out, "  Last changed:    never")
	}
	applyState := resources.ApplyState
	if applyState == "" {
		applyState = "unknown"
	}
	_, _ = fmt.Fprintf(out, "  Apply state:     %s\n", applyState)
	if resources.Message != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", resources.Message)
	}
}

// agentResourcesQuantityLabel says what a member is. A member the cluster does
// not store is the agent chart's default, not a missing value.
func agentResourcesQuantityLabel(quantity *string) string {
	if quantity == nil || *quantity == "" {
		return "chart default"
	}
	return *quantity
}

func init() {
	clusterAgentCmd.AddCommand(newClusterAgentResourcesCommand())
}
