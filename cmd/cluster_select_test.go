package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/manifoldco/promptui"
)

// TestIsPromptCancellation asserts that promptui's abort signals (Ctrl+C, ESC,
// EOF) are recognised as cancellations while real errors are not. The
// interactive picker itself needs a TTY, so cover the mapping via this helper.
func TestIsPromptCancellation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"interrupt", promptui.ErrInterrupt, true},
		{"abort", promptui.ErrAbort, true},
		{"eof", promptui.ErrEOF, true},
		{"wrapped interrupt", fmt.Errorf("prompt: %w", promptui.ErrInterrupt), true},
		{"real error", errors.New("backend unreachable"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPromptCancellation(tc.err); got != tc.want {
				t.Errorf("isPromptCancellation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestPickerAbortMapsToCancelledExit asserts that when the picker abort is
// mapped to errCancelled, it classifies as exitCancelled (exit 4) rather than
// the generic failure code.
func TestPickerAbortMapsToCancelledExit(t *testing.T) {
	if !isPromptCancellation(promptui.ErrInterrupt) {
		t.Fatal("interrupt should be treated as a cancellation")
	}
	// The handler returns errCancelled for a cancellation; confirm that
	// sentinel still carries exitCancelled.
	if got := exitCodeFor(errCancelled); got != exitCancelled {
		t.Errorf("picker abort should exit %d, got %d", exitCancelled, got)
	}
}
