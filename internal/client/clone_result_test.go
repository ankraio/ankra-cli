package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// TestCloneStackToClusterDecodesApplicationsCloned pins the parity fix: the
// platform reports applications_cloned alongside the addon and manifest
// counts (cluster#1971) and the client used to drop it on decode, so a
// stack whose only member was an application read as if nothing had been
// cloned.
func TestCloneStackToClusterDecodesApplicationsCloned(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/target-1/stacks/clone" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id":            "draft-1",
			"stack_name":          "deploy-notes",
			"warnings":            []string{},
			"addons_cloned":       0,
			"manifests_cloned":    1,
			"applications_cloned": 1,
		})
	}
	testClient := newTestClient(t, handler)

	result, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1", CloneStackToClusterRequest{
		SourceClusterID: "source-1",
		StackName:       "deploy-notes",
	})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if result.ApplicationsCloned != 1 || result.ManifestsCloned != 1 || result.AddonsCloned != 0 {
		t.Fatalf("counts drifted: %+v", result)
	}
}

// TestCloneStackToClusterToleratesPlatformsWithoutApplicationCounts pins the
// forward-compatibility half: a platform that predates application cloning
// answers without the field, and the count reads as zero - which is also
// what such a platform cloned.
func TestCloneStackToClusterToleratesPlatformsWithoutApplicationCounts(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id":         "draft-1",
			"stack_name":       "deploy-notes",
			"warnings":         []string{},
			"addons_cloned":    2,
			"manifests_cloned": 1,
		})
	}
	testClient := newTestClient(t, handler)

	result, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1", CloneStackToClusterRequest{
		SourceClusterID: "source-1",
		StackName:       "deploy-notes",
	})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if result.ApplicationsCloned != 0 || result.AddonsCloned != 2 {
		t.Fatalf("counts drifted: %+v", result)
	}
}

// TestCloneStackToClusterSendsTheDataBlock pins the with-data wire shape
// (cluster#3101): the data fields the route reads, the optional
// Idempotency-Key header, and the selection sent only when one was asked
// for - the platform tells an absent selection from an empty one, and an
// empty one would be read as covering nothing.
func TestCloneStackToClusterSendsTheDataBlock(t *testing.T) {
	var sentBody map[string]any
	var sentIdempotencyKey string
	handler := func(w http.ResponseWriter, r *http.Request) {
		sentIdempotencyKey = r.Header.Get("Idempotency-Key")
		if decodeError := json.NewDecoder(r.Body).Decode(&sentBody); decodeError != nil {
			t.Fatalf("decoding the clone body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id": "draft-1", "stack_name": "shop",
			"data_clone_run_id": "run-1", "data_restore_point_id": "rp-1",
		})
	}
	testClient := newTestClient(t, handler)

	databasesExcluded := false
	_, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1", CloneStackToClusterRequest{
		SourceClusterID: "source-1",
		StackName:       "shop",
		IncludeData:     true,
		DataCloneMode:   CloneDataModeLatest,
		BackupVaultID:   "vault-1",
		DataSelection: &CloneDataSelection{
			Databases:              &databasesExcluded,
			PersistentVolumeClaims: []string{"shop/data"},
		},
		ProtectSource:  true,
		IdempotencyKey: "clone-shop-1",
	})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if sentIdempotencyKey != "clone-shop-1" {
		t.Errorf("the idempotency key must ride as a header, got %q", sentIdempotencyKey)
	}
	if sentBody["include_data"] != true || sentBody["data_clone_mode"] != "latest" ||
		sentBody["backup_vault_id"] != "vault-1" || sentBody["protect_source"] != true {
		t.Fatalf("the data block drifted: %+v", sentBody)
	}
	if _, carried := sentBody["idempotency_key"]; carried {
		t.Error("the idempotency key is a header, never a body field")
	}
	selection, isObject := sentBody["data_selection"].(map[string]any)
	if !isObject {
		t.Fatalf("the selection must be an object, got %T", sentBody["data_selection"])
	}
	if selection["databases"] != false {
		t.Errorf("an explicit exclusion must survive, got %v", selection["databases"])
	}
	claims, isList := selection["persistent_volume_claims"].([]any)
	if !isList || len(claims) != 1 || claims[0] != "shop/data" {
		t.Errorf("the named volume must survive, got %v", selection["persistent_volume_claims"])
	}
}

