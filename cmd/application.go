package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type localApplicationRepository struct {
	Owner  string
	Name   string
	Branch string
}

type applicationAddOutput struct {
	ID             string `json:"id" yaml:"id"`
	Name           string `json:"name" yaml:"name"`
	Repository     string `json:"repository" yaml:"repository"`
	Branch         string `json:"branch" yaml:"branch"`
	CredentialName string `json:"credential_name" yaml:"credential_name"`
}

func newApplicationCommand() *cobra.Command {
	applicationCommand := &cobra.Command{
		Use:     "application",
		Aliases: []string{"applications", "app", "apps"},
		Short:   "Manage applications",
		Long:    "Connect application source repositories to Ankra for analysis, packaging, and deployment.",
	}
	applicationCommand.AddCommand(newApplicationAddCommand())
	registerApplicationResourceCommands(applicationCommand)
	return applicationCommand
}

func newApplicationAddCommand() *cobra.Command {
	addCommand := &cobra.Command{
		Use:   "add <path>",
		Short: "Add an application from a local GitHub checkout",
		Long: `Add an application by reading a local Git checkout.

The command detects the GitHub repository from the selected remote, uses the
remote's default branch when available, and falls back to the current branch.
It selects an available GitHub credential automatically when the choice is
unambiguous.`,
		Example: `  ankra application add .
  ankra application add ./services/payments --name payments
  ankra application add . --credential github-acme --branch main`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationAdd,
	}
	addCommand.Flags().String("name", "", "Application name (defaults to the repository name)")
	addCommand.Flags().String("credential", "", "GitHub credential name or ID (auto-detected when omitted)")
	addCommand.Flags().String("branch", "", "Repository branch (auto-detected when omitted)")
	addCommand.Flags().String("remote", "origin", "Git remote used to identify the GitHub repository")
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

func runApplicationAdd(command *cobra.Command, arguments []string) error {
	if _, outputError := structuredFormatFromFlags(command); outputError != nil {
		return outputError
	}

	remoteName, _ := command.Flags().GetString("remote")
	branchOverride, _ := command.Flags().GetString("branch")
	branchOverride = strings.TrimSpace(branchOverride)
	if command.Flags().Changed("branch") && branchOverride == "" {
		return withExitCode(exitUsage, errors.New("branch cannot be empty"))
	}
	repository, repositoryError := inspectLocalApplicationRepository(
		command.Context(),
		arguments[0],
		strings.TrimSpace(remoteName),
		branchOverride,
	)
	if repositoryError != nil {
		return repositoryError
	}

	applicationName, _ := command.Flags().GetString("name")
	applicationName = strings.TrimSpace(applicationName)
	if applicationName == "" {
		if command.Flags().Changed("name") {
			return withExitCode(exitUsage, errors.New("application name cannot be empty"))
		}
		applicationName = repository.Name
	}

	requestedCredential, _ := command.Flags().GetString("credential")
	requestedCredential = strings.TrimSpace(requestedCredential)
	if command.Flags().Changed("credential") && requestedCredential == "" {
		return withExitCode(exitUsage, errors.New("credential cannot be empty"))
	}
	githubProvider := "github"
	credentials, credentialsError := apiClient.ListCredentials(&githubProvider)
	if credentialsError != nil {
		return fmt.Errorf("listing GitHub credentials: %w", credentialsError)
	}
	selectedCredential, selectionError := selectApplicationCredential(
		credentials,
		repository.Owner,
		requestedCredential,
	)
	if selectionError != nil {
		return selectionError
	}

	applicationResponse, createError := apiClient.CreateApplication(command.Context(), client.CreateApplicationRequest{
		Name:                     applicationName,
		RepositoryCredentialName: selectedCredential.Name,
		RepositoryOwner:          repository.Owner,
		RepositoryName:           repository.Name,
		RepositoryBranch:         repository.Branch,
	})
	if createError != nil {
		return fmt.Errorf("adding application: %w", createError)
	}
	if applicationResponse == nil {
		return errors.New("adding application: platform returned an empty response")
	}
	if len(applicationResponse.Errors) > 0 {
		return applicationCreationError(applicationResponse.Errors)
	}
	if applicationResponse.ID == nil || strings.TrimSpace(*applicationResponse.ID) == "" {
		return errors.New("adding application: platform response did not include an application ID")
	}

	result := applicationAddOutput{
		ID:             *applicationResponse.ID,
		Name:           applicationName,
		Repository:     repository.Owner + "/" + repository.Name,
		Branch:         repository.Branch,
		CredentialName: selectedCredential.Name,
	}
	if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
		return renderError
	}

	output := command.OutOrStdout()
	_, _ = fmt.Fprintln(output, "Application added successfully.")
	_, _ = fmt.Fprintf(output, "  ID:         %s\n", result.ID)
	_, _ = fmt.Fprintf(output, "  Name:       %s\n", result.Name)
	_, _ = fmt.Fprintf(output, "  Repository: %s\n", result.Repository)
	_, _ = fmt.Fprintf(output, "  Branch:     %s\n", result.Branch)
	_, _ = fmt.Fprintf(output, "  Credential: %s\n", result.CredentialName)
	_, _ = fmt.Fprintln(output, "\nAnkra is now analyzing the repository.")
	return nil
}

