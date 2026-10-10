package cmd

// The Ankra merge train (cluster ankra-q573dh.11): a repository's queue of
// pull requests, each tested squashed onto the base branch and the pull
// requests ahead of it - exactly as its merge will land - and merged when that
// run passes, so the merge's own pipeline promotes the tested image instead of
// rebuilding it.

import (
	"fmt"
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
GitHub repositories only.

The train merges under the Ankra GitHub App's identity, so adding or removing
a pull request needs pipelines.operate and, on top of it, one of: you are an
organisation administrator, you opened the pull request, or you have write
access to the repository on GitHub. Ankra reads the last two through the
GitHub identity you linked with OAuth from the Ankra portal; without one only
an administrator can add or remove. You can always remove an entry you added
yourself. If GitHub cannot be asked, the request is refused and nothing
changes.`,
	}
	trainCommand.AddCommand(
		newPipelineTrainListCommand(),
		newPipelineTrainAddCommand(),
		newPipelineTrainRemoveCommand(),
		newPipelineTrainSettingsCommand("enable", "Switch the merge train on", true),
		newPipelineTrainSettingsCommand("disable", "Switch the merge train off and empty it", false),
	)
	return trainCommand
}

func newPipelineTrainListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show the merge train in order, and what recently left it",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
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
	return nil
}

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
			selector, selectorError := resolvePipelineSelector(command)
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
			selector, selectorError := resolvePipelineSelector(command)
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

func newPipelineTrainSettingsCommand(use string, short string, isEnabled bool) *cobra.Command {
	settingsCommand := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			request := client.SetPipelineMergeTrainSettingsRequest{Enabled: isEnabled}
			if command.Flags().Lookup("max-cars") != nil && command.Flags().Changed("max-cars") {
				maxCars, _ := command.Flags().GetInt("max-cars")
				if maxCars < 1 || maxCars > 8 {
					return withExitCode(exitUsage, fmt.Errorf("--max-cars must be between 1 and 8"))
				}
				request.MaxCars = &maxCars
			}
			settings, setError := apiClient.SetPipelineMergeTrainSettings(command.Context(), selector, request)
			if setError != nil {
				return setError
			}
			if isStructured, renderError := renderStructured(command, settings); isStructured || renderError != nil {
				return renderError
			}
			if settings.Enabled {
				_, _ = fmt.Fprintf(command.OutOrStdout(), "The merge train is on, testing %d car(s) at once.\n",
					settings.MaxCars)
				return nil
			}
			_, _ = fmt.Fprintln(command.OutOrStdout(),
				"The merge train is off; the pull requests in it leave on its next pass.")
			return nil
		},
	}
	registerPipelineSelectorFlags(settingsCommand)
	if isEnabled {
		settingsCommand.Flags().Int("max-cars", 1,
			"How many pull requests are tested at once (1-8), each stacked on the ones ahead of it")
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
