package cmd

// `ankra cluster addons settings set` (epic ankra-0xsdd, WS4).
//
// `addons update -f settings.json` already replaces a whole settings document.
// This is the other half: changing one field without first reading, editing
// and re-sending the rest - which is how a scripted edit drops the fields it
// did not know about. Every flag here is read-modify-write against the stored
// settings, so an unset flag leaves its field exactly as it was.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterAddonsSettingsSetCmd = &cobra.Command{
	Use:   "set <addon_name>",
	Short: "Change individual settings on an addon",
	Long: `Change one or more settings on an addon without resending the rest.

Each flag is applied to the addon's stored settings; anything you do not pass
is left as it is. To replace the whole document instead, use
'ankra cluster addons update <addon> -f settings.json'.

Backup settings (closed beta) override the add-on's stack backup policy for
this add-on only - use them for the one add-on in a protected stack that
should be captured differently:

  ankra cluster addons settings set cnpg-cluster --backup-enabled=true --backup-consistency=transactional
  ankra cluster addons settings set redis-cache --backup-enabled=false
  ankra cluster addons settings set cnpg-cluster --backup-schedule=hourly --backup-retention-daily=14`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addonName := args[0]

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		current, err := apiClient.GetAddonSettings(cluster.ID, addonName)
		if err != nil {
			return fmt.Errorf("getting addon settings: %w", err)
		}

		settings := current.Settings
		changed, err := applyAddonSettingsFlags(cmd, &settings)
		if err != nil {
			return err
		}
		if !changed {
			return withExitCode(exitUsage, fmt.Errorf("no settings to change: pass at least one flag (see 'ankra cluster addons settings set --help')"))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := apiClient.UpdateAddonSettings(ctx, cluster.ID, addonName, settings)
		if err != nil {
			return fmt.Errorf("updating addon settings: %w", err)
		}

		if handled, renderError := renderStructured(cmd, client.GetAddonSettingsResponse{
			AddonName: addonName, Settings: settings,
		}); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Settings for addon '%s' updated.\n\n", addonName)
		encoded, marshalError := json.MarshalIndent(settings, "", "  ")
		if marshalError != nil {
			return fmt.Errorf("formatting settings: %w", marshalError)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
		printGitPushDeferral(cmd.OutOrStdout(), result.GitPushDeferred, result.GitPushMessage)
		return nil
	},
}

// applyAddonSettingsFlags folds every flag the caller actually passed into the
// stored settings, and reports whether anything changed. Changed=false is a
// usage error rather than a no-op write: a `set` that sent the settings back
// unchanged would still queue an addon update job and roll the release.
func applyAddonSettingsFlags(cmd *cobra.Command, settings *client.AddonSettings) (bool, error) {
	changed := false

	if cmd.Flags().Changed("revision-history-limit") {
		limit, flagError := cmd.Flags().GetInt("revision-history-limit")
		if flagError != nil {
			return false, flagError
		}
		settings.RevisionHistoryLimit = &limit
		changed = true
	}

	for _, syncFlag := range []struct {
		name   string
		target func(*client.SyncPolicy) *bool
	}{
		{"automated", func(policy *client.SyncPolicy) *bool { return &policy.Automated }},
		{"self-heal", func(policy *client.SyncPolicy) *bool { return &policy.SelfHeal }},
		{"auto-prune", func(policy *client.SyncPolicy) *bool { return &policy.AutoPrune }},
	} {
		if !cmd.Flags().Changed(syncFlag.name) {
			continue
		}
		value, flagError := cmd.Flags().GetBool(syncFlag.name)
		if flagError != nil {
			return false, flagError
		}
		if settings.SyncPolicy == nil {
			settings.SyncPolicy = &client.SyncPolicy{}
		}
		*syncFlag.target(settings.SyncPolicy) = value
		changed = true
	}

	backupChanged, backupError := applyAddonBackupFlags(cmd, settings)
	if backupError != nil {
		return false, backupError
	}
	return changed || backupChanged, nil
}

