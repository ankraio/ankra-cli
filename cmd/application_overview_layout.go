package cmd

// `ankra application overview-layout show|versions|save|put-back|use-ankra`
// (bead ankra-5uowtk.22.3.1): the CLI side of the routes under
// /api/v1/org/applications/{application_id}/overview-layout. An
// application's Overview shows either Ankra's own layout, which the page
// draws and the platform keeps no version of, or a version the team saved.
// Every change names the version it was made on (--base, left out for
// Ankra's layout); when the Overview changed since, the platform changes
// nothing and says what it shows now.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const overviewLayoutBaseHelp = `--base is the id of the version the Overview showed when you made the change
('ankra application overview-layout show' prints it). Leave it out when the
Overview showed Ankra's own layout. When the Overview changed since, the
platform changes nothing: the command prints the platform's sentence and what
the Overview shows now, and exits 1.`

func newApplicationOverviewLayoutCommand() *cobra.Command {
	overviewLayoutCommand := &cobra.Command{
		Use:   "overview-layout",
		Short: "Show and change the layout of an application's Overview page",
		Long: `Show and change the layout of an application's Overview page.

The Overview shows Ankra's own layout until the team saves a version of its
own. Every saved version is kept: you can put an earlier one back, or go back
to Ankra's layout, and the saved versions stay listed.

A layout is a canvas spec of schema_version 2 (JSON). A summary card is an
ApplicationCard element whose props.card is one of health, environments,
services, cost, safety, releases, runs or backups; a chart binds a dataset of
the application with params.application set to the application's id. The
platform checks a layout before it saves it and refuses one that breaks a
rule, with the reason.`,
		Example: `  ankra application overview-layout show shop
  ankra application overview-layout show shop --content > layout.json
  ankra application overview-layout save shop --file layout.json --base <version-id>
  ankra application overview-layout versions shop
  ankra application overview-layout use-ankra shop --base <version-id>`,
	}
	overviewLayoutCommand.AddCommand(
		newApplicationOverviewLayoutShowCommand(),
		newApplicationOverviewLayoutVersionsCommand(),
		newApplicationOverviewLayoutSaveCommand(),
		newApplicationOverviewLayoutPutBackCommand(),
		newApplicationOverviewLayoutUseAnkraCommand(),
	)
	return overviewLayoutCommand
}

func newApplicationOverviewLayoutShowCommand() *cobra.Command {
	showCommand := &cobra.Command{
		Use:   "show <application>",
		Short: "Show what an application's Overview shows now",
		Long: `Show what an application's Overview shows now: Ankra's own layout, or the
version the team saved, with its id, who saved it and when, and its cards in
the order the page draws them.

Ankra draws its own layout on the page and keeps no version of it, so there
are no cards to list for it. --content prints only the saved version's layout,
so it can be written to a file, changed and saved again; -o json prints the
platform's answer.`,
		Example: `  ankra application overview-layout show shop
  ankra application overview-layout show shop --content > layout.json
  ankra application overview-layout show shop -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			layout, readError := apiClient.GetApplicationOverviewLayout(command.Context(), applicationID)
			if readError != nil {
				return fmt.Errorf("reading the Overview layout of application %s: %w", applicationReference, readError)
			}
			if rendered, renderError := renderStructured(command, layout); rendered || renderError != nil {
				return renderError
			}
			if contentOnly, _ := command.Flags().GetBool("content"); contentOnly {
				if layout.Revision == nil {
					return withExitCode(exitNotFound, fmt.Errorf(
						"the Overview of application %s shows Ankra's own layout, which the platform keeps no content of",
						applicationReference))
				}
				_, writeError := io.WriteString(command.OutOrStdout(), layout.Revision.Content)
				return writeError
			}
			return writeApplicationOverviewLayout(command.OutOrStdout(), applicationReference, layout)
		},
	}
	showCommand.Flags().Bool("content", false, "Print only the layout of the version on the page")
	registerStructuredOutputFlags(showCommand)
	return showCommand
}

func newApplicationOverviewLayoutVersionsCommand() *cobra.Command {
	versionsCommand := &cobra.Command{
		Use:   "versions <application>",
		Short: "List the saved versions of an application's Overview, newest first",
		Long: `List the saved versions of an application's Overview, newest first, with who
saved each and when, and which one the Overview shows now.

