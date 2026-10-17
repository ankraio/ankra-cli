package cmd

// The definition of record and its dry-run validation
// (go/internal/pipelineapi/definition.go).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"ankra/internal/affected"
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

After the per-event detail, a table shows every stage against every event
probed (push, pull_request, and tag when the pipeline declares on.tag):
"run", or "skip" with the planner's reason. Path filters - flat when.paths or
an event block's when.pull_request.paths / when.push.paths - cannot decide
without a diff, so they pass with a diagnostic; --changed-file (repeatable)
names a dry-run diff so they decide for real:

  ankra pipeline validate --repository <id> --changed-file frontend/app.ts

A stage that declares affected: (Go packages or pnpm workspace packages) is
decided at run time by the run's checkout, from the dependency graph of the
commit it checks out. With --changed-file, inside a git checkout, the CLI runs
the same analysis against the working tree and shows which of those stages
the change reaches ("graph: affected") and which it does not ("graph: not
affected", skipped and satisfied for the stages that need it).
--no-local-graph skips that and shows them as decided at run time.

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
			changedFileFlags, _ := command.Flags().GetStringArray("changed-file")
			changedFiles, changedFilesError := pipelineValidateChangedFiles(changedFileFlags)
			if changedFilesError != nil {
				return changedFilesError
			}
			return runPipelineValidate(command, selector, filePath, strings.TrimSpace(gitReference), changedFiles)
		},
	}
	registerPipelineSelectorFlags(validateCommand)
	validateCommand.Flags().String("spec-file", "",
		"Validate this definition file, the same as passing it as the argument")
	validateCommand.Flags().String("ref", "",
		"Read the definition from this git reference in the current checkout (for example origin/my-branch) instead of the working tree")
	validateCommand.Flags().StringArray("changed-file", nil,
		"Plan as if this repository-relative path changed (repeatable), so path filters decide instead of passing")
	validateCommand.Flags().Bool("no-local-graph", false,
		"Do not read the dependency graph of the local checkout for affected: stages")
	registerStructuredOutputFlags(validateCommand)
	return validateCommand
}

// pipelineValidateChangedFiles answers the dry-run diff --changed-file names:
// nil when the flag was not given, so the platform plans with no change list
// rather than an empty one, which would exclude every path-filtered stage.
//
// Each name is cleaned into the repository-relative form a provider reports
// ("./api/x.go" becomes "api/x.go"), and an absolute path or one above the
// repository root is refused: neither can match a path filter or the
// dependency graph, so planning with it would report every filtered stage as
// left out for a change that was never measured.
func pipelineValidateChangedFiles(values []string) ([]string, error) {
	var changedFiles []string
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		cleaned := path.Clean(filepath.ToSlash(trimmed))
		if path.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--changed-file %q is not a path inside the repository; name it relative to the repository root, for example api/main.go", value))
		}
		changedFiles = append(changedFiles, cleaned)
	}
	return changedFiles, nil
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
	changedFiles []string,
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

	validation, validateError := apiClient.ValidatePipelineDefinition(command.Context(), selector, specYAML, changedFiles)
	if validateError != nil {
		return validateError
	}
	if skipLocalGraph, _ := command.Flags().GetBool("no-local-graph"); !skipLocalGraph && changedFiles != nil {
		if gitReference != "" {
			// The definition was read at a reference, and the graph would be
			// read from the working tree: two different commits.
			if hasAffectedAnalysis(validation) {
				_, _ = fmt.Fprintln(command.ErrOrStderr(),
					"note: --ref reads the definition at a reference and the local graph is the working tree's, so the affected: stages are shown as decided at run time; check the reference out to read its graph")
			}
		} else {
			annotateLocalAffected(command, validation)
		}
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
	printPipelinePathSets(out, validation.PathSets)
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
			_, _ = fmt.Fprintf(out, "    %s%s\n", plannedStepLine(step), graphNote(event, step))
		}
		for _, skipped := range event.Skipped {
			_, _ = fmt.Fprintf(out, "    %s skipped: %s\n", skipped.StepKey, skipped.Message)
		}
		for _, diagnostic := range event.Diagnostics {
			_, _ = fmt.Fprintf(out, "    diagnostic: %s\n", diagnostic)
		}
	}
	printPipelineEventTable(out, validation.Events)
}

// printPipelinePathSets shows each named path set the definition declares:
// the plan below reads only the expanded lists, so this is where the names the
// author wrote meet the patterns they stand for and the lists that use them.
func printPipelinePathSets(out io.Writer, pathSets []client.PipelinePathSet) {
	if len(pathSets) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "Path sets:")
	for _, pathSet := range pathSets {
		_, _ = fmt.Fprintf(out, "  %s: %s\n", pathSet.Name, strings.Join(pathSet.Patterns, ", "))
		if len(pathSet.Includes) > 0 {
			_, _ = fmt.Fprintf(out, "    includes: %s\n", strings.Join(pathSet.Includes, ", "))
		}
		// The platform sends used_by as a list, empty for a set nothing
		// references. An absent one (nil) is a platform that did not say, and
		// is left unprinted rather than read as "unused".
		switch {
		case len(pathSet.UsedBy) > 0:
			_, _ = fmt.Fprintf(out, "    used by: %s\n", strings.Join(pathSet.UsedBy, ", "))
		case pathSet.UsedBy != nil:
			_, _ = fmt.Fprintln(out, "    used by: nothing")
		}
	}
}

