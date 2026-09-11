package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// shortenPipelineRunWait makes the run wait's poll interval and its
// appear-wait bound testable: the production values are three seconds and
// ten minutes, and a test must exercise several polls without spending
// either.
//
// It writes package-level vars and restores them on cleanup, so a test that
// calls it must not call t.Parallel.
func shortenPipelineRunWait(t *testing.T, appearBound time.Duration) {
	t.Helper()
	previousInterval, previousBound := pipelineRunWaitPollInterval, pipelineRunAppearWaitBound
	pipelineRunWaitPollInterval = time.Millisecond
	pipelineRunAppearWaitBound = appearBound
	t.Cleanup(func() {
		pipelineRunWaitPollInterval = previousInterval
		pipelineRunAppearWaitBound = previousBound
	})
}

// runPipelineCommandSeparately is runPipelineCommand with stdout and stderr
// kept apart, for the tests that pin that stdout carries nothing but the
// command's own output. Usage is silenced as the root command silences it,
// so a refused invocation does not write its usage text onto stdout.
func runPipelineCommandSeparately(t *testing.T, mockClient APIClient, arguments ...string) (string, string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() { apiClient = previousClient })

	pipelineCommand := newPipelineCommand()
	pipelineCommand.SilenceUsage = true
	var standardOutput, standardError bytes.Buffer
	pipelineCommand.SetOut(&standardOutput)
	pipelineCommand.SetErr(&standardError)
	pipelineCommand.SetArgs(arguments)
	executeError := pipelineCommand.Execute()
	return standardOutput.String(), standardError.String(), executeError
}

// pipelineRunDetailFixture is run #44 in the given state, carrying steps.
func pipelineRunDetailFixture(status string, outcome *string, steps ...client.PipelineStep) client.PipelineRunDetail {
	detail := client.PipelineRunDetail{Steps: steps}
	detail.ID = "run-44"
	detail.RunNumber = 44
	detail.Status = status
	detail.Outcome = outcome
	detail.Trigger = "pull_request"
	detail.TriggerRef = "refs/heads/feature"
	detail.HeadSHA = strings.Repeat("a", 40)
	detail.QueuedAt = "2026-09-11T00:00:00Z"
	return detail
}

// pipelineStepFixture is one step attempt in the given state.
func pipelineStepFixture(id string, stepKey string, status string, outcome *string) client.PipelineStep {
	return client.PipelineStep{ID: id, StepKey: stepKey, Status: status, Outcome: outcome, Attempt: 1}
}

func TestPipelineGetWaitPrintsTheConcludedRunAndSucceeds(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		pipelineRunDetailFixture("queued", nil),
		pipelineRunDetailFixture("running", nil),
		pipelineRunDetailFixture("concluded", strPipelinePtr("success")),
	}}
	output, errorOutput, executeError := runPipelineCommandSeparately(t, mockClient,
		"get", "run-44", "--application", testApplicationID, "--wait")
	if executeError != nil {
		t.Fatalf("get --wait error = %v", executeError)
	}
	if mockClient.getCalls != 3 {
		t.Errorf("reads = %d, want the wait to read until the run concluded", mockClient.getCalls)
	}
	if !strings.Contains(errorOutput, "Run #44 is running.") {
		t.Errorf("stderr = %q, want the progress line", errorOutput)
	}
	if !strings.Contains(output, "Run #44 (run-44)") {
		t.Errorf("stdout = %q, want the concluded run's detail", output)
	}
}

// TestPipelineGetWaitExitsNonZeroWhenTheRunDidNotSucceedInEveryFormat pins
// the customer's first complaint: a run that concluded "failure" must not
// exit 0, and -o json must not be the way around that.
func TestPipelineGetWaitExitsNonZeroWhenTheRunDidNotSucceedInEveryFormat(t *testing.T) {
	for _, format := range []string{"", "json"} {
		t.Run("format="+format, func(t *testing.T) {
			shortenPipelineRunWait(t, time.Second)
			failed := pipelineRunDetailFixture("concluded", strPipelinePtr("failure"))
			failed.ErrorMessage = strPipelinePtr("step build failed")
			mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
				pipelineRunDetailFixture("running", nil), failed,
			}}
			arguments := []string{"get", "run-44", "--application", testApplicationID, "--wait"}
			if format != "" {
				arguments = append(arguments, "-o", format)
			}
			output, _, executeError := runPipelineCommandSeparately(t, mockClient, arguments...)
			if executeError == nil || exitCodeFor(executeError) != exitError {
				t.Fatalf("error = %v, want exit %d for a failed run", executeError, exitError)
			}
			if !strings.Contains(executeError.Error(), "run #44 concluded failure: step build failed") {
				t.Errorf("error = %q, want the outcome and the platform's message", executeError.Error())
			}
			if format == "json" {
				var decoded client.PipelineRunDetail
				if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil ||
					decoded.Outcome == nil || *decoded.Outcome != "failure" {
					t.Errorf("stdout = %q, want exactly the final detail document", output)
				}
			}
		})
	}
}