The platform sends one page at a time, 25 versions unless --limit says
otherwise (at most 100). When more are kept, the command says so on stderr and
prints the --cursor that reads the next page.`,
		Example: `  ankra application overview-layout versions shop
  ankra application overview-layout versions shop --limit 100
  ankra application overview-layout versions shop --cursor <cursor> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			limit, _ := command.Flags().GetInt("limit")
			if command.Flags().Changed("limit") && (limit < 1 || limit > 100) {
				return withExitCode(exitUsage, errors.New("--limit must be between 1 and 100"))
			}
			cursor, _ := command.Flags().GetString("cursor")
			cursor = strings.TrimSpace(cursor)
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			versions, listError := apiClient.ListApplicationOverviewLayoutVersions(command.Context(), applicationID, cursor, limit)
			if listError != nil {
				var refusal *client.OverviewLayoutRefusalError
				if errors.As(listError, &refusal) && refusal.Code == client.OverviewLayoutCodeInvalidCursor {
					return withExitCode(exitUsage, fmt.Errorf(
						"reading the Overview versions of application %s: %w Leave --cursor out to read the newest versions",
						applicationReference, listError))
				}
				return fmt.Errorf("reading the Overview versions of application %s: %w", applicationReference, listError)
			}
			if versions.NextCursor != nil && *versions.NextCursor != "" {
				_, _ = fmt.Fprintf(command.ErrOrStderr(),
					"Showing %d versions. More are kept: re-run with --cursor %s\n", len(versions.Items), *versions.NextCursor)
			}
			if rendered, renderError := renderStructured(command, versions); rendered || renderError != nil {
				return renderError
			}
			return writeApplicationOverviewLayoutVersions(command.OutOrStdout(), versions, cursor != "")
		},
	}
	versionsCommand.Flags().Int("limit", 0, "Versions to read, 1 to 100 (default: the platform's page size, 25)")
	versionsCommand.Flags().String("cursor", "", "Read the page after the one that printed this cursor")
	registerStructuredOutputFlags(versionsCommand)
	return versionsCommand
}

func newApplicationOverviewLayoutSaveCommand() *cobra.Command {
	saveCommand := &cobra.Command{
		Use:   "save <application> --file <layout.json> [--base <version-id>]",
		Short: "Save a layout as a new version and put it on the Overview",
		Long: `Save a layout from a file as a new version of the application's Overview, and
put it on the page. --file - reads the layout from stdin.

` + overviewLayoutBaseHelp + `

The platform also refuses a layout that breaks one of its rules (it is not a
canvas spec of schema_version 2, it is over 65536 bytes or 64 cards, it names
a card the Overview does not offer, or a chart binds another application's
data) and says why. Nothing is saved then.`,
		Example: `  ankra application overview-layout show shop --content > layout.json
  ankra application overview-layout save shop --file layout.json --base 0b7c4d0a-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra application overview-layout save shop --file layout.json   # the Overview showed Ankra's own layout`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			baseRevisionID, baseError := overviewLayoutBaseFlag(command)
			if baseError != nil {
				return baseError
			}
			content, readError := readOverviewLayoutFile(command)
			if readError != nil {
				return readError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			layout, saveError := apiClient.SaveApplicationOverviewLayoutVersion(command.Context(), applicationID, content, baseRevisionID)
			if saveError != nil {
				return overviewLayoutChangeError("saving the layout", applicationReference, saveError)
			}
			if layout.Revision == nil {
				return withExitCode(exitError, fmt.Errorf(
					"the platform accepted the layout but answered that the Overview of application %s shows Ankra's own layout; "+
						"run 'ankra application overview-layout show %s' to check it", applicationReference, applicationReference))
			}
			if rendered, renderError := renderStructured(command, layout); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Saved version %d of the Overview of %s (%s). The Overview shows it now.\n",
				layout.Revision.Number, applicationReference, layout.Revision.ID)
			return nil
		},
	}
	saveCommand.Flags().String("file", "", "The layout file (JSON), or '-' to read stdin (required)")
	saveCommand.Flags().String("base", "", "The id of the version the Overview showed; leave out for Ankra's own layout")
	_ = saveCommand.MarkFlagRequired("file")
	registerStructuredOutputFlags(saveCommand)
	return saveCommand
}

