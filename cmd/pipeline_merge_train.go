package cmd

// The Ankra merge train (cluster ankra-q573dh.11): a repository's queue of
// pull requests, each tested squashed onto the base branch and the pull
// requests ahead of it - exactly as its merge will land - and merged when that
// run passes, so the merge's own pipeline promotes the tested image instead of
// rebuilding it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newPipelineTrainCommand() *cobra.Command {
	trainCommand := &cobra.Command{
		Use:     "train",
		Aliases: []string{"merge-train"},
		Short:   "Manage a repository's merge train",
		Long: `Manage a repository's Ankra merge train.

The merge train tests each pull request squashed onto the base branch and the
pull requests ahead of it, exactly as its merge will land, and merges it
through the repository host's own merge when that run passes. Every merge is
then a tree a pipeline run already passed, so a definition whose publish stage
declares promote_from: pull_request publishes the tested image instead of
rebuilding it.

The train is off by default: 'ankra pipeline train enable' switches it on
(pipelines.manage). 'ankra pipeline train add <pull request>' puts a pull
request at the end of it, 'list' shows it in order and 'remove' takes one out.
An organisation administrator can move a pull request to the front with
'front'. GitHub repositories only.

'ankra pipeline train enable --auto-admit on' lets the train add green pull
requests by itself; 'shadow' only records which ones it would add, and 'list'
shows its latest decisions. A pull request based on another pull request's
branch rides on that pull request's entry, shown as "(on #N)".

'--auto-admit-criterion' says what auto-admit waits for: green, the default,
waits for a pull request's own run to pass, and ready adds it when it is marked
ready, before that run has passed. 'ankra pipeline train enable --help' has the
details and the protected paths that keep the green rule at ready.

The train merges under the Ankra GitHub App's identity, so adding or removing
a pull request needs pipelines.operate and, on top of it, one of: you are an
organisation administrator, you opened the pull request, or you have write
access to the repository on GitHub. Ankra reads the last two through the
GitHub identity you linked with OAuth from the Ankra portal. One exception:
with pipelines.operate you can always remove an entry you added yourself,
with or without a linked identity. Otherwise, without a linked identity only
an administrator can add or remove. If GitHub cannot be asked, the request is
refused and nothing changes.`,
	}
	trainCommand.AddCommand(
		newPipelineTrainListCommand(),
		newPipelineTrainAddCommand(),
		newPipelineTrainRemoveCommand(),
		newPipelineTrainFrontCommand(),
		newPipelineTrainSettingsCommand("enable", "Switch the merge train on", true),
		newPipelineTrainSettingsCommand("disable", "Switch the merge train off and empty it", false),
	)
	return trainCommand
}

// maxPipelineRepositoryLookupPages bounds the repository listing walk a train
// command makes to find the checkout's repository. A platform that honours
// the owner/name filter answers in one page; an older one answers the
// unfiltered listing, and an organisation with more than this many pages of
// repositories is told to pass --repository rather than walked forever.
const maxPipelineRepositoryLookupPages = 20

// resolvePipelineTrainSelector is resolvePipelineSelector for the merge train
// commands, with one more way to answer from the checkout (ankra-q573dh.37).
// A train belongs to a pipeline repository, and a connected repository need
// not have an application bound to it (cluster and ankra-cli have none), so
// when neither flag is given and no single application answers for the
// checkout's origin, the repository itself is looked up by the origin's
// owner/name. An application that does answer is still used first, exactly
// as every other pipeline command resolves it.
func resolvePipelineTrainSelector(command *cobra.Command) (client.PipelineSelector, error) {
	applicationReference, _ := command.Flags().GetString("application")
	repositoryReference, _ := command.Flags().GetString("repository")
	if strings.TrimSpace(applicationReference) != "" || strings.TrimSpace(repositoryReference) != "" {
		return resolvePipelineSelector(command)
	}
	selector, inferred, inferError := pipelineSelectorFromWorkingDirectory(command.Context())
	if inferError == nil && inferred != "" {
		reportInferredPipelineTarget(command, inferred)
		return selector, nil
	}
	// Two applications bound to one repository is ambiguous for a run, but
	// not for a train: both name the same repository, and so the same train.
	// The repository lookup below answers it; inferError is kept for the one
	// case the lookup cannot run at all.
	owner, name, isCheckout := checkoutGitHubOrigin(command.Context())
	if !isCheckout {
		if inferError != nil {
			return client.PipelineSelector{}, inferError
		}
		return client.PipelineSelector{}, withExitCode(exitUsage,
			errors.New("one of --application or --repository is required"))
	}
	repository, lookupError := findPipelineRepositoryByIdentity(command.Context(), owner, name)
	if lookupError != nil {
		return client.PipelineSelector{}, lookupError
	}
	_, _ = fmt.Fprintf(command.ErrOrStderr(),
		"Using the pipeline repository %s/%s (pass --repository to choose another).\n",
		repository.Owner, repository.Name)
	return client.PipelineSelector{RepositoryID: repository.ID}, nil
}