// TestPipelineRunWaitJSONExitsNonZeroWhenTheRunDidNotSucceed pins the fix to
// `run --wait -o json`, which returned straight after encoding and so exited
// 0 on a failed run.
func TestPipelineRunWaitJSONExitsNonZeroWhenTheRunDidNotSucceed(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	failed := pipelineRunDetailFixture("concluded", strPipelinePtr("failure"))
	mockClient := &pipelineLaneMock{
		createResult: &client.CreatePipelineRunResult{PipelineRunID: "run-44", RunNumber: 44},
		getResult:    &failed,
	}
	output, _, executeError := runPipelineCommandSeparately(t, mockClient, "run", "--application", testApplicationID,
		"--sha", strings.Repeat("b", 40), "--wait", "-o", "json")
	if executeError == nil || exitCodeFor(executeError) != exitError {
		t.Fatalf("error = %v, want exit %d for a failed run under --wait -o json", executeError, exitError)
	}
	if !strings.Contains(output, `"outcome": "failure"`) {
		t.Errorf("stdout = %q, want the final detail document", output)
	}
}

// TestPipelineGetWithoutAWaitStillExitsZeroOnAFailedRun pins the
// compatibility promise: a plain get reports a run, it does not judge it.
func TestPipelineGetWithoutAWaitStillExitsZeroOnAFailedRun(t *testing.T) {
	failed := pipelineRunDetailFixture("concluded", strPipelinePtr("failure"))
	mockClient := &pipelineLaneMock{getResult: &failed}
	_, executeError := runPipelineCommand(t, mockClient, "get", "run-44", "--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("a plain get must exit 0 whatever the outcome, error = %v", executeError)
	}
}

func TestPipelineGetExitCodeCarriesTheOutcome(t *testing.T) {
	testCases := []struct {
		name         string
		detail       client.PipelineRunDetail
		wantExitCode int
	}{
		{"succeeded", pipelineRunDetailFixture("concluded", strPipelinePtr("success")), exitOK},
		{"failed", pipelineRunDetailFixture("concluded", strPipelinePtr("failure")), exitError},
		{"cancelled", pipelineRunDetailFixture("concluded", strPipelinePtr("cancelled")), exitError},
		{"concluded with no outcome", pipelineRunDetailFixture("concluded", nil), exitError},
		{"still running", pipelineRunDetailFixture("running", nil), exitWaitTimeout},
		{"queued", pipelineRunDetailFixture("queued", nil), exitWaitTimeout},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			detail := testCase.detail
			mockClient := &pipelineLaneMock{getResult: &detail}
			_, executeError := runPipelineCommand(t, mockClient, "get", "run-44",
				"--application", testApplicationID, "--exit-code")
			if exitCodeFor(executeError) != testCase.wantExitCode {
				t.Errorf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), testCase.wantExitCode, executeError)
			}
			if mockClient.getCalls != 1 {
				t.Errorf("reads = %d, want --exit-code to read the run once and not wait", mockClient.getCalls)
			}
		})
	}
}

func TestPipelineGetWaitTimeoutExitsFive(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	running := pipelineRunDetailFixture("running", nil)
	mockClient := &pipelineLaneMock{getResult: &running}
	_, executeError := runPipelineCommand(t, mockClient, "get", "run-44", "--application", testApplicationID,
		"--wait", "--timeout", "30ms")
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitWaitTimeout, executeError)
	}
	if !strings.Contains(executeError.Error(), "run #44 had not concluded after 30ms (last seen running)") {
		t.Errorf("error = %q, want it to say how far the run got and that it is still going", executeError.Error())
	}
}

