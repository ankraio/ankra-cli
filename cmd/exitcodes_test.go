package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func TestExitCodeForClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is ok", nil, exitOK},
		{"plain error", errors.New("boom"), exitError},
		{"explicit code", withExitCode(exitNotFound, errors.New("gone")), exitNotFound},
		{"explicit code wrapped deeper", fmt.Errorf("outer: %w", withExitCode(exitWaitTimeout, errors.New("slow"))), exitWaitTimeout},
		{"cancelled sentinel", errCancelled, exitCancelled},
		{"cancelled wrapped", fmt.Errorf("delete: %w", errCancelled), exitCancelled},
		{"unauthorized response", &client.UnexpectedResponseError{StatusCode: 401}, exitAuth},
		{"forbidden response", &client.UnexpectedResponseError{StatusCode: 403}, exitAuth},
		{"not-found response", &client.UnexpectedResponseError{StatusCode: 404}, exitNotFound},
		{"server error response", &client.UnexpectedResponseError{StatusCode: 500}, exitError},
		{"wait deadline", fmt.Errorf("waiting: %w", context.DeadlineExceeded), exitWaitTimeout},
		{"unknown command", errors.New(`unknown command "clusterz" for "ankra"`), exitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithExitCodeNilStaysNil(t *testing.T) {
	if err := withExitCode(exitNotFound, nil); err != nil {
		t.Errorf("withExitCode(_, nil) = %v, want nil", err)
	}
}

func TestWithExitCodePreservesMessageAndUnwraps(t *testing.T) {
	underlying := errors.New("cluster \"x\" not found")
	err := withExitCode(exitNotFound, underlying)
	if err.Error() != underlying.Error() {
		t.Errorf("message changed: %q vs %q", err.Error(), underlying.Error())
	}
	if !errors.Is(err, underlying) {
		t.Error("wrapped error should unwrap to the underlying error")
	}
}

func TestWrapArgsValidatorsMarksUsageErrors(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	child := &cobra.Command{
		Use:  "child",
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	root.AddCommand(child)
	wrapArgsValidators(root)

	err := child.Args(child, []string{})
	if err == nil {
		t.Fatal("expected an args-validation error")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("args error should classify as exitUsage, got %d", got)
	}

	if err := child.Args(child, []string{"one"}); err != nil {
		t.Errorf("valid args should still pass, got %v", err)
	}
}
