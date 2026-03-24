package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type PodSummary struct {
	UID              string  `json:"uid"`
	Name             string  `json:"name"`
	Namespace        *string `json:"namespace"`
	Phase            string  `json:"phase"`
	Ready            string  `json:"ready"`
	Restarts         int     `json:"restarts"`
	LastRestartTime  *string `json:"last_restart_time"`
	StartTime        *string `json:"start_time"`
	PodIP            *string `json:"pod_ip"`
	NodeName         *string `json:"node_name"`
	NominatedNode    *string `json:"nominated_node_name"`
	ReadinessGates   *string `json:"readiness_gates"`
}

type CacheInfo struct {
	ServedFromCache  bool    `json:"served_from_cache"`
	StalenessSeconds int     `json:"staleness_seconds"`
	SyncStatus       string  `json:"sync_status"`
	Warning          *string `json:"warning"`
}

type ListPodsResponse struct {
	Pods       []PodSummary `json:"pods"`
	TotalCount int          `json:"total_count"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
	TotalPages int          `json:"total_pages"`
	CacheInfo  CacheInfo    `json:"cache_info"`
	Namespaces []string     `json:"namespaces"`
}

type ListPodsOptions struct {
	Page         int
	PageSize     int
	Namespace    string
	NameContains string
	NodeName     string
}

func (c *Client) ListPods(clusterID string, opts *ListPodsOptions) (*ListPodsResponse, error) {
	params := url.Values{}
	if opts != nil {
		if opts.Page > 0 {
			params.Set("page", fmt.Sprintf("%d", opts.Page))
		}
		if opts.PageSize > 0 {
			params.Set("page_size", fmt.Sprintf("%d", opts.PageSize))
		}
		if opts.Namespace != "" {
			params.Set("namespace", opts.Namespace)
		}
		if opts.NameContains != "" {
			params.Set("name_contains", opts.NameContains)
		}
		if opts.NodeName != "" {
			params.Set("node_name", opts.NodeName)
		}
	}

	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/pods", c.BaseURL, url.PathEscape(clusterID))
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	var response ListPodsResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

type FieldSelector struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type ResourceRequestItem struct {
	Kind           string          `json:"kind"`
	Namespace      string          `json:"namespace,omitempty"`
	Name           string          `json:"name,omitempty"`
	Group          string          `json:"group,omitempty"`
	Version        string          `json:"version"`
	LabelSelector  string          `json:"label_selector,omitempty"`
	FieldSelectors []FieldSelector `json:"field_selectors,omitempty"`
}

type GetResourcesRequest struct {
	ResourceRequests []ResourceRequestItem `json:"resource_requests"`
	SkipCache        bool                  `json:"skip_cache"`
}

type ResourceResponseItem struct {
	Status       string        `json:"status"`
	Group        *string       `json:"group"`
	Items        []interface{} `json:"items"`
	Kind         string        `json:"kind"`
	Name         *string       `json:"name"`
	Namespace    *string       `json:"namespace"`
	Version      string        `json:"version"`
	TotalCount   *int          `json:"total_count"`
}

type GetResourcesResponse struct {
	ResourceResponses []ResourceResponseItem `json:"resource_responses"`
}

func (c *Client) GetResources(clusterID string, req GetResourcesRequest) (*GetResourcesResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/resources/get", c.BaseURL, url.PathEscape(clusterID))
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var response GetResourcesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

type PodLogOptions struct {
	Namespace     string
	PodName       string
	ContainerName string
	TailLines     int
	SinceSeconds  int
}

func (c *Client) StreamPodLogs(ctx context.Context, clusterID string, opts PodLogOptions, writer io.Writer) error {
	params := url.Values{}
	params.Set("namespace", opts.Namespace)
	params.Set("pod_name", opts.PodName)
	if opts.ContainerName != "" {
		params.Set("container_name", opts.ContainerName)
	}
	if opts.TailLines > 0 {
		params.Set("tail_lines", fmt.Sprintf("%d", opts.TailLines))
	}
	if opts.SinceSeconds > 0 {
		params.Set("since_seconds", fmt.Sprintf("%d", opts.SinceSeconds))
	}

	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/pod/logs?%s",
		c.BaseURL, url.PathEscape(clusterID), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.StreamingHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	if resp.StatusCode == http.StatusServiceUnavailable {
		body, err := readResponseBody(resp)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		return parseClusterError(body)
	}
	if resp.StatusCode != http.StatusOK {
		body, err := readResponseBody(resp)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		return fmt.Errorf("request failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: error" {
			if scanner.Scan() {
				errLine := strings.TrimPrefix(scanner.Text(), "data:")
				errLine = strings.TrimPrefix(errLine, " ")
				return fmt.Errorf("%s", errLine)
			}
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			if _, err := fmt.Fprintln(writer, data); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}
	}
	return scanner.Err()
}

type HelmReleasesRequestItem struct {
	Namespace     string `json:"namespace,omitempty"`
	ReleaseName   string `json:"release_name,omitempty"`
	AllNamespaces bool   `json:"all_namespaces"`
}

type HelmReleasesRequest struct {
	ResourceRequests []HelmReleasesRequestItem `json:"resource_requests"`
}

type HelmReleasesResponseItem struct {
	Status        string        `json:"status"`
	Namespace     *string       `json:"namespace"`
	Items         []interface{} `json:"items"`
	AllNamespaces bool          `json:"all_namespaces"`
	TotalCount    *int          `json:"total_count"`
}

type HelmReleasesResponse struct {
	ResourceResponses []HelmReleasesResponseItem `json:"resource_responses"`
}

type HelmReleasesOptions struct {
	Namespace     string
	AllNamespaces bool
}

func (c *Client) ListHelmReleases(clusterID string, opts *HelmReleasesOptions) (*HelmReleasesResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/resources/helm/get", c.BaseURL, url.PathEscape(clusterID))

	reqItem := HelmReleasesRequestItem{AllNamespaces: true}
	if opts != nil {
		if opts.Namespace != "" {
			reqItem.Namespace = opts.Namespace
			reqItem.AllNamespaces = false
		}
		if opts.AllNamespaces {
			reqItem.AllNamespaces = true
		}
	}

	reqBody := HelmReleasesRequest{ResourceRequests: []HelmReleasesRequestItem{reqItem}}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var response HelmReleasesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

type UninstallHelmReleaseRequest struct {
	ReleaseName string `json:"release_name"`
	Namespace   string `json:"namespace"`
}

type UninstallHelmReleaseResponse struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

func (c *Client) UninstallHelmRelease(clusterID, releaseName, namespace string) (*UninstallHelmReleaseResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/resources/helm/uninstall", c.BaseURL, url.PathEscape(clusterID))
	reqBody := UninstallHelmReleaseRequest{ReleaseName: releaseName, Namespace: namespace}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed: status %d: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var response UninstallHelmReleaseResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

type ApiErrorResponse struct {
	ErrorCode string  `json:"error_code"`
	Detail    string  `json:"detail"`
	RetryAfter *int   `json:"retry_after"`
}

type ClusterUnavailableError struct {
	ErrorCode string
	Detail    string
}

func (e *ClusterUnavailableError) Error() string {
	switch e.ErrorCode {
	case "CLUSTER_OFFLINE":
		return "Cluster is offline. Check that the Ankra agent is running."
	case "NO_AGENT":
		return "No agent available for this cluster. Install the Ankra agent first."
	case "AGENT_TIMEOUT":
		return "Agent is not responding. The cluster may be temporarily unreachable."
	default:
		return e.Detail
	}
}

func parseClusterError(body []byte) error {
	var apiErr ApiErrorResponse
	if json.Unmarshal(body, &apiErr) == nil && apiErr.ErrorCode != "" {
		return &ClusterUnavailableError{ErrorCode: apiErr.ErrorCode, Detail: apiErr.Detail}
	}
	return fmt.Errorf("service unavailable: %s", truncateForError(body, 500))
}
