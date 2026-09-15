package cmd

// `ankra pipeline branches` (ankra-n8q38.2): one row per ref of a pipeline's
// repository, carrying the newest run on it. `pipeline list
// --latest-per-branch` is the same table under the listing command, for the
// hands already typing `pipeline list`.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineBranchesLongHelp is the help both `pipeline branches` and
// `pipeline list --latest-per-branch` point at.
const pipelineBranchesLongHelp = `List a pipeline's branches, one row per ref, with the newest run on each.

The repository's default branch leads the listing; everything else follows by
most recent activity. Each row carries the latest run on the ref, the outcome
of the run concluded before it (PREVIOUS), how many runs the ref has ever had,
and how many of those a newer commit superseded - a run cancelled because
something newer took its concurrency group, which is history rather than a
failure to look at.

Branches with no run in the last fourteen days are hidden; --all shows them.
The count of what was hidden is printed after the table.

STATUS is the latest run's lifecycle - queued, running or concluded - and
OUTCOME its verdict, which is empty until the run concludes. 'ankra pipeline
get --help' documents both fields.`

func newPipelineBranchesCommand() *cobra.Command {
	branchesCommand := &cobra.Command{
		Use:     "branches",
		Aliases: []string{"branch"},
		Short:   "List a pipeline's branches with the latest run on each",
		Long:    pipelineBranchesLongHelp,
		Example: `  # Where does every branch of the application you are standing in stand
  ankra pipeline branches

  # Include the branches nothing has run on for a fortnight
  ankra pipeline branches --application orders-api --all

  # The branch whose latest run did not succeed, for a script
  ankra pipeline branches --application orders-api -o json | jq '.branches[] | select(.latest_run.outcome != "success")'`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineBranches(command, selector)
		},
	}
	registerPipelineSelectorFlags(branchesCommand)
	registerPipelineBranchesFlags(branchesCommand)
	registerStructuredOutputFlags(branchesCommand)
	return branchesCommand
}

// registerPipelineBranchesFlags declares the branch listing's own paging and
// its one filter. `pipeline list --latest-per-branch` declares the same three
// on the run listing (registerPipelineListFlags), which is what lets the
// by-application twin reach this table without a second command.
func registerPipelineBranchesFlags(command *cobra.Command) {
	command.Flags().Bool("all", false,
		"Include branches with no run in the last 14 days, which are hidden by default")
	command.Flags().String("cursor", "", "Page cursor from a previous listing's next_cursor")
	command.Flags().Int("limit", 0, "Maximum number of branches to return (server default 20, max 100)")
}

func runPipelineBranches(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	includeStale, _ := command.Flags().GetBool("all")
	cursor, _ := command.Flags().GetString("cursor")
	limit, limitError := pipelinePageLimitFromFlags(command)
	if limitError != nil {
		return limitError
	}

	page, listError := apiClient.ListPipelineBranches(command.Context(), selector,
		client.ListPipelineBranchesOptions{
			Cursor: strings.TrimSpace(cursor),
			Limit:  limit,
		})
	if listError != nil {
		return listError
	}
	// The structured output is the platform's answer untouched, stale rows
	// included: a script that asked for JSON is filtering it itself, and
	// silently dropping rows from a machine-readable page would make --all a
	// flag it has to know about to be given the truth.
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, page)
	}

	shown, hidden := partitionPipelineBranches(page.Branches, includeStale)
	switch {
	case len(shown) == 0 && hidden > 0:
		_, _ = fmt.Fprintf(command.OutOrStdout(),
			"No branches with a run in the last 14 days. %s\n", stalePipelineBranchHint(hidden))
	case len(shown) == 0:
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No pipeline branches found.")
	default:
		renderPipelineBranchTable(command.OutOrStdout(), shown)
		if hidden > 0 {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "\n%s\n", stalePipelineBranchHint(hidden))
		}
	}
	// The next-page hint prints whatever the page showed: a page whose every
	// row was hidden as stale still has pages after it, and --all only
	// unhides this one.
	if page.NextCursor != nil {
		_, _ = fmt.Fprintf(command.ErrOrStderr(),
			"\nMore branches available: pass --cursor %s to see the next page.\n", *page.NextCursor)
	}
	return nil
}

// partitionPipelineBranches answers the rows to print and how many were held
// back for being stale. Nothing is hidden when the caller asked for
// everything, so the count is zero exactly when the table is complete.
func partitionPipelineBranches(branches []client.PipelineBranch,
	includeStale bool) ([]client.PipelineBranch, int) {
	if includeStale {
		return branches, 0
	}
	shown := make([]client.PipelineBranch, 0, len(branches))
	hidden := 0
	for _, branch := range branches {
		if branch.Stale {
			hidden++
			continue
		}
		shown = append(shown, branch)
	}
	return shown, hidden
}

func stalePipelineBranchHint(hidden int) string {
	noun := "stale branches"
	if hidden == 1 {
		noun = "stale branch"
	}
	return fmt.Sprintf("%d %s hidden, --all shows them", hidden, noun)
}

func renderPipelineBranchTable(out io.Writer, branches []client.PipelineBranch) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"BRANCH", "KIND", "LATEST RUN #", "STATUS", "OUTCOME",
		"PREVIOUS", "RUNS", "SUPERSEDED", "LAST ACTIVITY"})
	for _, branch := range branches {
		writer.AppendRow(table.Row{
			pipelineBranchLabel(branch),
			branch.Kind,
			branch.LatestRun.RunNumber,
			branch.LatestRun.Status,
			renderPipelineBranchLatestOutcome(branch.LatestRun),
			renderPipelineBranchOutcome(branch.PreviousOutcome),
			branch.RunCount,
			branch.SupersededCount,
			formatTimeAgo(branch.LastActivityAt),
		})
	}
	writer.Render()
}

// renderPipelineBranchOutcome prints an outcome with the glyph the rest of
// the pipeline surface uses, and "-" for the runs that have none: the STATUS
// column already says a run is queued or running, so repeating that here
// would cost the column its one job of saying how the work ended.
func renderPipelineBranchOutcome(outcome *string) string {
	if outcome == nil || *outcome == "" {
		return "-"
	}
	return renderPipelineState(pipelineRunStatusConcluded, outcome)
}

// renderPipelineBranchLatestOutcome is renderPipelineBranchOutcome for the
// latest run, which is a whole run rather than an outcome string: a run a
// newer run superseded reads "superseded" here exactly as it does in
// 'ankra pipeline list', so the two tables never disagree about one run.
func renderPipelineBranchLatestOutcome(run client.PipelineRun) string {
	if run.Outcome == nil || *run.Outcome == "" {
		return "-"
	}
	return renderPipelineRunState(run)
}

// pipelineBranchLabel is what the BRANCH column prints: the short name, with
// the default branch marked and a pull request's number spelled out, because
// "42" on its own in a branch column reads as a branch called 42.
func pipelineBranchLabel(branch client.PipelineBranch) string {
	label := branch.Name
	if label == "" {
		label = branch.Ref
	}
	if branch.Kind == client.PipelineBranchKindPullRequest && branch.PullRequestNumber != nil {
		label += " (#" + strconv.FormatInt(*branch.PullRequestNumber, 10) + ")"
	}
	if branch.IsDefault {
		label += " (default)"
	}
	return label
}
