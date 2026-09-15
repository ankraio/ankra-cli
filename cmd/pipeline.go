package cmd

// The `ankra pipeline` family (ankra-vn0bd.2.8, WS-B item B8): the CLI
// surface over go/internal/pipelineapi's four route trees. Every leaf command
// here addresses one pipeline through a PipelineSelector - a repository or
// the application it is linked to - resolved from the shared --application /
// --repository flag pair. `ankra application pipeline …`
// (cmd/application_pipeline.go) is the by-application twin: it forces the
// selector from a leading <application-id> argument instead of a flag, and
// otherwise calls the exact same run* functions this file and its siblings
// define.
//
// `ankra pipeline repositories …` (cmd/pipeline_repositories.go,
// ankra-vn0bd.4.2) is the one exception: it addresses the organisation's
// directory of connected repositories, not one already-linked pipeline, so
// it takes no PipelineSelector.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

func newPipelineCommand() *cobra.Command {
	pipelineCommand := &cobra.Command{
		Use:     "pipeline",
		Aliases: []string{"pipelines"},
		Short:   "Manage Ankra Pipelines",
		Long: `Manage Ankra Pipelines: in-cluster CI/CD runs, the pipeline definition of
record, and its cron schedules.

Every command addresses one pipeline with exactly one of:

  --application <name-or-id>   the application the pipeline builds and deploys
  --repository <repository-id> the pipeline repository directly

There is no lookup route yet for a repository by "owner/name" - only by-id and
by-application addressing exist - so --repository takes the repository's id
(for example, the "repository.id" field 'ankra pipeline definition get'
returns). Most invocations want --application instead.

A run is addressed by its id (the RUN ID column, not the "run_id" field a
JSON/YAML response also carries - that second field is the cross-lifecycle
umbrella run this pipeline run belongs to, useful for correlating with
'ankra cluster operations', but not what these commands accept).
'pipeline get' can also select a run by its commit, branch or trigger, and
wait on or watch it - see 'ankra pipeline get --help'.

'pipeline definitions get|approve' are the one exception: they address a
stored definition directly by its own id and take neither flag - see
'ankra pipeline definitions --help'.`,
	}
	pipelineCommand.AddCommand(
		newPipelineRunCommand(),
		newPipelineListCommand(),
		newPipelineGetCommand(),
		newPipelineCancelCommand(),
		newPipelineRerunCommand(),
		newPipelineLogsCommand(),
		newPipelineArtifactsCommand(),
		newPipelineFindingsCommand(),
		newPipelineValidateCommand(),
		newPipelineDefinitionCommand(),
		newPipelineDefinitionsCommand(),
		newPipelineSchedulesCommand(),
		newPipelineRepositoriesCommand(),
	)
	return pipelineCommand
}

func init() {
	rootCmd.AddCommand(newPipelineCommand())
}

// registerPipelineSelectorFlags adds the shared --application / --repository
// pair to a pipeline command.
func registerPipelineSelectorFlags(command *cobra.Command) {
	command.Flags().String("application", "", "Application name or id whose pipeline to act on")
	command.Flags().String("repository", "", "Pipeline repository id to act on (mutually exclusive with --application)")
}

