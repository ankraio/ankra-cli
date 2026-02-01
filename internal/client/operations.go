package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

// CancelOperationResult is the response from cancelling an operation
type CancelOperationResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CancelJobResult is the response from cancelling a job
type CancelJobResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func ListClusterOperations(token, baseURL, clusterID string) ([]OperationResponseListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/operations?type_list=write", baseURL, clusterID)
	var operations OperationListResponse
	if err := getJSON(url, token, &operations.Result); err != nil {
		return nil, err
	}
	return operations.Result, nil
}

// CancelOperation cancels a running operation
func CancelOperation(ctx context.Context, token, baseURL, operationID string) (*CancelOperationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/operations/%s/cancel",
		strings.TrimRight(baseURL, "/"), operationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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

// CancelJob cancels a specific job within an operation
func CancelJob(ctx context.Context, token, baseURL, operationID, jobID string) (*CancelJobResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/operations/%s/jobs/%s/cancel",
		strings.TrimRight(baseURL, "/"), operationID, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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
