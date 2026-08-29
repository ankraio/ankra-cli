package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
)

// BackupVaultImportTarget is the in-cluster server one database restores
// into: the Service and Secret `ankra migrate convert` generated for it.
type BackupVaultImportTarget struct {
	Namespace      string `json:"namespace"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username,omitempty"`
	PasswordSecret string `json:"password_secret,omitempty"`
	PasswordKey    string `json:"password_key,omitempty"`
}

// BackupVaultImportArtifact is one uploaded dump as the export described it.
type BackupVaultImportArtifact struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Format    string `json:"format"`
	Database  string `json:"database,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// BackupVaultImportDatabase is one database server's dumps and target.
type BackupVaultImportDatabase struct {
	Workload      string                      `json:"workload"`
	Engine        string                      `json:"engine"`
	ServerVersion string                      `json:"server_version,omitempty"`
	Target        BackupVaultImportTarget     `json:"target"`
	Artifacts     []BackupVaultImportArtifact `json:"artifacts"`
}

// BackupVaultImportRestoreStep is one restore job's state as the platform
// last observed it.
type BackupVaultImportRestoreStep struct {
	StepID       string `json:"step_id"`
	Workload     string `json:"workload"`
	Status       string `json:"status"`
	ErrorExcerpt string `json:"error_excerpt,omitempty"`
}

// BackupVaultImportRestore is the restore operation an import ran or is
// running: one step per database server.
type BackupVaultImportRestore struct {
	OperationID string                         `json:"operation_id"`
	Status      string                         `json:"status"`
	Steps       []BackupVaultImportRestoreStep `json:"steps"`
}

// BackupVaultImport is an `ankra migrate export` directory on its way into a
// cluster through a backup vault. Status runs uploading -> uploaded ->
// restoring -> completed | failed.
type BackupVaultImport struct {
	ID            string                      `json:"id"`
	BackupVaultID string                      `json:"backup_vault_id"`
	ClusterID     string                      `json:"cluster_id"`
	StackName     string                      `json:"stack_name"`
	Status        string                      `json:"status"`
	ObjectPrefix  string                      `json:"object_prefix"`
	Databases     []BackupVaultImportDatabase `json:"databases"`
	Warnings      []string                    `json:"warnings"`
	Restore       *BackupVaultImportRestore   `json:"restore,omitempty"`
	ErrorExcerpt  *string                     `json:"error_excerpt"`
	CreatedAt     string                      `json:"created_at"`
	UpdatedAt     string                      `json:"updated_at"`
	CompletedAt   *string                     `json:"completed_at"`
}

// Import statuses on the wire.
const (
	BackupVaultImportStatusUploading = "uploading"
	BackupVaultImportStatusUploaded  = "uploaded"
	BackupVaultImportStatusRestoring = "restoring"
	BackupVaultImportStatusCompleted = "completed"
	BackupVaultImportStatusFailed    = "failed"
)

// BackupVaultImportUpload is one presigned PUT the CLI performs straight
// against the vault's bucket; the platform never sees the bytes.
type BackupVaultImportUpload struct {
	Path      string `json:"path"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	SizeBytes int64  `json:"size_bytes"`
}

// CreateBackupVaultImportRequest registers an export: the cluster its data
// goes to and the export's manifest.json, passed through verbatim.
type CreateBackupVaultImportRequest struct {
	ClusterID string          `json:"cluster_id"`
	StackName string          `json:"stack_name,omitempty"`
	Manifest  json.RawMessage `json:"manifest"`
}

// CreateBackupVaultImportResult is the registered import plus its uploads.
type CreateBackupVaultImportResult struct {
	Import  BackupVaultImport         `json:"import"`
	Uploads []BackupVaultImportUpload `json:"uploads"`
}

