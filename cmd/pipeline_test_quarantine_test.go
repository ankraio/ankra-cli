package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	quarantineCmdTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	quarantineCmdOtherID = "5d3f4a2b-1c0e-4f6a-9b8c-7d6e5f4a3b2c"
	quarantineCmdID      = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// pipelineTestQuarantineMock adds the quarantine routes to pipelineTestsMock.
type pipelineTestQuarantineMock struct {
	pipelineTestsMock

	view      *client.PipelineTestQuarantineView
	viewCalls int

	settingsUpdate *client.PipelineTestQuarantineSettingsUpdate
	settingsResult *client.PipelineTestQuarantineSettings

	candidatesLimit  int
	candidatesResult *client.PipelineTestQuarantineCandidates

	addRequest *client.QuarantinePipelineTestRequest
	addResult  *client.PipelineTestQuarantine
	addError   error

	releaseID     string
	releaseReason string
	releaseCalls  int
	releaseResult *client.PipelineTestQuarantine
}

func (mock *pipelineTestQuarantineMock) GetPipelineTestQuarantine(ctx context.Context,
	selector client.PipelineSelector) (*client.PipelineTestQuarantineView, error) {
	mock.lastSelector = selector
	mock.viewCalls++
	return mock.view, nil
}

func (mock *pipelineTestQuarantineMock) SetPipelineTestQuarantineSettings(ctx context.Context,
	selector client.PipelineSelector,
	update client.PipelineTestQuarantineSettingsUpdate) (*client.PipelineTestQuarantineSettings, error) {
	mock.lastSelector = selector
	mock.settingsUpdate = &update
	return mock.settingsResult, nil
}

func (mock *pipelineTestQuarantineMock) ListPipelineTestQuarantineCandidates(ctx context.Context,
	selector client.PipelineSelector, limit int) (*client.PipelineTestQuarantineCandidates, error) {
	mock.lastSelector = selector
	mock.candidatesLimit = limit
	return mock.candidatesResult, nil
}

func (mock *pipelineTestQuarantineMock) QuarantinePipelineTest(ctx context.Context,
	selector client.PipelineSelector,
	request client.QuarantinePipelineTestRequest) (*client.PipelineTestQuarantine, error) {
	mock.lastSelector = selector
	mock.addRequest = &request
	if mock.addError != nil {
		return nil, mock.addError
	}
	return mock.addResult, nil
}

func (mock *pipelineTestQuarantineMock) ReleasePipelineTestQuarantine(ctx context.Context,
	selector client.PipelineSelector, quarantineID string, reason string) (*client.PipelineTestQuarantine, error) {
	mock.lastSelector = selector
	mock.releaseID = quarantineID
	mock.releaseReason = reason
	mock.releaseCalls++
	return mock.releaseResult, nil
}

func quarantineSettingsFixture(isEnabled bool) client.PipelineTestQuarantineSettings {
	return client.PipelineTestQuarantineSettings{Enabled: isEnabled, MinFlakyRate: 0.1, MinSamples: 20,
		ReleaseAfterPasses: 10}
}

func quarantineFixture(id string, testKey string) client.PipelineTestQuarantine {
	suite, file, by := "orders", "orders/checkout_test.go", "dev@example.com"
	return client.PipelineTestQuarantine{ID: id, TestKey: testKey, Suite: &suite, Name: "TestCheckout", File: &file,
		Reason: "times out", Source: "manual", FlakyRate: 0.25, Samples: 40, CreatedByEmail: &by,
		CreatedAt: "2026-10-01T10:00:00Z", ConsecutivePasses: 3}
}

func quarantineViewFixture(isEnabled bool) *client.PipelineTestQuarantineView {
	released := quarantineFixture(quarantineCmdOtherID, strings.Repeat("b", 64))
	released.Name = "TestOldFlake"
	releasedAt, by, reason := "2026-10-05T10:00:00Z", "system", client.PipelineTestQuarantineReleasedByPasses
	released.ReleasedAt, released.ReleasedByEmail, released.ReleaseReason = &releasedAt, &by, &reason
	released.ConsecutivePasses = 10
	return &client.PipelineTestQuarantineView{
		Settings:         quarantineSettingsFixture(isEnabled),
		Quarantines:      []client.PipelineTestQuarantine{quarantineFixture(quarantineCmdID, quarantineCmdTestKey)},
		RecentlyReleased: []client.PipelineTestQuarantine{released},
	}
}

