package client

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
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

// BackupVaultImportUpload is one upload the CLI performs straight against
// the vault's bucket; the platform never sees the bytes. Method PUT carries
// one presigned URL for the whole object; Method multipart carries one
// presigned PUT per part of PartSizeBytes (the last one shorter) and the
// presigned calls that complete or abort the upload.
type BackupVaultImportUpload struct {
	Path          string                        `json:"path"`
	Method        string                        `json:"method"`
	URL           string                        `json:"url,omitempty"`
	ExpiresAt     string                        `json:"expires_at"`
	SizeBytes     int64                         `json:"size_bytes"`
	UploadID      string                        `json:"upload_id,omitempty"`
	PartSizeBytes int64                         `json:"part_size_bytes,omitempty"`
	Parts         []BackupVaultImportUploadPart `json:"parts,omitempty"`
	CompleteURL   string                        `json:"complete_url,omitempty"`
	AbortURL      string                        `json:"abort_url,omitempty"`
}

// BackupVaultImportUploadPart is one presigned PUT of a multipart upload.
type BackupVaultImportUploadPart struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
}

// Upload methods the platform mints.
const (
	BackupVaultImportUploadMethodPut       = "PUT"
	BackupVaultImportUploadMethodMultipart = "multipart"
)

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

// BackupVaultImportListResult is a vault's imports, newest first.
type BackupVaultImportListResult struct {
	Imports []BackupVaultImport `json:"imports"`
}

// ListBackupVaultImports reads a vault's imports without their live restore
// detail.
// GET /api/v1/org/backup-vaults/{vault_id}/imports
func (c *Client) ListBackupVaultImports(vaultID string) (*BackupVaultImportListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports", c.BaseURL, neturl.PathEscape(vaultID))
	var result BackupVaultImportListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DeleteBackupVaultImport removes an import's dumps from the vault and hides
// the import; the platform answers 409 while its restore is running.
// DELETE /api/v1/org/backup-vaults/{vault_id}/imports/{import_id}
func (c *Client) DeleteBackupVaultImport(vaultID string, importID string) error {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/imports/%s", c.BaseURL, neturl.PathEscape(vaultID), neturl.PathEscape(importID))
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// ImportArtifactMaximumBytes is the largest artifact an import accepts: the
// 10,000 parts of 64 MiB a multipart upload can carry. It mirrors the
// platform's limit so the CLI can refuse before registering anything.
const ImportArtifactMaximumBytes = int64(64<<20) * 10000

const presignedUploadAttempts = 3

// UploadBody is what an upload reads from: seekable, so a failed attempt
// restarts from the beginning, and addressable, so a multipart upload can
// send each part from its own offset. A file is both.
type UploadBody interface {
	io.ReadSeeker
	io.ReaderAt
}

// UploadPresignedObject sends one artifact to the vault as the platform
// described the upload: a single PUT, or a multipart upload part by part.
// The URLs are the whole credential, so no bearer token and no Ankra header
// is sent - the requests go through a plain transport, never the API
// client's. Every request carries its exact length because the vault
// rejects a chunked upload and the platform verifies the object's size
// afterwards. A transport failure or a 5xx is retried from the start of the
// body (or the part), which is why the body must seek; a refusal is final.
func (c *Client) UploadPresignedObject(ctx context.Context, upload BackupVaultImportUpload, body UploadBody, size int64) error {
	if size > ImportArtifactMaximumBytes {
		return fmt.Errorf("the artifact is %d bytes; an upload can carry at most %d", size, ImportArtifactMaximumBytes)
	}
	uploadClient := &http.Client{}
	if upload.Method == BackupVaultImportUploadMethodMultipart {
		return uploadMultipart(ctx, uploadClient, upload, body, size)
	}
	method := upload.Method
	if method == "" {
		method = http.MethodPut
	}
	_, putError := putPresigned(ctx, uploadClient, method, upload.URL, body, size)
	return putError
}

// putPresigned sends body to one presigned URL with retries and returns the
// ETag the store answered with, which a multipart completion needs.
func putPresigned(ctx context.Context, uploadClient *http.Client, method string, uploadURL string, body io.ReadSeeker, size int64) (string, error) {
	var lastError error
	for attempt := 1; attempt <= presignedUploadAttempts; attempt++ {
		if _, seekError := body.Seek(0, io.SeekStart); seekError != nil {
			return "", fmt.Errorf("rewind upload body: %w", seekError)
		}
		request, requestError := http.NewRequestWithContext(ctx, method, uploadURL, body)
		if requestError != nil {
			return "", fmt.Errorf("create upload request: %w", requestError)
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
				return "", lastError
			}
			continue
		}
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		closeBody(response)
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return response.Header.Get("ETag"), nil
		}
		lastError = newUnexpectedResponseError("upload", response.StatusCode, redactedBodyForError(responseBody, 500))
		if response.StatusCode < 500 {
			return "", lastError
		}
	}
	return "", lastError
}

