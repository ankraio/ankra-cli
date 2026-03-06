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

type UpcloudCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type CreateUpcloudCredentialRequest struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
}

type CreateUpcloudCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func ListUpcloudCredentials(token, baseURL string) ([]UpcloudCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/upcloud"
	var creds []UpcloudCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateUpcloudCredential(token, baseURL string, req CreateUpcloudCredentialRequest) (*CreateUpcloudCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/upcloud"
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

	var result CreateUpcloudCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ListUpcloudSSHKeyCredentials(token, baseURL string) ([]UpcloudCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/upcloud/ssh-keys"
	var creds []UpcloudCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateUpcloudSSHKeyCredential(token, baseURL string, req CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/upcloud/ssh-key"
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
