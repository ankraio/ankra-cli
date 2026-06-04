package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const defaultAsyncWriteTimeout = 10 * time.Minute

func registerAsyncWriteFlags(command *cobra.Command) {
	command.Flags().Bool("wait", false, "Wait for the operation to finish and report success or failure (default: submit and return immediately)")
	command.Flags().Duration("timeout", defaultAsyncWriteTimeout, "Maximum time to wait when --wait is set")
}

func asyncWriteWaitFlag(command *cobra.Command) bool {
	wait, err := command.Flags().GetBool("wait")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading --wait: %s\n", err)
		os.Exit(1)
	}
	return wait
}

func asyncWriteRequestContext(command *cobra.Command) (context.Context, context.CancelFunc) {
	wait := asyncWriteWaitFlag(command)
	if !wait {
		return context.Background(), func() {}
	}
	timeout, err := command.Flags().GetDuration("timeout")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading --timeout: %s\n", err)
		os.Exit(1)
	}
	return context.WithTimeout(context.Background(), timeout)
}

func printAsyncWriteSubmitted(operationLabel string) {
	fmt.Printf("%s submitted.\n", operationLabel)
	fmt.Println("The platform is applying changes in the background.")
	fmt.Println("Re-run the same command with --wait to block until completion and see the full result.")
	fmt.Println("Avoid submitting the same change again while it may still be running (duplicate node groups can double cost).")
}
