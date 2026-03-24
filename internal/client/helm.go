package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type HelmRegistryListItem struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	URL       string  `json:"url"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt *string `json:"updated_at"`
}

type ListHelmRegistriesResponse struct {
	Result     []HelmRegistryListItem `json:"result"`
	Pagination Pagination             `json:"pagination"`
}

type GetHelmRegistryResponse struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	URL       string      `json:"url"`
	Status    string      `json:"status"`
	Charts    interface{} `json:"charts"`
	CreatedAt string      `json:"created_at"`
}

type CreateHelmRegistryRequest struct {
	Spec json.RawMessage `json:"spec"`
}

type CreateHelmRegistryResponse struct {
	Errors []ResourceError `json:"errors"`
}

type DeleteHelmRegistryResponse struct {
	Success bool `json:"success"`
}

func (c *Client) ListHelmRegistries() (*ListHelmRegistriesResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries", c.BaseURL)
	var response ListHelmRegistriesResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetHelmRegistry(registryName string) (*GetHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s", c.BaseURL, url.PathEscape(registryName))
	var response GetHelmRegistryResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CreateHelmRegistry(req CreateHelmRegistryRequest) (*CreateHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries", c.BaseURL)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
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

	var response CreateHelmRegistryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

func (c *Client) DeleteHelmRegistry(registryName string) (*DeleteHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s", c.BaseURL, url.PathEscape(registryName))
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
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
		return nil, fmt.Errorf("delete failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var response DeleteHelmRegistryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

type HelmCredentialListItem struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type ListHelmCredentialsResponse struct {
	Result     []HelmCredentialListItem `json:"result"`
	Pagination Pagination               `json:"pagination"`
}

type CreateHelmCredentialRequest struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type CreateHelmCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors"`
}

type DeleteHelmCredentialResponse struct {
	Success bool `json:"success"`
}

func (c *Client) ListHelmRegistryCredentials() (*ListHelmCredentialsResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials", c.BaseURL)
	var response ListHelmCredentialsResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CreateHelmRegistryCredential(req CreateHelmCredentialRequest) (*CreateHelmCredentialResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials", c.BaseURL)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
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

	var response CreateHelmCredentialResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

func (c *Client) DeleteHelmRegistryCredential(credentialName string) (*DeleteHelmCredentialResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials/%s", c.BaseURL, url.PathEscape(credentialName))
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
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
		return nil, fmt.Errorf("delete failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var response DeleteHelmCredentialResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}