// checkoutGitHubOrigin answers the owner/name of the GitHub repository the
// working directory's origin points at. isCheckout is false when there is no
// such answer - not inside a checkout, no origin remote, or an origin that is
// not a github.com repository - and the caller falls back to asking for a
// flag, since the merge train serves GitHub repositories only.
func checkoutGitHubOrigin(requestContext context.Context) (string, string, bool) {
	remoteURL, remoteError := executeGit(requestContext, ".", "remote", "get-url", "origin")
	if remoteError != nil {
		return "", "", false
	}
	owner, name, parseError := parseGitHubRepositoryRemote(strings.TrimSpace(remoteURL))
	if parseError != nil {
		return "", "", false
	}
	return owner, name, true
}

// findPipelineRepositoryByIdentity looks a connected GitHub repository up by
// owner/name through GET /org/pipelines/repositories. The filter is sent, but
// every row is still matched here without case: a platform older than cluster
// ankra-q573dh.37 ignores owner and name and answers the unfiltered listing.
//
// Three answers, never two: the repository; not connected (exitNotFound, a
// listing read to its end that holds no such repository); or an error when
// the listing could not be read or was longer than the walk reads, which says
// nothing about whether the repository is connected.
func findPipelineRepositoryByIdentity(requestContext context.Context, owner string,
	name string) (client.PipelineRepository, error) {
	options := client.ListPipelineRepositoriesOptions{Provider: "github", Owner: owner, Name: name, Limit: 100}
	for page := 0; page < maxPipelineRepositoryLookupPages; page++ {
		listing, listError := apiClient.ListPipelineRepositories(requestContext, options)
		if listError != nil {
			return client.PipelineRepository{}, fmt.Errorf(
				"looking %s/%s up among the organisation's pipeline repositories: %w", owner, name, listError)
		}
		for _, repository := range listing.Repositories {
			if strings.EqualFold(repository.Provider, "github") &&
				strings.EqualFold(repository.Owner, owner) && strings.EqualFold(repository.Name, name) {
				return repository, nil
			}
		}
		if listing.NextCursor == nil || *listing.NextCursor == "" {
			return client.PipelineRepository{}, withExitCode(exitNotFound, fmt.Errorf(
				"%s/%s is not connected to Ankra Pipelines in this organisation and no application is bound to it, "+
					"so it has no merge train; connect it with 'ankra pipeline repositories connect' "+
					"or pass --application / --repository", owner, name))
		}
		options.Cursor = *listing.NextCursor
	}
	return client.PipelineRepository{}, fmt.Errorf(
		"looked through %d pages of pipeline repositories without reaching %s/%s; pass --repository with its id",
		maxPipelineRepositoryLookupPages, owner, name)
}

func newPipelineTrainListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show the merge train in order, and what recently left it",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineTrainSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTrainList(command, selector)
		},
	}
	registerPipelineSelectorFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func runPipelineTrainList(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	train, readError := apiClient.GetPipelineMergeTrain(command.Context(), selector)
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, train)
	}
	output := command.OutOrStdout()
	state := "off"
	if train.Settings.Enabled {
		state = "on"
	}
	_, _ = fmt.Fprintf(output, "Merge train: %s, %d car(s) tested at once.\n", state, train.Settings.MaxCars)
	printPipelineTrainAutoAdmit(output, train.Settings)
	if len(train.Entries) == 0 {
		_, _ = fmt.Fprintln(output, "No pull requests in the train.")
	} else {
		writer := table.NewWriter()
		writer.SetOutputMirror(output)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"#", "PULL REQUEST", "STATUS", "ATTEMPT", "CAR", "RUN", "ENTRY ID"})
		for index, entry := range train.Entries {
			writer.AppendRow(table.Row{
				index + 1,
				pipelineTrainPullRequestLabel(entry),
				pipelineTrainStatusLabel(entry),
				entry.Attempt,
				pipelineTrainShortSHA(entry.CarCommitSHA),
				pipelineTrainOptional(entry.PipelineRunID),
				entry.ID,
			})
		}
		writer.Render()
	}
	if len(train.Recent) > 0 {
		_, _ = fmt.Fprintln(output, "\nRecently left the train:")
		writer := table.NewWriter()
		writer.SetOutputMirror(output)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"PULL REQUEST", "STATUS", "WHY", "MERGED AS", "TESTED AS MERGED"})
		for _, entry := range train.Recent {
			writer.AppendRow(table.Row{
				pipelineTrainPullRequestLabel(entry),
				entry.Status,
				pipelineTrainOptional(entry.ReasonText),
				pipelineTrainShortSHA(entry.MergeCommitSHA),
				pipelineTrainTreeMatch(entry.MergeTreeMatches),
			})
		}
		writer.Render()
	}
	if len(train.AutoAdmitDecisions) > 0 {
		_, _ = fmt.Fprintln(output, "\nAuto-admit's latest decisions:")
		writer := table.NewWriter()
		writer.SetOutputMirror(output)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"PULL REQUEST", "HEAD", "MODE", "OUTCOME", "WHY", "DECIDED AT"})
		for index, decision := range train.AutoAdmitDecisions {
			if index == maxPipelineTrainDecisionsShown {
				break
			}
			head := decision.HeadSHA
			writer.AppendRow(table.Row{
				fmt.Sprintf("#%d", decision.PullRequestNumber),
				pipelineTrainShortSHA(&head),
				decision.Mode,
				decision.Outcome,
				pipelineTrainOptional(decision.Detail),
				decision.DecidedAt,
			})
		}
		writer.Render()
		if len(train.AutoAdmitDecisions) > maxPipelineTrainDecisionsShown {
			_, _ = fmt.Fprintf(output, "%d more; -o json lists them all.\n",
				len(train.AutoAdmitDecisions)-maxPipelineTrainDecisionsShown)
		}
	}
	return nil
}

// maxPipelineTrainDecisionsShown bounds the auto-admit decisions the table
// view prints; -o json carries every one the platform answered.
const maxPipelineTrainDecisionsShown = 10

// isPipelineTrainAutoAdmitMode reports whether mode is one the platform
// accepts for auto_admit.
func isPipelineTrainAutoAdmitMode(mode string) bool {
	return mode == "off" || mode == "shadow" || mode == "on"
}

// pipelineTrainAutoAdmitSentence says what auto-admit does in settings, and
// who set it. It is empty for a platform that sent no mode, which predates
// auto-admit, so nothing is claimed about it.
func pipelineTrainAutoAdmitSentence(settings client.PipelineMergeTrainSettings) string {
	var sentence string
	switch settings.AutoAdmit {
	case "":
		return ""
	case "off":
		sentence = "Auto-admit: off; pull requests enter only when someone adds them."
	case "shadow":
		sentence = "Auto-admit: shadow; the train records which green pull requests it would add, and adds none."
		if settings.AutoAdmitCriterion == client.PipelineMergeTrainCriterionReady {
			sentence = "Auto-admit: shadow; the train records which pull requests it would add at ready, and adds none."
		}
	case "on":
		sentence = "Auto-admit: on; the train adds green pull requests by itself."
		if settings.AutoAdmitCriterion == client.PipelineMergeTrainCriterionReady {
			sentence = "Auto-admit: on; the train adds pull requests by itself when they are marked ready."
		}
	default:
		sentence = "Auto-admit: " + settings.AutoAdmit + "."
	}
	if settings.AutoAdmitBy != nil && *settings.AutoAdmitBy != "" {
		sentence += " Set by " + *settings.AutoAdmitBy
		if settings.AutoAdmitSince != nil && *settings.AutoAdmitSince != "" {
			sentence += " at " + *settings.AutoAdmitSince
		}
		sentence += "."
	}
	return sentence
}