func TestPipelineTimeoutWithoutAWaitIsRefused(t *testing.T) {
	mockClient := &pipelineLaneMock{createResult: &client.CreatePipelineRunResult{PipelineRunID: "run-44"}}
	for _, arguments := range [][]string{
		{"get", "run-44", "--application", testApplicationID, "--timeout", "1m"},
		{"run", "--application", testApplicationID, "--sha", strings.Repeat("b", 40), "--timeout", "1m"},
		{"rerun", "run-44", "--application", testApplicationID, "--timeout", "1m"},
		{"get", "run-44", "--application", testApplicationID, "--wait", "--timeout=-1m"},
	} {
		_, executeError := runPipelineCommand(t, mockClient, arguments...)
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%v: exit code = %d, want %d (error %v)", arguments, exitCodeFor(executeError), exitUsage, executeError)
		}
	}
	if mockClient.createCalls != 0 {
		t.Errorf("dispatches = %d, want a refused --timeout refused before anything was dispatched", mockClient.createCalls)
	}
}

func TestPipelineGetSelectsTheNewestMatchingRun(t *testing.T) {
	succeeded := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	sha := strings.Repeat("c", 40)
	mockClient := &pipelineLaneMock{
		listResult: &client.PipelineRunList{Runs: []client.PipelineRun{succeeded.PipelineRun}},
		getResult:  &succeeded,
	}
	output, errorOutput, executeError := runPipelineCommandSeparately(t, mockClient, "get",
		"--application", testApplicationID, "--head-sha", sha, "--trigger", "pull_request", "--branch", "feature",
		"--latest")
	if executeError != nil {
		t.Fatalf("get --latest error = %v", executeError)
	}
	wantOptions := client.ListPipelineRunsOptions{HeadSHA: sha, Trigger: "pull_request", Branch: "feature", Limit: 1}
	if mockClient.listOptions != wantOptions {
		t.Errorf("list options = %+v, want %+v", mockClient.listOptions, wantOptions)
	}
	if mockClient.getRunID != "run-44" {
		t.Errorf("read run = %q, want the selected run", mockClient.getRunID)
	}
	if !strings.Contains(errorOutput, "Using run #44 (run-44), the newest run with head_sha "+sha) {
		t.Errorf("stderr = %q, want it to say which run was picked", errorOutput)
	}
	if strings.Contains(output, "Using run") {
		t.Errorf("stdout = %q, the pick belongs on stderr so stdout stays parseable", output)
	}
}

func TestPipelineGetRefusesToGuessBetweenSeveralMatches(t *testing.T) {
	newest := pipelineRunDetailFixture("concluded", strPipelinePtr("failure")).PipelineRun
	older := newest
	older.ID, older.RunNumber, older.Outcome = "run-43", 43, strPipelinePtr("success")
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{newest, older}}}
	_, executeError := runPipelineCommand(t, mockClient, "get", "--application", testApplicationID,
		"--head-sha", strings.Repeat("c", 40))
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitUsage, executeError)
	}
	for _, want := range []string{"#44 run-44", "#43 run-43", "--latest"} {
		if !strings.Contains(executeError.Error(), want) {
			t.Errorf("error = %q, want it to name %q", executeError.Error(), want)
		}
	}
	if mockClient.listOptions.Limit != pipelineRunSelectionProbeLimit {
		t.Errorf("list limit = %d, want %d", mockClient.listOptions.Limit, pipelineRunSelectionProbeLimit)
	}
	if mockClient.getCalls != 0 {
		t.Errorf("reads = %d, want neither run read", mockClient.getCalls)
	}
}

func TestPipelineGetSelectionThatMatchesNothingIsNotFound(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{}}}
	_, executeError := runPipelineCommand(t, mockClient, "get", "--application", testApplicationID,
		"--head-sha", strings.Repeat("c", 40), "--latest")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitNotFound, executeError)
	}
	if mockClient.listCalls != 1 {
		t.Errorf("listings = %d, want one read without --wait", mockClient.listCalls)
	}
}

