package cmd

import (
	"context"
	"errors"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Exit codes are part of the CLI's scripting contract. 0 and 1 match the
// historical behavior; 2-6 let scripts and CI distinguish failures they can
// act on (re-run usage errors after fixing the invocation, treat not-found
// as idempotent success, re-authenticate on auth failures, retry on wait
// expiry) without parsing error text.
const (
	exitOK          = 0
	exitError       = 1 // API or runtime failure
	exitUsage       = 2 // bad flags, arguments, or subcommand
	exitNotFound    = 3 // the targeted resource does not exist
	exitCancelled   = 4 // confirmation declined
	exitWaitTimeout = 5 // --wait/--timeout expired before completion
	exitAuth        = 6 // missing, expired, or rejected credentials
)

// codedError attaches an exit code to an error without changing its message.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// withExitCode wraps err so Execute exits with code. A nil err stays nil, so
// call sites can wrap unconditionally.
func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, err: err}
}

// errCancelled is the shared confirmation-declined error. It carries
// exitCancelled so a declined [y/N] prompt is distinguishable from a real
// failure in scripts.
var errCancelled = withExitCode(exitCancelled, errors.New("cancelled"))

// exitCodeFor classifies an error returned by ExecuteC into an exit code.
// Explicit withExitCode wrapping wins; otherwise auth and not-found API
// responses and --wait expiry map to their codes, and anything unclassified
// is a generic failure.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(err, &unexpected) {
		switch unexpected.StatusCode {
		case 401, 403:
			return exitAuth
		case 404:
			return exitNotFound
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return exitWaitTimeout
	}
	// Cobra reports unknown subcommands as plain errors with a fixed prefix.
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return exitUsage
	}
	return exitError
}

// wrapArgsValidators decorates every registered Args validator in the tree so
// argument-count errors exit with exitUsage instead of the generic failure
// code. Called once from Execute after all commands are registered.
func wrapArgsValidators(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			return withExitCode(exitUsage, validate(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		wrapArgsValidators(sub)
	}
}
