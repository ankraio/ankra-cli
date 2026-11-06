package cmd

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// removeNodeWaitUsage is remove-node's --wait. The platform answers once it
// has marked the node and started the operation that drains and deletes it,
// so that answer is all --wait can wait for.
const removeNodeWaitUsage = "Wait until the platform has checked the removal, marked the node and started the " +
	"operation that drains it and deletes its server (default: submit and return immediately). It does not wait " +
	"for the drain or the delete: follow the operation the command prints for those"

var clusterNodeGroupRemoveNodeCmd = &cobra.Command{
	Use:   "remove-node <cluster_id|name> <group_name> <node>",
	Short: "Remove one chosen node from a node group",
	Long: `Remove one named worker from its node group and delete its server. The
group's node count drops by one, so nothing recreates the node.

A scale-down picks the nodes it removes itself; remove-node is the way to
retire a particular one, such as a node on a failing host. <node> is the
node's name (its server name, which on most providers is also its Kubernetes
node name), the providerID its Kubernetes node reports, or its server id
(the ID column of 'ankra cluster <provider> nodes list', for example
'ankra cluster hetzner nodes list'). Before asking, the command looks the
reference up and shows the node's name, server id and providerID, or says
that it could not tell which node the reference names; the platform
resolves the reference itself either way. A node that is already cordoned
is removed the same way, but a refused drain makes the node it keeps
schedulable again: cordon it again if it must stay out of service.

` + guardedDrainHelp + `

The platform answers once it has marked the node for removal and started
the operation that drains it and deletes its server; the drain and the
delete run after that. The command prints the operation, and
'ankra cluster operations list <operation_id> --watch' follows it until it
ends. --wait waits for the platform's answer only, not for the drain or the
delete. When the drain is refused, the operation fails and names what
refused it, the node is kept and made schedulable again, and the group's
count goes back up.

When the platform starts no operation, one of two things is true. The
cluster is stopped: the node is dropped from the cluster's record now,
nothing is drained, and it is not created again when the cluster starts. Or
another node change on the cluster is still running: the platform drains
and deletes this node once that change has finished.

The platform refuses a control plane, a node of another group, a node
already being removed, and a removal that would take an autoscaling group
below its min count; each refusal exits 1 with the platform's reason. A
node the cluster does not have exits 3. A blank group or node, one made
only of dots, a '/' outside a providerID, or a '.' or '..' step inside one
exits 2 before anything is looked up or sent.

Examples:
  ankra cluster node-group remove-node <cluster_id> opensearch prod-opensearch-0
  ankra cluster node-group remove-node <cluster_id> opensearch prod-opensearch-0 --wait -o json
  ankra cluster node-group remove-node <cluster_id> workers hcloud://168428300 --force-drain --yes`,
	Args: cobra.MatchAll(nodeGroupArgs(3, 1), func(_ *cobra.Command, args []string) error {
		return validateNodeReference(args[2])
	}),
	RunE: runRemoveNodeGroupNode,
}

func runRemoveNodeGroupNode(cmd *cobra.Command, args []string) error {
	clusterID, resolveError := resolveClusterArg(args[0])
	if resolveError != nil {
		return resolveError
	}
	groupName, nodeReference := args[1], strings.TrimSpace(args[2])
	yes, _ := cmd.Flags().GetBool("yes")
	drainOptions := drainOptionsFromFlags(cmd)
	kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
	if kindError != nil {
		return kindError
	}

	if !yes {
		consequence := "This drains the node and deletes its server!"
		if drainOptions.ForceDrain {
			consequence = "This deletes the node's server without honouring its PodDisruptionBudgets!"
		}
		target := resolveRemoveNodeTarget(kind, clusterID, nodeReference)
		prompt := removeNodePrompt(target, nodeReference, groupName, clusterTarget(args[0], clusterID), consequence)
		if err := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt, false); err != nil {
			return err
		}
	}

	requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
	if err != nil {
		return err
	}
	defer cancelRequestContext()

	result, accepted, removeError := apiClient.RemoveNodeGroupNode(requestContext, kind, clusterID, groupName,
		nodeReference, drainOptions, wait)
	if removeError != nil {
		return removeNodeGroupNodeError(wait, removeError)
	}
	if accepted != nil {
		submitted := newNodeRemovalSubmitted(clusterID, result, accepted)
		if handled, err := renderStructured(cmd, submitted); err != nil {
			return err
		} else if handled {
			return nil
		}
		printNodeRemovalAccepted(cmd.OutOrStdout(), clusterID, result, accepted, drainOptions)
		return nil
	}
	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}
	printNodeRemoval(cmd.OutOrStdout(), clusterID, result, drainOptions)
	return nil
}

