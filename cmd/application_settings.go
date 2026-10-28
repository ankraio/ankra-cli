package cmd

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

// The organisation-level application settings: the GitHub Actions runner
// label every pipeline Ankra generates runs on.
//
// Generated pipelines used to carry a hardcoded ubuntu-latest, so an
// organisation whose GitHub-hosted runners are refused - Actions billing in
// arrears, or a spending limit - was handed a pipeline it could not execute.
// Any member may read the setting; only an organisation admin may change it,
// and the backend answers a member's write with that reason rather than a
// generic denial.

func newApplicationSettingsCommand() *cobra.Command {
	settingsCommand := &cobra.Command{
		Use:   "settings",
		Short: "Inspect and set the organisation's application CI settings",
		Long: `Inspect and set the organisation's application CI settings.

These apply to every application in the organisation, not to one of them. The
CI runner label decides which GitHub Actions runner the pipelines Ankra
generates request - change it when GitHub-hosted runners are unavailable to
you and your builds run on self-hosted ones.`,
	}
	settingsCommand.AddCommand(
		newApplicationSettingsGetCommand(),
		newApplicationSettingsSetCommand(),
	)
	return settingsCommand
}

func newApplicationSettingsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the organisation's application CI settings",
		Long: `Show the organisation's application CI settings.

Readable by any member: a member who cannot explain why a build never started
is the reason this setting exists.`,
		Example: "  ankra application settings get",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, getError := apiClient.GetApplicationSettings(command.Context())
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationSettingsSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Set or clear the GitHub Actions runner label generated pipelines run on",
		Long: `Set or clear the GitHub Actions runner label generated pipelines run on.

Pass --ci-runner-label to choose the label, or --clear to drop the
organisation's choice and put every future generation back on the default.
Existing pipelines are not rewritten by either.

Only organisation admins may change this.`,
		Example: `  ankra application settings set --ci-runner-label self-hosted
  ankra application settings set --clear`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clear, _ := command.Flags().GetBool("clear")
			labelChanged := command.Flags().Changed("ci-runner-label")
			if clear && labelChanged {
				return withExitCode(exitUsage,
					errors.New("--clear and --ci-runner-label are mutually exclusive: --clear returns the organisation to the default runner"))
			}
			if !clear && !labelChanged {
				return withExitCode(exitUsage,
					errors.New("--ci-runner-label is required: name the runner label, or pass --clear to return to the default"))
			}
			var runnerLabel *string
			if labelChanged {
				label := strings.TrimSpace(mustFlagString(command, "ci-runner-label"))
				if label == "" {
					return withExitCode(exitUsage,
						errors.New("--ci-runner-label cannot be empty: name a runner label, or pass --clear to return to the default"))
				}
				runnerLabel = &label
			}
			payload, updateError := apiClient.UpdateApplicationSettings(command.Context(), runnerLabel)
			if updateError != nil {
				return updateError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("ci-runner-label", "", "GitHub Actions runner label generated pipelines request")
	setCommand.Flags().Bool("clear", false, "Drop the organisation's choice and return to the default runner")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}