func TestPipelineTestQuarantineRegistered(t *testing.T) {
	testsCommand := findSubcommandOrNil(newPipelineCommand(), "tests")
	if testsCommand == nil {
		t.Fatal("pipeline subcommand \"tests\" is not registered")
	}
	quarantineCommand := findSubcommandOrNil(testsCommand, "quarantine")
	if quarantineCommand == nil {
		t.Fatal("pipeline tests subcommand \"quarantine\" is not registered")
	}
	for _, name := range []string{"list", "candidates", "add", "release", "settings"} {
		if findSubcommandOrNil(quarantineCommand, name) == nil {
			t.Errorf("pipeline tests quarantine subcommand %q is not registered", name)
		}
	}
}

func TestPipelineTestQuarantineListShowsSettingsQuarantinesAndReleases(t *testing.T) {
	for _, arguments := range [][]string{{"tests", "quarantine"}, {"tests", "quarantine", "list"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			mockClient := &pipelineTestQuarantineMock{view: quarantineViewFixture(true)}
			output, executeError := runPipelineCommand(t, mockClient,
				append(arguments, "--application", testApplicationID)...)
			if executeError != nil {
				t.Fatalf("quarantine error = %v", executeError)
			}
			for _, expected := range []string{
				"Quarantine: on, flaky rate >= 10% over >= 20 default-branch runs, released after 10 passes in a row",
				"NAME", "PASSES SO FAR", "orders > TestCheckout", "orders/checkout_test.go", "25%", "40",
				"3 of 10", "dev@example.com", quarantineCmdID,
				"Released in the last 30 days", "orders > TestOldFlake", "passed 10 times in a row", "system",
			} {
				if !strings.Contains(output, expected) {
					t.Errorf("output lacks %q:\n%s", expected, output)
				}
			}
			if mockClient.lastSelector.ApplicationID != testApplicationID {
				t.Errorf("selector = %+v", mockClient.lastSelector)
			}
		})
	}
}

func TestPipelineTestQuarantineListOffAndEmpty(t *testing.T) {
	mockClient := &pipelineTestQuarantineMock{view: &client.PipelineTestQuarantineView{
		Settings: quarantineSettingsFixture(false)}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("quarantine error = %v", executeError)
	}
	for _, expected := range []string{
		"Quarantine: off (when on: flaky rate >= 10% over >= 20 default-branch runs, released after 10 passes in a row)",
		"settings --enable", "No tests are quarantined.", "quarantine candidates",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "Released in the last 30 days") || strings.Contains(output, "PASSES SO FAR") {
		t.Errorf("empty lists rendered tables:\n%s", output)
	}
}

