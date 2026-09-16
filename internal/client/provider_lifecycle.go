package client

// Kind-parameterized helpers for the per-provider cluster endpoints under
// /api/v1/clusters/<kind>/... . The exported per-provider methods in
// proxmox_clusters.go and morpheus_clusters.go stay thin wrappers over
// these so the exported surface keeps the established per-provider naming
// convention.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ProviderStopClusterResponse is the shared stop-cluster response shape for
// the provider lifecycle endpoints.
type ProviderStopClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
	// StatePreserved reports a stop that first captures an encrypted etcd
	// snapshot and tears the VMs down only once it is stored; the next
	// start restores it (StateSnapshot names the capture to follow).
	StatePreserved bool              `json:"state_preserved"`
	StateSnapshot  *StateSnapshotRef `json:"state_snapshot,omitempty"`
	// Message explains a stop that could not preserve state, or what a
	// preserving stop does next.
	Message string `json:"message,omitempty"`
	// StopMode is what the stop does to the VMs: "pause" (powered off and
	// kept with their disks) or "delete_resources" (terminated).
	StopMode string `json:"stop_mode,omitempty"`
}

// StateSnapshotRef points at the cluster state capture a stop armed.
type StateSnapshotRef struct {
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
}

// StopClusterOptions parameterises a provider cluster stop.
type StopClusterOptions struct {
	// Force cancels every in-flight operation and tears down now; it never
	// captures the cluster's state.
	Force bool
	// PreserveState: nil lets the backend capture the cluster's state
	// (an encrypted etcd snapshot) whenever the provider and distribution
	// support it, true requires the capture, false tears down without it.
	PreserveState *bool
	// Mode is how the stop leaves the VMs: "" or "delete_resources" tears
	// them down, "pause" powers the servers off and keeps them with their
	// disks (k3s on Hetzner, UpCloud and DigitalOcean; what a stop always
	// does on AWS and Scaleway). A pause cannot be forced and takes no
	// preserve_state.
	Mode string
}

// StartClusterOptions parameterises a provider cluster start.
type StartClusterOptions struct {
	// Scope is "all" or "control_plane"; "" leaves the backend default.
	Scope string
	// RestoreState: nil restores the newest captured state snapshot when
	// there is one, true requires one, false starts a fresh cluster.
	RestoreState *bool
}

