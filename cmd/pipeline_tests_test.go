package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// pipelineTestsMock adds the three test result reads to pipelineLaneMock
// without touching its shared definition, the way pipelineFindingsMock does.
type pipelineTestsMock struct {
	pipelineLaneMock

	runTestsRunID   string
	runTestsSlowest int
	runTestsResult  *client.PipelineRunTests
	runTestsError   error

	historyTestKey string
	historyOptions client.PipelineTestHistoryOptions
	historyResult  *client.PipelineTestHistory

	timingsStage   string
	timingsOptions client.PipelineTestTimingsOptions
	timingsResult  *client.PipelineTestTimings
	timingsCalls   int
}

func (mock *pipelineTestsMock) GetPipelineRunTests(ctx context.Context, selector client.PipelineSelector,
	runID string, slowest int) (*client.PipelineRunTests, error) {
	mock.lastSelector = selector
	mock.runTestsRunID = runID
	mock.runTestsSlowest = slowest
	if mock.runTestsError != nil {
		return nil, mock.runTestsError
	}
	return mock.runTestsResult, nil
}

func (mock *pipelineTestsMock) GetPipelineTestHistory(ctx context.Context, selector client.PipelineSelector,
	testKey string, options client.PipelineTestHistoryOptions) (*client.PipelineTestHistory, error) {
	mock.lastSelector = selector
	mock.historyTestKey = testKey
	mock.historyOptions = options
	return mock.historyResult, nil
}

func (mock *pipelineTestsMock) GetPipelineTestTimings(ctx context.Context, selector client.PipelineSelector,
	stage string, options client.PipelineTestTimingsOptions) (*client.PipelineTestTimings, error) {
	mock.lastSelector = selector
	mock.timingsStage = stage
	mock.timingsOptions = options
	mock.timingsCalls++
	return mock.timingsResult, nil
}

func TestPipelineTestsRegistered(t *testing.T) {
	testsCommand := findSubcommandOrNil(newPipelineCommand(), "tests")
	if testsCommand == nil {
		t.Fatal("pipeline subcommand \"tests\" is not registered")
	}
	for _, name := range []string{"history", "timings"} {
		if findSubcommandOrNil(testsCommand, name) == nil {
			t.Errorf("pipeline tests subcommand %q is not registered", name)
		}
	}
}

