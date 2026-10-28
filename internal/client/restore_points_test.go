package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const (
	testClusterUUID     = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"
	testRestorePointID  = "0b2f1c3d-4e5a-4b6c-8d7e-9f0a1b2c3d4e"
	testStackRestoreURL = "/api/v1/org/clusters/imported/" + testClusterUUID + "/stacks/shop/restore-points"
)

func TestListStackRestorePoints_AddressesTheBearerTwinAndCarriesEveryFilter(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != testStackRestoreURL {
			t.Errorf("path = %s", r.URL.Path)
		}
		query := r.URL.Query()
		if got := query["status"]; len(got) != 2 || got[0] != "complete" || got[1] != "failed" {
			t.Errorf("status = %v, want both values repeated", got)
		}
		if got := query.Get("trigger"); got != "scheduled" {
			t.Errorf("trigger = %q", got)
		}
		if got := query.Get("backup_vault_id"); got != "vault-1" {
			t.Errorf("backup_vault_id = %q", got)
		}
		if got := query.Get("limit"); got != "25" {
			t.Errorf("limit = %q", got)
		}
		if got := query.Get("cursor"); got != "opaque" {
			t.Errorf("cursor = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, RestorePointListResult{
			RestorePoints: []RestorePoint{{ID: testRestorePointID, Status: RestorePointStatusComplete}},
		})
	}
	testClient := newTestClient(t, handler)

	result, listError := testClient.ListStackRestorePoints(testClusterUUID, "shop", ListRestorePointsOptions{
		Statuses: []string{"complete", "failed"}, Triggers: []string{"scheduled"},
		VaultID: "vault-1", Cursor: "opaque", Limit: 25,
	})

	if listError != nil {
		t.Fatalf("ListStackRestorePoints: %v", listError)
	}
	if len(result.RestorePoints) != 1 || result.RestorePoints[0].ID != testRestorePointID {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListOrganisationRestorePoints_CarriesClusterAndStackAsQuery(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/restore-points" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("cluster_id"); got != testClusterUUID {
			t.Errorf("cluster_id = %q", got)
		}
		if got := r.URL.Query().Get("stack_name"); got != "shop" {
			t.Errorf("stack_name = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, RestorePointListResult{})
	}
	testClient := newTestClient(t, handler)

	if _, listError := testClient.ListOrganisationRestorePoints(ListRestorePointsOptions{
		ClusterID: testClusterUUID, StackName: "shop",
	}); listError != nil {
		t.Fatalf("ListOrganisationRestorePoints: %v", listError)
	}
}

func TestGetStackRestorePoint_DecodesTheManifestAndOmissions(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != testStackRestoreURL+"/"+testRestorePointID {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, RestorePoint{
			ID: testRestorePointID, Status: RestorePointStatusComplete, AssetCount: 1,
			Assets: []RestorePointAsset{{ID: "volume-shop-data", Kind: "volume", Engine: "velero"}},
			NotCarried: []RestorePointNotCarried{{
				Kind: "pvc", Name: "shop/cache", Reason: "Not selected.", Remedy: "Name it.",
			}},
			Manifest: &RestorePointManifest{SchemaVersion: 2, Warnings: []string{"sizes are unknown"}},
			Run:      &RestorePointRun{ID: "run-1", Kind: RunKindBackup, Status: RunStatusSucceeded},
		})
	}
	testClient := newTestClient(t, handler)

	restorePoint, getError := testClient.GetStackRestorePoint(testClusterUUID, "shop", testRestorePointID)

	if getError != nil {
		t.Fatalf("GetStackRestorePoint: %v", getError)
	}
	if restorePoint.Manifest == nil || restorePoint.Manifest.SchemaVersion != 2 {
		t.Fatalf("the manifest must survive: %+v", restorePoint.Manifest)
	}
	if len(restorePoint.NotCarried) != 1 || restorePoint.NotCarried[0].Remedy != "Name it." {
		t.Fatalf("the omissions must survive: %+v", restorePoint.NotCarried)
	}
	if restorePoint.Run == nil || restorePoint.Run.ID != "run-1" {
		t.Fatalf("the producing run must survive: %+v", restorePoint.Run)
	}
}

// Omitting the selection and sending an empty one are different answers to the
// platform, so a nil selection must not appear in the body at all.
func TestCreateStackRestorePoint_OmitsAnAbsentSelection(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding the request body: %v", decodeError)
		}
		if _, present := body["selection"]; present {
			t.Errorf("an absent selection must not be sent, got %+v", body)
		}
		if _, present := body["vault_id"]; present {
			t.Errorf("an unnamed vault must not be sent, got %+v", body)
		}
		jsonResponse(t, w, http.StatusAccepted, CreateRestorePointResult{
			RestorePointID: testRestorePointID, RunID: "run-1",
		})
	}
	testClient := newTestClient(t, handler)

	result, createError := testClient.CreateStackRestorePoint(testClusterUUID, "shop", CreateRestorePointRequest{})

	if createError != nil {
		t.Fatalf("CreateStackRestorePoint: %v", createError)
	}
	if result.RunID != "run-1" {
		t.Fatalf("the run to watch must survive: %+v", result)
	}
}