// resolvePipelineSelector reads --application / --repository and resolves
// them into the PipelineSelector every pipeline client call takes. Exactly
// one of the two must be given.
func resolvePipelineSelector(command *cobra.Command) (client.PipelineSelector, error) {
	applicationReference, _ := command.Flags().GetString("application")
	repositoryID, _ := command.Flags().GetString("repository")
	applicationReference = strings.TrimSpace(applicationReference)
	repositoryID = strings.TrimSpace(repositoryID)

	switch {
	case applicationReference != "" && repositoryID != "":
		return client.PipelineSelector{}, withExitCode(exitUsage,
			errors.New("--application and --repository are mutually exclusive"))
	case applicationReference != "":
		applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
		if resolveError != nil {
			return client.PipelineSelector{}, resolveError
		}
		return client.PipelineSelector{ApplicationID: applicationID}, nil
	case repositoryID != "":
		if !looksLikeUUID(repositoryID) {
			return client.PipelineSelector{}, withExitCode(exitUsage, fmt.Errorf(
				"--repository %q must be the pipeline repository id - there is no lookup by owner/name yet, "+
					"so pass the id from 'ankra pipeline definition get --application <name>' "+
					"(its repository.id field), or use --application instead", repositoryID))
		}
		return client.PipelineSelector{RepositoryID: repositoryID}, nil
	default:
		// Neither flag: the working directory usually answers the question.
		// A user standing in the checkout they want built has already told
		// the shell which application they mean, and making them repeat it
		// as --application is the kind of step this command can simply take
		// off them (ankra-ctsmd). Nothing is guessed silently - the caller
		// prints what was inferred - and an ambiguous or absent answer falls
		// through to the same usage error as before.
		selector, inferred, inferError := pipelineSelectorFromWorkingDirectory(command.Context())
		if inferError != nil {
			return client.PipelineSelector{}, inferError
		}
		if inferred != "" {
			reportInferredPipelineTarget(command, inferred)
			return selector, nil
		}
		return client.PipelineSelector{}, withExitCode(exitUsage,
			errors.New("one of --application or --repository is required"))
	}
}

// pipelineSelectorFromWorkingDirectory answers the application whose
// repository is checked out in the working directory, and the "owner/name"
// it matched so the caller can say so.
//
// Every way of not getting a confident answer - not inside a checkout, no
// origin remote, no application on that repository, or more than one - is an
// empty answer rather than an error, because the caller has a perfectly good
// usage error to fall back on and a wrong inference is worse than none.
func pipelineSelectorFromWorkingDirectory(requestContext context.Context) (client.PipelineSelector, string, error) {
	fullName, matchedIDs, matchedNames, known := checkoutApplications(requestContext)
	if !known {
		return client.PipelineSelector{}, "", nil
	}
	if len(matchedIDs) > 1 {
		// The checkout answered the question and the answer was "more than
		// one". Saying so beats repeating the generic usage line, which
		// would leave the user re-reading a flag they were about to pass.
		sort.Strings(matchedNames)
		return client.PipelineSelector{}, "", withExitCode(exitUsage, fmt.Errorf(
			"%s has %d applications in this organisation (%s); pass --application to say which",
			fullName, len(matchedNames), strings.Join(matchedNames, ", ")))
	}
	if len(matchedIDs) == 0 {
		return client.PipelineSelector{}, "", nil
	}
	return client.PipelineSelector{ApplicationID: matchedIDs[0]}, fullName, nil
}

// checkoutApplications answers the "owner/name" of the repository checked out
// in the working directory and every application bound to it, by id and by
// label. known is false for every way of not having a complete answer - not
// inside a checkout, no origin remote, a failed or partly read listing - so
// callers never mistake an unread page for "no application here".
func checkoutApplications(requestContext context.Context) (string, []string, []string, bool) {
	if apiClient == nil {
		return "", nil, nil, false
	}
	repository, inspectError := inspectLocalApplicationRepository(requestContext, ".", "origin", "")
	if inspectError != nil {
		return "", nil, nil, false
	}
	fullName := repository.Owner + "/" + repository.Name
	matchedIDs := []string{}
	matchedNames := []string{}
	listingExhausted := false
	// The listing is walked unfiltered: the server-side `search` matches an
	// application's NAME, and an application is free to be called something
	// other than the repository it builds, so filtering by the repository
	// name would silently miss exactly those. The walk is bounded the same
	// way resolveApplicationID's is.
	for page := 1; page <= maxApplicationLookupPages; page++ {
		payload, listError := apiClient.ListApplicationsRaw(
			requestContext, page, maxApplicationLookupPageSize, "")
		if listError != nil {
			return "", nil, nil, false
		}
		var listing applicationRepositoryListingPage
		if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
			return "", nil, nil, false
		}
		for _, application := range listing.Result {
			if strings.EqualFold(strings.TrimSpace(application.RepositoryOwner), repository.Owner) &&
				strings.EqualFold(strings.TrimSpace(application.RepositoryName), repository.Name) {
				matchedIDs = append(matchedIDs, application.ID)
				matchedNames = append(matchedNames, applicationLabel(application.Name, application.ID))
			}
		}
		if listing.Pagination.TotalPages <= page || len(listing.Result) == 0 {
			listingExhausted = true
			break
		}
	}
	// A listing that ran past the page cap was only partly read, so "no
	// application on this repository" is not something this walk knows. It
	// answers nothing and the caller asks for the flag, rather than treating
	// an unread page as an absence.
	if !listingExhausted {
		return "", nil, nil, false
	}
	return fullName, matchedIDs, matchedNames, true
}

