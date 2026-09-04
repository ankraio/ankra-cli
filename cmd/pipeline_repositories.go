package cmd

// `ankra pipeline repositories …` (ankra-vn0bd.4.2, WS-D item D2): the CLI
// surface over the organisation-scoped routes cluster PRs #2490 and #2509
// added - POST/GET/DELETE /org/pipelines/repositories[/{repository_id}]
// (go/internal/pipelineapi/repositories.go) - which connect a bare Git
// repository to Ankra Pipelines (optionally linking an application and a CI
// cluster override), list what the organisation has connected, read one by
// id, and disconnect one.
//
// These four routes address the organisation alone, not one already-linked
// pipeline: unlike every other `ankra pipeline …` command (cmd/pipeline.go
// and its siblings) there is no --application/--repository selector here.
// Once connected, a repository is addressed the usual way -
// `ankra pipeline get --repository <id>` and friends, or
// `ankra application pipeline …` once it is linked to an application.
//
// There is still no `ankra application pipeline connect` alias: --application
// on connect names an id the caller already has (an application does not
// resolve itself), and an application already on Ankra links to its own
// repository automatically through the scheduler's own onboarding writer
// (enginekit/pipelineonboard.Onboard) - this route is for a bare repository
// or a repository connected ahead of the application that will use it.
//
// Re-read the merged handlers (go/internal/pipelineapi/repositories.go,
// go/internal/usecase/pipelines/repositories.go) before widening this surface
// further; this comment is not a promise of what they carry today.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineRepositoryProviderHelp documents the accepted --provider values.
const pipelineRepositoryProviderHelp = "Repository provider: github, gitlab, or bitbucket"

// normalisePipelineRepositoryProvider translates the CLI-friendly
// "bitbucket" spelling to the wire value the server's provider vocabulary
// uses (enginekit/pipelinerun.ProviderBitbucketCloud = "bitbucket_cloud").
// Every other value, known or not, passes through unchanged so the server's
// own 422 names the ones that are not valid - the vocabulary
// (pipelinerun.IsKnownProvider) is not duplicated client-side.
func normalisePipelineRepositoryProvider(provider string) string {
	if provider == "bitbucket" {
		return "bitbucket_cloud"
	}
	return provider
}

func newPipelineRepositoriesCommand() *cobra.Command {
	repositoriesCommand := &cobra.Command{
		Use:     "repositories",
		Aliases: []string{"repos"},
		Short:   "Manage the organisation's connected pipeline repositories",
		Long: `Connect a bare Git repository to Ankra Pipelines, list what the
organisation has connected, read one by id, and disconnect one.

A connected repository is what a push, pull request or tag webhook resolves
against to start a run. Connecting one here does not require an Ankra
application - use it for a repository with no application (docs, website,
values repos) or before one exists; an application already on Ankra links to
its own repository automatically.`,
	}
	repositoriesCommand.AddCommand(
		newPipelineRepositoriesListCommand(),
		newPipelineRepositoriesGetCommand(),
		newPipelineRepositoriesConnectCommand(),
		newPipelineRepositoriesDisconnectCommand(),
	)
	return repositoriesCommand
}

func newPipelineRepositoriesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the organisation's connected pipeline repositories",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineRepositoriesList(command)
		},
	}
	listCommand.Flags().String("provider", "", pipelineRepositoryProviderHelp+" (filter)")
	listCommand.Flags().String("cursor", "", "Page cursor from a previous listing's next_cursor")
	listCommand.Flags().Int("limit", 0, "Maximum number of repositories to return (server default 50, max 100)")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func runPipelineRepositoriesList(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	provider, _ := command.Flags().GetString("provider")
	cursor, _ := command.Flags().GetString("cursor")
	limit, _ := command.Flags().GetInt("limit")

	page, listError := apiClient.ListPipelineRepositories(command.Context(), client.ListPipelineRepositoriesOptions{
		Provider: normalisePipelineRepositoryProvider(strings.TrimSpace(provider)),
		Cursor:   strings.TrimSpace(cursor),
		Limit:    limit,
	})
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, page)
	}
	if len(page.Repositories) == 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(),
			"No pipeline repositories connected. Connect one with 'ankra pipeline repositories connect'.")
		return nil
	}
	renderPipelineRepositoryTable(command.OutOrStdout(), page.Repositories)
	if page.NextCursor != nil {
		_, _ = fmt.Fprintf(command.ErrOrStderr(),
			"\nMore repositories available: pass --cursor %s to see the next page.\n", *page.NextCursor)
	}
	return nil
}

