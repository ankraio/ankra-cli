package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const (
	securityHistoryDefaultDays     = 90
	securityReportMaxRecipients    = 20
	securityReportFrequencyWeekly  = "weekly"
	securityReportFrequencyMonthly = "monthly"
)

var securityHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "One cluster's daily security snapshots: how the posture moved over a window",
	Long: `List a cluster's daily security snapshots for the last --days days (default
90, the platform clamps to 1..365): findings by severity, what is actionable
once dispositions are applied, the workloads and namespaces covered and the
risk score, oldest first, with a one-line trend between the first and last
snapshot. A day the scanner recorded no snapshot for is simply absent - it
never reads as zero findings.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		days, _ := cmd.Flags().GetInt("days")
		if days < 1 {
			return withExitCode(exitUsage, fmt.Errorf("--days must be at least 1, got %d", days))
		}
		history, err := apiClient.GetSecurityHistory(clusterID, days)
		if err != nil {
			return fmt.Errorf("reading security history: %w", err)
		}
		if rendered, err := renderStructured(cmd, history); rendered || err != nil {
			return err
		}
		renderSecurityHistory(cmd.OutOrStdout(), history)
		return nil
	},
}

func renderSecurityHistory(out io.Writer, history *client.SecurityHistory) {
	if len(history.Items) == 0 {
		_, _ = fmt.Fprintf(out, "No security snapshots in the last %d days - the scanner has not recorded a daily posture yet, so the trend is unknown, not clean.\n", history.Days)
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Date", "Findings", "Critical", "High", "Medium", "Low", "Actionable", "Fixable severe", "Workloads", "Namespaces", "Risk"})
	for _, point := range history.Items {
		writer.AppendRow(table.Row{
			point.Date,
			point.Findings,
			point.Critical,
			point.High,
			point.Medium,
			point.Low,
			point.ActionableFindings,
			point.FixableCritical + point.FixableHigh,
			point.Workloads,
			point.Namespaces,
			fmt.Sprintf("%.1f", point.ActionableRiskScore),
		})
	}
	writer.Render()
	first := history.Items[0]
	last := history.Items[len(history.Items)-1]
	_, _ = fmt.Fprintf(out, "%d of %d days have a snapshot · %s\n", len(history.Items), history.Days, securityHistoryTrendText(first, last))
}

// securityHistoryTrendText compares the first and last snapshot of the
// window; one snapshot is a point, not a trend.
func securityHistoryTrendText(first client.SecurityHistoryPoint, last client.SecurityHistoryPoint) string {
	if first.Date == last.Date {
		return fmt.Sprintf("one snapshot (%s): %d actionable findings, risk %.1f", last.Date, last.ActionableFindings, last.ActionableRiskScore)
	}
	delta := last.ActionableFindings - first.ActionableFindings
	direction := "unchanged"
	switch {
	case delta > 0:
		direction = text.FgRed.Sprintf("up %d", delta)
	case delta < 0:
		direction = text.FgGreen.Sprintf("down %d", -delta)
	}
	return fmt.Sprintf("actionable findings %s from %s (%d) to %s (%d) · risk %.1f -> %.1f",
		direction, first.Date, first.ActionableFindings, last.Date, last.ActionableFindings, first.ActionableRiskScore, last.ActionableRiskScore)
}

var securityReportScheduleCmd = &cobra.Command{
	Use:   "report-schedule",
	Short: "The recurring security report a cluster emails: frequency, recipients and when it last went out",
	Long: `Show one cluster's scheduled security report - weekly or monthly, who
receives it, whether it is enabled, and when it was last sent and is next
due. A cluster without a schedule reads "not configured". Use
"report-schedule set" to create or change it and "report-schedule send" to
queue one now.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		status, err := apiClient.GetSecurityReportSchedule(clusterID)
		if err != nil {
			return fmt.Errorf("reading the security report schedule: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		renderSecurityReportSchedule(cmd.OutOrStdout(), status)
		return nil
	},
}

func renderSecurityReportSchedule(out io.Writer, status *client.SecurityReportScheduleStatus) {
	if status == nil || !status.Configured || status.Schedule == nil {
		_, _ = fmt.Fprintln(out, "Security report schedule: not configured - ankra security report-schedule set --cluster <c> --frequency weekly --recipient <email> creates one.")
		return
	}
	schedule := status.Schedule
	state := text.FgGreen.Sprint("enabled")
	if !schedule.Enabled {
		state = text.FgYellow.Sprint("paused")
	}
	_, _ = fmt.Fprintf(out, "Security report schedule: %s, %s\n", schedule.Frequency, state)
	if len(schedule.Recipients) == 0 {
		_, _ = fmt.Fprintln(out, "  recipients: none")
	} else {
		_, _ = fmt.Fprintf(out, "  recipients: %s\n", strings.Join(schedule.Recipients, ", "))
	}
	_, _ = fmt.Fprintf(out, "  last sent:  %s\n", securityReportTimeText(schedule.LastSentAt, "never"))
	_, _ = fmt.Fprintf(out, "  next due:   %s\n", securityReportTimeText(schedule.NextDueAt, "not scheduled"))
	if schedule.UpdatedAt != "" {
		_, _ = fmt.Fprintf(out, "  updated:    %s\n", formatTimeAgo(schedule.UpdatedAt))
	}
}