// applicationRepositoryListingPage is the applications listing read for the
// repository each application is bound to, beside the id.
type applicationRepositoryListingPage struct {
	Result []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		RepositoryOwner string `json:"app_repo_owner"`
		RepositoryName  string `json:"app_repo_name"`
	} `json:"result"`
	Pagination struct {
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// applicationLabel names an application by its name, falling back to its id
// for an application whose definition carries none.
func applicationLabel(name string, id string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return id
}

// reportInferredPipelineTarget says on stderr which application the working
// directory resolved to. Stderr, not stdout, so a structured -o json output
// stays exactly what a caller can parse.
func reportInferredPipelineTarget(command *cobra.Command, fullName string) {
	_, _ = fmt.Fprintf(command.ErrOrStderr(),
		"Using the application bound to %s (pass --application to choose another).\n", fullName)
}

// parsePipelineInputFlags parses repeated --input key=value flags into the
// dispatch input map. A missing '=' or a repeated key is a usage error rather
// than a silently dropped or overwritten input.
func parsePipelineInputFlags(rawInputs []string) (map[string]string, error) {
	if len(rawInputs) == 0 {
		return nil, nil
	}
	inputs := make(map[string]string, len(rawInputs))
	for _, rawInput := range rawInputs {
		key, value, found := strings.Cut(rawInput, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--input %q is not key=value", rawInput))
		}
		if _, isDuplicate := inputs[key]; isDuplicate {
			return nil, withExitCode(exitUsage,
				fmt.Errorf("--input %s given more than once: name each input at most once", key))
		}
		inputs[key] = value
	}
	return inputs, nil
}

// The PipelineRun.Outcome / PipelineStep.Outcome vocabulary: one set for
// runs and steps alike (enginekit/pipelinerun's Outcome* constants, mirrored
// by the outcome CHECK constraints in migrations pipe_003 and pipe_004). An
// outcome is only ever set once the status is "concluded"; the status itself
// is the lifecycle, never the verdict - see pipelineGetLongHelp.
const (
	// pipelineOutcomeSuccess is work that did what was asked.
	pipelineOutcomeSuccess = "success"
	// pipelineOutcomeFailure is the user's own work failing.
	pipelineOutcomeFailure = "failure"
	// pipelineOutcomeCancelled is work stopped on purpose.
	pipelineOutcomeCancelled = "cancelled"
	// pipelineOutcomeTimedOut is work that exceeded its timeout.
	pipelineOutcomeTimedOut = "timed_out"
	// pipelineOutcomeSkipped is work that never ran: a dependency did not
	// succeed, a condition excluded it, or the trigger filter excluded the
	// whole run.
	pipelineOutcomeSkipped = "skipped"
	// pipelineOutcomeInfraError is Ankra failing, not the user's work.
	pipelineOutcomeInfraError = "infra_error"
)

// pipelineErrorClassSuperseded is the run error class that tells a
// supersession apart from a cancel (enginekit/pipelinerun's
// ErrorClassSuperseded). Both settle "cancelled": one because a newer run
// took the concurrency group, the other because somebody stopped it. Only
// the class says which, so it is what the state cell reads.
const pipelineErrorClassSuperseded = "superseded"

// pipelineOutcomeLabel renders a run or step's status/outcome pair as one
// word for a table cell: the outcome once the work has concluded, the status
// while it has not.
func pipelineOutcomeLabel(status string, outcome *string) string {
	if outcome != nil && *outcome != "" {
		return *outcome
	}
	return status
}

// renderPipelineState renders a run or step's status/outcome pair for a
// human-readable cell: pipelineOutcomeLabel's word behind a glyph and a
// colour that mean the same thing everywhere the pipeline commands print a
// state for a person (get and list). wait's failure summary deliberately
// prints bare words instead: it is as often a CI log as a terminal, and
// glyphs and escapes do not belong there (see pipeline_wait.go).
//
// The glyph is the point (PLA-856, support #1178). ⟳ is the spinner, and it
// is reserved for work that has not concluded - queued, pending, running -
// so a run that finished failed never reads as if it were still working.
// The terminal outcomes each get their own: ✓ success; ✗ for failure,
// timed_out and infra_error, which are the three ways work can end badly;
// ⊘ cancelled; ○ skipped. A blocked step is ○ too - it is waiting, not
// working - but in the live colour, since it is going to move. Anything
// outside the vocabulary the server publishes is printed as a bare word,
// because a glyph would claim a meaning the CLI does not know.
func renderPipelineState(status string, outcome *string) string {
	return renderPipelineStateAs(status, outcome, pipelineOutcomeLabel(status, outcome))
}

// renderPipelineRunState is renderPipelineState for a RUN, whose cancelled
// outcome has one thing more to say: a run a newer run superseded reads
// "superseded", not "cancelled". The glyph and the colour stay the ones
// every stopped run carries - it was stopped, and nothing about it needs a
// person's attention - so the word is the only difference.
func renderPipelineRunState(run client.PipelineRun) string {
	return renderPipelineStateAs(run.Status, run.Outcome, pipelineRunStateLabel(run))
}

// pipelineRunStateLabel is the word a run's state cell carries:
// pipelineOutcomeLabel's, except that a run cancelled because a newer run
// took its concurrency group reads "superseded". Nobody stopped that run,
// and "cancelled" sends its author looking for who did.
func pipelineRunStateLabel(run client.PipelineRun) string {
	label := pipelineOutcomeLabel(run.Status, run.Outcome)
	if label == pipelineOutcomeCancelled && pipelineRunErrorClass(run) == pipelineErrorClassSuperseded {
		return pipelineErrorClassSuperseded
	}
	return label
}

// renderPipelineStateAs is the shared body: the glyph and colour for a
// status/outcome pair, around whichever word the caller decided on.
func renderPipelineStateAs(status string, outcome *string, label string) string {
	if outcome == nil || *outcome == "" {
		switch strings.ToLower(status) {
		case pipelineRunStatusQueued, pipelineStepStatusPending, pipelineStepStatusRunning:
			return text.FgYellow.Sprint("⟳ " + label)
		case pipelineStepStatusBlocked:
			return text.FgYellow.Sprint("○ " + label)
		}
		return label
	}
	switch strings.ToLower(*outcome) {
	case pipelineOutcomeSuccess:
		return text.FgGreen.Sprint("✓ " + label)
	case pipelineOutcomeFailure, pipelineOutcomeTimedOut, pipelineOutcomeInfraError:
		return text.FgRed.Sprint("✗ " + label)
	case pipelineOutcomeCancelled:
		return text.FgHiBlack.Sprint("⊘ " + label)
	case pipelineOutcomeSkipped:
		return text.FgHiBlack.Sprint("○ " + label)
	}
	return label
}

func pipelineOptionalString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

// pipelinePageLimitFromFlags reads a listing's --limit. Zero means "let the
// server choose its own page" and is passed through unsent; a negative one
// is refused rather than dropped, because silently answering a smaller
// listing than the caller asked for is the same lie as presenting one page
// as a whole record. A limit above the route's ceiling is left to the
// server, which names its own maximum in the refusal.
func pipelinePageLimitFromFlags(command *cobra.Command) (int, error) {
	limit, _ := command.Flags().GetInt("limit")
	if limit < 0 {
		return 0, withExitCode(exitUsage,
			fmt.Errorf("--limit must be a positive number of rows, got %d", limit))
	}
	return limit, nil
}

// pipelineShortSHA renders a commit for a table cell: short enough to scan,
// long enough that two commits on the same branch stay distinguishable.
func pipelineShortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