func TestPipelineTestQuarantineListStructuredOutputIsTheBody(t *testing.T) {
	mockClient := &pipelineTestQuarantineMock{view: quarantineViewFixture(true)}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "--application",
		testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("quarantine -o json error = %v", executeError)
	}
	var decoded client.PipelineTestQuarantineView
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the JSON body (%v):\n%s", decodeError, output)
	}
	if len(decoded.Quarantines) != 1 || decoded.Quarantines[0].ID != quarantineCmdID || !decoded.Settings.Enabled {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestPipelineTestQuarantineCandidates(t *testing.T) {
	mockClient := &pipelineTestQuarantineMock{candidatesResult: &client.PipelineTestQuarantineCandidates{
		DefaultBranch: "main", Settings: quarantineSettingsFixture(false),
		Candidates: []client.PipelineTestQuarantineCandidate{{TestKey: quarantineCmdTestKey, Suite: "orders",
			Name: "TestCheckout", File: "orders/checkout_test.go", FlakyRate: 0.125, Flaky: 5, Observed: 40}},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "candidates",
		"--application", testApplicationID, "--limit", "5")
	if executeError != nil {
		t.Fatalf("candidates error = %v", executeError)
	}
	for _, expected := range []string{
		"at least 10% of at least 20 runs on main",
		"FLAKY/RAN", "TEST KEY", "orders > TestCheckout", "12.5%", "5/40", quarantineCmdTestKey,
		"ankra pipeline tests quarantine add <test key> --reason",
		"The quarantine is off for this repository",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
	if mockClient.candidatesLimit != 5 {
		t.Errorf("limit = %d, want 5", mockClient.candidatesLimit)
	}
}

func TestPipelineTestQuarantineCandidatesLimit(t *testing.T) {
	mockClient := &pipelineTestQuarantineMock{candidatesResult: &client.PipelineTestQuarantineCandidates{
		DefaultBranch: "main", Settings: quarantineSettingsFixture(true)}}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "candidates",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("candidates error = %v", executeError)
	}
	if mockClient.candidatesLimit != 0 || !strings.Contains(output, "No test qualifies for quarantine.") {
		t.Errorf("limit = %d, output:\n%s", mockClient.candidatesLimit, output)
	}
	for _, limit := range []string{"0", "101"} {
		_, executeError := runPipelineCommand(t, &pipelineTestQuarantineMock{}, "tests", "quarantine", "candidates",
			"--application", testApplicationID, "--limit", limit)
		if executeError == nil || exitCodeFor(executeError) != exitUsage {
			t.Errorf("--limit %s: error = %v, want a usage error", limit, executeError)
		}
	}
}

func TestPipelineTestQuarantineAdd(t *testing.T) {
	created := quarantineFixture(quarantineCmdID, quarantineCmdTestKey)
	mockClient := &pipelineTestQuarantineMock{addResult: &created}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "add",
		strings.ToUpper(quarantineCmdTestKey), "--reason", "  times out on the shared runner ",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("add error = %v", executeError)
	}
	if mockClient.addRequest == nil || mockClient.addRequest.TestKey != quarantineCmdTestKey ||
		mockClient.addRequest.Reason != "times out on the shared runner" {
		t.Fatalf("request = %+v, want the key lower-cased and the reason trimmed", mockClient.addRequest)
	}
	for _, expected := range []string{
		"Quarantined orders > TestCheckout (orders/checkout_test.go): flaky in 25% of 40 default-branch runs.",
		"ankra pipeline tests quarantine release " + quarantineCmdID,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestPipelineTestQuarantineAddRefusesBeforeSending(t *testing.T) {
	for name, arguments := range map[string][]string{
		"no reason":       {quarantineCmdTestKey},
		"blank reason":    {quarantineCmdTestKey, "--reason", "   "},
		"reason too long": {quarantineCmdTestKey, "--reason", strings.Repeat("é", 501)},
		"not a test key":  {"go:orders/TestCheckout", "--reason", "flaky"},
		"short test key":  {quarantineCmdTestKey[:63], "--reason", "flaky"},
	} {
		t.Run(name, func(t *testing.T) {
			mockClient := &pipelineTestQuarantineMock{}
			_, executeError := runPipelineCommand(t, mockClient, append(append([]string{"tests", "quarantine", "add"},
				arguments...), "--application", testApplicationID)...)
			if executeError == nil || exitCodeFor(executeError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", executeError)
			}
			if mockClient.addRequest != nil {
				t.Errorf("a refused add reached the platform: %+v", mockClient.addRequest)
			}
		})
	}
	// Exactly 500 characters is allowed.
	created := quarantineFixture(quarantineCmdID, quarantineCmdTestKey)
	mockClient := &pipelineTestQuarantineMock{addResult: &created}
	if _, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "add", quarantineCmdTestKey,
		"--reason", strings.Repeat("é", 500), "--application", testApplicationID); executeError != nil {
		t.Fatalf("a 500-character reason was refused: %v", executeError)
	}
}

// The platform's refusal is its own sentence, returned as it came: the
// command prints nothing on stdout and exits non-zero.
func TestPipelineTestQuarantineAddReturnsTheServersRefusal(t *testing.T) {
	refusal := fmt.Errorf("%s", "Test quarantine is off for this repository; turn it on first.")
	mockClient := &pipelineTestQuarantineMock{addError: refusal}
	output, _, executeError := runPipelineCommandSeparately(t, mockClient, "tests", "quarantine", "add",
		quarantineCmdTestKey, "--reason", "flaky", "--application", testApplicationID)
	if !errors.Is(executeError, refusal) || exitCodeFor(executeError) == 0 {
		t.Fatalf("error = %v, want the platform's refusal and a non-zero exit", executeError)
	}
	if output != "" {
		t.Errorf("a refused add printed on stdout:\n%s", output)
	}
}

func TestPipelineTestQuarantineReleaseByID(t *testing.T) {
	released := quarantineFixture(quarantineCmdID, quarantineCmdTestKey)
	mockClient := &pipelineTestQuarantineMock{releaseResult: &released}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "release", quarantineCmdID,
		"--reason", "fixed in #123", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("release error = %v", executeError)
	}
	if mockClient.releaseID != quarantineCmdID || mockClient.releaseReason != "fixed in #123" {
		t.Errorf("released %q with %q", mockClient.releaseID, mockClient.releaseReason)
	}
	if mockClient.viewCalls != 0 {
		t.Errorf("an id needs no lookup, read the quarantine %d times", mockClient.viewCalls)
	}
	if !strings.Contains(output, "Released the quarantine of orders > TestCheckout") {
		t.Errorf("output = %q", output)
	}
}

// A test key is resolved to its live quarantine through the list; recently
// released quarantines of the same test do not count.
func TestPipelineTestQuarantineReleaseByTestKey(t *testing.T) {
	view := quarantineViewFixture(true)
	view.RecentlyReleased[0].TestKey = quarantineCmdTestKey
	released := quarantineFixture(quarantineCmdID, quarantineCmdTestKey)
	mockClient := &pipelineTestQuarantineMock{view: view, releaseResult: &released}
	if _, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "release",
		strings.ToUpper(quarantineCmdTestKey), "--application", testApplicationID); executeError != nil {
		t.Fatalf("release error = %v", executeError)
	}
	if mockClient.releaseID != quarantineCmdID || mockClient.releaseReason != "" {
		t.Errorf("released %q with %q, want the live quarantine's id and no reason",
			mockClient.releaseID, mockClient.releaseReason)
	}
}

