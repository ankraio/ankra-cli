package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationAddMock struct {
	baseMock
	credentials         []client.Credential
	applicationResponse *client.CreateApplicationResponse
	applicationRequest  client.CreateApplicationRequest
	createError         error
	createCalls         int
}

func (mock *applicationAddMock) ListCredentials(provider *string) ([]client.Credential, error) {
	return mock.credentials, nil
}

func (mock *applicationAddMock) CreateApplication(
	requestContext context.Context,
	applicationRequest client.CreateApplicationRequest,
) (*client.CreateApplicationResponse, error) {
	mock.createCalls++
	mock.applicationRequest = applicationRequest
	if mock.createError != nil {
		return nil, mock.createError
	}
	return mock.applicationResponse, nil
}

func TestParseGitHubRepositoryRemote(t *testing.T) {
	testCases := []struct {
		name              string
		remoteURL         string
		expectedOwner     string
		expectedName      string
		shouldReturnError bool
	}{
		{
			name:          "https",
			remoteURL:     "https://github.com/ankraio/ankra-cli.git",
			expectedOwner: "ankraio",
			expectedName:  "ankra-cli",
		},
		{
			name:          "scp_ssh",
			remoteURL:     "git@github.com:ankraio/ankra-cli.git",
			expectedOwner: "ankraio",
			expectedName:  "ankra-cli",
		},
		{
			name:          "ssh_url",
			remoteURL:     "ssh://git@github.com/ankraio/ankra-cli.git",
			expectedOwner: "ankraio",
			expectedName:  "ankra-cli",
		},
		{
			name:              "other_provider",
			remoteURL:         "git@gitlab.com:ankraio/ankra-cli.git",
			shouldReturnError: true,
		},
		{
			name:              "missing_owner",
			remoteURL:         "https://github.com/ankra-cli.git",
			shouldReturnError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			owner, repositoryName, parseError := parseGitHubRepositoryRemote(testCase.remoteURL)
			if testCase.shouldReturnError {
				if parseError == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if parseError != nil {
				t.Fatalf("parseGitHubRepositoryRemote() error = %v", parseError)
			}
			if owner != testCase.expectedOwner || repositoryName != testCase.expectedName {
				t.Errorf(
					"parseGitHubRepositoryRemote() = %s/%s, want %s/%s",
					owner,
					repositoryName,
					testCase.expectedOwner,
					testCase.expectedName,
				)
			}
		})
	}
}