func renderPipelineRepositoryTable(out io.Writer, repositories []client.PipelineRepository) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "PROVIDER", "REPOSITORY", "DEFAULT BRANCH", "APPLICATION", "CI CLUSTER", "CREATED"})
	for _, repository := range repositories {
		writer.AppendRow(table.Row{
			repository.ID,
			repository.Provider,
			repository.Owner + "/" + repository.Name,
			repository.DefaultBranch,
			pipelineOptionalString(repository.ApplicationID),
			pipelineOptionalString(repository.ClusterID),
			formatTimeAgo(repository.CreatedAt),
		})
	}
	writer.Render()
}

func newPipelineRepositoriesGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <repository-id>",
		Short: "Show a connected pipeline repository",
		Long: `Show one connected repository by id.

There is no lookup by owner/name yet, only by id - run
'ankra pipeline repositories list' to find it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineRepositoriesGet(command, arguments[0])
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runPipelineRepositoriesGet(command *cobra.Command, repositoryID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	repositoryID = strings.TrimSpace(repositoryID)
	if !looksLikeUUID(repositoryID) {
		return withExitCode(exitUsage, fmt.Errorf(
			"%q is not a repository id - there is no lookup by owner/name yet, "+
				"run 'ankra pipeline repositories list' to find it", repositoryID))
	}
	repository, getError := apiClient.GetPipelineRepository(command.Context(), repositoryID)
	if getError != nil {
		return getError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, repository)
	}
	printPipelineRepository(command.OutOrStdout(), repository)
	return nil
}

func printPipelineRepository(out io.Writer, repository *client.PipelineRepository) {
	credentialName := repository.CredentialName
	if credentialName == "" {
		credentialName = "-"
	}
	_, _ = fmt.Fprintf(out, "Repository:     %s/%s\n", repository.Owner, repository.Name)
	_, _ = fmt.Fprintf(out, "  ID:             %s\n", repository.ID)
	_, _ = fmt.Fprintf(out, "  Provider:       %s\n", repository.Provider)
	_, _ = fmt.Fprintf(out, "  Credential:     %s\n", credentialName)
	_, _ = fmt.Fprintf(out, "  Default branch: %s\n", repository.DefaultBranch)
	_, _ = fmt.Fprintf(out, "  Application:    %s\n", pipelineOptionalString(repository.ApplicationID))
	_, _ = fmt.Fprintf(out, "  CI cluster:     %s\n", pipelineOptionalString(repository.ClusterID))
	_, _ = fmt.Fprintf(out, "  Created:        %s\n", formatTimeAgo(repository.CreatedAt))
	_, _ = fmt.Fprintf(out, "  Updated:        %s\n", formatTimeAgo(repository.UpdatedAt))
}

func newPipelineRepositoriesConnectCommand() *cobra.Command {
	connectCommand := &cobra.Command{
		Use:   "connect",
		Short: "Connect a Git repository to Ankra Pipelines",
		Long: `Connect a bare Git repository to Ankra Pipelines: a push, pull request or
tag webhook on it can then start a run.

Connecting a repository that is already connected does not create a second
row or refresh the first - the platform refuses with the existing
repository's id named in the error, so a setup script that runs this
unconditionally can still learn the id it has either way.

--application links the repository to an application already in this
organisation (by id) without changing that application's own pipeline
source - it is refused (422) for an application outside the organisation.
--cluster overrides the organisation's declared CI cluster
('ankra ci-settings' / GET /org/ci-settings) for just this repository's
pipelines - it is refused (422) for a cluster outside the organisation, or
one whose agent has not advertised it can run pipeline steps.

For a GitHub repository, the committed .ankra/pipeline.yaml on the default
branch (read through --credential) is recorded as the definition of record in
the same call when it parses. A repository with no committed file, or on
another provider, connects with no definition - store one with
'ankra pipeline definition put --repository <id> <file>', or commit the file
and connect again.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineRepositoriesConnect(command)
		},
	}
	connectCommand.Flags().String("provider", "", pipelineRepositoryProviderHelp)
	connectCommand.Flags().String("owner", "", "Repository owner or organisation")
	connectCommand.Flags().String("name", "", "Repository name")
	connectCommand.Flags().String("credential", "",
		"Organisation Git credential used to read the repository; omit to connect without reading the committed pipeline file")
	connectCommand.Flags().String("default-branch", "", "Branch the pipeline file is read from (default: main)")
	connectCommand.Flags().String("application", "", "Application id to link this repository to (optional; must belong to this organisation)")
	connectCommand.Flags().String("cluster", "", "Cluster id to run this repository's pipelines on, overriding the organisation default (optional; its agent must support pipeline steps)")
	_ = connectCommand.MarkFlagRequired("provider")
	_ = connectCommand.MarkFlagRequired("owner")
	_ = connectCommand.MarkFlagRequired("name")
	registerStructuredOutputFlags(connectCommand)
	return connectCommand
}