// A configuration-only clone must put exactly the bytes on the wire it did
// before the data lane existed, an absent selection included.
func TestCloneStackToClusterOmitsTheDataBlockWhenNoDataWasAsked(t *testing.T) {
	var sentBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("a clone with no key must send no Idempotency-Key header")
		}
		if decodeError := json.NewDecoder(r.Body).Decode(&sentBody); decodeError != nil {
			t.Fatalf("decoding the clone body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"draft_id": "draft-1", "stack_name": "shop"})
	}
	testClient := newTestClient(t, handler)

	if _, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1",
		CloneStackToClusterRequest{SourceClusterID: "source-1", StackName: "shop",
			IncludeAddonConfigurations: true}); cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	for _, field := range []string{"include_data", "data_clone_mode", "backup_vault_id",
		"data_selection", "protect_source", "deploy_after_clone"} {
		if _, carried := sentBody[field]; carried {
			t.Errorf("a configuration clone must not carry %q, got %+v", field, sentBody)
		}
	}
}

// The data half's result is what tells a caller whether the copy carries the
// data, so every field of it has to survive the decode.
func TestCloneStackToClusterDecodesTheDataResult(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id": "draft-1", "stack_name": "shop", "addons_cloned": 2,
			"data_clone_run_id": "run-1", "data_restore_point_id": "rp-1",
			"data_assets": []map[string]any{{
				"id": "velero:shop/data", "kind": "persistentvolumeclaim", "engine": "velero",
				"namespace": "shop", "name": "data", "size_bytes": 1024,
			}},
			"data_warnings": []string{"shop/cache: This volume is not in the backup selection."},
			"needs_input": []map[string]any{{
				"member_kind": "manifest", "member_name": "shop-secrets",
				"reason": "secrets_stripped", "paths": []string{"data.password"},
			}},
		})
	}
	testClient := newTestClient(t, handler)

	result, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1",
		CloneStackToClusterRequest{SourceClusterID: "source-1", StackName: "shop", IncludeData: true})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if result.DataCloneRunID == nil || *result.DataCloneRunID != "run-1" ||
		result.DataRestorePointID == nil || *result.DataRestorePointID != "rp-1" {
		t.Fatalf("the data identifiers drifted: %+v", result)
	}
	if len(result.DataAssets) != 1 || result.DataAssets[0].SizeBytes != 1024 ||
		result.DataAssets[0].Namespace != "shop" {
		t.Fatalf("the data assets drifted: %+v", result.DataAssets)
	}
	if len(result.DataWarnings) != 1 {
		t.Fatalf("the data warnings drifted: %+v", result.DataWarnings)
	}
	if len(result.NeedsInput) != 1 || result.NeedsInput[0].MemberName != "shop-secrets" {
		t.Fatalf("needs_input drifted: %+v", result.NeedsInput)
	}
}

// The data half refuses with sentences the caller has to read to act on, so
// the detail is the error message rather than a raw body dump.
func TestCloneStackToClusterRelaysTheRefusalDetail(t *testing.T) {
	refusal := "The target cluster already has a stack with this name, and a clone carrying data keeps the names."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": refusal})
	}
	testClient := newTestClient(t, handler)

	_, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1",
		CloneStackToClusterRequest{SourceClusterID: "source-1", StackName: "shop", IncludeData: true})
	if cloneError == nil {
		t.Fatal("a refused clone must be an error")
	}
	if cloneError.Error() != refusal {
		t.Fatalf("the platform's sentence must be the message, got %q", cloneError.Error())
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(cloneError, &unexpected) || unexpected.StatusCode != http.StatusConflict {
		t.Fatalf("the status must survive for the exit-code mapping, got %v", cloneError)
	}
}
