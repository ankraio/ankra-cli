package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The searches saved for an application's Logs: the CLI side of the routes
// under /api/v1/org/applications/{application_id}/saved-log-searches, the
// same searches the Logs page's "Saved searches" menu shows. A saved search
// belongs to the application, not to one person: everyone who can read the
// application sees it, and anyone who can change the application can change
// or remove it.
//
// The family is `log-searches`, not `logs searches`, for the reason `cluster
// logs-ship` is not `cluster logs ship`: a later `application logs
// <application>` that reads the lines would take an application named
// "searches" as its argument.

// savedLogSearchPodField is the field the Logs page filters a copy (a pod)
// by.
const savedLogSearchPodField = "pod"

// savedLogSearchLevelField is the field the Logs page filters a level by.
const savedLogSearchLevelField = "level"

// savedLogSearchQueryFlags are the flags that give a search's query. Any one
// of them gives the whole query.
var savedLogSearchQueryFlags = []string{"text", "level", "copy", "filter", "from", "to", "last", "file"}

const savedLogSearchQueryHelp = `The query is what the search looks for, as the Logs page searches:

  --text      the words to search the lines for
  --level     only lines of this level: error, warn, info or debug
  --copy      only lines of this copy of the application (the pod's name)
  --filter    FIELD=VALUE, only lines whose field has the value, or
              FIELD!=VALUE, to hide them. FIELD is a name the platform
              stores: level, pod, container, version, or a field of the line
  --last      the time range up to the moment the search is used: 15m, 6h, 7d
  --from/--to the time range's ends, each "now", "now-15m" or a UTC moment
              such as "2026-11-14 14:00"; --to is "now" when left out

--level, --copy and --filter can be given more than once; a query holds at
most 20 filters. With no time range the search keeps the range the page
shows when it is used. --file reads the whole query document instead: JSON
with text, filters and span, as each search's query in 'log-searches list
-o json'. --file - reads it from stdin.`

const savedLogSearchExitHelp = `Exit codes: 2 when the platform refuses the name or the query, the name is
taken or the application keeps as many searches as it may (the platform's
sentence says which); 3 when the application or the search is not there; 7
when your role may not change the application.`

// savedLogSearchLastPattern is a --last value: a count and a unit, the Logs
// page's relative time without "now-".
var savedLogSearchLastPattern = regexp.MustCompile(`^\d{1,4}[smhd]$`)

// savedLogSearchRelativePattern is one end of a time range relative to the
// moment the search is used, the Logs page's grammar.
var savedLogSearchRelativePattern = regexp.MustCompile(`^now(?:-(\d{1,4})([smhd]))?$`)

// savedLogSearchZonePattern is a UTC moment that names its zone.
var savedLogSearchZonePattern = regexp.MustCompile(`(?i)(z|[+-]\d{2}:\d{2})$`)

func newApplicationLogSearchesCommand() *cobra.Command {
	logSearchesCommand := &cobra.Command{
		Use:     "log-searches",
		Aliases: []string{"log-search"},
		Short:   "List, save, replace and remove the searches saved for an application's Logs",
		Long: `List, save, replace and remove the searches saved for an application's Logs.

These are the searches in the Logs page's "Saved searches" menu. A saved
search belongs to the application: everyone who can read the application sees
it, and anyone who can change the application can replace or remove it. An
application keeps at most 50, and no two whose names read the same with
capitals aside. The platform keeps a search; the Logs page runs it.`,
		Example: `  ankra application log-searches list shop
  ankra application log-searches save shop --name "Checkout errors" --text checkout --level error --last 1h
  ankra application log-searches replace shop "Checkout errors" --level error --level warn --last 6h
  ankra application log-searches remove shop "Checkout errors"`,
	}
	logSearchesCommand.AddCommand(
		newApplicationLogSearchesListCommand(),
		newApplicationLogSearchesSaveCommand(),
		newApplicationLogSearchesReplaceCommand(),
		newApplicationLogSearchesRemoveCommand(),
	)
	return logSearchesCommand
}

func newApplicationLogSearchesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list <application>",
		Short: "List the searches saved for an application's Logs",
		Long: `List the searches saved for an application's Logs, by name, with what each one
searches, who saved it and when.

A search whose stored query the platform no longer accepts is listed and says
so; replace it or remove it. -o json and -o yaml print the platform's answer
as it was sent.`,
		Example: `  ankra application log-searches list shop
  ankra application log-searches list shop -o json`,
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
			searches, listError := apiClient.ListApplicationSavedLogSearches(command.Context(), applicationID)
			if listError != nil {
				return fmt.Errorf("reading the saved Logs searches of application %s: %w", applicationReference, listError)
			}
			if rendered, renderError := renderStructured(command, searches); rendered || renderError != nil {
				return renderError
			}
			return writeSavedLogSearches(command.OutOrStdout(), applicationReference, searches)
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationLogSearchesSaveCommand() *cobra.Command {
	saveCommand := &cobra.Command{
		Use:   "save <application> --name <name> [query flags]",
		Short: "Save a search for an application's Logs",
		Long: `Save a search for an application's Logs. It shows in the Logs page's "Saved
searches" menu for everyone who can read the application.

--name is 1 to 80 characters. The platform trims it and keeps one space
between words.

` + savedLogSearchQueryHelp + `

With no query flag the search finds every line in the time range the page
shows.

` + savedLogSearchExitHelp,
		Example: `  ankra application log-searches save shop --name "Checkout errors" --text checkout --level error --last 1h
  ankra application log-searches save shop --name "Not the canary" --filter version!=2.8.1
  ankra application log-searches save shop --name "Incident 14 Nov" --file query.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			name, _ := command.Flags().GetString("name")
			if strings.TrimSpace(name) == "" {
				return withExitCode(exitUsage, errors.New("--name is empty: give the search a name of 1 to 80 characters"))
			}
			query, queryError := readSavedLogSearchQueryFlags(command)
			if queryError != nil {
				return queryError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			saved, saveError := apiClient.SaveApplicationLogSearch(command.Context(), applicationID,
				client.SavedLogSearchRequest{Name: name, Query: query})
			if saveError != nil {
				return savedLogSearchChangeError("saving the search", applicationReference, saveError)
			}
			if rendered, renderError := renderStructured(command, saved); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Saved the Logs search %q on %s (%s).\n",
				saved.Name, applicationReference, saved.ID)
			return nil
		},
	}
	saveCommand.Flags().String("name", "", "The search's name in the Logs page's menu (1 to 80 characters)")
	_ = saveCommand.MarkFlagRequired("name")
	registerSavedLogSearchQueryFlags(saveCommand)
	registerStructuredOutputFlags(saveCommand)
	return saveCommand
}

func newApplicationLogSearchesReplaceCommand() *cobra.Command {
	replaceCommand := &cobra.Command{
		Use:   "replace <application> <search> [--name <name>] [query flags]",
		Short: "Replace the name or the query of a saved Logs search",
		Long: `Replace the name or the query of a search saved for an application's Logs.
<search> is the search's name or its id, as 'log-searches list' shows them.

--name gives a new name. Any query flag gives the whole new query: what the
search looked for before is not kept, so give every part of it again. What
you leave out of the two stays as it is. A search whose stored query the
platform no longer accepts needs a new query.

` + savedLogSearchQueryHelp + `

` + savedLogSearchExitHelp,
		Example: `  ankra application log-searches replace shop "Checkout errors" --name "Checkout failures"
  ankra application log-searches replace shop "Checkout errors" --text checkout --level error --last 6h
  ankra application log-searches replace shop 0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e --file query.json`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			flags := command.Flags()
			queryGiven := savedLogSearchQueryGiven(command)
			if !flags.Changed("name") && !queryGiven {
				return withExitCode(exitUsage, errors.New(
					"nothing to replace: pass --name, or the query with --text, --level, --copy, --filter, --last, --from or --file"))
			}
			name, _ := flags.GetString("name")
			if flags.Changed("name") && strings.TrimSpace(name) == "" {
				return withExitCode(exitUsage, errors.New("--name is empty: give the search a name of 1 to 80 characters"))
			}
			var query json.RawMessage
			if queryGiven {
				readQuery, queryError := readSavedLogSearchQueryFlags(command)
				if queryError != nil {
					return queryError
				}
				query = readQuery
			}
			applicationReference := strings.TrimSpace(arguments[0])
			searchReference := strings.TrimSpace(arguments[1])
			if searchReference == "" {
				return withExitCode(exitUsage, errors.New("the search is empty: give its name or id, as 'log-searches list' shows them"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			searchID := searchReference
			if !looksLikeUUID(searchReference) || !flags.Changed("name") || !queryGiven {
				current, findError := findSavedLogSearch(command, applicationID, applicationReference, searchReference)
				if findError != nil {
					return findError
				}
				searchID = current.ID
				if !flags.Changed("name") {
					name = current.Name
				}
				if !queryGiven {
					if current.QueryState == client.SavedLogSearchQueryUnreadable || len(current.Query) == 0 ||
						string(current.Query) == "null" {
						return withExitCode(exitUsage, fmt.Errorf(
							"the stored query of %q cannot be read, so it cannot be kept: give a new query with "+
								"--text, --level, --copy, --filter, --last, --from or --file", current.Name))
					}
					query = current.Query
				}
			}
			replaced, replaceError := apiClient.ReplaceApplicationSavedLogSearch(command.Context(), applicationID, searchID,
				client.SavedLogSearchRequest{Name: name, Query: query})
			if replaceError != nil {
				return savedLogSearchChangeError("replacing the search", applicationReference, replaceError)
			}
			if rendered, renderError := renderStructured(command, replaced); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Replaced the Logs search %q on %s (%s).\n",
				replaced.Name, applicationReference, replaced.ID)
			return nil
		},
	}
	replaceCommand.Flags().String("name", "", "A new name (1 to 80 characters)")
	registerSavedLogSearchQueryFlags(replaceCommand)
	registerStructuredOutputFlags(replaceCommand)
	return replaceCommand
}

func newApplicationLogSearchesRemoveCommand() *cobra.Command {
	removeCommand := &cobra.Command{
		Use:   "remove <application> <search>",
		Short: "Remove a saved Logs search for everyone",
		Long: `Remove a search saved for an application's Logs. It leaves the Logs page's menu
for everyone, and it cannot be undone. <search> is the search's name or its
id, as 'log-searches list' shows them.

The command names the search and asks before it removes it; --yes skips the
question. Exit codes: 3 when the application or the search is not there, 4
when you answer no, 7 when your role may not change the application.`,
		Example: `  ankra application log-searches remove shop "Checkout errors"
  ankra application log-searches remove shop 0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			applicationReference := strings.TrimSpace(arguments[0])
			searchReference := strings.TrimSpace(arguments[1])
			if searchReference == "" {
				return withExitCode(exitUsage, errors.New("the search is empty: give its name or id, as 'log-searches list' shows them"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			yes, _ := command.Flags().GetBool("yes")
			searchID := searchReference
			searchName := ""
			if !yes || !looksLikeUUID(searchReference) {
				current, findError := findSavedLogSearch(command, applicationID, applicationReference, searchReference)
				if findError != nil {
					return findError
				}
				searchID = current.ID
				searchName = current.Name
				if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
					fmt.Sprintf("Remove the Logs search %q from application %s for everyone? It cannot be undone. [y/N]: ",
						current.Name, applicationReference), yes); confirmError != nil {
					return confirmError
				}
			}
			if removeError := apiClient.DeleteApplicationSavedLogSearch(command.Context(), applicationID, searchID); removeError != nil {
				return savedLogSearchChangeError("removing the search", applicationReference, removeError)
			}
			if searchName == "" {
				_, _ = fmt.Fprintf(command.OutOrStdout(), "Removed the Logs search %s from %s.\n", searchID, applicationReference)
				return nil
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Removed the Logs search %q from %s.\n", searchName, applicationReference)
			return nil
		},
	}
	removeCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	return removeCommand
}

// registerSavedLogSearchQueryFlags adds the flags that give a search's query.
func registerSavedLogSearchQueryFlags(command *cobra.Command) {
	flags := command.Flags()
	flags.String("text", "", "The words to search the lines for")
	flags.StringSlice("level", nil, "Only lines of this level: error, warn, info or debug (repeatable)")
	flags.StringSlice("copy", nil, "Only lines of this copy of the application, the pod's name (repeatable)")
	flags.StringArray("filter", nil, "FIELD=VALUE for lines whose field has the value, FIELD!=VALUE to hide them (repeatable)")
	flags.String("last", "", "The time range up to the moment the search is used: 15m, 6h or 7d")
	flags.String("from", "", "The time range's start: now, now-15m or a UTC moment such as '2026-11-14 14:00'")
	flags.String("to", "", "The time range's end, like --from (default now when --from is given)")
	flags.String("file", "", "Read the whole query document (JSON) from this file, or '-' for stdin")
}

// savedLogSearchQueryGiven reports whether any flag that gives the query was
// passed.
func savedLogSearchQueryGiven(command *cobra.Command) bool {
	for _, flagName := range savedLogSearchQueryFlags {
		if command.Flags().Changed(flagName) {
			return true
		}
	}
	return false
}

// readSavedLogSearchQueryFlags builds the query document the flags give, or
// reads it from --file. What the flags can tell before anything is sent is
// refused here with exit 2; what the query holds is the platform's to judge,
// and its sentence says why it refuses one.
func readSavedLogSearchQueryFlags(command *cobra.Command) (json.RawMessage, error) {
	flags := command.Flags()
	if flags.Changed("file") {
		for _, flagName := range savedLogSearchQueryFlags {
			if flagName != "file" && flags.Changed(flagName) {
				return nil, withExitCode(exitUsage, fmt.Errorf(
					"--file gives the whole query, so it cannot be combined with --%s", flagName))
			}
		}
		return readSavedLogSearchQueryFile(command)
	}
	text, _ := flags.GetString("text")
	levels, _ := flags.GetStringSlice("level")
	copies, _ := flags.GetStringSlice("copy")
	rawFilters, _ := flags.GetStringArray("filter")
	query := client.SavedLogSearchQuery{Text: text, Filters: []client.SavedLogSearchFilter{}}
	for _, level := range trimmedValues(levels) {
		query.Filters = append(query.Filters, client.SavedLogSearchFilter{Field: savedLogSearchLevelField, Value: level})
	}
	for _, copyName := range trimmedValues(copies) {
		query.Filters = append(query.Filters, client.SavedLogSearchFilter{Field: savedLogSearchPodField, Value: copyName})
	}
	for _, rawFilter := range rawFilters {
		filter, filterError := parseSavedLogSearchFilter(rawFilter)
		if filterError != nil {
			return nil, filterError
		}
		query.Filters = append(query.Filters, filter)
	}
	span, spanError := savedLogSearchSpanFromFlags(command)
	if spanError != nil {
		return nil, spanError
	}
	query.Span = span
	encoded, marshalError := json.Marshal(query)
	if marshalError != nil {
		return nil, fmt.Errorf("encoding the query: %w", marshalError)
	}
	return encoded, nil
}

// parseSavedLogSearchFilter reads one --filter: FIELD=VALUE, or FIELD!=VALUE
// for lines whose field does not have the value. The value is kept as typed,
// so it may hold "=".
func parseSavedLogSearchFilter(rawFilter string) (client.SavedLogSearchFilter, error) {
	separator := strings.Index(rawFilter, "=")
	if separator < 0 {
		return client.SavedLogSearchFilter{}, withExitCode(exitUsage, fmt.Errorf(
			"--filter %q has no '=': write FIELD=VALUE, or FIELD!=VALUE to hide the lines", rawFilter))
	}
	field := rawFilter[:separator]
	isNot := strings.HasSuffix(field, "!")
	field = strings.TrimSpace(strings.TrimSuffix(field, "!"))
	value := rawFilter[separator+1:]
	if field == "" || value == "" {
		return client.SavedLogSearchFilter{}, withExitCode(exitUsage, fmt.Errorf(
			"--filter %q needs a field and a value: write FIELD=VALUE, or FIELD!=VALUE to hide the lines", rawFilter))
	}
	return client.SavedLogSearchFilter{Field: field, Value: value, Not: isNot}, nil
}

// savedLogSearchSpanFromFlags reads --last, --from and --to: nil when none is
// given, so the search keeps the time range the page shows.
func savedLogSearchSpanFromFlags(command *cobra.Command) (*client.SavedLogSearchSpan, error) {
	flags := command.Flags()
	last, _ := flags.GetString("last")
	from, _ := flags.GetString("from")
	to, _ := flags.GetString("to")
	last, from, to = strings.TrimSpace(last), strings.TrimSpace(from), strings.TrimSpace(to)
	if flags.Changed("last") {
		if flags.Changed("from") || flags.Changed("to") {
			return nil, withExitCode(exitUsage, errors.New("--last is a whole time range: leave out --from and --to, or --last"))
		}
		if !savedLogSearchLastPattern.MatchString(last) {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--last %q is not a time range: give a number and a unit, s, m, h or d, such as 15m, 6h or 7d", last))
		}
		return &client.SavedLogSearchSpan{From: "now-" + last, To: "now"}, nil
	}
	if flags.Changed("to") && !flags.Changed("from") {
		return nil, withExitCode(exitUsage, errors.New("--to needs --from: give where the time range starts"))
	}
	if !flags.Changed("from") {
		return nil, nil
	}
	if from == "" {
		return nil, withExitCode(exitUsage, errors.New("--from is empty: give now, now-15m or a UTC moment such as '2026-11-14 14:00'"))
	}
	if to == "" {
		to = "now"
	}
	return &client.SavedLogSearchSpan{From: from, To: to}, nil
}

// readSavedLogSearchQueryFile reads --file (stdin for '-'), refusing an empty
// file and one that is not one JSON object before anything is sent.
func readSavedLogSearchQueryFile(command *cobra.Command) (json.RawMessage, error) {
	filePath, _ := command.Flags().GetString("file")
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil, withExitCode(exitUsage, errors.New("--file is empty: give the query file, or '-' for stdin"))
	}
	var content []byte
	var readError error
	if filePath == "-" {
		content, readError = io.ReadAll(command.InOrStdin())
	} else {
		content, readError = os.ReadFile(filePath)
	}
	if readError != nil {
		return nil, fmt.Errorf("reading the query: %w", readError)
	}
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is empty: give the query, with text, filters and span", filePath))
	}
	if !json.Valid([]byte(trimmed)) || !strings.HasPrefix(trimmed, "{") {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"%s does not hold one JSON object: give the query, with text, filters and span", filePath))
	}
	return json.RawMessage(trimmed), nil
}

