package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// canvasMaximumCharacters is the largest page the platform accepts.
const canvasMaximumCharacters = 200000

// canvasMaximumBytes is four bytes per character, the most a valid
// 200000-character UTF-8 file can occupy. A larger file is over the limit
// without being read.
const canvasMaximumBytes = canvasMaximumCharacters * utf8.UTFMax

var canvasLanguageByExtension = map[string]string{
	".html":     "html",
	".htm":      "html",
	".svg":      "svg",
	".md":       "markdown",
	".markdown": "markdown",
}

var canvasSupportedLanguages = []string{"html", "svg", "markdown"}

var canvasCmd = &cobra.Command{
	Use:   "canvas",
	Short: "Publish canvas pages at a public, unguessable URL",
	Long: `Publish an HTML, SVG or Markdown file as a static page on the Ankra pages
host, list the pages your organisation has published, and unpublish them.

  ankra canvas publish report.html --title "Q3 report"
  ankra canvas list
  ankra canvas unpublish 3f0a2f7e-0000-4000-8000-000000000001

Anyone holding the URL can open the page, so publish nothing you would not
hand to that audience. Pages are static snapshots, expire after a retention
period (--ttl-hours) and can be unpublished at any time.`,
}

var canvasPublishCmd = &cobra.Command{
	Use:   "publish <file>",
	Short: "Publish a file as a canvas page",
	Long: `Publish an HTML, SVG or Markdown file and print its public URL.

The language is inferred from the file extension (.html and .htm, .svg, .md)
and can be set with --language. Other languages are refused, as is a file over
200000 characters.

Publishing the same file again updates the same page and keeps its URL: when
--conversation-id and --artifact-id are not given, both are derived from a hash
of the file's absolute path, so the page is identified by where the file lives.
Moving or renaming the file publishes a new page. Pass both ids to take control
of the identity yourself, for example to update a page published from the portal
canvas.

The URL is printed last, on its own line, so scripts can take the final line of
output; with -o json the publication is printed as a JSON document instead.`,
	Example: `  ankra canvas publish report.html
  ankra canvas publish notes.md --title "Release notes" --ttl-hours 24
  URL=$(ankra canvas publish diagram.svg | tail -n 1)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		request, err := buildCanvasPublicationRequest(cmd, args[0])
		if err != nil {
			return withExitCode(exitUsage, err)
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		result, err := apiClient.PublishCanvasPublication(ctx, request)
		if err != nil {
			return canvasPagesError("publish canvas page", err)
		}
		if out != outputDefault {
			_, err := renderStructured(cmd, result.CanvasPublication)
			return err
		}
		verb := "Updated"
		if result.Created {
			verb = "Published"
		}
		writer := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(writer, "%s %q (%s, %d bytes), live until %s.\n",
			verb, result.Title, result.Language, result.SizeBytes, result.ExpiresAt)
		_, _ = fmt.Fprintln(writer, result.URL)
		return nil
	},
}

var canvasListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the canvas pages your organisation has published",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		cursor, _ := cmd.Flags().GetString("cursor")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 0 {
			return withExitCode(exitUsage, errors.New("--limit must not be negative"))
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListCanvasPublications(ctx, cursor, limit)
		if err != nil {
			return canvasPagesError("list canvas pages", err)
		}
		if out != outputDefault {
			_, err := renderStructured(cmd, list)
			return err
		}
		if list.NextCursor != "" {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "More pages available: re-run with --cursor %s\n", list.NextCursor)
		}
		renderCanvasPublications(cmd.OutOrStdout(), list.Items)
		return nil
	},
}

var canvasUnpublishCmd = &cobra.Command{
	Use:   "unpublish <id>",
	Short: "Unpublish a canvas page",
	Long: `Unpublish a page by its id (shown by 'ankra canvas list'). Its URL stops
serving the page immediately.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		publicationID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Unpublish canvas page %q? [y/N]: ", publicationID),
			yes,
		); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if err := apiClient.UnpublishCanvasPublication(ctx, publicationID); err != nil {
			if errors.Is(err, client.ErrCanvasPublicationNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("canvas page %q not found; run 'ankra canvas list'", publicationID))
			}
			return canvasPagesError("unpublish canvas page", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Canvas page %q unpublished.\n", publicationID)
		return nil
	},
}

