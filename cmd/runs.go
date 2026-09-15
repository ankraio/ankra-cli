package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// runsCmd is the organisation's run history: the pipeline, promotion, backup,
// restore and clone work the platform has been asked to do.
var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Inspect and act on the organisation's runs",
	Long: "Read the organisation's run history and act on the runs that move data. " +
		"A backup, restore or clone run also carries the plan it was given and every " +
		"attempt of every step, which is what explains a restore that took an hour.",
}

// dataRunPollInterval paces the --wait poll of a run the CLI is following.
var dataRunPollInterval = 5 * time.Second

// runKinds are the kinds `--kind` accepts. The platform answers 422 for
// anything outside the set, so refusing a typo here saves a round-trip and
// names the alternatives.
var runKinds = []string{
	client.RunKindBackup, client.RunKindRestore, client.RunKindClone,
	client.RunKindPipeline, client.RunKindPromotion,
	client.RunKindDisasterRecoveryDrill, client.RunKindEnvironmentHydrate,
}

func validateRunKind(kind string) error {
	if kind == "" {
		return nil
	}
	for _, known := range runKinds {
		if kind == known {
			return nil
		}
	}
	return withExitCode(exitUsage, fmt.Errorf("unknown run kind %q - expected one of %s",
		kind, strings.Join(runKinds, ", ")))
}

// runDisplayName names a run in a table. A data run's display name is often
// empty, and the stack it moved is what the reader is looking for.
func runDisplayName(run client.Run) string {
	if run.DisplayName != "" {
		return run.DisplayName
	}
	if run.DataRun != nil {
		switch {
		case run.DataRun.SourceStackName != "" && run.DataRun.TargetStackName != "" &&
			run.DataRun.SourceStackName != run.DataRun.TargetStackName:
			return run.DataRun.SourceStackName + " -> " + run.DataRun.TargetStackName
		case run.DataRun.SourceStackName != "":
			return run.DataRun.SourceStackName
		case run.DataRun.TargetStackName != "":
			return run.DataRun.TargetStackName
		}
	}
	return "-"
}

func printRunTable(out io.Writer, runs []client.Run) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Kind", "Status", "Name", "Started", "Finished"})
	for _, run := range runs {
		writer.AppendRow(table.Row{
			run.ID,
			run.Kind,
			run.Status,
			runDisplayName(run),
			formatOptionalTimestamp(run.StartedAt),
			formatOptionalTimestamp(run.FinishedAt),
		})
	}
	writer.Render()
}

// formatOptionalTimestamp renders a nullable RFC3339 string as a relative
// time, with "-" for absent values.
func formatOptionalTimestamp(timestamp *string) string {
	if timestamp == nil || *timestamp == "" {
		return "-"
	}
	return formatTimeAgo(*timestamp)
}

