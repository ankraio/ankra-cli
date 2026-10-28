package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// AnkraCloudDefaultEndpoint is the Ankra Cloud API the platform calls when a
// credential names no endpoint of its own.
const AnkraCloudDefaultEndpoint = "https://cloud.ankra.app"

// CreateAnkraCloudCredentialRequest is the POST /credentials/ankracloud body:
// an Ankra Cloud API token (act_...) and, for a non-default installation,
// the https endpoint it belongs to. One stored credential serves both the
// self-managed ankracloud clusters and managed ankracloud_k8s clusters.
type CreateAnkraCloudCredentialRequest struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
	Endpoint string `json:"endpoint,omitempty"`
}

type CreateAnkraCloudCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListAnkraCloudCredentials() ([]Credential, error) {
	endpoint := c.BaseURL + "/api/v1/credentials/ankracloud"
	var credentials []Credential
	if listError := c.getJSON(endpoint, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateAnkraCloudCredential(createRequest CreateAnkraCloudCredentialRequest) (*CreateAnkraCloudCredentialResponse, error) {
	endpoint := c.BaseURL + "/api/v1/credentials/ankracloud"
	payload, marshalError := json.Marshal(createRequest)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
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

	var result CreateAnkraCloudCredentialResponse
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}