func TestPipelineTestQuarantineReleaseByTestKeyRefusals(t *testing.T) {
	t.Run("no live quarantine", func(t *testing.T) {
		mockClient := &pipelineTestQuarantineMock{view: quarantineViewFixture(true)}
		otherKey := strings.Repeat("c", 64)
		_, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "release", otherKey,
			"--application", testApplicationID)
		if executeError == nil || exitCodeFor(executeError) != exitNotFound {
			t.Fatalf("error = %v, want not found", executeError)
		}
		if !strings.Contains(executeError.Error(), otherKey) ||
			!strings.Contains(executeError.Error(), "among the repository's 1 quarantined tests") {
			t.Errorf("the error does not name what was searched: %v", executeError)
		}
		if mockClient.releaseCalls != 0 {
			t.Error("a release was sent without a match")
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		view := quarantineViewFixture(true)
		view.Quarantines = append(view.Quarantines, quarantineFixture(quarantineCmdOtherID, quarantineCmdTestKey))
		mockClient := &pipelineTestQuarantineMock{view: view}
		_, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "release", quarantineCmdTestKey,
			"--application", testApplicationID)
		if executeError == nil || !strings.Contains(executeError.Error(), quarantineCmdID) ||
			!strings.Contains(executeError.Error(), quarantineCmdOtherID) {
			t.Fatalf("error = %v, want both ids named", executeError)
		}
		if mockClient.releaseCalls != 0 {
			t.Error("an ambiguous key released something")
		}
	})
	t.Run("neither id nor key", func(t *testing.T) {
		mockClient := &pipelineTestQuarantineMock{}
		_, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "release", "TestCheckout",
			"--application", testApplicationID)
		if executeError == nil || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("error = %v, want a usage error", executeError)
		}
		if mockClient.viewCalls != 0 || mockClient.releaseCalls != 0 {
			t.Error("an unusable reference reached the platform")
		}
	})
}

func TestPipelineTestQuarantineSettingsSendsOnlyNamedFlags(t *testing.T) {
	stored := quarantineSettingsFixture(true)
	stored.MinFlakyRate = 0.2
	mockClient := &pipelineTestQuarantineMock{settingsResult: &stored}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "settings", "--enable",
		"--min-flaky-rate", "0.2", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("settings error = %v", executeError)
	}
	update := mockClient.settingsUpdate
	if update == nil || update.Enabled == nil || !*update.Enabled || update.MinFlakyRate == nil ||
		*update.MinFlakyRate != 0.2 || update.MinSamples != nil || update.ReleaseAfterPasses != nil {
		t.Fatalf("update = %+v, want enabled and min_flaky_rate only", update)
	}
	if !strings.Contains(output, "Quarantine: on, flaky rate >= 20% over >= 20 default-branch runs") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineTestQuarantineSettingsDisableAndThresholds(t *testing.T) {
	stored := quarantineSettingsFixture(false)
	mockClient := &pipelineTestQuarantineMock{settingsResult: &stored}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "settings", "--disable",
		"--min-samples", "30", "--release-after", "15", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("settings error = %v", executeError)
	}
	update := mockClient.settingsUpdate
	if update == nil || update.Enabled == nil || *update.Enabled || update.MinSamples == nil ||
		*update.MinSamples != 30 || update.ReleaseAfterPasses == nil || *update.ReleaseAfterPasses != 15 ||
		update.MinFlakyRate != nil {
		t.Fatalf("update = %+v", update)
	}
	if !strings.Contains(output, "Quarantine: off") {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineTestQuarantineSettingsWithNoFlagPrintsThem(t *testing.T) {
	mockClient := &pipelineTestQuarantineMock{view: quarantineViewFixture(true)}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "quarantine", "settings",
		"--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("settings error = %v", executeError)
	}
	if mockClient.settingsUpdate != nil {
		t.Errorf("a read wrote settings: %+v", mockClient.settingsUpdate)
	}
	if strings.TrimSpace(output) !=
		"Quarantine: on, flaky rate >= 10% over >= 20 default-branch runs, released after 10 passes in a row" {
		t.Errorf("output = %q", output)
	}
}

