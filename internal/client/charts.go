package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get details failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var details ChartDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &details, nil
}

func (c *Client) SearchCharts(query string) ([]ChartItem, error) {
	resp, err := c.ListCharts(1, 100, false)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []ChartItem
	for _, chart := range resp.Charts {
		if strings.Contains(strings.ToLower(chart.Name), query) ||
			strings.Contains(strings.ToLower(chart.Description), query) {
			results = append(results, chart)
		}
	}
	return results, nil
}