// printPipelineTrainAutoAdmit prints what auto-admit does in settings, leaving
// out whatever the platform did not report.
func printPipelineTrainAutoAdmit(output io.Writer, settings client.PipelineMergeTrainSettings) {
	for _, sentence := range []string{
		pipelineTrainAutoAdmitSentence(settings),
		pipelineTrainCriterionSentence(settings),
		pipelineTrainProtectedPathsSentence(settings),
	} {
		if sentence != "" {
			_, _ = fmt.Fprintln(output, sentence)
		}
	}
}

// pipelineTrainCriterionSentence says what auto-admit waits for. It is empty
// for a platform that sent no criterion, which predates it: nobody chose green
// there, so nothing is claimed.
func pipelineTrainCriterionSentence(settings client.PipelineMergeTrainSettings) string {
	var sentence string
	switch settings.AutoAdmitCriterion {
	case "":
		return ""
	case client.PipelineMergeTrainCriterionGreen:
		sentence = "Auto-admit criterion: green; a pull request enters once its own run has passed."
	case client.PipelineMergeTrainCriterionReadyShadow:
		sentence = "Auto-admit criterion: ready_shadow; a pull request still enters once its own run has passed, " +
			"and the train records which ones it would have added at ready."
	case client.PipelineMergeTrainCriterionReady:
		sentence = "Auto-admit criterion: ready; a pull request enters the train when it is marked ready, before " +
			"its own run has passed, unless a step of that run already failed. One that changes a protected path " +
			"keeps the green rule and waits for its own run to pass."
	default:
		sentence = "Auto-admit criterion: " + settings.AutoAdmitCriterion + "."
	}
	switch settings.AutoAdmit {
	case "off":
		sentence += " Auto-admit is off, so nothing enters this way until it is on."
	case "shadow":
		sentence += " Auto-admit is in shadow, so this is only recorded until it is on."
	}
	return sentence
}

// pipelineTrainProtectedPathsSentence names the paths that keep the green rule
// at ready. It is empty for a platform that sent no list, which is "not
// reported": only a list the platform sent empty is "none". The patterns end
// the line, with no full stop that could read as part of the last one.
func pipelineTrainProtectedPathsSentence(settings client.PipelineMergeTrainSettings) string {
	if settings.AutoAdmitProtectedPaths == nil {
		return ""
	}
	patterns := *settings.AutoAdmitProtectedPaths
	if len(patterns) == 0 {
		return "Protected paths: none; at ready, no path keeps the green rule."
	}
	label := "Protected paths"
	if settings.AutoAdmitProtectedPathsIsDefault != nil && *settings.AutoAdmitProtectedPathsIsDefault {
		label = "Protected paths (the default list)"
	}
	return label + ": " + strings.Join(patterns, ", ")
}

// pipelineTrainReadyCarSentence qualifies a write that named ready: the sentence
// a read prints says what enters, and this one what that costs, which a person
// deciding it needs once rather than on every list.
const pipelineTrainReadyCarSentence = "At ready the car's run is the only run that has to pass before the merge, " +
	"so use it only once this repository's cars run everything its pull request runs do."

// pipelineTrainCriterionFromFlag reads --auto-admit-criterion, nil when it is
// not named. The vocabulary is closed, so a misspelling is refused here before
// anything is sent; the platform validates it again.
func pipelineTrainCriterionFromFlag(command *cobra.Command) (*string, error) {
	if !command.Flags().Changed("auto-admit-criterion") {
		return nil, nil
	}
	named, _ := command.Flags().GetString("auto-admit-criterion")
	criterion := strings.ToLower(strings.TrimSpace(named))
	if !slices.Contains(client.PipelineMergeTrainCriteria, criterion) {
		return nil, withExitCode(exitUsage, fmt.Errorf("--auto-admit-criterion must be %s, got %q",
			strings.Join(client.PipelineMergeTrainCriteria, ", "), named))
	}
	return &criterion, nil
}