func TestPipelineTestQuarantineSettingsRefusesBeforeSending(t *testing.T) {
	for name, arguments := range map[string][]string{
		"enable and disable":  {"--enable", "--disable"},
		"rate zero":           {"--min-flaky-rate", "0"},
		"rate above one":      {"--min-flaky-rate", "10"},
		"samples too few":     {"--min-samples", "2"},
		"samples too many":    {"--min-samples", "1001"},
		"release after zero":  {"--release-after", "0"},
		"release after above": {"--release-after", "101"},
	} {
		t.Run(name, func(t *testing.T) {
			mockClient := &pipelineTestQuarantineMock{}
			_, executeError := runPipelineCommand(t, mockClient, append(append([]string{"tests", "quarantine",
				"settings"}, arguments...), "--application", testApplicationID)...)
			if executeError == nil || exitCodeFor(executeError) != exitUsage {
				t.Fatalf("error = %v, want a usage error", executeError)
			}
			if mockClient.settingsUpdate != nil || mockClient.viewCalls != 0 {
				t.Error("a refused settings change reached the platform")
			}
		})
	}
}

func quarantinedRunTestsFixture() *client.PipelineRunTests {
	return &client.PipelineRunTests{
		RunID: "run-1", HasReports: true, DurationMS: 2000,
		Counts:  client.PipelineTestCounts{Total: 4, Passed: 1, Failed: 3},
		Reports: []client.PipelineTestReport{{Stage: "test", Format: "go-test", Status: "ingested"}},
		Failed: []client.PipelineTestCase{
			{Suite: "orders", Name: "TestCheckout", Stage: "test", FailureMessage: "timeout"},
			{Suite: "billing", Name: "TestInvoice", Stage: "e2e", FailureMessage: "flaky socket"},
			{Suite: "billing", Name: "TestRefund", Stage: "e2e", FailureMessage: "real bug"},
		},
		Quarantined: []client.PipelineTestQuarantinedFailure{
			{TestKey: quarantineCmdTestKey, Suite: "orders", Name: "TestCheckout", File: "orders/checkout_test.go",
				Stage: "test", StepKey: "unit", FailureMessage: "timeout after 30s\nstack", DidRescueStep: true},
			{TestKey: strings.Repeat("b", 64), Suite: "billing", Name: "TestInvoice", File: "billing/invoice_test.go",
				Stage: "e2e", StepKey: "playwright", FailureMessage: "flaky socket", DidRescueStep: false},
		},
	}
}

func TestPipelineTestsRunSummaryListsQuarantinedFailures(t *testing.T) {
	mockClient := &pipelineTestsMock{runTestsResult: quarantinedRunTestsFixture()}
	output, executeError := runPipelineCommand(t, mockClient, "tests", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("tests error = %v", executeError)
	}
	rescuedHeading := "Quarantined failures (did not fail their step)"
	notRescuedHeading := "Quarantined failures (still failed the step: another test failed too)"
	for _, expected := range []string{
		rescuedHeading, notRescuedHeading, "STAGE / STEP", "test / unit", "e2e / playwright",
		"orders/checkout_test.go", "timeout after 30s", "billing > TestRefund",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "stack") {
		t.Errorf("a failure message's later lines reached the table:\n%s", output)
	}
	// Nothing is hidden: the quarantined tests stay in the failed list, which
	// comes first, and the rescued group sits under its own heading.
	failedAt := strings.Index(output, "\nFailed\n")
	rescuedAt := strings.Index(output, rescuedHeading)
	notRescuedAt := strings.Index(output, notRescuedHeading)
	if failedAt < 0 || rescuedAt < failedAt || notRescuedAt < rescuedAt {
		t.Errorf("sections out of order (failed %d, rescued %d, not rescued %d):\n%s",
			failedAt, rescuedAt, notRescuedAt, output)
	}
	rescuedSection := output[rescuedAt:notRescuedAt]
	if !strings.Contains(rescuedSection, "TestCheckout") || strings.Contains(rescuedSection, "TestInvoice") {
		t.Errorf("the rescued group holds the wrong tests:\n%s", rescuedSection)
	}
	if strings.Count(output, "orders > TestCheckout") != 2 {
		t.Errorf("a quarantined failure must appear in Failed and in its own section:\n%s", output)
	}
}

