package cmd

// The stack backup block in the apply dialect (epic ankra-0xsdd, WS4).
//
// `ankra cluster apply` replaces a stack with what the file says, so a field
// the dialect does not read is a field the apply silently clears. That is why
// stack variables had to be added here after the fact (ankra-yxxa), and it is
// exactly the hazard that made `backup` worth parsing carefully: the block
// decides whether the stack is backed up at all.
//
// The platform preserves a stored policy when the request carries no block,
// so a file written before this field existed cannot unprotect anything. This
// parser's job is the other direction - a block that IS in the file has to
// arrive intact, and a block with a typo has to be refused here, with the
// file's own vocabulary in the message, rather than travelling as a partial
// policy that looks like a deliberate narrowing on the far end.

import (
	"fmt"
	"strconv"

	"ankra/internal/client"
)

// parseStackBackup reads the optional stack-level 'backup' block. A nil
// return means the file carried none, which preserves the stored policy.
func parseStackBackup(raw interface{}) (*client.StackBackup, error) {
	if raw == nil {
		return nil, nil
	}
	rawMap, isMap := raw.(map[string]interface{})
	if !isMap {
		return nil, fmt.Errorf("'backup' must be a block with fields such as 'enabled' and 'vault', got %v", raw)
	}
	backup := &client.StackBackup{}
	enabled, enabledError := backupBool(rawMap, "enabled", "backup.enabled")
	if enabledError != nil {
		return nil, enabledError
	}
	backup.Enabled = enabled != nil && *enabled

	vault, vaultError := backupString(rawMap, "vault", "backup.vault")
	if vaultError != nil {
		return nil, vaultError
	}
	backup.Vault = vault

	schedule, scheduleError := backupString(rawMap, "schedule", "backup.schedule")
	if scheduleError != nil {
		return nil, scheduleError
	}
	backup.Schedule = schedule

	retention, retentionError := parseStackBackupRetention(rawMap["retention"])
	if retentionError != nil {
		return nil, retentionError
	}
	backup.Retention = retention

	selection, selectionError := parseStackBackupSelection(rawMap["selection"])
	if selectionError != nil {
		return nil, selectionError
	}
	backup.Selection = selection

	if backup.Enabled && backup.Vault == "" {
		return nil, fmt.Errorf("'backup.vault' is required when 'backup.enabled' is true: a protected stack needs a backup vault to write restore points to")
	}
	if selection != nil && selection.Databases != nil && !*selection.Databases && !selection.ConfirmExcludeDatabases {
		return nil, fmt.Errorf(
			"'backup.selection.databases: false' excludes this stack's databases from its restore points; set 'backup.selection.confirm_exclude_databases: true' in the same block to acknowledge that")
	}
	return backup, nil
}

func parseStackBackupRetention(raw interface{}) (*client.StackBackupRetention, error) {
	if raw == nil {
		return nil, nil
	}
	rawMap, isMap := raw.(map[string]interface{})
	if !isMap {
		return nil, fmt.Errorf("'backup.retention' must be a block of tier counts, got %v", raw)
	}
	retention := &client.StackBackupRetention{}
	for _, tier := range []struct {
		key    string
		target *int
	}{
		{"hourly", &retention.Hourly},
		{"daily", &retention.Daily},
		{"weekly", &retention.Weekly},
		{"monthly", &retention.Monthly},
		{"yearly", &retention.Yearly},
		{"minimum_count", &retention.MinimumCount},
	} {
		count, countError := backupNonNegativeInt(rawMap, tier.key, "backup.retention."+tier.key)
		if countError != nil {
			return nil, countError
		}
		*tier.target = count
	}
	minimumAge, minimumAgeError := backupString(rawMap, "minimum_age", "backup.retention.minimum_age")
	if minimumAgeError != nil {
		return nil, minimumAgeError
	}
	retention.MinimumAge = minimumAge
	return retention, nil
}

func parseStackBackupSelection(raw interface{}) (*client.StackBackupSelection, error) {
	if raw == nil {
		return nil, nil
	}
	rawMap, isMap := raw.(map[string]interface{})
	if !isMap {
		return nil, fmt.Errorf("'backup.selection' must be a block with 'databases' and 'persistent_volume_claims', got %v", raw)
	}
	selection := &client.StackBackupSelection{}
	databases, databasesError := backupBool(rawMap, "databases", "backup.selection.databases")
	if databasesError != nil {
		return nil, databasesError
	}
	selection.Databases = databases

	confirmed, confirmedError := backupBool(rawMap, "confirm_exclude_databases", "backup.selection.confirm_exclude_databases")
	if confirmedError != nil {
		return nil, confirmedError
	}
	selection.ConfirmExcludeDatabases = confirmed != nil && *confirmed

	rawClaims, hasClaims := rawMap["persistent_volume_claims"]
	if !hasClaims || rawClaims == nil {
		return selection, nil
	}
	claimList, isList := rawClaims.([]interface{})
	if !isList {
		return nil, fmt.Errorf("'backup.selection.persistent_volume_claims' must be a list of data asset names, got %v", rawClaims)
	}
	for _, entry := range claimList {
		name, isString := entry.(string)
		if !isString {
			return nil, fmt.Errorf("'backup.selection.persistent_volume_claims' entries must be strings, got %v", entry)
		}
		selection.PersistentVolumeClaims = append(selection.PersistentVolumeClaims, name)
	}
	return selection, nil
}

// backupBool reads an optional boolean. The pointer return keeps absent and
// false apart, which is what lets `databases: false` demand a confirmation
// while an omitted key takes the default.
func backupBool(rawMap map[string]interface{}, key string, field string) (*bool, error) {
	raw, present := rawMap[key]
	if !present || raw == nil {
		return nil, nil
	}
	value, isBool := raw.(bool)
	if !isBool {
		return nil, fmt.Errorf("'%s' must be true or false, got %v", field, raw)
	}
	return &value, nil
}

func backupString(rawMap map[string]interface{}, key string, field string) (string, error) {
	raw, present := rawMap[key]
	if !present || raw == nil {
		return "", nil
	}
	value, isString := raw.(string)
	if !isString {
		return "", fmt.Errorf("'%s' must be a string, got %v", field, raw)
	}
	return value, nil
}

// backupNonNegativeInt reads an optional retention tier. yaml.v3 hands back a
// Go int for an unquoted number, the same contract parseStackVariables
// depends on; int64 and a whole float64 are accepted for a file that reached
// here through a JSON round-trip.
func backupNonNegativeInt(rawMap map[string]interface{}, key string, field string) (int, error) {
	raw, present := rawMap[key]
	if !present || raw == nil {
		return 0, nil
	}
	var count int
	switch typed := raw.(type) {
	case int:
		count = typed
	case int64:
		count = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("'%s' must be a whole number, got %v", field, typed)
		}
		count = int(typed)
	case string:
		parsed, parseError := strconv.Atoi(typed)
		if parseError != nil {
			return 0, fmt.Errorf("'%s' must be an integer, got %q", field, typed)
		}
		count = parsed
	default:
		return 0, fmt.Errorf("'%s' must be an integer, got %v", field, raw)
	}
	if count < 0 {
		return 0, fmt.Errorf("'%s' must be zero or positive, got %d", field, count)
	}
	return count, nil
}
