package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

type StackProfileSummary struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	Category      string   `json:"category"`
	Tags          []string `json:"tags"`
	LatestVersion int      `json:"latest_version"`
}

type StackProfileListResponse struct {
	Result     []StackProfileSummary `json:"result"`
	TotalCount int                   `json:"total_count"`
	Page       int                   `json:"page"`
	PageSize   int                   `json:"page_size"`
}

type StackProfileIacExport struct {
	ProfileID     string `json:"profile_id"`
	Version       int    `json:"version"`
	ContentBase64 string `json:"content_base64"`
}

type ImportStackProfileRequest struct {
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Category      string   `json:"category"`
	Tags          []string `json:"tags"`
	ContentBase64 string   `json:"content_base64"`
	Changelog     *string  `json:"changelog,omitempty"`
}

func (c *Client) ListStackProfiles(page, pageSize int, search string) (*StackProfileListResponse, error) {
	query := neturl.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if search != "" {
		query.Set("search", search)
	}
	requestURL := fmt.Sprintf("%s/api/v1/org/stack-profiles?%s", c.BaseURL, query.Encode())
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer closeBody(response)
	body, err := readResponseBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list stack profiles failed (%d): %s", response.StatusCode, redactedBodyForError(body, 512))
	}
	var result StackProfileListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ExportStackProfileIac(profileID string, version int) (*StackProfileIacExport, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/stack-profiles/%s/versions/%d/iac-export", c.BaseURL, profileID, version)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer closeBody(response)
	body, err := readResponseBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("export stack profile failed (%d): %s", response.StatusCode, redactedBodyForError(body, 512))
	}
	var result StackProfileIacExport
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ImportStackProfile(importRequest ImportStackProfileRequest) (*CreateStackProfileResult, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/stack-profiles/import", c.BaseURL)
	payload, err := json.Marshal(importRequest)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer closeBody(response)
	body, err := readResponseBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("import stack profile failed (%d): %s", response.StatusCode, redactedBodyForError(body, 512))
	}
	var result CreateStackProfileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type CreateStackProfileResult struct {
	Profile StackProfileSummary `json:"profile"`
}