// TestPipelineGetWaitWaitsForASelectedRunToAppear pins the CI case: a job
// asks for the run of the commit it just pushed, a moment before the webhook
// has created it.
func TestPipelineGetWaitWaitsForASelectedRunToAppear(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	succeeded := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	mockClient := &pipelineLaneMock{
		listResults: []client.PipelineRunList{
			{Runs: []client.PipelineRun{}},
			{Runs: []client.PipelineRun{}},
			{Runs: []client.PipelineRun{succeeded.PipelineRun}},
		},
		getResult: &succeeded,
	}
	_, errorOutput, executeError := runPipelineCommandSeparately(t, mockClient, "get",
		"--application", testApplicationID, "--head-sha", strings.Repeat("c", 40), "--trigger", "push", "--latest", "--wait")
	if executeError != nil {
		t.Fatalf("get --wait error = %v", executeError)
	}
	if mockClient.listCalls != 3 {
		t.Errorf("listings = %d, want the wait to list until the run appeared", mockClient.listCalls)
	}
	if strings.Count(errorOutput, "Waiting for a run with head_sha") != 1 {
		t.Errorf("stderr = %q, want the waiting line exactly once", errorOutput)
	}
	if mockClient.getRunID != "run-44" {
		t.Errorf("read run = %q, want the run that appeared", mockClient.getRunID)
	}
}

func TestPipelineGetWaitGivesUpOnARunThatNeverAppears(t *testing.T) {
	shortenPipelineRunWait(t, 20*time.Millisecond)
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{}}}
	_, executeError := runPipelineCommand(t, mockClient, "get", "--application", testApplicationID,
		"--head-sha", strings.Repeat("c", 40), "--latest", "--wait")
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitWaitTimeout, executeError)
	}
	if !strings.Contains(executeError.Error(), "appeared within 20ms") {
		t.Errorf("error = %q, want it to say how long it waited", executeError.Error())
	}
}

func TestPipelineGetTimeoutAlsoBoundsTheWaitForARunToAppear(t *testing.T) {
	shortenPipelineRunWait(t, time.Hour)
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{}}}
	_, executeError := runPipelineCommand(t, mockClient, "get", "--application", testApplicationID,
		"--head-sha", strings.Repeat("c", 40), "--latest", "--wait", "--timeout", "30ms")
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitWaitTimeout, executeError)
	}
	if !strings.Contains(executeError.Error(), "appeared within 30ms") {
		t.Errorf("error = %q, want the --timeout named as the wait", executeError.Error())
	}
}

// TestPipelineGetWaitRidesOutAFailedListingWhileARunAppears pins that the
// wait for a run to appear absorbs a platform blip the way the wait for its
// conclusion does (ankra-platform[bot] review on #292).
func TestPipelineGetWaitRidesOutAFailedListingWhileARunAppears(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	succeeded := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	mockClient := &pipelineLaneMock{
		listResults: []client.PipelineRunList{
			{Runs: []client.PipelineRun{}},
			{Runs: []client.PipelineRun{}},
			{Runs: []client.PipelineRun{succeeded.PipelineRun}},
		},
		listErrorsOnCall: map[int]error{2: errors.New("request failed: connection refused")},
		getResult:        &succeeded,
	}
	_, errorOutput, executeError := runPipelineCommandSeparately(t, mockClient, "get",
		"--application", testApplicationID, "--head-sha", strings.Repeat("c", 40), "--latest", "--wait")
	if executeError != nil {
		t.Fatalf("get --wait error = %v, want the failed listing ridden out", executeError)
	}
	if mockClient.listCalls != 3 {
		t.Errorf("listings = %d, want 3", mockClient.listCalls)
	}
	if !strings.Contains(errorOutput, "Could not list the pipeline's runs") {
		t.Errorf("stderr = %q, want the failed listing said", errorOutput)
	}
}

// TestPipelineGetWaitReportsAFailedFirstListingAtOnce pins the other half: a
// listing that fails before any has succeeded is most often a refused filter,
// which retrying for a minute would only delay.
func TestPipelineGetWaitReportsAFailedFirstListingAtOnce(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	mockClient := &pipelineLaneMock{listError: errors.New("head_sha must be a full commit sha")}
	_, executeError := runPipelineCommand(t, mockClient, "get", "--application", testApplicationID,
		"--head-sha", "abc123", "--latest", "--wait")
	if executeError == nil || executeError.Error() != "head_sha must be a full commit sha" {
		t.Fatalf("error = %v, want the platform's refusal verbatim", executeError)
	}
	if mockClient.listCalls != 1 {
		t.Errorf("listings = %d, want a first listing's failure reported without retrying", mockClient.listCalls)
	}
}

