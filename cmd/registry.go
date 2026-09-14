package cmd

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The organisation registry commands. Every organisation has a private
// project on the Ankra registry; Ankra mints the robots its own lanes log in
// with (the organisation's ci and pull logins, one push robot per
// application). These commands are for the robots you need yourself: a login
// for CI you run outside Ankra, a laptop pushing an image by hand, or a
// cluster Ankra does not manage pulling from the project.

func newRegistryCommand() *cobra.Command {
	registryCommand := &cobra.Command{
		Use:   "registry",
		Short: "Manage the organisation's Ankra registry",
		Long: `Manage the organisation's Ankra registry.

Every organisation publishes to a private project on the Ankra registry. Ankra
mints the logins its own lanes use - the organisation's ci and pull robots, and
one push robot per application - and these commands cover the robot accounts
you need on top of that: a login for CI you run outside Ankra, a laptop
pushing an image by hand, or a cluster Ankra does not manage pulling from the
project.`,
	}
	registryCommand.AddCommand(newRegistryRobotsCommand())
	return registryCommand
}

func newRegistryRobotsCommand() *cobra.Command {
	robotsCommand := &cobra.Command{
		Use:     "robots",
		Aliases: []string{"robot"},
		Short:   "Create, list, rotate and revoke robot accounts on the organisation's registry project",
		Long: `Create, list, rotate and revoke robot accounts on the organisation's registry
project.

A robot is a named login with push (push and pull) or pull rights on the
organisation's project and nothing else. Its secret is shown once, when it is
created or rotated; the login is also stored as the managed registry credential
ankra-harbor-robot-<name>, so it can be referenced from clusters and
applications like any other registry credential. Revoking a robot deletes it
from the registry and drops that credential in one step.`,
	}
	robotsCommand.AddCommand(newRegistryRobotsCreateCommand())
	robotsCommand.AddCommand(newRegistryRobotsListCommand())
	robotsCommand.AddCommand(newRegistryRobotsGetCommand())
	robotsCommand.AddCommand(newRegistryRobotsRotateCommand())
	robotsCommand.AddCommand(newRegistryRobotsDeleteCommand())
	return robotsCommand
}

func newRegistryRobotsCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a robot account and show its secret once",
		Long: `Create a robot account on the organisation's registry project and show its
secret once.

The name is 2 to 32 lower-case letters, digits and hyphens. The registry login
becomes robot$<project>+user-<name>. --scope push (the default) grants push and
pull; --scope pull grants pull only. The secret is printed exactly once, with
the docker login command that uses it - copy it now, it is not stored anywhere
you can read it back from. Rotate it with 'ankra registry robots rotate' if it
is lost or leaked.`,
		Example: `  ankra registry robots create jenkins --description "Jenkins on the office server"
  ankra registry robots create edge-cluster --scope pull
  ankra registry robots create jenkins -o json | jq -r .secret`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			scope, _ := command.Flags().GetString("scope")
			description, _ := command.Flags().GetString("description")
			created, createError := apiClient.CreateRegistryRobot(command.Context(), client.CreateRegistryRobotRequest{
				Name:        strings.TrimSpace(arguments[0]),
				Scope:       scope,
				Description: description,
			})
			if createError != nil {
				return createError
			}
			return renderRegistryRobotSecret(command, created, "Robot account created.")
		},
	}
	createCommand.Flags().String("scope", client.RegistryRobotScopePush, "Rights on the project: push (push and pull) or pull")
	createCommand.Flags().String("description", "", "What this robot is for (shown in the listing)")
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

func newRegistryRobotsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the organisation's robot accounts",
		Long: `List the robot accounts created on the organisation's registry project.

Shows each robot's name, registry login, scope, description and when it was
created and last rotated. Secrets are never listed. The robots Ankra mints for
its own lanes (ci, pull, one per application) are not robots you created and
are not listed here; they show up as managed credentials in
'ankra credentials list'.`,
		Example: "  ankra registry robots list\n  ankra registry robots list -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			list, listError := apiClient.ListRegistryRobots(command.Context())
			if listError != nil {
				return listError
			}
			if rendered, renderError := renderStructured(command, list); rendered || renderError != nil {
				return renderError
			}
			if len(list.Robots) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), "No robot accounts yet. Create one with 'ankra registry robots create <name>'.")
				return nil
			}
			robotTable := table.NewWriter()
			robotTable.SetOutputMirror(command.OutOrStdout())
			robotTable.SetStyle(table.StyleRounded)
			robotTable.AppendHeader(table.Row{"Name", "Login", "Scope", "Description", "Created", "Rotated"})
			for _, robot := range list.Robots {
				rotated := "-"
				if robot.RotatedAt != nil && *robot.RotatedAt != "" {
					rotated = *robot.RotatedAt
				}
				robotTable.AppendRow(table.Row{robot.Name, robot.RobotName, robot.Scope, robot.Description, robot.CreatedAt, rotated})
			}
			robotTable.Render()
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newRegistryRobotsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <name>",
		Short: "Show one robot account",
		Long: `Show one robot account: its registry login, scope, description, the managed
credential holding its login, and when it was created and last rotated. The
secret is never shown here; rotate the robot to get a new one.`,
		Example: "  ankra registry robots get jenkins",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			robot, getError := apiClient.GetRegistryRobot(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, robot); rendered || renderError != nil {
				return renderError
			}
			printRegistryRobot(command, &robot.Name, robot)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newRegistryRobotsRotateCommand() *cobra.Command {
	rotateCommand := &cobra.Command{
		Use:   "rotate <name>",
		Short: "Mint a new secret for a robot account and show it once",
		Long: `Mint a new secret for a robot account and show it once.

The previous secret stops working the moment the registry answers, which makes
this the response to a leaked or lost secret. Update every place that logs in
with the robot afterwards. A robot the registry no longer has is minted again
under the same name and scope.`,
		Example: "  ankra registry robots rotate jenkins",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			rotated, rotateError := apiClient.RotateRegistryRobotSecret(command.Context(), strings.TrimSpace(arguments[0]))
			if rotateError != nil {
				return rotateError
			}
			return renderRegistryRobotSecret(command, rotated, "Robot secret rotated. The previous secret no longer works.")
		},
	}
	registerStructuredOutputFlags(rotateCommand)
	return rotateCommand
}

func newRegistryRobotsDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"revoke", "rm"},
		Short:   "Revoke a robot account: delete it from the registry and drop its credential",
		Long: `Revoke a robot account: delete it from the registry and drop the managed
credential holding its login.

Everything still logging in with the robot stops working, which is the point
of a revoke. Prefer 'rotate' when the robot should keep working under a new
secret.`,
		Example: "  ankra registry robots delete jenkins --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			robotName := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Revoke robot account %q? Everything logging in with it stops working. [y/N]: ", robotName),
				yes); confirmError != nil {
				return confirmError
			}
			if deleteError := apiClient.DeleteRegistryRobot(command.Context(), robotName); deleteError != nil {
				return deleteError
			}
			if rendered, renderError := renderStructured(command, map[string]any{"name": robotName, "deleted": true}); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Robot account %q revoked.\n", robotName)
			return nil
		},
	}
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

// renderRegistryRobotSecret prints a create or rotate answer: structured
// output carries the secret as a field; the human form shows it once with
// the login command that uses it.
func renderRegistryRobotSecret(command *cobra.Command, robot *client.RegistryRobotWithSecret, headline string) error {
	if rendered, renderError := renderStructured(command, robot); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintln(out, headline)
	_, _ = fmt.Fprintln(out)
	printRegistryRobot(command, nil, &robot.RegistryRobot)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Secret (save this, it will not be shown again):")
	_, _ = fmt.Fprintf(out, "  %s\n", robot.Secret)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Log in with it:")
	_, _ = fmt.Fprintf(out, "  %s\n", robot.DockerLogin)
	return nil
}

// printRegistryRobot prints the robot's record as labelled lines.
func printRegistryRobot(command *cobra.Command, title *string, robot *client.RegistryRobot) {
	out := command.OutOrStdout()
	if title != nil {
		_, _ = fmt.Fprintf(out, "Robot account %q\n", *title)
	}
	_, _ = fmt.Fprintf(out, "  Name:        %s\n", robot.Name)
	_, _ = fmt.Fprintf(out, "  Login:       %s\n", robot.RobotName)
	_, _ = fmt.Fprintf(out, "  Registry:    %s/%s\n", robot.Host, robot.Project)
	_, _ = fmt.Fprintf(out, "  Scope:       %s\n", robot.Scope)
	if robot.Description != "" {
		_, _ = fmt.Fprintf(out, "  Description: %s\n", robot.Description)
	}
	_, _ = fmt.Fprintf(out, "  Credential:  %s\n", robot.CredentialName)
	_, _ = fmt.Fprintf(out, "  Created:     %s\n", robot.CreatedAt)
	if robot.RotatedAt != nil && *robot.RotatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Rotated:     %s\n", *robot.RotatedAt)
	}
}

func init() {
	rootCmd.AddCommand(newRegistryCommand())
}
