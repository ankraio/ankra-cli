package cmd

// The definition of record and its dry-run validation
// (go/internal/pipelineapi/definition.go).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// defaultPipelineDefinitionPath is the committed file `validate` reads by
// default, matching the setup PR's `.ankra/pipeline.yaml` alongside
// `.ankra/ankra.yaml`.
const defaultPipelineDefinitionPath = ".ankra/pipeline.yaml"

func newPipelineValidateCommand() *cobra.Command {
	validateCommand := &cobra.Command{
		Use:   "validate [file]",
		Short: "Dry-run a pipeline definition without writing anything",
		Long: fmt.Sprintf(`Dry-run a pipeline definition: parse it, validate it, and plan it for a
synthetic push and a synthetic pull request, without writing anything.

Defaults to %s when no file is given; with neither that file nor a
--application/--repository definition already stored, there is nothing to
validate. Passing a file validates its content directly, which is what a
'is my pipeline.yaml correct before I commit it' check wants. --spec-file is
the flag spelling of that argument, matching 'pipeline run --spec-file'.

--ref reads the definition from a git reference in the current checkout
instead of the working tree, so a candidate on a branch can be checked
before it is merged:

  ankra pipeline validate --ref origin/my-branch --application my-app

The reference is resolved locally, so a branch someone else pushed needs a
'git fetch' first, and the path is read from the repository root.`, defaultPipelineDefinitionPath),
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			filePath, pathError := pipelineValidateFilePath(command, arguments)
			if pathError != nil {
				return pathError
			}
			gitReference, _ := command.Flags().GetString("ref")
			return runPipelineValidate(command, selector, filePath, strings.TrimSpace(gitReference))
		},
	}
	registerPipelineSelectorFlags(validateCommand)
	validateCommand.Flags().String("spec-file", "",
		"Validate this definition file, the same as passing it as the argument")
	validateCommand.Flags().String("ref", "",
		"Read the definition from this git reference in the current checkout (for example origin/my-branch) instead of the working tree")
	registerStructuredOutputFlags(validateCommand)
	return validateCommand
}

// pipelineValidateFilePath answers which definition file `validate` reads.
// --spec-file is the flag spelling of the positional argument, so a caller
// that already writes `pipeline run --spec-file <file>` does not have to
// learn a second shape for the same thing; naming the file both ways is a
// usage error rather than a silent preference for one of them.
func pipelineValidateFilePath(command *cobra.Command, arguments []string) (string, error) {
	specFile, _ := command.Flags().GetString("spec-file")
	specFile = strings.TrimSpace(specFile)
	switch {
	case len(arguments) == 1 && specFile != "":
		return "", withExitCode(exitUsage,
			fmt.Errorf("pass the definition either as the argument or as --spec-file, not both"))
	case specFile != "":
		return specFile, nil
	case len(arguments) == 1:
		return arguments[0], nil
	}
	return defaultPipelineDefinitionPath, nil
}

// readPipelineDefinitionAtReference reads the definition as it stands on a
// git reference rather than in the working tree. That is what checking a
// candidate needs: until now the only way to find out whether a change was
// valid was to merge it to the default branch and run it (PLA-863). The
// reference is resolved in the local repository, so a colleague's branch
// reads as `origin/<branch>` once it has been fetched, and the path is
// resolved from the repository root so it does not depend on which
// subdirectory the command was typed in.
func readPipelineDefinitionAtReference(
	requestContext context.Context,
	gitReference string,
	filePath string,
) (string, error) {
	repositoryRoot, rootError := executeGit(requestContext, ".", "rev-parse", "--show-toplevel")
	if rootError != nil {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"--ref reads the definition from a git repository and the working directory is not inside one"))
	}
	contents, showError := executeGit(requestContext, strings.TrimSpace(repositoryRoot),
		"show", gitReference+":"+filePath)
	if showError != nil {
		return "", withExitCode(exitNotFound, fmt.Errorf("reading %s at %s: %w", filePath, gitReference, showError))
	}
	if strings.TrimSpace(contents) == "" {
		return "", withExitCode(exitNotFound, fmt.Errorf("%s is empty at %s", filePath, gitReference))
	}
	return contents, nil
}

func runPipelineValidate(
	command *cobra.Command,
	selector client.PipelineSelector,
	filePath string,
	gitReference string,
) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	var specYAML string
	if gitReference != "" {
		// A named reference is never optional: falling back to the stored
		// definition would answer "ok" about something the caller did not
		// ask about.
		contents, referenceError := readPipelineDefinitionAtReference(command.Context(), gitReference, filePath)
		if referenceError != nil {
			return referenceError
		}
		specYAML = contents
	} else {
		contents, readError := readApplicationFile(filePath)
		switch {
		case readError == nil:
			specYAML = string(contents)
		case filePath == defaultPipelineDefinitionPath && errors.Is(readError, fs.ErrNotExist):
			// The default file is optional, and only its absence is optional:
			// falling back validates whatever is already stored server-side,
			// which is the honest answer for a repository that generated its
			// pipeline rather than committing one. Any other read failure - a
			// permission denial, an unreadable directory, a transient fault -
			// is reported, because validating the stored definition and printing
			// "ok" would answer a question the caller did not ask about a file
			// this command could not read.
		default:
			return readError
		}
	}

	validation, validateError := apiClient.ValidatePipelineDefinition(command.Context(), selector, specYAML)
	if validateError != nil {
		return validateError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, validation)
	}
	printPipelineValidation(command, validation)
	if validation.Severity == "fatal" {
		return withExitCode(exitError, fmt.Errorf("the pipeline definition has fatal violations"))
	}
	return nil
}

