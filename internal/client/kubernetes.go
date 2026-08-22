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
	"time"
)

type PodSummary struct {
	UID             string  `json:"uid"`
	Name            string  `json:"name"`
	Namespace       *string `json:"namespace"`
	Phase           string  `json:"phase"`
	Ready           string  `json:"ready"`
	Restarts        int     `json:"restarts"`
	LastRestartTime *string `json:"last_restart_time"`
	StartTime       *string `json:"start_time"`
	PodIP           *string `json:"pod_ip"`
	NodeName        *string `json:"node_name"`
	NominatedNode   *string `json:"nominated_node_name"`
	ReadinessGates  *string `json:"readiness_gates"`
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
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Group     string `json:"group,omitempty"`
	// Resource is the API plural. The backend derives one from Kind when
	// this is empty, and that derivation is wrong for the aggregated
	// metrics API (PodMetrics is served as "pods", not "podmetricses"), so
	// callers outside the ordinary kinds send it explicitly.
	Resource       string          `json:"resource,omitempty"`
	Version        string          `json:"version"`
	LabelSelector  string          `json:"label_selector,omitempty"`
	FieldSelectors []FieldSelector `json:"field_selectors,omitempty"`
}

type GetResourcesRequest struct {
	ResourceRequests []ResourceRequestItem `json:"resource_requests"`
	SkipCache        bool                  `json:"skip_cache"`
}

// ResourceCacheMetadata is present only when the platform answered from its
// Kubernetes cache instead of the live cluster. Reads that are only correct
// live - the aggregated metrics API above all - must refuse a cached answer
// rather than render yesterday's objects as today's measurements.
type ResourceCacheMetadata struct {
	ServedFromCache  bool    `json:"served_from_cache"`
	StalenessSeconds int     `json:"staleness_seconds"`
	SyncStatus       string  `json:"sync_status"`
	Warning          *string `json:"warning"`
	IsDeleted        bool    `json:"is_deleted"`
	DeletedAt        *string `json:"deleted_at"`
}

type ResourceResponseItem struct {
	Status        string                 `json:"status"`
	Group         *string                `json:"group"`
	Items         []interface{}          `json:"items"`
	Kind          string                 `json:"kind"`
	Name          *string                `json:"name"`
	Namespace     *string                `json:"namespace"`
	Version       string                 `json:"version"`
	TotalCount    *int                   `json:"total_count"`
	CacheMetadata *ResourceCacheMetadata `json:"cache_metadata"`
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
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
	// Follow keeps the stream open for new lines (kubectl logs -f). When
	// false the request carries follow=false, so the route snapshots the
	// selected tail and closes the response on the agent's end frame. A
	// platform that predates that parameter ignores it and keeps following,
	// so the client still detects the drain itself - an idle gap after the
	// first batch, or the server's keepalive comment, which it only sends
	// once it has nothing buffered.
	Follow bool
	// Previous reads the log of the container instance that terminated
	// (kubectl logs --previous) - the only readable output of a container
	// stuck in CrashLoopBackOff. The terminated log is closed, so the
	// platform bounds the read regardless of Follow. A platform or agent
	// that predates the parameter ignores it and serves the current
	// container instead, which is why the CLI states the requirement in
	// its help rather than silently presenting the wrong log as the right
	// one.
	Previous bool
}

// podLogsDrainIdle is how long a non-follow read waits for another line
// after the last one before deciding the backlog is drained. The agent
// pushes the backlog in quick batches, so a genuine gap means it is done.
const podLogsDrainIdle = 2 * time.Second

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
	if !opts.Follow {
		// The route defaults to follow=true; only the bounded read has to
		// say so, which keeps the following request byte-identical to what
		// every released CLI has always sent.
		params.Set("follow", "false")
	}
	if opts.Previous {
		params.Set("previous", "true")
		// The route bounds a previous read whatever follow says, so the
		// client has to take the drain path or it would sit waiting on a
		// stream the server already closed.
		opts.Follow = false
		params.Set("follow", "false")
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
		return newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	if opts.Follow {
		for scanner.Scan() {
			if done, lineError := writePodLogLine(scanner, writer); done || lineError != nil {
				return lineError
			}
		}
		return scanner.Err()
	}
	return drainPodLogs(ctx, scanner, resp, writer)
}

// sseData strips the "data:" prefix and its optional space from an SSE
// line, leaving the payload.
func sseData(line string) string {
	return strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
}

// writePodLogLine handles one SSE line: an error event surfaces as the
// error, an end event stops the read, a data line is written. done reports
// the stream is over. Both terminating events carry their own data line
// ("stream complete", "stream idle timeout") - that is protocol text, so it
// is consumed with the event rather than printed as if it were a log line.
func writePodLogLine(scanner *bufio.Scanner, writer io.Writer) (bool, error) {
	line := scanner.Text()
	switch line {
	case "event: error":
		if scanner.Scan() {
			return true, fmt.Errorf("%s", sseData(scanner.Text()))
		}
		return true, nil
	case "event: end":
		scanner.Scan()
		return true, nil
	}
	if strings.HasPrefix(line, "data:") {
		if _, err := fmt.Fprintln(writer, sseData(line)); err != nil {
			return false, fmt.Errorf("write output: %w", err)
		}
	}
	return false, nil
}

// drainPodLogs reads a non-follow stream until the backlog is drained. A
// route that honours follow=false ends it with its own "end" frame; against
// an older one the drain is inferred client-side from the first idle gap
// after at least one line, or from a server keepalive comment (sent only
// when nothing is buffered), and the response is closed so the server tears
// the stream down. A stream that ends on its own returns its scanner error,
// if any.
func drainPodLogs(ctx context.Context, scanner *bufio.Scanner, resp *http.Response, writer io.Writer) error {
	lines := make(chan string)
	scanErrors := make(chan error, 1)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		scanErrors <- scanner.Err()
	}()
	idle := time.NewTimer(podLogsDrainIdle)
	defer idle.Stop()
	sawLine := false
	pendingError := false
	pendingEnd := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-idle.C:
			if sawLine {
				closeBody(resp)
				return nil
			}
			idle.Reset(podLogsDrainIdle)
		case line, open := <-lines:
			if !open {
				select {
				case scanError := <-scanErrors:
					return scanError
				default:
					return nil
				}
			}
			switch {
			case pendingError:
				return fmt.Errorf("%s", sseData(line))
			case pendingEnd:
				// The end frame's payload ("stream complete", "stream idle
				// timeout") is protocol text, not a log line: swallow it and
				// leave, the server has already closed its side.
				return nil
			case line == "event: error":
				pendingError = true
				continue
			case line == "event: end":
				pendingEnd = true
				continue
			case strings.HasPrefix(line, ":") && sawLine:
				closeBody(resp)
				return nil
			case strings.HasPrefix(line, "data:"):
				if _, err := fmt.Fprintln(writer, sseData(line)); err != nil {
					return fmt.Errorf("write output: %w", err)
				}
				sawLine = true
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(podLogsDrainIdle)
		}
	}
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var response UninstallHelmReleaseResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

type ApiErrorResponse struct {
	ErrorCode  string `json:"error_code"`
	Detail     string `json:"detail"`
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
	return fmt.Errorf("service unavailable: %s", redactedBodyForError(body, 500))
}
