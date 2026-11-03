package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"
)

const environmentsAutoDeployPayload = `{
  "enabled": true,
  "branch": "main",
  "environments": [
    {"id": "env-production", "name": "production", "role": "production", "enabled": true, "inherited": false, "branches": ["main"]},
    {"id": "env-development", "name": "development", "role": "development", "enabled": true, "inherited": true, "branches": null},
    {"id": "env-staging", "name": "staging", "role": "staging", "enabled": false, "inherited": false, "branches": ["release/*"]}
  ]
}`

type applicationEnvironmentsMock struct {
	baseMock
	getPayload    json.RawMessage
	updatePayload json.RawMessage
	updateFail    error

	updateCalls   int
	updateRequest client.SetApplicationAutoDeployRequest
}

func (mock *applicationEnvironmentsMock) GetApplicationAutoDeploy(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return mock.getPayload, nil
}

func (mock *applicationEnvironmentsMock) UpdateApplicationAutoDeploy(requestContext context.Context, applicationID string, autoDeployRequest client.SetApplicationAutoDeployRequest) (json.RawMessage, error) {
	mock.updateCalls++
	mock.updateRequest = autoDeployRequest
	if mock.updateFail != nil {
		return nil, mock.updateFail
	}
	if mock.updatePayload != nil {
		return mock.updatePayload, nil
	}
	return mock.getPayload, nil
}

func TestApplicationEnvironmentsListsWhatEachEnvironmentFollows(t *testing.T) {
	mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
	output, executeError := runApplicationCommand(t, mockClient, "environments", testApplicationID)
	if executeError != nil {
		t.Fatalf("environments error = %v", executeError)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 4 {
		t.Fatalf("output has %d lines, want 4: %q", len(lines), output)
	}
	for _, expected := range []string{"NAME", "ROLE", "FOLLOWS", "ENVIRONMENT ID"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("header %q lacks %q", lines[0], expected)
		}
	}
	expectedRows := map[int][]string{
		1: {"production", "main", "env-production"},
		2: {"development", "main (tracked)", "env-development"},
		3: {"staging", "manual", "env-staging"},
	}
	for lineIndex, fragments := range expectedRows {
		for _, fragment := range fragments {
			if !strings.Contains(lines[lineIndex], fragment) {
				t.Errorf("row %q lacks %q", lines[lineIndex], fragment)
			}
		}
	}
}

func TestApplicationEnvironmentsJSONPrintsTheEnvironmentsArray(t *testing.T) {
	mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
	output, executeError := runApplicationCommand(t, mockClient, "environments", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("environments error = %v", executeError)
	}
	var environments []map[string]any
	if decodeError := json.Unmarshal([]byte(output), &environments); decodeError != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", decodeError, output)
	}
	if len(environments) != 3 || environments[1]["branches"] != nil {
		t.Errorf("environments = %v", environments)
	}
}

func TestApplicationEnvironmentsSetSendsTheRuleForTheNamedEnvironment(t *testing.T) {
	mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
	_, executeError := runApplicationCommand(t, mockClient,
		"environments", "set", testApplicationID, "Development", "--branches", "**, !main")
	if executeError != nil {
		t.Fatalf("environments set error = %v", executeError)
	}
	if mockClient.updateCalls != 1 {
		t.Fatalf("UpdateApplicationAutoDeploy calls = %d, want 1", mockClient.updateCalls)
	}
	request := mockClient.updateRequest
	if request.EnvironmentID != "env-development" || !request.Enabled {
		t.Errorf("request = %+v", request)
	}
	if request.Branches == nil || !reflect.DeepEqual(*request.Branches, []string{"**", "!main"}) {
		t.Errorf("branches = %v", request.Branches)
	}
}

func TestApplicationEnvironmentsSetKeepsADisabledEnvironmentDisabled(t *testing.T) {
	mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
	_, executeError := runApplicationCommand(t, mockClient,
		"environments", "set", testApplicationID, "env-staging", "--branches", "release/*")
	if executeError != nil {
		t.Fatalf("environments set error = %v", executeError)
	}
	if mockClient.updateRequest.Enabled {
		t.Errorf("enabled = true, want the environment's current false")
	}
}

