package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type agentTolerationsMock struct {
	tolerations  *client.AgentTolerations
	getError     error
	replaceError error

	getCalls      int
	replaceCalls  int
	lastClusterID string
	lastReplaced  []client.AgentToleration
}

func (mock *agentTolerationsMock) GetAgentTolerations(requestContext context.Context,
	clusterID string) (*client.AgentTolerations, error) {
	mock.getCalls++
	mock.lastClusterID = clusterID
	if mock.getError != nil {
		return nil, mock.getError
	}
	return mock.tolerations, nil
}

func (mock *agentTolerationsMock) ReplaceAgentTolerations(requestContext context.Context, clusterID string,
	tolerations []client.AgentToleration) (*client.AgentTolerations, error) {
	mock.replaceCalls++
	mock.lastClusterID = clusterID
	mock.lastReplaced = tolerations
	if mock.replaceError != nil {
		return nil, mock.replaceError
	}
	return mock.tolerations, nil
}

// agentTolerationsCommands collects the `cluster agent tolerations` tree so a
// test can put its flags back: the commands are built once in init and shared
// by the whole package.
func agentTolerationsCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterAgentCmd.Commands() {
		if candidate.Name() == "tolerations" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster agent tolerations' command is not registered")
	return nil
}

func runAgentTolerationsCommand(t *testing.T, mock any, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, agentTolerationsCommands(t)...) })
	return executeCommand(append([]string{"cluster", "agent", "tolerations"}, arguments...)...)
}

const agentTolerationsAppliedMessage = "The agent is re-rendering its release with these tolerations."

