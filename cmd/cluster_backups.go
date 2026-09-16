package cmd

// `ankra cluster backups status` (epic ankra-0xsdd, bead ankra-0xsdd.46): the
// terminal twin of the portal's cluster Backups tab.
//
// It is the one backup question `restore-points list` cannot answer. That
// listing is per stack and only about restore points, so finding the stack
// nobody protected means knowing to ask about it first. This reads the whole
// cluster at once: the verdict per stack, what produces it, what it can be
// restored from, and whether the data plane that takes the captures is even
// installed.

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterBackupsCmd = &cobra.Command{
	Use:   "backups",
	Short: "See what a cluster is backing up",
	Long: "Read the cluster's backup posture: which stacks are protected, which " +
		"hold data and are not, what each one can be restored from, and whether " +
		"the backup components are installed on the cluster.",
}

var clusterBackupsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show every stack on the cluster against what protects it",
	Long: `Show what this cluster is backing up, one row per stack.

The verdict has three values, not two: "unknown" is a stack Ankra could not
assess - its inventory has not landed, or its backup settings could not be
read - and it is deliberately not reported as unprotected.

Examples:
  ankra cluster backups status
  ankra cluster backups status --cluster production
  ankra cluster backups status --protection unprotected
  ankra cluster backups status --limit 10 --output json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		options, optionsError := clusterBackupsOptions(cmd)
		if optionsError != nil {
			return optionsError
		}
		page, readError := apiClient.GetClusterBackups(cluster.ID, options)
		if readError != nil {
			return backupLaneError("reading the cluster's backups", readError)
		}
		if rendered, renderError := renderStructured(cmd, page); rendered || renderError != nil {
			return renderError
		}
		printClusterBackups(cmd.OutOrStdout(), cluster.Name, page)
		printNextCursor(cmd.ErrOrStderr(), page.NextCursor, "ankra cluster backups status")
		return nil
	},
}

// clusterBackupsOptions reads the flags, refusing a verdict that does not
// exist rather than sending it and answering an empty page for a typo.
func clusterBackupsOptions(cmd *cobra.Command) (client.ClusterBackupsOptions, error) {
	options := client.ClusterBackupsOptions{}
	states, _ := cmd.Flags().GetStringArray("protection")
	for _, state := range states {
		normalised := strings.ToLower(strings.TrimSpace(state))
		if !client.IsProtectionState(normalised) {
			return options, withExitCode(exitUsage, fmt.Errorf(
				"unknown protection state %q - use one of: %s",
				state, strings.Join(client.ProtectionStates, ", ")))
		}
		options.ProtectionStates = append(options.ProtectionStates, normalised)
	}
	options.Cursor, _ = cmd.Flags().GetString("cursor")
	// A limit outside the bound the help promises is refused here, not sent.
	// Passing it through would relay a server validation error for a rule
	// this command already states, and a negative one would be dropped
	// silently - a page size nobody gets and nobody is told about.
	limit, _ := cmd.Flags().GetInt("limit")
	if cmd.Flags().Changed("limit") && (limit < 1 || limit > client.MaximumClusterBackupsPageSize) {
		return options, withExitCode(exitUsage, fmt.Errorf(
			"--limit must be between 1 and %d", client.MaximumClusterBackupsPageSize))
	}
	options.Limit = limit
	return options, nil
}

// describeBackupStackState is what the header says about the data plane. A
// cluster whose every stack names a ready vault and which has no backup stack
// is not a protected cluster, so the absent and degraded cases say what to do
// rather than printing a bare word.
func describeBackupStackState(state string) string {
	switch state {
	case client.ClusterBackupStackReady:
		return "ready"
	case client.ClusterBackupStackInstalling:
		return "installing - a backup started now waits for this to finish"
	case client.ClusterBackupStackDegraded:
		return "not running - nothing is being captured; check the ankra-backup stack"
	case client.ClusterBackupStackAbsent:
		return "not installed - Ankra installs it once a stack here is protected and a vault is ready"
	default:
		// A state this build does not know is said to be unrecognised rather
		// than printed bare: a lone word reads as a verdict this command
		// stands behind, and "ready" is the one it must never imply.
		return fmt.Sprintf("%q - this version of the CLI does not recognise that state", state)
	}
}

// describeDataAssets keeps an unread inventory out of the "0 assets" column:
// a stack Ankra cannot see into is not a stack holding nothing.
func describeDataAssets(row client.StackBackupsRow) string {
	if !row.DataAssetCountKnown {
		return "not reported"
	}
	return fmt.Sprintf("%d (%d db)", row.DataAssetCount, row.DatabaseAssetCount)
}

func describeProtection(row client.StackBackupsRow) string {
	switch row.ProtectionState {
	case client.ProtectionStateProtected:
		if row.FailingRuns24h > 0 {
			return fmt.Sprintf("failing (%d in 24h)", row.FailingRuns24h)
		}
		return client.ProtectionStateProtected
	case client.ProtectionStateUnprotected:
		if row.UnprotectedReason != "" {
			return fmt.Sprintf("unprotected (%s)", row.UnprotectedReason)
		}
		return client.ProtectionStateUnprotected
	case client.ProtectionStateUnknown:
		if row.UnknownReason != "" {
			return fmt.Sprintf("unknown (%s)", row.UnknownReason)
		}
		return client.ProtectionStateUnknown
	default:
		return row.ProtectionState
	}
}

func describeLastRestorePoint(row client.StackBackupsRow) string {
	if row.LastRestorePoint == nil {
		return "none"
	}
	return fmt.Sprintf("%s %s, %s", row.LastRestorePoint.Status,
		describeRestorePointSize(row.LastRestorePoint.SizeBytes, row.LastRestorePoint.SizeBytesKnown),
		formatTimeAgo(row.LastRestorePoint.CreatedAt))
}

func describeLatestRun(row client.StackBackupsRow) string {
	if row.LatestRun == nil {
		return "none"
	}
	return fmt.Sprintf("%s %s, %s", row.LatestRun.Kind, row.LatestRun.Status,
		formatTimeAgo(row.LatestRun.CreatedAt))
}

func describeNextRun(row client.StackBackupsRow) string {
	if row.NextScheduledAt == nil {
		return "not scheduled"
	}
	return formatTimeAgo(*row.NextScheduledAt)
}

func printClusterBackups(out io.Writer, clusterName string, page *client.ClusterBackupsPage) {
	rollup := page.Rollup
	_, _ = fmt.Fprintf(out, "Cluster %s: %d of %d stacks with data are backed up",
		clusterName, rollup.ProtectedStacks, rollup.StatefulStacks)
	if rollup.UnknownStacks > 0 {
		_, _ = fmt.Fprintf(out, ", %d could not be assessed", rollup.UnknownStacks)
	}
	if rollup.FailingSchedules > 0 {
		_, _ = fmt.Fprintf(out, ", %d had a backup fail in the last 24 hours", rollup.FailingSchedules)
	}
	_, _ = fmt.Fprintln(out, ".")
	_, _ = fmt.Fprintf(out, "Backup components: %s\n", describeBackupStackState(page.BackupStackState))

	// VaultsKnown false is an unread listing, not an organisation with no
	// vaults - so the setup nudge is withheld rather than printed on a blip.
	switch {
	case !rollup.VaultsKnown:
		_, _ = fmt.Fprintln(out, "Backup vaults: could not be read.")
	case rollup.VaultsReady == 0:
		_, _ = fmt.Fprintln(out, "Backup vaults: none ready. Create one with 'ankra backup vaults create' "+
			"- backups need somewhere to write to.")
	default:
		_, _ = fmt.Fprintf(out, "Backup vaults: %d of %d ready.\n", rollup.VaultsReady, rollup.VaultsTotal)
	}

	if len(page.Stacks) == 0 {
		_, _ = fmt.Fprintln(out, "\nNo stack matched.")
		return
	}
	_, _ = fmt.Fprintln(out)

	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{
		"Stack", "Protection", "Data", "Vault", "Schedule", "Last restore point", "Next run", "Last run",
	})
	for _, row := range page.Stacks {
		stackName := row.StackName
		if row.SystemStack {
			stackName += " (Ankra)"
		}
		writer.AppendRow(table.Row{
			stackName,
			describeProtection(row),
			describeDataAssets(row),
			orDash(row.VaultName),
			orDash(row.Schedule),
			describeLastRestorePoint(row),
			describeNextRun(row),
			describeLatestRun(row),
		})
	}
	writer.Render()
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func init() {
	clusterBackupsStatusCmd.Flags().StringArray("protection", nil,
		"Only stacks with this verdict: "+strings.Join(client.ProtectionStates, ", ")+" (repeatable)")
	clusterBackupsStatusCmd.Flags().String("cursor", "", "Continue from a previous page's cursor")
	clusterBackupsStatusCmd.Flags().Int("limit", 0,
		fmt.Sprintf("Stacks per page (default 25, maximum %d)", client.MaximumClusterBackupsPageSize))
	registerStructuredOutputFlags(clusterBackupsStatusCmd)

	clusterBackupsCmd.AddCommand(clusterBackupsStatusCmd)
	clusterCmd.AddCommand(clusterBackupsCmd)
}
