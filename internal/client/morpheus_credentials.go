package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type MorpheusCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type CreateMorpheusCredentialRequest struct {
	Name        string              `json:"name"`
	APIURL      string              `json:"api_url"`
	AccessToken string              `json:"access_token"`
	TLSInsecure bool                `json:"tls_insecure"`
	Jumphost    *CredentialJumphost `json:"jumphost,omitempty"`
}

type CreateMorpheusCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListMorpheusCredentials() ([]MorpheusCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/morpheus"
	var credentials []MorpheusCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateMorpheusCredential(createRequest CreateMorpheusCredentialRequest) (*CreateMorpheusCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/morpheus"
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

	var result CreateMorpheusCredentialResponse
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) ListMorpheusSSHKeyCredentials() ([]MorpheusCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/morpheus/ssh-keys"
	var credentials []MorpheusCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateMorpheusSSHKeyCredential(createRequest CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/morpheus/ssh-key"
	return c.doCreateSSHKeyCredential(url, createRequest)
}