func newApplicationOverviewLayoutPutBackCommand() *cobra.Command {
	putBackCommand := &cobra.Command{
		Use:   "put-back <application> <version-id> [--base <version-id>]",
		Short: "Put an earlier version back on the Overview",
		Long: `Put an earlier version back on the application's Overview. The platform saves
its layout as a new version by you and puts that on the page; the earlier
version stays as it was. 'ankra application overview-layout versions' lists
the version ids.

` + overviewLayoutBaseHelp + `

The layout is checked by today's rules, so the platform refuses a version
that a rule added since would refuse, and says why.`,
		Example: `  ankra application overview-layout put-back shop 0b7c4d0b-5f6a-4b8c-9d0e-1f2a3b4c5d6e --base 0b7c4d0a-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra application overview-layout put-back shop 0b7c4d0b-5f6a-4b8c-9d0e-1f2a3b4c5d6e   # the Overview showed Ankra's own layout`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			baseRevisionID, baseError := overviewLayoutBaseFlag(command)
			if baseError != nil {
				return baseError
			}
			versionID := strings.TrimSpace(arguments[1])
			if versionID == "" {
				return withExitCode(exitUsage, errors.New("the version id is empty: 'ankra application overview-layout versions' lists them"))
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			layout, putBackError := apiClient.PutBackApplicationOverviewLayoutVersion(command.Context(), applicationID, versionID, baseRevisionID)
			if putBackError != nil {
				var refusal *client.OverviewLayoutRefusalError
				if errors.As(putBackError, &refusal) && refusal.Code == client.OverviewLayoutCodeVersionNotFound {
					return fmt.Errorf("putting back version %s: %w 'ankra application overview-layout versions %s' lists them",
						versionID, putBackError, applicationReference)
				}
				return overviewLayoutChangeError("putting back version "+versionID, applicationReference, putBackError)
			}
			if layout.Revision == nil {
				return withExitCode(exitError, fmt.Errorf(
					"the platform accepted the change but answered that the Overview of application %s shows Ankra's own layout; "+
						"run 'ankra application overview-layout show %s' to check it", applicationReference, applicationReference))
			}
			if rendered, renderError := renderStructured(command, layout); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Put version %s back on the Overview of %s as version %d (%s). The Overview shows it now.\n",
				versionID, applicationReference, layout.Revision.Number, layout.Revision.ID)
			return nil
		},
	}
	putBackCommand.Flags().String("base", "", "The id of the version the Overview showed; leave out for Ankra's own layout")
	registerStructuredOutputFlags(putBackCommand)
	return putBackCommand
}

func newApplicationOverviewLayoutUseAnkraCommand() *cobra.Command {
	useAnkraCommand := &cobra.Command{
		Use:   "use-ankra <application> [--base <version-id>]",
		Short: "Put the Overview back on Ankra's own layout",
		Long: `Put the application's Overview back on Ankra's own layout. No version is
written or removed: every saved version stays listed and can be put back.
When the Overview already shows Ankra's layout, nothing changes.

` + overviewLayoutBaseHelp,
		Example: `  ankra application overview-layout use-ankra shop --base 0b7c4d0a-5f6a-4b8c-9d0e-1f2a3b4c5d6e`,
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			baseRevisionID, baseError := overviewLayoutBaseFlag(command)
			if baseError != nil {
				return baseError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			layout, useAnkraError := apiClient.UseAnkraApplicationOverviewLayout(command.Context(), applicationID, baseRevisionID)
			if useAnkraError != nil {
				return overviewLayoutChangeError("going back to Ankra's own layout", applicationReference, useAnkraError)
			}
			if layout.OnPage != client.OverviewLayoutOnPageAnkra {
				return withExitCode(exitError, fmt.Errorf(
					"the platform accepted the change but answered that the Overview of application %s does not show Ankra's own layout; "+
						"run 'ankra application overview-layout show %s' to check it", applicationReference, applicationReference))
			}
			if rendered, renderError := renderStructured(command, layout); rendered || renderError != nil {
				return renderError
			}
			if baseRevisionID == nil {
				_, _ = fmt.Fprintf(command.OutOrStdout(), "The Overview of %s already showed Ankra's own layout. Nothing changed.\n",
					applicationReference)
				return nil
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "The Overview of %s shows Ankra's own layout now. Its %s stay listed.\n",
				applicationReference, overviewLayoutVersionCount(layout.Versions))
			return nil
		},
	}
	useAnkraCommand.Flags().String("base", "", "The id of the version the Overview showed; leave out for Ankra's own layout")
	registerStructuredOutputFlags(useAnkraCommand)
	return useAnkraCommand
}

// overviewLayoutBaseFlag reads --base: nil when it is left out (the Overview
// showed Ankra's own layout), else the version id it names.
func overviewLayoutBaseFlag(command *cobra.Command) (*string, error) {
	if !command.Flags().Changed("base") {
		return nil, nil
	}
	base, _ := command.Flags().GetString("base")
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, withExitCode(exitUsage, errors.New(
			"--base is empty: pass the id of the version the Overview showed, or leave --base out when it showed Ankra's own layout"))
	}
	if !looksLikeUUID(base) {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"--base %q is not a version id: 'ankra application overview-layout show' prints the id of the version on the page", base))
	}
	return &base, nil
}