func securityReportTimeText(value *string, absent string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return absent
	}
	return fmt.Sprintf("%s (%s)", *value, formatTimeAgo(*value))
}

var securityReportScheduleSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Create or replace a cluster's recurring security report",
	Long: `Create or replace the cluster's scheduled security report. --frequency is
weekly or monthly; --recipient is repeatable, at most 20 addresses, and at
least one is required while the schedule is enabled. --disabled keeps the
schedule but pauses sending. Asks for confirmation unless --yes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		frequencyFlag, _ := cmd.Flags().GetString("frequency")
		frequency := strings.ToLower(strings.TrimSpace(frequencyFlag))
		if frequency != securityReportFrequencyWeekly && frequency != securityReportFrequencyMonthly {
			return withExitCode(exitUsage, fmt.Errorf("--frequency must be weekly or monthly, got %q", frequencyFlag))
		}
		recipientFlags, _ := cmd.Flags().GetStringSlice("recipient")
		recipients, err := securityReportRecipients(recipientFlags)
		if err != nil {
			return err
		}
		disabled, _ := cmd.Flags().GetBool("disabled")
		enabled := !disabled
		if enabled && len(recipients) == 0 {
			return withExitCode(exitUsage, errors.New("at least one --recipient is required while the schedule is enabled; pass --disabled to store a paused schedule"))
		}
		yes, _ := cmd.Flags().GetBool("yes")
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		state := "enabled"
		if !enabled {
			state = "paused"
		}
		prompt := fmt.Sprintf("Store a %s security report schedule (%s) for %d recipient(s)? [y/N]: ", frequency, state, len(recipients))
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, prompt, yes); confirmError != nil {
			return confirmError
		}
		status, err := apiClient.SetSecurityReportSchedule(clusterID, client.SecurityReportScheduleRequest{
			Frequency:  frequency,
			Recipients: recipients,
			Enabled:    enabled,
		})
		if err != nil {
			return fmt.Errorf("setting the security report schedule: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		renderSecurityReportSchedule(cmd.OutOrStdout(), status)
		return nil
	},
}

// securityReportRecipients trims, drops blanks and duplicates, and enforces
// the platform's cap so the request fails here, not after the prompt.
func securityReportRecipients(values []string) ([]string, error) {
	recipients := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		address := strings.TrimSpace(value)
		if address == "" {
			continue
		}
		if !strings.Contains(address, "@") {
			return nil, withExitCode(exitUsage, fmt.Errorf("--recipient %q is not an email address", address))
		}
		key := strings.ToLower(address)
		if seen[key] {
			continue
		}
		seen[key] = true
		recipients = append(recipients, address)
	}
	if len(recipients) > securityReportMaxRecipients {
		return nil, withExitCode(exitUsage, fmt.Errorf("at most %d recipients are allowed, got %d", securityReportMaxRecipients, len(recipients)))
	}
	return recipients, nil
}

var securityReportScheduleSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Queue one security report for a cluster now, outside its schedule",
	Long: `Queue the cluster's security report for immediate delivery to the
schedule's recipients. Asks for confirmation unless --yes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, "Send the security report to the schedule's recipients now? [y/N]: ", yes); confirmError != nil {
			return confirmError
		}
		result, err := apiClient.SendSecurityReportNow(clusterID)
		if err != nil {
			return fmt.Errorf("sending the security report: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}
		if result.Queued {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Security report queued: %s\n", result.Message)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Security report not queued: %s\n", result.Message)
		}
		return nil
	},
}

func init() {
	securityCmd.AddCommand(securityHistoryCmd, securityReportScheduleCmd)
	securityReportScheduleCmd.AddCommand(securityReportScheduleSetCmd, securityReportScheduleSendCmd)

	for _, command := range []*cobra.Command{securityHistoryCmd, securityReportScheduleCmd, securityReportScheduleSetCmd, securityReportScheduleSendCmd} {
		command.Flags().String("cluster", "", "Cluster (name or id), required")
	}
	securityHistoryCmd.Flags().Int("days", securityHistoryDefaultDays, "Window in days (1..365)")
	securityReportScheduleSetCmd.Flags().String("frequency", "", "weekly or monthly, required")
	securityReportScheduleSetCmd.Flags().StringSlice("recipient", nil, "Recipient email address (repeatable, at most 20)")
	securityReportScheduleSetCmd.Flags().Bool("disabled", false, "Store the schedule paused instead of enabled")
	securityReportScheduleSetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	securityReportScheduleSendCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(securityHistoryCmd, securityReportScheduleCmd, securityReportScheduleSetCmd, securityReportScheduleSendCmd)
}
