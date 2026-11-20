package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const applicationBackupsTestAppID = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"

// applicationBackupsWithKeeping is the platform's answer for one deployment
// whose backups keep 30 days of restore points: every field of the keeping
// block, each with a value.
const applicationBackupsWithKeeping = `{"application_id":"3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f",` +
	`"application_name":"shop","database":{"status":"recorded","engine":"postgresql"},` +
	`"database_backup":true,"deployments":[{"deployment_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a",` +
	`"cluster_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a","cluster_name":"production",` +
	`"stack_name":"deploy-shop","namespace":"shop","has_database":true,"has_database_known":true,` +
	`"posture":null,"keeping":{"state":"known","policy":{"hourly":0,"daily":7,"weekly":4,` +
	`"monthly":0,"yearly":0,"minimum_count":0},"keeps_days":28,"keeps_everything":false,` +
	`"oldest_kept_at":"2026-10-22T02:17:00Z","kept_count":11,"unattributed_kept_count":2,` +
	`"protected_since":"2026-03-04T02:17:00Z","protected_since_state":"known",` +
	`"last_restore_test":{"tested_at":"2026-11-02T09:00:00Z","state":"passed","took_seconds":null,` +
	`"bytes_back":null},"last_restore_test_state":"known"}}],"ready_vault_count":1,` +
	`"ready_vault_known":true,"warnings":[]}`

// applicationBackupsWithUnreadKeeping is the block when Ankra could not read
// it: every field null or false, none of them meaning none.
const applicationBackupsWithUnreadKeeping = `{"application_id":"3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f",` +
	`"application_name":"shop","database":{"status":"absent"},"database_backup":true,` +
	`"deployments":[{"deployment_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a",` +
	`"cluster_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a","cluster_name":"production",` +
	`"stack_name":"deploy-shop","namespace":"shop","has_database":false,"has_database_known":true,` +
	`"posture":null,"keeping":{"state":"unknown","policy":null,"keeps_days":null,` +
	`"keeps_everything":false,"oldest_kept_at":null,"kept_count":null,"unattributed_kept_count":null,` +
	`"protected_since":null,"protected_since_state":"unknown","last_restore_test":null,` +
	`"last_restore_test_state":"unknown","zz_added_by_a_newer_platform":{"days":3}}}],` +
	`"ready_vault_count":1,"ready_vault_known":true,"warnings":[]}`

// applicationBackupsWithoutKeeping is the answer of a platform that predates
// the block: the key is not there at all.
const applicationBackupsWithoutKeeping = `{"application_id":"3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f",` +
	`"application_name":"shop","database":{"status":"absent"},"database_backup":true,` +
	`"deployments":[{"deployment_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a",` +
	`"cluster_id":"4d5e6f7a-8b9c-4d0e-8f1a-2b3c4d5e6f7a","cluster_name":"production",` +
	`"stack_name":null,"namespace":null,"has_database":false,"has_database_known":true,` +
	`"posture":null}],"ready_vault_count":0,"ready_vault_known":true,"warnings":[]}`

func readApplicationBackups(t *testing.T, document string) *ApplicationBackups {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/"+applicationBackupsTestAppID+"/backups" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(server.Close)
	backups, readError := New("test-token", server.URL).GetApplicationBackups(applicationBackupsTestAppID)
	if readError != nil {
		t.Fatalf("GetApplicationBackups: %v", readError)
	}
	return backups
}

func firstDeploymentObject(t *testing.T, document []byte) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if decodeError := json.Unmarshal(document, &decoded); decodeError != nil {
		t.Fatalf("not a JSON object: %v\n%s", decodeError, document)
	}
	deployments, isList := decoded["deployments"].([]any)
	if !isList || len(deployments) != 1 {
		t.Fatalf("expected one deployment, got %v", decoded["deployments"])
	}
	deployment, isObject := deployments[0].(map[string]any)
	if !isObject {
		t.Fatalf("the deployment is not an object: %v", deployments[0])
	}
	return deployment
}

// The typed decode carries the keeping block: re-encoding what the CLI read
// gives back every field of the block the platform sent, with its value.
// Before the block was declared, it was dropped here, and with it from
// -o json and -o yaml.
func TestApplicationBackupsDecodeKeepsTheKeepingBlock(t *testing.T) {
	backups := readApplicationBackups(t, applicationBackupsWithKeeping)

	reencoded, encodeError := json.Marshal(backups)
	if encodeError != nil {
		t.Fatalf("encoding the decoded answer: %v", encodeError)
	}
	got := firstDeploymentObject(t, reencoded)["keeping"]
	want := firstDeploymentObject(t, []byte(applicationBackupsWithKeeping))["keeping"]
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("the keeping block did not survive the decode:\nwant %v\ngot  %v", want, got)
	}
}