func applyAddonBackupFlags(cmd *cobra.Command, settings *client.AddonSettings) (bool, error) {
	backupFlagNames := []string{
		"backup-enabled", "backup-consistency", "backup-schedule",
		"backup-retention-hourly", "backup-retention-daily", "backup-retention-weekly",
		"backup-retention-monthly", "backup-retention-yearly",
		"backup-retention-minimum-count", "backup-retention-minimum-age",
	}
	touched := false
	for _, name := range backupFlagNames {
		if cmd.Flags().Changed(name) {
			touched = true
			break
		}
	}
	if !touched {
		return false, nil
	}
	if settings.Backup == nil {
		// A block created by a schedule or retention flag alone is an
		// override of HOW the add-on is captured, not a request to stop
		// capturing it: 'enabled' has no omitempty on the wire, so the block
		// starts on and only --backup-enabled=false turns it off.
		settings.Backup = &client.AddonBackupSettings{Enabled: true}
	}
	backup := settings.Backup

	if cmd.Flags().Changed("backup-enabled") {
		enabled, flagError := cmd.Flags().GetBool("backup-enabled")
		if flagError != nil {
			return false, flagError
		}
		backup.Enabled = enabled
	}
	if cmd.Flags().Changed("backup-consistency") {
		consistency, flagError := cmd.Flags().GetString("backup-consistency")
		if flagError != nil {
			return false, flagError
		}
		if !isKnownBackupConsistency(consistency) {
			return false, withExitCode(exitUsage, fmt.Errorf(
				"--backup-consistency must be one of transactional, application, crash, logical (got %q)", consistency))
		}
		backup.Consistency = consistency
	}
	if cmd.Flags().Changed("backup-schedule") {
		schedule, flagError := cmd.Flags().GetString("backup-schedule")
		if flagError != nil {
			return false, flagError
		}
		backup.Schedule = schedule
	}

	retentionFlags := []struct {
		name   string
		target func(*client.AddonBackupRetention) *int
	}{
		{"backup-retention-hourly", func(r *client.AddonBackupRetention) *int { return &r.Hourly }},
		{"backup-retention-daily", func(r *client.AddonBackupRetention) *int { return &r.Daily }},
		{"backup-retention-weekly", func(r *client.AddonBackupRetention) *int { return &r.Weekly }},
		{"backup-retention-monthly", func(r *client.AddonBackupRetention) *int { return &r.Monthly }},
		{"backup-retention-yearly", func(r *client.AddonBackupRetention) *int { return &r.Yearly }},
		{"backup-retention-minimum-count", func(r *client.AddonBackupRetention) *int { return &r.MinimumCount }},
	}
	for _, retentionFlag := range retentionFlags {
		if !cmd.Flags().Changed(retentionFlag.name) {
			continue
		}
		count, flagError := cmd.Flags().GetInt(retentionFlag.name)
		if flagError != nil {
			return false, flagError
		}
		if count < 0 {
			return false, withExitCode(exitUsage, fmt.Errorf("--%s must be zero or positive (got %d)", retentionFlag.name, count))
		}
		if backup.Retention == nil {
			backup.Retention = &client.AddonBackupRetention{}
		}
		*retentionFlag.target(backup.Retention) = count
	}
	if cmd.Flags().Changed("backup-retention-minimum-age") {
		minimumAge, flagError := cmd.Flags().GetString("backup-retention-minimum-age")
		if flagError != nil {
			return false, flagError
		}
		if _, parseError := time.ParseDuration(minimumAge); minimumAge != "" && parseError != nil {
			return false, withExitCode(exitUsage, fmt.Errorf(
				"--backup-retention-minimum-age must be a duration such as 24h or 720h (got %q)", minimumAge))
		}
		if backup.Retention == nil {
			backup.Retention = &client.AddonBackupRetention{}
		}
		backup.Retention.MinimumAge = minimumAge
	}
	return true, nil
}

func isKnownBackupConsistency(value string) bool {
	switch value {
	case "transactional", "application", "crash", "logical":
		return true
	}
	return false
}

func registerAddonSettingsSetFlags(command *cobra.Command) {
	command.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	command.Flags().Int("revision-history-limit", 10, "How many previous releases to keep")
	command.Flags().Bool("automated", true, "Sync the addon automatically when its definition changes")
	command.Flags().Bool("self-heal", true, "Re-apply the addon when the live state drifts")
	command.Flags().Bool("auto-prune", true, "Delete resources the addon no longer declares")
	command.Flags().Bool("backup-enabled", false, "Capture this addon's data assets under the stack's backup policy")
	command.Flags().String("backup-consistency", "", "How the capture is taken: transactional, application, crash or logical")
	command.Flags().String("backup-schedule", "", "Override the stack's schedule for this addon: hourly, daily, weekly, or a cron expression")
	command.Flags().Int("backup-retention-hourly", 0, "Hourly restore points to keep for this addon")
	command.Flags().Int("backup-retention-daily", 0, "Daily restore points to keep for this addon")
	command.Flags().Int("backup-retention-weekly", 0, "Weekly restore points to keep for this addon")
	command.Flags().Int("backup-retention-monthly", 0, "Monthly restore points to keep for this addon")
	command.Flags().Int("backup-retention-yearly", 0, "Yearly restore points to keep for this addon")
	command.Flags().Int("backup-retention-minimum-count", 0, "Keep at least this many restore points for this addon")
	command.Flags().String("backup-retention-minimum-age", "", "Never expire a restore point younger than this (e.g. 24h)")
	registerStructuredOutputFlags(command)
}