// buildCanvasPublicationRequest validates the file and flags client side and
// assembles the publish payload.
func buildCanvasPublicationRequest(cmd *cobra.Command, path string) (client.CanvasPublicationRequest, error) {
	var request client.CanvasPublicationRequest

	title, _ := cmd.Flags().GetString("title")
	languageFlag, _ := cmd.Flags().GetString("language")
	conversationID, _ := cmd.Flags().GetString("conversation-id")
	artifactID, _ := cmd.Flags().GetString("artifact-id")

	if (conversationID == "") != (artifactID == "") {
		return request, errors.New("--conversation-id and --artifact-id must be given together")
	}

	language, err := resolveCanvasLanguage(path, languageFlag)
	if err != nil {
		return request, err
	}

	if cmd.Flags().Changed("ttl-hours") {
		ttlHours, _ := cmd.Flags().GetInt("ttl-hours")
		if ttlHours < 1 {
			return request, errors.New("--ttl-hours must be at least 1")
		}
		request.TTLHours = &ttlHours
	}

	content, err := readCanvasContent(path)
	if err != nil {
		return request, err
	}

	if conversationID == "" {
		absolutePath, err := canvasStablePath(path)
		if err != nil {
			return request, err
		}
		conversationID, artifactID = canvasIdentityForPath(absolutePath)
	}
	if title == "" {
		base := filepath.Base(path)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	request.ConversationID = conversationID
	request.ArtifactID = artifactID
	request.Title = title
	request.Language = language
	request.Content = content
	return request, nil
}

// resolveCanvasLanguage picks the page language from --language or the file
// extension and refuses anything the platform would not publish.
func resolveCanvasLanguage(path, languageFlag string) (string, error) {
	if languageFlag != "" {
		language := strings.ToLower(languageFlag)
		for _, supported := range canvasSupportedLanguages {
			if language == supported {
				return language, nil
			}
		}
		return "", fmt.Errorf("language %q cannot be published: only %s are supported",
			languageFlag, strings.Join(canvasSupportedLanguages, ", "))
	}
	extension := strings.ToLower(filepath.Ext(path))
	if language, ok := canvasLanguageByExtension[extension]; ok {
		return language, nil
	}
	if extension == "" {
		return "", fmt.Errorf("cannot infer the language of %q without a file extension: use .html, .svg or .md, or pass --language", path)
	}
	return "", fmt.Errorf("cannot publish %q files: only %s are supported (extensions .html, .htm, .svg, .md); pass --language to publish this file as one of them",
		extension, strings.Join(canvasSupportedLanguages, ", "))
}

// readCanvasContent reads the file, refusing one over the character limit.
func readCanvasContent(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("file %q does not exist", path)
		}
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory: publish a single file", path)
	}
	if info.Size() > canvasMaximumBytes {
		return "", canvasTooLargeError(path)
	}
	data, err := io.ReadAll(io.LimitReader(file, canvasMaximumBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	content := string(data)
	if utf8.RuneCountInString(content) > canvasMaximumCharacters {
		return "", canvasTooLargeError(path)
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("%q is empty: nothing to publish", path)
	}
	return content, nil
}

func canvasTooLargeError(path string) error {
	return fmt.Errorf("%q is over the %d character limit for a canvas page", path, canvasMaximumCharacters)
}

// canvasStablePath returns the absolute path of the file with symlinks
// resolved, so the same file reached through different routes keeps one
// identity.
func canvasStablePath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	if resolved, err := filepath.EvalSymlinks(absolutePath); err == nil {
		return resolved, nil
	}
	return absolutePath, nil
}

