package cmd

// The canvas library (ankra-hq41xb.22): the saved canvases behind the
// portal's canvas pages, with their revisions and comment threads.
// `ankra canvas list|publish|unpublish` are the published pages; these
// commands are the canvases themselves.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// canvasLibraryMaximumBytes is the largest canvas content the platform saves.
const canvasLibraryMaximumBytes = 524288

const canvasLibraryRequestTimeout = 60 * time.Second

var (
	canvasLibraryKinds        = []string{"page", "dashboard"}
	canvasLibraryLanguages    = []string{"json", "html", "svg", "markdown"}
	canvasLibraryVisibilities = []string{"private", "organisation"}
)

type canvasSummary struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Title           string  `json:"title"`
	Visibility      string  `json:"visibility"`
	IsOwner         bool    `json:"is_owner"`
	IsPinned        bool    `json:"is_pinned"`
	Language        string  `json:"language"`
	DraftRevisionID *string `json:"draft_revision_id"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type canvasRevision struct {
	ID         string `json:"id"`
	Number     int    `json:"number"`
	Language   string `json:"language"`
	SizeBytes  int    `json:"size_bytes"`
	AuthorKind string `json:"author_kind"`
	CreatedAt  string `json:"created_at"`
	Content    string `json:"content"`
}

type canvasDocument struct {
	canvasSummary
	Draft canvasRevision `json:"draft"`
}

type canvasComment struct {
	ID              string  `json:"id"`
	ParentCommentID *string `json:"parent_comment_id"`
	RevisionNumber  *int    `json:"revision_number"`
	ElementID       *string `json:"element_id"`
	Body            string  `json:"body"`
	AuthorUserID    string  `json:"author_user_id"`
	AuthorEmail     *string `json:"author_email"`
	ResolvedAt      *string `json:"resolved_at"`
	CreatedAt       string  `json:"created_at"`
}

// canvasLibraryRun wraps a canvas library command body: it validates -o,
// bounds the request, and turns a platform refusal into an error that carries
// the right exit code.
func canvasLibraryRun(action string, run func(ctx context.Context, command *cobra.Command, arguments []string,
	format outputFormat) error) func(*cobra.Command, []string) error {
	return func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		ctx, cancel := context.WithTimeout(command.Context(), canvasLibraryRequestTimeout)
		defer cancel()
		return canvasLibraryError(action, run(ctx, command, arguments, format))
	}
}

// canvasLibraryError says what a canvas library refusal means for the caller:
// a canvas, revision or comment that is not there exits 3, one that belongs
// to someone else exits 7, and a rejected request exits 2.
func canvasLibraryError(action string, runError error) error {
	var refusal *client.CanvasPagesError
	if runError == nil || !errors.As(runError, &refusal) {
		return runError
	}
	switch refusal.Code {
	case "canvas_not_found":
		return withExitCode(exitNotFound, fmt.Errorf(
			"%s: no such canvas, or it is another member's private canvas; run 'ankra canvas library'", action))
	case "canvas_revision_not_found":
		return withExitCode(exitNotFound, fmt.Errorf(
			"%s: the canvas has no such revision; run 'ankra canvas revisions list <canvas-id>'", action))
	case "comment_not_found":
		return withExitCode(exitNotFound, fmt.Errorf(
			"%s: the canvas has no such comment; run 'ankra canvas comments list <canvas-id> --include-resolved'", action))
	case "canvas_owned_by_another_member":
		return withExitCode(exitForbidden, fmt.Errorf("%s: the canvas belongs to another member; only its owner can change it", action))
	case "comment_forbidden":
		return withExitCode(exitForbidden, fmt.Errorf("%s: you may not do that to this comment", action))
	case "canvas_limit_reached":
		return fmt.Errorf("%s: the organisation has reached its limit of canvases; delete one with 'ankra canvas delete <canvas-id>'", action)
	}
	wrapped := fmt.Errorf("%s: %w", action, runError)
	switch refusal.StatusCode {
	case http.StatusNotFound:
		return withExitCode(exitNotFound, wrapped)
	case http.StatusForbidden:
		return withExitCode(exitForbidden, wrapped)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return withExitCode(exitUsage, wrapped)
	}
	return wrapped
}

// renderCanvasPayload prints the platform's document for -o json|yaml and
// reports whether it did.
func renderCanvasPayload(command *cobra.Command, format outputFormat, payload json.RawMessage) (bool, error) {
	if format == outputDefault {
		return false, nil
	}
	_, renderError := renderStructured(command, payload)
	return true, renderError
}

func canvasPageLimit(command *cobra.Command) (int, error) {
	limit, _ := command.Flags().GetInt("limit")
	if limit < 0 || limit > 100 {
		return 0, withExitCode(exitUsage, errors.New("--limit must be between 1 and 100"))
	}
	return limit, nil
}

func canvasChoice(flagName string, value string, choices []string) error {
	for _, choice := range choices {
		if value == choice {
			return nil
		}
	}
	return withExitCode(exitUsage, fmt.Errorf("--%s must be one of %s, got %q", flagName, strings.Join(choices, ", "), value))
}

func printCanvasNextCursor(command *cobra.Command, nextCursor *string) {
	if nextCursor != nil && *nextCursor != "" {
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "More available: re-run with --cursor %s\n", *nextCursor)
	}
}

func newCanvasLibraryCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "library",
		Short: "List the saved canvases you can see",
		Long: `List the canvases in the organisation's library that you can see: your own and
