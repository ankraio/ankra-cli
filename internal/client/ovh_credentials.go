package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type OvhCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type CreateOvhCredentialRequest struct {
	Name              string `json:"name"`
	ApplicationKey    string `json:"application_key"`
	ApplicationSecret string `json:"application_secret"`
	ConsumerKey       string `json:"consumer_key"`
	ProjectID         string `json:"project_id"`
}

type CreateOvhCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func ListOvhCredentials(token, baseURL string) ([]OvhCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/ovh"
	var creds []OvhCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateOvhCredential(token, baseURL string, req CreateOvhCredentialRequest) (*CreateOvhCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/ovh"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result CreateOvhCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ListOvhSSHKeyCredentials(token, baseURL string) ([]OvhCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/ovh/ssh-keys"
	var creds []OvhCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateOvhSSHKeyCredential(token, baseURL string, req CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/ovh/ssh-key"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result CreateSSHKeyCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}
