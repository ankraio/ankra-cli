package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestListBackupVaults_Success(t *testing.T) {
	lastVerifiedAt := "2026-08-25T09:00:00Z"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/backup-vaults" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, BackupVaultListResult{Items: []BackupVault{
			{ID: "vault-1", Name: "offsite", Provider: "other", Endpoint: "https://s3.example.com",
				Bucket: "cluster-backups", PathStyle: true, Status: "ready", LastVerifiedAt: &lastVerifiedAt},
		}})
	}
	testClient := newTestClient(t, handler)
	result, listError := testClient.ListBackupVaults()
	if listError != nil {
		t.Fatalf("ListBackupVaults: %v", listError)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "offsite" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Items[0].LastVerifiedAt == nil || *result.Items[0].LastVerifiedAt != lastVerifiedAt {
		t.Errorf("LastVerifiedAt = %v, want %s", result.Items[0].LastVerifiedAt, lastVerifiedAt)
	}
}

func TestListBackupVaults_Empty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, BackupVaultListResult{Items: []BackupVault{}})
	}
	testClient := newTestClient(t, handler)
	result, listError := testClient.ListBackupVaults()
	if listError != nil {
		t.Fatalf("ListBackupVaults: %v", listError)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected an empty listing, got: %+v", result)
	}
}

func TestCreateBackupVault_SendsBodyAndDecodesVault(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/backup-vaults" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decoding body: %v", decodeError)
		}
		if body["name"] != "offsite" || body["provider"] != "other" ||
			body["endpoint"] != "https://s3.example.com" || body["region"] != "" ||
			body["bucket"] != "cluster-backups" || body["path_style"] != true ||
			body["access_key_id"] != "AKIA123" || body["secret_access_key"] != "shhh" {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, BackupVault{
			ID: "vault-new", Name: "offsite", Provider: "other", Status: "ready"})
	}
	testClient := newTestClient(t, handler)
	vault, createError := testClient.CreateBackupVault(CreateBackupVaultRequest{
		Name: "offsite", Provider: "other", Endpoint: "https://s3.example.com",
		Bucket: "cluster-backups", PathStyle: true,
		AccessKeyID: "AKIA123", SecretAccessKey: "shhh",
	})
	if createError != nil {
		t.Fatalf("CreateBackupVault: %v", createError)
	}
	if vault.ID != "vault-new" || vault.Status != "ready" {
		t.Fatalf("unexpected result: %+v", vault)
	}
}

func TestCreateBackupVault_ValidationDetailSurfaces(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{
			"detail": "A backup vault named 'offsite' already exists."})
	}
	testClient := newTestClient(t, handler)
	_, createError := testClient.CreateBackupVault(CreateBackupVaultRequest{Name: "offsite"})
	if createError == nil || !strings.Contains(createError.Error(), "already exists") {
		t.Fatalf("expected the backend detail to surface, got %v", createError)
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(createError, &unexpected) || unexpected.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected an UnexpectedResponseError carrying 400, got %v", createError)
	}
}

func TestCreateBackupVault_PermissionDeniedMapsToTypedError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{
			"detail": "permission_denied", "permission": "backups.manage"})
	}
	testClient := newTestClient(t, handler)
	_, createError := testClient.CreateBackupVault(CreateBackupVaultRequest{Name: "offsite"})
	var denied *PermissionDeniedError
	if !errors.As(createError, &denied) {
		t.Fatalf("expected a PermissionDeniedError, got %v", createError)
	}
	if denied.Permission != "backups.manage" {
		t.Errorf("Permission = %q, want backups.manage", denied.Permission)
	}
}

func TestGetBackupVault_ReturnsErrorExcerpt(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/backup-vaults/vault-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BackupVault{
			ID: "vault-1", Name: "offsite", Status: "error",
			ErrorExcerpt: strPtr("SignatureDoesNotMatch: the request signature we calculated does not match")})
	}
	testClient := newTestClient(t, handler)
	vault, getError := testClient.GetBackupVault("vault-1")
	if getError != nil {
		t.Fatalf("GetBackupVault: %v", getError)
	}
	if vault.Status != "error" || vault.ErrorExcerpt == nil ||
		!strings.Contains(*vault.ErrorExcerpt, "SignatureDoesNotMatch") {
		t.Fatalf("unexpected result: %+v", vault)
	}
}

func TestGetBackupVault_NotFoundCarries404(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{
			"detail": "Backup vault not found."})
	}
	testClient := newTestClient(t, handler)
	_, getError := testClient.GetBackupVault("vault-missing")
	if getError == nil || !strings.Contains(getError.Error(), "Backup vault not found.") {
		t.Fatalf("expected the backend detail to surface, got %v", getError)
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(getError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected an UnexpectedResponseError carrying 404, got %v", getError)
	}
}

func TestVerifyBackupVault_PostsAndDecodesRefreshedStatus(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/backup-vaults/vault-1/verify" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BackupVault{ID: "vault-1", Name: "offsite", Status: "ready"})
	}
	testClient := newTestClient(t, handler)
	vault, verifyError := testClient.VerifyBackupVault("vault-1")
	if verifyError != nil {
		t.Fatalf("VerifyBackupVault: %v", verifyError)
	}
	if vault.Status != "ready" {
		t.Fatalf("unexpected result: %+v", vault)
	}
}

func TestDeleteBackupVault_AcceptsNoContent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/org/backup-vaults/vault-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	testClient := newTestClient(t, handler)
	if deleteError := testClient.DeleteBackupVault("vault-1"); deleteError != nil {
		t.Fatalf("DeleteBackupVault: %v", deleteError)
	}
}

func TestDeleteBackupVault_NotFoundCarries404(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{
			"detail": "Backup vault not found."})
	}
	testClient := newTestClient(t, handler)
	deleteError := testClient.DeleteBackupVault("vault-missing")
	var unexpected *UnexpectedResponseError
	if !errors.As(deleteError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected an UnexpectedResponseError carrying 404, got %v", deleteError)
	}
}