// readOverviewLayoutFile reads the --file layout (stdin for '-'), refusing an
// empty file and one that is not JSON before anything is sent.
func readOverviewLayoutFile(command *cobra.Command) (string, error) {
	filePath, _ := command.Flags().GetString("file")
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", withExitCode(exitUsage, errors.New("--file is required: the layout file, or '-' for stdin"))
	}
	var content []byte
	var readError error
	if filePath == "-" {
		content, readError = io.ReadAll(command.InOrStdin())
	} else {
		content, readError = os.ReadFile(filePath)
	}
	if readError != nil {
		return "", fmt.Errorf("reading the layout: %w", readError)
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("%s is empty: nothing to save", filePath))
	}
	if !json.Valid(content) {
		return "", withExitCode(exitUsage, fmt.Errorf("%s does not contain valid JSON", filePath))
	}
	return string(content), nil
}

// overviewLayoutChangeError is a refused change. A stale base says the
// platform's sentence, that nothing changed, and what the Overview shows now
// with the --base the next try names; any other refusal is the platform's
// sentence after what was being done.
func overviewLayoutChangeError(action string, applicationReference string, changeError error) error {
	var conflict *client.OverviewLayoutConflictError
	if !errors.As(changeError, &conflict) {
		return fmt.Errorf("%s: %w", action, changeError)
	}
	var now string
	switch {
	case conflict.Layout.Revision != nil:
		now = "The Overview shows " + overviewLayoutRevisionPhrase(conflict.Layout.Revision) + " now. " +
			"Make the change on that version ('ankra application overview-layout show " + applicationReference +
			" --content' prints it) and pass --base " + conflict.Layout.Revision.ID + "."
	case conflict.CurrentRevisionID != nil:
		now = "The Overview shows version " + *conflict.CurrentRevisionID + " now. Make the change on that version and pass --base " +
			*conflict.CurrentRevisionID + "."
	default:
		now = "The Overview shows Ankra's own layout now. Make the change on that and leave --base out."
	}
	return fmt.Errorf("%s: %w Nothing changed. %s", action, changeError, now)
}

// writeApplicationOverviewLayout prints what the Overview shows and, for a
// saved version, its cards in the order the page draws them.
func writeApplicationOverviewLayout(output io.Writer, applicationReference string, layout *client.ApplicationOverviewLayout) error {
	versionsLine := fmt.Sprintf("%s: 'ankra application overview-layout versions %s' lists them.",
		overviewLayoutVersionCount(layout.Versions), applicationReference)
	if layout.Versions == 0 {
		versionsLine = "No version has been saved."
	}
	if layout.Revision == nil {
		if layout.OnPage != client.OverviewLayoutOnPageAnkra {
			return withExitCode(exitError, fmt.Errorf(
				"the platform answered that the Overview of application %s shows %q but sent no version", applicationReference, layout.OnPage))
		}
		_, _ = fmt.Fprintf(output, "The Overview of %s shows Ankra's own layout. Ankra draws it on the page and keeps no version of it, "+
			"so there are no cards to list.\n%s\n", applicationReference, versionsLine)
		return nil
	}
	_, _ = fmt.Fprintf(output, "The Overview of %s shows %s.\n%s\n", applicationReference,
		overviewLayoutRevisionPhrase(layout.Revision), versionsLine)
	cards, cardsError := overviewLayoutCards(layout.Revision.Content)
	if cardsError != nil {
		_, _ = fmt.Fprintf(output, "\nThe cards of version %d could not be read from its layout (%v). "+
			"'ankra application overview-layout show %s --content' prints the layout.\n",
			layout.Revision.Number, cardsError, applicationReference)
		return nil
	}
	if len(cards) == 0 {
		_, _ = fmt.Fprintf(output, "\nVersion %d places no cards.\n", layout.Revision.Number)
		return nil
	}
	_, _ = fmt.Fprintln(output)
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "#\tELEMENT\tCARD")
	for index, card := range cards {
		_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\n", index+1, card.elementID, card.says)
	}
	return writer.Flush()
}