func TestInspectLocalApplicationRepositoryUsesRemoteDefaultBranch(t *testing.T) {
	repositoryPath := createTestGitRepository(
		t,
		"feature/local-work",
		"git@github.com:acme/payments.git",
	)
	runTestGit(t, repositoryPath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	nestedPath := filepath.Join(repositoryPath, "services", "api")
	if makeDirectoryError := os.MkdirAll(nestedPath, 0o755); makeDirectoryError != nil {
		t.Fatalf("create nested directory: %v", makeDirectoryError)
	}

	repository, inspectError := inspectLocalApplicationRepository(
		context.Background(),
		nestedPath,
		"origin",
		"",
	)
	if inspectError != nil {
		t.Fatalf("inspectLocalApplicationRepository() error = %v", inspectError)
	}
	if repository.Owner != "acme" || repository.Name != "payments" {
		t.Errorf("repository = %s/%s, want acme/payments", repository.Owner, repository.Name)
	}
	if repository.Branch != "main" {
		t.Errorf("branch = %q, want main", repository.Branch)
	}
}

func TestInspectLocalApplicationRepositoryExitCodes(t *testing.T) {
	t.Run("missing_path", func(t *testing.T) {
		_, inspectError := inspectLocalApplicationRepository(
			context.Background(),
			filepath.Join(t.TempDir(), "missing"),
			"origin",
			"",
		)
		if exitCodeFor(inspectError) != exitNotFound {
			t.Errorf("exit code = %d, want %d: %v", exitCodeFor(inspectError), exitNotFound, inspectError)
		}
	})

	t.Run("not_a_repository", func(t *testing.T) {
		_, inspectError := inspectLocalApplicationRepository(
			context.Background(),
			t.TempDir(),
			"origin",
			"",
		)
		if exitCodeFor(inspectError) != exitUsage {
			t.Errorf("exit code = %d, want %d: %v", exitCodeFor(inspectError), exitUsage, inspectError)
		}
	})

	t.Run("git_not_installed", func(t *testing.T) {
		t.Setenv("PATH", "")
		_, inspectError := inspectLocalApplicationRepository(
			context.Background(),
			t.TempDir(),
			"origin",
			"",
		)
		if exitCodeFor(inspectError) != exitError {
			t.Errorf("exit code = %d, want %d: %v", exitCodeFor(inspectError), exitError, inspectError)
		}
		if inspectError == nil || !strings.Contains(inspectError.Error(), "execute git") {
			t.Errorf("error = %v, want preserved Git execution failure", inspectError)
		}
	})
}

func TestSelectApplicationCredential(t *testing.T) {
	acmeLogin := "acme"
	otherLogin := "other"
	credentials := []client.Credential{
		{
			ID:           "credential-acme",
			Name:         "github-acme",
			Provider:     "github",
			Available:    true,
			AccountLogin: &acmeLogin,
		},
		{
			ID:           "credential-other",
			Name:         "github-other",
			Provider:     "github",
			Available:    true,
			AccountLogin: &otherLogin,
		},
	}

	selectedCredential, selectionError := selectApplicationCredential(credentials, "acme", "")
	if selectionError != nil {
		t.Fatalf("selectApplicationCredential() error = %v", selectionError)
	}
	if selectedCredential.ID != "credential-acme" {
		t.Errorf("selected credential = %q, want credential-acme", selectedCredential.ID)
	}

	selectedCredential, selectionError = selectApplicationCredential(
		credentials,
		"unmatched-owner",
		"credential-other",
	)
	if selectionError != nil {
		t.Fatalf("selectApplicationCredential() explicit error = %v", selectionError)
	}
	if selectedCredential.ID != "credential-other" {
		t.Errorf("explicit credential = %q, want credential-other", selectedCredential.ID)
	}

	_, selectionError = selectApplicationCredential(credentials, "unmatched-owner", "")
	if selectionError == nil || !strings.Contains(selectionError.Error(), "--credential") {
		t.Errorf("ambiguous selection error = %v, want --credential guidance", selectionError)
	}
	if exitCodeFor(selectionError) != exitUsage {
		t.Errorf("ambiguous selection exit code = %d, want %d", exitCodeFor(selectionError), exitUsage)
	}

	upState := "up"
	_, selectionError = selectApplicationCredential(
		[]client.Credential{
			{
				ID:        "unavailable-credential",
				Name:      "github-unavailable",
				Provider:  "github",
				Available: false,
				State:     &upState,
			},
		},
		"acme",
		"unavailable-credential",
	)
	if selectionError == nil || !strings.Contains(selectionError.Error(), "not available") {
		t.Errorf("unavailable selection error = %v, want availability error", selectionError)
	}
}

func TestApplicationAddCommand(t *testing.T) {
	repositoryPath := createTestGitRepository(
		t,
		"main",
		"https://github.com/acme/payments.git",
	)
	applicationID := "application-id"
	acmeLogin := "acme"
	mockClient := &applicationAddMock{
		credentials: []client.Credential{
			{
				ID:           "credential-id",
				Name:         "github-acme",
				Provider:     "github",
				Available:    true,
				AccountLogin: &acmeLogin,
			},
		},
		applicationResponse: &client.CreateApplicationResponse{
			ID:     &applicationID,
			Errors: []client.ApplicationResourceError{},
		},
	}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() {
		apiClient = previousClient
	})

	applicationCommand := newApplicationCommand()
	var output bytes.Buffer
	applicationCommand.SetOut(&output)
	applicationCommand.SetErr(&output)
	applicationCommand.SetArgs([]string{"add", repositoryPath})
	if executeError := applicationCommand.Execute(); executeError != nil {
		t.Fatalf("application add error = %v", executeError)
	}

	if mockClient.applicationRequest.Name != "payments" {
		t.Errorf("application name = %q, want payments", mockClient.applicationRequest.Name)
	}
	if mockClient.applicationRequest.RepositoryCredentialName != "github-acme" {
		t.Errorf(
			"credential name = %q, want github-acme",
			mockClient.applicationRequest.RepositoryCredentialName,
		)
	}
	if mockClient.applicationRequest.RepositoryOwner != "acme" ||
		mockClient.applicationRequest.RepositoryName != "payments" {
		t.Errorf(
			"repository = %s/%s, want acme/payments",
			mockClient.applicationRequest.RepositoryOwner,
			mockClient.applicationRequest.RepositoryName,
		)
	}
	if mockClient.applicationRequest.RepositoryBranch != "main" {
		t.Errorf(
			"repository branch = %q, want main",
			mockClient.applicationRequest.RepositoryBranch,
		)
	}
	if !strings.Contains(output.String(), "Application added successfully.") ||
		!strings.Contains(output.String(), applicationID) {
		t.Errorf("output = %q", output.String())
	}
}

