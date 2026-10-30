package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationTrackedBranchMock struct {
	baseMock
	applicationPayload json.RawMessage
	setPayload         json.RawMessage

	getCalls      int
	setCalls      int
	setRequest    client.SetApplicationTrackedBranchRequest
	applicationID string
}

func (mock *applicationTrackedBranchMock) GetApplicationRaw(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.getCalls++
	mock.applicationID = applicationID
	return mock.applicationPayload, nil
}

func (mock *applicationTrackedBranchMock) SetApplicationTrackedBranch(requestContext context.Context, applicationID string, branchRequest client.SetApplicationTrackedBranchRequest) (json.RawMessage, error) {
	mock.setCalls++
	mock.applicationID = applicationID
	mock.setRequest = branchRequest
	return mock.setPayload, nil
}

func TestApplicationTrackedBranchCommandsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() != "tracked-branch" {
			continue
		}
		for _, child := range subcommand.Commands() {
			registered[child.Name()] = true
		}
	}
	for _, expected := range []string{"get", "set"} {
		if !registered[expected] {
			t.Errorf("tracked-branch subcommand %q is not registered", expected)
		}
	}
}

func TestApplicationTrackedBranchGetReadsTheApplication(t *testing.T) {
	mockClient := &applicationTrackedBranchMock{
		applicationPayload: json.RawMessage(`{"id":"x","name":"shop","app_repo_branch":"main"}`),
	}
	output, executeError := runApplicationCommand(t, mockClient, "tracked-branch", "get", testApplicationID)
	if executeError != nil {
		t.Fatalf("tracked-branch get error = %v", executeError)
	}
	if mockClient.getCalls != 1 || mockClient.applicationID != testApplicationID {
		t.Errorf("get calls = %d for %q", mockClient.getCalls, mockClient.applicationID)
	}
	if !strings.Contains(output, "\"branch\": \"main\"") {
		t.Errorf("output does not name the branch: %q", output)
	}
}

func TestApplicationTrackedBranchSetSendsTheTrimmedBranch(t *testing.T) {
	mockClient := &applicationTrackedBranchMock{
		setPayload: json.RawMessage(`{"branch":"develop","previous_branch":"main"}`),
	}
	output, executeError := runApplicationCommand(t, mockClient, "branch", "set", testApplicationID, " develop ")
	if executeError != nil {
		t.Fatalf("tracked-branch set error = %v", executeError)
	}
	if mockClient.setCalls != 1 || mockClient.setRequest.Branch != "develop" || mockClient.applicationID != testApplicationID {
		t.Errorf("set calls = %d request = %+v application = %q", mockClient.setCalls, mockClient.setRequest, mockClient.applicationID)
	}
	if !strings.Contains(output, "\"previous_branch\": \"main\"") {
		t.Errorf("output does not carry the previous branch: %q", output)
	}
}

func TestApplicationTrackedBranchSetRefusesABlankBranch(t *testing.T) {
	mockClient := &applicationTrackedBranchMock{}
	_, executeError := runApplicationCommand(t, mockClient, "tracked-branch", "set", testApplicationID, "  ")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("a blank branch must be a usage error, got %v", executeError)
	}
	if mockClient.setCalls != 0 {
		t.Errorf("a blank branch must not reach the API, got %d calls", mockClient.setCalls)
	}
}