// findSavedLogSearch reads the application's saved searches and answers the
// one reference names: its id, or its name as the platform compares names,
// with capitals aside and one space between words. The list is whole, so a
// search that is not on it is not there.
func findSavedLogSearch(command *cobra.Command, applicationID string, applicationReference string,
	reference string) (client.SavedLogSearch, error) {
	searches, listError := apiClient.ListApplicationSavedLogSearches(command.Context(), applicationID)
	if listError != nil {
		return client.SavedLogSearch{}, fmt.Errorf("reading the saved Logs searches of application %s: %w",
			applicationReference, listError)
	}
	wanted := strings.Join(strings.Fields(reference), " ")
	for _, search := range searches.Searches {
		if strings.EqualFold(search.ID, reference) {
			return search, nil
		}
	}
	for _, search := range searches.Searches {
		if strings.EqualFold(strings.Join(strings.Fields(search.Name), " "), wanted) {
			return search, nil
		}
	}
	return client.SavedLogSearch{}, withExitCode(exitNotFound, fmt.Errorf(
		"application %s has no saved Logs search %q: 'ankra application log-searches list %s' lists them",
		applicationReference, reference, applicationReference))
}

// savedLogSearchChangeError is a failed save, replace or remove: what was
// being done and the platform's sentence. A refusal of the name or the query,
// a name taken and the limit reached exit 2, since the same request is
// refused again; a search that is not there exits 3 by its status.
func savedLogSearchChangeError(action string, applicationReference string, changeError error) error {
	var refusal *client.SavedLogSearchRefusalError
	if !errors.As(changeError, &refusal) {
		return fmt.Errorf("%s: %w", action, changeError)
	}
	switch refusal.Code {
	case client.SavedLogSearchCodeNameInvalid, client.SavedLogSearchCodeQueryInvalid, client.SavedLogSearchCodeTooLarge,
		client.SavedLogSearchCodeNameTaken, client.SavedLogSearchCodeLimitReached:
		return withExitCode(exitUsage, fmt.Errorf("%s: %w", action, changeError))
	case client.SavedLogSearchCodeNotFound:
		return fmt.Errorf("%s: %w 'ankra application log-searches list %s' lists them",
			action, changeError, applicationReference)
	default:
		return fmt.Errorf("%s: %w", action, changeError)
	}
}

