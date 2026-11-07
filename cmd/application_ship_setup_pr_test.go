package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// shipSetupMock adds the pull request's run and its findings to the journey.
type shipSetupMock struct {
	*shipPipelineMock
	findings      *client.PipelineFindingList
	findingsError error
}

func (mock *shipSetupMock) ListPipelineFindings(context.Context, client.PipelineSelector, string) (*client.PipelineFindingList, error) {
	return mock.findings, mock.findingsError
}

const shipTestSetupPullRequestNumber int64 = 1

func newShipSetupMock(t *testing.T, pullRequestRunDetail func(client.PipelineRun) *client.PipelineRunDetail) *shipSetupMock {
	t.Helper()
	fastShipPolling(t)
	mock := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 1, "push")
	number := shipTestSetupPullRequestNumber
	pullRequest := shipPipelineRun("run-pr", 2, shipPipelineTriggerPullRequest)
	pullRequest.PullRequestNumber = &number
	mock.runPages = [][]client.PipelineRun{{pullRequest, push}}
	skipped := shipRunDetail(push, "concluded", "skipped")
	reason := "No trigger this pipeline declares matches this event."
	skipped.ErrorMessage = &reason
	mock.runDetails["run-push"] = []*client.PipelineRunDetail{skipped}
	mock.runDetails["run-pr"] = []*client.PipelineRunDetail{pullRequestRunDetail(pullRequest)}
	return &shipSetupMock{shipPipelineMock: mock}
}

func gateStep(attempt int16, outcome string) client.PipelineStep {
	step := shipStep(shipPipelineStepKindGate, attempt, outcome)
	step.Stage = "gate"
	step.StepKey = "gate"
	return step
}

func TestShipStopsWaitingWhenTheSetupPullRequestRunPasses(t *testing.T) {
	mock := newShipSetupMock(t, func(run client.PipelineRun) *client.PipelineRunDetail {
		return shipRunDetail(run, "concluded", "success")
	})
	_, progress, executeError := runApplicationShipCommand(t, mock, "--cluster", "production")
	if code := exitCodeFor(executeError); code != exitWaiting {
		t.Fatalf("exit code = %d (%v), want %d", code, executeError, exitWaiting)
	}
	for _, want := range []string{"waiting for the setup pull request", "run #2 passed", "ready to merge", "run ship again"} {
		if !strings.Contains(executeError.Error(), want) {
			t.Errorf("the stop must say %q: %v", want, executeError)
		}
	}
	if !strings.Contains(progress, "Waiting for the setup pull request: https://github.com/acme/shop/pull/1") {
		t.Errorf("progress must announce the wait: %s", progress)
	}
	if mock.deployCalls != 0 {
		t.Errorf("nothing may deploy without an image; calls = %d", mock.deployCalls)
	}
}

func TestShipReportsTheFindingWhenTheGateBlocksTheSetupPullRequest(t *testing.T) {
	mock := newShipSetupMock(t, func(run client.PipelineRun) *client.PipelineRunDetail {
		return shipRunDetail(run, "concluded", "failure", gateStep(1, "failure"))
	})
	cve := "CVE-2026-1234"
	mock.findings = &client.PipelineFindingList{Findings: []client.PipelineFinding{
		{Tool: client.PipelineFindingToolSBOM, Severity: client.PipelineFindingSeverityUnknown, Title: "bill of materials"},
		{Tool: client.PipelineFindingToolSemgrep, Severity: client.PipelineFindingSeverityMedium, RuleID: "rule-1", Path: "main.go"},
		{Tool: client.PipelineFindingToolTrivy, Severity: client.PipelineFindingSeverityCritical, CVEID: &cve,
			PackageName: "lodash", PackageVersion: "4.17.20", FixedVersion: "4.17.21"},
	}}
	stdout, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production", "-o", "json")
	if code := exitCodeFor(executeError); code != exitBlocked {
		t.Fatalf("exit code = %d (%v), want %d", code, executeError, exitBlocked)
	}
	if !strings.Contains(executeError.Error(), "CRITICAL CVE-2026-1234 in lodash 4.17.20 (fixed in 4.17.21)") {
		t.Errorf("the stop must name the finding: %v", executeError)
	}
	// The real root command silences usage; this bare one prints it after the
	// document, so only the first value is read.
	var document shipResult
	if decodeError := json.NewDecoder(strings.NewReader(stdout)).Decode(&document); decodeError != nil {
		t.Fatalf("stdout does not start with the document: %v\n%s", decodeError, stdout)
	}
	if document.State != shipStateSetupPullRequestBlocked || document.SetupPullRequestRun == nil ||
		len(document.BlockingFindings) != 2 || document.BlockingFindings[0].Identifier != cve {
		t.Errorf("document = %+v", document)
	}
}

