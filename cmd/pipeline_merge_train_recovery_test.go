package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestPipelineTrainEnableSendsInfrastructureRecoveryOnlyWhenGiven(t *testing.T) {
	mock := &pipelineTrainMock{}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	if mock.settingsRequest.InfrastructureRecovery != nil {
		t.Fatalf("an enable without --infrastructure-recovery keeps the current value, got %v",
			*mock.settingsRequest.InfrastructureRecovery)
	}
	isOn := true
	mock.settingsAnswer = &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 2, InfrastructureRecovery: &isOn}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--infrastructure-recovery", " On ",
		"--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("enable --infrastructure-recovery on: %v", runError)
	}
	if mock.settingsRequest.InfrastructureRecovery == nil || !*mock.settingsRequest.InfrastructureRecovery {
		t.Fatalf("settings request = %+v", mock.settingsRequest)
	}
	if !strings.Contains(output, "Infrastructure recovery: on") {
		t.Fatalf("output does not say the setting:\n%s", output)
	}
	isOff := false
	mock.settingsAnswer = &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 2, InfrastructureRecovery: &isOff}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--infrastructure-recovery", "off",
		"--repository", testPipelineRepositoryID); runError != nil || mock.settingsRequest.InfrastructureRecovery == nil ||
		*mock.settingsRequest.InfrastructureRecovery {
		t.Fatalf("enable --infrastructure-recovery off sends false, got %v / %+v", runError, mock.settingsRequest)
	}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--infrastructure-recovery", "yes",
		"--repository", testPipelineRepositoryID); runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("a value other than on or off is a usage error, got %v", runError)
	}
	if command := newPipelineTrainSettingsCommand("disable", "off", false); command.Flags().Lookup(
		"infrastructure-recovery") != nil {
		t.Fatal("disable takes no --infrastructure-recovery")
	}
}

func TestPipelineTrainEnableSaysAnOlderPlatformDidNotStoreInfrastructureRecovery(t *testing.T) {
	mock := &pipelineTrainMock{settingsAnswer: &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 1}}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--infrastructure-recovery", "on",
		"--repository", testPipelineRepositoryID)
	if runError == nil || !strings.Contains(runError.Error(), "did not store --infrastructure-recovery") {
		t.Fatalf("expected the unstored setting reported, got %v", runError)
	}
	if strings.Contains(output, "Infrastructure recovery") {
		t.Fatalf("a platform that sent no value is not described:\n%s", output)
	}
}
