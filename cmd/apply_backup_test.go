package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

// decodeBackupBlock parses a YAML stack fragment the way apply does, so the
// scalar types the tests exercise are the ones yaml.v3 actually produces.
func decodeBackupBlock(t *testing.T, document string) interface{} {
	t.Helper()
	var stack map[string]interface{}
	if unmarshalError := yaml.Unmarshal([]byte(document), &stack); unmarshalError != nil {
		t.Fatalf("decoding the fixture: %v", unmarshalError)
	}
	return stack["backup"]
}

func TestParseStackBackupIsNilForAnAbsentBlock(t *testing.T) {
	backup, parseError := parseStackBackup(decodeBackupBlock(t, "name: platform\n"))
	if parseError != nil {
		t.Fatalf("parseError = %v", parseError)
	}
	if backup != nil {
		t.Fatalf("an absent block must stay nil so the platform preserves the stored policy, got %+v", backup)
	}
}

func TestParseStackBackupReadsTheWholeBlock(t *testing.T) {
	backup, parseError := parseStackBackup(decodeBackupBlock(t, `
name: platform
backup:
  enabled: true
  vault: offsite
  schedule: daily
  retention:
    hourly: 24
    daily: 7
    weekly: 4
    monthly: 6
    yearly: 1
    minimum_count: 3
    minimum_age: 24h
  selection:
    databases: true
    persistent_volume_claims:
      - shop/data
      - shop/uploads
`))
	if parseError != nil {
		t.Fatalf("parseError = %v", parseError)
	}
	if !backup.Enabled || backup.Vault != "offsite" || backup.Schedule != "daily" {
		t.Fatalf("backup = %+v", backup)
	}
	expected := client.StackBackupRetention{
		Hourly: 24, Daily: 7, Weekly: 4, Monthly: 6, Yearly: 1, MinimumCount: 3, MinimumAge: "24h",
	}
	if *backup.Retention != expected {
		t.Fatalf("retention = %+v, want %+v", *backup.Retention, expected)
	}
	if backup.Selection.Databases == nil || !*backup.Selection.Databases {
		t.Fatal("databases: true must parse as an explicit selection")
	}
	if len(backup.Selection.PersistentVolumeClaims) != 2 {
		t.Fatalf("claims = %+v", backup.Selection.PersistentVolumeClaims)
	}
}

func TestParseStackBackupRefusesAProtectedStackWithNoVault(t *testing.T) {
	_, parseError := parseStackBackup(decodeBackupBlock(t, "backup:\n  enabled: true\n"))
	if parseError == nil {
		t.Fatal("a protected stack with no vault must be refused before the request is sent")
	}
	if !strings.Contains(parseError.Error(), "backup.vault") {
		t.Fatalf("the refusal must name the field: %v", parseError)
	}
}

func TestParseStackBackupRefusesAnUnconfirmedDatabaseExclusion(t *testing.T) {
	_, parseError := parseStackBackup(decodeBackupBlock(t, `
backup:
  enabled: true
  vault: offsite
  selection:
    databases: false
`))
	if parseError == nil {
		t.Fatal("excluding databases without a confirmation must be refused")
	}
	if !strings.Contains(parseError.Error(), "confirm_exclude_databases") {
		t.Fatalf("the refusal must name the confirmation: %v", parseError)
	}
}

func TestParseStackBackupAcceptsAConfirmedDatabaseExclusion(t *testing.T) {
	backup, parseError := parseStackBackup(decodeBackupBlock(t, `
backup:
  enabled: true
  vault: offsite
  selection:
    databases: false
    confirm_exclude_databases: true
    persistent_volume_claims:
      - shop/data
`))
	if parseError != nil {
		t.Fatalf("parseError = %v", parseError)
	}
	if !backup.Selection.ConfirmExcludeDatabases {
		t.Fatal("the confirmation must travel with the request")
	}
}

func TestParseStackBackupKeepsAbsentAndFalseApartForDatabases(t *testing.T) {
	absent, absentError := parseStackBackup(decodeBackupBlock(t, `
backup:
  enabled: true
  vault: offsite
  selection:
    persistent_volume_claims: [shop/data]
`))
	if absentError != nil {
		t.Fatalf("absentError = %v", absentError)
	}
	if absent.Selection.Databases != nil {
		t.Fatal("an absent databases key must stay nil so the platform applies its own default")
	}
}

func TestParseStackBackupRefusesMalformedMembers(t *testing.T) {
	for name, document := range map[string]string{
		"block is a scalar":          "backup: yes\n",
		"enabled is not a boolean":   "backup:\n  enabled: maybe\n",
		"vault is not a string":      "backup:\n  vault: 7\n",
		"retention is a scalar":      "backup:\n  retention: 7\n",
		"a tier is not a number":     "backup:\n  retention:\n    daily: seven\n",
		"a tier is negative":         "backup:\n  retention:\n    daily: -1\n",
		"selection is a list":        "backup:\n  selection: [a]\n",
		"databases is not a boolean": "backup:\n  selection:\n    databases: maybe\n",
		"a claim is not a string":    "backup:\n  selection:\n    persistent_volume_claims: [7]\n",
		"claims is not a list":       "backup:\n  selection:\n    persistent_volume_claims: shop/data\n",
	} {
		t.Run(name, func(subtest *testing.T) {
			if _, parseError := parseStackBackup(decodeBackupBlock(subtest, document)); parseError == nil {
				subtest.Fatal("a malformed block must be refused")
			}
		})
	}
}

func TestParseStackBackupAcceptsACronSchedule(t *testing.T) {
	backup, parseError := parseStackBackup(decodeBackupBlock(t, `
backup:
  enabled: true
  vault: offsite
  schedule: "*/15 * * * *"
`))
	if parseError != nil {
		t.Fatalf("parseError = %v", parseError)
	}
	if backup.Schedule != "*/15 * * * *" {
		t.Fatalf("schedule = %q", backup.Schedule)
	}
}

// A key the dialect does not read is the one typo that is not harmless here:
// 'enable: true' parses as a block with enabled unset, which the platform
// reads as a deliberate "enabled: false" and unprotects the stack. Refusing
// the block with the file's own vocabulary is the whole point of parsing it.
func TestParseStackBackupRefusesAKeyItDoesNotRead(t *testing.T) {
	for name, document := range map[string]string{
		"enabled misspelled":        "backup:\n  enable: true\n  vault: prod\n",
		"vault misspelled":          "backup:\n  enabled: true\n  vaults: prod\n",
		"retention tier misspelled": "backup:\n  enabled: true\n  vault: prod\n  retention:\n    dayly: 7\n",
		"selection key misspelled":  "backup:\n  enabled: true\n  vault: prod\n  selection:\n    database: false\n",
	} {
		t.Run(name, func(subtest *testing.T) {
			_, parseError := parseStackBackup(decodeBackupBlock(subtest, document))
			if parseError == nil {
				subtest.Fatal("a block with a key this CLI does not read must be refused, not sent as a narrower policy")
			}
			if !strings.Contains(parseError.Error(), "does not read") {
				subtest.Fatalf("the refusal must name the unread key, got: %v", parseError)
			}
		})
	}
}
