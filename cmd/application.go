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
	// RegistryURL is reported only when the application declared a registry
	// of its own; without one it publishes to the organisation's Ankra
	// registry project and there is nothing to name.
	RegistryURL string `json:"registry_url,omitempty" yaml:"registry_url,omitempty"`
}

func newApplicationCommand() *cobra.Command {
	applicationCommand := &cobra.Command{
		Use:     "application",
		Aliases: []string{"applications", "app", "apps"},
		Short:   "Manage applications",
		Long:    "Connect application source repositories to Ankra for analysis, packaging, and deployment.",
	}
	applicationCommand.AddCommand(newApplicationAddCommand())
	applicationCommand.AddCommand(newApplicationShipCommand())
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
unambiguous.

Pass --registry-url to have the application publish to a container image
registry you already operate instead of the organisation's own Ankra registry
project. Declare it here rather than afterwards: the setup job generates the
build workflow from the declaration the application is created with, so a
registry added later leaves a workflow that logs in with the wrong one.`,
		Example: `  ankra application add .
  ankra application add ./services/payments --name payments
  ankra application add . --credential github-acme --branch main
  ankra application add . --registry-url oci://artifact.example.com/commerce \
    --registry-credential example-harbor`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationAdd,
	}
	registerApplicationAddFlags(addCommand)
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

// registerApplicationAddFlags registers the flags the add flow reads.
// `application ship` registers the same set, so registering an application
// through ship carries the identical contract as `application add`.
func registerApplicationAddFlags(command *cobra.Command) {
	command.Flags().String("name", "", "Application name (defaults to the repository name)")
	command.Flags().String("credential", "", "GitHub credential name or ID (auto-detected when omitted)")
	command.Flags().String("branch", "", "Repository branch (auto-detected when omitted)")
	command.Flags().String("remote", "origin", "Git remote used to identify the GitHub repository")
	command.Flags().String("registry-url", "",
		"Registry project the application publishes to, as oci://<host>/<project>")
	command.Flags().String("registry-credential", "",
		"Registry credential of this organisation that authenticates to it")
	command.Flags().String("registry-api-url", "",
		"Registry management API base (defaults to https://<host>)")
	command.Flags().String("registry-pull-secret", "",
		"Name of the dockerconfigjson Secret generated manifests reference")
	command.Flags().String("registry-username-secret", "",
		"Repository Actions secret the build workflow logs in with")
	command.Flags().String("registry-password-secret", "",
		"Repository Actions secret holding the registry password")
	command.Flags().Bool("registry-manage-actions-secrets", false,
		"Let Ankra write the named credential into the repository's Actions secrets")
	command.Flags().String("registry-admin-credential", "",
		"Registry credential with project administrator rights, for Ankra to mint the application's robot")
	command.Flags().Bool("registry-flat-repositories", false,
		"Publish monorepo components as <project>/<component> instead of <project>/<app>/<component>")
	command.Flags().StringArray("registry-component-repository", nil,
		"Repository inside the project for one component, as <component>=<repository> (repeatable)")
}

// applicationAddRegistryFlags are the flags that only mean something
// alongside --registry-url. They mirror `application registry set`, prefixed
// so they do not read as the add command's own GitHub credential flags.
var applicationAddRegistryFlags = []string{
	"registry-credential",
	"registry-api-url",
	"registry-pull-secret",
	"registry-username-secret",
	"registry-password-secret",
	"registry-manage-actions-secrets",
	"registry-admin-credential",
	"registry-flat-repositories",
	"registry-component-repository",
}

// applicationAddImageRegistry reads the --registry-* flags into the optional
// declaration the create request carries, and returns nil when no registry
// was declared so the key is omitted entirely - an empty declaration is not
// the same as none, and would point the application at a registry with no
// host.
func applicationAddImageRegistry(command *cobra.Command) (*client.ApplicationImageRegistry, error) {
	registryURL, _ := command.Flags().GetString("registry-url")
	registryURL = strings.TrimSpace(registryURL)
	if registryURL == "" {
		if command.Flags().Changed("registry-url") {
			return nil, withExitCode(exitUsage, errors.New("registry URL cannot be empty"))
		}
		for _, flagName := range applicationAddRegistryFlags {
			if command.Flags().Changed(flagName) {
				return nil, withExitCode(exitUsage, fmt.Errorf(
					"--%s needs --registry-url: the registry project, as oci://<host>/<project>",
					flagName,
				))
			}
		}
		return nil, nil
	}

	credentialName, _ := command.Flags().GetString("registry-credential")
	apiURL, _ := command.Flags().GetString("registry-api-url")
	pullSecretName, _ := command.Flags().GetString("registry-pull-secret")
	usernameSecretName, _ := command.Flags().GetString("registry-username-secret")
	passwordSecretName, _ := command.Flags().GetString("registry-password-secret")
	manageActionsSecrets, _ := command.Flags().GetBool("registry-manage-actions-secrets")
	adminCredentialName, _ := command.Flags().GetString("registry-admin-credential")
	flatRepositories, _ := command.Flags().GetBool("registry-flat-repositories")
	componentRepositoryFlags, _ := command.Flags().GetStringArray("registry-component-repository")
	componentRepositories, componentRepositoriesError := parseComponentRepositories(componentRepositoryFlags)
	if componentRepositoriesError != nil {
		return nil, componentRepositoriesError
	}

	return &client.ApplicationImageRegistry{
		URL:                   registryURL,
		CredentialName:        strings.TrimSpace(credentialName),
		APIURL:                strings.TrimSpace(apiURL),
		PullSecretName:        strings.TrimSpace(pullSecretName),
		UsernameSecretName:    strings.TrimSpace(usernameSecretName),
		PasswordSecretName:    strings.TrimSpace(passwordSecretName),
		ManageActionsSecrets:  manageActionsSecrets,
		AdminCredentialName:   strings.TrimSpace(adminCredentialName),
		FlatRepositories:      flatRepositories,
		ComponentRepositories: componentRepositories,
	}, nil
}

// applicationAddPlan is everything the add flow resolves before it calls the
// platform: the repository identity, the application name, the GitHub
// credential, and the optional registry declaration.
type applicationAddPlan struct {
	repository    localApplicationRepository
	name          string
	credential    client.Credential
	imageRegistry *client.ApplicationImageRegistry
}

// resolveApplicationAddPlan reads the add flags and the local checkout into a
// creation plan. Flag validation runs before the repository inspection and the
// inspection before the credential listing, so an invocation mistake never
// costs an API round-trip.
func resolveApplicationAddPlan(command *cobra.Command, repositoryPath string) (applicationAddPlan, error) {
	imageRegistry, registryError := applicationAddImageRegistry(command)
	if registryError != nil {
		return applicationAddPlan{}, registryError
	}

	remoteName, _ := command.Flags().GetString("remote")
	branchOverride, _ := command.Flags().GetString("branch")
	branchOverride = strings.TrimSpace(branchOverride)
	if command.Flags().Changed("branch") && branchOverride == "" {
		return applicationAddPlan{}, withExitCode(exitUsage, errors.New("branch cannot be empty"))
	}
	repository, repositoryError := inspectLocalApplicationRepository(
		command.Context(),
		repositoryPath,
		strings.TrimSpace(remoteName),
		branchOverride,
	)
	if repositoryError != nil {
		return applicationAddPlan{}, repositoryError
	}

	applicationName, _ := command.Flags().GetString("name")
	applicationName = strings.TrimSpace(applicationName)
	if applicationName == "" {
		if command.Flags().Changed("name") {
			return applicationAddPlan{}, withExitCode(exitUsage, errors.New("application name cannot be empty"))
		}
		applicationName = repository.Name
	}

	requestedCredential, _ := command.Flags().GetString("credential")
	requestedCredential = strings.TrimSpace(requestedCredential)
	if command.Flags().Changed("credential") && requestedCredential == "" {
		return applicationAddPlan{}, withExitCode(exitUsage, errors.New("credential cannot be empty"))
	}
	githubProvider := "github"
	credentials, credentialsError := apiClient.ListCredentials(&githubProvider)
	if credentialsError != nil {
		return applicationAddPlan{}, fmt.Errorf("listing GitHub credentials: %w", credentialsError)
	}
	selectedCredential, selectionError := selectApplicationCredential(
		credentials,
		repository.Owner,
		requestedCredential,
	)
	if selectionError != nil {
		return applicationAddPlan{}, selectionError
	}

	return applicationAddPlan{
		repository:    repository,
		name:          applicationName,
		credential:    selectedCredential,
		imageRegistry: imageRegistry,
	}, nil
}

// createApplicationFromPlan registers the planned application on the platform
// and returns the created identity.
func createApplicationFromPlan(requestContext context.Context, plan applicationAddPlan) (applicationAddOutput, error) {
	applicationResponse, createError := apiClient.CreateApplication(requestContext, client.CreateApplicationRequest{
		Name:                     plan.name,
		RepositoryCredentialName: plan.credential.Name,
		RepositoryOwner:          plan.repository.Owner,
		RepositoryName:           plan.repository.Name,
		RepositoryBranch:         plan.repository.Branch,
		ImageRegistry:            plan.imageRegistry,
	})
	if createError != nil {
		return applicationAddOutput{}, fmt.Errorf("adding application: %w", createError)
	}
	if applicationResponse == nil {
		return applicationAddOutput{}, errors.New("adding application: platform returned an empty response")
	}
	if len(applicationResponse.Errors) > 0 {
		return applicationAddOutput{}, applicationCreationError(applicationResponse.Errors)
	}
	if applicationResponse.ID == nil || strings.TrimSpace(*applicationResponse.ID) == "" {
		return applicationAddOutput{}, errors.New("adding application: platform response did not include an application ID")
	}

	result := applicationAddOutput{
		ID:             *applicationResponse.ID,
		Name:           plan.name,
		Repository:     plan.repository.Owner + "/" + plan.repository.Name,
		Branch:         plan.repository.Branch,
		CredentialName: plan.credential.Name,
	}
	if plan.imageRegistry != nil {
		result.RegistryURL = plan.imageRegistry.URL
	}
	return result, nil
}

func runApplicationAdd(command *cobra.Command, arguments []string) error {
	if _, outputError := structuredFormatFromFlags(command); outputError != nil {
		return outputError
	}
	plan, planError := resolveApplicationAddPlan(command, arguments[0])
	if planError != nil {
		return planError
	}
	result, createError := createApplicationFromPlan(command.Context(), plan)
	if createError != nil {
		return createError
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
	if result.RegistryURL != "" {
		_, _ = fmt.Fprintf(output, "  Registry:   %s\n", result.RegistryURL)
	}
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

// Variables through which Git binds a command to one specific repository,
// index or work tree. `git commit` exports them into every hook it runs, so a
// CLI invoked from a hook — or from `git rebase --exec`, or a CI step wrapped
// in one — would silently address the hook's repository instead of the
// directory named with -C.
var gitRepositoryEnvironmentVariables = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_DIR":                          {},
	"GIT_INDEX_FILE":                   {},
	"GIT_NAMESPACE":                    {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_PREFIX":                       {},
	"GIT_WORK_TREE":                    {},
}

// gitEnvironment is the process environment with those repository-binding
// variables removed and the C locale forced, so a Git invocation addresses the
// directory it was given and its output stays parseable. Everything else the
// user configured (GIT_SSH_COMMAND, credential helpers, proxies) is preserved.
func gitEnvironment() []string {
	processEnvironment := os.Environ()
	gitCommandEnvironment := make([]string, 0, len(processEnvironment)+1)
	for _, environmentEntry := range processEnvironment {
		variableName, _, _ := strings.Cut(environmentEntry, "=")
		if _, isRepositoryBinding := gitRepositoryEnvironmentVariables[variableName]; isRepositoryBinding {
			continue
		}
		gitCommandEnvironment = append(gitCommandEnvironment, environmentEntry)
	}
	return append(gitCommandEnvironment, "LC_ALL=C")
}

func executeGit(
	requestContext context.Context,
	directory string,
	arguments ...string,
) (string, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	gitCommand := exec.CommandContext(requestContext, "git", commandArguments...)
	gitCommand.Env = gitEnvironment()
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
