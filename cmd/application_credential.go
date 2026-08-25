package cmd

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The application repository-credential commands. The GitHub credential an
// application names decides which GitHub App installation every call for its
// repository rides on; until these existed it was set once at create time and
// an application bound to an installation that could not reach its repository
// answered 404 on every build, deploy and secrets write.

func newApplicationCredentialCommand() *cobra.Command {
	credentialCommand := &cobra.Command{
		Use:     "credential",
		Aliases: []string{"repository-credential"},
		Short:   "Inspect and re-bind the GitHub credential an application's repository calls ride on",
		Long: `Inspect and re-bind the GitHub credential an application's repository calls
ride on.

An application is bound to one GitHub credential of the organisation; its App
installation must reach the repository for builds, deploys and Actions secrets
to work. Move an application onto another credential when its installation
lost - or never had - access to the repository.`,
	}
	credentialCommand.AddCommand(newApplicationCredentialGetCommand())
	credentialCommand.AddCommand(newApplicationCredentialSetCommand())
	return credentialCommand
}

func newApplicationCredentialGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get <application-id>",
		Short:   "Show the GitHub credential an application is bound to",
		Example: "  ankra application credential get 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationRepositoryCredential(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationCredentialSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id> --credential <name>",
		Short: "Re-bind an application to another GitHub credential of the organisation",
		Long: `Re-bind an application to another GitHub credential of the organisation.

The credential must exist. Whether its App installation reaches the repository
is reported in the answer (resolved / message) rather than refused, since a
re-bind is usually how an application gets off an installation that cannot.`,
		Example: "  ankra application credential set 67f4ba9c-2dbe-42e2-8e93-a3431bb464fb --credential github-app-acme",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			credentialName, _ := command.Flags().GetString("credential")
			credentialName = strings.TrimSpace(credentialName)
			if credentialName == "" {
				return withExitCode(exitUsage,
					errors.New("--credential is required: a GitHub credential of this organisation"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, setError := apiClient.SetApplicationRepositoryCredential(command.Context(), applicationID,
				client.SetApplicationRepositoryCredentialRequest{CredentialName: credentialName})
			if setError != nil {
				return setError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("credential", "", "GitHub credential of this organisation to bind the application to (required)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}
