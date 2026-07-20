package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type ProviderStopClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
}

type ProviderStartClusterResult struct {
	MarkedToStartAt   string `json:"marked_to_start_at"`
	Scope             string `json:"scope"`
	CreatedOperations int    `json:"created_operations"`
}

func (c *Client) providerClusterURL(provider, clusterID, suffix string) string {
	endpoint := fmt.Sprintf(
		"%s/api/v1/clusters/%s/%s",
		c.BaseURL,
		url.PathEscape(provider),
		url.PathEscape(clusterID),
	)
	if suffix != "" {
		endpoint += "/" + url.PathEscape(suffix)
	}
	return endpoint
}

func (c *Client) stopProviderCluster(provider, clusterID string) (*ProviderStopClusterResponse, error) {
	request, requestError := http.NewRequest(
		http.MethodPost,
		c.providerClusterURL(provider, clusterID, "stop"),
		nil,
	)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, responseError := c.HTTP.Do(request)
	if responseError != nil {
		return nil, fmt.Errorf("request failed: %w", responseError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError(
			"stop failed",
			response.StatusCode,
			redactedBodyForError(body, 500),
		)
	}

	var result ProviderStopClusterResponse
	if parseError := json.Unmarshal(body, &result); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &result, nil
}

func (c *Client) startProviderCluster(provider, clusterID, scope string) (*ProviderStartClusterResult, error) {
	endpoint := c.providerClusterURL(provider, clusterID, "start")
	if scope != "" {
		endpoint += "?scope=" + url.QueryEscape(scope)
	}
	request, requestError := http.NewRequest(http.MethodPost, endpoint, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, responseError := c.HTTP.Do(request)
	if responseError != nil {
		return nil, fmt.Errorf("request failed: %w", responseError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError(
			"start failed",
			response.StatusCode,
			redactedBodyForError(body, 500),
		)
	}

	var result ProviderStartClusterResult
	if parseError := json.Unmarshal(body, &result); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &result, nil
}
