package client

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// Credential mirrors cluster-2.0's ListCredentialResponseListItem
// from src/usecase/credentials_v2/list_credentials.py. The legacy
// `cluster_count` and `usage_count` fields the CLI used to render were
// removed in the v2 list; they are kept here only for backwards
// compatibility against older platform versions.
type Credential struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	Provider            string                    `json:"provider"`
	OrganisationID      string                    `json:"organisation_id"`
	System              bool                      `json:"system"`
	Available           bool                      `json:"available"`
	State               *string                   `json:"state,omitempty"`
	CreatedAt           string                    `json:"created_at"`
	UpdatedAt           *string                   `json:"updated_at,omitempty"`
	AccountLogin        *string                   `json:"account_login,omitempty"`
	AccountType         *string                   `json:"account_type,omitempty"`
	InstallationID      *int                      `json:"installation_id,omitempty"`
	RepositorySelection *string                   `json:"repository_selection,omitempty"`
	CoTenantCount       *int                      `json:"co_tenant_count,omitempty"`
	LastSyncedAt        *string                   `json:"last_synced_at,omitempty"`
	Syncing             bool                      `json:"syncing"`
	RepositoryCount     *int                      `json:"repository_count,omitempty"`
	Health              *CredentialHealthSummary  `json:"health,omitempty"`

	// Legacy fields kept for old responses; preferring backend values
	// where available.
	Description  *string `json:"description,omitempty"`
	LastUsedAt   *string `json:"last_used_at,omitempty"`
	UsageCount   int     `json:"usage_count"`
	ClusterCount int     `json:"cluster_count"`
}

// CredentialHealthSummary mirrors cluster-2.0's CredentialHealthSummary.
type CredentialHealthSummary struct {
	Status      string  `json:"status"`
	Message     *string `json:"message,omitempty"`
	LastChecked *string `json:"last_checked_at,omitempty"`
}

type CredentialValidationResult struct {
	Valid   bool    `json:"valid"`
	Message *string `json:"message,omitempty"`
}

type CredentialDetail struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Provider        string  `json:"provider"`
	Description     *string `json:"description,omitempty"`
	CreatedAt       string  `json:"created_at"`
	OrganisationID  string  `json:"organisation_id"`
	InstallationID  *string `json:"installation_id,omitempty"`
	Repository      *string `json:"repository,omitempty"`
	Owner           *string `json:"owner,omitempty"`
}

type DeleteCredentialResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) ListCredentials(provider *string) ([]Credential, error) {
	url := c.BaseURL + "/api/v1/org/credentials"
	if provider != nil && *provider != "" {
		url = url + "?provider=" + neturl.QueryEscape(*provider)
	}
	var creds []Credential
	if err := c.getJSON(url, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (c *Client) GetCredential(credentialID string) (*CredentialDetail, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/%s", c.BaseURL, neturl.PathEscape(credentialID))
	var cred CredentialDetail
	if err := c.getJSON(url, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

func (c *Client) ValidateCredentialName(name string) (*CredentialValidationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/validate?credential_name=%s", c.BaseURL, neturl.QueryEscape(name))
	var result CredentialValidationResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteCredential(ctx context.Context, credentialID, organisationID string) (*DeleteCredentialResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/%s?organisation_id=%s",
		c.BaseURL, neturl.PathEscape(credentialID), neturl.QueryEscape(organisationID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &DeleteCredentialResult{Success: true, Message: "Credential deleted"}, nil
}