func TestApplicationAddCommandStructuredOutput(t *testing.T) {
	repositoryPath := createTestGitRepository(
		t,
		"main",
		"https://github.com/acme/payments.git",
	)
	applicationID := "application-id"
	mockClient := &applicationAddMock{
		credentials: []client.Credential{
			{
				ID:        "credential-id",
				Name:      "github-acme",
				Provider:  "github",
				Available: true,
			},
		},
		applicationResponse: &client.CreateApplicationResponse{
			ID:     &applicationID,
			Errors: []client.ApplicationResourceError{},
		},
	}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() {
		apiClient = previousClient
	})

	applicationCommand := newApplicationCommand()
	var output bytes.Buffer
	applicationCommand.SetOut(&output)
	applicationCommand.SetErr(&output)
	applicationCommand.SetArgs([]string{"add", repositoryPath, "-o", "json"})
	if executeError := applicationCommand.Execute(); executeError != nil {
		t.Fatalf("application add error = %v", executeError)
	}

	var result applicationAddOutput
	if decodeError := json.Unmarshal(output.Bytes(), &result); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, output.String())
	}
	if result.ID != applicationID || result.Repository != "acme/payments" {
		t.Errorf("structured output = %+v", result)
	}
	if strings.Contains(output.String(), "successfully") {
		t.Errorf("structured output contains human text: %q", output.String())
	}
}

func TestApplicationAddCommandRejectsInvalidOutputBeforeCreating(t *testing.T) {
	mockClient := &applicationAddMock{}
	previousClient := apiClient
	apiClient = mockClient
	t.Cleanup(func() {
		apiClient = previousClient
	})

	applicationCommand := newApplicationCommand()
	applicationCommand.SetArgs([]string{"add", ".", "-o", "xml"})
	executeError := applicationCommand.Execute()
	if executeError == nil {
		t.Fatal("expected invalid output format to fail")
	}
	if mockClient.createCalls != 0 {
		t.Errorf("CreateApplication() calls = %d, want 0", mockClient.createCalls)
	}
}

func TestApplicationCreationError(t *testing.T) {
	creationError := applicationCreationError([]client.ApplicationResourceError{
		{
			Name: "payments",
			Kind: "application",
			Errors: []client.ApplicationErrorItem{
				{Key: "name", Message: "Application already exists."},
			},
		},
	})
	if creationError.Error() != "Application already exists." {
		t.Errorf("applicationCreationError() = %v", creationError)
	}
}

func createTestGitRepository(t *testing.T, branchName string, remoteURL string) string {
	t.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		t.Skip("git is not installed")
	}
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	runExternalCommand(t, "git", "init", "--initial-branch", branchName, repositoryPath)
	runTestGit(t, repositoryPath, "remote", "add", "origin", remoteURL)
	return repositoryPath
}

func runTestGit(t *testing.T, repositoryPath string, arguments ...string) {
	t.Helper()
	commandArguments := append([]string{"-C", repositoryPath}, arguments...)
	runExternalCommand(t, "git", commandArguments...)
}

func runExternalCommand(
	t *testing.T,
	commandName string,
	arguments ...string,
) {
	t.Helper()
	command := exec.Command(commandName, arguments...)
	if commandOutput, commandError := command.CombinedOutput(); commandError != nil {
		t.Fatalf("%s failed: %v\n%s", commandName, commandError, commandOutput)
	}
}