func printPipelineValidation(command *cobra.Command, validation *client.PipelineValidation) {
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Severity: %s\n", validation.Severity)
	if len(validation.Violations) > 0 {
		_, _ = fmt.Fprintln(out, "Violations:")
		for _, violation := range validation.Violations {
			_, _ = fmt.Fprintf(out, "  - %s\n", violation)
		}
	}
	for _, event := range validation.Events {
		_, _ = fmt.Fprintf(out, "\n%s:\n", event.Event)
		if !event.Run {
			_, _ = fmt.Fprintf(out, "  Would not run: %s\n", pipelineOptionalString(event.Reason))
			continue
		}
		_, _ = fmt.Fprintf(out, "  Would run %d step(s)", len(event.Steps))
		if event.MatchedTrigger != nil && *event.MatchedTrigger != "" {
			_, _ = fmt.Fprintf(out, " (matched trigger %q)", *event.MatchedTrigger)
		}
		_, _ = fmt.Fprintln(out)
		for _, step := range event.Steps {
			_, _ = fmt.Fprintf(out, "    %s\n", plannedStepLine(step))
		}
		for _, skipped := range event.Skipped {
			_, _ = fmt.Fprintf(out, "    %s skipped: %s\n", skipped.StepKey, skipped.Message)
		}
		for _, diagnostic := range event.Diagnostics {
			_, _ = fmt.Fprintf(out, "    diagnostic: %s\n", diagnostic)
		}
	}
}

// plannedStepLine describes one node of a dry-run DAG. The resolved egress
// tier joins the stage and kind because it is the property of a planned step
// that most often explains a failure the dry run is meant to pre-empt - a
// build planned on `none` cannot pull its base image. An Ankra older than the
// field sends no tier at all, and the line then reads as it always did rather
// than claiming the step runs with no egress.
func plannedStepLine(step client.PipelinePlannedStep) string {
	if step.Network == "" {
		return fmt.Sprintf("%s (%s, %s)", step.StepKey, step.Stage, step.Kind)
	}
	return fmt.Sprintf("%s (%s, %s, %s)", step.StepKey, step.Stage, step.Kind, step.Network)
}

func newPipelineDefinitionCommand() *cobra.Command {
	definitionCommand := &cobra.Command{
		Use:   "definition",
		Short: "Manage the pipeline definition of record",
	}
	definitionCommand.AddCommand(newPipelineDefinitionGetCommand(), newPipelineDefinitionPutCommand())
	return definitionCommand
}

func newPipelineDefinitionGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the pipeline definition of record",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineDefinitionGet(command, selector)
		},
	}
	registerPipelineSelectorFlags(getCommand)
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runPipelineDefinitionGet(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	definition, getError := apiClient.GetPipelineDefinition(command.Context(), selector)
	if getError != nil {
		return getError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, definition)
	}
	printPipelineDefinition(command.OutOrStdout(), definition)
	return nil
}

func printPipelineDefinition(out io.Writer, definition *client.PipelineDefinition) {
	_, _ = fmt.Fprintf(out, "Repository:  %s/%s\n", definition.Repository.Owner, definition.Repository.Name)
	_, _ = fmt.Fprintf(out, "Source:      %s\n", definition.Source)
	_, _ = fmt.Fprintf(out, "Spec hash:   %s\n", definition.SpecHash)
	if len(definition.Violations) > 0 {
		_, _ = fmt.Fprintln(out, "Violations:")
		for _, violation := range definition.Violations {
			_, _ = fmt.Fprintf(out, "  - %s\n", violation)
		}
	}
	_, _ = fmt.Fprintln(out)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"STAGE", "KIND", "SECTION", "NEEDS"})
	for _, stage := range definition.Stages {
		writer.AppendRow(table.Row{stage.Name, stage.Kind, stage.Section, strings.Join(stage.Needs, ", ")})
	}
	writer.Render()
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, definition.SpecYAML)
}

func newPipelineDefinitionPutCommand() *cobra.Command {
	putCommand := &cobra.Command{
		Use:   "put <file>",
		Short: "Store a pipeline definition as the definition of record",
		Long: `Store a generated pipeline definition server-side, replacing the
repository's definition of record. Requires the pipelines.manage permission.

This does not touch the repository's committed .ankra/pipeline.yaml: a
committed file still wins for logic per the DescriptorOfRecord contract,
so 'put' is for a repository whose pipeline Ankra generates and stores rather
than one you author and commit yourself.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineDefinitionPut(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(putCommand)
	registerStructuredOutputFlags(putCommand)
	return putCommand
}

func runPipelineDefinitionPut(command *cobra.Command, selector client.PipelineSelector, filePath string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	contents, readError := readApplicationFile(filePath)
	if readError != nil {
		return readError
	}
	definition, putError := apiClient.PutPipelineDefinition(command.Context(), selector, string(contents))
	if putError != nil {
		return putError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, definition)
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Stored definition %s (source: %s)\n", definition.SpecHash, definition.Source)
	if len(definition.Violations) > 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "Violations:")
		for _, violation := range definition.Violations {
			_, _ = fmt.Fprintf(command.OutOrStdout(), "  - %s\n", violation)
		}
	}
	return nil
}