func optionalText(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func printRunDetail(out io.Writer, run *client.Run) {
	_, _ = fmt.Fprintln(out, "Run:")
	_, _ = fmt.Fprintf(out, "  ID:       %s\n", run.ID)
	_, _ = fmt.Fprintf(out, "  Kind:     %s\n", run.Kind)
	_, _ = fmt.Fprintf(out, "  Status:   %s\n", run.Status)
	_, _ = fmt.Fprintf(out, "  Name:     %s\n", runDisplayName(*run))
	_, _ = fmt.Fprintf(out, "  Started:  %s\n", formatOptionalTimestamp(run.StartedAt))
	_, _ = fmt.Fprintf(out, "  Finished: %s\n", formatOptionalTimestamp(run.FinishedAt))
	if run.ErrorExcerpt != nil && *run.ErrorExcerpt != "" {
		_, _ = fmt.Fprintf(out, "  Error:    %s\n", *run.ErrorExcerpt)
	}
	if run.DataRun == nil {
		return
	}
	dataRun := run.DataRun
	_, _ = fmt.Fprintln(out, "\nData movement:")
	_, _ = fmt.Fprintf(out, "  Mode:          %s\n", dataRun.Mode)
	_, _ = fmt.Fprintf(out, "  Phase:         %s\n", dataRun.Phase)
	if len(dataRun.Plan) > 0 {
		_, _ = fmt.Fprintf(out, "  Plan:          %s\n", strings.Join(dataRun.Plan, " -> "))
	}
	_, _ = fmt.Fprintf(out, "  Vault:         %s\n", dataRun.BackupVaultID)
	_, _ = fmt.Fprintf(out, "  Restore point: %s\n", optionalText(dataRun.RestorePointID))
	if dataRun.SourceStackName != "" {
		_, _ = fmt.Fprintf(out, "  Source stack:  %s\n", dataRun.SourceStackName)
	}
	if dataRun.TargetStackName != "" {
		_, _ = fmt.Fprintf(out, "  Target stack:  %s\n", dataRun.TargetStackName)
	}
	if dataRun.BlockedReason != nil && *dataRun.BlockedReason != "" {
		_, _ = fmt.Fprintf(out, "  Blocked on:    %s\n", *dataRun.BlockedReason)
	}
	printNotCarried(out, dataRun.AssetPlan.NotCarried)
	if len(dataRun.Steps) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nSteps:")
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"#", "Step", "Attempt", "Status", "Job", "Error"})
	for _, step := range dataRun.Steps {
		writer.AppendRow(table.Row{
			step.Position, step.StepKey, step.Attempt, step.Status, step.JobName,
			optionalText(step.ErrorExcerpt),
		})
	}
	writer.Render()
}

var runsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's runs",
	Long: `List the organisation's runs, newest first.

Naming a cluster or a stack asks the backup lane's own listing, which carries
each run's data-movement payload; without one, the listing covers every kind
of run the organisation has.

Examples:
  ankra runs list --kind backup
  ankra runs list --cluster production --status failed
  ankra runs list --kind restore --stack shop`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, _ := cmd.Flags().GetString("kind")
		if kindError := validateRunKind(kind); kindError != nil {
			return kindError
		}
		status, _ := cmd.Flags().GetString("status")
		clusterReference, _ := cmd.Flags().GetString("cluster")
		stackName, _ := cmd.Flags().GetString("stack")
		cursor, _ := cmd.Flags().GetString("cursor")
		limit, _ := cmd.Flags().GetInt("limit")

		options := client.ListRunsOptions{
			Kind: kind, Status: status, StackName: stackName, Cursor: cursor, Limit: limit,
		}
		if clusterReference != "" {
			clusterID, resolveError := resolveClusterID(clusterReference)
			if resolveError != nil {
				return resolveError
			}
			options.ClusterID = clusterID
		}

		listing, listError := apiClient.ListRuns(options)
		if listError != nil {
			return backupLaneError("listing runs", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		if len(listing.Runs) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No runs found.")
			return nil
		}
		printRunTable(cmd.OutOrStdout(), listing.Runs)
		printNextCursor(cmd.ErrOrStderr(), listing.NextCursor, "ankra runs list")
		return nil
	},
}

var runsGetCmd = &cobra.Command{
	Use:   "get <run-id>",
	Short: "Show a run with its steps",
	Long: "Describe one run. A backup, restore or clone also shows the plan it was " +
		"given and every attempt of every step, with the status, the agent job and " +
		"the failure excerpt of each.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		run, getError := apiClient.GetRun(args[0])
		if getError != nil {
			return backupLaneError("getting run", getError)
		}
		if rendered, renderError := renderStructured(cmd, run); rendered || renderError != nil {
			return renderError
		}
		printRunDetail(cmd.OutOrStdout(), run)
		return nil
	},
}

var runsCancelCmd = &cobra.Command{
	Use:   "cancel <run-id>",
	Short: "Cancel a run that has not finished",
	Long: "Stop a run that is still going. A run that has already finished is " +
		"refused rather than reported as cancelled.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Cancel run %q? [y/N]: ", args[0]), yes); confirmError != nil {
			return confirmError
		}
		run, cancelError := apiClient.CancelRun(args[0])
		if cancelError != nil {
			return backupLaneError("cancelling run", cancelError)
		}
		if rendered, renderError := renderStructured(cmd, run); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Run %s is %s.\n", run.ID, run.Status)
		return nil
	},
}