func TestApplicationEnvironmentsSetClearAndToggle(t *testing.T) {
	cases := map[string]struct {
		arguments       []string
		expectedEnabled bool
		expectedRule    *[]string
	}{
		"clear":          {[]string{"--clear"}, true, &[]string{}},
		"empty branches": {[]string{"--branches", ""}, true, &[]string{}},
		"disable":        {[]string{"--disable"}, false, nil},
		"enable":         {[]string{"--enable"}, true, nil},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
			arguments := append([]string{"environments", "set", testApplicationID, "production"}, testCase.arguments...)
			if _, executeError := runApplicationCommand(t, mockClient, arguments...); executeError != nil {
				t.Fatalf("environments set error = %v", executeError)
			}
			request := mockClient.updateRequest
			if request.Enabled != testCase.expectedEnabled {
				t.Errorf("enabled = %v, want %v", request.Enabled, testCase.expectedEnabled)
			}
			if !reflect.DeepEqual(request.Branches, testCase.expectedRule) {
				t.Errorf("branches = %v, want %v", request.Branches, testCase.expectedRule)
			}
		})
	}
}

func TestApplicationEnvironmentsSetRejectsInvalidFlagsLocally(t *testing.T) {
	cases := map[string][]string{
		"nothing to change":  {},
		"branches and clear": {"--branches", "main", "--clear"},
		"enable and disable": {"--enable", "--disable"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(environmentsAutoDeployPayload)}
			arguments := append([]string{"environments", "set", testApplicationID, "production"}, flags...)
			_, executeError := runApplicationCommand(t, mockClient, arguments...)
			if exitCodeFor(executeError) != exitUsage {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
			}
			if mockClient.updateCalls != 0 {
				t.Errorf("UpdateApplicationAutoDeploy calls = %d, want 0", mockClient.updateCalls)
			}
		})
	}
}

func TestApplicationEnvironmentsSetRefusesUnknownAndAmbiguousNames(t *testing.T) {
	ambiguousPayload := `{"enabled": true, "branch": "main", "environments": [
	  {"id": "env-a", "name": "preview", "enabled": true},
	  {"id": "env-b", "name": "Preview", "enabled": true}
	]}`
	cases := map[string]struct {
		payload          string
		environment      string
		expectedExitCode int
	}{
		"unknown":   {environmentsAutoDeployPayload, "qa", exitNotFound},
		"ambiguous": {ambiguousPayload, "preview", exitUsage},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			mockClient := &applicationEnvironmentsMock{getPayload: json.RawMessage(testCase.payload)}
			_, executeError := runApplicationCommand(t, mockClient,
				"environments", "set", testApplicationID, testCase.environment, "--branches", "main")
			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if mockClient.updateCalls != 0 {
				t.Errorf("UpdateApplicationAutoDeploy calls = %d, want 0", mockClient.updateCalls)
			}
		})
	}
}

func TestApplicationEnvironmentsSetSurfacesTheInvalidPatternSentence(t *testing.T) {
	sentence := `branch pattern "[" is not a valid glob`
	mockClient := &applicationEnvironmentsMock{
		getPayload: json.RawMessage(environmentsAutoDeployPayload),
		updateFail: client.NewUnexpectedResponseError(http.StatusUnprocessableEntity, sentence),
	}
	_, executeError := runApplicationCommand(t, mockClient,
		"environments", "set", testApplicationID, "production", "--branches", "[")
	if executeError == nil || !strings.Contains(executeError.Error(), sentence) {
		t.Fatalf("error = %v, want it to carry %q", executeError, sentence)
	}
}

func TestApplicationDeployMapsEnvironmentAndFollowBranches(t *testing.T) {
	mockClient := &applicationResourceMock{payload: json.RawMessage(`{"status":"queued"}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"deploy", testApplicationID, "--cluster", "cluster-1",
		"--environment", "production", "--follow-branches", "main,release/*")
	if executeError != nil {
		t.Fatalf("deploy error = %v", executeError)
	}
	request := mockClient.deployRequest
	if request.EnvironmentName != "production" || !reflect.DeepEqual(request.FollowBranches, []string{"main", "release/*"}) {
		t.Errorf("deploy request = %+v", request)
	}
}

func TestApplicationDeployRejectsFollowBranchesWithoutEnvironment(t *testing.T) {
	mockClient := &applicationResourceMock{}
	_, executeError := runApplicationCommand(t, mockClient,
		"deploy", testApplicationID, "--cluster", "cluster-1", "--follow-branches", "main")
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitUsage, executeError)
	}
	if mockClient.deployCalls != 0 {
		t.Errorf("DeployApplication calls = %d, want 0", mockClient.deployCalls)
	}
}