func TestPipelineGetNeedsARunOrASelectionButNotBoth(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	for _, arguments := range [][]string{
		{"get", "--application", testApplicationID},
		{"get", "run-44", "--application", testApplicationID, "--latest"},
		{"get", "run-44", "--application", testApplicationID, "--head-sha", strings.Repeat("c", 40)},
	} {
		_, executeError := runPipelineCommand(t, mockClient, arguments...)
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%v: exit code = %d, want %d (error %v)", arguments, exitCodeFor(executeError), exitUsage, executeError)
		}
	}
	if mockClient.getCalls != 0 || mockClient.listCalls != 0 {
		t.Errorf("reads = %d, listings = %d, want a refused invocation to call nothing",
			mockClient.getCalls, mockClient.listCalls)
	}
}

// TestPipelineGetWatchWritesOneJSONObjectPerStateChange pins the event
// stream: the first read's full state, then one line per change and nothing
// for a read that changed nothing, step events ahead of the run's own in each
// read, so the run's conclusion is the last line.
func TestPipelineGetWatchWritesOneJSONObjectPerStateChange(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	checkoutRunning := pipelineStepFixture("step-checkout", "checkout", "running", nil)
	checkoutSucceeded := pipelineStepFixture("step-checkout", "checkout", "concluded", strPipelinePtr("success"))
	buildRunning := pipelineStepFixture("step-build", "build", "running", nil)
	buildFailed := pipelineStepFixture("step-build", "build", "concluded", strPipelinePtr("failure"))
	buildExitCode := int32(1)
	buildFailed.ExitCode = &buildExitCode
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		pipelineRunDetailFixture("queued", nil),
		pipelineRunDetailFixture("running", nil, checkoutRunning),
		pipelineRunDetailFixture("running", nil, checkoutRunning),
		pipelineRunDetailFixture("running", nil, checkoutSucceeded, buildRunning),
		pipelineRunDetailFixture("concluded", strPipelinePtr("failure"), checkoutSucceeded, buildFailed),
	}}
	output, _, executeError := runPipelineCommandSeparately(t, mockClient, "get", "run-44",
		"--application", testApplicationID, "--watch", "-o", "json")
	if executeError == nil || exitCodeFor(executeError) != exitError {
		t.Fatalf("error = %v, want exit %d for a watched run that failed", executeError, exitError)
	}

	type observation struct {
		event, stepKey, status, previousStatus, outcome string
	}
	var events []pipelineRunWatchEvent
	var observed []observation
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var event pipelineRunWatchEvent
		if unmarshalError := json.Unmarshal([]byte(line), &event); unmarshalError != nil {
			t.Fatalf("stdout line %q is not one JSON object: %v", line, unmarshalError)
		}
		if event.ObservedAt == "" || event.PipelineRunID != "run-44" || event.RunNumber != 44 {
			t.Errorf("event = %+v, want every line to name its run and when it was seen", event)
		}
		entry := observation{event: event.Event, stepKey: event.StepKey, status: event.Status}
		if event.PreviousStatus != nil {
			entry.previousStatus = *event.PreviousStatus
		}
		if event.Outcome != nil {
			entry.outcome = *event.Outcome
		}
		events = append(events, event)
		observed = append(observed, entry)
	}
	want := []observation{
		{event: "run", status: "queued"},
		{event: "step", stepKey: "checkout", status: "running"},
		{event: "run", status: "running", previousStatus: "queued"},
		{event: "step", stepKey: "checkout", status: "concluded", previousStatus: "running", outcome: "success"},
		{event: "step", stepKey: "build", status: "running"},
		{event: "step", stepKey: "build", status: "concluded", previousStatus: "running", outcome: "failure"},
		{event: "run", status: "concluded", previousStatus: "running", outcome: "failure"},
	}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("events =\n%+v\nwant\n%+v", observed, want)
	}
	if events[5].Step == nil || events[5].Step.ExitCode == nil || *events[5].Step.ExitCode != 1 {
		t.Errorf("step event = %+v, want the whole step carried, exit code included", events[5])
	}
	if events[6].Run == nil || events[6].Step != nil {
		t.Errorf("run event = %+v, want the run carried and no step", events[6])
	}
}