func inspectLocalApplicationRepository(
	requestContext context.Context,
	repositoryPath string,
	remoteName string,
	branchOverride string,
) (localApplicationRepository, error) {
	if remoteName == "" {
		return localApplicationRepository{}, withExitCode(
			exitUsage,
			errors.New("remote name cannot be empty"),
		)
	}
	pathInformation, statError := os.Stat(repositoryPath)
	if statError != nil {
		if os.IsNotExist(statError) {
			return localApplicationRepository{}, withExitCode(
				exitNotFound,
				fmt.Errorf("application path %q does not exist", repositoryPath),
			)
		}
		return localApplicationRepository{}, fmt.Errorf("cannot access application path %q: %w", repositoryPath, statError)
	}
	if !pathInformation.IsDir() {
		return localApplicationRepository{}, withExitCode(
			exitUsage,
			fmt.Errorf("application path %q is not a directory", repositoryPath),
		)
	}

	repositoryRoot, rootError := executeGit(
		requestContext,
		repositoryPath,
		"rev-parse",
		"--show-toplevel",
	)
	if rootError != nil {
		if strings.Contains(strings.ToLower(rootError.Error()), "not a git repository") {
			return localApplicationRepository{}, withExitCode(
				exitUsage,
				fmt.Errorf("application path %q is not inside a Git repository", repositoryPath),
			)
		}
		return localApplicationRepository{}, fmt.Errorf(
			"inspect Git repository at %q: %w",
			repositoryPath,
			rootError,
		)
	}

	remoteURL, remoteError := executeGit(
		requestContext,
		repositoryRoot,
		"remote",
		"get-url",
		remoteName,
	)
	if remoteError != nil {
		if strings.Contains(strings.ToLower(remoteError.Error()), "no such remote") {
			return localApplicationRepository{}, withExitCode(
				exitUsage,
				fmt.Errorf("git remote %q was not found; add it or pass --remote", remoteName),
			)
		}
		return localApplicationRepository{}, fmt.Errorf("read git remote %q: %w", remoteName, remoteError)
	}
	repositoryOwner, repositoryName, parseError := parseGitHubRepositoryRemote(remoteURL)
	if parseError != nil {
		return localApplicationRepository{}, withExitCode(
			exitUsage,
			fmt.Errorf("git remote %q: %w", remoteName, parseError),
		)
	}

	branchName := branchOverride
	if branchName == "" {
		var branchError error
		branchName, branchError = detectApplicationBranch(requestContext, repositoryRoot, remoteName)
		if branchError != nil {
			return localApplicationRepository{}, fmt.Errorf("detect repository branch: %w", branchError)
		}
	}
	if branchName == "" {
		return localApplicationRepository{}, withExitCode(
			exitUsage,
			errors.New("could not determine the repository branch; pass --branch"),
		)
	}
	if _, branchError := executeGit(
		requestContext,
		repositoryRoot,
		"check-ref-format",
		"--branch",
		branchName,
	); branchError != nil {
		if requestContextError := requestContext.Err(); requestContextError != nil {
			return localApplicationRepository{}, fmt.Errorf("validate repository branch: %w", requestContextError)
		}
		var exitError *exec.ExitError
		if !errors.As(branchError, &exitError) {
			return localApplicationRepository{}, fmt.Errorf("validate repository branch: %w", branchError)
		}
		return localApplicationRepository{}, withExitCode(
			exitUsage,
			fmt.Errorf("repository branch %q is invalid", branchName),
		)
	}

	return localApplicationRepository{
		Owner:  repositoryOwner,
		Name:   repositoryName,
		Branch: branchName,
	}, nil
}