func (options StopClusterOptions) query() string {
	values := url.Values{}
	if options.Force {
		values.Set("force", "true")
	}
	if options.PreserveState != nil {
		values.Set("preserve_state", strconv.FormatBool(*options.PreserveState))
	}
	if options.Mode != "" {
		values.Set("mode", options.Mode)
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func (options StartClusterOptions) query() string {
	values := url.Values{}
	if options.Scope != "" {
		values.Set("scope", options.Scope)
	}
	if options.RestoreState != nil {
		values.Set("restore_state", strconv.FormatBool(*options.RestoreState))
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// ProviderStartClusterResult is the shared start-cluster response shape for
// the provider lifecycle endpoints.
type ProviderStartClusterResult struct {
	MarkedToStartAt   string `json:"marked_to_start_at"`
	Scope             string `json:"scope"`
	CreatedOperations int    `json:"created_operations"`
	// StateRestore is "requested" when the first control plane restores a
	// captured state snapshot, "none" when there was none, "skipped" when
	// the caller asked for a fresh cluster; "" from older backends.
	StateRestore    string  `json:"state_restore,omitempty"`
	StateSnapshotID *string `json:"state_snapshot_id,omitempty"`
}

// ProviderDeprovisionClusterResponse is the shared deprovision response shape
// for the provider lifecycle endpoints.
type ProviderDeprovisionClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
}

func (c *Client) providerClusterURL(kind, clusterID, suffix string) string {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s", c.BaseURL, kind, url.PathEscape(clusterID))
	if suffix != "" {
		endpoint += "/" + suffix
	}
	return endpoint
}

func (c *Client) createProviderCluster(kind string, request interface{}, result interface{}) error {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s", c.BaseURL, kind)
	payload, marshalError := json.Marshal(request)
	if marshalError != nil {
		return fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return newUnexpectedResponseError("create failed", response.StatusCode, redactedBodyForError(body, 500))
	}
	if parseError := json.Unmarshal(body, result); parseError != nil {
		return fmt.Errorf("parse response: %w", parseError)
	}
	return nil
}

func (c *Client) deprovisionProviderCluster(kind, clusterID string, force bool) (*ProviderDeprovisionClusterResponse, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "")
	if force {
		endpoint += "?force=true"
	}
	httpRequest, requestError := http.NewRequest(http.MethodDelete, endpoint, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("deprovision failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var result ProviderDeprovisionClusterResponse
	if parseError := json.Unmarshal(body, &result); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &result, nil
}

func (c *Client) stopProviderCluster(kind, clusterID string, options StopClusterOptions) (*ProviderStopClusterResponse, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "stop") + options.query()
	httpRequest, requestError := http.NewRequest(http.MethodPost, endpoint, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("stop failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var result ProviderStopClusterResponse
	if parseError := json.Unmarshal(body, &result); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &result, nil
}

func (c *Client) startProviderCluster(kind, clusterID string, options StartClusterOptions) (*ProviderStartClusterResult, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "start") + options.query()
	httpRequest, requestError := http.NewRequest(http.MethodPost, endpoint, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("start failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var result ProviderStartClusterResult
	if parseError := json.Unmarshal(body, &result); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &result, nil
}

func (c *Client) getProviderWorkerCount(kind, clusterID string) (*WorkerCountResult, error) {
	var result WorkerCountResult
	if getError := c.getJSON(c.providerClusterURL(kind, clusterID, "worker-count"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) scaleProviderWorkers(kind, clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	return c.doScaleWorkers(c.providerClusterURL(kind, clusterID, "scale-workers"), workerCount)
}

func (c *Client) getProviderK8sVersion(kind, clusterID string) (*K8sVersionInfo, error) {
	var result K8sVersionInfo
	if getError := c.getJSON(c.providerClusterURL(kind, clusterID, "k8s-version"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) upgradeProviderK8sVersion(kind, clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.doUpgradeK8sVersion(c.providerClusterURL(kind, clusterID, "upgrade-k8s-version"), targetVersion, force)
}

func (c *Client) listProviderNodeGroups(kind, clusterID string) (*NodeGroupListResult, error) {
	var result NodeGroupListResult
	if getError := c.getJSON(c.providerClusterURL(kind, clusterID, "node-groups"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) addProviderNodeGroup(ctx context.Context, kind, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	payload, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doAddNodeGroup(ctx, c.providerClusterURL(kind, clusterID, "node-groups"), payload, wait)
}

func (c *Client) scaleProviderNodeGroup(ctx context.Context, kind, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	payload, marshalError := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	endpoint := c.providerClusterURL(kind, clusterID, "node-groups") + "/" + url.PathEscape(groupName) + "/scale"
	return c.doScaleNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) updateProviderNodeGroupInstanceType(ctx context.Context, kind, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	payload, marshalError := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	endpoint := c.providerClusterURL(kind, clusterID, "node-groups") + "/" + url.PathEscape(groupName) + "/instance-type"
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) deleteProviderNodeGroup(ctx context.Context, kind, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "node-groups") + "/" + url.PathEscape(groupName)
	return c.doDeleteNodeGroup(ctx, endpoint, wait)
}

func (c *Client) getProviderNodeGroupAutoscaling(kind, clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "node-groups") + "/" + url.PathEscape(groupName) + "/autoscaling"
	return c.doGetNodeGroupAutoscaling(endpoint)
}

func (c *Client) updateProviderNodeGroupAutoscaling(ctx context.Context, kind, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	payload, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	endpoint := c.providerClusterURL(kind, clusterID, "node-groups") + "/" + url.PathEscape(groupName) + "/autoscaling"
	return c.doUpdateNodeGroupAutoscaling(ctx, endpoint, payload, wait)
}