// pipelineTrainProtectedPathsFromFlags reads the protected path flags into the
// three things the platform takes: nil when none is named, a pointer to a nil
// slice to restore the default list, and a pointer to the list otherwise.
//
// An empty --auto-admit-protected-paths is refused rather than sent as []. That
// list lets migrations and deploy values into the train before their own run
// passed, and a script whose variable came out empty must not choose it, so
// protecting nothing takes its own flag.
func pipelineTrainProtectedPathsFromFlags(command *cobra.Command) (*[]string, error) {
	if isDefault, _ := command.Flags().GetBool("auto-admit-protected-paths-default"); isDefault {
		defaultList := []string(nil)
		return &defaultList, nil
	}
	if isNone, _ := command.Flags().GetBool("auto-admit-protected-paths-none"); isNone {
		nothing := []string{}
		return &nothing, nil
	}
	if !command.Flags().Changed("auto-admit-protected-paths") {
		return nil, nil
	}
	named, _ := command.Flags().GetStringSlice("auto-admit-protected-paths")
	if len(named) == 0 {
		return nil, withExitCode(exitUsage, errors.New("--auto-admit-protected-paths names no pattern: name at least "+
			"one, or pass --auto-admit-protected-paths-default for the default list or "+
			"--auto-admit-protected-paths-none to protect nothing"))
	}
	if len(named) > client.PipelineMergeTrainProtectedPathsLimit {
		return nil, withExitCode(exitUsage, fmt.Errorf("--auto-admit-protected-paths takes at most %d patterns, got %d",
			client.PipelineMergeTrainProtectedPathsLimit, len(named)))
	}
	patterns := make([]string, 0, len(named))
	for _, pattern := range named {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" || trimmed == "!" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--auto-admit-protected-paths has an empty pattern: %q",
				strings.Join(named, ",")))
		}
		if len(trimmed) > client.PipelineMergeTrainProtectedPathLengthLimit {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--auto-admit-protected-paths takes patterns of at most %d characters, one has %d",
				client.PipelineMergeTrainProtectedPathLengthLimit, len(trimmed)))
		}
		patterns = append(patterns, trimmed)
	}
	return &patterns, nil
}

// pipelineTrainCriterionNotStored is the error for a write that named a
// criterion the platform's answer does not carry: the platform predates it, so
// whatever else the write named is stored and the criterion is not.
func pipelineTrainCriterionNotStored(request client.SetPipelineMergeTrainSettingsRequest,
	settings *client.PipelineMergeTrainSettings) error {
	if request.AutoAdmitCriterion == nil || settings.AutoAdmitCriterion != "" {
		return nil
	}
	return fmt.Errorf("the platform did not store --auto-admit-criterion %s: it predates the auto-admit criterion; "+
		"the other settings shown were stored", *request.AutoAdmitCriterion)
}

// pipelineTrainProtectedPathsNotStored is pipelineTrainCriterionNotStored for
// the protected paths, whichever of the three flags named them.
func pipelineTrainProtectedPathsNotStored(request client.SetPipelineMergeTrainSettingsRequest,
	settings *client.PipelineMergeTrainSettings) error {
	if request.AutoAdmitProtectedPaths == nil || settings.AutoAdmitProtectedPaths != nil {
		return nil
	}
	return errors.New("the platform did not store the protected paths: it predates them; " +
		"the other settings shown were stored")
}

