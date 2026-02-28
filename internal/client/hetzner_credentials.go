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

type HetznerCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type CreateHetznerCredentialRequest struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
}

type ResourceError struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

type CreateHetznerCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

type CreateSSHKeyCredentialRequest struct {
	Name         string  `json:"name"`
	SSHPublicKey *string `json:"ssh_public_key,omitempty"`
	Generate     bool    `json:"generate"`
}

type CreateSSHKeyCredentialResponse struct {
	Success    bool            `json:"success"`
	PrivateKey *string         `json:"private_key,omitempty"`
	Errors     []ResourceError `json:"errors,omitempty"`
}

func ListHetznerCredentials(token, baseURL string) ([]HetznerCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/hetzner"
	var creds []HetznerCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateHetznerCredential(token, baseURL string, req CreateHetznerCredentialRequest) (*CreateHetznerCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/hetzner"
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

	var result CreateHetznerCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ListSSHKeyCredentials(token, baseURL string) ([]HetznerCredentialListItem, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/hetzner/ssh-keys"
	var creds []HetznerCredentialListItem
	if err := getJSON(url, token, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateSSHKeyCredential(token, baseURL string, req CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/credentials/hetzner/ssh-key"
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
