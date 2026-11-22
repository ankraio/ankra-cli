package client

import (
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
)

// clusterBackupsWireFixture is the cluster Backups read as the platform sends
// it after ankra-t5wgj3: the backup stack in the unknown state, and one row for
// each verdict the CloudNativePG backup path can decide, each with the sentence
// behind it. It is raw JSON rather than a typed value so the test pins what the
// client keeps from the wire, not what a Go literal happens to set.
const clusterBackupsWireFixture = `{
  "cluster_id": "` + testClusterUUID + `",
  "backup_stack_state": "unknown",
  "backup_stack_issue": "Ankra could not confirm that the barman-cloud plugin is running on this cluster.",
  "rollup": {"total_stacks": 4, "stateful_stacks": 4, "protected_stacks": 0,
    "unprotected_stateful_stacks": 2, "unknown_stacks": 2, "failing_schedules": 0,
    "last_restore_point_at": null, "vaults_total": 1, "vaults_ready": 1, "vaults_known": true},
  "stacks": [
    {"stack_name": "billing", "stateful": true, "protection_state": "unprotected",
     "unprotected_reason": "barman_plugin_not_running",
     "data_asset_count": 1, "data_asset_count_known": true, "database_asset_count": 1,
     "next_scheduled_at": null, "failing_runs_24h": 0, "last_restore_point": null, "latest_run": null,
     "backup_path_detail": "The barman-cloud plugin has no available replica. Restart its Deployment in the cnpg-system namespace."},
    {"stack_name": "orders", "stateful": true, "protection_state": "unprotected",
     "unprotected_reason": "archiving_failing",
     "data_asset_count": 1, "data_asset_count_known": true, "database_asset_count": 1,
     "next_scheduled_at": null, "failing_runs_24h": 0, "last_restore_point": null, "latest_run": null,
     "backup_path_detail": "CloudNativePG cluster 'orders/orders-db' reports that its write-ahead log archive is failing, so its data is not reaching the backup vault."},
    {"stack_name": "shop", "stateful": true, "protection_state": "unknown",
     "unknown_reason": "barman_plugin_unverified",
     "data_asset_count": 1, "data_asset_count_known": true, "database_asset_count": 1,
     "next_scheduled_at": null, "failing_runs_24h": 0, "last_restore_point": null, "latest_run": null,
     "backup_path_detail": "Ankra could not confirm that the barman-cloud plugin is running on this cluster."},
    {"stack_name": "users", "stateful": true, "protection_state": "unknown",
     "unknown_reason": "archiving_unreported",
     "data_asset_count": 1, "data_asset_count_known": true, "database_asset_count": 1,
     "next_scheduled_at": null, "failing_runs_24h": 0, "last_restore_point": null, "latest_run": null,
     "backup_path_detail": "CloudNativePG cluster 'users/users-db' has not reported on its write-ahead log archive."}
  ],
  "next_cursor": null
}`

// detailsByStack reads stack_name -> backup_path_detail out of a decoded page.
func detailsByStack(t *testing.T, document map[string]any) map[string]string {
	t.Helper()
	rows, _ := document["stacks"].([]any)
	details := map[string]string{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		name, _ := row["stack_name"].(string)
		detail, _ := row["backup_path_detail"].(string)
		details[name] = detail
	}
	return details
}

// The sentence behind a verdict the backup path decided is what says which
// database is affected and what to do. A page that dropped it on decode would
// leave `-o json` and `-o yaml` carrying the reason code and nothing a person
// can act on (ankra-z2tfp4).
func TestGetClusterBackups_KeepsTheBackupPathDetailOfEveryRow(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(clusterBackupsWireFixture))
	})

	page, readError := testClient.GetClusterBackups(testClusterUUID, ClusterBackupsOptions{})
	if readError != nil {
		t.Fatalf("GetClusterBackups: %v", readError)
	}
	if page.BackupStackState != "unknown" {
		t.Errorf("backup_stack_state = %q, want the platform's unknown kept as sent", page.BackupStackState)
	}

	var sent map[string]any
	if decodeError := json.Unmarshal([]byte(clusterBackupsWireFixture), &sent); decodeError != nil {
		t.Fatalf("decoding the fixture: %v", decodeError)
	}
	want := detailsByStack(t, sent)
	if len(want) != 4 {
		t.Fatalf("the fixture should carry four rows, got %d", len(want))
	}

	// -o json and -o yaml encode the typed page, so what survives this
	// round trip is what a script reading the command's output gets.
	encodedJSON, jsonError := json.Marshal(page)
	if jsonError != nil {
		t.Fatalf("encoding the page as JSON: %v", jsonError)
	}
	var asJSON map[string]any
	if decodeError := json.Unmarshal(encodedJSON, &asJSON); decodeError != nil {
		t.Fatalf("decoding the JSON encoding: %v", decodeError)
	}
	encodedYAML, yamlError := yaml.Marshal(page)
	if yamlError != nil {
		t.Fatalf("encoding the page as YAML: %v", yamlError)
	}
	var asYAML map[string]any
	if decodeError := yaml.Unmarshal(encodedYAML, &asYAML); decodeError != nil {
		t.Fatalf("decoding the YAML encoding: %v", decodeError)
	}

	for format, got := range map[string]map[string]string{
		"json": detailsByStack(t, asJSON),
		"yaml": detailsByStack(t, asYAML),
	} {
		for stack, detail := range want {
			if got[stack] != detail {
				t.Errorf("%s output, stack %s: backup_path_detail = %q, want %q", format, stack, got[stack], detail)
			}
		}
	}
}