func TestShipFailsWhenTheSetupPullRequestRunFailsOutsideTheGate(t *testing.T) {
	mock := newShipSetupMock(t, func(run client.PipelineRun) *client.PipelineRunDetail {
		return shipRunDetail(run, "concluded", "failure", gateStep(1, "failure"), gateStep(2, "success"))
	})
	stdout, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production", "-o", "json")
	if code := exitCodeFor(executeError); code != exitError {
		t.Fatalf("exit code = %d (%v), want %d: a gate that passed on retry did not block", code, executeError, exitError)
	}
	if !strings.Contains(executeError.Error(), "pipeline logs") {
		t.Errorf("the failure must say where to look: %v", executeError)
	}
	if !strings.Contains(stdout, shipStateSetupPullRequestFailed) {
		t.Errorf("document must carry the state: %s", stdout)
	}
}

func TestShipStopsWaitingWhenTheSetupPullRequestHasNoRun(t *testing.T) {
	fastShipPolling(t)
	mock := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 1, "push")
	mock.runPages = [][]client.PipelineRun{{push}}
	mock.runDetails["run-push"] = []*client.PipelineRunDetail{shipRunDetail(push, "concluded", "skipped")}

	stdout, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production", "-o", "json")
	if code := exitCodeFor(executeError); code != exitWaiting {
		t.Fatalf("exit code = %d (%v), want %d", code, executeError, exitWaiting)
	}
	if !strings.Contains(executeError.Error(), "no pipeline run yet") {
		t.Errorf("the stop must say the run is missing: %v", executeError)
	}
	if !strings.Contains(stdout, shipStateWaitingForSetupPullRequest) {
		t.Errorf("document must carry the state: %s", stdout)
	}
}

func TestShipSetupPullRequestNumberReadsOnlyAGitHubPullRequest(t *testing.T) {
	cases := map[string]int64{
		"https://github.com/acme/shop/pull/12":             12,
		"https://github.com/acme/shop/pull/12/":            12,
		"https://gitlab.com/acme/shop/-/merge_requests/12": 0,
		"https://bitbucket.org/acme/shop/pull-requests/12": 0,
		"https://github.com/acme/shop/pull/not-a-number":   0,
		"": 0,
	}
	for pullRequestURL, want := range cases {
		got, isGitHub := shipSetupPullRequestNumber(pullRequestURL)
		if got != want || isGitHub != (want != 0) {
			t.Errorf("%q: got %d, %v, want %d", pullRequestURL, got, isGitHub, want)
		}
	}
}

func TestShipWaitsWhenTheSetupPullRequestMergesDuringTheWait(t *testing.T) {
	mock := newShipSetupMock(t, func(run client.PipelineRun) *client.PipelineRunDetail {
		detail := shipRunDetail(run, "concluded", "cancelled")
		reason := shipPipelineCancelReasonPullRequestClosed
		detail.CancelReason = &reason
		return detail
	})
	stdout, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production", "-o", "json")
	if code := exitCodeFor(executeError); code != exitWaiting {
		t.Fatalf("exit code = %d (%v), want %d: a merged pull request is not a failed run", code, executeError, exitWaiting)
	}
	if !strings.Contains(executeError.Error(), "merged or closed while ship waited") {
		t.Errorf("the stop must say the pull request closed: %v", executeError)
	}
	if !strings.Contains(stdout, shipStateWaitingForSetupPullRequest) {
		t.Errorf("document must carry the waiting state: %s", stdout)
	}
}

func TestShipSaysTheFindingsAreUnknownWhenTheirReadFails(t *testing.T) {
	mock := newShipSetupMock(t, func(run client.PipelineRun) *client.PipelineRunDetail {
		return shipRunDetail(run, "concluded", "failure", gateStep(1, "failure"))
	})
	mock.findingsError = errors.New("unexpected status 503")
	_, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production")
	if code := exitCodeFor(executeError); code != exitBlocked {
		t.Fatalf("exit code = %d (%v), want %d", code, executeError, exitBlocked)
	}
	if !strings.Contains(executeError.Error(), "its findings could not be read") {
		t.Errorf("a failed findings read must not read as no findings: %v", executeError)
	}
}
