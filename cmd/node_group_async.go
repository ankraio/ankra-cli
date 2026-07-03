package cmd

import (
	"context"

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