every canvas shared with the organisation, most recently updated first.

These are the saved canvases themselves. 'ankra canvas list' lists the pages
published at a public URL.`,
		Example: `  ankra canvas library
  ankra canvas library --query report --kind page
  ankra canvas library --pinned -o json`,
		Args: cobra.NoArgs,
		RunE: canvasLibraryRun("list canvases", func(ctx context.Context, command *cobra.Command, _ []string, format outputFormat) error {
			limit, limitError := canvasPageLimit(command)
			if limitError != nil {
				return limitError
			}
			options := client.CanvasListOptions{
				Cursor: mustFlagString(command, "cursor"),
				Limit:  limit,
				Query:  mustFlagString(command, "query"),
				Kind:   mustFlagString(command, "kind"),
			}
			options.IsPinnedOnly, _ = command.Flags().GetBool("pinned")
			if options.Kind != "" {
				if kindError := canvasChoice("kind", options.Kind, canvasLibraryKinds); kindError != nil {
					return kindError
				}
			}
			payload, listError := apiClient.ListCanvases(ctx, options)
			if listError != nil {
				return listError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var listing struct {
				Items      []canvasSummary `json:"items"`
				NextCursor *string         `json:"next_cursor"`
			}
			if decodeError := json.Unmarshal(payload, &listing); decodeError != nil {
				return fmt.Errorf("parsing the canvases: %w", decodeError)
			}
			printCanvasNextCursor(command, listing.NextCursor)
			if len(listing.Items) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), "No canvases found.")
				return nil
			}
			canvasTable := table.NewWriter()
			canvasTable.SetOutputMirror(command.OutOrStdout())
			canvasTable.SetStyle(table.StyleRounded)
			canvasTable.AppendHeader(table.Row{"ID", "TITLE", "KIND", "LANGUAGE", "VISIBILITY", "OWNER", "UPDATED"})
			for _, canvas := range listing.Items {
				owner := "another member"
				if canvas.IsOwner {
					owner = "you"
				}
				canvasTable.AppendRow(table.Row{canvas.ID, truncateForDisplay(canvas.Title, 40), canvas.Kind,
					canvas.Language, canvas.Visibility, owner, canvas.UpdatedAt})
			}
			canvasTable.Render()
			return nil
		}),
	}
	command.Flags().Int("limit", 0, "Canvases to return, 1 to 100 (default: the platform's page size, 25)")
	command.Flags().String("cursor", "", "Continue from the cursor printed by a previous page")
	command.Flags().String("query", "", "Only canvases whose title contains this text")
	command.Flags().String("kind", "", "Only canvases of this kind: page or dashboard")
	command.Flags().Bool("pinned", false, "Only the canvases you pinned")
	return command
}

func printCanvas(output io.Writer, canvas canvasDocument) {
	_, _ = fmt.Fprintf(output, "ID:         %s\nTitle:      %s\nKind:       %s\nLanguage:   %s\nVisibility: %s\n",
		canvas.ID, canvas.Title, canvas.Kind, canvas.Language, canvas.Visibility)
	_, _ = fmt.Fprintf(output, "Draft:      revision %d (%s, %d bytes)\nUpdated:    %s\n",
		canvas.Draft.Number, canvas.Draft.ID, canvas.Draft.SizeBytes, canvas.UpdatedAt)
}

func newCanvasGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get <canvas-id>",
		Short: "Show a saved canvas",
		Long: `Show a saved canvas and its draft revision. --content prints only the draft's
