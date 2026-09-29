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
	"fmt"
	"strings"

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
		newApplicationPipelineConvertCommand(),
	)
	return pipelineCommand
}

// newApplicationPipelineConvertCommand is the one-click conversion of an
// application that still builds from a GitHub Actions workflow onto Ankra
// Pipelines (cluster ankra-484en). It has no `ankra pipeline` twin: the
// conversion is judged from the application's stored pipeline_source, which
// only an application carries.
func newApplicationPipelineConvertCommand() *cobra.Command {
	convertCommand := &cobra.Command{
		Use:   "convert <application-id>",
		Short: "Convert the application from its GitHub workflow onto Ankra Pipelines",
		Long: `Convert an application that still builds from a GitHub Actions workflow onto
Ankra Pipelines, with one call.

Ankra reads the workflow, converts it to a .ankra/pipeline.yaml, stores that as
the application's pipeline definition of record and builds the next push
through it. It switches the generated workflow off on GitHub so the two never
build the same commit twice, and opens a pull request that commits the pipeline
file and removes Ankra's generated workflow file. A workflow the repository
wrote itself is never touched. Nothing about building waits on the pull
request merging.

--keep-workflows leaves the generated workflow file in the repository (it stays
switched off). Calling again while the pull request is still open answers the
same conversion; an application already on Ankra Pipelines is refused with
nothing to convert.`,
		Example: `  ankra application pipeline convert <application-id>
  ankra application pipeline convert <application-id> --keep-workflows
  ankra application pipeline convert <application-id> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			keepWorkflows, _ := command.Flags().GetBool("keep-workflows")
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			conversion, convertError := apiClient.ConvertApplicationPipeline(command.Context(), applicationID, keepWorkflows)
			if convertError != nil {
				return convertError
			}
			if rendered, renderError := renderStructured(command, conversion); rendered || renderError != nil {
				return renderError
			}
			return renderPipelineConversion(command, conversion)
		},
	}
	convertCommand.Flags().Bool("keep-workflows", false,
		"Leave Ankra's generated workflow file in the repository instead of removing it in the pull request")
	registerStructuredOutputFlags(convertCommand)
	return convertCommand
}

// renderPipelineConversion says what the click did, in the order a reader
// acts on it: the sentence, the pull request, what leaves the repository,
// what is already switched off, and the one thing they may still have to do
// by hand.
func renderPipelineConversion(command *cobra.Command, conversion *client.PipelineConversion) error {
	output := command.OutOrStdout()
	if _, writeError := fmt.Fprintln(output, conversion.Message); writeError != nil {
		return writeError
	}
	if conversion.PullRequestURL != "" {
		if _, writeError := fmt.Fprintf(output, "Pull request: %s\n", conversion.PullRequestURL); writeError != nil {
			return writeError
		}
	}
	if len(conversion.RemovedPaths) > 0 {
		if _, writeError := fmt.Fprintf(output, "Removed by the pull request: %s\n",
			strings.Join(conversion.RemovedPaths, ", ")); writeError != nil {
			return writeError
		}
	}
	if len(conversion.DisabledWorkflows) > 0 {
		if _, writeError := fmt.Fprintf(output, "Switched off on GitHub: %s\n",
			strings.Join(conversion.DisabledWorkflows, ", ")); writeError != nil {
			return writeError
		}
	}
	if conversion.DisableWorkflowsMessage != "" {
		if _, writeError := fmt.Fprintf(output, "Warning: %s\n", conversion.DisableWorkflowsMessage); writeError != nil {
			return writeError
		}
	}
	return nil
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
			return runPipelineDispatch(command, pipelineTarget{
				selector: client.PipelineSelector{ApplicationID: applicationID},
			})
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
		Use:   "get <application-id> [run]",
		Short: "Show a pipeline run's detail, or wait on or watch it",
		Long:  pipelineGetLongHelp,
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			runID := ""
			if len(arguments) == 2 {
				runID = arguments[1]
			}
			return runPipelineGet(command, client.PipelineSelector{ApplicationID: applicationID}, runID)
		},
	}
	registerPipelineGetFlags(getCommand)
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
	registerPipelineRerunFlags(rerunCommand)
	registerStructuredOutputFlags(rerunCommand)
	return rerunCommand
}

func newApplicationPipelineLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <application-id> <run>",
		Short: "Show a pipeline step's output",
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
	registerPipelineArtifactsListFlags(artifactsCommand)
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
			filePath, pathError := pipelineValidateFilePath(command, arguments[1:])
			if pathError != nil {
				return pathError
			}
			gitReference, _ := command.Flags().GetString("ref")
			return runPipelineValidate(command, client.PipelineSelector{ApplicationID: applicationID},
				filePath, strings.TrimSpace(gitReference))
		},
	}
	validateCommand.Flags().String("spec-file", "",
		"Validate this definition file, the same as passing it as the argument")
	validateCommand.Flags().String("ref", "",
		"Read the definition from this git reference in the current checkout (for example origin/my-branch) instead of the working tree")
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
