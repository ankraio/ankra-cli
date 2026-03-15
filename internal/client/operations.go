package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

type OperationResponseListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type OperationListResponse struct {
	Result     []OperationResponseListItem `json:"result"`
	Pagination Pagination                  `json:"pagination"`
}

type CancelOperationResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CancelJobResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) ListClusterOperations(clusterID string) ([]OperationResponseListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/operations?type_list=write", c.BaseURL, clusterID)
	var operations OperationListResponse
	if err := c.getJSON(url, &operations.Result); err != nil {
		return nil, err
	}
	return operations.Result, nil
}

func (c *Client) CancelOperation(ctx context.Context, operationID string) (*CancelOperationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/operations/%s/cancel", c.BaseURL, operationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
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
		return nil, fmt.Errorf("cancel failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &CancelOperationResult{Success: true, Message: "Operation cancelled"}, nil
}

func (c *Client) CancelJob(ctx context.Context, operationID, jobID string) (*CancelJobResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/operations/%s/jobs/%s/cancel", c.BaseURL, operationID, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
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
		return nil, fmt.Errorf("cancel failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &CancelJobResult{Success: true, Message: "Job cancelled"}, nil
}