// writeSavedLogSearches prints the searches as a table, then how many the
// application keeps of the most it may.
func writeSavedLogSearches(output io.Writer, applicationReference string, searches *client.SavedLogSearches) error {
	if len(searches.Searches) == 0 {
		_, _ = fmt.Fprintf(output, "No Logs searches are saved on %s.\n", applicationReference)
		return nil
	}
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "NAME\tSEARCHES\tSAVED BY\tSAVED\tSEARCH ID")
	for _, search := range searches.Searches {
		savedBy := "-"
		if search.CreatedBy.Name != nil && strings.TrimSpace(*search.CreatedBy.Name) != "" {
			savedBy = strings.TrimSpace(*search.CreatedBy.Name)
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", valueOrDashString(search.Name), savedLogSearchWords(search),
			savedBy, valueOrDashString(applicationAlertMoment(search.CreatedAt)), search.ID)
	}
	if flushError := writer.Flush(); flushError != nil {
		return flushError
	}
	if searches.Limit > 0 {
		_, _ = fmt.Fprintf(output, "Saved searches: %d of at most %d.\n", len(searches.Searches), searches.Limit)
	}
	return nil
}

// savedLogSearchWords says in words what a saved search searches. A search
// the platform cannot read says so: it is never shown as one that finds
// every line.
func savedLogSearchWords(search client.SavedLogSearch) string {
	if search.QueryState == client.SavedLogSearchQueryUnreadable {
		return "cannot be read: the platform no longer accepts its stored query; replace or remove it"
	}
	query, readError := search.ReadQuery()
	if readError != nil || query == nil {
		return "the platform sent no query this command can read"
	}
	return describeSavedLogSearchQuery(*query)
}

