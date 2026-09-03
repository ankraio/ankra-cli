package cmd

// `ankra application pipeline …`: the by-application twin of `ankra
// pipeline …` (cmd/pipeline.go and its siblings). Every leaf here resolves
// the leading <application-id> argument the way every other
// `application <subcommand> <application-id>` command does
// (resolveApplicationArgument) and then calls the exact same runPipeline*
// function the top-level command calls, forcing the selector from the
// resolved application id - there is exactly one place each behaviour is
// implemented.

import (
	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func newApplicationPipelineCommand() *cobra.Command {
	pipelineCommand := &cobra.Command{
		Use:     "pipeline",
		Aliases: []string{"pipelines"},
		Short:   "Manage the application's pipeline",
	}
	pipelineCommand.AddCommand(
		newApplicationPipelineRunCommand(),
		newApplicationPipelineListCommand(),
		newApplicationPipelineGetCommand(),
		newApplicationPipelineCancelCommand(),
		newApplicationPipelineRerunCommand(),
		newApplicationPipelineLogsCommand(),
		newApplicationPipelineArtifactsCommand(),
		newApplicationPipelineValidateCommand(),
		newApplicationPipelineDefinitionCommand(),
		newApplicationPipelineSchedulesCommand(),
	)
	return pipelineCommand
}

func newApplicationPipelineRunCommand() *cobra.Command {
	runCommand := &cobra.Command{
		Use:   "run <application-id>",
		Short: "Dispatch a manual run of the application's pipeline",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineDispatch(command, client.PipelineSelector{ApplicationID: applicationID})
		},
	}
	registerPipelineRunDispatchFlags(runCommand)
	registerStructuredOutputFlags(runCommand)
	return runCommand
}

func newApplicationPipelineListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <application-id>",
		Aliases: []string{"ls"},
		Short:   "List the application's pipeline runs",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineList(command, client.PipelineSelector{ApplicationID: applicationID})
		},
	}
	registerPipelineListFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationPipelineGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id> <run>",
		Short: "Show a pipeline run's detail",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineGet(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationPipelineCancelCommand() *cobra.Command {
	cancelCommand := &cobra.Command{
		Use:     "cancel <application-id> <run>",
		Aliases: []string{"stop"},
		Short:   "Cancel a pipeline run that has not concluded",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineCancel(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	registerStructuredOutputFlags(cancelCommand)
	return cancelCommand
}

func newApplicationPipelineRerunCommand() *cobra.Command {
	rerunCommand := &cobra.Command{
		Use:   "rerun <application-id> <run>",
		Short: "Re-run a concluded pipeline run",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineRerun(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	rerunCommand.Flags().Bool("failed-only", false, "Re-run only the steps that did not succeed, and whatever depends on them")
	rerunCommand.Flags().Bool("wait", false, "Wait for the new run to conclude before returning")
	registerStructuredOutputFlags(rerunCommand)
	return rerunCommand
}

func newApplicationPipelineLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <application-id> <run>",
		Short: "Show a pipeline step's live output",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineLogs(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	registerPipelineLogsFlags(logsCommand)
	return logsCommand
}

func newApplicationPipelineArtifactsCommand() *cobra.Command {
	artifactsCommand := &cobra.Command{
		Use:   "artifacts <application-id> <run>",
		Short: "List a pipeline run's stored artifacts",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineArtifactsList(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	registerStructuredOutputFlags(artifactsCommand)
	artifactsCommand.AddCommand(newApplicationPipelineArtifactsDownloadCommand())
	return artifactsCommand
}

func newApplicationPipelineArtifactsDownloadCommand() *cobra.Command {
	downloadCommand := &cobra.Command{
		Use:   "download <application-id> <artifact-id>",
		Short: "Download a stored artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineArtifactsDownload(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	downloadCommand.Flags().String("out", "", "Local file to write the artifact to (default: the artifact id, in the current directory)")
	return downloadCommand
}

func newApplicationPipelineValidateCommand() *cobra.Command {
	validateCommand := &cobra.Command{
		Use:   "validate <application-id> [file]",
		Short: "Dry-run a pipeline definition without writing anything",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			filePath := defaultPipelineDefinitionPath
			if len(arguments) == 2 {
				filePath = arguments[1]
			}
			return runPipelineValidate(command, client.PipelineSelector{ApplicationID: applicationID}, filePath)
		},
	}
	registerStructuredOutputFlags(validateCommand)
	return validateCommand
}

func newApplicationPipelineDefinitionCommand() *cobra.Command {
	definitionCommand := &cobra.Command{
		Use:   "definition",
		Short: "Manage the application pipeline's definition of record",
	}
	definitionCommand.AddCommand(
		&cobra.Command{
			Use:   "get <application-id>",
			Short: "Show the pipeline definition of record",
			Args:  cobra.ExactArgs(1),
			RunE: func(command *cobra.Command, arguments []string) error {
				applicationID, resolveError := resolveApplicationArgument(command, arguments)
				if resolveError != nil {
					return resolveError
				}
				return runPipelineDefinitionGet(command, client.PipelineSelector{ApplicationID: applicationID})
			},
		},
		&cobra.Command{
			Use:   "put <application-id> <file>",
			Short: "Store a pipeline definition as the definition of record",
			Args:  cobra.ExactArgs(2),
			RunE: func(command *cobra.Command, arguments []string) error {
				applicationID, resolveError := resolveApplicationArgument(command, arguments)
				if resolveError != nil {
					return resolveError
				}
				return runPipelineDefinitionPut(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
			},
		},
	)
	for _, subcommand := range definitionCommand.Commands() {
		registerStructuredOutputFlags(subcommand)
	}
	return definitionCommand
}

func newApplicationPipelineSchedulesCommand() *cobra.Command {
	schedulesCommand := &cobra.Command{
		Use:   "schedules",
		Short: "Manage the application pipeline's cron schedules",
	}

	listCommand := &cobra.Command{
		Use:     "list <application-id>",
		Aliases: []string{"ls"},
		Short:   "List the pipeline's cron schedules",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineSchedulesList(command, client.PipelineSelector{ApplicationID: applicationID})
		},
	}
	registerStructuredOutputFlags(listCommand)

	createCommand := &cobra.Command{
		Use:   "create <application-id>",
		Short: "Add a cron schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineSchedulesCreate(command, client.PipelineSelector{ApplicationID: applicationID})
		},
	}
	createCommand.Flags().String("cron", "", "Cron expression (required)")
	createCommand.Flags().String("timezone", "", "IANA timezone the cron is evaluated in (default UTC)")
	createCommand.Flags().String("ref", "", "Git reference the schedule runs at (default the repository's default branch)")
	createCommand.Flags().StringArray("input", nil, "Dispatch input as key=value (repeatable)")
	createCommand.Flags().Bool("enabled", true, "Whether the schedule fires")
	registerStructuredOutputFlags(createCommand)

	updateCommand := &cobra.Command{
		Use:   "update <application-id> <schedule-id>",
		Short: "Change a cron schedule",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineSchedulesUpdate(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	updateCommand.Flags().String("cron", "", "New cron expression")
	updateCommand.Flags().String("timezone", "", "New IANA timezone")
	updateCommand.Flags().String("ref", "", "New git reference")
	updateCommand.Flags().StringArray("input", nil, "Replace the dispatch inputs entirely, as key=value (repeatable)")
	updateCommand.Flags().Bool("enabled", false, "Enable the schedule")
	updateCommand.Flags().Bool("disabled", false, "Disable the schedule")
	registerStructuredOutputFlags(updateCommand)

	deleteCommand := &cobra.Command{
		Use:     "delete <application-id> <schedule-id>",
		Aliases: []string{"rm"},
		Short:   "Remove a cron schedule",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			return runPipelineSchedulesDelete(command, client.PipelineSelector{ApplicationID: applicationID}, arguments[1])
		},
	}
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")

	schedulesCommand.AddCommand(listCommand, createCommand, updateCommand, deleteCommand)
	return schedulesCommand
}
