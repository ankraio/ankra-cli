package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func nodeGroupAsyncContext(command *cobra.Command) (context.Context, context.CancelFunc, bool, error) {
	wait, err := asyncWriteWaitFlag(command)
	if err != nil {
		return nil, nil, false, err
	}
	requestContext, cancelRequestContext, err := asyncWriteRequestContext(command)
	if err != nil {
		return nil, nil, false, err
	}
	return requestContext, cancelRequestContext, wait, nil
}

// failedNodeGroupWriteCommand is the command that lists the operation the
// platform records when a node-group write it already accepted is refused or
// fails afterwards. The 202 answer names no operation (the record is written
// when the failure is decided, after the answer), so the listing is the way
// to find it. The cluster is named explicitly so it works from any selection.
func failedNodeGroupWriteCommand(clusterID string) string {
	return "ankra cluster operations list --failed --cluster " + clusterID
}

// newNodeGroupSubmittedResult is newAsyncSubmittedResult for a node-group
// write: the hint also names where a late refusal is recorded.
func newNodeGroupSubmittedResult(operationLabel, clusterID string) asyncSubmittedResult {
	result := newAsyncSubmittedResult(operationLabel)
	result.Hint += "; if the platform refuses it after accepting it, '" + failedNodeGroupWriteCommand(clusterID) +
		"' shows the failed node_group_write operation with the provider's reason"
	return result
}

func printNodeGroupWriteSubmitted(operationLabel, clusterID string) {
	printAsyncWriteSubmitted(operationLabel)
	fmt.Printf("If the platform refuses it after accepting it (for example the provider has no stock of the requested server type), the refusal is recorded as a failed node_group_write operation with the provider's reason:\n  %s\n", failedNodeGroupWriteCommand(clusterID))
}
