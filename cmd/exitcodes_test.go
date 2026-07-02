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
		{"bare deadline stays generic (internal request timeouts are not --wait expiry)", fmt.Errorf("waiting: %w", context.DeadlineExceeded), exitError},
		{"unauthorized sentinel", fmt.Errorf("listing clusters: %w", client.ErrUnauthorized), exitAuth},
		{"unknown command", errors.New(`unknown command "clusterz" for "ankra"`), exitUsage},
		{"required flag", errors.New(`required flag(s) "file" not set`), exitUsage},
		{"flag group violation", errors.New(`if any flags in the group [file cluster] are set none of the others can be; [cluster file] were all set`), exitUsage},
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

func TestAsyncWriteErrorTagsWaitExpiryOnly(t *testing.T) {
	deadline := fmt.Errorf("request failed: %w", context.DeadlineExceeded)

	if got := exitCodeFor(asyncWriteError("applying cluster", true, deadline)); got != exitWaitTimeout {
		t.Errorf("--wait deadline expiry should exit %d, got %d", exitWaitTimeout, got)
	}
	if got := exitCodeFor(asyncWriteError("applying cluster", false, deadline)); got != exitError {
		t.Errorf("internal deadline without --wait should exit %d, got %d", exitError, got)
	}
	if got := exitCodeFor(asyncWriteError("applying cluster", true, errors.New("rejected"))); got != exitError {
		t.Errorf("non-deadline failure with --wait should exit %d, got %d", exitError, got)
	}
}
