package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type StepSummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
}

type ExecutionSummary struct {
	ID             string      `json:"id"`
	Scope          string      `json:"scope"`
	ClusterID      *string     `json:"cluster_id"`
	ClusterName    *string     `json:"cluster_name"`
	OrganisationID string      `json:"organisation_id"`
	Name           string      `json:"name"`
	DisplayName    string      `json:"display_name"`
	Type           string      `json:"type"`
	Status         string      `json:"status"`
	ErrorExcerpt   *string     `json:"error_excerpt"`
	StepSummary    StepSummary `json:"step_summary"`
	CreatedAt      *string     `json:"created_at"`
	UpdatedAt      *string     `json:"updated_at"`
}

type ExecutionStep struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	Type               *string `json:"type"`
	TargetResourceKind *string `json:"target_resource_kind"`
	TargetResourceID   *string `json:"target_resource_id"`
	ErrorExcerpt       *string `json:"error_excerpt"`
	SchedulerName      *string `json:"scheduler_name"`
	StartAt            *string `json:"start_at"`
	StopAt             *string `json:"stop_at"`
	CreatedAt          *string `json:"created_at"`
	UpdatedAt          *string `json:"updated_at"`
}

type ExecutionDetail struct {
	Execution   ExecutionSummary `json:"execution"`
	Steps       []ExecutionStep  `json:"steps"`
	StepSummary StepSummary      `json:"step_summary"`
}

type ExecutionListResponse struct {
	Result     []ExecutionSummary `json:"result"`
	Pagination Pagination         `json:"pagination"`
}

type ListExecutionsOptions struct {
	ClusterID          string
	Scope              string
	StatusList         []string
	TargetResourceKind string
	TargetResourceID   string
	Page               int
	PageSize           int
}

type CancelExecutionResponse struct {
	ExecutionID string  `json:"execution_id"`
	Status      string  `json:"status"`
	UpdatedAt   *string `json:"updated_at"`
}

type CancelStepResponse struct {
	ExecutionID string `json:"execution_id"`
	StepID      string `json:"step_id"`
	Status      string `json:"status"`
}

type BatchCancelExecutionsRequest struct {
	ExecutionIDs []string `json:"execution_ids"`
}

type BatchCancelExecutionsResponse struct {
	Cancelled  []string `json:"cancelled"`
	NotFound   []string `json:"not_found"`
	NotRunning []string `json:"not_running"`
}

func (c *Client) ListExecutions(opts ListExecutionsOptions) (ExecutionListResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions", c.BaseURL)
	params := url.Values{}
	if opts.ClusterID != "" {
		params.Set("cluster_id", opts.ClusterID)
	}
	if opts.Scope != "" {
		params.Set("scope", opts.Scope)
	}
	for _, status := range opts.StatusList {
		if status != "" {
			params.Add("status", status)
		}
	}
	if opts.TargetResourceKind != "" {
		params.Set("target_resource_kind", opts.TargetResourceKind)
	}
	if opts.TargetResourceID != "" {
		params.Set("target_resource_id", opts.TargetResourceID)
	}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("page_size", fmt.Sprintf("%d", opts.PageSize))
	}
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	var response ExecutionListResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return ExecutionListResponse{}, err
	}
	return response, nil
}

func (c *Client) GetExecution(executionID string) (ExecutionDetail, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/%s", c.BaseURL, executionID)
	var detail ExecutionDetail
	if err := c.getJSON(endpoint, &detail); err != nil {
		return ExecutionDetail{}, err
	}
	return detail, nil
}

func (c *Client) ListExecutionSteps(executionID string) ([]ExecutionStep, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/%s/steps", c.BaseURL, executionID)
	var steps []ExecutionStep
	if err := c.getJSON(endpoint, &steps); err != nil {
		return nil, err
	}
	return steps, nil
}

func (c *Client) CancelExecution(ctx context.Context, executionID string) (*CancelExecutionResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/%s/cancel", c.BaseURL, executionID)
	return doExecutionPostJSON[CancelExecutionResponse](c, ctx, endpoint, nil)
}

func (c *Client) CancelExecutionStep(ctx context.Context, executionID, stepID string) (*CancelStepResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/%s/steps/%s/cancel", c.BaseURL, executionID, stepID)
	return doExecutionPostJSON[CancelStepResponse](c, ctx, endpoint, nil)
}

func (c *Client) BatchCancelExecutions(ctx context.Context, executionIDs []string) (*BatchCancelExecutionsResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/cancel", c.BaseURL)
	body := BatchCancelExecutionsRequest{ExecutionIDs: executionIDs}
	return doExecutionPostJSON[BatchCancelExecutionsResponse](c, ctx, endpoint, body)
}

func (c *Client) RetryExecution(ctx context.Context, executionID string) (*ExecutionSummary, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/executions/%s/retry", c.BaseURL, executionID)
	return doExecutionPostJSON[ExecutionSummary](c, ctx, endpoint, nil)
}

func doExecutionPostJSON[T any](c *Client, ctx context.Context, endpoint string, payload any) (*T, error) {
	var bodyReader *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	}
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

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized. Run `ankra login` to re-authenticate")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trimmed := strings.TrimSpace(string(body))
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("execution endpoint returned status %d: %s", resp.StatusCode, redactedBodyForError([]byte(trimmed), 500)))
	}

	if len(body) == 0 {
		var zero T
		return &zero, nil
	}

	var decoded T
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &decoded, nil
}