// TestPipelineGetWatchReportsARetryAsANewAttempt pins that a retried step
// reads as the lost attempt concluding and a fresh attempt appearing, since
// the platform writes the retry as a new row with its own id.
func TestPipelineGetWatchReportsARetryAsANewAttempt(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	firstAttempt := pipelineStepFixture("step-build-1", "build", "running", nil)
	lostAttempt := pipelineStepFixture("step-build-1", "build", "concluded", strPipelinePtr("infra_error"))
	secondAttempt := pipelineStepFixture("step-build-2", "build", "concluded", strPipelinePtr("success"))
	secondAttempt.Attempt = 2
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		pipelineRunDetailFixture("running", nil, firstAttempt),
		pipelineRunDetailFixture("concluded", strPipelinePtr("success"), lostAttempt, secondAttempt),
	}}
	output, _, executeError := runPipelineCommandSeparately(t, mockClient, "get", "run-44",
		"--application", testApplicationID, "--watch")
	if executeError != nil {
		t.Fatalf("get --watch error = %v", executeError)
	}
	for _, want := range []string{"step build  ", "infra_error", "step build (attempt 2)", "success"} {
		if !strings.Contains(output, want) {
			t.Errorf("stdout = %q, want %q", output, want)
		}
	}
}

func TestPipelineGetWatchPrintsReadableLinesByDefault(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	buildFailed := pipelineStepFixture("step-build", "build", "concluded", strPipelinePtr("failure"))
	buildExitCode := int32(2)
	buildFailed.ExitCode = &buildExitCode
	buildFailed.ErrorMessage = strPipelinePtr("the build command exited 2")
	mockClient := &pipelineLaneMock{getResults: []client.PipelineRunDetail{
		pipelineRunDetailFixture("running", nil, pipelineStepFixture("step-build", "build", "running", nil)),
		pipelineRunDetailFixture("concluded", strPipelinePtr("failure"), buildFailed),
	}}
	output, _, executeError := runPipelineCommandSeparately(t, mockClient, "get", "run-44",
		"--application", testApplicationID, "--watch")
	if exitCodeFor(executeError) != exitError {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitError, executeError)
	}
	for _, want := range []string{"run #44", "step build", "failure", "exit 2", "the build command exited 2"} {
		if !strings.Contains(output, want) {
			t.Errorf("stdout = %q, want %q", output, want)
		}
	}
	if strings.Contains(output, "{") {
		t.Errorf("stdout = %q, want readable lines rather than JSON without -o json", output)
	}
	if strings.Contains(output, "\x1b[") {
		t.Errorf("stdout = %q, want no terminal escapes in lines that are as often a CI log", output)
	}
}

func TestPipelineGetWatchRefusesYAML(t *testing.T) {
	mockClient := &pipelineLaneMock{}
	_, executeError := runPipelineCommand(t, mockClient, "get", "run-44", "--application", testApplicationID,
		"--watch", "-o", "yaml")
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitUsage, executeError)
	}
	if mockClient.getCalls != 0 {
		t.Errorf("reads = %d, want the refusal before any read", mockClient.getCalls)
	}
}

// TestPipelineWaitRidesOutFailedReadsAfterTheRunWasRead pins that a platform
// restart partway through a long wait is not reported as the run failing.
func TestPipelineWaitRidesOutFailedReadsAfterTheRunWasRead(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	refused := errors.New("request failed: connection refused")
	mockClient := &pipelineLaneMock{
		getResults: []client.PipelineRunDetail{
			pipelineRunDetailFixture("running", nil),
			pipelineRunDetailFixture("running", nil),
			pipelineRunDetailFixture("running", nil),
			pipelineRunDetailFixture("concluded", strPipelinePtr("success")),
		},
		getErrorsOnCall: map[int]error{2: refused, 3: refused},
	}
	_, errorOutput, executeError := runPipelineCommandSeparately(t, mockClient, "get", "run-44",
		"--application", testApplicationID, "--wait")
	if executeError != nil {
		t.Fatalf("get --wait error = %v, want the failed reads ridden out", executeError)
	}
	if mockClient.getCalls != 4 {
		t.Errorf("reads = %d, want 4", mockClient.getCalls)
	}
	if strings.Count(errorOutput, "Could not read run #44") != 1 {
		t.Errorf("stderr = %q, want one line for the whole streak of failures", errorOutput)
	}
	if !strings.Contains(errorOutput, "Read run #44 again after 2 failed attempts.") {
		t.Errorf("stderr = %q, want the recovery said", errorOutput)
	}
}

