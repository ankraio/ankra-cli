package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func nodeGroupAsyncContext(command *cobra.Command) (context.Context, context.CancelFunc, bool) {
	wait := asyncWriteWaitFlag(command)
	requestContext, cancelRequestContext := asyncWriteRequestContext(command)
	return requestContext, cancelRequestContext, wait
}

func handleNodeGroupSubmitError(operationLabel string, err error) {
	fmt.Fprintf(os.Stderr, "Error %s: %v\n", operationLabel, err)
	os.Exit(1)
}
