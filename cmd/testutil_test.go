package cmd

import (
	"bytes"
	"os"
	"regexp"
	"testing"
	"time"

	"ankra/internal/client"
)

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSICodes(input string) string {
	return ansiEscapePattern.ReplaceAllString(input, "")
}

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	os.Stdout = writer

	fn()

	_ = writer.Close()
	os.Stdout = originalStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(reader)
	return buf.String()
}

// withFastClientRetries shortens, for one test, the real client's waits
// between the attempts of a retried read (1s, then 2s), for tests whose fake
// platform answers a read with 502, 503 or 504 on purpose. The read is still
// tried three times; only the sleeps go.
func withFastClientRetries(t *testing.T) {
	t.Helper()
	t.Cleanup(client.SetRetryBackoffSchedule([]time.Duration{time.Millisecond, time.Millisecond}))
}