content, so it can be written to a file; -o json prints the whole document.`,
		Example: `  ankra canvas get 3f0a2f7e-0000-4000-8000-000000000001
  ankra canvas get 3f0a2f7e-0000-4000-8000-000000000001 --content > report.html`,
		Args: cobra.ExactArgs(1),
		RunE: canvasLibraryRun("get canvas", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			payload, getError := apiClient.GetCanvas(ctx, arguments[0])
			if getError != nil {
				return getError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var canvas canvasDocument
			if decodeError := json.Unmarshal(payload, &canvas); decodeError != nil {
				return fmt.Errorf("parsing the canvas: %w", decodeError)
			}
			if isContentOnly, _ := command.Flags().GetBool("content"); isContentOnly {
				_, writeError := io.WriteString(command.OutOrStdout(), canvas.Draft.Content)
				return writeError
			}
			printCanvas(command.OutOrStdout(), canvas)
			return nil
		}),
	}
	command.Flags().Bool("content", false, "Print only the content of the draft revision")
	return command
}

// readCanvasLibraryContent reads the file a canvas is created from, refusing
// one the platform would not save.
func readCanvasLibraryContent(path string) (string, error) {
	info, statError := os.Stat(path)
	if statError != nil {
		if errors.Is(statError, os.ErrNotExist) {
			return "", fmt.Errorf("file %q does not exist", path)
		}
		return "", fmt.Errorf("stat %q: %w", path, statError)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory: create a canvas from a single file", path)
	}
	if info.Size() > canvasLibraryMaximumBytes {
		return "", fmt.Errorf("%q is over the %d byte limit for a canvas", path, canvasLibraryMaximumBytes)
	}
	data, readError := os.ReadFile(path)
	if readError != nil {
		return "", fmt.Errorf("read %q: %w", path, readError)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("%q is empty: nothing to save", path)
	}
	return string(data), nil
}

func newCanvasCreateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "create <file>",
		Short: "Save a file as a new canvas in the library",
		Long: `Save an HTML, SVG, Markdown or JSON file as a new canvas. The canvas is private
to you until you share it with 'ankra canvas update <canvas-id> --visibility
organisation'; publishing it at a public URL is 'ankra canvas publish'.

The language is inferred from the file extension (.html and .htm, .svg, .md,
.json) and can be set with --language. A json canvas is a dashboard
specification, which the platform validates and never repairs. A file over
524288 bytes is refused.`,
		Example: `  ankra canvas create report.html --title "Q3 report"
  ankra canvas create capacity.json --kind dashboard -o json`,
		Args: cobra.ExactArgs(1),
		RunE: canvasLibraryRun("create canvas", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			path := arguments[0]
			request := client.CreateCanvasRequest{
				Kind:     mustFlagString(command, "kind"),
				Title:    mustFlagString(command, "title"),
				Language: strings.ToLower(mustFlagString(command, "language")),
			}
			if kindError := canvasChoice("kind", request.Kind, canvasLibraryKinds); kindError != nil {
				return kindError
			}
			extension := strings.ToLower(filepath.Ext(path))
			if request.Language == "" {
				request.Language = canvasLanguageByExtension[extension]
				if extension == ".json" {
					request.Language = "json"
				}
				if request.Language == "" {
					return withExitCode(exitUsage, fmt.Errorf(
						"cannot infer the language of %q: use .html, .svg, .md or .json, or pass --language", path))
				}
			}
			if languageError := canvasChoice("language", request.Language, canvasLibraryLanguages); languageError != nil {
				return languageError
			}
			content, contentError := readCanvasLibraryContent(path)
			if contentError != nil {
				return withExitCode(exitUsage, contentError)
			}
			request.Content = content
			if request.Title == "" {
				base := filepath.Base(path)
				request.Title = strings.TrimSuffix(base, filepath.Ext(base))
			}
			payload, createError := apiClient.CreateCanvas(ctx, request)
			if createError != nil {
				return createError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var canvas canvasDocument
			if decodeError := json.Unmarshal(payload, &canvas); decodeError != nil {
				return fmt.Errorf("parsing the canvas: %w", decodeError)
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Saved %q as canvas %s (%s, private to you).\n",
				canvas.Title, canvas.ID, canvas.Language)
			return nil
		}),
	}
	command.Flags().String("title", "", "Canvas title (default: the file name without its extension)")
	command.Flags().String("kind", "page", "Canvas kind: page or dashboard")
	command.Flags().String("language", "", "Content language: html, svg, markdown or json (default: inferred from the extension)")
	return command
}

func newCanvasUpdateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "update <canvas-id>",
		Short: "Rename a canvas or change who can read it",
		Long: `Change a canvas's title, its visibility, or both. --visibility private keeps