// providerIDShape is the start of a Kubernetes providerID ("hcloud://...",
// "aws:///eu-west-1a/i-..."): the one form of node reference with a '/'.
var providerIDShape = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// validateNodeReference refuses a node reference no node can have, before
// anything is looked up or sent: blank, made only of dots, holding a '/' when
// it is not a providerID, or a providerID with a "." or ".." step. The
// reference is path-escaped into one segment, but a reference of dots is a
// path step the platform's edge resolves, so `remove-node <c> <group> ..`
// would reach the group's own DELETE (ankra-6pi023). The edge leaves the
// escaped '/' of a providerID alone today; no provider's id has a dot step,
// so one is refused here rather than resting on that. Space around the
// reference is not part of it: this checks what is sent, trimmed.
func validateNodeReference(reference string) error {
	trimmed := strings.TrimSpace(reference)
	decoded := client.PercentDecoded(trimmed)
	if scheme := providerIDShape.FindString(decoded); scheme != "" {
		for _, step := range strings.Split(strings.TrimPrefix(decoded, scheme), "/") {
			if step == "." || step == ".." {
				return withExitCode(exitUsage, fmt.Errorf(
					"the node %q has a '%s' step, which no providerID has; name the node, its providerID or "+
						"its server id", reference, step))
			}
		}
		return nil
	}
	if strings.Contains(decoded, "/") {
		return withExitCode(exitUsage, fmt.Errorf(
			"the node %q contains '/' but is not a providerID (scheme://...); name the node, its providerID "+
				"or its server id", reference))
	}
	return validatePathArgument("node", trimmed)
}

// removeNodeGroupNodeError wraps a remove-node failure for the terminal. A
// platform that predates the route answers the router's bare 404, which is
// not "the node does not exist": it is said plainly and exits 1, rather than
// the not-found code a script would read as the node being gone already. The
// platform's own 404 for a node the cluster does not have carries its reason
// and exits 3.
func removeNodeGroupNodeError(wait bool, removeError error) error {
	if isRouteAbsent(removeError) {
		return withExitCode(exitError, errors.New(
			"removing node from node group: this Ankra platform does not serve node removal yet (the route answered 404); "+
				"check the selected organisation with 'ankra org current' and try again once the platform has it"))
	}
	return asyncWriteError("removing node from node group", wait, removeError)
}

// nodeRemovalSubmitted is remove-node's -o json|yaml answer when the platform
// accepted the removal without waiting: the shared submitted shape, plus the
// node that leaves and the group's counts when the platform named them.
type nodeRemovalSubmitted struct {
	asyncSubmittedResult `yaml:",inline"`
	GroupName            string `json:"group_name,omitempty" yaml:"group_name,omitempty"`
	NodeName             string `json:"node_name,omitempty" yaml:"node_name,omitempty"`
	NodeID               string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	PreviousCount        *int   `json:"previous_count,omitempty" yaml:"previous_count,omitempty"`
	NewCount             *int   `json:"new_count,omitempty" yaml:"new_count,omitempty"`
}

