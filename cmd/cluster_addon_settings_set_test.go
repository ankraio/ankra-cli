package cmd

import (
	"testing"

	"ankra/internal/client"
)

// A schedule or retention flag on an add-on with no override yet creates the
// override block. 'enabled' has no omitempty on the wire, so that block must
// start ON: the command's own example
// `settings set cnpg-cluster --backup-schedule=hourly --backup-retention-daily=14`
// is a request to capture the add-on differently, not to stop capturing it.
func TestSettingsSetScheduleAloneKeepsTheAddonCaptured(t *testing.T) {
	t.Cleanup(func() { resetTreeFlags(t, clusterAddonsSettingsSetCmd) })
	for flag, value := range map[string]string{"backup-schedule": "hourly", "backup-retention-daily": "14"} {
		if err := clusterAddonsSettingsSetCmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	settings := client.AddonSettings{}
	changed, err := applyAddonBackupFlags(clusterAddonsSettingsSetCmd, &settings)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || settings.Backup == nil {
		t.Fatalf("the flags must create the override block, got changed=%v backup=%+v", changed, settings.Backup)
	}
	if !settings.Backup.Enabled {
		t.Errorf("an override created by a schedule flag alone must be enabled, got %+v", settings.Backup)
	}
	if settings.Backup.Schedule != "hourly" || settings.Backup.Retention == nil || settings.Backup.Retention.Daily != 14 {
		t.Errorf("the schedule and retention must land on the block, got %+v", settings.Backup)
	}
}

// --backup-enabled=false on the same call still turns the block off.
func TestSettingsSetBackupEnabledFalseWinsOverTheDefault(t *testing.T) {
	t.Cleanup(func() { resetTreeFlags(t, clusterAddonsSettingsSetCmd) })
	for flag, value := range map[string]string{"backup-schedule": "hourly", "backup-enabled": "false"} {
		if err := clusterAddonsSettingsSetCmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	settings := client.AddonSettings{}
	if _, err := applyAddonBackupFlags(clusterAddonsSettingsSetCmd, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Backup == nil || settings.Backup.Enabled {
		t.Errorf("--backup-enabled=false must turn the block off, got %+v", settings.Backup)
	}
}