// describeSavedLogSearchQuery says a query in words: the text, each filter,
// then the time range.
func describeSavedLogSearchQuery(query client.SavedLogSearchQuery) string {
	parts := []string{}
	if query.Text != "" {
		parts = append(parts, "text "+strconv.Quote(query.Text))
	}
	for _, filter := range query.Filters {
		field := savedLogSearchPlainWords(filter.Field)
		if filter.Field == savedLogSearchPodField {
			field = "copy"
		}
		verb := " is "
		if filter.Not {
			verb = " is not "
		}
		parts = append(parts, field+verb+savedLogSearchPlainWords(filter.Value))
	}
	if len(parts) == 0 {
		parts = append(parts, "every line")
	}
	return strings.Join(append(parts, describeSavedLogSearchSpan(query.Span)), ", ")
}

// savedLogSearchPlainWords says a filter's field or value as it is when it is
// one plain word, and quoted otherwise. The platform keeps a value whole, so
// one that another member saved can hold a space, a comma, a line break or a
// terminal control sequence. Quoted, it stays on its row of the table, reads
// apart from the next part, and reaches the terminal as text.
func savedLogSearchPlainWords(text string) string {
	for _, character := range text {
		if character == ' ' || character == ',' || character == '"' || !strconv.IsPrint(character) {
			return strconv.Quote(text)
		}
	}
	return text
}