func newNodeRemovalSubmitted(clusterID string, result *client.RemoveNodeGroupNodeResult,
	accepted *client.AsyncWriteAcceptedResponse) nodeRemovalSubmitted {
	submitted := nodeRemovalSubmitted{asyncSubmittedResult: asyncSubmittedResult{
		Submitted: true,
		Operation: "Node removal",
	}}
	submitted.OperationID = accepted.OperationID
	if result != nil {
		submitted.GroupName, submitted.NodeName, submitted.NodeID = result.GroupName, result.NodeName, result.NodeID
		previousCount, newCount := result.PreviousCount, result.NewCount
		submitted.PreviousCount, submitted.NewCount = &previousCount, &newCount
		if submitted.OperationID == "" && result.OperationID != nil {
			submitted.OperationID = *result.OperationID
		}
	}
	switch {
	case submitted.OperationID != "":
		submitted.Hint = "follow the drain and the delete with '" + watchOperationCommand(submitted.OperationID) +
			"'; a refused drain fails the operation, names what refused it and keeps the node"
	case result != nil:
		submitted.Hint = "no operation was started yet: either the cluster is stopped and the node was dropped from " +
			"its record (nothing is drained, and it is not created again on start), or another node change is " +
			"still running and the platform drains and deletes this node after it; '" +
			clusterOperationsCommand(clusterID) + "' lists the removal's operation once it starts"
	default:
		submitted.Hint = "the platform was still checking the removal when it answered, so it has not named the " +
			"node yet; '" + clusterOperationsCommand(clusterID) + "' lists the removal's operation once it starts, " +
			"and '" + failedNodeGroupWriteCommand(clusterID) + "' shows a refusal as a failed node_group_write " +
			"operation once it is decided"
	}
	return submitted
}

// watchOperationCommand follows one operation until it ends.
func watchOperationCommand(operationID string) string {
	return followOperationCommand(operationID) + " --watch"
}

// clusterOperationsCommand lists a cluster's operations from any selection.
func clusterOperationsCommand(clusterID string) string {
	return "ankra cluster operations list --cluster " + clusterID
}

// printNodeRemoval tells what a removal the platform decided started: which
// node leaves, the counts, and how to follow the drain and the delete.
func printNodeRemoval(out io.Writer, clusterID string, result *client.RemoveNodeGroupNodeResult,
	drainOptions client.DrainOptions) {
	_, _ = fmt.Fprintf(out, "Node '%s' (server id %s) is leaving node group '%s' (%d -> %d nodes).\n",
		result.NodeName, result.NodeID, result.GroupName, result.PreviousCount, result.NewCount)
	if result.OperationID == nil {
		_, _ = fmt.Fprintln(out, "No operation was started for it yet. Either:")
		_, _ = fmt.Fprintln(out, "  - the cluster is stopped: the node was dropped from the cluster's record now, nothing is "+
			"drained, and it is not created again when the cluster starts; or")
		_, _ = fmt.Fprintf(out, "  - another node change on the cluster is still running: the platform drains and deletes this "+
			"node once it has finished. Find the removal's operation then with:\n      %s\n",
			clusterOperationsCommand(clusterID))
		return
	}
	drain := "drains it through its pods' PodDisruptionBudgets"
	if drainOptions.ForceDrain {
		drain = "drains it without honouring its pods' PodDisruptionBudgets"
	}
	_, _ = fmt.Fprintf(out, "The node is marked for removal. Operation %s %s, then deletes its server.\n",
		*result.OperationID, drain)
	_, _ = fmt.Fprintf(out, "Follow the drain and the delete with:\n  %s\n", watchOperationCommand(*result.OperationID))
	if !drainOptions.ForceDrain {
		_, _ = fmt.Fprintf(out, "If the drain is refused, the operation fails and names what refused it, the node is kept "+
			"and made schedulable again, and the group goes back to %d nodes.\n", result.PreviousCount)
	}
}

// printNodeRemovalAccepted is printNodeRemoval for a removal the platform
// accepted without waiting. When it decided the removal before answering it
// named the node, and the output is the same; otherwise it says what is
// known and where the outcome will be recorded.
func printNodeRemovalAccepted(out io.Writer, clusterID string, result *client.RemoveNodeGroupNodeResult,
	accepted *client.AsyncWriteAcceptedResponse, drainOptions client.DrainOptions) {
	if result != nil {
		if result.OperationID == nil && accepted.OperationID != "" {
			operationID := accepted.OperationID
			result.OperationID = &operationID
		}
		printNodeRemoval(out, clusterID, result, drainOptions)
		return
	}
	_, _ = fmt.Fprintln(out, "Node removal submitted. The platform was still checking it when it answered, so it has not "+
		"named the node that leaves yet.")
	if accepted.OperationID != "" {
		_, _ = fmt.Fprintf(out, "Follow it with:\n  %s\n", watchOperationCommand(accepted.OperationID))
		return
	}
	_, _ = fmt.Fprintf(out, "The removal's operation is listed here once it starts:\n  %s\n", clusterOperationsCommand(clusterID))
	_, _ = fmt.Fprintf(out, "If the platform refuses the removal, the refusal is recorded as a failed node_group_write "+
		"operation with its reason:\n  %s\n", failedNodeGroupWriteCommand(clusterID))
	_, _ = fmt.Fprintln(out, "The record appears once the refusal is decided, so an empty listing right away does not mean "+
		"the node was removed. Do not run the removal again until one of the two shows up.")
}