func TestPipelineTestsRunSummaryTable(t *testing.T) {
	mockClient := &pipelineTestsMock{runTestsResult: &client.PipelineRunTests{
		RunID: "run-1", HasReports: true, DurationMS: 61_500,
		Counts: client.PipelineTestCounts{Total: 12, Passed: 9, Failed: 1, Skipped: 1, Flaky: 1},
		Reports: []client.PipelineTestReport{
			{Stage: "test", Format: "go-test", Status: "ingested", Counts: client.PipelineTestCounts{Total: 12}},
			{Stage: "e2e", Format: "playwright", Status: "not_uploaded",
				ErrorMessage: "the step did not write the report\nsecond line"},
		},
		Failed: []client.PipelineTestCase{{Suite: "orders", Name: "TestCheckout", File: "orders/checkout_test.go",
			Stage: "test", Outcome: "failed", DurationMS: 1200, Attempts: 1,
			FailureMessage: "expected 200, got 500\nstack trace"}},
		Flaky: []client.PipelineTestCase{{Name: "TestRetry", Stage: "test", Outcome: "flaky", Attempts: 2}},
		Slowest: []client.PipelineTestCase{{Name: "TestSlow", Stage: "test", DurationMS: 30_000, Attempts: 1,
			FailureMessage: "never shown for a slow test"}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("tests error = %v", executeError)
	}
	for _, expected := range []string{
		"12 tests: 9 passed, 1 failed, 1 flaky, 1 skipped in 1m1.5s",
		"not_uploaded", "the step did not write the report",
		"orders > TestCheckout", "expected 200, got 500",
		"Flaky", "TestRetry", "Slowest", "TestSlow", "30s",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
	for _, unexpected := range []string{"second line", "stack trace", "never shown for a slow test"} {
		if strings.Contains(output, unexpected) {
			t.Errorf("output carries %q, which the table must not:\n%s", unexpected, output)
		}
	}
	if mockClient.runTestsRunID != "run-1" || mockClient.runTestsSlowest != 10 {
		t.Errorf("read run %q slowest %d, want run-1 and the default 10",
			mockClient.runTestsRunID, mockClient.runTestsSlowest)
	}
}

// A run that declared no reports is not a passing run, and must not read as one.
func TestPipelineTestsRunWithNoReportsSaysSo(t *testing.T) {
	mockClient := &pipelineTestsMock{runTestsResult: &client.PipelineRunTests{RunID: "run-1"}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "run-1", "--application", testApplicationID,
		"--slowest", "0")
	if executeError != nil {
		t.Fatalf("tests error = %v", executeError)
	}
	if !strings.Contains(output, "declared no test reports") || strings.Contains(output, "passed") {
		t.Errorf("output = %q", output)
	}
	if mockClient.runTestsSlowest != 0 {
		t.Errorf("slowest = %d, want 0 passed through", mockClient.runTestsSlowest)
	}
}

func TestPipelineTestsStructuredOutputSkipsTheTable(t *testing.T) {
	mockClient := &pipelineTestsMock{runTestsResult: &client.PipelineRunTests{RunID: "run-1", HasReports: true,
		Failed: []client.PipelineTestCase{{TestKey: "go:orders/testcheckout", Name: "TestCheckout"}}}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "run-1", "--application", testApplicationID,
		"-o", "json")
	if executeError != nil {
		t.Fatalf("tests -o json error = %v", executeError)
	}
	if !strings.Contains(output, `"test_key": "go:orders/testcheckout"`) || strings.Contains(output, "Reports") {
		t.Errorf("output = %q, want the raw JSON shape rather than the table", output)
	}
}

func TestPipelineTestsRefusesASlowestOutsideTheBound(t *testing.T) {
	for _, slowest := range []string{"-1", "51"} {
		mockClient := &pipelineTestsMock{}
		_, executeError := runPipelineCommand(t, mockClient, "tests", "run-1", "--application", testApplicationID,
			"--slowest", slowest)
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("--slowest %s: error = %v (exit %d), want a usage error", slowest, executeError,
				exitCodeFor(executeError))
		}
		if mockClient.runTestsRunID != "" {
			t.Errorf("--slowest %s: the server was asked anyway", slowest)
		}
	}
}

func TestPipelineTestsHistoryRefusesRunsOutsideTheBound(t *testing.T) {
	for _, runs := range []string{"-1", "101"} {
		mockClient := &pipelineTestsMock{}
		_, executeError := runPipelineCommand(t, mockClient, "tests", "history", "go:x/y",
			"--application", testApplicationID, "--runs", runs)
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("--runs %s: error = %v (exit %d), want a usage error", runs, executeError,
				exitCodeFor(executeError))
		}
		if mockClient.historyTestKey != "" {
			t.Errorf("--runs %s: the server was asked anyway", runs)
		}
	}
}

func TestPipelineTestsRunNotFoundKeepsTheSentinel(t *testing.T) {
	mockClient := &pipelineTestsMock{runTestsError: errors.New("Pipeline run not found")}
	_, executeError := runPipelineCommand(t, mockClient, "tests", "missing", "--application", testApplicationID)
	if executeError == nil || executeError.Error() != "Pipeline run not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

func TestPipelineTestsHistory(t *testing.T) {
	mockClient := &pipelineTestsMock{historyResult: &client.PipelineTestHistory{
		TestKey: "go:orders/testcheckout", Suite: "orders", Name: "TestCheckout", Branch: "main",
		Observed: 4, Failed: 1, Flaky: 1, FlakyRate: floatPointer(0.25), FailureRate: floatPointer(0.25),
		Entries: []client.PipelineTestHistoryEntry{{RunID: "run-9", RunNumber: 9, HeadSHA: strings.Repeat("a", 40),
			Outcome: "flaky", DurationMS: 900, Attempts: 2, RecordedAt: "2026-10-09T10:00:00Z"}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "history", "go:orders/testcheckout",
		"--application", testApplicationID, "--branch", "release", "--runs", "50")
	if executeError != nil {
		t.Fatalf("tests history error = %v", executeError)
	}
	if mockClient.historyTestKey != "go:orders/testcheckout" || mockClient.historyOptions.Branch != "release" ||
		mockClient.historyOptions.Runs != 50 {
		t.Errorf("asked for %q %+v", mockClient.historyTestKey, mockClient.historyOptions)
	}
	for _, expected := range []string{"orders > TestCheckout on main", "Flaky rate 25.0%", "#9",
		strings.Repeat("a", 12), "flaky"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
}

// A test that never ran on the branch has no flaky rate, which is not 0%.
func TestPipelineTestsHistoryOfATestThatNeverRanIsNotARateOfZero(t *testing.T) {
	mockClient := &pipelineTestsMock{historyResult: &client.PipelineTestHistory{TestKey: "go:x/y", Branch: "main"}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "history", "go:x/y",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("tests history error = %v", executeError)
	}
	if !strings.Contains(output, "has not run on the branch") || strings.Contains(output, "0.0%") {
		t.Errorf("output = %q", output)
	}
	if pipelineTestRate(nil) != "n/a" {
		t.Errorf("a nil rate renders %q, want n/a", pipelineTestRate(nil))
	}
}

func TestPipelineTestsTimingsByTest(t *testing.T) {
	suite, name, key := "orders", "TestCheckout", "go:orders/testcheckout"
	mockClient := &pipelineTestsMock{timingsResult: &client.PipelineTestTimings{
		Stage: "test", Branch: "main", GroupBy: "test", RunsSampled: 5,
		Entries: []client.PipelineTestTiming{{File: "orders/checkout_test.go", Suite: &suite, Name: &name,
			TestKey: &key, DurationMS: 2500, Samples: 5}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "timings", "--stage", "test",
		"--group-by", "test", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("tests timings error = %v", executeError)
	}
	if mockClient.timingsStage != "test" || mockClient.timingsOptions.GroupBy != "test" {
		t.Errorf("asked for %q %+v", mockClient.timingsStage, mockClient.timingsOptions)
	}
	for _, expected := range []string{`Stage "test" on main, by test, over 5 runs`, "orders > TestCheckout", "2.5s"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
}

// No samples is "fall back to an even split", never every test taking 0s.
func TestPipelineTestsTimingsWithNoSamplesSaysToSplitEvenly(t *testing.T) {
	mockClient := &pipelineTestsMock{timingsResult: &client.PipelineTestTimings{Stage: "test", Branch: "main",
		GroupBy: "file"}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "timings", "--stage", "test",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("tests timings error = %v", executeError)
	}
	if !strings.Contains(output, "No timings recorded yet") || mockClient.timingsOptions.GroupBy != "file" {
		t.Errorf("output = %q, group_by = %q", output, mockClient.timingsOptions.GroupBy)
	}
}

func TestPipelineTestsTimingsRefusesBadFlags(t *testing.T) {
	for _, arguments := range [][]string{
		{"tests", "timings", "--application", testApplicationID},
		{"tests", "timings", "--stage", "test", "--group-by", "suite", "--application", testApplicationID},
		{"tests", "timings", "--stage", "test", "--runs", "-3", "--application", testApplicationID},
		{"tests", "timings", "--stage", "test", "--runs", "101", "--application", testApplicationID},
	} {
		mockClient := &pipelineTestsMock{}
		_, executeError := runPipelineCommand(t, mockClient, arguments...)
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%v: error = %v (exit %d), want a usage error", arguments, executeError, exitCodeFor(executeError))
		}
		if mockClient.timingsCalls != 0 {
			t.Errorf("%v: the server was asked anyway", arguments)
		}
	}
}