// describeSavedLogSearchSpan says a time range in words.
func describeSavedLogSearchSpan(span *client.SavedLogSearchSpan) string {
	if span == nil {
		return "the time range on the page"
	}
	if span.To == "now" {
		if match := savedLogSearchRelativePattern.FindStringSubmatch(span.From); match != nil && match[1] != "" {
			count, unit := match[1], savedLogSearchUnitWord(match[2])
			if count == "1" {
				return "the last " + unit
			}
			return "the last " + count + " " + unit + "s"
		}
	}
	return "from " + describeSavedLogSearchTime(span.From) + " to " + describeSavedLogSearchTime(span.To)
}

// describeSavedLogSearchTime says one end of a time range: "now", "2 hours
// ago", or a moment with UTC named when it names no zone of its own.
func describeSavedLogSearchTime(moment string) string {
	if match := savedLogSearchRelativePattern.FindStringSubmatch(moment); match != nil {
		if match[1] == "" {
			return "now"
		}
		unit := savedLogSearchUnitWord(match[2])
		if match[1] != "1" {
			unit += "s"
		}
		return match[1] + " " + unit + " ago"
	}
	if savedLogSearchZonePattern.MatchString(moment) {
		return moment
	}
	return moment + " UTC"
}

func savedLogSearchUnitWord(unit string) string {
	switch unit {
	case "s":
		return "second"
	case "m":
		return "minute"
	case "h":
		return "hour"
	default:
		return "day"
	}
}
