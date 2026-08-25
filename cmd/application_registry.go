package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The application image-registry commands. An application publishes into the
// organisation's own Ankra registry project by default; one that declares a
// registry it already operates publishes there instead, and Ankra reads its
// image tags back with the organisation registry credential the declaration
// names. Until these commands existed the declaration could only be made at
// create time, so an already-onboarded application could not be pointed at a
// customer-operated Harbor at all.

func newApplicationRegistryCommand() *cobra.Command {
	registryCommand := &cobra.Command{
		Use:     "registry",
		Aliases: []string{"image-registry"},
		Short:   "Inspect and set the container image registry an application publishes to",
		Long: `Inspect and set the container image registry an application publishes to.

An application with no declaration publishes into the organisation's own Ankra
registry project. Declare a registry you already operate to have Ankra read
image tags, verify builds, and pull demo images from there instead.`,
	}
	registryCommand.AddCommand(newApplicationRegistryGetCommand())
	registryCommand.AddCommand(newApplicationRegistrySetCommand())
	registryCommand.AddCommand(newApplicationRegistryClearCommand())
	registryCommand.AddCommand(newApplicationRegistryRobotCommand())
	return registryCommand
}

func newApplicationRegistryGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id>",
		Short: "Show the image registry an application publishes to",
		Long: `Show the image registry an application publishes to.

Reports the stored declaration, whether it is one at all, the host and project
it resolves to, and the image repository each component is expected to publish
to - so you can compare them against where your builds actually push.`,
		Example: "  ankra application registry get 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationImageRegistry(command.Context(),
				applicationID)
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationRegistrySetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Point an application at a container image registry you operate",
		Long: `Point an application at a container image registry you operate.

--url is the registry project, as oci://<host>/<project> (the scheme is
optional). --credential names an existing registry credential of this
organisation; without one Ankra can describe where the images live but cannot
read or pull them, so builds keep reporting as never published.

Ankra never mints robots for a registry it was not handed the keys to. Name a
credential with project administrator rights as --admin-credential to have
Ankra mint, rotate and revoke a push robot for the application there and
store it in the repository's Actions secrets; without one it leaves the
secrets to you unless you ask it to write the declared credential with
--manage-actions-secrets.`,
		Example: `  ankra application registry set 23298741-6a5a-401a-a681-66f31fbdebe1 \
    --url oci://artifact.example.com/commerce --credential example-harbor

  ankra application registry set <application-id> \
    --url artifact.example.com/commerce --credential example-harbor \
    --username-secret HARBOR_USERNAME --password-secret HARBOR_PASSWORD

  ankra application registry set <application-id> \
    --url oci://artifact.example.com/commerce --credential example-harbor-pull \
    --admin-credential example-harbor-admin`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			registryURL, _ := command.Flags().GetString("url")
			registryURL = strings.TrimSpace(registryURL)
			if registryURL == "" {
				return withExitCode(exitUsage,
					errors.New("--url is required: the registry project, as oci://<host>/<project>"))
			}
			credentialName, _ := command.Flags().GetString("credential")
			apiURL, _ := command.Flags().GetString("api-url")
			pullSecretName, _ := command.Flags().GetString("pull-secret")
			usernameSecretName, _ := command.Flags().GetString("username-secret")
			passwordSecretName, _ := command.Flags().GetString("password-secret")
			manageActionsSecrets, _ := command.Flags().GetBool("manage-actions-secrets")
			adminCredentialName, _ := command.Flags().GetString("admin-credential")
			flatRepositories, _ := command.Flags().GetBool("flat-repositories")
			componentRepositoryFlags, _ := command.Flags().GetStringArray("component-repository")
			componentRepositories, componentRepositoriesError := parseComponentRepositories(componentRepositoryFlags)
			if componentRepositoriesError != nil {
				return componentRepositoriesError
			}

			declaration := &client.ApplicationImageRegistry{
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
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, updateError := apiClient.UpdateApplicationImageRegistry(command.Context(),
				applicationID,
				client.UpdateApplicationImageRegistryRequest{ImageRegistry: declaration})
			if updateError != nil {
				return updateError
			}
			if declaration.CredentialName == "" {
				_, _ = fmt.Fprintln(command.ErrOrStderr(),
					"Warning: no --credential was named, so Ankra cannot read image tags from this registry. "+
						"Builds will keep reporting as never published.")
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("url", "", "Registry project as oci://<host>/<project> (required)")
	setCommand.Flags().String("credential", "", "Registry credential of this organisation that authenticates to it")
	setCommand.Flags().String("api-url", "", "Registry management API base (defaults to https://<host>)")
	setCommand.Flags().String("pull-secret", "", "Name of the dockerconfigjson Secret generated manifests reference")
	setCommand.Flags().String("username-secret", "", "Repository Actions secret the build workflow logs in with")
	setCommand.Flags().String("password-secret", "", "Repository Actions secret holding the registry password")
	setCommand.Flags().Bool("manage-actions-secrets", false,
		"Let Ankra write the named credential into the repository's Actions secrets")
	setCommand.Flags().String("admin-credential", "",
		"Registry credential with project administrator rights, for Ankra to mint the application's robot")
	setCommand.Flags().Bool("flat-repositories", false,
		"Publish monorepo components as <project>/<component> instead of <project>/<app>/<component>")
	setCommand.Flags().StringArray("component-repository", nil,
		"Repository inside the project for one component, as <component>=<repository> (repeatable)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newApplicationRegistryClearCommand() *cobra.Command {
	clearCommand := &cobra.Command{
		Use:   "clear <application-id>",
		Short: "Return an application to the organisation's own Ankra registry",
		Long: `Return an application to the organisation's own Ankra registry.

Clears the declaration, so the application publishes into - and is read back
from - the organisation's provisioned registry project again.`,
		Example: "  ankra application registry clear 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(
				command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Clear the declared image registry of application %q? "+
					"It will publish to the organisation's own registry again. [y/N]: ", strings.TrimSpace(arguments[0])),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, updateError := apiClient.UpdateApplicationImageRegistry(command.Context(), applicationID,
				client.UpdateApplicationImageRegistryRequest{ImageRegistry: nil})
			if updateError != nil {
				return updateError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	clearCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clearCommand)
	return clearCommand
}

// parseComponentRepositories reads the repeatable --component-repository
// flags (<component>=<repository>) into the declaration's map.
func parseComponentRepositories(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	repositories := map[string]string{}
	for _, flag := range flags {
		componentName, repository, found := strings.Cut(flag, "=")
		componentName = strings.TrimSpace(componentName)
		repository = strings.TrimSpace(repository)
		if !found || componentName == "" || repository == "" {
			return nil, withExitCode(exitUsage,
				fmt.Errorf("--component-repository %q must be <component>=<repository>", flag))
		}
		repositories[componentName] = repository
	}
	return repositories, nil
}
