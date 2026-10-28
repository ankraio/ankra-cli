package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// BackupVault is one organisation-level object-storage backup target. Status
// is "provisioning" while the platform has not yet checked the credentials,
// "ready" once a verification succeeded, and "error" when the last check
// failed - ErrorExcerpt then carries the failure's first lines. Credential
// material is never returned.
type BackupVault struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	Provider       string  `json:"provider"`
	Endpoint       string  `json:"endpoint"`
	Region         string  `json:"region"`
	Bucket         string  `json:"bucket"`
	PathStyle      bool    `json:"path_style"`
	Status         string  `json:"status"`
	ErrorExcerpt   *string `json:"error_excerpt,omitempty"`
	LastVerifiedAt *string `json:"last_verified_at,omitempty"`
	// ProvisionedViaCredentialID is set on vaults the platform provisioned
	// from one of the organisation's provider credentials.
	ProvisionedViaCredentialID *string `json:"provisioned_via_credential_id,omitempty"`
	CreatedAt                  string  `json:"created_at"`
	UpdatedAt                  string  `json:"updated_at"`
}

// ProvisionBackupVaultRequest asks the platform to create the bucket itself
// from one of the organisation's provider credentials. The provider comes
// from the credential; Bucket is optional (the platform derives a unique
// name); the key pair is required for Hetzner only, whose Cloud API cannot
// mint Object Storage keys.
type ProvisionBackupVaultRequest struct {
	Name            string `json:"name"`
	CredentialID    string `json:"credential_id"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// BackupVaultListResult wraps the organisation's backup vault listing.
type BackupVaultListResult struct {
	Items []BackupVault `json:"items"`
}

// CreateBackupVaultRequest is the body for creating a backup vault. The
// platform verifies the credentials against the bucket synchronously, so the
// returned vault already carries the verification outcome in Status.
type CreateBackupVaultRequest struct {
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	PathStyle       bool   `json:"path_style"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// ListBackupVaults returns the organisation's backup vaults.
// GET /api/v1/org/backup-vaults
func (c *Client) ListBackupVaults() (*BackupVaultListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults", c.BaseURL)
	var result BackupVaultListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetBackupVault returns one backup vault by id.
// GET /api/v1/org/backup-vaults/{vault_id}
func (c *Client) GetBackupVault(vaultID string) (*BackupVault, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s", c.BaseURL, neturl.PathEscape(vaultID))
	var result BackupVault
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreateBackupVault creates a backup vault and returns it with the outcome
// of the platform's synchronous credential verification.
// POST /api/v1/org/backup-vaults
func (c *Client) CreateBackupVault(request CreateBackupVaultRequest) (*BackupVault, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults", c.BaseURL)
	var result BackupVault
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ProvisionBackupVault records an Ankra-provisioned vault and enqueues the
// platform job that creates the bucket. The returned vault is in
// "provisioning"; poll GetBackupVault for ready or error.
// POST /api/v1/org/backup-vaults/provision
func (c *Client) ProvisionBackupVault(request ProvisionBackupVaultRequest) (*BackupVault, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/provision", c.BaseURL)
	var result BackupVault
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// VerifyBackupVault re-runs the credential check against the vault's bucket
// and returns the vault with its refreshed status.
// POST /api/v1/org/backup-vaults/{vault_id}/verify
func (c *Client) VerifyBackupVault(vaultID string) (*BackupVault, error) {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/verify", c.BaseURL, neturl.PathEscape(vaultID))
	var result BackupVault
	if requestError := c.sendJSON(http.MethodPost, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DeleteBackupVault deletes one backup vault. With
// destroyProviderResources the platform also destroys what it created for
// an Ankra-provisioned vault - the bucket and everything in it, plus the
// provider handle - which it refuses for a bring-your-own vault.
// DELETE /api/v1/org/backup-vaults/{vault_id}
func (c *Client) DeleteBackupVault(vaultID string, destroyProviderResources bool) error {
	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s", c.BaseURL, neturl.PathEscape(vaultID))
	if destroyProviderResources {
		url += "?destroy_provider_resources=true"
	}
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}