func executeGit(
	requestContext context.Context,
	directory string,
	arguments ...string,
) (string, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	gitCommand := exec.CommandContext(requestContext, "git", commandArguments...)
	gitCommand.Env = append(os.Environ(), "LC_ALL=C")
	commandOutput, commandError := gitCommand.CombinedOutput()
	if commandError != nil {
		errorDetail := strings.TrimSpace(string(commandOutput))
		if errorDetail == "" {
			return "", fmt.Errorf("execute git: %w", commandError)
		}
		return "", fmt.Errorf("execute git: %w: %s", commandError, errorDetail)
	}
	return strings.TrimSpace(string(commandOutput)), nil
}

func detectApplicationBranch(
	requestContext context.Context,
	repositoryRoot string,
	remoteName string,
) (string, error) {
	remoteHead, remoteHeadError := executeGit(
		requestContext,
		repositoryRoot,
		"symbolic-ref",
		"--quiet",
		"--short",
		"refs/remotes/"+remoteName+"/HEAD",
	)
	if remoteHeadError == nil {
		remotePrefix := remoteName + "/"
		if strings.HasPrefix(remoteHead, remotePrefix) {
			if branchName := strings.TrimSpace(strings.TrimPrefix(remoteHead, remotePrefix)); branchName != "" {
				return branchName, nil
			}
		}
	} else {
		if requestContextError := requestContext.Err(); requestContextError != nil {
			return "", requestContextError
		}
		var exitError *exec.ExitError
		if !errors.As(remoteHeadError, &exitError) {
			return "", remoteHeadError
		}
	}

	currentBranch, currentBranchError := executeGit(
		requestContext,
		repositoryRoot,
		"branch",
		"--show-current",
	)
	if currentBranchError != nil {
		return "", currentBranchError
	}
	return strings.TrimSpace(currentBranch), nil
}

func parseGitHubRepositoryRemote(remoteURL string) (string, string, error) {
	trimmedRemoteURL := strings.TrimSpace(remoteURL)
	if trimmedRemoteURL == "" {
		return "", "", errors.New("remote URL is empty")
	}

	var repositoryPath string
	if strings.Contains(trimmedRemoteURL, "://") {
		parsedURL, parseError := url.Parse(trimmedRemoteURL)
		if parseError != nil || !strings.EqualFold(parsedURL.Hostname(), "github.com") {
			return "", "", errors.New("remote is not a github.com repository")
		}
		switch strings.ToLower(parsedURL.Scheme) {
		case "git", "http", "https", "ssh":
		default:
			return "", "", errors.New("remote uses an unsupported GitHub URL scheme")
		}
		repositoryPath = parsedURL.EscapedPath()
	} else {
		separatorIndex := strings.Index(trimmedRemoteURL, ":")
		if separatorIndex <= 0 {
			return "", "", errors.New("remote is not a github.com repository")
		}
		hostPart := trimmedRemoteURL[:separatorIndex]
		if userSeparatorIndex := strings.LastIndex(hostPart, "@"); userSeparatorIndex >= 0 {
			hostPart = hostPart[userSeparatorIndex+1:]
		}
		if !strings.EqualFold(hostPart, "github.com") {
			return "", "", errors.New("remote is not a github.com repository")
		}
		repositoryPath = trimmedRemoteURL[separatorIndex+1:]
	}

	decodedPath, decodeError := url.PathUnescape(repositoryPath)
	if decodeError != nil {
		return "", "", errors.New("remote repository path is invalid")
	}
	pathParts := strings.Split(strings.Trim(decodedPath, "/"), "/")
	if len(pathParts) != 2 {
		return "", "", errors.New("remote must identify a GitHub repository as owner/name")
	}
	repositoryOwner := strings.TrimSpace(pathParts[0])
	repositoryName := strings.TrimSuffix(strings.TrimSpace(pathParts[1]), ".git")
	if repositoryOwner == "" || repositoryName == "" ||
		repositoryOwner == "." || repositoryOwner == ".." ||
		repositoryName == "." || repositoryName == ".." {
		return "", "", errors.New("remote must identify a GitHub repository as owner/name")
	}
	return repositoryOwner, repositoryName, nil
}