const pipelineTrainEnableLong = `Switch the merge train on and set how it runs. A flag you leave out keeps its
current value.

--auto-admit off|shadow|on decides whether the train adds pull requests by
itself. shadow only records which ones it would add.

--auto-admit-criterion green|ready_shadow|ready says what auto-admit waits for
once it is on. Every other auto-admit rule applies under all three: a draft, an
ankra:hold label or a blocked review keeps a pull request out.

  green         A pull request enters once its own run has passed. Every
                repository's default.
  ready_shadow  Still enters at green, and records which pull requests ready
                would have added while their own run was going, as
                ready_shadow_admit in 'ankra pipeline train list'. Use it to
                read what ready would do first.
  ready         A pull request enters when it is marked ready, before its own
                run has passed, unless a step of that run already failed or
                GitHub says it does not merge cleanly. If its own run fails
                before its car has passed, it leaves the train. A pull request
                that changes a protected path, or whose list of changes could
                not be read in full, keeps the green rule and waits for its own
                run to pass.

At ready the car's run is the only run that has to pass before the merge, so
use it only once this repository's cars run everything its pull request runs
do. Going back to green leaves the entries already in the train where they are.

--auto-admit-protected-paths <glob,...> sets the paths that keep the green rule
at ready: at most 50 GitHub-style patterns of at most 256 characters, where the
last matching pattern wins and a leading ! excludes.
--auto-admit-protected-paths-default restores the default list, alembic/**,
deploy/charts/*/values.yaml and deploy/charts/*/values.schema.json.
--auto-admit-protected-paths-none protects nothing, so migrations and deploy
values enter at ready too. An empty --auto-admit-protected-paths is refused.

A service account token may set green or ready_shadow. It may not set ready or
change the protected paths: those need a human member, and the platform refuses
the token with its reason.`

const pipelineTrainEnableExample = `  ankra pipeline train enable --max-cars 4
  ankra pipeline train enable --auto-admit on --auto-admit-criterion ready_shadow
  ankra pipeline train enable --auto-admit-criterion ready
  ankra pipeline train enable --auto-admit-protected-paths 'alembic/**,deploy/**'
  ankra pipeline train enable --auto-admit-protected-paths-default`

func newPipelineTrainAddCommand() *cobra.Command {
	addCommand := &cobra.Command{
		Use:   "add <pull request number>",
		Short: "Put a pull request at the end of the merge train",
		Long: `Put a pull request at the end of the merge train.

The train tests the pull request's head as it is now: pass --head-sha to pin
the commit you reviewed. A push to the pull request after it entered takes it
out of the train again. A pull request already in the train is answered with
its entry.

You must be an organisation administrator, the pull request's author, or have
write access to the repository on GitHub (through the GitHub identity you
linked from the Ankra portal), as well as pipelines.operate.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineTrainSelector(command)
			if selectorError != nil {
				return selectorError
			}
			number, parseError := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(arguments[0]), "#"), 10, 64)
			if parseError != nil || number <= 0 {
				return withExitCode(exitUsage, fmt.Errorf("%q is not a pull request number", arguments[0]))
			}
			headSHA, _ := command.Flags().GetString("head-sha")
			entry, enqueueError := apiClient.EnqueuePipelineMergeTrain(command.Context(), selector,
				client.EnqueuePipelineMergeTrainRequest{PullRequestNumber: number, HeadSHA: strings.TrimSpace(headSHA)})
			if enqueueError != nil {
				return enqueueError
			}
			if isStructured, renderError := renderStructured(command, entry); isStructured || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Pull request #%d is in the merge train (%s, entry %s).\n",
				entry.PullRequestNumber, pipelineTrainStatusLabel(*entry), entry.ID)
			return nil
		},
	}
	registerPipelineSelectorFlags(addCommand)
	addCommand.Flags().String("head-sha", "", "Full commit sha to pin as the head the train tests and merges")
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

func newPipelineTrainRemoveCommand() *cobra.Command {
	removeCommand := &cobra.Command{
		Use:     "remove <entry id>",
		Aliases: []string{"rm"},
		Short:   "Take a pull request out of the merge train",
		Long: `Take a pull request out of the merge train.

You can always remove an entry you added yourself. Removing anyone else's needs
the authority adding it would: organisation administrator, the pull request's
author, or write access to the repository on GitHub, as well as
pipelines.operate.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineTrainSelector(command)
			if selectorError != nil {
				return selectorError
			}
			entry, dequeueError := apiClient.DequeuePipelineMergeTrain(command.Context(), selector,
				strings.TrimSpace(arguments[0]))
			if dequeueError != nil {
				return dequeueError
			}
			if isStructured, renderError := renderStructured(command, entry); isStructured || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Pull request #%d left the merge train.\n",
				entry.PullRequestNumber)
			return nil
		},
	}
	registerPipelineSelectorFlags(removeCommand)
	registerStructuredOutputFlags(removeCommand)
	return removeCommand
}