func TestCreateStackRestorePoint_SendsAnExplicitDatabaseExclusion(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Selection struct {
				Databases               *bool    `json:"databases"`
				PersistentVolumeClaims  []string `json:"persistent_volume_claims"`
				ConfirmExcludeDatabases bool     `json:"confirm_exclude_databases"`
			} `json:"selection"`
		}
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding the request body: %v", decodeError)
		}
		if body.Selection.Databases == nil || *body.Selection.Databases {
			t.Errorf("databases must be an explicit false, got %v", body.Selection.Databases)
		}
		if !body.Selection.ConfirmExcludeDatabases {
			t.Error("the acknowledgement must travel with the exclusion")
		}
		jsonResponse(t, w, http.StatusAccepted, CreateRestorePointResult{})
	}
	testClient := newTestClient(t, handler)

	excluded := false
	if _, createError := testClient.CreateStackRestorePoint(testClusterUUID, "shop", CreateRestorePointRequest{
		Selection: &RestorePointSelection{
			Databases: &excluded, PersistentVolumeClaims: []string{"shop/data"},
			ConfirmExcludeDatabases: true,
		},
	}); createError != nil {
		t.Fatalf("CreateStackRestorePoint: %v", createError)
	}
}

func TestDeleteStackRestorePoint_DecodesTheRetainedObjectsReason(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		jsonResponse(t, w, http.StatusAccepted, DeleteRestorePointResult{
			RestorePointID: testRestorePointID, Status: RestorePointStatusDeleted,
			ObjectsRetainedReason: "the source cluster no longer exists",
		})
	}
	testClient := newTestClient(t, handler)

	result, deleteError := testClient.DeleteStackRestorePoint(testClusterUUID, "shop", testRestorePointID)

	if deleteError != nil {
		t.Fatalf("DeleteStackRestorePoint: %v", deleteError)
	}
	if result.OperationID != nil {
		t.Errorf("no sweep was dispatched, so the operation must be null: %v", result.OperationID)
	}
	if result.ObjectsRetainedReason == "" {
		t.Error("a delete that left the objects behind must say why")
	}
}

func TestRestoreStackRestorePoint_PostsToTheRestoreSubresource(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != testStackRestoreURL+"/"+testRestorePointID+"/restore" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body RestoreRestorePointRequest
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding the request body: %v", decodeError)
		}
		if body.Mode != RestoreModeInPlace || body.Confirm != "shop" || !body.Force {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusAccepted, RestoreRestorePointResult{
			RestorePointID: testRestorePointID, RunID: "run-1", Mode: RestoreModeInPlace,
			Sequence: []string{"Scale down.", "Remove.", "Restore.", "Scale up."},
		})
	}
	testClient := newTestClient(t, handler)

	result, restoreError := testClient.RestoreStackRestorePoint(testClusterUUID, "shop", testRestorePointID,
		RestoreRestorePointRequest{Mode: RestoreModeInPlace, Confirm: "shop", Force: true})

	if restoreError != nil {
		t.Fatalf("RestoreStackRestorePoint: %v", restoreError)
	}
	if len(result.Sequence) != 4 {
		t.Fatalf("the sequence must survive: %+v", result.Sequence)
	}
}

// The platform refuses a cnpg or percona restore point with a sentence the CLI
// must relay rather than reword.
func TestRestoreStackRestorePoint_RelaysTheConflictDetail(t *testing.T) {
	const reason = "Restoring this restore point in place is not supported yet."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{"detail": reason})
	}
	testClient := newTestClient(t, handler)

	_, restoreError := testClient.RestoreStackRestorePoint(testClusterUUID, "shop", testRestorePointID,
		RestoreRestorePointRequest{Mode: RestoreModeInPlace, Confirm: "shop"})

	if restoreError == nil {
		t.Fatal("a 409 must be an error")
	}
	if restoreError.Error() != reason {
		t.Fatalf("the detail must be relayed verbatim, got %q", restoreError.Error())
	}
}

func TestProtectStack_PostsThePolicyAndDecodesTheBackupStackState(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/"+testClusterUUID+"/stacks/shop/protect" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body ProtectStackRequest
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding the request body: %v", decodeError)
		}
		if body.VaultID != "vault-1" || body.Schedule != "daily" || !body.BackupNow {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, StackProtection{
			StackName:   "shop",
			Policy:      BackupPolicy{Enabled: true, Vault: "production-backups", Schedule: "17 2 * * *"},
			BackupStack: BackupStackInstalling,
		})
	}
	testClient := newTestClient(t, handler)

	protection, protectError := testClient.ProtectStack(testClusterUUID, "shop", ProtectStackRequest{
		VaultID: "vault-1", Schedule: "daily", BackupNow: true,
	})

	if protectError != nil {
		t.Fatalf("ProtectStack: %v", protectError)
	}
	if protection.BackupStack != BackupStackInstalling {
		t.Fatalf("the backup stack state must survive: %+v", protection)
	}
}

