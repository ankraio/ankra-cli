package cmd

// `ankra cluster state-snapshots retention [--days N]` (ankra-hn630j.21): the
// CLI surface over GET/PUT
// /api/v1/org/clusters/{cluster_id}/state-snapshot-retention, the per-cluster
// retention window of Ankra's scheduled etcd backups.
//
// Without --days the command shows the window; with it, it sets the window.
// A shorter window deletes the daily backups that fall outside it, so a
// shortening asks for confirmation first (--yes skips it), like the registry
// retention commands. The cluster is resolved like the sibling
// `control-plane-heal` verbs, through the shared --cluster override or the
// selected cluster.

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The platform's bounds, checked before sending so a typo is a usage error
// rather than a round trip.
const (
	stateSnapshotRetentionMinimumDays = 1
	stateSnapshotRetentionMaximumDays = 90
)

func newClusterStateSnapshotsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "state-snapshots",
		Short: "Settings for the scheduled backups of the cluster's etcd state",
		Long: `Ankra backs up the etcd state of every running cluster it provisions - every
Secret, ConfigMap, custom resource and PersistentVolume binding - every six
hours, encrypted, to your organisation's backup vault (or the Ankra-managed
default vault). A start that finds the cluster's etcd lost restores the newest
backup.`,
		Example: `  ankra cluster state-snapshots retention
  ankra cluster state-snapshots retention --days 30 --cluster prod`,
	}
	command.AddCommand(newClusterStateSnapshotsRetentionCommand())
	return command
}

func newClusterStateSnapshotsRetentionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "retention [--days N]",
		Short: "Show or set how many days of state snapshots Ankra keeps",
		Long: `Show or set how many days of scheduled etcd backups Ankra keeps for the cluster.

Ankra keeps the four newest backups, plus the newest backup of each UTC day for
the retention window: 7 days unless you set another between 1 and 90. The
newest backup from before the cluster's last start or recovery incident is kept
whatever the window.

Without --days this shows the window. With --days it sets it; the next
retention pass applies it. A longer window keeps backups from then on and cannot
bring back ones already deleted. A shorter window deletes the daily backups
outside it from the vault, so it asks for confirmation first (--yes skips it).
Setting the window needs permission to change cluster settings (clusters.write).

-o json prints the platform's answer as it is.`,
		Example: `  ankra cluster state-snapshots retention
  ankra cluster state-snapshots retention --days 30
  ankra cluster state-snapshots retention --days 3 --yes --cluster dev -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterStateSnapshotsRetention(command)
		},
	}
	command.Flags().Int("days", 0, fmt.Sprintf("Set the retention window in days (%d to %d)",
		stateSnapshotRetentionMinimumDays, stateSnapshotRetentionMaximumDays))
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation a shorter window asks for")
	registerStructuredOutputFlags(command)
	return command
}

func runClusterStateSnapshotsRetention(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	isSetting := command.Flags().Changed("days")
	days, _ := command.Flags().GetInt("days")
	if isSetting && days < stateSnapshotRetentionMinimumDays {
		return withExitCode(exitUsage, fmt.Errorf("--days must be at least %d, got %d",
			stateSnapshotRetentionMinimumDays, days))
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	current, getError := apiClient.GetStateSnapshotRetention(ctx, cluster.ID)
	if getError != nil {
		return stateSnapshotRetentionError(getError, cluster, false)
	}
	if !isSetting {
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, current)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "State snapshot retention for cluster '%s':\n\n", cluster.Name)
		printStateSnapshotRetention(out, current)
		return nil
	}
	minimumDays, maximumDays := stateSnapshotRetentionBounds(current)
	if days < minimumDays || days > maximumDays {
		return withExitCode(exitUsage, fmt.Errorf("--days must be %d to %d, got %d", minimumDays, maximumDays, days))
	}
	if days < current.RetentionDays {
		isConfirmed, _ := command.Flags().GetBool("yes")
		if promptError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf(
			"This keeps fewer days of state snapshots for cluster '%s'.\n  Now: %d days.\n  New: %d days.\n"+
				"Daily backups older than %d days are deleted from the vault at the next retention pass and "+
				"cannot be restored. Continue? [y/N]: ", cluster.Name, current.RetentionDays, days, days),
			isConfirmed); promptError != nil {
			return promptError
		}
	}
	stored, setError := apiClient.SetStateSnapshotRetention(ctx, cluster.ID, days)
	if setError != nil {
		return stateSnapshotRetentionError(setError, cluster, true)
	}
	// A stored window that disagrees with the request is a change that did
	// not take effect: report it and exit non-zero so a script does not read
	// it as the window having moved.
	var disagreement error
	if stored.RetentionDays != days {
		disagreement = withExitCode(exitError, fmt.Errorf(
			"the platform stored a state snapshot retention of %d days for cluster '%s', not %d as requested; "+
				"run 'ankra cluster state-snapshots retention' to check it", stored.RetentionDays, cluster.Name, days))
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, stored); encodeError != nil {
			return encodeError
		}
		return disagreement
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "State snapshot retention set to %d days for cluster '%s'.\n\n", stored.RetentionDays, cluster.Name)
	printStateSnapshotRetention(out, stored)
	return disagreement
}

func printStateSnapshotRetention(out io.Writer, retention *client.StateSnapshotRetention) {
	window := fmt.Sprintf("%d days", retention.RetentionDays)
	if retention.IsDefault {
		window += " (default)"
	}
	_, _ = fmt.Fprintf(out, "  Retention window: %s\n", window)
	newest := "the newest backups"
	if retention.KeepLatest > 0 {
		newest = fmt.Sprintf("the %d newest backups", retention.KeepLatest)
	}
	_, _ = fmt.Fprintf(out, "  Kept:             %s, plus the newest of each UTC day in the window\n", newest)
	if retention.MinimumRetentionDays > 0 && retention.MaximumRetentionDays > 0 {
		_, _ = fmt.Fprintf(out, "  Allowed:          %d to %d days\n", retention.MinimumRetentionDays, retention.MaximumRetentionDays)
	}
}

// stateSnapshotRetentionBounds is the window the platform advertises, or the
// built-in 1 to 90 days when it does not advertise one.
func stateSnapshotRetentionBounds(retention *client.StateSnapshotRetention) (int, int) {
	minimumDays, maximumDays := stateSnapshotRetentionMinimumDays, stateSnapshotRetentionMaximumDays
	if retention.MinimumRetentionDays > 0 && retention.MaximumRetentionDays >= retention.MinimumRetentionDays {
		minimumDays, maximumDays = retention.MinimumRetentionDays, retention.MaximumRetentionDays
	}
	return minimumDays, maximumDays
}

// stateSnapshotRetentionError maps a refusal onto what the user can act on: a
// 403 on the write is a missing clusters.write (exit 7), the platform's own
// "Cluster not found" is a cluster outside the selected organisation (exit 3),
// and the router's own 404 is a platform that predates the setting (exit 1).
func stateSnapshotRetentionError(apiError error, cluster client.ClusterListItem, writing bool) error {
	operation := fmt.Sprintf("reading the state snapshot retention of cluster '%s'", cluster.Name)
	if writing {
		operation = fmt.Sprintf("setting the state snapshot retention of cluster '%s'", cluster.Name)
	}
	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change cluster settings (clusters.write) to set the state snapshot "+
					"retention of cluster '%s'. Ask an organisation admin for a role that has it", cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		switch unexpected.Detail {
		case "":
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer this setting yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		case routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer the state snapshot retention setting yet (the route answered 404)", operation))
		case hostedLogsClusterNotFoundDetail:
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
					"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
		default:
			return withExitCode(exitError, fmt.Errorf("%s: %w", operation, apiError))
		}
	}
	if hasStatus && unexpected.StatusCode == http.StatusUnprocessableEntity {
		return withExitCode(exitUsage, fmt.Errorf("%s: the platform refused the window: %w", operation, apiError))
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

func init() {
	clusterCmd.AddCommand(newClusterStateSnapshotsCommand())
}