func TestPipelineWaitGivesUpAfterTooManyFailedReadsInARow(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	failures := map[int]error{}
	for call := 2; call <= maxConsecutivePipelineRunReadFailures+2; call++ {
		failures[call] = errors.New("request failed: connection refused")
	}
	mockClient := &pipelineLaneMock{
		getResults:      []client.PipelineRunDetail{pipelineRunDetailFixture("running", nil)},
		getErrorsOnCall: failures,
	}
	_, executeError := runPipelineCommand(t, mockClient, "get", "run-44", "--application", testApplicationID, "--wait")
	if executeError == nil || !strings.Contains(executeError.Error(), "connection refused") {
		t.Fatalf("error = %v, want the last read's own error", executeError)
	}
	if mockClient.getCalls != maxConsecutivePipelineRunReadFailures+2 {
		t.Errorf("reads = %d, want %d", mockClient.getCalls, maxConsecutivePipelineRunReadFailures+2)
	}
}

func TestPipelineWaitDoesNotRideOutARefusedCredential(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	mockClient := &pipelineLaneMock{
		getResults:      []client.PipelineRunDetail{pipelineRunDetailFixture("running", nil)},
		getErrorsOnCall: map[int]error{2: client.ErrUnauthorized},
	}
	_, executeError := runPipelineCommand(t, mockClient, "get", "run-44", "--application", testApplicationID, "--wait")
	if exitCodeFor(executeError) != exitAuth {
		t.Fatalf("exit code = %d, want %d (error %v)", exitCodeFor(executeError), exitAuth, executeError)
	}
	if mockClient.getCalls != 2 {
		t.Errorf("reads = %d, want no retry after a refused credential", mockClient.getCalls)
	}
}

func TestPipelineWaitReportsAFailedFirstReadAtOnce(t *testing.T) {
	shortenPipelineRunWait(t, time.Second)
	mockClient := &pipelineLaneMock{getError: errors.New("Pipeline run not found")}
	_, executeError := runPipelineCommand(t, mockClient, "get", "missing-run", "--application", testApplicationID, "--wait")
	if executeError == nil || executeError.Error() != "Pipeline run not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
	if mockClient.getCalls != 1 {
		t.Errorf("reads = %d, want a first read's failure reported without retrying", mockClient.getCalls)
	}
}

// TestApplicationPipelineGetSelectsARunWithoutItsID pins that the
// by-application twin takes the same optional run and selection flags.
func TestApplicationPipelineGetSelectsARunWithoutItsID(t *testing.T) {
	succeeded := pipelineRunDetailFixture("concluded", strPipelinePtr("success"))
	mockClient := &pipelineLaneMock{
		listResult: &client.PipelineRunList{Runs: []client.PipelineRun{succeeded.PipelineRun}},
		getResult:  &succeeded,
	}
	_, executeError := runApplicationCommand(t, mockClient, "pipeline", "get", testApplicationID, "--latest", "--exit-code")
	if executeError != nil {
		t.Fatalf("application pipeline get --latest error = %v", executeError)
	}
	if mockClient.lastSelector.ApplicationID != testApplicationID {
		t.Errorf("selector = %+v", mockClient.lastSelector)
	}
	if mockClient.getRunID != "run-44" {
		t.Errorf("read run = %q, want the selected run", mockClient.getRunID)
	}
}

func TestPipelineWaitFlagsOnBothSurfaces(t *testing.T) {
	applicationPipelineCommand := findSubcommand(t, newApplicationCommand(), "pipeline")
	getSurfaces := map[string]*cobra.Command{
		"pipeline get":             findSubcommand(t, newPipelineCommand(), "get"),
		"application pipeline get": findSubcommand(t, applicationPipelineCommand, "get"),
	}
	for name, command := range getSurfaces {
		for _, flag := range []string{"wait", "watch", "exit-code", "timeout", "head-sha", "branch", "trigger", "latest"} {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%q does not register --%s", name, flag)
			}
		}
	}
	for _, verb := range []string{"run", "rerun"} {
		dispatchSurfaces := map[string]*cobra.Command{
			"pipeline " + verb:             findSubcommand(t, newPipelineCommand(), verb),
			"application pipeline " + verb: findSubcommand(t, applicationPipelineCommand, verb),
		}
		for name, command := range dispatchSurfaces {
			for _, flag := range []string{"wait", "timeout"} {
				if command.Flags().Lookup(flag) == nil {
					t.Errorf("%q does not register --%s", name, flag)
				}
			}
		}
	}
}