// skipCell renders a skipped stage's table cell, naming the reason when the
// platform gave one. The reason is folded onto one line so a reason carrying
// a line break cannot break the table.
func skipCell(reason string) string {
	folded := strings.Join(strings.Fields(reason), " ")
	if folded == "" {
		return "skip"
	}
	return "skip: " + folded
}

// printPipelineEventTable renders the per-event plan as one row per stage and
// one column per event probed: "run", "skip: <reason>", "-" for an event
// that would start no run at all, or "not listed" for a stage the event's plan
// does not name either way - absence is reported, never read as a skip. It
// answers "which of my stages run on a pull request and which on a push" at a
// glance, which the per-event detail above spreads over many lines. Stages
// are listed in the order the events first name them.
func printPipelineEventTable(out io.Writer, events []client.PipelineEventPlan) {
	var stages []string
	isListed := map[string]bool{}
	cells := make([]map[string]string, len(events))
	header := table.Row{"STAGE"}
	for eventIndex, event := range events {
		header = append(header, strings.ToUpper(event.Event))
		cells[eventIndex] = map[string]string{}
		for _, step := range event.Steps {
			if !isListed[step.Stage] {
				isListed[step.Stage] = true
				stages = append(stages, step.Stage)
			}
			cells[eventIndex][step.Stage] = graphCell(event, step)
		}
		for _, skipped := range event.Skipped {
			if !isListed[skipped.Stage] {
				isListed[skipped.Stage] = true
				stages = append(stages, skipped.Stage)
			}
			if _, isRun := cells[eventIndex][skipped.Stage]; !isRun {
				cells[eventIndex][skipped.Stage] = skipCell(skipped.Reason)
			}
		}
	}
	if len(stages) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(header)
	for _, stage := range stages {
		row := table.Row{stage}
		for eventIndex, event := range events {
			cell, isPlanned := cells[eventIndex][stage]
			switch {
			case !event.Run:
				cell = "-"
			case !isPlanned:
				cell = "not listed"
			}
			row = append(row, cell)
		}
		writer.AppendRow(row)
	}
	writer.Render()
}

// annotateLocalAffected runs the dependency-graph analysis against the local
// checkout for every event whose plan left stages to the run's checkout, and
// records the answers on the event (LocalAffected). It never fails the
// command: a dry run that cannot read the graph locally still shows what the
// platform planned, and says on stderr why the graph was not read. Events
// asking the same question share one analysis.
func annotateLocalAffected(command *cobra.Command, validation *client.PipelineValidation) {
	stderr := command.ErrOrStderr()
	var root string
	isDigestNoted := false
	answered := map[string]map[string]bool{}
	for index := range validation.Events {
		analysis := validation.Events[index].AffectedAnalysis
		if analysis == nil {
			continue
		}
		if root == "" {
			top, topError := executeGit(command.Context(), ".", "rev-parse", "--show-toplevel")
			if topError != nil {
				_, _ = fmt.Fprintln(stderr,
					"note: not inside a git checkout, so the affected: stages are shown as decided at run time")
				return
			}
			root = strings.TrimSpace(top)
		}
		if !isDigestNoted && analysis.ProgramDigest != "" && analysis.ProgramDigest != affected.ProgramDigest() {
			isDigestNoted = true
			_, _ = fmt.Fprintln(stderr,
				"note: this CLI's copy of the dependency-graph analysis differs from the platform's; update the CLI if an answer below looks wrong")
		}
		targets := make([]affected.Target, 0, len(analysis.Targets))
		for _, target := range analysis.Targets {
			targets = append(targets, affected.Target{Stage: target.Stage, Go: target.Go, Pnpm: target.Pnpm})
		}
		key := fmt.Sprintf("%q %q", analysis.ChangedFiles, targets)
		answers, isAnswered := answered[key]
		if !isAnswered {
			var log string
			var analyseError error
			answers, log, analyseError = affected.Analyse(command.Context(), root, analysis.ChangedFiles, targets)
			if analyseError != nil {
				_, _ = fmt.Fprintf(stderr, "note: the dependency graph could not be read locally (%v); the affected: stages are shown as decided at run time\n", analyseError)
				if strings.TrimSpace(log) != "" {
					_, _ = fmt.Fprintln(stderr, strings.TrimSpace(log))
				}
				return
			}
			answered[key] = answers
		}
		validation.Events[index].LocalAffected = answers
	}
}

// hasAffectedAnalysis reports whether any event left stages to the graph.
func hasAffectedAnalysis(validation *client.PipelineValidation) bool {
	for _, event := range validation.Events {
		if event.AffectedAnalysis != nil {
			return true
		}
	}
	return false
}

// graphNote is what a dry-run step line adds for a step the dependency graph
// decides: the local answer when there is one.
func graphNote(event client.PipelineEventPlan, step client.PipelinePlannedStep) string {
	if !step.AffectedByGraph {
		return ""
	}
	isAffected, isAnswered := event.LocalAffected[step.Stage]
	switch {
	case !isAnswered:
		return " [graph: decided by the run's checkout]"
	case isAffected:
		return " [graph: affected]"
	default:
		return " [graph: not affected, skipped; the stages that need it still run]"
	}
}

// graphCell is a planned step's table cell: "run", or for a step the
// dependency graph decides, its local answer.
func graphCell(event client.PipelineEventPlan, step client.PipelinePlannedStep) string {
	if !step.AffectedByGraph {
		return "run"
	}
	isAffected, isAnswered := event.LocalAffected[step.Stage]
	switch {
	case !isAnswered:
		return "graph: decided at run time"
	case isAffected:
		return "run (graph: affected)"
	default:
		return "skip: not affected (graph)"
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