func selectApplicationCredential(
	credentials []client.Credential,
	repositoryOwner string,
	requestedCredential string,
) (client.Credential, error) {
	githubCredentials := make([]client.Credential, 0, len(credentials))
	for _, credential := range credentials {
		if strings.EqualFold(credential.Provider, "github") {
			githubCredentials = append(githubCredentials, credential)
		}
	}

	if requestedCredential != "" {
		for _, credential := range githubCredentials {
			if credential.ID == requestedCredential {
				if !applicationCredentialAvailable(credential) {
					return client.Credential{}, fmt.Errorf(
						"GitHub credential %q is not available",
						credential.Name,
					)
				}
				return credential, nil
			}
		}
		nameMatches := make([]client.Credential, 0, 1)
		for _, credential := range githubCredentials {
			if credential.Name == requestedCredential {
				nameMatches = append(nameMatches, credential)
			}
		}
		switch len(nameMatches) {
		case 0:
			return client.Credential{}, withExitCode(
				exitNotFound,
				fmt.Errorf(
					"GitHub credential %q was not found; run `ankra credentials list --provider github`",
					requestedCredential,
				),
			)
		case 1:
			if !applicationCredentialAvailable(nameMatches[0]) {
				return client.Credential{}, fmt.Errorf(
					"GitHub credential %q is not available",
					nameMatches[0].Name,
				)
			}
			return nameMatches[0], nil
		default:
			return client.Credential{}, withExitCode(
				exitUsage,
				fmt.Errorf(
					"multiple GitHub credentials are named %q; pass the credential ID instead",
					requestedCredential,
				),
			)
		}
	}

	availableCredentials := make([]client.Credential, 0, len(githubCredentials))
	ownerMatches := make([]client.Credential, 0, 1)
	for _, credential := range githubCredentials {
		if !applicationCredentialAvailable(credential) {
			continue
		}
		availableCredentials = append(availableCredentials, credential)
		if credential.AccountLogin != nil &&
			strings.EqualFold(strings.TrimSpace(*credential.AccountLogin), repositoryOwner) {
			ownerMatches = append(ownerMatches, credential)
		}
	}

	if len(ownerMatches) == 1 {
		return ownerMatches[0], nil
	}
	if len(ownerMatches) > 1 {
		return client.Credential{}, ambiguousApplicationCredentialError(ownerMatches, repositoryOwner)
	}
	if len(availableCredentials) == 1 {
		return availableCredentials[0], nil
	}
	if len(availableCredentials) == 0 {
		return client.Credential{}, errors.New(
			"no available GitHub credential found; install the Ankra GitHub App, then run this command again",
		)
	}
	return client.Credential{}, ambiguousApplicationCredentialError(availableCredentials, repositoryOwner)
}

func applicationCredentialAvailable(credential client.Credential) bool {
	return credential.Available
}

func ambiguousApplicationCredentialError(
	credentials []client.Credential,
	repositoryOwner string,
) error {
	credentialNames := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		credentialNames = append(credentialNames, credential.Name)
	}
	sort.Strings(credentialNames)
	return withExitCode(
		exitUsage,
		fmt.Errorf(
			"multiple GitHub credentials are available for repository owner %q (%s); pass --credential <name-or-id>",
			repositoryOwner,
			strings.Join(credentialNames, ", "),
		),
	)
}

func applicationCreationError(resourceErrors []client.ApplicationResourceError) error {
	errorMessages := make([]string, 0)
	for _, resourceError := range resourceErrors {
		for _, errorItem := range resourceError.Errors {
			if strings.TrimSpace(errorItem.Message) != "" {
				errorMessages = append(errorMessages, errorItem.Message)
			}
		}
	}
	if len(errorMessages) == 0 {
		return errors.New("application could not be created")
	}
	return errors.New(strings.Join(errorMessages, "; "))
}

func init() {
	rootCmd.AddCommand(newApplicationCommand())
}