the canvas to its owner; organisation lets every member read it. Only the
owner can change a canvas.`,
		Example: `  ankra canvas update 3f0a2f7e-0000-4000-8000-000000000001 --title "Q3 report (final)"
  ankra canvas update 3f0a2f7e-0000-4000-8000-000000000001 --visibility organisation`,
		Args: cobra.ExactArgs(1),
		RunE: canvasLibraryRun("update canvas", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			var request client.UpdateCanvasRequest
			if command.Flags().Changed("title") {
				title := mustFlagString(command, "title")
				if title == "" {
					return withExitCode(exitUsage, errors.New("--title must not be empty"))
				}
				request.Title = &title
			}
			if command.Flags().Changed("visibility") {
				visibility := mustFlagString(command, "visibility")
				if visibilityError := canvasChoice("visibility", visibility, canvasLibraryVisibilities); visibilityError != nil {
					return visibilityError
				}
				request.Visibility = &visibility
			}
			if request.Title == nil && request.Visibility == nil {
				return withExitCode(exitUsage, errors.New("nothing to change: pass --title, --visibility or both"))
			}
			payload, updateError := apiClient.UpdateCanvas(ctx, arguments[0], request)
			if updateError != nil {
				return updateError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var canvas canvasDocument
			if decodeError := json.Unmarshal(payload, &canvas); decodeError != nil {
				return fmt.Errorf("parsing the canvas: %w", decodeError)
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Canvas %s is %q, visible to: %s.\n", canvas.ID, canvas.Title, canvas.Visibility)
			return nil
		}),
	}
	command.Flags().String("title", "", "New title")
	command.Flags().String("visibility", "", "Who can read the canvas: private or organisation")
	return command
}

func newCanvasDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete <canvas-id>",
		Short: "Delete a canvas from the library",
		Long: `Delete a canvas from the library; its revisions are purged with it. Only the
owner can delete a canvas. The command asks first; --yes skips the question
and declining exits 4.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			canvasID := arguments[0]
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Delete canvas %q and its revisions? [y/N]: ", canvasID), skipConfirmation); confirmError != nil {
				return confirmError
			}
			ctx, cancel := context.WithTimeout(command.Context(), canvasLibraryRequestTimeout)
			defer cancel()
			if deleteError := apiClient.DeleteCanvas(ctx, canvasID); deleteError != nil {
				return canvasLibraryError("delete canvas", deleteError)
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Canvas %q deleted.\n", canvasID)
			return nil
		},
	}
	command.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return command
}

