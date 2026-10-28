package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type ankraCloudCredentialMock struct {
	baseMock
	createRequests []client.CreateAnkraCloudCredentialRequest
	createResult   client.CreateAnkraCloudCredentialResponse
	credentials    []client.Credential
}

func (mock *ankraCloudCredentialMock) CreateAnkraCloudCredential(request client.CreateAnkraCloudCredentialRequest) (*client.CreateAnkraCloudCredentialResponse, error) {
	mock.createRequests = append(mock.createRequests, request)
	return &mock.createResult, nil
}

func (mock *ankraCloudCredentialMock) ListAnkraCloudCredentials() ([]client.Credential, error) {
	return mock.credentials, nil
}

func runAnkraCloudCredentialCommand(t *testing.T, mock APIClient, input string, arguments ...string) (string, error) {
	t.Helper()
	var runError error
	var cobraOutput string
	standardOutput := captureStdout(t, func() {
		cobraOutput, runError = runConfirmCommand(t, mock, input,
			[]*cobra.Command{ankraCloudCredentialCreateCmd, ankraCloudCredentialListCmd},
			append([]string{"credentials", "ankracloud"}, arguments...)...)
	})
	return standardOutput + cobraOutput, runError
}

func TestAnkraCloudCredentialCreateReadsTokenFromStdin(t *testing.T) {
	mock := &ankraCloudCredentialMock{createResult: client.CreateAnkraCloudCredentialResponse{Success: true}}
	output, runError := runAnkraCloudCredentialCommand(t, mock, "act_secret\n",
		"create", "--name", "ankra-cloud", "--endpoint", "https://cloud.ankra.dev", "--token-stdin")
	if runError != nil {
		t.Fatalf("create: %v", runError)
	}
	if len(mock.createRequests) != 1 {
		t.Fatalf("create calls = %d, want 1", len(mock.createRequests))
	}
	request := mock.createRequests[0]
	if request.Name != "ankra-cloud" || request.APIToken != "act_secret" || request.Endpoint != "https://cloud.ankra.dev" {
		t.Errorf("request = %+v, want name, trimmed token and endpoint", request)
	}
	if strings.Contains(output, "act_secret") {
		t.Errorf("output echoes the token: %q", output)
	}
}

func TestAnkraCloudCredentialCreateReadsTokenFromEnvironment(t *testing.T) {
	t.Setenv(ankraCloudTokenEnvironmentName, "act_from_environment")
	mock := &ankraCloudCredentialMock{createResult: client.CreateAnkraCloudCredentialResponse{Success: true}}
	if _, runError := runAnkraCloudCredentialCommand(t, mock, "", "create", "--name", "ankra-cloud"); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	if mock.createRequests[0].APIToken != "act_from_environment" || mock.createRequests[0].Endpoint != "" {
		t.Errorf("request = %+v, want the environment token and no endpoint", mock.createRequests[0])
	}
}

func TestAnkraCloudCredentialCreateFallsBackToThePrompt(t *testing.T) {
	t.Setenv(ankraCloudTokenEnvironmentName, "")
	originalPrompt := ankraCloudTokenPrompt
	t.Cleanup(func() { ankraCloudTokenPrompt = originalPrompt })
	ankraCloudTokenPrompt = func() (string, error) { return "act_prompted", nil }

	mock := &ankraCloudCredentialMock{createResult: client.CreateAnkraCloudCredentialResponse{Success: true}}
	if _, runError := runAnkraCloudCredentialCommand(t, mock, "", "create", "--name", "ankra-cloud"); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	if mock.createRequests[0].APIToken != "act_prompted" {
		t.Errorf("token = %q, want the prompted one", mock.createRequests[0].APIToken)
	}

	ankraCloudTokenPrompt = func() (string, error) { return "", errors.New("prompt cancelled") }
	if _, runError := runAnkraCloudCredentialCommand(t, mock, "", "create", "--name", "ankra-cloud"); runError == nil {
		t.Error("a cancelled prompt created a credential")
	}
}

func TestAnkraCloudCredentialCreateRefusesBadInput(t *testing.T) {
	testCases := []struct {
		name      string
		input     string
		arguments []string
	}{
		{"http endpoint", "act_secret", []string{"create", "--name", "ankra-cloud", "--endpoint", "http://cloud.ankra.dev", "--token-stdin"}},
		{"endpoint without host", "act_secret", []string{"create", "--name", "ankra-cloud", "--endpoint", "https://", "--token-stdin"}},
		{"empty stdin", "  \n", []string{"create", "--name", "ankra-cloud", "--token-stdin"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &ankraCloudCredentialMock{createResult: client.CreateAnkraCloudCredentialResponse{Success: true}}
			_, runError := runAnkraCloudCredentialCommand(t, mock, testCase.input, testCase.arguments...)
			if exitCodeFor(runError) != exitUsage {
				t.Errorf("error = %v, want a usage error", runError)
			}
			if len(mock.createRequests) != 0 {
				t.Errorf("create calls = %d, want 0", len(mock.createRequests))
			}
		})
	}
}

func TestAnkraCloudCredentialCreateSurfacesRefusal(t *testing.T) {
	mock := &ankraCloudCredentialMock{createResult: client.CreateAnkraCloudCredentialResponse{
		Errors: []client.ResourceError{{Key: "api_token", Message: "Ankra Cloud rejected the token"}},
	}}
	_, runError := runAnkraCloudCredentialCommand(t, mock, "act_secret", "create", "--name", "ankra-cloud", "--token-stdin")
	if runError == nil || !strings.Contains(runError.Error(), "rejected the token") {
		t.Fatalf("error = %v, want the refusal", runError)
	}
}

func TestAnkraCloudCredentialList(t *testing.T) {
	mock := &ankraCloudCredentialMock{credentials: []client.Credential{
		{ID: "credential-1", Name: "ankra-cloud", Available: true, CreatedAt: "2026-09-29T10:00:00Z"},
		{ID: "credential-2", Name: "ankra-cloud-dev", Available: false, CreatedAt: "2026-09-29T10:00:00Z"},
	}}
	output, runError := runAnkraCloudCredentialCommand(t, mock, "", "list")
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	if !strings.Contains(output, "ankra-cloud-dev") || !strings.Contains(output, "credential-1") {
		t.Errorf("output = %q, want both credentials", output)
	}

	empty := &ankraCloudCredentialMock{}
	output, runError = runAnkraCloudCredentialCommand(t, empty, "", "list")
	if runError != nil || !strings.Contains(output, "No Ankra Cloud credentials found") {
		t.Fatalf("empty list: error %v, output %q", runError, output)
	}

	output, runError = runAnkraCloudCredentialCommand(t, mock, "", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("list -o json: %v", runError)
	}
	var decoded []client.Credential
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || len(decoded) != 2 {
		t.Fatalf("output %q is not the JSON list: %v", output, decodeError)
	}
}
