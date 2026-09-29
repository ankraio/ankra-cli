package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"ankra/internal/client"
)

func exitFrame(code int) client.PodTerminalFrame {
	return client.PodTerminalFrame{Type: "exit", Code: &code}
}

func stderrFrame(text string) client.PodTerminalFrame {
	return client.PodTerminalFrame{Type: "stderr", Data: base64.StdEncoding.EncodeToString([]byte(text))}
}

func resetExecFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		_ = clusterExecCmd.Flags().Set("namespace", "")
		_ = clusterExecCmd.Flags().Set("container", "")
		_ = clusterExecCmd.Flags().Set("stdin", "false")
		_ = clusterExecCmd.Flags().Set("output", "")
		clusterExecCmd.SilenceErrors = false
		clusterExecCmd.Flags().Init(clusterExecCmd.Name(), pflag.ContinueOnError)
		rootCmd.SetIn(nil)
	}
	reset()
	t.Cleanup(reset)
}

func TestClusterExecPassesTheArgvVerbatimAndStreamsOutput(t *testing.T) {
	terminal := newFakePodTerminal(nil,
		client.PodTerminalFrame{Type: "connecting"},
		client.PodTerminalFrame{Type: "connected"},
		stdoutFrame("42\n"),
		exitFrame(0),
		client.PodTerminalFrame{Type: "end"})
	mock := &terminalMock{terminal: terminal}
	setMockClient(t, mock)
	resetExecFlags(t)
	writeSelectedClusterJSON(t)

	output, runError := executeCommand("cluster", "exec", "cnpg-cluster-1", "-n", "cnpg-database", "-c", "postgres",
		"--", "psql", "-U", "postgres", "-Atc", "select count(*) from users")
	if runError != nil {
		t.Fatalf("unexpected error: %v", runError)
	}
	if mock.openRequest == nil {
		t.Fatal("no session was opened")
	}
	request := *mock.openRequest
	expectedArgv := []string{"psql", "-U", "postgres", "-Atc", "select count(*) from users"}
	if !reflect.DeepEqual(request.Command, expectedArgv) {
		t.Errorf("argv = %q, want %q", request.Command, expectedArgv)
	}
	if request.Namespace != "cnpg-database" || request.PodName != "cnpg-cluster-1" || request.ContainerName != "postgres" || request.Stdin {
		t.Errorf("request not carried: %+v", request)
	}
	if output != "42\n" {
		t.Errorf("only the command's own output belongs on the streams, got %q", output)
	}
}

func TestClusterExecExitsWithTheRemoteExitCode(t *testing.T) {
	terminal := newFakePodTerminal(nil,
		stderrFrame("psql: error: connection refused\n"),
		exitFrame(2),
		client.PodTerminalFrame{Type: "end"})
	setMockClient(t, &terminalMock{terminal: terminal})
	resetExecFlags(t)
	writeSelectedClusterJSON(t)

	output, runError := executeCommand("cluster", "exec", "web-1", "-n", "default", "-c", "app", "--", "psql", "-c", "select 1")
	if runError == nil {
		t.Fatal("a non-zero remote exit must fail the command")
	}
	if code := exitCodeFor(runError); code != 2 {
		t.Errorf("exit code = %d, want the remote 2", code)
	}
	if !clusterExecCmd.SilenceErrors {
		t.Error("a remote exit must not add a CLI error line on top of the command's own stderr")
	}
	if strings.Contains(output, "Error:") || !strings.Contains(output, "connection refused") {
		t.Errorf("output = %q", output)
	}
}

func TestClusterExecJSONEnvelope(t *testing.T) {
	terminal := newFakePodTerminal(nil,
		stdoutFrame("ok\n"),
		stderrFrame("warn\n"),
		exitFrame(3),
		client.PodTerminalFrame{Type: "end"})
	setMockClient(t, &terminalMock{terminal: terminal})
	resetExecFlags(t)
	writeSelectedClusterJSON(t)

	output, runError := executeCommand("cluster", "exec", "web-1", "-n", "default", "-c", "app", "-o", "json", "--", "check")
	if code := exitCodeFor(runError); code != 3 {
		t.Fatalf("exit code = %d (%v), want 3", code, runError)
	}
	var result podExecResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("stdout must be exactly one JSON document, got %q: %v", output, decodeError)
	}
	expected := podExecResult{ExitCode: 3, Stdout: "ok\n", Stderr: "warn\n"}
	if result != expected {
		t.Errorf("envelope = %+v, want %+v", result, expected)
	}
}

func TestClusterExecRequiresTheDashSeparator(t *testing.T) {
	setMockClient(t, &terminalMock{})
	resetExecFlags(t)
	writeSelectedClusterJSON(t)

	_, runError := executeCommand("cluster", "exec", "web-1", "ls", "-n", "default")
	if code := exitCodeFor(runError); code != exitUsage || !strings.Contains(runError.Error(), "after --") {
		t.Fatalf("got %d %v, want a usage error naming --", code, runError)
	}
}

func TestClusterExecReportsACommandThatNeverRan(t *testing.T) {
	terminal := newFakePodTerminal(nil,
		client.PodTerminalFrame{Type: "error", Message: `exec: "nope": executable file not found in $PATH`},
		client.PodTerminalFrame{Type: "end"})
	setMockClient(t, &terminalMock{terminal: terminal})
	resetExecFlags(t)
	writeSelectedClusterJSON(t)

	_, runError := executeCommand("cluster", "exec", "web-1", "-n", "default", "-c", "app", "--", "nope")
	if runError == nil || !strings.Contains(runError.Error(), "executable file not found") {
		t.Fatalf("got %v", runError)
	}
	if code := exitCodeFor(runError); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

func TestClusterExecForwardsStdinAndClosesIt(t *testing.T) {
	frames := make(chan client.PodTerminalFrame, 4)
	terminal := &fakePodTerminal{frames: frames}
	setMockClient(t, &terminalMock{terminal: terminal})
	resetExecFlags(t)
	writeSelectedClusterJSON(t)
	rootCmd.SetIn(bytes.NewBufferString("select 1;\n"))

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !terminal.isStdinClosed() {
			time.Sleep(10 * time.Millisecond)
		}
		frames <- exitFrame(0)
		close(frames)
	}()

	if _, runError := executeCommand("cluster", "exec", "db-0", "-n", "default", "-c", "db", "--stdin", "--", "psql"); runError != nil {
		t.Fatalf("unexpected error: %v", runError)
	}
	if terminal.typed() != "select 1;\n" {
		t.Errorf("forwarded input = %q", terminal.typed())
	}
	if !terminal.isStdinClosed() {
		t.Error("the end of the local input must close the command's stdin")
	}
}