// uploadMultipart sends the body part by part to the presigned part URLs,
// then completes the upload with the ETags the store handed back. Any
// failure aborts the upload so the store does not keep the parts.
func uploadMultipart(ctx context.Context, uploadClient *http.Client, upload BackupVaultImportUpload, body UploadBody, size int64) error {
	if upload.PartSizeBytes <= 0 || len(upload.Parts) == 0 || upload.CompleteURL == "" {
		return errors.New("the platform described a multipart upload without parts or a completion")
	}
	expectedParts := int((size + upload.PartSizeBytes - 1) / upload.PartSizeBytes)
	if expectedParts != len(upload.Parts) {
		return fmt.Errorf("the platform minted %d part(s) for an artifact that needs %d; run `ankra migrate export` again", len(upload.Parts), expectedParts)
	}
	completed := make([]multipartCompletedPart, 0, len(upload.Parts))
	for index, part := range upload.Parts {
		offset := int64(index) * upload.PartSizeBytes
		length := min(upload.PartSizeBytes, size-offset)
		section := io.NewSectionReader(body, offset, length)
		etag, putError := putPresigned(ctx, uploadClient, http.MethodPut, part.URL, section, length)
		if putError != nil {
			abortMultipart(ctx, uploadClient, upload.AbortURL)
			return fmt.Errorf("part %d of %d: %w", part.PartNumber, len(upload.Parts), putError)
		}
		completed = append(completed, multipartCompletedPart{PartNumber: part.PartNumber, ETag: etag})
	}
	if completeError := completeMultipart(ctx, uploadClient, upload.CompleteURL, completed); completeError != nil {
		abortMultipart(ctx, uploadClient, upload.AbortURL)
		return completeError
	}
	return nil
}

type multipartCompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartUploadRequest struct {
	XMLName xml.Name                 `xml:"CompleteMultipartUpload"`
	Parts   []multipartCompletedPart `xml:"Part"`
}

// completeMultipart posts the part list to the presigned completion. S3 may
// answer 200 with an error document when the assembly fails after the
// headers went out, so the body is read for one.
func completeMultipart(ctx context.Context, uploadClient *http.Client, completeURL string, parts []multipartCompletedPart) error {
	document, marshalError := xml.Marshal(completeMultipartUploadRequest{Parts: parts})
	if marshalError != nil {
		return fmt.Errorf("render the completion: %w", marshalError)
	}
	var lastError error
	for attempt := 1; attempt <= presignedUploadAttempts; attempt++ {
		request, requestError := http.NewRequestWithContext(ctx, http.MethodPost, completeURL, bytes.NewReader(document))
		if requestError != nil {
			return fmt.Errorf("create completion request: %w", requestError)
		}
		request.ContentLength = int64(len(document))
		request.Header.Set("Content-Type", "application/xml")
		response, doError := uploadClient.Do(request)
		if doError != nil {
			lastError = fmt.Errorf("completing the upload failed: %w", doError)
			if ctx.Err() != nil {
				return lastError
			}
			continue
		}
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		closeBody(response)
		switch {
		case response.StatusCode >= 200 && response.StatusCode < 300 && !bytes.Contains(responseBody, []byte("<Error>")):
			return nil
		case response.StatusCode >= 500 || bytes.Contains(responseBody, []byte("<Error>")) && response.StatusCode < 300:
			lastError = newUnexpectedResponseError("completing the upload", response.StatusCode, redactedBodyForError(responseBody, 500))
		default:
			return newUnexpectedResponseError("completing the upload", response.StatusCode, redactedBodyForError(responseBody, 500))
		}
	}
	return lastError
}

// abortMultipart tells the store to drop the parts of an upload that will
// never complete, best effort: the platform's completion check would have
// refused the object anyway, this just stops the parts costing storage.
func abortMultipart(ctx context.Context, uploadClient *http.Client, abortURL string) {
	if abortURL == "" {
		return
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodDelete, abortURL, nil)
	if requestError != nil {
		return
	}
	if response, doError := uploadClient.Do(request); doError == nil {
		closeBody(response)
	}
}
