package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// The push-to-deploy switch. With it on, a build Ankra observes on the
// application's tracked branch is rolled out to its deployments without
// anyone asking; with it off, a push builds and stops there.
//
// The read carries the newest build the platform observed on that branch
// alongside the switch, because "on" on its own does not answer the question
// this is usually consulted for - whether the last push was picked up, and
// what it did.

func newApplicationAutoDeployCommand() *cobra.Command {
	autoDeployCommand := &cobra.Command{
		Use:     "auto-deploy",
		Aliases: []string{"push-to-deploy"},
		Short:   "Inspect and set whether pushes to the tracked branch deploy themselves",
		Long: `Inspect and set whether pushes to the tracked branch deploy themselves.

With auto-deploy on, a build Ankra observes on the application's tracked branch
is rolled out to its deployments unattended. With it off, a push still builds -
it just waits for an explicit 'ankra application deploy'.`,
	}
	autoDeployCommand.AddCommand(
		newApplicationAutoDeployGetCommand(),
		newApplicationAutoDeploySetCommand(),
	)
	return autoDeployCommand
}

func newApplicationAutoDeployGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id>",
		Short: "Show whether auto-deploy is on, and the newest build seen on the tracked branch",
		Long: `Show whether auto-deploy is on, and the newest build seen on the tracked branch.

The newest observed build comes back with the switch so you can tell an
auto-deploy that is off from one that is on but has had nothing to pick up.`,
		Example: "  ankra application auto-deploy get 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationAutoDeploy(command.Context(),
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

func newApplicationAutoDeploySetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Turn auto-deploy on or off",
		Long: `Turn auto-deploy on or off.

--enabled is required and must be given explicitly: turning unattended
deployment on and turning it off are both deliberate acts, and neither is a
safe default to infer from a bare 'set'.`,
		Example: `  ankra application auto-deploy set <application-id> --enabled
  ankra application auto-deploy set <application-id> --enabled=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			if !command.Flags().Changed("enabled") {
				return withExitCode(exitUsage,
					errors.New("--enabled is required: pass --enabled to turn auto-deploy on, or --enabled=false to turn it off"))
			}
			enabled, _ := command.Flags().GetBool("enabled")
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, setError := apiClient.SetApplicationAutoDeploy(command.Context(),
				applicationID, enabled)
			if setError != nil {
				return setError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().Bool("enabled", false, "Deploy builds observed on the tracked branch without asking (required)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}
