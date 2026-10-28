package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The application registry robot commands. Every application gets a push
// robot of its own on the registry it publishes to - minted by Ankra on the
// organisation's registry, and on a registry the organisation operates once
// the declaration names an admin credential. These commands show it, mint or
// confirm it, rotate it after a leak, and revoke it.

func newApplicationRegistryRobotCommand() *cobra.Command {
	robotCommand := &cobra.Command{
		Use:   "robot",
		Short: "Manage the registry robot account minted for an application",
		Long: `Manage the registry robot account minted for an application.

Ankra mints one push robot per application on the registry it publishes to,
stores it as a managed registry credential, and writes it into the repository's
Actions secrets as the login the build workflow uses. On the organisation's own
Ankra registry that happens automatically; on a registry you operate it needs
the declaration to name a credential with project administrator rights
(ankra application registry set --admin-credential).`,
	}
	robotCommand.AddCommand(newApplicationRegistryRobotGetCommand())
	robotCommand.AddCommand(newApplicationRegistryRobotEnsureCommand())
	robotCommand.AddCommand(newApplicationRegistryRobotRotateCommand())
	robotCommand.AddCommand(newApplicationRegistryRobotRevokeCommand())
	return robotCommand
}

func newApplicationRegistryRobotGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id>",
		Short: "Show the registry robot account minted for an application",
		Long: `Show the registry robot account minted for an application.

Reports whether a robot backs the application's registry login, its name, the
registry it lives on, when it was created and last rotated - and when none
does, why, and what would let Ankra mint one. Never contacts the registry.`,
		Example: "  ankra application registry robot get 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationRegistryRobot(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationRegistryRobotEnsureCommand() *cobra.Command {
	ensureCommand := &cobra.Command{
		Use:   "ensure <application-id>",
		Short: "Mint the application's registry robot if it has none, and store its login in the repository",
		Long: `Mint the application's registry robot if it has none, and store its login in
the repository's Actions secrets.

An application that already has a robot keeps it; only its login is re-written
into the repository. A registry Ankra may not administer answers with
provisioned false and a message naming what would let it.`,
		Example: "  ankra application registry robot ensure 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runApplicationRegistryRobotEnsure(command, arguments, false)
		},
	}
	registerStructuredOutputFlags(ensureCommand)
	return ensureCommand
}

func newApplicationRegistryRobotRotateCommand() *cobra.Command {
	rotateCommand := &cobra.Command{
		Use:   "rotate <application-id>",
		Short: "Rotate the application's registry robot secret and store the new login in the repository",
		Long: `Rotate the application's registry robot secret and store the new login in the
repository's Actions secrets.

The previous secret stops working the moment the registry answers, which makes
this the response to a leaked repository secret. A build that starts before the
new login is stored fails at the registry login; re-run it afterwards. An
application with no robot yet gets one minted.`,
		Example: "  ankra application registry robot rotate 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runApplicationRegistryRobotEnsure(command, arguments, true)
		},
	}
	registerStructuredOutputFlags(rotateCommand)
	return rotateCommand
}

func runApplicationRegistryRobotEnsure(command *cobra.Command, arguments []string, rotate bool) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	applicationID, resolveError := resolveApplicationArgument(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	payload, ensureError := apiClient.EnsureApplicationRegistryRobot(command.Context(), applicationID,
		client.EnsureApplicationRegistryRobotRequest{Rotate: rotate})
	if ensureError != nil {
		return ensureError
	}
	return renderApplicationPayload(command, payload)
}

func newApplicationRegistryRobotRevokeCommand() *cobra.Command {
	revokeCommand := &cobra.Command{
		Use:   "revoke <application-id>",
		Short: "Delete the application's registry robot and drop its credential",
		Long: `Delete the application's registry robot from the registry and drop the
credential holding its login.

The repository's Actions secrets are left holding a login that no longer works,
which is the point of a revoke; run 'ensure' afterwards to mint a fresh robot
and store it. Prefer 'rotate' when the application should keep working.`,
		Example: "  ankra application registry robot revoke 23298741-6a5a-401a-a681-66f31fbdebe1 --yes",
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
				fmt.Sprintf("Revoke the registry robot of application %q? "+
					"Its builds cannot push until a new one is minted with 'ensure'. [y/N]: ",
					strings.TrimSpace(arguments[0])),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, revokeError := apiClient.RevokeApplicationRegistryRobot(command.Context(), applicationID)
			if revokeError != nil {
				return revokeError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	revokeCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(revokeCommand)
	return revokeCommand
}