func newPipelineTrainFrontCommand() *cobra.Command {
	frontCommand := &cobra.Command{
		Use:   "front <pull request number | entry id>",
		Short: "Move a pull request to the front of the merge train",
		Long: `Move a pull request to the front of the merge train.

The pull request goes ahead of every car that is still queued, building or
testing. Cars that already passed or are being merged stay ahead of it. Every
car it passes is built again on top of it and its running test is stopped, so
those pull requests wait longer.

Name the pull request by its number, as 'ankra pipeline train list' shows it,
or by its entry id. Only a pull request that is queued, building or testing
can move.

You must be an organisation administrator, as well as hold pipelines.operate.
Being the pull request's author or having write access is not enough, because
moving it delays pull requests other people added.`,
		Example: `  ankra pipeline train front 776
  ankra pipeline train front 59a73935-a162-4c30-8a17-616a1cfcedee`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineTrainSelector(command)
			if selectorError != nil {
				return selectorError
			}
			entryID, resolveError := resolvePipelineTrainEntryID(command.Context(), selector, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			entry, moveError := apiClient.MovePipelineMergeTrainToFront(command.Context(), selector, entryID)
			if moveError != nil {
				return moveError
			}
			if isStructured, renderError := renderStructured(command, entry); isStructured || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(),
				"Pull request #%d is at the front of the merge train (%s, entry %s).\n",
				entry.PullRequestNumber, pipelineTrainStatusLabel(*entry), entry.ID)
			return nil
		},
	}
	registerPipelineSelectorFlags(frontCommand)
	registerStructuredOutputFlags(frontCommand)
	return frontCommand
}

// resolvePipelineTrainEntryID answers the entry id a person named: an entry id
// as given, or the entry of the pull request whose number they typed (with or
// without a leading #), read from the train.
func resolvePipelineTrainEntryID(ctx context.Context, selector client.PipelineSelector,
	argument string) (string, error) {
	trimmed := strings.TrimSpace(argument)
	number, parseError := strconv.ParseInt(strings.TrimPrefix(trimmed, "#"), 10, 64)
	if parseError != nil {
		if trimmed == "" {
			return "", withExitCode(exitUsage, errors.New("name a pull request number or an entry id"))
		}
		return trimmed, nil
	}
	if number <= 0 {
		return "", withExitCode(exitUsage, fmt.Errorf("%q is not a pull request number", argument))
	}
	train, readError := apiClient.GetPipelineMergeTrain(ctx, selector)
	if readError != nil {
		return "", readError
	}
	for _, entry := range train.Entries {
		if entry.PullRequestNumber == number {
			return entry.ID, nil
		}
	}
	return "", withExitCode(exitNotFound, fmt.Errorf("pull request #%d is not in the merge train; add it with "+
		"'ankra pipeline train add %d'", number, number))
}

