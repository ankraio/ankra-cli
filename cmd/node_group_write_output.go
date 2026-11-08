package cmd

import (
	"fmt"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// nodeGroupWriteWaitUsage is the --wait help of the node-group writes whose
// work on the nodes the platform does after it answers (add, scale, upgrade,
// delete, labels, taints). With wait=true the platform holds the request
// until the change is recorded or refused, not until the nodes have changed:
// a scale-up reported "scaled" about two minutes before its new node was
// Ready, with nothing to follow (ankra-v3qdto.6). The reconcile, or the
// operation an instance-type change starts, does the rest.
const nodeGroupWriteWaitUsage = "Wait until the platform has recorded the change or refused it. " +
	"It does not wait for the change to reach the nodes (nodes created, resized, drained or removed, labels and taints applied) " +
	"or for nodes to become Ready: the platform does that afterwards in an operation, which " +
	"'ankra cluster operations list --cluster <cluster>' shows (default: submit and return immediately)"

// registerNodeGroupWriteFlags is registerAsyncWriteFlags for those writes,
// with the --wait help saying what it waits for.
func registerNodeGroupWriteFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		registerAsyncWriteFlags(command)
		command.Flags().Lookup("wait").Usage = nodeGroupWriteWaitUsage
	}
}

// followNodeGroupWorkCommand lists the cluster's operations, where the
// reconcile operation doing a node-group write's cloud work appears. The
// write's answer names no operation, so the listing is how to follow it.
func followNodeGroupWorkCommand(clusterID string) string {
	return "ankra cluster operations list --cluster " + clusterID
}

// printNodeGroupReconciling ends a recorded node-group write: what is still
// to happen and where to watch it.
func printNodeGroupReconciling(clusterID string) {
	fmt.Println("The platform does the cloud work in a reconcile operation, which may take a moment to appear; nodes take a few minutes to join the cluster and become Ready.")
	fmt.Printf("Follow it with:\n  %s\n", followNodeGroupWorkCommand(clusterID))
}

// printNodeGroupAdded reports an add the platform recorded. The answer comes
// when the group's definition is stored, before any server exists, so it
// says the nodes are being created rather than that they were.
func printNodeGroupAdded(result *client.AddNodeGroupResult, clusterID string) {
	fmt.Printf("Node group '%s' recorded with %d node(s); the nodes are being created.\n", result.GroupName, result.Count)
	printNodeGroupReconciling(clusterID)
}

// printNodeGroupScaled reports a scale the platform recorded, or that there
// was nothing to change.
func printNodeGroupScaled(result *client.ScaleNodeGroupResult, clusterID string) {
	switch {
	case result.NewCount == result.PreviousCount:
		fmt.Printf("Node group '%s' already has %d node(s); nothing to change.\n", result.GroupName, result.NewCount)
		return
	case result.NewCount > result.PreviousCount:
		fmt.Printf("Node group '%s' set to %d node(s), up from %d; the new nodes are being created.\n",
			result.GroupName, result.NewCount, result.PreviousCount)
	default:
		fmt.Printf("Node group '%s' set to %d node(s), down from %d; the nodes leaving are drained and removed.\n",
			result.GroupName, result.NewCount, result.PreviousCount)
	}
	printNodeGroupReconciling(clusterID)
}

// printNodeGroupDeleted reports a delete the platform recorded: its nodes
// are drained and removed afterwards, not by the time it answers.
func printNodeGroupDeleted(result *client.DeleteNodeGroupResult, clusterID string) {
	fmt.Printf("Node group '%s' marked for deletion; its %d node(s) are drained and removed.\n", result.GroupName, result.Deleted)
	printNodeGroupReconciling(clusterID)
}

// printNodeGroupResized reports an instance-type change the platform
// recorded. change names what the command changes ("instance type", "plan",
// "size"). The platform answers once the change is recorded, before any node
// is resized, and names the operation resizing them when it started one, so
// the answer says the resize rolls out rather than that it happened.
func printNodeGroupResized(result *client.UpdateNodeGroupResult, clusterID, change string) {
	if result.OperationID != nil && *result.OperationID != "" {
		fmt.Printf("Node group '%s' %s change recorded for %d node(s); the resize rolls out in operation %s.\n",
			result.GroupName, change, result.Updated, *result.OperationID)
	} else {
		fmt.Printf("Node group '%s' %s change recorded for %d node(s); the resize rolls out afterwards. "+
			"The platform started no operation for it: a stopped cluster applies it when it starts, and a group "+
			"still being created, or already being changed, takes it up in that work.\n",
			result.GroupName, change, result.Updated)
	}
	if result.Notice != "" {
		fmt.Printf("Note: %s\n", result.Notice)
	}
	if result.OperationID != nil && *result.OperationID != "" {
		fmt.Printf("Follow it with:\n  ankra cluster operations list %s\n", *result.OperationID)
		return
	}
	fmt.Printf("Follow it with:\n  %s\n", followNodeGroupWorkCommand(clusterID))
}