func newCanvasRevisionsCommand() *cobra.Command {
	revisionsCommand := &cobra.Command{
		Use:   "revisions",
		Short: "Read the revisions of a saved canvas",
	}
	listCommand := &cobra.Command{
		Use:   "list <canvas-id>",
		Short: "List a canvas's revisions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: canvasLibraryRun("list canvas revisions", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			limit, limitError := canvasPageLimit(command)
			if limitError != nil {
				return limitError
			}
			payload, listError := apiClient.ListCanvasRevisions(ctx, arguments[0], mustFlagString(command, "cursor"), limit)
			if listError != nil {
				return listError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var listing struct {
				Items      []canvasRevision `json:"items"`
				NextCursor *string          `json:"next_cursor"`
			}
			if decodeError := json.Unmarshal(payload, &listing); decodeError != nil {
				return fmt.Errorf("parsing the revisions: %w", decodeError)
			}
			printCanvasNextCursor(command, listing.NextCursor)
			revisionTable := table.NewWriter()
			revisionTable.SetOutputMirror(command.OutOrStdout())
			revisionTable.SetStyle(table.StyleRounded)
			revisionTable.AppendHeader(table.Row{"NUMBER", "ID", "LANGUAGE", "BYTES", "AUTHOR", "CREATED"})
			for _, revision := range listing.Items {
				revisionTable.AppendRow(table.Row{revision.Number, revision.ID, revision.Language, revision.SizeBytes,
					revision.AuthorKind, revision.CreatedAt})
			}
			revisionTable.Render()
			return nil
		}),
	}
	listCommand.Flags().Int("limit", 0, "Revisions to return, 1 to 100 (default: the platform's page size, 25)")
	listCommand.Flags().String("cursor", "", "Continue from the cursor printed by a previous page")
	getCommand := &cobra.Command{
		Use:   "get <canvas-id> <revision-id>",
		Short: "Show one revision of a canvas",
		Long: `Show one revision of a canvas. --content prints only the revision's content,
so an earlier version can be written to a file.`,
		Args: cobra.ExactArgs(2),
		RunE: canvasLibraryRun("get canvas revision", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			payload, getError := apiClient.GetCanvasRevision(ctx, arguments[0], arguments[1])
			if getError != nil {
				return getError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var revision canvasRevision
			if decodeError := json.Unmarshal(payload, &revision); decodeError != nil {
				return fmt.Errorf("parsing the revision: %w", decodeError)
			}
			if isContentOnly, _ := command.Flags().GetBool("content"); isContentOnly {
				_, writeError := io.WriteString(command.OutOrStdout(), revision.Content)
				return writeError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Revision: %d (%s)\nLanguage: %s\nSize:     %d bytes\nAuthor:   %s\nCreated:  %s\n",
				revision.Number, revision.ID, revision.Language, revision.SizeBytes, revision.AuthorKind, revision.CreatedAt)
			return nil
		}),
	}
	getCommand.Flags().Bool("content", false, "Print only the content of the revision")
	registerStructuredOutputFlags(listCommand, getCommand)
	revisionsCommand.AddCommand(listCommand, getCommand)
	return revisionsCommand
}

func printCanvasComment(output io.Writer, comment canvasComment) {
	state := "open"
	if comment.ResolvedAt != nil && *comment.ResolvedAt != "" {
		state = "resolved"
	}
	author := strings.TrimSpace(pipelineStringValue(comment.AuthorEmail))
	if author == "" {
		author = comment.AuthorUserID
	}
	_, _ = fmt.Fprintf(output, "%s  %s  %s  %s\n", comment.ID, state, comment.CreatedAt, pipelineTestText(author))
	if comment.ParentCommentID != nil && *comment.ParentCommentID != "" {
		_, _ = fmt.Fprintf(output, "  reply to %s\n", *comment.ParentCommentID)
	}
	if comment.ElementID != nil && *comment.ElementID != "" {
		_, _ = fmt.Fprintf(output, "  on element %s\n", pipelineTestText(*comment.ElementID))
	}
	for _, line := range strings.Split(strings.TrimRight(comment.Body, "\n"), "\n") {
		_, _ = fmt.Fprintf(output, "  %s\n", pipelineTestText(line))
	}
}

// canvasCommentDecision builds `comments resolve` and `comments reopen`,
// which differ only in the call and the word printed.
func canvasCommentDecision(verb string, short string, outcome string,
	decide func(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <canvas-id> <comment-id>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: canvasLibraryRun(verb+" canvas comment", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			payload, decideError := decide(ctx, arguments[0], arguments[1])
			if decideError != nil {
				return decideError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Comment %s %s.\n", arguments[1], outcome)
			return nil
		}),
	}
}

