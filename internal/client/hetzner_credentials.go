package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

func (c *Client) ListHetznerCredentials() ([]HetznerCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/hetzner"
	var creds []HetznerCredentialListItem
	if err := c.getJSON(url, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (c *Client) CreateHetznerCredential(req CreateHetznerCredentialRequest) (*CreateHetznerCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/hetzner"
	return c.doCreateCredential(url, req)
}

func (c *Client) ListSSHKeyCredentials() ([]HetznerCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/hetzner/ssh-keys"
	var creds []HetznerCredentialListItem
	if err := c.getJSON(url, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (c *Client) CreateSSHKeyCredential(req CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/hetzner/ssh-key"
	return c.doCreateSSHKeyCredential(url, req)
}

func (c *Client) doCreateCredential(url string, reqBody interface{}) (*CreateHetznerCredentialResponse, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result CreateHetznerCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) doCreateSSHKeyCredential(url string, reqBody interface{}) (*CreateSSHKeyCredentialResponse, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result CreateSSHKeyCredentialResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}
