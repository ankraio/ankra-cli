package cmd

// `ankra application timeline` and `ankra application launch-plan`
// (ankra-hq41xb.22): two reads the portal's application page shows and the
// CLI did not.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// applicationTimelineMaximumLimit is the largest page the platform serves.
const applicationTimelineMaximumLimit = 100

type applicationTimelineEvent struct {
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	OccurredAt  string  `json:"occurred_at"`
	ClusterName *string `json:"cluster_name"`
	Namespace   *string `json:"namespace"`
	CommitSHA   *string `json:"commit_sha"`
	Message     *string `json:"message"`
	ImageTag    *string `json:"image_tag"`
	ActorName   *string `json:"actor_name"`
	Actor       *string `json:"actor"`
}

type applicationTimeline struct {
	Events             []applicationTimelineEvent `json:"events"`
	IsCapped           bool                       `json:"capped"`
	UnavailableSources []string                   `json:"unavailable_sources"`
	NextBefore         *string                    `json:"next_before"`
}

func newApplicationTimelineCommand() *cobra.Command {
	timelineCommand := &cobra.Command{
		Use:   "timeline <application>",
		Short: "Show what happened to an application, newest first",
		Long: `Show an application's timeline, newest first: builds, deploys, promotions,
rollbacks, demos, AI reviews, what Ankra did on its own and lifecycle events.

A source the platform could not read is named on stderr: it is unknown, not a
source with no events. When older events remain, the command prints the
--before value that reads the next page.`,
		Example: `  ankra application timeline shop
  ankra application timeline shop --limit 20
  ankra application timeline shop --before <cursor> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			query := url.Values{}
			if command.Flags().Changed("limit") {
				limit, _ := command.Flags().GetInt("limit")
				if limit < 1 || limit > applicationTimelineMaximumLimit {
					return withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", applicationTimelineMaximumLimit))
				}
				query.Set("limit", strconv.Itoa(limit))
			}
			if before := mustFlagString(command, "before"); before != "" {
				query.Set("before", before)
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, readError := apiClient.GetApplicationTimeline(command.Context(), applicationID, query)
			if readError != nil {
				return readError
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, json.RawMessage(payload))
				return renderError
			}
			var timeline applicationTimeline
			if decodeError := json.Unmarshal(payload, &timeline); decodeError != nil {
				return fmt.Errorf("parsing the timeline: %w", decodeError)
			}
			printApplicationTimeline(command.OutOrStdout(), timeline)
			notes := command.ErrOrStderr()
			if len(timeline.UnavailableSources) > 0 {
				_, _ = fmt.Fprintf(notes, "Could not read: %s. Their events are unknown, not absent.\n",
					strings.Join(timeline.UnavailableSources, ", "))
			}
			if next := applicationTimelineText(timeline.NextBefore); next != "" {
				_, _ = fmt.Fprintf(notes, "Older events remain: re-run with --before %s\n", next)
			}
			return nil
		},
	}
	timelineCommand.Flags().Int("limit", 0, "Events to return, 1 to 100 (default: the platform's page size, 60)")
	timelineCommand.Flags().String("before", "", "Read only the events older than this cursor, printed by a previous page")
	registerStructuredOutputFlags(timelineCommand)
	return timelineCommand
}

// applicationTimelineText is an optional member of the timeline as text:
// empty when the platform sent null.
func applicationTimelineText(value *string) string {
	return strings.TrimSpace(pipelineStringValue(value))
}

// applicationTimelineActor is who an event is attributed to: the name the
// platform resolved, or the raw actor when it resolved none.
func applicationTimelineActor(event applicationTimelineEvent) string {
	if name := applicationTimelineText(event.ActorName); name != "" {
		return name
	}
	return applicationTimelineText(event.Actor)
}

func printApplicationTimeline(out io.Writer, timeline applicationTimeline) {
	if len(timeline.Events) == 0 {
		_, _ = fmt.Fprintln(out, "No events on this application's timeline.")
		return
	}
	eventTable := table.NewWriter()
	eventTable.SetOutputMirror(out)
	eventTable.SetStyle(table.StyleRounded)
	eventTable.AppendHeader(table.Row{"WHEN", "KIND", "STATUS", "WHERE", "BUILD", "BY", "DETAIL"})
	for _, event := range timeline.Events {
		where := applicationTimelineText(event.ClusterName)
		if namespace := applicationTimelineText(event.Namespace); namespace != "" && where != "" {
			where += "/" + namespace
		}
		build := applicationTimelineText(event.ImageTag)
		if build == "" {
			build = pipelineShortSHA(applicationTimelineText(event.CommitSHA))
		}
		eventTable.AppendRow(table.Row{event.OccurredAt, event.Kind, event.Status, where, build,
			applicationTimelineActor(event),
			truncateForDisplay(pipelineTestText(applicationTimelineText(event.Message)), 60)})
	}
	eventTable.Render()
}

func newApplicationLaunchPlanCommand() *cobra.Command {
	launchPlanCommand := &cobra.Command{
		Use:   "launch-plan <application>",
		Short: "Show an application's Launch Plan",
		Long: `Show the Launch Plan Ankra resolved for an application from an analysed
commit: for each external service its lock-in tier, the default choice and the
alternatives; where each environment variable's value comes from; the
migration step, port and exposure; and the decisions only a person can make.

The plan is resolved for the latest analysed commit unless --commit names one,
and for the target Ankra picks unless --cluster names one. --backup-vault and
--data-migration-choice select the data move the plan describes; without them
the plan keeps every vendor and moves nothing. The command only reads: the
runs themselves are 'ankra application data-migration'.

It needs the applications.read permission. The plan is printed as JSON, or
YAML with -o yaml.`,
		Example: `  ankra application launch-plan shop
  ankra application launch-plan shop --cluster production -o yaml
  ankra application launch-plan shop --backup-vault <vault-id> \
    --data-migration-choice postgres:ankra-open-source`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			query := url.Values{}
			if commit := mustFlagString(command, "commit"); commit != "" {
				query.Set("commit_sha", commit)
			}
			if vault := mustFlagString(command, "backup-vault"); vault != "" {
				query.Set("backup_vault_id", vault)
			}
			choices, _ := command.Flags().GetStringArray("data-migration-choice")
			for _, choice := range choices {
				if dependency, selection, isPair := strings.Cut(choice, ":"); !isPair ||
					strings.TrimSpace(dependency) == "" || strings.TrimSpace(selection) == "" {
					return withExitCode(exitUsage, errors.New(
						"--data-migration-choice takes <dependency>:<choice>, for example postgres:ankra-open-source"))
				}
			}
			if len(choices) > 0 {
				query.Set("data_migration_choices", strings.Join(choices, ","))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			if clusterReference := mustFlagString(command, "cluster"); clusterReference != "" {
				clusterID, clusterError := resolveClusterArg(clusterReference)
				if clusterError != nil {
					return clusterError
				}
				query.Set("cluster_id", clusterID)
			}
			payload, readError := apiClient.GetApplicationLaunchPlan(command.Context(), applicationID, query)
			if readError != nil {
				return readError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	launchPlanCommand.Flags().String("commit", "", "Analysed commit whose plan to resolve (default: the most recently analysed one)")
	launchPlanCommand.Flags().String("cluster", "", "Target cluster to resolve the plan for, by name or id (default: Ankra picks the target)")
	launchPlanCommand.Flags().String("backup-vault", "", "Backup vault id the data migration moves dumps and exports through")
	launchPlanCommand.Flags().StringArray("data-migration-choice", nil,
		"Dependency to move off its vendor, as <dependency>:<choice> (ankra-open-source or needs-portability-pr); repeatable")
	registerStructuredOutputFlags(launchPlanCommand)
	return launchPlanCommand
}
