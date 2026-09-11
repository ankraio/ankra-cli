package cmd

// The waiting half of the run lifecycle: `get --wait`, `--timeout`,
// `--exit-code`, and the rule every one of them rests on - a run's conclusion
// is reported through the exit code whatever stdout was asked to look like.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// shortenPipelineRunWaitPolling makes the poll loops turn without the tests
// waiting three seconds a lap.
func shortenPipelineRunWaitPolling(t *testing.T) {
	t.Helper()
	previous := pipelineRunWaitPollInterval
	pipelineRunWaitPollInterval = time.Millisecond
	t.Cleanup(func() { pipelineRunWaitPollInterval = previous })
}

// concludedPipelineRun is a settled run detail with the given outcome.
func concludedPipelineRun(runNumber int64, outcome string) client.PipelineRunDetail {
	return client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-1", RunNumber: runNumber, Status: "concluded",
		Outcome: &outcome, Trigger: "push", TriggerRef: "refs/heads/main",
		HeadSHA: strings.Repeat("a", 40), QueuedAt: "2026-09-01T00:00:00Z",
	}}
}

// runningPipelineRun is the same run before it settled.
func runningPipelineRun(runNumber int64, status string) client.PipelineRunDetail {
	return client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		ID: "run-1", RunNumber: runNumber, Status: status,
		Trigger: "push", TriggerRef: "refs/heads/main",
		HeadSHA: strings.Repeat("a", 40), QueuedAt: "2026-09-01T00:00:00Z",
	}}
}

// TestPipelineGetWaitBlocksUntilTheRunConcludes is the ticket's first ask: a
// run started by the push/pull_request webhook can only be addressed by
// `get`, so `get` is where waiting has to live.
func TestPipelineGetWaitBlocksUntilTheRunConcludes(t *testing.T) {
	shortenPipelineRunWaitPolling(t)
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		runningPipelineRun(44, "queued"),
		runningPipelineRun(44, "running"),
		concludedPipelineRun(44, "success"),
	}}
	output, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--wait")
	if executeError != nil {
		t.Fatalf("get --wait error = %v", executeError)
	}
	if mockClient.getCalls != 3 {
		t.Errorf("GetPipelineRun calls = %d, want 3 (polled until concluded)", mockClient.getCalls)
	}
	if !strings.Contains(output, "Run #44 is running.") {
		t.Errorf("each new status is announced, got %q", output)
	}
	if !strings.Contains(output, "Run #44") || !strings.Contains(output, "success") {
		t.Errorf("the final detail is printed, got %q", output)
	}
}

// TestPipelineGetWaitExitsNonZeroOnAFailedRun is ask 3 under --wait: a script
// that waited for a verdict must be able to branch on the exit status instead
// of parsing the outcome field back out of the payload.
func TestPipelineGetWaitExitsNonZeroOnAFailedRun(t *testing.T) {
	shortenPipelineRunWaitPolling(t)
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(concludedPipelineRun(44, "failure"))}
	_, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--wait")
	if executeError == nil {
		t.Fatal("a run that concluded failure must not exit 0 under --wait")
	}
	if !strings.Contains(executeError.Error(), "concluded failure") {
		t.Errorf("error = %v, want the outcome named", executeError)
	}
}

// TestPipelineWaitReportsTheConclusionUnderStructuredOutput pins the bug this
// change fixes on the waits that already existed: the structured branch
// returned encodeStructured's nil and never reached the conclusion, so
// `--wait -o json` - the one shape a CI script uses - printed a failed run and
// exited 0.
func TestPipelineWaitReportsTheConclusionUnderStructuredOutput(t *testing.T) {
	shortenPipelineRunWaitPolling(t)
	for _, invocation := range []struct {
		name      string
		arguments []string
	}{
		{"get", []string{"get", "run-1", "--application", testApplicationID, "--wait", "-o", "json"}},
		{"run", []string{"run", "--application", testApplicationID, "--sha", strings.Repeat("a", 40), "--wait", "-o", "json"}},
		{"rerun", []string{"rerun", "run-0", "--application", testApplicationID, "--wait", "-o", "json"}},
	} {
		t.Run(invocation.name, func(t *testing.T) {
			queued := &client.CreatePipelineRunResult{PipelineRunID: "run-1", RunNumber: 44}
			mockClient := &pipelineLaneMock{
				getResult:    pipelineRunDetailPtr(concludedPipelineRun(44, "failure")),
				createResult: queued,
				rerunResult:  queued,
			}
			output, executeError := runPipelineCommand(t, mockClient, invocation.arguments...)
			if executeError == nil {
				t.Fatal("a failed run under --wait -o json must not exit 0")
			}
			if exitCodeFor(executeError) != exitError {
				t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitError)
			}
			// The payload is still emitted: the exit code is an extra answer,
			// not a replacement for the one a script parses. The test harness
			// points stdout and stderr at one buffer, so the payload is read
			// as the first JSON value in it rather than as the whole buffer -
			// the progress lines and cobra's own error trailer are stderr.
			jsonStart := strings.Index(output, "{")
			if jsonStart < 0 {
				t.Fatalf("no JSON on stdout, got %q", output)
			}
			var decoded map[string]any
			if decodeError := json.NewDecoder(
				strings.NewReader(output[jsonStart:])).Decode(&decoded); decodeError != nil {
				t.Fatalf("stdout is not the run payload: %v (%q)", decodeError, output)
			}
			if decoded["outcome"] != "failure" {
				t.Errorf("payload outcome = %v, want failure", decoded["outcome"])
			}
		})
	}
}