func newPipelineTrainSettingsCommand(use string, short string, isEnabled bool) *cobra.Command {
	settingsCommand := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			criterion, criterionError := pipelineTrainCriterionFromFlag(command)
			if criterionError != nil {
				return criterionError
			}
			protectedPaths, protectedPathsError := pipelineTrainProtectedPathsFromFlags(command)
			if protectedPathsError != nil {
				return protectedPathsError
			}
			selector, selectorError := resolvePipelineTrainSelector(command)
			if selectorError != nil {
				return selectorError
			}
			request := client.SetPipelineMergeTrainSettingsRequest{Enabled: isEnabled, AutoAdmitCriterion: criterion,
				AutoAdmitProtectedPaths: protectedPaths}
			if command.Flags().Lookup("max-cars") != nil && command.Flags().Changed("max-cars") {
				maxCars, _ := command.Flags().GetInt("max-cars")
				if maxCars < 1 || maxCars > 8 {
					return withExitCode(exitUsage, fmt.Errorf("--max-cars must be between 1 and 8"))
				}
				request.MaxCars = &maxCars
			}
			if command.Flags().Lookup("auto-admit") != nil && command.Flags().Changed("auto-admit") {
				autoAdmit, _ := command.Flags().GetString("auto-admit")
				autoAdmit = strings.ToLower(strings.TrimSpace(autoAdmit))
				if !isPipelineTrainAutoAdmitMode(autoAdmit) {
					return withExitCode(exitUsage, fmt.Errorf("--auto-admit must be off, shadow or on"))
				}
				request.AutoAdmit = &autoAdmit
			}
			settings, setError := apiClient.SetPipelineMergeTrainSettings(command.Context(), selector, request)
			if setError != nil {
				return setError
			}
			// A platform older than the criterion ignores the fields and answers
			// without them. Saying nothing and exiting 0 would tell the caller a
			// rule is in force that the platform never stored.
			unstoredError := errors.Join(pipelineTrainCriterionNotStored(request, settings),
				pipelineTrainProtectedPathsNotStored(request, settings))
			isStructured, renderError := renderStructured(command, settings)
			if renderError != nil {
				return renderError
			}
			if isStructured {
				return unstoredError
			}
			if settings.Enabled {
				_, _ = fmt.Fprintf(command.OutOrStdout(), "The merge train is on, testing %d car(s) at once.\n",
					settings.MaxCars)
				printPipelineTrainAutoAdmit(command.OutOrStdout(), *settings)
				if criterion != nil && *criterion == client.PipelineMergeTrainCriterionReady &&
					settings.AutoAdmitCriterion != "" {
					_, _ = fmt.Fprintln(command.OutOrStdout(), pipelineTrainReadyCarSentence)
				}
				return unstoredError
			}
			_, _ = fmt.Fprintln(command.OutOrStdout(),
				"The merge train is off; the pull requests in it leave on its next pass.")
			return unstoredError
		},
	}
	registerPipelineSelectorFlags(settingsCommand)
	if isEnabled {
		settingsCommand.Flags().Int("max-cars", 1,
			"How many pull requests are tested at once (1-8), each stacked on the ones ahead of it")
		settingsCommand.Flags().String("auto-admit", "",
			"Whether the train puts pull requests in by itself: off, shadow (only records what it "+
				"would admit) or on; omitted keeps the current mode")
		settingsCommand.Long = pipelineTrainEnableLong
		settingsCommand.Example = pipelineTrainEnableExample
		settingsCommand.Flags().String("auto-admit-criterion", "",
			"What auto-admit waits for: green (its own run passed), ready_shadow (green, and records what ready "+
				"would add) or ready (marked ready, before its own run passed); omitted keeps the current one")
		settingsCommand.Flags().StringSlice("auto-admit-protected-paths", nil,
			"Glob patterns, comma-separated, that keep the green rule at ready; omitted keeps the current list")
		settingsCommand.Flags().Bool("auto-admit-protected-paths-default", false,
			"Restore the default protected paths")
		settingsCommand.Flags().Bool("auto-admit-protected-paths-none", false,
			"Protect no path, so migrations and deploy values enter at ready too")
		settingsCommand.MarkFlagsMutuallyExclusive("auto-admit-protected-paths", "auto-admit-protected-paths-default",
			"auto-admit-protected-paths-none")
	}
	registerStructuredOutputFlags(settingsCommand)
	return settingsCommand
}

func pipelineTrainPullRequestLabel(entry client.PipelineMergeTrainEntry) string {
	label := fmt.Sprintf("#%d", entry.PullRequestNumber)
	if title := strings.TrimSpace(entry.PullRequestTitle); title != "" {
		if len([]rune(title)) > 48 {
			title = string([]rune(title)[:47]) + "…"
		}
		label += " " + title
	}
	if entry.ParentPullRequestNumber != nil && *entry.ParentPullRequestNumber > 0 {
		label += fmt.Sprintf(" (on #%d)", *entry.ParentPullRequestNumber)
	}
	return label
}

func pipelineTrainStatusLabel(entry client.PipelineMergeTrainEntry) string {
	if entry.Status == "queued" && entry.Reason != nil && *entry.Reason != "" {
		return "queued (" + *entry.Reason + ")"
	}
	return entry.Status
}

func pipelineTrainShortSHA(sha *string) string {
	if sha == nil || *sha == "" {
		return "-"
	}
	if len(*sha) > 7 {
		return (*sha)[:7]
	}
	return *sha
}

func pipelineTrainOptional(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func pipelineTrainTreeMatch(matches *bool) string {
	switch {
	case matches == nil:
		return "-"
	case *matches:
		return "yes"
	default:
		return "no"
	}
}