// removeNodeTarget is what the cluster's records say about the node a
// reference names, read before the prompt so the user approves a node
// rather than a string. node is nil when the lookup could not tell which
// node it is (unresolved says why); the platform resolves the reference
// itself either way, so an inconclusive lookup never refuses the removal.
type removeNodeTarget struct {
	node *client.NodeSummary
	// providerID is the providerID the node's Kubernetes node reports, and
	// providerIDUnknown why it is not known when it is empty.
	providerID        string
	providerIDUnknown string
	unresolved        string
}

// removeNodeKubernetesReadTimeout bounds the providerID read before the
// prompt: it goes through the cluster's agent, and a slow agent must not
// hold the question back for long.
const removeNodeKubernetesReadTimeout = 10 * time.Second

// nodesOpsByKind is each node-group kind's node listing.
var nodesOpsByKind = map[string]func() clusterNodesOps{
	"hetzner":      hetznerNodesOps,
	"ovh":          ovhNodesOps,
	"upcloud":      upcloudNodesOps,
	"digitalocean": digitaloceanNodesOps,
	"scaleway":     scalewayNodesOps,
	"ankracloud":   ankraCloudNodesOps,
	"aws":          awsNodesOps,
	"proxmox":      proxmoxNodesOps,
	"morpheus":     morpheusNodesOps,
}

// resolveRemoveNodeTarget looks reference up in the cluster's node list and,
// for its providerID, in the cluster's Kubernetes nodes.
func resolveRemoveNodeTarget(kind, clusterID, reference string) removeNodeTarget {
	opsFor, known := nodesOpsByKind[kind]
	if !known {
		return removeNodeTarget{unresolved: "the CLI cannot list the nodes of a " + kind + " cluster"}
	}
	listed, listError := opsFor().list(clusterID)
	if listError != nil || listed == nil {
		return removeNodeTarget{unresolved: fmt.Sprintf("the cluster's node list could not be read (%v)", listError)}
	}
	return matchRemoveNodeReference(listed.Nodes, readKubernetesNodeProviderIDs(clusterID), reference)
}

// readKubernetesNodeProviderIDs maps the cluster's Kubernetes nodes, by
// lowercased name, to the providerID each reports ("" for none). nil when
// they could not be read within removeNodeKubernetesReadTimeout.
func readKubernetesNodeProviderIDs(clusterID string) map[string]string {
	reader := apiClient
	answers := make(chan map[string]string, 1)
	go func() {
		response, readError := reader.GetResources(clusterID, client.GetResourcesRequest{
			ResourceRequests: []client.ResourceRequestItem{{Kind: "Node", Version: "v1"}},
		})
		if readError != nil || response == nil || len(response.ResourceResponses) != 1 ||
			response.ResourceResponses[0].Status == "error" {
			answers <- nil
			return
		}
		providerIDs := map[string]string{}
		for _, item := range response.ResourceResponses[0].Items {
			object, isObject := item.(map[string]interface{})
			if !isObject {
				continue
			}
			if name := getNestedString(object, "metadata", "name"); name != "" {
				providerIDs[strings.ToLower(name)] = getNestedString(object, "spec", "providerID")
			}
		}
		answers <- providerIDs
	}()
	select {
	case providerIDs := <-answers:
		return providerIDs
	case <-time.After(removeNodeKubernetesReadTimeout):
		return nil
	}
}