func writeApplicationOverviewLayoutVersions(output io.Writer, versions *client.ApplicationOverviewLayoutVersions, isLaterPage bool) error {
	if len(versions.Items) == 0 {
		if isLaterPage {
			_, _ = fmt.Fprintln(output, "No more versions.")
		} else {
			_, _ = fmt.Fprintln(output, "No version of this Overview has been saved. It shows Ankra's own layout.")
		}
		return nil
	}
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "VERSION\tSAVED BY\tSAVED AT\tBYTES\tON PAGE\tVERSION ID")
	for _, version := range versions.Items {
		onPage := "-"
		if version.OnPage {
			onPage = "yes"
		}
		_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\t%d\t%s\t%s\n", version.Number,
			overviewLayoutAuthor(version.AuthorName, version.AuthorUserID), overviewLayoutMoment(version.CreatedAt),
			version.SizeBytes, onPage, version.ID)
	}
	return writer.Flush()
}

// overviewLayoutRevisionPhrase says which version is on the page and who
// saved it when, from what the platform sent.
func overviewLayoutRevisionPhrase(revision *client.ApplicationOverviewLayoutRevision) string {
	phrase := fmt.Sprintf("version %d (%s), saved", revision.Number, revision.ID)
	author := overviewLayoutAuthor(revision.AuthorName, revision.AuthorUserID)
	if author != "-" {
		phrase += " by " + author
	}
	phrase += " on " + overviewLayoutMoment(revision.CreatedAt)
	if author == "-" {
		phrase += "; it records no author"
	}
	return phrase
}

// overviewLayoutAuthor is who saved a version: the name the platform sent,
// else the member id, else "-".
func overviewLayoutAuthor(name *string, userID *string) string {
	if author := strings.TrimSpace(stringValue(name)); author != "" {
		return author
	}
	if member := strings.TrimSpace(stringValue(userID)); member != "" {
		return "member " + member
	}
	return "-"
}

// overviewLayoutMoment prints a platform timestamp as its UTC minute, or as
// sent when it does not parse.
func overviewLayoutMoment(timestamp string) string {
	moment, parseError := time.Parse(time.RFC3339, timestamp)
	if parseError != nil {
		return valueOrDashString(timestamp)
	}
	return moment.UTC().Format("2006-01-02 15:04 UTC")
}

func overviewLayoutVersionCount(count int) string {
	if count == 1 {
		return "1 saved version"
	}
	return fmt.Sprintf("%d saved versions", count)
}

// overviewLayoutCard is one card of a layout: a summary card or a chart.
type overviewLayoutCard struct {
	elementID string
	says      string
}

// overviewLayoutCards lists a layout's cards in the order the page draws
// them: the element tree followed from root through each element's children.
// A card is what the platform counts as one: an ApplicationCard element (a
// summary card) or an element whose props carry a binding (a chart).
func overviewLayoutCards(content string) ([]overviewLayoutCard, error) {
	var document struct {
		Root     string `json:"root"`
		Elements map[string]struct {
			Type     string                     `json:"type"`
			Props    map[string]json.RawMessage `json:"props"`
			Children []string                   `json:"children"`
		} `json:"elements"`
	}
	if decodeError := json.Unmarshal([]byte(content), &document); decodeError != nil {
		return nil, errors.New("it is not a canvas spec")
	}
	if _, hasRoot := document.Elements[document.Root]; !hasRoot {
		return nil, errors.New("its root is not one of its elements")
	}
	cards := []overviewLayoutCard{}
	visited := map[string]bool{}
	var walk func(elementID string)
	walk = func(elementID string) {
		element, exists := document.Elements[elementID]
		if !exists || visited[elementID] {
			return
		}
		visited[elementID] = true
		var binding struct {
			Dataset string `json:"dataset"`
		}
		rawBinding, hasBinding := element.Props["binding"]
		hasBinding = hasBinding && strings.TrimSpace(string(rawBinding)) != "null"
		switch {
		case element.Type == "ApplicationCard":
			var card string
			_ = json.Unmarshal(element.Props["card"], &card)
			cards = append(cards, overviewLayoutCard{elementID: elementID, says: "summary card: " + valueOrDashString(card)})
		case hasBinding:
			_ = json.Unmarshal(rawBinding, &binding)
			says := valueOrDashString(element.Type) + " of dataset " + valueOrDashString(binding.Dataset)
			var title string
			if json.Unmarshal(element.Props["title"], &title) == nil && strings.TrimSpace(title) != "" {
				says += " (" + strings.TrimSpace(title) + ")"
			}
			cards = append(cards, overviewLayoutCard{elementID: elementID, says: says})
		}
		for _, childID := range element.Children {
			walk(childID)
		}
	}
	walk(document.Root)
	return cards, nil
}
