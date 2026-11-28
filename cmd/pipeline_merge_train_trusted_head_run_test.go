package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestPipelineTrainEnableSendsTrustedHeadRunOnlyWhenGiven(t *testing.T) {
	mock := &pipelineTrainMock{}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	if mock.settingsRequest.TrustedHeadRun != nil {
		t.Fatalf("an enable without --trusted-head-run keeps the current value, got %v",
			*mock.settingsRequest.TrustedHeadRun)
	}
	isOn := true
	mock.settingsAnswer = &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 2, TrustedHeadRun: &isOn}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--trusted-head-run", " On ",
		"--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("enable --trusted-head-run on: %v", runError)
	}
	if mock.settingsRequest.TrustedHeadRun == nil || !*mock.settingsRequest.TrustedHeadRun {
		t.Fatalf("settings request = %+v", mock.settingsRequest)
	}
	if !strings.Contains(output, "Trusted head runs: on") {
		t.Fatalf("output does not say the setting:\n%s", output)
	}
	isOff := false
	mock.settingsAnswer = &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 2, TrustedHeadRun: &isOff}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--trusted-head-run", "off",
		"--repository", testPipelineRepositoryID); runError != nil || mock.settingsRequest.TrustedHeadRun == nil ||
		*mock.settingsRequest.TrustedHeadRun {
		t.Fatalf("enable --trusted-head-run off sends false, got %v / %+v", runError, mock.settingsRequest)
	}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--trusted-head-run", "yes",
		"--repository", testPipelineRepositoryID); runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("a value other than on or off is a usage error, got %v", runError)
	}
	if command := newPipelineTrainSettingsCommand("disable", "off", false); command.Flags().Lookup(
		"trusted-head-run") != nil {
		t.Fatal("disable takes no --trusted-head-run")
	}
}

func TestPipelineTrainEnableSaysAnOlderPlatformDidNotStoreTrustedHeadRun(t *testing.T) {
	mock := &pipelineTrainMock{settingsAnswer: &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 1}}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--trusted-head-run", "on",
		"--repository", testPipelineRepositoryID)
	if runError == nil || !strings.Contains(runError.Error(), "did not store --trusted-head-run") {
		t.Fatalf("expected the unstored setting reported, got %v", runError)
	}
	if strings.Contains(output, "Trusted head runs") {
		t.Fatalf("a platform that sent no value is not described:\n%s", output)
	}
}

func TestPipelineTrainStatusLabelNamesACarPassedOnItsHeadsRun(t *testing.T) {
	reason := "head_run_reused"
	if label := pipelineTrainStatusLabel(client.PipelineMergeTrainEntry{Status: "passed", Reason: &reason}); label !=
		"passed (head_run_reused)" {
		t.Fatalf("a car passed on its head's run reads %q", label)
	}
	if label := pipelineTrainStatusLabel(client.PipelineMergeTrainEntry{Status: "passed"}); label != "passed" {
		t.Fatalf("a car passed on its own run reads %q", label)
	}
}
