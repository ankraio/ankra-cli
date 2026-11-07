package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// nodeGroupAutoscalingWaitTimeout is the default --timeout of
// 'node-group autoscaling set'. A scale-down to a lowered max removes one node
// at a time and each removal may wait up to fifteen minutes for the pipeline
// steps on its node, so the shared ten-minute default would report a failure
// for a scale-down that is still succeeding.
const nodeGroupAutoscalingWaitTimeout = time.Hour

// nodeGroupOperationPollInterval is how often --wait reads the operation.
var nodeGroupOperationPollInterval = 10 * time.Second

// waitForNodeGroupOperation reads the operation until it ends or ctx expires,
// and answers it. An operation that ends in anything but success is an error
// naming its status and recorded error; ctx expiring answers ctx's error, so
// asyncWriteError tags it as a wait timeout.
func waitForNodeGroupOperation(ctx context.Context, operationID string) (client.ExecutionSummary, error) {
	for {
		detail, readError := apiClient.GetExecution(operationID)
		if readError != nil {
			return client.ExecutionSummary{}, fmt.Errorf("reading operation %s: %w", operationID, readError)
		}
		status := strings.ToLower(strings.TrimSpace(detail.Execution.Status))
		if isTerminalExecutionStatus(status) {
			if status == "success" || status == "succeeded" {
				return detail.Execution, nil
			}
			reason := "no error was recorded"
			if detail.Execution.ErrorExcerpt != nil && *detail.Execution.ErrorExcerpt != "" {
				reason = *detail.Execution.ErrorExcerpt
			}
			return detail.Execution, fmt.Errorf("operation %s ended %s: %s (see 'ankra cluster operations steps %s')",
				operationID, status, reason, operationID)
		}
		pollTimer := time.NewTimer(nodeGroupOperationPollInterval)
		select {
		case <-ctx.Done():
			pollTimer.Stop()
			return detail.Execution, ctx.Err()
		case <-pollTimer.C:
		}
	}
}

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
		"' shows the failed node_group_write operation with the provider's reason once it is decided; an empty listing right away does not mean the change was applied"
	return result
}

func printNodeGroupWriteSubmitted(operationLabel, clusterID string) {
	printAsyncWriteSubmitted(operationLabel)
	fmt.Printf("If the platform refuses it after accepting it (for example the provider has no stock of the requested server type), the refusal is recorded as a failed node_group_write operation with the provider's reason:\n  %s\n", failedNodeGroupWriteCommand(clusterID))
	fmt.Println("The record appears once the refusal is decided, so an empty listing right away does not mean the change was applied.")
}