// Every field-level reason is answered at once, and all of them must reach the
// caller: a refusal that showed only the first would cost a round-trip per
// mistake.
func TestProtectStack_CarriesEveryPolicyViolation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "The backup policy is not valid.",
			"violations": []map[string]string{
				{"key": "backup.schedule", "message": "'evry day' is neither a cron expression nor a keyword."},
				{"key": "backup.vault", "message": "This vault is not ready."},
			},
		})
	}
	testClient := newTestClient(t, handler)

	_, protectError := testClient.ProtectStack(testClusterUUID, "shop", ProtectStackRequest{VaultID: "vault-1"})

	var validation *BackupPolicyValidationError
	if !errors.As(protectError, &validation) {
		t.Fatalf("a 422 with violations must be a *BackupPolicyValidationError, got %T: %v", protectError, protectError)
	}
	if len(validation.Violations) != 2 {
		t.Fatalf("every violation must survive: %+v", validation.Violations)
	}
	if !strings.Contains(validation.Error(), "backup.schedule") ||
		!strings.Contains(validation.Error(), "backup.vault") {
		t.Fatalf("the message must name every field, got %q", validation.Error())
	}
}

// A 422 the route answers without violations - a malformed body, say - keeps
// its ordinary handling rather than becoming an empty policy refusal.
func TestProtectStack_LeavesAViolationlessRefusalAlone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnprocessableEntity,
			map[string]string{"detail": "Input should be a valid dictionary"})
	}
	testClient := newTestClient(t, handler)

	_, protectError := testClient.ProtectStack(testClusterUUID, "shop", ProtectStackRequest{VaultID: "vault-1"})

	var validation *BackupPolicyValidationError
	if errors.As(protectError, &validation) {
		t.Fatalf("a 422 with no violations is not a policy refusal, got %+v", validation)
	}
	if protectError == nil || protectError.Error() != "Input should be a valid dictionary" {
		t.Fatalf("the detail must be relayed, got %v", protectError)
	}
}

func TestProtectStack_RelaysTheFeatureFlagRefusal(t *testing.T) {
	const detail = "Backups are not enabled for this organisation."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{"detail": detail})
	}
	testClient := newTestClient(t, handler)

	_, protectError := testClient.ProtectStack(testClusterUUID, "shop", ProtectStackRequest{VaultID: "vault-1"})

	var unexpected *UnexpectedResponseError
	if !errors.As(protectError, &unexpected) {
		t.Fatalf("expected an *UnexpectedResponseError, got %T: %v", protectError, protectError)
	}
	if unexpected.StatusCode != http.StatusForbidden || unexpected.Error() != detail {
		t.Fatalf("the flag refusal must arrive intact, got %d %q", unexpected.StatusCode, unexpected.Error())
	}
}

func TestUnprotectStack_SendsTheTypedConfirmation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		var body UnprotectStackRequest
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding the request body: %v", decodeError)
		}
		if body.Confirm != "shop" {
			t.Errorf("confirm = %q, want the stack name", body.Confirm)
		}
		jsonResponse(t, w, http.StatusOK, StackProtection{
			StackName: "shop", Policy: BackupPolicy{Enabled: false},
			Warnings: []string{"Protection was removed from this stack."},
		})
	}
	testClient := newTestClient(t, handler)

	protection, unprotectError := testClient.UnprotectStack(testClusterUUID, "shop",
		UnprotectStackRequest{Confirm: "shop"})

	if unprotectError != nil {
		t.Fatalf("UnprotectStack: %v", unprotectError)
	}
	if protection.Policy.Enabled || len(protection.Warnings) != 1 {
		t.Fatalf("unexpected protection: %+v", protection)
	}
}

func TestGetStackDataAssets_ReadsTheInventory(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/clusters/imported/"+testClusterUUID+"/stacks/shop/data-assets" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, StackDataInventory{
			StackName: "shop", Namespaces: []string{"shop"},
			Assets:              []StackDataAsset{{Kind: "cnpg_cluster", Engine: "cnpg", Name: "orders"}},
			TotalRequestedBytes: 1024, CustomResourceScan: CustomResourceScanUnavailable,
		})
	}
	testClient := newTestClient(t, handler)

	inventory, inventoryError := testClient.GetStackDataAssets(testClusterUUID, "shop")

	if inventoryError != nil {
		t.Fatalf("GetStackDataAssets: %v", inventoryError)
	}
	if inventory.CustomResourceScan != CustomResourceScanUnavailable || len(inventory.Assets) != 1 {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
}
