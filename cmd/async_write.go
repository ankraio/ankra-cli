package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

const defaultAsyncWriteTimeout = 10 * time.Minute

func registerAsyncWriteFlags(command *cobra.Command) {
	registerAsyncWriteFlagsWithTimeout(command, defaultAsyncWriteTimeout)
}

// registerAsyncWriteFlagsWithTimeout is for operations the platform itself
// allows longer than the shared default. A --wait shorter than the bound the
// platform works to reports a failure for a run that is still succeeding, so
// a command whose lane can legitimately outlast ten minutes sets its own
// default here rather than leaving users to discover it (ankra-0xsdd.42).
func registerAsyncWriteFlagsWithTimeout(command *cobra.Command, defaultTimeout time.Duration) {
	command.Flags().Bool("wait", false, "Wait for the operation to finish and report success or failure (default: submit and return immediately)")
	command.Flags().Duration("timeout", defaultTimeout, "Maximum time to wait when --wait is set")
}

func asyncWriteWaitFlag(command *cobra.Command) (bool, error) {
	wait, err := command.Flags().GetBool("wait")
	if err != nil {
		return false, fmt.Errorf("reading --wait: %w", err)
	}
	return wait, nil
}

func asyncWriteRequestContext(command *cobra.Command) (context.Context, context.CancelFunc, error) {
	wait, err := asyncWriteWaitFlag(command)
	if err != nil {
		return nil, nil, err
	}
	if !wait {
		return context.Background(), func() {}, nil
	}
	timeout, err := command.Flags().GetDuration("timeout")
	if err != nil {
		return nil, nil, fmt.Errorf("reading --timeout: %w", err)
	}
	requestContext, cancel := context.WithTimeout(context.Background(), timeout)
	return requestContext, cancel, nil
}

// asyncWriteError wraps a failed asynchronous write, tagging it with
// exitWaitTimeout when the --wait deadline expired so scripts can distinguish
// "gave up waiting" (the write may still complete) from a rejected write.
func asyncWriteError(operationLabel string, wait bool, err error) error {
	wrapped := fmt.Errorf("%s: %w", operationLabel, err)
	if wait && errors.Is(err, context.DeadlineExceeded) {
		return withExitCode(exitWaitTimeout, wrapped)
	}
	return wrapped
}

func printAsyncWriteSubmitted(operationLabel string) {
	fmt.Printf("%s submitted.\n", operationLabel)
	fmt.Println("The platform is applying changes in the background.")
	fmt.Println("Re-run the same command with --wait to block until completion and see the full result.")
	fmt.Println("Avoid submitting the same change again while it may still be running (duplicate node groups can double cost).")
}