// matchRemoveNodeReference finds the node reference names the way the
// platform does: by server id, then by name case-insensitively, a providerID
// first mapped to the Kubernetes node reporting it. A name or providerID
// that several nodes answer to names none of them.
func matchRemoveNodeReference(nodes []client.NodeSummary, providerIDs map[string]string,
	reference string) removeNodeTarget {
	trimmed := strings.TrimSpace(reference)
	var live []client.NodeSummary
	for _, node := range nodes {
		if !node.IsDeleted {
			live = append(live, node)
		}
	}
	for index := range live {
		if strings.EqualFold(live[index].ID, trimmed) {
			return removeNodeTargetFor(&live[index], providerIDs)
		}
	}

	name := trimmed
	var reporting []string
	for nodeName, providerID := range providerIDs {
		if providerID != "" && strings.EqualFold(providerID, trimmed) {
			reporting = append(reporting, nodeName)
		}
	}
	switch {
	case len(reporting) == 1:
		name = reporting[0]
	case len(reporting) > 1:
		return removeNodeTarget{unresolved: fmt.Sprintf("%d Kubernetes nodes report that providerID", len(reporting))}
	}

	var named []*client.NodeSummary
	for index := range live {
		if strings.EqualFold(live[index].Name, name) {
			named = append(named, &live[index])
		}
	}
	switch {
	case len(named) == 1:
		return removeNodeTargetFor(named[0], providerIDs)
	case len(named) > 1:
		return removeNodeTarget{unresolved: fmt.Sprintf("%d nodes in the cluster's records are named %q", len(named), name)}
	case name != trimmed:
		return removeNodeTarget{unresolved: fmt.Sprintf(
			"the Kubernetes node reporting that providerID, %q, matches no node in the cluster's records", name)}
	default:
		return removeNodeTarget{unresolved: "no node in the cluster's records has that name or server id"}
	}
}

func removeNodeTargetFor(node *client.NodeSummary, providerIDs map[string]string) removeNodeTarget {
	target := removeNodeTarget{node: node}
	providerID, known := providerIDs[strings.ToLower(node.Name)]
	switch {
	case providerIDs == nil:
		target.providerIDUnknown = "not known: the cluster's Kubernetes nodes could not be read"
	case !known:
		target.providerIDUnknown = "not known: no Kubernetes node is named " + node.Name
	case providerID == "":
		target.providerIDUnknown = "none: its Kubernetes node reports no providerID"
	default:
		target.providerID = providerID
	}
	return target
}

// removeNodePrompt is remove-node's question: the node the reference
// resolved to, or that it could not be resolved and why.
func removeNodePrompt(target removeNodeTarget, reference, groupName, cluster, consequence string) string {
	var prompt strings.Builder
	if target.node == nil {
		_, _ = fmt.Fprintf(&prompt, "Remove the node %q names from node group %q of cluster %s?\n",
			reference, groupName, cluster)
		_, _ = fmt.Fprintf(&prompt, "  The CLI could not tell which node that is: %s. The platform resolves the "+
			"reference itself, and refuses it if it names no node of the group.\n", target.unresolved)
	} else {
		node := target.node
		_, _ = fmt.Fprintf(&prompt, "Remove node %q from node group %q of cluster %s?\n", node.Name, groupName, cluster)
		_, _ = fmt.Fprintf(&prompt, "  Server id:   %s\n", node.ID)
		providerID := target.providerID
		if providerID == "" {
			providerID = target.providerIDUnknown
		}
		_, _ = fmt.Fprintf(&prompt, "  Provider id: %s\n", providerID)
		if node.NodeGroup != nil && *node.NodeGroup != groupName {
			_, _ = fmt.Fprintf(&prompt, "  Node group:  %s, not %s: the platform refuses a node of another group\n",
				*node.NodeGroup, groupName)
		}
		if node.Role != nil && *node.Role != "" && *node.Role != "worker" {
			_, _ = fmt.Fprintf(&prompt, "  Role:        %s: the platform removes workers only\n", *node.Role)
		}
	}
	_, _ = fmt.Fprintf(&prompt, "%s [y/N]: ", consequence)
	return prompt.String()
}
