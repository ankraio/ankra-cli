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
	if apiClient == nil {
		return client.PipelineSelector{}, "", nil
	}
	repository, inspectError := inspectLocalApplicationRepository(requestContext, ".", "origin", "")
	if inspectError != nil {
		return client.PipelineSelector{}, "", nil
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
			return client.PipelineSelector{}, "", nil
		}
		var listing applicationRepositoryListingPage
		if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
			return client.PipelineSelector{}, "", nil
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

// pipelineOutcomeLabel renders a run or step's status/outcome pair as one
// word for a table cell: the outcome once the work has concluded, the status
// while it has not.
func pipelineOutcomeLabel(status string, outcome *string) string {
	if outcome != nil && *outcome != "" {
		return *outcome
	}
	return status
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
