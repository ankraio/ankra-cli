package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestIsTerminalExecutionStatus(t *testing.T) {
	cases := []struct {
		status       string
		wantTerminal bool
	}{
		{"success", true},
		{"failed", true},
		{"critical", true},
		{"cancelled", true},
		{"timeout", true},
		{"SUCCESS", true},
		{"  Failed  ", true},
		{"running", false},
		{"stopping", false},
		{"cleanup", false},
		{"pending", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isTerminalExecutionStatus(tc.status); got != tc.wantTerminal {
			t.Errorf("isTerminalExecutionStatus(%q) = %v, want %v", tc.status, got, tc.wantTerminal)
		}
	}
}

func TestClampWatchInterval(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, defaultWatchInterval},
		{-5 * time.Second, defaultWatchInterval},
		{10 * time.Millisecond, minWatchInterval},
		{minWatchInterval, minWatchInterval},
		{10 * time.Second, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := clampWatchInterval(tc.in); got != tc.want {
			t.Errorf("clampWatchInterval(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestEncodeStructured(t *testing.T) {
	value := map[string]string{"name": "demo", "status": "running"}

	t.Run("json", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputJSON, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `"name": "demo"`) || !strings.Contains(out, `"status": "running"`) {
			t.Errorf("json output missing fields: %q", out)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputYAML, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "name: demo") || !strings.Contains(out, "status: running") {
			t.Errorf("yaml output missing fields: %q", out)
		}
	})

	t.Run("default is a no-op", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputDefault, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if buf.Len() != 0 {
			t.Errorf("expected no output for default format, got %q", buf.String())
		}
	})
}

func TestDeleteClusterDryRun(t *testing.T) {
	originalDryRun := dryRunDelete
	dryRunDelete = true
	t.Cleanup(func() { dryRunDelete = originalDryRun })

	output := captureStdout(t, func() {
		if err := deleteClusterCmd.RunE(deleteClusterCmd, []string{"my-cluster"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(output, "my-cluster") || !strings.Contains(output, "--dry-run") {
		t.Errorf("expected dry-run notice naming the cluster, got: %q", output)
	}
	if !strings.Contains(output, "Would delete") {
		t.Errorf("expected 'Would delete' notice, got: %q", output)
	}
}
