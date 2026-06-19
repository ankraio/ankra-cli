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
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    *string  `json:"description"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags"`
	Visibility     string   `json:"visibility"`
	LatestVersion  int      `json:"latest_version"`
	CurrentVersion int      `json:"current_version"`
}

type StackProfileListResponse struct {
	Result     []StackProfileSummary `json:"result"`
	TotalCount int                   `json:"total_count"`
	Page       int                   `json:"page"`
	PageSize   int                   `json:"page_size"`
}

type StackProfileIacExport struct {
	ProfileID     string                  `json:"profile_id"`
	Version       int                     `json:"version"`
	ContentBase64 string                  `json:"content_base64"`
	Parameters    []StackProfileParameter `json:"parameters"`
}

type StackProfileParameter struct {
	Name        string   `json:"name"`
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     *string  `json:"default"`
	EnumValues  []string `json:"enum_values"`
	Group       *string  `json:"group"`
}

type StackProfileVersionSummary struct {
	ID        string  `json:"id"`
	Version   int     `json:"version"`
	Channel   string  `json:"channel"`
	Changelog *string `json:"changelog"`
	CreatedAt string  `json:"created_at"`
}

type StackProfileVersionDetail struct {
	ID         string                  `json:"id"`
	Version    int                     `json:"version"`
	Channel    string                  `json:"channel"`
	Parameters []StackProfileParameter `json:"parameters"`
	Changelog  *string                 `json:"changelog"`
	CreatedAt  string                  `json:"created_at"`
}

type StackProfileUpdateStatus struct {
	OutdatedInstantiationCount int  `json:"outdated_instantiation_count"`
	HasUpdateAvailable         bool `json:"has_update_available"`
}

type StackProfileDetail struct {
	Profile              StackProfileSummary          `json:"profile"`
	Versions             []StackProfileVersionSummary `json:"versions"`
	LatestVersionDetail  *StackProfileVersionDetail   `json:"latest_version_detail"`
	CurrentVersionDetail *StackProfileVersionDetail   `json:"current_version_detail"`
	UpdateStatus         StackProfileUpdateStatus     `json:"update_status"`
}

type ParameterBinding struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type InstantiateStackProfileRequest struct {
	ProfileID    string             `json:"profile_id"`
	Version      *int               `json:"version,omitempty"`
	NewStackName string             `json:"new_stack_name,omitempty"`
	Parameters   []ParameterBinding `json:"parameters"`
	Deploy       bool               `json:"deploy"`
}

type InstantiateStackProfileResult struct {
	DraftID        string   `json:"draft_id"`
	StackName      string   `json:"stack_name"`
	ProfileVersion int      `json:"profile_version"`
	Warnings       []string `json:"warnings"`
	AddonsCount    int      `json:"addons_count"`
	ManifestsCount int      `json:"manifests_count"`
	Deployed       bool     `json:"deployed"`
	OperationID    *string  `json:"operation_id"`
	JobCount       int      `json:"job_count"`
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

func (c *Client) GetStackProfile(profileID string) (*StackProfileDetail, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/stack-profiles/%s", c.BaseURL, neturl.PathEscape(profileID))
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
		return nil, fmt.Errorf("get stack profile failed (%d): %s", response.StatusCode, redactedBodyForError(body, 512))
	}
	var result StackProfileDetail
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) InstantiateStackProfile(ctx context.Context, clusterID string, instantiateRequest InstantiateStackProfileRequest) (*InstantiateStackProfileResult, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/from-profile",
		c.BaseURL, neturl.PathEscape(clusterID))
	payload, err := json.Marshal(instantiateRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)
	body, err := readResponseBody(response)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("instantiate stack profile failed", response.StatusCode, redactedBodyForError(body, 512))
	}
	var result InstantiateStackProfileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}
