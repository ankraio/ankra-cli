package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ChartItem struct {
	ChartID                string  `json:"chart_id"`
	Name                   string  `json:"name"`
	Description            string  `json:"description"`
	RepositoryName         string  `json:"repository_name"`
	RepositoryURL          string  `json:"repository_url"`
	RepositoryID           string  `json:"repository_id"`
	Icon                   string  `json:"icon"`
	Version                string  `json:"version"`
	RegistryCredentialName *string `json:"registry_credential_name,omitempty"`
}

type ChartsPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalPages int `json:"total_pages"`
}

type ListChartsResponse struct {
	Charts     []ChartItem      `json:"charts"`
	Pagination ChartsPagination `json:"pagination"`
}

type ChartProfile struct {
	ProfileID   string  `json:"profile_id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	UpdatedAt   string  `json:"updated_at"`
}

type ChartDetails struct {
	Name           string         `json:"name"`
	Icon           string         `json:"icon"`
	RepositoryName string         `json:"repository_name"`
	RepositoryURL  string         `json:"repository_url"`
	Versions       []string       `json:"versions"`
	Profiles       []ChartProfile `json:"profiles"`
}

type GetChartDetailsRequest struct {
	ChartName     string `json:"chart_name"`
	RepositoryURL string `json:"repository_url"`
}

func (c *Client) ListCharts(page, pageSize int, onlySubscribed bool) (*ListChartsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/stacks/charts?page=%d&page_size=%d&only_subscribed=%t",
		c.BaseURL, page, pageSize, onlySubscribed)
	var resp ListChartsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to list charts: %w", err)
	}
	return &resp, nil
}

func (c *Client) GetChartDetails(chartName, repositoryURL string) (*ChartDetails, error) {
	url := c.BaseURL + "/api/v1/org/stacks/charts/details"
	reqBody := GetChartDetailsRequest{ChartName: chartName, RepositoryURL: repositoryURL}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("get details failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var details ChartDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &details, nil
}

const (
	// searchChartsPageSize controls the page size used by SearchCharts.
	// 100 is the API's max, so larger queries do fewer round trips.
	searchChartsPageSize = 100
	// searchChartsMaxPages caps how many pages SearchCharts will walk.
	// The platform currently exposes a few thousand charts at most, so
	// 50 pages * 100 items = 5000 entries is a safe upper bound that
	// also protects against runaway pagination if the backend ever
	// returns a stale total_pages count.
	searchChartsMaxPages = 50
)

// SearchCharts performs a client-side filter across all available chart
// pages. The platform does not expose a free-text search endpoint for
// `/org/stacks/charts`, so SearchCharts walks pagination until the last
// page (or searchChartsMaxPages, whichever comes first) and matches the
// query against chart name and description, case-insensitively.
func (c *Client) SearchCharts(query string) ([]ChartItem, error) {
	query = strings.ToLower(strings.TrimSpace(query))

	var results []ChartItem
	for page := 1; page <= searchChartsMaxPages; page++ {
		resp, err := c.ListCharts(page, searchChartsPageSize, false)
		if err != nil {
			return nil, err
		}
		for _, chart := range resp.Charts {
			if query == "" ||
				strings.Contains(strings.ToLower(chart.Name), query) ||
				strings.Contains(strings.ToLower(chart.Description), query) {
				results = append(results, chart)
			}
		}
		if resp.Pagination.TotalPages <= page || len(resp.Charts) == 0 {
			break
		}
	}
	return results, nil
}
