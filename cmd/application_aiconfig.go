package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// The application AI-lane configuration commands. Each application follows
// the organisation's AI defaults until it overrides them; these commands
// read the effective configuration, replace the override, and reset the
// application back to the organisation defaults.

func newApplicationAIConfigCommand() *cobra.Command {
	aiConfigCommand := &cobra.Command{
		Use:   "ai-config",
		Short: "Read, set, or reset the application's AI lane configuration",
		Long: `Read, set, or reset which AI lanes run on this application's repository
(pull request review, demo URL, and the rest), on which model, and within
what limits. An application follows the organisation's defaults until an
override is set here.`,
	}
	aiConfigCommand.AddCommand(
		newApplicationAIConfigGetCommand(),
		newApplicationAIConfigSetCommand(),
		newApplicationAIConfigClearCommand(),
	)
	return aiConfigCommand
}

func newApplicationAIConfigGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id>",
		Short: "Show the application's effective AI lane configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationAIConfig(command.Context(),
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

func newApplicationAIConfigSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Replace the application's AI lane configuration",
		Long: `Replace the application's AI lane configuration with a JSON document.
Start from the current one: 'ankra application ai-config get <id> -o json',
edit it, and pass the file back with --file (or '-' to read stdin).`,
		Example: `  ankra application ai-config get <application-id> -o json > lanes.json
  ankra application ai-config set <application-id> --file lanes.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			filePath, _ := command.Flags().GetString("file")
			if filePath == "" {
				return withExitCode(exitUsage,
					errors.New("--file is required: the AI configuration JSON, or '-' for stdin"))
			}
			var content []byte
			var readError error
			if filePath == "-" {
				content, readError = io.ReadAll(command.InOrStdin())
			} else {
				content, readError = os.ReadFile(filePath)
			}
			if readError != nil {
				return fmt.Errorf("reading configuration: %w", readError)
			}
			if !json.Valid(content) {
				return withExitCode(exitUsage,
					fmt.Errorf("%s does not contain valid JSON", filePath))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, updateError := apiClient.UpdateApplicationAIConfig(command.Context(),
				applicationID, json.RawMessage(content))
			if updateError != nil {
				return updateError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("file", "", "AI configuration JSON file, or '-' to read stdin (required)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newApplicationAIConfigClearCommand() *cobra.Command {
	clearCommand := &cobra.Command{
		Use:   "clear <application-id>",
		Short: "Reset the application to the organisation's AI defaults",
		Args:  cobra.ExactArgs(1),
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
				fmt.Sprintf("Reset the AI configuration of application %q to the organisation defaults? [y/N]: ",
					strings.TrimSpace(arguments[0])),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, resetError := apiClient.ResetApplicationAIConfig(command.Context(), applicationID)
			if resetError != nil {
				return resetError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	clearCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clearCommand)
	return clearCommand
}
