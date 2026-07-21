package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type CreateDigitaloceanClusterRequest struct {
	Name                  string  `json:"name"`
	Description           *string `json:"description,omitempty"`
	CredentialID          string  `json:"credential_id"`
	SSHKeyCredentialID    string  `json:"ssh_key_credential_id"`
	Region                string  `json:"region"`
	NetworkIPRange        string  `json:"network_ip_range"`
	BastionSize           string  `json:"bastion_size"`
	ControlPlaneCount     int     `json:"control_plane_count"`
	ControlPlaneSize      string  `json:"control_plane_size"`
	WorkerCount           int     `json:"worker_count"`
	WorkerSize            string  `json:"worker_size"`
	Distribution          string  `json:"distribution"`
	KubernetesVersion     *string `json:"kubernetes_version,omitempty"`
	EtcdTopology          string  `json:"etcd_topology,omitempty"`
	EtcdNodeCount         int     `json:"etcd_node_count,omitempty"`
	EtcdSize              string  `json:"etcd_size,omitempty"`
	ExternalCloudProvider bool    `json:"external_cloud_provider"`
	IncludeNetworking     bool    `json:"include_networking"`
	GitopsCredentialName  *string `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string `json:"gitops_repository,omitempty"`
	GitopsBranch          *string `json:"gitops_branch,omitempty"`
}

type CreateDigitaloceanClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type StopDigitaloceanClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
}

type StartDigitaloceanClusterResult struct {
	MarkedToStartAt   string `json:"marked_to_start_at"`
	Scope             string `json:"scope"`
	CreatedOperations int    `json:"created_operations"`
}

type DeprovisionDigitaloceanClusterResponse struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	OperationID     *string  `json:"operation_id,omitempty"`
	ResourcesMarked int      `json:"resources_marked"`
	Errors          []string `json:"errors"`
}

func (c *Client) CreateDigitaloceanCluster(req CreateDigitaloceanClusterRequest) (*CreateDigitaloceanClusterResponse, error) {
	url := c.BaseURL + "/api/v1/clusters/digitalocean"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("create failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result CreateDigitaloceanClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeprovisionDigitaloceanCluster(clusterID string) (*DeprovisionDigitaloceanClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s", c.BaseURL, clusterID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, newUnexpectedResponseError("deprovision failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result DeprovisionDigitaloceanClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetDigitaloceanWorkerCount(clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/worker-count", c.BaseURL, clusterID)
	var result WorkerCountResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetDigitaloceanK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/k8s-version", c.BaseURL, clusterID)
	var result K8sVersionInfo
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpgradeDigitaloceanK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/upgrade-k8s-version", c.BaseURL, clusterID)
	return c.doUpgradeK8sVersion(url, targetVersion, force)
}

func (c *Client) ListDigitaloceanNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups", c.BaseURL, clusterID)
	var result NodeGroupListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AddDigitaloceanNodeGroup(ctx context.Context, clusterID string, req AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups", c.BaseURL, clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doAddNodeGroup(ctx, url, payload, wait)
}

func (c *Client) ScaleDigitaloceanNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/scale", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doScaleNodeGroup(ctx, url, payload, wait)
}

func (c *Client) GetDigitaloceanNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/autoscaling", c.BaseURL, clusterID, groupName)
	return c.doGetNodeGroupAutoscaling(url)
}

func (c *Client) UpdateDigitaloceanNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, req NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/autoscaling", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroupAutoscaling(ctx, url, payload, wait)
}

func (c *Client) UpdateDigitaloceanNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/instance-type", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, url, payload, wait)
}

func (c *Client) UpdateDigitaloceanNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateDigitaloceanNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteDigitaloceanNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/node-groups/%s", c.BaseURL, clusterID, groupName)
	return c.doDeleteNodeGroup(ctx, url, wait)
}

func (c *Client) ScaleDigitaloceanWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	scaleURL := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/scale-workers", c.BaseURL, clusterID)
	return c.doScaleWorkers(scaleURL, workerCount)
}

func (c *Client) StopDigitaloceanCluster(clusterID string) (*StopDigitaloceanClusterResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/stop", c.BaseURL, url.PathEscape(clusterID))
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("stop failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result StopDigitaloceanClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) StartDigitaloceanCluster(clusterID, scope string) (*StartDigitaloceanClusterResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/%s/start", c.BaseURL, url.PathEscape(clusterID))
	if scope != "" {
		endpoint += "?scope=" + url.QueryEscape(scope)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("start failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result StartDigitaloceanClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type DigitaloceanRegion struct {
	Slug      string   `json:"slug"`
	Name      string   `json:"name"`
	Available bool     `json:"available"`
	Sizes     []string `json:"sizes"`
}

type DigitaloceanSize struct {
	Slug         string  `json:"slug"`
	Description  string  `json:"description"`
	Vcpus        int     `json:"vcpus"`
	Memory       int     `json:"memory"`
	Disk         int     `json:"disk"`
	PriceMonthly float64 `json:"price_monthly"`
	Available    bool    `json:"available"`
}

func (c *Client) ListDigitaloceanRegions(credentialID string) ([]DigitaloceanRegion, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/regions?credential_id=%s", c.BaseURL, url.QueryEscape(credentialID))
	var result []DigitaloceanRegion
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) ListDigitaloceanSizes(credentialID, region string) ([]DigitaloceanSize, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/digitalocean/sizes?credential_id=%s", c.BaseURL, url.QueryEscape(credentialID))
	if region != "" {
		endpoint += "&region=" + url.QueryEscape(region)
	}
	var result []DigitaloceanSize
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return result, nil
}
