package cmd

// The definition of record and its dry-run validation
// (go/internal/pipelineapi/definition.go).

import (
	"fmt"
	"io"
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
'is my pipeline.yaml correct before I commit it' check wants.`, defaultPipelineDefinitionPath),
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			filePath := defaultPipelineDefinitionPath
			if len(arguments) == 1 {
				filePath = arguments[0]
			}
			return runPipelineValidate(command, selector, filePath)
		},
	}
	registerPipelineSelectorFlags(validateCommand)
	registerStructuredOutputFlags(validateCommand)
	return validateCommand
}

func runPipelineValidate(command *cobra.Command, selector client.PipelineSelector, filePath string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	var specYAML string
	contents, readError := readApplicationFile(filePath)
	switch {
	case readError == nil:
		specYAML = string(contents)
	case filePath == defaultPipelineDefinitionPath:
		// The default file is optional: falling back validates whatever is
		// already stored server-side, which is the honest answer for a
		// repository that generated its pipeline rather than committing one.
	default:
		return readError
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
			_, _ = fmt.Fprintf(out, "    %s (%s, %s)\n", step.StepKey, step.Stage, step.Kind)
		}
		for _, skipped := range event.Skipped {
			_, _ = fmt.Fprintf(out, "    %s skipped: %s\n", skipped.StepKey, skipped.Message)
		}
		for _, diagnostic := range event.Diagnostics {
			_, _ = fmt.Fprintf(out, "    diagnostic: %s\n", diagnostic)
		}
	}
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
