package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func unapprovedRunDetail(run client.PipelineRun, status string, outcome string, steps ...client.PipelineStep) *client.PipelineRunDetail {
	detail := shipRunDetail(run, status, outcome, steps...)
	state := pipelineAuthorityUnapproved
	definition := "def-current"
	detail.AuthorityState = &state
	detail.ApproveDefinitionID = &definition
	detail.AuthorityOverrides = []client.PipelineAuthorityOverride{
		{Section: "resources", Path: "stages[build].resources"},
		{Section: "permissions", Path: "permissions"},
	}
	return detail
}

func TestPipelineGetSaysWhatUnapprovedChanges(t *testing.T) {
	detail := unapprovedRunDetail(client.PipelineRun{ID: "run-1", RunNumber: 6, Trigger: "push",
		TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("a", 40), QueuedAt: "2026-09-01T00:00:00Z"},
		"concluded", "success")
	output, executeError := runPipelineCommand(t, &pipelineLaneMock{getResult: detail}, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	for _, want := range []string{
		"Authority: unapproved",
		"taken from trusted authority instead of the definition: stages[build].resources, permissions",
		"does not stop an image build or publish",
		"ankra pipeline definitions approve def-current",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q: %s", want, output)
		}
	}
}

func TestPipelineGetOnAnotherHeadMakesNoClaimAboutPublishing(t *testing.T) {
	state := "changed_on_head"
	detail := &client.PipelineRunDetail{PipelineRun: client.PipelineRun{ID: "run-1", RunNumber: 9, Status: "concluded",
		Trigger: "pull_request", TriggerRef: "refs/heads/feature", HeadSHA: strings.Repeat("a", 40),
		QueuedAt: "2026-09-01T00:00:00Z", AuthorityState: &state}}
	output, executeError := runPipelineCommand(t, &pipelineLaneMock{getResult: detail}, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	if strings.Contains(output, "does not stop an image build") {
		t.Errorf("the sentence is about unapproved runs: %s", output)
	}
}

func TestShipNamesTheApproveCommandBeforeThePublish(t *testing.T) {
	fastShipPolling(t)
	mock := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 3, "push")
	mock.runPages = [][]client.PipelineRun{{push}}
	mock.runDetails["run-push"] = []*client.PipelineRunDetail{
		unapprovedRunDetail(push, "running", ""),
		unapprovedRunDetail(push, "running", "", shipStep(shipPipelineStepKindPublish, 1, pipelineOutcomeSuccess)),
	}

	_, progress, _ := runApplicationShipCommand(t, mock, "--cluster", "production")

	note := "runs without approved authority"
	if count := strings.Count(progress, note); count != 1 {
		t.Errorf("the note must appear once, got %d: %s", count, progress)
	}
	notedAt := strings.Index(progress, note)
	builtAt := strings.Index(progress, "Ankra Pipelines built the image")
	if notedAt < 0 || builtAt < 0 || notedAt > builtAt {
		t.Errorf("the note must come before the publish: %s", progress)
	}
	if !strings.Contains(progress, "ankra pipeline definitions approve def-current") ||
		!strings.Contains(progress, "stages[build].resources, permissions") {
		t.Errorf("the note must name the command and the sections: %s", progress)
	}
}

func TestShipFailedRunNamesTheApproveCommand(t *testing.T) {
	fastShipPolling(t)
	mock := newShipPipelineMock("ankra_pipeline")
	push := shipPipelineRun("run-push", 3, "push")
	mock.runPages = [][]client.PipelineRun{{push}}
	mock.runDetails["run-push"] = []*client.PipelineRunDetail{unapprovedRunDetail(push, "concluded", pipelineOutcomeFailure)}

	_, _, executeError := runApplicationShipCommand(t, mock, "--cluster", "production")
	if executeError == nil || !strings.Contains(executeError.Error(), "ankra pipeline definitions approve def-current") {
		t.Errorf("a failed unapproved run must name the approve command: %v", executeError)
	}
}

func TestShipSaysNothingAboutAuthorityOnAnApprovedRun(t *testing.T) {
	state := "approved"
	detail := shipRunDetail(shipPipelineRun("run", 1, "push"), "running", "")
	detail.AuthorityState = &state
	if note := shipPipelineAuthorityNote(detail); note != "" {
		t.Errorf("approved run got a note: %q", note)
	}
	if clause := shipPipelineAuthorityClause(detail); clause != "" {
		t.Errorf("approved run got a clause: %q", clause)
	}
}

func TestShipSaysNothingWhenThePlatformNamesNoDefinitionToApprove(t *testing.T) {
	state := pipelineAuthorityUnapproved
	detail := shipRunDetail(shipPipelineRun("run", 1, "push"), "running", "")
	detail.AuthorityState = &state
	if note := shipPipelineAuthorityNote(detail); note != "" {
		t.Errorf("a note with no command to run is not actionable: %q", note)
	}
}

func TestShipClauseNamesNothingWhenEveryOverridePathIsBlank(t *testing.T) {
	detail := unapprovedRunDetail(shipPipelineRun("run", 1, "push"), "concluded", "failed")
	detail.AuthorityOverrides = []client.PipelineAuthorityOverride{{Section: "resources", Path: "  "}}
	if clause := shipPipelineAuthorityClause(detail); clause != "" {
		t.Errorf("blank override paths got a clause: %q", clause)
	}
}