func newCanvasCommentsCommand() *cobra.Command {
	commentsCommand := &cobra.Command{
		Use:   "comments",
		Short: "Read and answer the comment threads on a saved canvas",
	}
	listCommand := &cobra.Command{
		Use:   "list <canvas-id>",
		Short: "List a canvas's comments",
		Long: `List a canvas's comment threads: the open ones, and the resolved ones too with
--include-resolved.`,
		Args: cobra.ExactArgs(1),
		RunE: canvasLibraryRun("list canvas comments", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			limit, limitError := canvasPageLimit(command)
			if limitError != nil {
				return limitError
			}
			includeResolved, _ := command.Flags().GetBool("include-resolved")
			payload, listError := apiClient.ListCanvasComments(ctx, arguments[0], mustFlagString(command, "cursor"), limit, includeResolved)
			if listError != nil {
				return listError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var listing struct {
				Items      []canvasComment `json:"items"`
				NextCursor *string         `json:"next_cursor"`
			}
			if decodeError := json.Unmarshal(payload, &listing); decodeError != nil {
				return fmt.Errorf("parsing the comments: %w", decodeError)
			}
			printCanvasNextCursor(command, listing.NextCursor)
			if len(listing.Items) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), "No comments.")
				return nil
			}
			for _, comment := range listing.Items {
				printCanvasComment(command.OutOrStdout(), comment)
			}
			return nil
		}),
	}
	listCommand.Flags().Int("limit", 0, "Comments to return, 1 to 100 (default: the platform's page size)")
	listCommand.Flags().String("cursor", "", "Continue from the cursor printed by a previous page")
	listCommand.Flags().Bool("include-resolved", false, "Include resolved threads")
	addCommand := &cobra.Command{
		Use:   "add <canvas-id> <text>",
		Short: "Comment on a canvas",
		Long: `Add a comment to a canvas. --reply-to answers another comment, and --element
or --revision anchor the comment to one element or one revision.`,
		Example: `  ankra canvas comments add 3f0a2f7e-0000-4000-8000-000000000001 "The Q3 total looks low"
  ankra canvas comments add 3f0a2f7e-0000-4000-8000-000000000001 "Fixed in revision 4" --reply-to <comment-id>`,
		Args: cobra.ExactArgs(2),
		RunE: canvasLibraryRun("add canvas comment", func(ctx context.Context, command *cobra.Command, arguments []string, format outputFormat) error {
			request := client.CreateCanvasCommentRequest{Body: strings.TrimSpace(arguments[1])}
			if request.Body == "" {
				return withExitCode(exitUsage, errors.New("the comment text must not be empty"))
			}
			for flagName, target := range map[string]**string{
				"reply-to": &request.ParentCommentID, "element": &request.ElementID, "revision": &request.RevisionID,
			} {
				if value := mustFlagString(command, flagName); value != "" {
					*target = &value
				}
			}
			payload, createError := apiClient.CreateCanvasComment(ctx, arguments[0], request)
			if createError != nil {
				return createError
			}
			if handled, renderError := renderCanvasPayload(command, format, payload); handled {
				return renderError
			}
			var comment canvasComment
			if decodeError := json.Unmarshal(payload, &comment); decodeError != nil {
				return fmt.Errorf("parsing the comment: %w", decodeError)
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Comment %s added.\n", comment.ID)
			return nil
		}),
	}
	addCommand.Flags().String("reply-to", "", "Comment id this comment answers")
	addCommand.Flags().String("element", "", "Element id of the canvas the comment is about")
	addCommand.Flags().String("revision", "", "Revision id the comment is about")
	resolveCommand := canvasCommentDecision("resolve", "Mark a comment thread resolved", "resolved",
		func(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error) {
			return apiClient.ResolveCanvasComment(ctx, canvasID, commentID)
		})
	reopenCommand := canvasCommentDecision("reopen", "Reopen a resolved comment thread", "reopened",
		func(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error) {
			return apiClient.ReopenCanvasComment(ctx, canvasID, commentID)
		})
	registerStructuredOutputFlags(listCommand, addCommand, resolveCommand, reopenCommand)
	commentsCommand.AddCommand(listCommand, addCommand, resolveCommand, reopenCommand)
	return commentsCommand
}

// newCanvasLibraryCommands is every canvas library command, for the canvas
// group to carry.
func newCanvasLibraryCommands() []*cobra.Command {
	libraryCommand, getCommand := newCanvasLibraryCommand(), newCanvasGetCommand()
	createCommand, updateCommand := newCanvasCreateCommand(), newCanvasUpdateCommand()
	registerStructuredOutputFlags(libraryCommand, getCommand, createCommand, updateCommand)
	return []*cobra.Command{libraryCommand, getCommand, createCommand, updateCommand, newCanvasDeleteCommand(),
		newCanvasRevisionsCommand(), newCanvasCommentsCommand()}
}

func init() {
	canvasCmd.AddCommand(newCanvasLibraryCommands()...)
}