// CreateBackupVaultImport registers an export and returns the presigned
// uploads for its artifacts.
// POST /api/v1/org/backup-vaults/{vault_id}/imports
func (c *Client) CreateBackupVaultImport(vaultID string, request CreateBackupVaultImportRequest) (*CreateBackupVaultImportResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports", c.BaseURL, neturl.PathEscape(vaultID))
	var result CreateBackupVaultImportResult
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CompleteBackupVaultImport asks the platform to verify every artifact is in
// the bucket at its recorded size.
// POST /api/v1/org/backup-vaults/{vault_id}/imports/{import_id}/complete
func (c *Client) CompleteBackupVaultImport(vaultID string, importID string) (*BackupVaultImport, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports/%s/complete", c.BaseURL, neturl.PathEscape(vaultID), neturl.PathEscape(importID))
	var result BackupVaultImport
	if requestError := c.sendJSON(http.MethodPost, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RestoreBackupVaultImport dispatches the in-cluster restore of an uploaded
// import; the platform answers 202 with the import in restoring.
// POST /api/v1/org/backup-vaults/{vault_id}/imports/{import_id}/restore
func (c *Client) RestoreBackupVaultImport(vaultID string, importID string) (*BackupVaultImport, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports/%s/restore", c.BaseURL, neturl.PathEscape(vaultID), neturl.PathEscape(importID))
	var result BackupVaultImport
	if requestError := c.sendJSON(http.MethodPost, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetBackupVaultImport reads an import with its restore's live state.
// GET /api/v1/org/backup-vaults/{vault_id}/imports/{import_id}
func (c *Client) GetBackupVaultImport(vaultID string, importID string) (*BackupVaultImport, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports/%s", c.BaseURL, neturl.PathEscape(vaultID), neturl.PathEscape(importID))
	var result BackupVaultImport
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// PresignedUploadMaximumBytes is the largest object a single presigned PUT
// can carry on S3 and every compatible store; a bigger artifact needs a
// multipart upload the platform does not mint yet.
const PresignedUploadMaximumBytes = 5 << 30

const presignedUploadAttempts = 3

// UploadPresignedObject sends one artifact to a presigned URL. The URL is the
// whole credential, so no bearer token and no Ankra header is sent - the
// request goes through a plain transport, never the API client's - and the
// body carries its exact size because the vault rejects a chunked upload and
// the platform verifies the object at that size afterwards. A dump can be
// large, so the upload has no per-request timeout beyond ctx; a transport
// failure or a 5xx is retried from the start of the body, and a redirect
// replays it, which is why the body must seek.
func (c *Client) UploadPresignedObject(ctx context.Context, method string, uploadURL string, body io.ReadSeeker, size int64) error {
	if method == "" {
		method = http.MethodPut
	}
	if size > PresignedUploadMaximumBytes {
		return fmt.Errorf("the artifact is %d bytes; a single upload can carry at most %d", size, PresignedUploadMaximumBytes)
	}
	uploadClient := &http.Client{}
	var lastError error
	for attempt := 1; attempt <= presignedUploadAttempts; attempt++ {
		if _, seekError := body.Seek(0, io.SeekStart); seekError != nil {
			return fmt.Errorf("rewind upload body: %w", seekError)
		}
		request, requestError := http.NewRequestWithContext(ctx, method, uploadURL, body)
		if requestError != nil {
			return fmt.Errorf("create upload request: %w", requestError)
		}
		request.ContentLength = size
		request.Header.Set("Content-Type", "application/octet-stream")
		request.GetBody = func() (io.ReadCloser, error) {
			if _, seekError := body.Seek(0, io.SeekStart); seekError != nil {
				return nil, seekError
			}
			return io.NopCloser(body), nil
		}

		response, doError := uploadClient.Do(request)
		if doError != nil {
			lastError = fmt.Errorf("upload failed: %w", doError)
			if ctx.Err() != nil {
				return lastError
			}
			continue
		}
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		closeBody(response)
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
		lastError = newUnexpectedResponseError("upload", response.StatusCode, redactedBodyForError(responseBody, 500))
		if response.StatusCode < 500 {
			return lastError
		}
	}
	return lastError
}