func newAgentTolerations() *client.AgentTolerations {
	updatedAt := "2026-11-25T10:00:00Z"
	return &client.AgentTolerations{
		Tolerations: []client.AgentToleration{
			{Key: "purpose", Operator: "Equal", Value: "db", Effect: "NoSchedule"},
			{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"},
		},
		UpdatedAt:  &updatedAt,
		ApplyState: client.AgentTolerationsApplyStateApplied,
		Message:    agentTolerationsAppliedMessage,
	}
}

func TestClusterAgentTolerationsGetPrintsTheListAndTheMessage(t *testing.T) {
	mock := &agentTolerationsMock{tolerations: newAgentTolerations()}

	output, runError := runAgentTolerationsCommand(t, mock, "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if mock.getCalls != 1 || mock.lastClusterID != testClusterID {
		t.Fatalf("expected one read of the selected cluster, got %d for %q", mock.getCalls, mock.lastClusterID)
	}
	for _, want := range []string{
		"Agent tolerations for cluster 'test-cluster':\n\n",
		"  Tolerations:     purpose=db:NoSchedule\n                   dedicated:NoExecute\n",
		"  Apply state:     applied\n",
		"\n" + agentTolerationsAppliedMessage + "\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterAgentTolerationsGetNamesAnEmptyList(t *testing.T) {
	mock := &agentTolerationsMock{tolerations: &client.AgentTolerations{
		ApplyState: client.AgentTolerationsApplyStateNotSet,
		Message:    "Ankra holds no extra tolerations for this agent.",
	}}

	output, runError := runAgentTolerationsCommand(t, mock, "get")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	for _, want := range []string{
		"  Tolerations:     none\n",
		"  Last changed:    never\n",
		"  Apply state:     not_set\n",
		"\nAnkra holds no extra tolerations for this agent.\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterAgentTolerationsStructuredOutputStaysParseable(t *testing.T) {
	mock := &agentTolerationsMock{tolerations: newAgentTolerations()}
	for _, arguments := range [][]string{
		{"get", "-o", "json"},
		{"set", "--toleration", "purpose=db:NoSchedule", "-o", "json"},
		{"clear", "--yes", "-o", "json"},
	} {
		output, runError := runAgentTolerationsCommand(t, mock, arguments...)
		if runError != nil {
			t.Fatalf("%v: expected success, got %v", arguments, runError)
		}
		var decoded client.AgentTolerations
		if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
			t.Fatalf("%v: stdout is not parseable JSON (%v):\n%s", arguments, decodeError, output)
		}
		if !reflect.DeepEqual(&decoded, mock.tolerations) {
			t.Errorf("%v: decoded = %+v", arguments, decoded)
		}
		resetTreeFlags(t, agentTolerationsCommands(t)...)
	}
}

func TestClusterAgentTolerationsSetReplacesTheList(t *testing.T) {
	mock := &agentTolerationsMock{tolerations: newAgentTolerations()}

	output, runError := runAgentTolerationsCommand(t, mock, "set",
		"--toleration", "purpose=db:NoSchedule", "--toleration", "dedicated:NoExecute",
		"--toleration", "example.com/pool=:PreferNoSchedule")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := []client.AgentToleration{
		{Key: "purpose", Operator: "Equal", Value: "db", Effect: "NoSchedule"},
		{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"},
		{Key: "example.com/pool", Operator: "Equal", Effect: "PreferNoSchedule"},
	}
	if mock.replaceCalls != 1 || mock.lastClusterID != testClusterID || !reflect.DeepEqual(mock.lastReplaced, want) {
		t.Fatalf("sent %d call(s) for %q: %+v, want %+v", mock.replaceCalls, mock.lastClusterID, mock.lastReplaced, want)
	}
	for _, want := range []string{
		"Agent tolerations updated for cluster 'test-cluster'.\n\n",
		"purpose=db:NoSchedule",
		"\n" + agentTolerationsAppliedMessage + "\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// A set that names nothing would clear the stored list, so it is refused
// before anything is sent, as is a value that is not a taint.
func TestClusterAgentTolerationsSetRefusesBeforeSending(t *testing.T) {
	for name, arguments := range map[string][]string{
		"no toleration":    {"set"},
		"empty toleration": {"set", "--toleration", ""},
		"no effect":        {"set", "--toleration", "purpose=db"},
		"unknown effect":   {"set", "--toleration", "purpose=db:Sometimes"},
		"no key":           {"set", "--toleration", "=db:NoSchedule"},
		"only an effect":   {"set", "--toleration", ":NoSchedule"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &agentTolerationsMock{tolerations: newAgentTolerations()}
			_, runError := runAgentTolerationsCommand(t, mock, arguments...)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", runError)
			}
			if !strings.Contains(runError.Error(), "--toleration") {
				t.Errorf("error should name --toleration, got %v", runError)
			}
			if mock.replaceCalls != 0 {
				t.Errorf("expected nothing sent, got %d call(s)", mock.replaceCalls)
			}
		})
	}
}

func runAgentTolerationsClear(t *testing.T, mock any, interactive bool, input string,
	arguments ...string) (string, string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return interactive }
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "agent", "tolerations", "clear"}, arguments...))
	t.Cleanup(func() {
		promptIsInteractive = previous
		rootCmd.SetIn(nil)
		resetTreeFlags(t, agentTolerationsCommands(t)...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestClusterAgentTolerationsClearAsksFirst(t *testing.T) {
	cleared := &client.AgentTolerations{
		Tolerations: []client.AgentToleration{},
		ApplyState:  client.AgentTolerationsApplyStatePendingUpgrade,
		Message:     "Ankra no longer holds tolerations for this agent.",
	}

	t.Run("confirmed sends an empty list", func(t *testing.T) {
		mock := &agentTolerationsMock{tolerations: cleared, lastReplaced: newAgentTolerations().Tolerations}
		stdout, stderr, runError := runAgentTolerationsClear(t, mock, true, "y\n")
		if runError != nil {
			t.Fatalf("expected success, got %v", runError)
		}
		if mock.replaceCalls != 1 || mock.lastClusterID != testClusterID || len(mock.lastReplaced) != 0 {
			t.Errorf("sent %d call(s) for %q: %+v", mock.replaceCalls, mock.lastClusterID, mock.lastReplaced)
		}
		if !strings.Contains(stderr, "stays Pending") || !strings.Contains(stderr, "[y/N]") {
			t.Errorf("stderr should say what clearing can do and ask, got %q", stderr)
		}
		if !strings.Contains(stdout, "Agent tolerations cleared for cluster 'test-cluster'.") ||
			!strings.Contains(stdout, cleared.Message) {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("declined changes nothing", func(t *testing.T) {
		mock := &agentTolerationsMock{tolerations: cleared}
		_, _, runError := runAgentTolerationsClear(t, mock, true, "n\n")
		if !errors.Is(runError, errCancelled) || mock.replaceCalls != 0 {
			t.Errorf("error = %v after %d call(s), want a cancellation and nothing sent", runError, mock.replaceCalls)
		}
	})

	t.Run("no terminal needs --yes", func(t *testing.T) {
		mock := &agentTolerationsMock{tolerations: cleared}
		_, _, runError := runAgentTolerationsClear(t, mock, false, "y\n")
		if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--yes") {
			t.Errorf("error = %v, want a usage error naming --yes", runError)
		}
		if mock.replaceCalls != 0 {
			t.Errorf("expected nothing sent, got %d call(s)", mock.replaceCalls)
		}
	})

	t.Run("--yes asks nothing", func(t *testing.T) {
		mock := &agentTolerationsMock{tolerations: cleared}
		_, stderr, runError := runAgentTolerationsClear(t, mock, false, "", "--yes")
		if runError != nil || mock.replaceCalls != 1 || strings.Contains(stderr, "[y/N]") {
			t.Errorf("error = %v, calls = %d, stderr = %q", runError, mock.replaceCalls, stderr)
		}
	})
}

func TestClusterAgentTolerationsRefusalsReachTheUserUnchanged(t *testing.T) {
	t.Run("403 keeps the permission and exits 7", func(t *testing.T) {
		mock := &agentTolerationsMock{replaceError: &client.PermissionDeniedError{Permission: "agents.manage"}}
		_, runError := runAgentTolerationsCommand(t, mock, "set", "--toleration", "purpose=db:NoSchedule")
		if runError == nil || exitCodeFor(runError) != exitForbidden ||
			!strings.Contains(runError.Error(), `"agents.manage"`) {
			t.Errorf("error = %v, want the permission refusal", runError)
		}
	})

	t.Run("404 exits 3", func(t *testing.T) {
		mock := &agentTolerationsMock{getError: client.NewUnexpectedResponseError(404,
			"cluster not found in this organisation")}
		_, runError := runAgentTolerationsCommand(t, mock, "get")
		if runError == nil || exitCodeFor(runError) != exitNotFound ||
			runError.Error() != "cluster not found in this organisation" {
			t.Errorf("error = %v, want the server's wording and exit 3", runError)
		}
	})

	t.Run("a clear the platform refuses is not reported as done", func(t *testing.T) {
		mock := &agentTolerationsMock{replaceError: errors.New("This cluster has no registered agent")}
		stdout, _, runError := runAgentTolerationsClear(t, mock, false, "", "--yes")
		if runError == nil || runError.Error() != "This cluster has no registered agent" ||
			strings.Contains(stdout, "cleared") {
			t.Errorf("error = %v, stdout = %q", runError, stdout)
		}
	})

	t.Run("an answer with no body is an error", func(t *testing.T) {
		_, runError := runAgentTolerationsCommand(t, &agentTolerationsMock{}, "get")
		if runError == nil || !strings.Contains(runError.Error(), "test-cluster") {
			t.Errorf("error = %v", runError)
		}
	})
}