var runsRetryCmd = &cobra.Command{
	Use:   "retry <run-id>",
	Short: "Retry a failed backup, restore or clone from the step that failed",
	Long: `Open a fresh run that resumes a failed backup, restore or clone from the
step that failed.

The answer is the new run, not the old one: the failed row will never move
again, so anything watching it would wait forever.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		run, retryError := apiClient.RetryRun(args[0])
		if retryError != nil {
			return backupLaneError("retrying run", retryError)
		}
		if rendered, renderError := renderStructured(cmd, run); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Run %s opened, resuming %s from where it failed.\n", run.ID, args[0])
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Watch it with 'ankra runs get %s'.\n", run.ID)
		return nil
	},
}

// followRunToCompletion polls a run until it reaches a terminal status or the
// context expires. Progress goes to progressWriter - stderr for every caller -
// so a command's structured output stays parseable while it waits.
func followRunToCompletion(ctx context.Context, runs APIClient, runID string,
	progressWriter io.Writer) (*client.Run, error) {
	lastReported := ""
	for {
		run, getError := runs.GetRun(runID)
		if getError != nil {
			return nil, getError
		}
		state := run.Status
		if run.DataRun != nil && run.DataRun.Phase != "" {
			state = run.Status + " (" + run.DataRun.Phase + ")"
		}
		if state != lastReported {
			_, _ = fmt.Fprintf(progressWriter, "Run %s: %s\n", runID, state)
			lastReported = state
		}
		if client.IsTerminalRunStatus(run.Status) {
			return run, nil
		}
		timer := time.NewTimer(dataRunPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return run, ctx.Err()
		case <-timer.C:
		}
	}
}

// runOutcomeError turns a run that concluded badly into a non-zero exit
// carrying the platform's own reason, so a script that waited sees the
// failure rather than a green submit.
func runOutcomeError(operationLabel string, run *client.Run) error {
	if run == nil || run.Status == client.RunStatusSucceeded {
		return nil
	}
	message := fmt.Sprintf("%s: run %s %s", operationLabel, run.ID, run.Status)
	if run.ErrorExcerpt != nil && *run.ErrorExcerpt != "" {
		message += ": " + *run.ErrorExcerpt
	}
	return fmt.Errorf("%s - inspect it with 'ankra runs get %s'", message, run.ID)
}

// printNextCursor points at the next keyset page on stderr, so a structured
// caller never has to parse it out of stdout.
func printNextCursor(out io.Writer, nextCursor *string, commandPrefix string) {
	if nextCursor == nil || *nextCursor == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "\nMore results: %s --cursor %s\n", commandPrefix, *nextCursor)
}

func init() {
	runsListCmd.Flags().String("kind", "", "Only runs of this kind: "+strings.Join(runKinds, ", "))
	runsListCmd.Flags().String("status", "", "Only runs in this status (pending, running, blocked, succeeded, failed, cancelled)")
	runsListCmd.Flags().String("cluster", "", "Only runs touching this cluster (name or id); asks the backup lane's listing")
	runsListCmd.Flags().String("stack", "", "Only runs touching this stack; asks the backup lane's listing")
	runsListCmd.Flags().String("cursor", "", "Continue from a previous page's cursor")
	runsListCmd.Flags().Int("limit", 0, "Page size (server default 50, maximum 100)")

	runsCancelCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(runsListCmd, runsGetCmd, runsCancelCmd, runsRetryCmd)

	runsCmd.AddCommand(runsListCmd)
	runsCmd.AddCommand(runsGetCmd)
	runsCmd.AddCommand(runsCancelCmd)
	runsCmd.AddCommand(runsRetryCmd)

	rootCmd.AddCommand(runsCmd)
}