// An older platform sends no quarantined member: the summary prints exactly
// what it did before the quarantine existed, the same as a platform that
// sends an empty list.
func TestPipelineTestsRunSummaryFromAnOlderPlatformIsUnchanged(t *testing.T) {
	render := func(body string) string {
		var summary client.PipelineRunTests
		if decodeError := json.Unmarshal([]byte(body), &summary); decodeError != nil {
			t.Fatalf("decode: %v", decodeError)
		}
		output, executeError := runPipelineCommand(t, &pipelineTestsMock{runTestsResult: &summary},
			"tests", "run-1", "--application", testApplicationID)
		if executeError != nil {
			t.Fatalf("tests error = %v", executeError)
		}
		return output
	}
	const older = `{"run_id":"run-1","has_reports":true,"counts":{"total":2,"passed":1,"failed":1},
		"duration_ms":1500,"reports":[{"stage":"test","format":"go-test","status":"ingested",
		"counts":{"total":2}}],"failed":[{"name":"TestCheckout","stage":"test","failure_message":"boom"}],
		"flaky":[],"slowest":[{"name":"TestSlow","stage":"test","duration_ms":900}]}`
	olderOutput := render(older)
	newerOutput := render(strings.Replace(older, `"flaky":[]`, `"flaky":[],"quarantined":[]`, 1))
	if olderOutput != newerOutput {
		t.Errorf("an empty quarantined list changed the output:\n%s\nvs\n%s", olderOutput, newerOutput)
	}
	if strings.Contains(olderOutput, "Quarantined") || strings.Contains(olderOutput, "quarantine") {
		t.Errorf("an older platform's summary mentions the quarantine:\n%s", olderOutput)
	}
	var headings []string
	for _, line := range strings.Split(olderOutput, "\n") {
		switch line {
		case "Reports", "Failed", "Flaky", "Slowest":
			headings = append(headings, line)
		}
	}
	if strings.Join(headings, ",") != "Reports,Failed,Slowest" {
		t.Errorf("headings = %v, want Reports, Failed, Slowest", headings)
	}

	jsonOutput, executeError := runPipelineCommand(t, &pipelineTestsMock{runTestsResult: func() *client.PipelineRunTests {
		var summary client.PipelineRunTests
		_ = json.Unmarshal([]byte(older), &summary)
		return &summary
	}()}, "tests", "run-1", "--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("tests -o json error = %v", executeError)
	}
	if strings.Contains(jsonOutput, "quarantined") {
		t.Errorf("an older platform's -o json gained a quarantined member:\n%s", jsonOutput)
	}
}

// `ankra pipeline get` says which steps read success only because the
// quarantine forgave their failed tests, and says nothing for a run with
// none, which is every run on an older platform.
func TestPipelineRunDetailSaysWhichStepsTheQuarantineForgave(t *testing.T) {
	success := "success"
	two, one := 2, 1
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded", Outcome: &success},
		Steps: []client.PipelineStep{
			{StepKey: "unit", Status: "concluded", Outcome: &success, QuarantinedTestFailures: &two},
			{StepKey: "lint", Status: "concluded", Outcome: &success},
			{StepKey: "e2e", Status: "concluded", Outcome: &success, QuarantinedTestFailures: &one},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"Test quarantine:",
		"unit: success (2 quarantined tests failed; the quarantine forgave them)",
		"e2e: success (1 quarantined test failed; the quarantine forgave it)",
		"'ankra pipeline tests run-9' lists the quarantined failures.",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output lacks %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "lint: success (") {
		t.Errorf("a step the quarantine did not forgive is listed:\n%s", rendered)
	}

	output.Reset()
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded", Outcome: &success},
		Steps:       []client.PipelineStep{{StepKey: "unit", Status: "concluded", Outcome: &success}},
	}, client.PipelineSelector{})
	if strings.Contains(output.String(), "quarantine") {
		t.Errorf("a run with no forgiven step mentions the quarantine:\n%s", output.String())
	}
}