// canvasIdentityForPath derives the conversation and artifact ids used when
// none are given. They are UUID-shaped so any id validation on the platform
// accepts them, and depend only on the absolute path.
func canvasIdentityForPath(absolutePath string) (string, string) {
	return canvasDerivedUUID("ankra-cli-canvas-conversation\x00" + absolutePath),
		canvasDerivedUUID("ankra-cli-canvas-artifact\x00" + absolutePath)
}

func canvasDerivedUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	identifier := sum[:16]
	identifier[6] = (identifier[6] & 0x0f) | 0x50
	identifier[8] = (identifier[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", identifier[0:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:16])
}

// canvasPagesError turns a platform refusal into a message that says what to do.
func canvasPagesError(action string, err error) error {
	var pagesError *client.CanvasPagesError
	if errors.As(err, &pagesError) {
		switch pagesError.Code {
		case "canvas_pages_disabled":
			return fmt.Errorf("%s: canvas pages are not enabled on this Ankra platform", action)
		case "unsupported_language":
			return withExitCode(exitUsage, fmt.Errorf("%s: only %s can be published", action, strings.Join(canvasSupportedLanguages, ", ")))
		case "content_too_large":
			return withExitCode(exitUsage, fmt.Errorf("%s: the page is over the %d character limit", action, canvasMaximumCharacters))
		case "invalid_ttl":
			return withExitCode(exitUsage, fmt.Errorf("%s: --ttl-hours is outside the range this platform allows", action))
		case "publication_taken_down":
			return fmt.Errorf("%s: this page was taken down; it cannot be republished", action)
		case "publication_owned_by_another_member":
			return fmt.Errorf("%s: this page was published by another member; ask them or an organisation admin", action)
		case "publication_limit_reached":
			return fmt.Errorf("%s: your organisation has reached its limit of published pages; run 'ankra canvas unpublish <id>' on one you no longer need", action)
		}
		if pagesError.StatusCode == 503 {
			return fmt.Errorf("%s: canvas pages are unavailable right now: %w", action, err)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}

func renderCanvasPublications(out io.Writer, publications []client.CanvasPublication) {
	if len(publications) == 0 {
		_, _ = fmt.Fprintln(out, "No canvas pages published.")
		return
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "TITLE", "LANGUAGE", "STATUS", "EXPIRES", "URL"})
	for _, publication := range publications {
		t.AppendRow(table.Row{publication.ID, truncateForDisplay(publication.Title, 40), publication.Language,
			publication.Status, publication.ExpiresAt, publication.URL})
	}
	t.Render()
}

func init() {
	registerStructuredOutputFlags(canvasPublishCmd, canvasListCmd)
	canvasPublishCmd.Flags().String("title", "", "Page title (default: the file name without its extension)")
	canvasPublishCmd.Flags().String("language", "", "Page language: html, svg or markdown (default: inferred from the extension)")
	canvasPublishCmd.Flags().String("conversation-id", "", "Conversation id identifying the page; give together with --artifact-id (default: derived from the file's absolute path)")
	canvasPublishCmd.Flags().String("artifact-id", "", "Artifact id identifying the page; give together with --conversation-id (default: derived from the file's absolute path)")
	canvasPublishCmd.Flags().Int("ttl-hours", 0, "Hours the page stays live (default and maximum are set by the platform)")
	canvasListCmd.Flags().Int("limit", 0, "Maximum number of pages to return (default: the platform's page size)")
	canvasListCmd.Flags().String("cursor", "", "Continue from the cursor printed by a previous list")
	canvasUnpublishCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	canvasCmd.AddCommand(canvasPublishCmd)
	canvasCmd.AddCommand(canvasListCmd)
	canvasCmd.AddCommand(canvasUnpublishCmd)
	rootCmd.AddCommand(canvasCmd)
}
