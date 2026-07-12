package client

import (
	"fmt"
	"net/http"
)

// AI provider settings and the custom model catalog, reachable over the
// bearer-PAT /api/v1/org/ai-settings routes (the CLI twins of the browser
// /org/ai-settings surface). Mutations require the ai.manage permission; the
// status read and the model listing are member-level.

// AIAnthropicStatus mirrors the anthropic block of the provider status.
type AIAnthropicStatus struct {
	Configured bool    `json:"configured"`
	KeyPreview *string `json:"key_preview"`
}

// AIOpenAICompatibleStatus mirrors the openai_compatible block of the provider
// status (the legacy single-endpoint config).
type AIOpenAICompatibleStatus struct {
	Configured bool    `json:"configured"`
	BaseURL    *string `json:"base_url"`
	Model      *string `json:"model"`
	KeyPreview *string `json:"key_preview"`
}

// AIProviderStatus is the aggregated AI provider status.
type AIProviderStatus struct {
	Provider         string                   `json:"provider"`
	Anthropic        AIAnthropicStatus        `json:"anthropic"`
	OpenAICompatible AIOpenAICompatibleStatus `json:"openai_compatible"`
}

// AICatalogModel is one entry in the organisation model catalog. ID is null
// for a virtual default that has not been materialised; EndpointID is set for a
// model routed through a custom OpenAI-compatible endpoint.
type AICatalogModel struct {
	ID                  *string `json:"id"`
	Key                 string  `json:"key"`
	DisplayName         string  `json:"display_name"`
	Description         string  `json:"description"`
	Provider            string  `json:"provider"`
	EndpointID          *string `json:"endpoint_id"`
	ModelID             string  `json:"model_id"`
	Tier                *string `json:"tier"`
	ContextWindowTokens int     `json:"context_window_tokens"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	SupportsTools       bool    `json:"supports_tools"`
	SupportsThinking    bool    `json:"supports_thinking"`
	SupportsImages      bool    `json:"supports_images"`
	IsEnabled           bool    `json:"is_enabled"`
	SortOrder           int     `json:"sort_order"`
	IsDefault           bool    `json:"is_default"`
}

// AIModelRequest is the create/update body for a catalog entry.
type AIModelRequest struct {
	Key                 string  `json:"key"`
	DisplayName         string  `json:"display_name"`
	Description         string  `json:"description"`
	EndpointID          *string `json:"endpoint_id,omitempty"`
	ModelID             string  `json:"model_id"`
	Tier                string  `json:"tier"`
	ContextWindowTokens int     `json:"context_window_tokens"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	SupportsTools       bool    `json:"supports_tools"`
	SupportsThinking    bool    `json:"supports_thinking"`
	SupportsImages      bool    `json:"supports_images"`
	IsEnabled           bool    `json:"is_enabled"`
	SortOrder           int     `json:"sort_order"`
}

