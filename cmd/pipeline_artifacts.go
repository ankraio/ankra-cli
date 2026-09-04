package cmd

// Artifacts: a run's step logs and declared artifacts, stored as objects in
// the organisation's backup vault (go/internal/pipelineapi/artifacts.go,
// enginekit/pipelineartifacts). list reads a keyset page of the run's rows;
// download follows the route's 302 to a five-minute presigned GET on the
// vault - the bytes never pass through the platform. An artifact that
// cannot be downloaded says which fact it hit (its own Status, surfaced in
// the list) rather than a bare 404: 409 while its step has not settled, its
// upload failed, or its vault is gone; 410 once the retention sweep removed
// it.

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
		Long: `List a pipeline run's stored step logs and declared artifacts, oldest
first.

Every step's complete output is archived as a "step_log" artifact once the
step concludes ('ankra pipeline logs' reads that one automatically for a
concluded step); anything a stage's own "artifacts:" block declared shows up
as "artifact". An empty first page means the run genuinely has nothing
stored yet - no step has concluded, or the organisation has no ready backup
vault to store into. STATUS is "pending" until the upload is confirmed,
"uploaded" once "artifacts download" can fetch it, "failed" if it never
arrived (see the row's error), or "expired" once retention removed it.

The listing is paged: it prints one server page (50 rows by default) and
says when there is another, which --cursor reads. So a run with more
artifacts than one page shows its oldest first, and the list is complete
only once no further page is offered.`,
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
	registerPipelineArtifactsListFlags(artifactsCommand)
	artifactsCommand.AddCommand(newPipelineArtifactsDownloadCommand())
	return artifactsCommand
}

// registerPipelineArtifactsListFlags is shared by `pipeline artifacts` and
// `application pipeline artifacts`, so both surfaces can walk a run with
// more artifacts than one page rather than only one of them.
func registerPipelineArtifactsListFlags(command *cobra.Command) {
	command.Flags().String("cursor", "", "Page cursor from a previous listing's next_cursor")
	command.Flags().Int("limit", 0, "Maximum number of artifacts to return (server default 50, max 100)")
}

func runPipelineArtifactsList(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cursor, _ := command.Flags().GetString("cursor")
	limit, _ := command.Flags().GetInt("limit")
	list, listError := apiClient.ListPipelineArtifacts(command.Context(), selector, strings.TrimSpace(runID),
		client.ListPipelineArtifactsOptions{Cursor: strings.TrimSpace(cursor), Limit: limit})
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, list)
	}
	if len(list.Artifacts) == 0 {
		if list.NextCursor == nil {
			_, _ = fmt.Fprintln(command.OutOrStdout(), "No artifacts stored for this run.")
			return nil
		}
		// An empty page the server still offers a cursor past is "more to
		// read", not "this run stored nothing".
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No artifacts on this page.")
		renderPipelineArtifactsNextPageHint(command, list.NextCursor)
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(command.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "KIND", "NAME", "STEP", "STATUS", "CONTENT TYPE", "SIZE", "CREATED"})
	for _, artifact := range list.Artifacts {
		writer.AppendRow(table.Row{
			artifact.ID,
			artifact.Kind,
			artifact.Name,
			pipelineOptionalString(artifact.StepID),
			artifact.Status,
			artifact.ContentType,
			artifact.SizeBytes,
			formatTimeAgo(artifact.CreatedAt),
		})
	}
	writer.Render()
	renderPipelineArtifactsNextPageHint(command, list.NextCursor)
	return nil
}

// renderPipelineArtifactsNextPageHint says on stderr that the listing was a
// page, not the whole record. Both table branches route it here so the hint
// lands on the same stream either way and a piped listing keeps stdout to
// itself; `-o json` needs no hint at all, since next_cursor is in the body.
func renderPipelineArtifactsNextPageHint(command *cobra.Command, nextCursor *string) {
	if nextCursor == nil {
		return
	}
	_, _ = fmt.Fprintf(command.ErrOrStderr(),
		"\nMore artifacts available: pass --cursor %s to see the next page.\n", *nextCursor)
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