func TestApplicationBackupsDecodeReadsEveryKeepingField(t *testing.T) {
	backups := readApplicationBackups(t, applicationBackupsWithKeeping)

	keeping := backups.Deployments[0].Keeping
	if keeping == nil {
		t.Fatal("the keeping block was not decoded")
	}
	if keeping.State != "known" || keeping.KeepsEverything {
		t.Fatalf("state and keeps_everything: got %+v", keeping)
	}
	if keeping.KeepsDays == nil || *keeping.KeepsDays != 28 {
		t.Fatalf("keeps_days: got %v", keeping.KeepsDays)
	}
	if keeping.KeptCount == nil || *keeping.KeptCount != 11 ||
		keeping.UnattributedKeptCount == nil || *keeping.UnattributedKeptCount != 2 {
		t.Fatalf("kept counts: got %v and %v", keeping.KeptCount, keeping.UnattributedKeptCount)
	}
	if keeping.OldestKeptAt == nil || *keeping.OldestKeptAt != "2026-10-22T02:17:00Z" {
		t.Fatalf("oldest_kept_at: got %v", keeping.OldestKeptAt)
	}
	if keeping.ProtectedSinceState != "known" || keeping.ProtectedSince == nil ||
		*keeping.ProtectedSince != "2026-03-04T02:17:00Z" {
		t.Fatalf("protected_since: got %q %v", keeping.ProtectedSinceState, keeping.ProtectedSince)
	}
	if keeping.Policy == nil || keeping.Policy.Daily != 7 || keeping.Policy.Weekly != 4 {
		t.Fatalf("policy: got %+v", keeping.Policy)
	}
	if keeping.LastRestoreTestState != "known" || keeping.LastRestoreTest == nil ||
		keeping.LastRestoreTest.State != "passed" || keeping.LastRestoreTest.TookSeconds != nil {
		t.Fatalf("last restore test: got %q %+v", keeping.LastRestoreTestState, keeping.LastRestoreTest)
	}
}

// -o json and -o yaml print the document the platform answered, so null
// stays null, a key the platform left out stays out, and a field this CLI
// does not know yet is passed through rather than dropped.
func TestApplicationBackupsKeepTheResponseDocumentBesideTheDecode(t *testing.T) {
	for name, document := range map[string]string{
		"with the keeping block":          applicationBackupsWithKeeping,
		"with an unread keeping block":    applicationBackupsWithUnreadKeeping,
		"from a platform with no keeping": applicationBackupsWithoutKeeping,
	} {
		t.Run(name, func(t *testing.T) {
			backups := readApplicationBackups(t, document)
			if got := string(backups.ResponseDocument().RawJSON()); got != document {
				t.Fatalf("RawJSON = %s, want the body as answered", got)
			}
		})
	}
}

// An unread block decodes with every value unset rather than as zero, and a
// field the CLI does not declare does not break the read.
func TestApplicationBackupsDecodeKeepsAnUnreadKeepingBlockUnset(t *testing.T) {
	backups := readApplicationBackups(t, applicationBackupsWithUnreadKeeping)

	keeping := backups.Deployments[0].Keeping
	if keeping == nil || keeping.State != "unknown" {
		t.Fatalf("expected an unknown keeping block, got %+v", keeping)
	}
	if keeping.KeptCount != nil || keeping.UnattributedKeptCount != nil || keeping.KeepsDays != nil ||
		keeping.OldestKeptAt != nil || keeping.ProtectedSince != nil || keeping.Policy != nil ||
		keeping.LastRestoreTest != nil {
		t.Fatalf("an unread block must decode with every value unset, got %+v", keeping)
	}
}

// A platform that predates the block leaves Keeping nil, and re-encoding
// the decode does not invent a keeping key for it.
func TestApplicationBackupsDecodeWithoutKeepingLeavesItOut(t *testing.T) {
	backups := readApplicationBackups(t, applicationBackupsWithoutKeeping)

	if backups.Deployments[0].Keeping != nil {
		t.Fatalf("no block was sent, got %+v", backups.Deployments[0].Keeping)
	}
	reencoded, encodeError := json.Marshal(backups)
	if encodeError != nil {
		t.Fatalf("encoding the decoded answer: %v", encodeError)
	}
	if _, present := firstDeploymentObject(t, reencoded)["keeping"]; present {
		t.Fatalf("a keeping key was invented for an answer that had none:\n%s", reencoded)
	}
}
