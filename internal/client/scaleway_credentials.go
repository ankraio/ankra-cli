package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type ScalewayCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CreateScalewayCredentialRequest mirrors enginekit/scalewayapi.Credential:
// a project-scoped IAM application key. The project id is not derivable from
// the key, so it is a required member rather than something the server can
// look up.
type CreateScalewayCredentialRequest struct {
	Name      string `json:"name"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	ProjectID string `json:"project_id"`
}

type CreateScalewayCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListScalewayCredentials() ([]ScalewayCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/scaleway"
	var credentials []ScalewayCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateScalewayCredential(createRequest CreateScalewayCredentialRequest) (*CreateScalewayCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/scaleway"
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

	var result CreateScalewayCredentialResponse
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) ListScalewaySSHKeyCredentials() ([]ScalewayCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/scaleway/ssh-keys"
	var credentials []ScalewayCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateScalewaySSHKeyCredential(createRequest CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/scaleway/ssh-key"
	return c.doCreateSSHKeyCredential(url, createRequest)
}
