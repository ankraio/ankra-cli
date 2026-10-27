package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The application tracked-branch commands. The tracked branch decides which
// branch's builds auto-deploy, which pipeline runs belong to the application
// and which branch setup pull requests target; until these existed it was set
// once at create time and could only be changed by deleting the application.

func newApplicationTrackedBranchCommand() *cobra.Command {
	trackedBranchCommand := &cobra.Command{
		Use:     "tracked-branch",
		Aliases: []string{"branch"},
		Short:   "Inspect and change the branch an application tracks",
		Long: `Inspect and change the branch an application tracks.

The tracked branch decides which branch's builds auto-deploy, which pipeline
runs are attributed to the application, and which branch Ankra's setup pull
requests target. List the repository's branches with 'ankra application
branches'.`,
	}
	trackedBranchCommand.AddCommand(newApplicationTrackedBranchGetCommand())
	trackedBranchCommand.AddCommand(newApplicationTrackedBranchSetCommand())
	return trackedBranchCommand
}

func newApplicationTrackedBranchGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get <application-id>",
		Short:   "Show the branch an application tracks",
		Example: "  ankra application tracked-branch get 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			applicationPayload, getError := apiClient.GetApplicationRaw(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			var application struct {
				Branch string `json:"app_repo_branch"`
			}
			if decodeError := json.Unmarshal(applicationPayload, &application); decodeError != nil {
				return fmt.Errorf("reading the application's tracked branch: %w", decodeError)
			}
			payload, encodeError := json.Marshal(map[string]string{"branch": application.Branch})
			if encodeError != nil {
				return fmt.Errorf("encoding the tracked branch: %w", encodeError)
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationTrackedBranchSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id> <branch>",
		Short: "Point an application at another branch of its repository",
		Long: `Point an application at another branch of its repository.

The branch must exist on the repository. When GitHub cannot confirm that it
does (no credential, GitHub unreachable, or a repository the credential cannot
read) nothing is changed and the command fails, rather than leaving the
application watching a branch nobody pushes to.`,
		Example: "  ankra application tracked-branch set 67f4ba9c-2dbe-42e2-8e93-a3431bb464fb develop",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			branch := strings.TrimSpace(arguments[1])
			if branch == "" {
				return withExitCode(exitUsage, errors.New("the branch must name a branch of the application's repository"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments[:1])
			if resolveError != nil {
				return resolveError
			}
			payload, setError := apiClient.SetApplicationTrackedBranch(command.Context(), applicationID,
				client.SetApplicationTrackedBranchRequest{Branch: branch})
			if setError != nil {
				return setError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(setCommand)
	return setCommand
}
