package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/google/uuid"
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
node's server id (the ID column of 'ankra cluster <provider> nodes list',
for example 'ankra cluster hetzner nodes list'), its server name, its
Kubernetes node name (the server name, on every provider but AWS) or the
providerID its Kubernetes node reports, in any letter case.

Before asking, the command looks the reference up and shows the node's
name, server id and providerID, or says that it could not tell which node
the reference names. When it found the node, it sends that node's server
id, so the node removed is the node you approved; otherwise it sends the
reference as typed, and the platform resolves it itself. With --yes the
node is not looked up and the reference is sent as typed. A node that is
already cordoned is removed the same way, but a refused drain makes the
node it keeps schedulable again: cordon it again if it must stay out of
service.

` + guardedDrainHelp + `

The platform answers once it has marked the node for removal and started
the operation that drains it and deletes its server; the drain and the
delete run after that. The command prints the operation, and
'ankra cluster operations list <operation_id> --watch' follows it until it
ends. --wait waits for the platform's answer only, not for the drain or the
delete. When the drain is refused, the operation fails and names what
refused it, the node is kept and made schedulable again, and the group's
count goes back up.

When the platform names no operation, the removal is still recorded: the
platform could not start the operation with the request, for example
because another node operation is still running or the cluster's agent is
offline, and its reconcile drains and deletes the node later.

A stopped cluster's nodes are not removed: the command refuses the removal
before it looks the node up, asks or sends anything, as the platform
refuses it, and exits 1. Start the cluster first.

Every refusal of the platform's exits 1 with its reason. Among them: a
node that is not a worker, a node of another group, a node already marked
for deletion or whose server is stopping, the last node of its group,
which is not removed on its own (scale the node group to 0 or delete the
node group instead), a removal that would take an autoscaling group below
its min count (for both, only nodes that have joined the cluster count, so
a node still being created does not keep the group), a reference several
nodes answer to, even nodes already being removed (the reason lists each
by its node id, which is the server id; the command's lookup lists them
too and sends the reference as typed), and a change while an identical
one is in progress. A reference the platform cannot resolve is refused
the same way: a providerID it cannot see on the cluster's Kubernetes
nodes, and on AWS a name that may be a Kubernetes node name it has not
recorded yet. Name the node by its server name or its server id then;
neither needs the Kubernetes nodes.