func runPipelineRepositoriesConnect(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	provider, _ := command.Flags().GetString("provider")
	owner, _ := command.Flags().GetString("owner")
	name, _ := command.Flags().GetString("name")
	credentialName, _ := command.Flags().GetString("credential")
	defaultBranch, _ := command.Flags().GetString("default-branch")
	applicationID, _ := command.Flags().GetString("application")
	clusterID, _ := command.Flags().GetString("cluster")

	result, connectError := apiClient.ConnectPipelineRepository(command.Context(), client.ConnectPipelineRepositoryRequest{
		Provider:       normalisePipelineRepositoryProvider(strings.TrimSpace(provider)),
		Owner:          strings.TrimSpace(owner),
		Name:           strings.TrimSpace(name),
		CredentialName: strings.TrimSpace(credentialName),
		DefaultBranch:  strings.TrimSpace(defaultBranch),
		ApplicationID:  strings.TrimSpace(applicationID),
		ClusterID:      strings.TrimSpace(clusterID),
	})
	if connectError != nil {
		var alreadyConnected *client.PipelineRepositoryAlreadyConnectedError
		if errors.As(connectError, &alreadyConnected) {
			return fmt.Errorf("%s - see it with 'ankra pipeline repositories get %s'",
				alreadyConnected.Detail, alreadyConnected.RepositoryID)
		}
		return connectError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, result)
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Connected %s/%s as repository %s (provider: %s, default branch: %s)\n",
		result.Owner, result.Name, result.ID, result.Provider, result.DefaultBranch)
	if result.ApplicationID != nil && *result.ApplicationID != "" {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Application: %s\n", *result.ApplicationID)
	}
	if result.ClusterID != nil && *result.ClusterID != "" {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "CI cluster:  %s\n", *result.ClusterID)
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Definition: %s - %s\n", result.Definition.Status, result.Definition.Detail)
	if result.Definition.ReadError != nil && *result.Definition.ReadError != "" {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "  (committed file: %s)\n", *result.Definition.ReadError)
	}
	if len(result.Definition.Violations) > 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "Violations:")
		for _, violation := range result.Definition.Violations {
			_, _ = fmt.Fprintf(command.OutOrStdout(), "  - %s\n", violation)
		}
	}
	return nil
}

func newPipelineRepositoriesDisconnectCommand() *cobra.Command {
	disconnectCommand := &cobra.Command{
		Use:     "disconnect <repository-id>",
		Aliases: []string{"rm"},
		Short:   "Disconnect a pipeline repository",
		Long: `Disconnect a connected repository: from then on a push, pull request or
tag webhook against it starts no run, and it drops out of
'ankra pipeline repositories list'/'get'.

This is reversible by construction - connecting the same provider/owner/name
again revives this exact row, definitions, runs, artifacts and findings kept -
so disconnecting is not a delete. A repository with a pipeline run still
queued or running refuses to disconnect (409); cancel or wait for it to
conclude, then disconnect again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineRepositoriesDisconnect(command, arguments[0])
		},
	}
	disconnectCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(disconnectCommand)
	return disconnectCommand
}

func runPipelineRepositoriesDisconnect(command *cobra.Command, repositoryID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	repositoryID = strings.TrimSpace(repositoryID)
	if !looksLikeUUID(repositoryID) {
		return withExitCode(exitUsage, fmt.Errorf(
			"%q is not a repository id - there is no lookup by owner/name yet, "+
				"run 'ankra pipeline repositories list' to find it", repositoryID))
	}
	skipConfirmation, _ := command.Flags().GetBool("yes")
	confirmMessage := fmt.Sprintf("Disconnect pipeline repository %s? [y/N] ", repositoryID)
	// The prompt goes to stderr, not OutOrStdout: unlike 'schedules delete'
	// (which never registers -o json/-o yaml), this command does, and a
	// prompt byte ahead of encodeStructured's JSON/YAML would leave stdout
	// unparseable for a script that passed --format without --yes.
	if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), confirmMessage, skipConfirmation); confirmError != nil {
		return confirmError
	}
	if disconnectError := apiClient.DisconnectPipelineRepository(command.Context(), repositoryID); disconnectError != nil {
		return disconnectError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, map[string]any{"repository_id": repositoryID, "disconnected": true})
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Repository %s disconnected.\n", repositoryID)
	return nil
}
