package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Credential represents a credential in the system
type Credential struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Provider     string  `json:"provider"`
	Description  *string `json:"description,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    *string `json:"updated_at,omitempty"`
	LastUsedAt   *string `json:"last_used_at,omitempty"`
	UsageCount   int     `json:"usage_count"`
	ClusterCount int     `json:"cluster_count"`
}

// CredentialValidationResult is the response from validating a credential name
type CredentialValidationResult struct {
	Valid   bool    `json:"valid"`
	Message *string `json:"message,omitempty"`
}

// CredentialDetail represents detailed credential information
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

// DeleteCredentialResult is the response from deleting a credential
type DeleteCredentialResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ListCredentials returns all credentials for the organisation
func ListCredentials(token, baseURL string, provider *string) ([]Credential, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/credentials"
	if provider != nil && *provider != "" {
		url = url + "?provider=" + *provider
	}
	var creds []Credential
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// GetCredential returns details of a specific credential
func GetCredential(token, baseURL, credentialID string) (*CredentialDetail, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/%s", strings.TrimRight(baseURL, "/"), credentialID)
	var cred CredentialDetail
	if err := getJSON(url, token, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

// ValidateCredentialName checks if a credential name is valid/available
func ValidateCredentialName(token, baseURL, name string) (*CredentialValidationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/validate?credential_name=%s", strings.TrimRight(baseURL, "/"), name)
	var result CredentialValidationResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteCredential deletes a credential by ID
func DeleteCredential(ctx context.Context, token, baseURL, credentialID, organisationID string) (*DeleteCredentialResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/credentials/%s?organisation_id=%s",
		strings.TrimRight(baseURL, "/"), credentialID, organisationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &DeleteCredentialResult{Success: true, Message: "Credential deleted"}, nil
}