A reference that names no node of the cluster exits 3 (the platform says
it is not found in the node group); a node of another group is a refusal
and exits 1. A group or node that is blank, has spaces around it, or is
'.' or '..' (also percent-encoded), a '/' outside a providerID, a
providerID with no id or with a '.' or '..' step, and an -o other than
json or yaml exit 2 before anything is looked up or sent, and a node or
group the platform cannot read (its 422) exits 2 too.

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
	groupName, nodeReference := args[1], args[2]
	yes, _ := cmd.Flags().GetBool("yes")
	drainOptions := drainOptionsFromFlags(cmd)
	cluster, clusterError := resolveNodeGroupCluster(args[0], clusterID)
	if clusterError != nil {
		return clusterError
	}
	kind := cluster.Kind
	if cluster.State == clusterStateStopped {
		return stoppedClusterRemovalRefusal(nodeReference, groupName)
	}

	// sent is the node the request names: the server id of the node the user
	// approved when the lookup found it, so the platform removes that node and
	// no other, whatever else answers to the reference by then; the reference
	// as typed otherwise. Only a UUID is sent as an id, because the platform
	// reads a UUID as a server id and any other reference as a name.
	sent := nodeReference
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
		if target.node != nil {
			if serverID, isServerID := parseServerID(target.node.ID); isServerID {
				sent = serverID
			}
		}
	}

	requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
	if err != nil {
		return err
	}
	defer cancelRequestContext()

	result, removeError := apiClient.RemoveNodeGroupNode(requestContext, kind, clusterID, groupName, sent,
		drainOptions, wait)
	if removeError != nil {
		return removeNodeGroupNodeError(wait, removeError)
	}
	if result.Accepted {
		if handled, err := renderStructured(cmd, newNodeRemovalSubmitted(clusterID, result, drainOptions)); err != nil {
			return err
		} else if handled {
			return nil
		}
		printNodeRemovalAccepted(cmd.OutOrStdout(), clusterID, result, drainOptions)
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

// nameANodeHint ends a refused node reference with the forms that work.
const nameANodeHint = "name the node, its providerID or its server id"

// validateNodeReference refuses a node reference no node can have, before
// anything is looked up or sent: blank or with spaces around it, "." or ".."
// (or one that decodes to them), holding a '/' when it is not a providerID,
// or a providerID with no id or with a "." or ".." step. The reference is
// path-escaped into one segment, but ".." is a path step the platform's edge
// resolves, so `remove-node <c> <group> ..` would reach the group's own
// DELETE (ankra-6pi023). The edge leaves the escaped '/' of a providerID
// alone today; no provider's id has a dot step, so one is refused here
// rather than resting on that. What is checked is what is sent: a reference
// with spaces around it is refused, as a group name is, not trimmed.
func validateNodeReference(reference string) error {
	if strings.TrimSpace(reference) != reference || reference == "" {
		return validatePathArgument("node", reference)
	}
	decoded, _ := client.UnsafePathSegment(reference)
	if scheme := providerIDShape.FindString(decoded); scheme != "" {
		id := strings.Trim(strings.TrimPrefix(decoded, scheme), "/")
		if id == "" {
			return withExitCode(exitUsage, fmt.Errorf("the node %q is a providerID with no id; %s",
				reference, nameANodeHint))
		}
		for _, step := range strings.Split(id, "/") {
			if step == "." || step == ".." {
				return withExitCode(exitUsage, fmt.Errorf("the node %q has a '%s' step, which no providerID has; %s",
					reference, step, nameANodeHint))
			}
		}
		return nil
	}
	if strings.Contains(decoded, "/") {
		return withExitCode(exitUsage, fmt.Errorf("the node %q contains '/' but is not a providerID (scheme://...); %s",
			reference, nameANodeHint))
	}
	return validatePathArgument("node", reference)
}

// stoppedClusterRemovalRefusal refuses a removal from a stopped cluster
// before anything is read, asked or sent. The platform refuses every one,
// whatever the reference names (cluster#5995): offline, its edit would drop
// the worker's rows while a server some providers keep across a stop lived
// on. The refusal is the platform's own sentence, in the shape and with the
// exit code its 409 has here, so a script reads both alike.
func stoppedClusterRemovalRefusal(reference, groupName string) error {
	return withExitCode(exitError, fmt.Errorf("removing node from node group: Node '%s' cannot be removed from "+
		"node group '%s' while the cluster is stopped: start the cluster first", reference, groupName))
}

// removeNodeGroupNodeError wraps a remove-node failure for the terminal. A
// platform that predates the route answers the router's bare 404, which is
// not "the node does not exist": it is said plainly and exits 1, rather than
// the not-found code a script would read as the node being gone already. The
// platform's own 404, for a reference no node of the cluster answers to,
// carries its reason ("not found in node group") and exits 3. Its 422 is a
// group or node it cannot read, an argument to change, so it exits 2 like
// the CLI's own checks.
func removeNodeGroupNodeError(wait bool, removeError error) error {
	if isRouteAbsent(removeError) {
		return withExitCode(exitError, errors.New(
			"removing node from node group: this Ankra platform does not serve node removal yet (the route answered 404); "+
				"check the selected organisation with 'ankra org current' and try again once the platform has it"))
	}
	wrapped := asyncWriteError("removing node from node group", wait, removeError)
	var unexpected *client.UnexpectedResponseError
	if errors.As(removeError, &unexpected) && unexpected.StatusCode == http.StatusUnprocessableEntity {
		return withExitCode(exitUsage, wrapped)
	}
	return wrapped
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

// newNodeRemovalSubmitted is the -o answer for an accepted removal. Its hint
// names commands a script can run: an operation is read with -o json, since
// --watch renders for a terminal and refuses -o. A forced drain is not
// refused, so its hint promises nothing about a refusal.
func newNodeRemovalSubmitted(clusterID string, result *client.RemoveNodeGroupNodeResult,
	drainOptions client.DrainOptions) nodeRemovalSubmitted {
	submitted := nodeRemovalSubmitted{asyncSubmittedResult: asyncSubmittedResult{
		Submitted:   true,
		Operation:   "Node removal",
		OperationID: removalOperationID(result),
	}}
	decided := result.NodeID != ""
	if decided {
		submitted.GroupName, submitted.NodeName, submitted.NodeID = result.GroupName, result.NodeName, result.NodeID
		previousCount, newCount := result.PreviousCount, result.NewCount
		submitted.PreviousCount, submitted.NewCount = &previousCount, &newCount
	}
	switch {
	case submitted.OperationID != "":
		submitted.Hint = "operation " + submitted.OperationID + " drains the node and deletes its server; read it with '" +
			followOperationCommand(submitted.OperationID) + " -o json' until it has ended"
		if !drainOptions.ForceDrain {
			submitted.Hint += ". A refused drain fails it, names what refused it and keeps the node"
		}
	case decided:
		submitted.Hint = "no operation was started with the request: the platform's reconcile drains and deletes the " +
			"node later, and '" + clusterOperationsCommand(clusterID) + "' lists the removal's operation once it starts"
	default:
		submitted.Hint = "the platform was still checking the removal when it answered, so it has not named the " +
			"node yet; '" + clusterOperationsCommand(clusterID) + "' lists the removal's operation once it starts, " +
			"and '" + failedNodeGroupWriteCommand(clusterID) + "' shows a refusal as a failed node_group_write " +
			"operation once it is decided"
	}
	return submitted
}

// removalOperationID is the operation the platform named for the removal,
// or "" for none.
func removalOperationID(result *client.RemoveNodeGroupNodeResult) string {
	if result.OperationID == nil {
		return ""
	}
	return *result.OperationID
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
	operationID := removalOperationID(result)
	if operationID == "" {
		_, _ = fmt.Fprintf(out, "No operation was started with the request: the platform could not start it then (for "+
			"example, another node operation is still running or the cluster's agent is offline), and its reconcile "+
			"drains and deletes this node later. Find the removal's operation then with:\n  %s\n",
			clusterOperationsCommand(clusterID))
		return
	}
	drain := "drains it through its pods' PodDisruptionBudgets"
	if drainOptions.ForceDrain {
		drain = "drains it without honouring its pods' PodDisruptionBudgets"
	}
	_, _ = fmt.Fprintf(out, "The node is marked for removal. Operation %s %s, then deletes its server.\n",
		operationID, drain)
	_, _ = fmt.Fprintf(out, "Follow the drain and the delete with:\n  %s\n", watchOperationCommand(operationID))
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
	drainOptions client.DrainOptions) {
	if result.NodeID != "" {
		printNodeRemoval(out, clusterID, result, drainOptions)
		return
	}
	_, _ = fmt.Fprintln(out, "Node removal submitted. The platform was still checking it when it answered, so it has not "+
		"named the node that leaves yet.")
	if operationID := removalOperationID(result); operationID != "" {
		_, _ = fmt.Fprintf(out, "Follow it with:\n  %s\n", watchOperationCommand(operationID))
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
// itself then, so an inconclusive lookup never refuses the removal.
type removeNodeTarget struct {
	node *client.NodeSummary
	// providerID is the providerID the node's Kubernetes node reports, and
	// providerIDUnknown why it is not known when it is empty.
	providerID        string
	providerIDUnknown string
	unresolved        string
}

// removeNodeLookupTimeout bounds the whole lookup before the prompt. Its
// reads go through the platform, the Kubernetes one through the cluster's
// agent, and a slow answer must not hold the question back for long: when
// the time is up, the prompt says what could not be read. A variable so a
// test can wait less.
var removeNodeLookupTimeout = 10 * time.Second

// readAnswer is one background read's answer.
type readAnswer[T any] struct {
	value T
	err   error
}

// startRead runs read in the background, so reads that do not depend on
// each other run at the same time. A read nobody waits for any more
// finishes on its own.
func startRead[T any](read func() (T, error)) <-chan readAnswer[T] {
	answers := make(chan readAnswer[T], 1)
	go func() {
		value, err := read()
		answers <- readAnswer[T]{value: value, err: err}
	}()
	return answers
}

// awaitRead is the read's answer, or answered false when deadline passed
// first. A read that has already finished counts however late it is asked:
// with the deadline gone too, a select over both would pick at random.
func awaitRead[T any](answers <-chan readAnswer[T], deadline time.Time) (answer readAnswer[T], answered bool) {
	select {
	case answer = <-answers:
		return answer, true
	default:
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case answer = <-answers:
		return answer, true
	case <-timer.C:
		return readAnswer[T]{}, false
	}
}

// kubernetesNodesNamedAfterServers reports whether a kind's servers register
// as the Kubernetes node named after them (the lowercased server name). Every
// node-group kind does but AWS, which registers the EC2 private DNS name and
// records it on the server, where the node list does not show it, so the CLI
// cannot pair an AWS server with its Kubernetes node.
func kubernetesNodesNamedAfterServers(kind string) bool {
	return kind != "aws"
}

// kubernetesNodeView is what the lookup learnt of the cluster's Kubernetes
// nodes: the providerID each node it read reports, by lowercased name (""
// for one that reports none). providerIDs is nil when nothing was read, and
// unread says why.
type kubernetesNodeView struct {
	namedAfterServers bool
	providerIDs       map[string]string
	unread            string
}

// nodesReporting is the names of the Kubernetes nodes read that report
// providerID, without regard to case, in name order.
func (view kubernetesNodeView) nodesReporting(providerID string) []string {
	var reporting []string
	for name, reported := range view.providerIDs {
		if reported != "" && strings.EqualFold(reported, providerID) {
			reporting = append(reporting, name)
		}
	}
	sort.Strings(reporting)
	return reporting
}

// startKubernetesNodeRead reads the providerID of the cluster's Kubernetes
// node called name, or of every node when name is "". A node of that name
// that does not exist answers an empty map. The read asks for a live answer
// and takes no other, as cluster top's capacity read does: a cached copy can
// pair a providerID with a node name that a replaced server has reused since,
// and a cached "not found" can miss a node that has joined since.
func startKubernetesNodeRead(clusterID, name string) <-chan readAnswer[map[string]string] {
	getResources := apiClient.GetResources
	return startRead(func() (map[string]string, error) {
		response, readError := getResources(clusterID, client.GetResourcesRequest{
			ResourceRequests: []client.ResourceRequestItem{{Kind: "Node", Version: "v1", Name: name}},
			SkipCache:        true,
		})
		if readError != nil {
			return nil, readError
		}
		if response == nil || len(response.ResourceResponses) != 1 {
			return nil, noItemInReplyError("the cluster's Nodes")
		}
		if response.ResourceResponses[0].CacheMetadata != nil {
			return nil, errors.New("the cluster did not answer live; only the platform's cached copy did")
		}
		var items []interface{}
		if name != "" {
			named, namedError := namedReadItems(response, "Node", name, "")
			switch {
			case exitCodeFor(namedError) == exitNotFound:
				return map[string]string{}, nil
			case namedError != nil:
				return nil, namedError
			}
			items = named
		} else {
			if failed := failedReadError(response.ResourceResponses[0], "the cluster's Nodes"); failed != nil {
				return nil, failed
			}
			items = response.ResourceResponses[0].Items
		}
		providerIDs := map[string]string{}
		for _, item := range items {
			object, isObject := item.(map[string]interface{})
			if !isObject {
				continue
			}
			if nodeName := getNestedString(object, "metadata", "name"); nodeName != "" {
				providerIDs[strings.ToLower(nodeName)] = getNestedString(object, "spec", "providerID")
			}
		}
		return providerIDs, nil
	})
}

// resolveRemoveNodeTarget looks reference up in the cluster's node list and,
// for the providerID, in the cluster's Kubernetes nodes, within
// removeNodeLookupTimeout. The two reads run at the same time whenever the
// reference says which Kubernetes read it needs: every node for a providerID
// (the one reporting it is not known yet), the one node named like the
// server for a name. A server id names its node only once the list is read.
// It runs on a running cluster only: a stopped one is refused before it.
func resolveRemoveNodeTarget(kind, clusterID, reference string) removeNodeTarget {
	nodesOps := nodeGroupCallsFor(kind).nodes
	if nodesOps == nil {
		return removeNodeTarget{unresolved: "the CLI cannot list the nodes of a " + kind + " cluster"}
	}
	deadline := time.Now().Add(removeNodeLookupTimeout)
	list := nodesOps().list
	listing := startRead(func() (*client.NodeListResult, error) { return list(clusterID) })

	namedAfterServers := kubernetesNodesNamedAfterServers(kind)
	_, isServerID := parseServerID(reference)
	var kubernetesRead <-chan readAnswer[map[string]string]
	switch {
	case !namedAfterServers:
	case providerIDShape.MatchString(reference):
		kubernetesRead = startKubernetesNodeRead(clusterID, "")
	case !isServerID:
		kubernetesRead = startKubernetesNodeRead(clusterID, strings.ToLower(reference))
	}

	listed, answered := awaitRead(listing, deadline)
	switch {
	case !answered:
		return removeNodeTarget{unresolved: fmt.Sprintf("the cluster's node list did not answer within %s",
			removeNodeLookupTimeout)}
	case listed.err != nil:
		return removeNodeTarget{unresolved: fmt.Sprintf("the cluster's node list could not be read (%v)", listed.err)}
	case listed.value == nil:
		return removeNodeTarget{unresolved: "the platform answered no node list"}
	}
	nodes := nodesTheRemovalCanName(listed.value.Nodes, kind)

	if kubernetesRead == nil && namedAfterServers {
		if candidates := nodesAnswering(nodes, kubernetesNodeView{}, reference); len(candidates) == 1 &&
			candidates[0].Name != "" {
			kubernetesRead = startKubernetesNodeRead(clusterID, strings.ToLower(candidates[0].Name))
		}
	}
	// A read that starts after the list has only what the list left of the
	// budget, so running out of time is said as that, not as a read failure.
	kubernetes := kubernetesNodeView{namedAfterServers: namedAfterServers}
	switch {
	case !namedAfterServers:
		kubernetes.unread = "on AWS the CLI cannot tell which Kubernetes node a server registered as"
	case kubernetesRead == nil:
		kubernetes.unread = "the server has no name to find its Kubernetes node by"
	default:
		read, readAnswered := awaitRead(kubernetesRead, deadline)
		switch {
		case !readAnswered:
			kubernetes.unread = fmt.Sprintf("the cluster's Kubernetes nodes did not answer within the lookup's %s",
				removeNodeLookupTimeout)
		case read.err != nil:
			kubernetes.unread = fmt.Sprintf("the cluster's Kubernetes nodes could not be read (%v)", read.err)
		default:
			kubernetes.providerIDs = read.value
		}
	}
	return matchRemoveNodeReference(nodes, kubernetes, reference)
}

// matchRemoveNodeReference finds the node reference names the way the
// platform does: a UUID that is a server's id names that server; any other
// reference names the servers that answer to it (nodeAnswersTo). A reference
// several nodes answer to names none of them, whatever their states, as on
// the platform, which refuses it naming each by its node id: the CLI never
// picks one, since the server id it would send is matched before any name.
// So does a providerID several Kubernetes nodes report, which the platform
// matches to none of them either: a server the list leaves out may be
// behind the other node. nodes is what the platform looks in
// (nodesTheRemovalCanName).
func matchRemoveNodeReference(nodes []client.NodeSummary, kubernetes kubernetesNodeView,
	reference string) removeNodeTarget {
	reporting := kubernetes.nodesReporting(reference)
	if len(reporting) > 1 {
		return removeNodeTarget{unresolved: fmt.Sprintf("%d of the cluster's Kubernetes nodes report that providerID",
			len(reporting))}
	}
	candidates := nodesAnswering(nodes, kubernetes, reference)
	switch {
	case len(candidates) == 1:
		return removeNodeTargetFor(candidates[0], kubernetes)
	case len(candidates) > 1:
		serverIDs := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			serverIDs = append(serverIDs, candidate.ID)
		}
		sort.Strings(serverIDs)
		return removeNodeTarget{unresolved: fmt.Sprintf("%d nodes in the cluster's records answer to it "+
			"(server ids %s), so name the one to remove by its server id", len(candidates),
			strings.Join(serverIDs, ", "))}
	case !kubernetes.namedAfterServers:
		return removeNodeTarget{unresolved: "no server in the cluster's records has that id or name, and " +
			kubernetes.unread}
	case kubernetes.providerIDs == nil && providerIDShape.MatchString(reference):
		return removeNodeTarget{unresolved: kubernetes.unread + ", so the CLI could not tell which node reports " +
			"that providerID"}
	case len(reporting) == 1:
		// The node reporting it pairs with no server a removal can name: one
		// that is down or deleted, or a node not named after its server.
		return removeNodeTarget{unresolved: fmt.Sprintf("the Kubernetes node %q reports that providerID, but no "+
			"live server in the cluster's records has that name", reporting[0])}
	default:
		return removeNodeTarget{unresolved: "no node in the cluster's records has that server id, name or providerID"}
	}
}

// clusterStateStopped is a stopped cluster's state in the cluster list, and
// nodeStateDown and nodeStateStopping the node list's states for a server
// that is gone and for one being removed.
const (
	clusterStateStopped = "stopped"
	nodeStateDown       = "down"
	nodeStateStopping   = "stopping"
)

// nodesTheRemovalCanName is the rows of a running cluster's node list the
// platform looks a reference up in: its servers (the list also has its
// bastion and VMs), without the deleted (deleted_at, rows the platform no
// longer loads) and down ones. Rows being removed stay: the platform counts
// them as answers to a name.
func nodesTheRemovalCanName(nodes []client.NodeSummary, kind string) []client.NodeSummary {
	serverKind := kind + "_server"
	var named []client.NodeSummary
	for _, node := range nodes {
		if node.Kind == serverKind && !node.IsDeleted && node.State != nodeStateDown {
			named = append(named, node)
		}
	}
	return named
}

// parseServerID reads reference as a server id the way the platform does,
// with uuid.Parse: the canonical form, and also 32 hex digits, {braces} and
// urn:uuid:, none of which looksLikeUUID takes. canonical is the id as the
// node list writes it.
func parseServerID(reference string) (canonical string, isServerID bool) {
	parsed, parseError := uuid.Parse(reference)
	if parseError != nil {
		return "", false
	}
	return parsed.String(), true
}

// nodesAnswering is the nodes reference names: the one whose server id it is,
// when it is a UUID that is one; otherwise every node that answers to it.
func nodesAnswering(nodes []client.NodeSummary, kubernetes kubernetesNodeView,
	reference string) []*client.NodeSummary {
	if serverID, isServerID := parseServerID(reference); isServerID {
		for index := range nodes {
			if strings.EqualFold(nodes[index].ID, serverID) {
				return []*client.NodeSummary{&nodes[index]}
			}
		}
	}
	var candidates []*client.NodeSummary
	for index := range nodes {
		if nodeAnswersTo(&nodes[index], kubernetes, reference) {
			candidates = append(candidates, &nodes[index])
		}
	}
	return candidates
}

// nodeAnswersTo reports whether reference is one of the names node answers
// to, without regard to case: its server name, which is also its Kubernetes
// node name where nodes are named after their servers, and the providerID
// that Kubernetes node reports.
func nodeAnswersTo(node *client.NodeSummary, kubernetes kubernetesNodeView, reference string) bool {
	if node.Name == "" {
		return false
	}
	if strings.EqualFold(node.Name, reference) {
		return true
	}
	providerID := kubernetes.providerIDs[strings.ToLower(node.Name)]
	return providerID != "" && strings.EqualFold(providerID, reference)
}

func removeNodeTargetFor(node *client.NodeSummary, kubernetes kubernetesNodeView) removeNodeTarget {
	target := removeNodeTarget{node: node}
	nodeName := strings.ToLower(node.Name)
	providerID, known := kubernetes.providerIDs[nodeName]
	switch {
	case kubernetes.providerIDs == nil:
		target.providerIDUnknown = "not known: " + kubernetes.unread
	case !known:
		target.providerIDUnknown = "not known: no Kubernetes node is named " + nodeName
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
			"reference itself, and refuses it with its reason if it cannot remove the node it names.\n", target.unresolved)
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
		if node.State == nodeStateStopping {
			_, _ = fmt.Fprintln(&prompt, "  State:       stopping: the platform refuses a node whose server is stopping "+
				"or that is already marked for deletion")
		}
	}
	_, _ = fmt.Fprintf(&prompt, "%s [y/N]: ", consequence)
	return prompt.String()
}
