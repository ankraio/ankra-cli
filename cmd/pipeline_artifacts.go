package cmd

// Artifacts: the store behind these routes is WS-C item C1 and does not
// exist yet, so the list always answers empty and the download always
// answers 404 today (go/internal/pipelineapi/artifacts.go). Both commands are
// wired to the real contract now so nothing about them changes once the
// store lands.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newPipelineArtifactsCommand() *cobra.Command {
	artifactsCommand := &cobra.Command{
		Use:   "artifacts <run>",
		Short: "List a pipeline run's stored artifacts",
		Long: `List a pipeline run's stored artifacts.

The artifact store has not shipped yet, so every run answers an empty list
until it does - this is not a sign that a run produced nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineArtifactsList(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(artifactsCommand)
	registerStructuredOutputFlags(artifactsCommand)
	artifactsCommand.AddCommand(newPipelineArtifactsDownloadCommand())
	return artifactsCommand
}

func runPipelineArtifactsList(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	list, listError := apiClient.ListPipelineArtifacts(command.Context(), selector, strings.TrimSpace(runID))
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, list)
	}
	if len(list.Artifacts) == 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No artifacts stored for this run.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(command.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "NAME", "STEP", "CONTENT TYPE", "SIZE", "CREATED"})
	for _, artifact := range list.Artifacts {
		writer.AppendRow(table.Row{
			artifact.ID,
			artifact.Name,
			artifact.StepID,
			artifact.ContentType,
			artifact.SizeBytes,
			formatTimeAgo(artifact.CreatedAt),
		})
	}
	writer.Render()
	return nil
}

func newPipelineArtifactsDownloadCommand() *cobra.Command {
	downloadCommand := &cobra.Command{
		Use:   "download <artifact-id>",
		Short: "Download a stored artifact",
		Long: `Download a stored artifact, following the platform's redirect to its
presigned storage URL and streaming it to disk.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineArtifactsDownload(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(downloadCommand)
	downloadCommand.Flags().String("out", "", "Local file to write the artifact to (default: the artifact id, in the current directory)")
	return downloadCommand
}

func runPipelineArtifactsDownload(command *cobra.Command, selector client.PipelineSelector, artifactID string) error {
	artifactID = strings.TrimSpace(artifactID)
	outputPath, _ := command.Flags().GetString("out")
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		// The default destination is derived from the artifact id, which the
		// server chose: only its base name is used, so an id carrying a
		// separator or an absolute path cannot decide where this command
		// writes. --out, by contrast, is the caller's own choice of
		// destination and may name a subdirectory.
		outputPath = filepath.Base(filepath.Clean(artifactID))
		if outputPath == "." || outputPath == ".." || outputPath == string(filepath.Separator) {
			return withExitCode(exitUsage,
				fmt.Errorf("the artifact id %q does not name a file to write; pass --out", artifactID))
		}
	}
	// Neither destination may walk out of the directory the command was run
	// in, so a path that escapes after cleaning is refused rather than
	// written.
	outputPath = filepath.Clean(outputPath)
	if outputPath == ".." || strings.HasPrefix(outputPath, ".."+string(filepath.Separator)) {
		return withExitCode(exitUsage,
			fmt.Errorf("refusing to write outside the current directory: %q", outputPath))
	}
	if directory := filepath.Dir(outputPath); directory != "." {
		if mkdirError := os.MkdirAll(directory, 0o755); mkdirError != nil {
			return fmt.Errorf("creating %q: %w", directory, mkdirError)
		}
	}

	destination, createError := os.Create(outputPath) // #nosec G304 -- user-supplied download destination, by design
	if createError != nil {
		return fmt.Errorf("creating %q: %w", outputPath, createError)
	}
	defer func() { _ = destination.Close() }()

	if downloadError := apiClient.DownloadPipelineArtifact(command.Context(), selector, artifactID, destination); downloadError != nil {
		_ = os.Remove(outputPath)
		return downloadError
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Downloaded artifact %s to %s\n", artifactID, outputPath)
	return nil
}
