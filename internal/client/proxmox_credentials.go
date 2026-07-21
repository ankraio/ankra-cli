package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type ProxmoxCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CredentialJumphost is the optional SSH jumphost member shared by the
// Proxmox VE and HPE Morpheus credential create requests. The server
// defaults port to 22 and username to "root" when omitted.
type CredentialJumphost struct {
	Host       string `json:"host"`
	Port       int    `json:"port,omitempty"`
	Username   string `json:"username,omitempty"`
	PrivateKey string `json:"private_key"`
}

type CreateProxmoxCredentialRequest struct {
	Name        string              `json:"name"`
	APIURL      string              `json:"api_url"`
	TokenID     string              `json:"token_id"`
	TokenSecret string              `json:"token_secret"`
	TLSInsecure bool                `json:"tls_insecure"`
	Jumphost    *CredentialJumphost `json:"jumphost,omitempty"`
}

type CreateProxmoxCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListProxmoxCredentials() ([]ProxmoxCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox"
	var credentials []ProxmoxCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateProxmoxCredential(createRequest CreateProxmoxCredentialRequest) (*CreateProxmoxCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox"
	payload, marshalError := json.Marshal(createRequest)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	httpResponse, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(httpResponse)

	body, readError := readResponseBody(httpResponse)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("create failed", httpResponse.StatusCode, redactedBodyForError(body, 500))
	}

	var result CreateProxmoxCredentialResponse
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) ListProxmoxSSHKeyCredentials() ([]ProxmoxCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox/ssh-keys"
	var credentials []ProxmoxCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateProxmoxSSHKeyCredential(createRequest CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox/ssh-key"
	return c.doCreateSSHKeyCredential(url, createRequest)
}
