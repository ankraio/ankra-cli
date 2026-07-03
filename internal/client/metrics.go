package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type PrometheusQueryResult struct {
	Status      string        `json:"status"`
	ResultType  *string       `json:"result_type"`
	Result      []interface{} `json:"result"`
	TotalSeries int           `json:"total_series"`
	Truncated   bool          `json:"truncated"`
}

type prometheusInstantRequest struct {
	Query          string   `json:"query"`
	TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
}

type prometheusRangeRequest struct {
	Query          string   `json:"query"`
	Range          string   `json:"range,omitempty"`
	Start          *float64 `json:"start,omitempty"`
	End            *float64 `json:"end,omitempty"`
	Step           string   `json:"step,omitempty"`
	TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
}

type PrometheusRangeOptions struct {
	Query          string
	Range          string
	Start          string
	End            string
	Step           string
	TimeoutSeconds int
}

func (c *Client) QueryPrometheusInstant(clusterID, query string, timeoutSeconds int) (*PrometheusQueryResult, error) {
	request := prometheusInstantRequest{Query: query}
	if timeoutSeconds > 0 {
		seconds := float64(timeoutSeconds)
		request.TimeoutSeconds = &seconds
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/metrics/query", c.BaseURL, url.PathEscape(clusterID))
	return c.postPrometheusQuery(endpoint, payload)
}

func (c *Client) QueryPrometheusRange(clusterID string, opts PrometheusRangeOptions) (*PrometheusQueryResult, error) {
	request := prometheusRangeRequest{Query: opts.Query, Range: opts.Range, Step: opts.Step}
	if opts.Start != "" {
		start, err := strconv.ParseFloat(opts.Start, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid --start %q: expected unix seconds", opts.Start)
		}
		request.Start = &start
	}
	if opts.End != "" {
		end, err := strconv.ParseFloat(opts.End, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid --end %q: expected unix seconds", opts.End)
		}
		request.End = &end
	}
	if opts.TimeoutSeconds > 0 {
		seconds := float64(opts.TimeoutSeconds)
		request.TimeoutSeconds = &seconds
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/metrics/query_range", c.BaseURL, url.PathEscape(clusterID))
	return c.postPrometheusQuery(endpoint, payload)
}

func (c *Client) postPrometheusQuery(endpoint string, payload []byte) (*PrometheusQueryResult, error) {
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

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, parseClusterError(body)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var response PrometheusQueryResult
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}