// TestPipelineGetWithoutWaitStillExitsZero pins the compatibility floor: a
// bare `get` is a read, and every script that prints a run's detail today
// keeps working. The outcome reaches the exit code only when it is asked for.
func TestPipelineGetWithoutWaitStillExitsZero(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(concludedPipelineRun(44, "failure"))}
	if _, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID); executeError != nil {
		t.Fatalf("a bare get must stay exit 0 whatever it finds, got %v", executeError)
	}
}

// TestPipelineGetExitCodeReportsAConcludedOutcome is ask 3 without waiting:
// the run has already settled and the caller only wants the verdict.
func TestPipelineGetExitCodeReportsAConcludedOutcome(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(concludedPipelineRun(44, "failure"))}
	_, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--exit-code")
	if executeError == nil || !strings.Contains(executeError.Error(), "concluded failure") {
		t.Fatalf("--exit-code must report a failed outcome, got %v", executeError)
	}
	if mockClient.getCalls != 1 {
		t.Errorf("GetPipelineRun calls = %d, want 1 (--exit-code does not wait)", mockClient.getCalls)
	}

	succeeded := &pipelineLaneMock{getResult: pipelineRunDetailPtr(concludedPipelineRun(44, "success"))}
	if _, executeError := runPipelineCommand(t, succeeded,
		"get", "run-1", "--application", testApplicationID, "--exit-code"); executeError != nil {
		t.Fatalf("a successful run exits 0 under --exit-code, got %v", executeError)
	}
}

// TestPipelineGetExitCodeIsSilentOnARunStillGoing: "has not concluded" is not
// "did not succeed". Answering an unfinished run with a failure exit would
// make --exit-code a worse poll loop than the one it replaces.
func TestPipelineGetExitCodeIsSilentOnARunStillGoing(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(runningPipelineRun(44, "running"))}
	if _, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--exit-code"); executeError != nil {
		t.Fatalf("a run still going is not a failure, got %v", executeError)
	}
}

// TestPipelineWaitTimeoutExitsWithTheWaitCode gives the scripting contract's
// retryable answer to a run that outlives its budget, rather than a bare
// context error that reads like the platform refused.
func TestPipelineWaitTimeoutExitsWithTheWaitCode(t *testing.T) {
	shortenPipelineRunWaitPolling(t)
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(runningPipelineRun(44, "running"))}
	_, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--wait", "--timeout", "20ms")
	if executeError == nil {
		t.Fatal("an expired --timeout must not exit 0")
	}
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Errorf("exit code = %d, want %d (wait timeout)", exitCodeFor(executeError), exitWaitTimeout)
	}
	if !strings.Contains(executeError.Error(), "keeps running") {
		t.Errorf("error = %v, want it to say the run is still going", executeError)
	}
}

// TestPipelineWaitRefusesANegativeTimeout: a negative budget is a typo, and
// silently treating it as "no limit" would block a script that asked for the
// opposite.
func TestPipelineWaitRefusesANegativeTimeout(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: pipelineRunDetailPtr(concludedPipelineRun(44, "success"))}
	_, executeError := runPipelineCommand(t, mockClient,
		"get", "run-1", "--application", testApplicationID, "--wait", "--timeout", "-1m")
	if executeError == nil {
		t.Fatal("a negative --timeout must be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}

// TestPipelineWaitFlagsOnBothSurfaces: `ankra pipeline get` and
// `ankra application pipeline get` call the same runPipelineGet, so a flag
// registered on only one of them is a flag half the users cannot reach.
func TestPipelineWaitFlagsOnBothSurfaces(t *testing.T) {
	surfaces := map[string]map[string][]string{
		"pipeline": {
			"get":   {"wait", "timeout", "exit-code"},
			"run":   {"wait", "timeout"},
			"rerun": {"wait", "timeout"},
		},
		"application pipeline": {
			"get":   {"wait", "timeout", "exit-code"},
			"run":   {"wait", "timeout"},
			"rerun": {"wait", "timeout"},
		},
	}
	roots := map[string]func() *cobra.Command{
		"pipeline":             newPipelineCommand,
		"application pipeline": newApplicationPipelineCommand,
	}
	for surface, expected := range surfaces {
		root := roots[surface]()
		for _, subcommand := range root.Commands() {
			flags, wanted := expected[subcommand.Name()]
			if !wanted {
				continue
			}
			for _, flagName := range flags {
				if subcommand.Flags().Lookup(flagName) == nil {
					t.Errorf("%s %s is missing --%s", surface, subcommand.Name(), flagName)
				}
			}
		}
	}
}

func pipelineRunDetailPtr(detail client.PipelineRunDetail) *client.PipelineRunDetail {
	return &detail
}
