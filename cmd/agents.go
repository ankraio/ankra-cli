package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Inspect and control AI agent runs",
	Long: `Inspect and control the organisation's AI agent runs.

Every time an AI agent is dispatched (a board ticket, a schedule, an
alert, or run-now) the platform records a run with a linked session.
These commands list those runs, show one in full, read its transcript,
and cancel a live run — the platform interrupts the in-flight turn
within seconds.`,
}

var agentsRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List the organisation's AI agent runs, newest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID, _ := cmd.Flags().GetString("task")
		statuses, _ := cmd.Flags().GetStringSlice("status")
		limit, _ := cmd.Flags().GetInt("limit")

		response, err := apiClient.ListAgentRuns(taskID, statuses, limit)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, response); handled || renderError != nil {
			return renderError
		}
		if len(response.Runs) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No agent runs found.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(cmd.OutOrStdout())
		writer.AppendHeader(table.Row{"RUN ID", "AGENT", "STATUS", "STARTED", "FINISHED", "OUTCOME"})
		for _, run := range response.Runs {
			finished := ""
			if run.FinishedAt != nil {
				finished = *run.FinishedAt
			}
			outcome := ""
			if run.OutcomeSummary != nil {
				outcome = truncateCell(*run.OutcomeSummary, 60)
			}
			writer.AppendRow(table.Row{run.ID, run.TaskName, run.Status, run.StartedAt, finished, outcome})
		}
		writer.Render()
		return nil
	},
}

var agentsRunCmd = &cobra.Command{
	Use:   "run <run_id>",
	Short: "Show one AI agent run in full",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		run, err := apiClient.GetAgentRun(args[0])
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, run); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Run:      %s\n", run.ID)
		_, _ = fmt.Fprintf(out, "Agent:    %s (%s)\n", run.TaskName, run.TaskID)
		_, _ = fmt.Fprintf(out, "Status:   %s\n", run.Status)
		_, _ = fmt.Fprintf(out, "Started:  %s\n", run.StartedAt)
		if run.FinishedAt != nil {
			_, _ = fmt.Fprintf(out, "Finished: %s\n", *run.FinishedAt)
		}
		if run.GoalStatus != nil && *run.GoalStatus != "" {
			_, _ = fmt.Fprintf(out, "Goal:     %s\n", *run.GoalStatus)
		}
		if run.AiSessionID != nil {
			_, _ = fmt.Fprintf(out, "Session:  %s\n", *run.AiSessionID)
		}
		if len(run.TriggerContext) > 0 {
			encoded, marshalError := json.Marshal(run.TriggerContext)
			if marshalError == nil {
				_, _ = fmt.Fprintf(out, "Trigger:  %s\n", encoded)
			}
		}
		if run.OutcomeSummary != nil && *run.OutcomeSummary != "" {
			_, _ = fmt.Fprintf(out, "Outcome:  %s\n", *run.OutcomeSummary)
		}
		_, _ = fmt.Fprintf(out, "Turns:    %d\n", run.TurnsUsed)
		return nil
	},
}

var agentsTranscriptCmd = &cobra.Command{
	Use:   "transcript <run_id>",
	Short: "Read an AI agent run's session transcript",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		since, _ := cmd.Flags().GetInt64("since")
		limit, _ := cmd.Flags().GetInt("limit")

		transcript, err := apiClient.GetAgentRunTranscript(args[0], since, limit)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, transcript); handled || renderError != nil {
			return renderError
		}
		if len(transcript.Events) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No transcript events (the run may not have produced output yet).")
			return nil
		}
		for _, event := range transcript.Events {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%5d  %-12s %s\n", event.SequenceNumber, event.EventType,
				transcriptEventSummary(event.Payload))
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nLast sequence: %d (pass --since %d for the next page)\n",
			transcript.LastSequenceNumber, transcript.LastSequenceNumber)
		return nil
	},
}

var agentsCancelCmd = &cobra.Command{
	Use:   "cancel <run_id>",
	Short: "Cancel a live AI agent run",
	Long: `Cancel a live AI agent run. The run and its session flip to
'cancelled' and the platform interrupts the in-flight turn within
seconds, without pausing the agent itself. Organisation admins only.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Cancel agent run %q? [y/N]: ", runID), yes); err != nil {
			return err
		}
		result, err := apiClient.CancelAgentRun(runID)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, result); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run cancelled. The platform interrupts the in-flight turn within seconds.")
		if result.CancelledSessionID != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cancelled session: %s\n", result.CancelledSessionID)
		}
		return nil
	},
}

// transcriptEventSummary renders a one-line human summary of a session
// event payload: its text when present, else compact JSON.
func transcriptEventSummary(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	for _, key := range []string{"text", "content", "message"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return truncateCell(strings.ReplaceAll(value, "\n", " "), 160)
		}
	}
	encoded, marshalError := json.Marshal(payload)
	if marshalError != nil {
		return ""
	}
	return truncateCell(string(encoded), 160)
}

// truncateCell caps a table/summary cell without breaking the layout.
func truncateCell(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength-1] + "…"
}

func init() {
	agentsRunsCmd.Flags().String("task", "", "Filter to one agent task id (UUID)")
	agentsRunsCmd.Flags().StringSlice("status", nil,
		"Filter by run status (pending, running, awaiting_user, completed, failed, cancelled, expired, skipped); repeatable")
	agentsRunsCmd.Flags().Int("limit", 20, "Maximum runs to return (max 100)")
	agentsTranscriptCmd.Flags().Int64("since", 0, "Return events with a sequence number greater than this")
	agentsTranscriptCmd.Flags().Int("limit", 200, "Maximum events to return (max 1000)")
	agentsCancelCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(agentsRunsCmd, agentsRunCmd, agentsTranscriptCmd, agentsCancelCmd)
	agentsCmd.AddCommand(agentsRunsCmd)
	agentsCmd.AddCommand(agentsRunCmd)
	agentsCmd.AddCommand(agentsTranscriptCmd)
	agentsCmd.AddCommand(agentsCancelCmd)
	rootCmd.AddCommand(agentsCmd)
}