// AIEndpoint is one configured OpenAI-compatible provider connection. The raw
// key never leaves the server; only the masked preview is returned.
type AIEndpoint struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url"`
	KeyPreview string `json:"key_preview"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

const aiSettingsBasePath = "/api/v1/org/ai-settings"

// GetAIProviderStatus returns the aggregated provider status.
func (c *Client) GetAIProviderStatus() (*AIProviderStatus, error) {
	var status AIProviderStatus
	if err := c.sendJSON(http.MethodGet, c.BaseURL+aiSettingsBasePath, nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SetAIProvider switches the active AI provider and returns the new status.
func (c *Client) SetAIProvider(provider string) (*AIProviderStatus, error) {
	var status AIProviderStatus
	payload := map[string]string{"provider": provider}
	if err := c.sendJSON(http.MethodPut, c.BaseURL+aiSettingsBasePath+"/provider", payload, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SaveAnthropicKey stores a custom Anthropic API key.
func (c *Client) SaveAnthropicKey(apiKey string) (*AIAnthropicStatus, error) {
	var status AIAnthropicStatus
	payload := map[string]string{"api_key": apiKey}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+aiSettingsBasePath+"/anthropic", payload, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteAnthropicKey removes the custom Anthropic API key.
func (c *Client) DeleteAnthropicKey() (*AIAnthropicStatus, error) {
	var status AIAnthropicStatus
	if err := c.sendJSON(http.MethodDelete, c.BaseURL+aiSettingsBasePath+"/anthropic", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SaveOpenAICompatible stores the legacy single OpenAI-compatible config.
func (c *Client) SaveOpenAICompatible(baseURL, apiKey, model string) (*AIOpenAICompatibleStatus, error) {
	var status AIOpenAICompatibleStatus
	payload := map[string]string{"base_url": baseURL, "api_key": apiKey, "model": model}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+aiSettingsBasePath+"/openai-compatible", payload, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteOpenAICompatible removes the legacy single OpenAI-compatible config.
func (c *Client) DeleteOpenAICompatible() (*AIOpenAICompatibleStatus, error) {
	var status AIOpenAICompatibleStatus
	if err := c.sendJSON(http.MethodDelete, c.BaseURL+aiSettingsBasePath+"/openai-compatible", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// ListAIModels returns the organisation model catalog.
func (c *Client) ListAIModels() ([]AICatalogModel, error) {
	var response struct {
		Models []AICatalogModel `json:"models"`
	}
	if err := c.sendJSON(http.MethodGet, c.BaseURL+aiSettingsBasePath+"/models", nil, &response); err != nil {
		return nil, err
	}
	return response.Models, nil
}

// CreateAIModel adds a catalog entry.
func (c *Client) CreateAIModel(request AIModelRequest) (*AICatalogModel, error) {
	var model AICatalogModel
	if err := c.sendJSON(http.MethodPost, c.BaseURL+aiSettingsBasePath+"/models", request, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

// UpdateAIModel updates a catalog entry addressed by its row id or catalog key.
func (c *Client) UpdateAIModel(reference string, request AIModelRequest) (*AICatalogModel, error) {
	var model AICatalogModel
	url := fmt.Sprintf("%s%s/models/%s", c.BaseURL, aiSettingsBasePath, reference)
	if err := c.sendJSON(http.MethodPut, url, request, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

// DeleteAIModel deletes a catalog entry addressed by its row id or catalog key.
func (c *Client) DeleteAIModel(reference string) error {
	url := fmt.Sprintf("%s%s/models/%s", c.BaseURL, aiSettingsBasePath, reference)
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// ResetAIModels reverts the catalog to the built-in defaults and returns it.
func (c *Client) ResetAIModels() ([]AICatalogModel, error) {
	var response struct {
		Models []AICatalogModel `json:"models"`
	}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+aiSettingsBasePath+"/models/reset", nil, &response); err != nil {
		return nil, err
	}
	return response.Models, nil
}

// ListAIEndpoints returns the configured OpenAI-compatible endpoints.
func (c *Client) ListAIEndpoints() ([]AIEndpoint, error) {
	var response struct {
		Endpoints []AIEndpoint `json:"endpoints"`
	}
	if err := c.sendJSON(http.MethodGet, c.BaseURL+aiSettingsBasePath+"/endpoints", nil, &response); err != nil {
		return nil, err
	}
	return response.Endpoints, nil
}

// CreateAIEndpoint validates, probes, and stores a new endpoint.
func (c *Client) CreateAIEndpoint(name, baseURL, apiKey string) (*AIEndpoint, error) {
	var endpoint AIEndpoint
	payload := map[string]string{"name": name, "base_url": baseURL, "api_key": apiKey}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+aiSettingsBasePath+"/endpoints", payload, &endpoint); err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// UpdateAIEndpoint updates an endpoint; an empty apiKey keeps the stored key.
func (c *Client) UpdateAIEndpoint(endpointID, name, baseURL, apiKey string) (*AIEndpoint, error) {
	var endpoint AIEndpoint
	payload := map[string]string{"name": name, "base_url": baseURL}
	if apiKey != "" {
		payload["api_key"] = apiKey
	}
	url := fmt.Sprintf("%s%s/endpoints/%s", c.BaseURL, aiSettingsBasePath, endpointID)
	if err := c.sendJSON(http.MethodPut, url, payload, &endpoint); err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// DeleteAIEndpoint deletes an endpoint and its stored key.
func (c *Client) DeleteAIEndpoint(endpointID string) error {
	url := fmt.Sprintf("%s%s/endpoints/%s", c.BaseURL, aiSettingsBasePath, endpointID)
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// DiscoverEndpointModels returns the model ids the endpoint advertises on
// /models, so a catalog entry can reference a real model.
func (c *Client) DiscoverEndpointModels(endpointID string) ([]string, error) {
	var response struct {
		Models []string `json:"models"`
	}
	url := fmt.Sprintf("%s%s/endpoints/%s/discover-models", c.BaseURL, aiSettingsBasePath, endpointID)
	if err := c.sendJSON(http.MethodPost, url, nil, &response); err != nil {
		return nil, err
	}
	return response.Models, nil
}
