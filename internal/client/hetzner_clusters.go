package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type NodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type CreateNodeGroupRequest struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Labels       map[string]string `json:"labels,omitempty"`
	Taints       []NodeTaint       `json:"taints,omitempty"`
}

type CreateHetznerClusterRequest struct {
	Name                   string                   `json:"name"`
	Description            *string                  `json:"description,omitempty"`
	CredentialID           string                   `json:"credential_id"`
	SSHKeyCredentialID     string                   `json:"ssh_key_credential_id,omitempty"`
	SSHKeyCredentialIDs    []string                 `json:"ssh_key_credential_ids,omitempty"`
	Location               string                   `json:"location"`
	NetworkIPRange         string                   `json:"network_ip_range"`
	SubnetRange            string                   `json:"subnet_range"`
	BastionServerType      string                   `json:"bastion_server_type"`
	ControlPlaneCount      int                      `json:"control_plane_count"`
	ControlPlaneServerType string                   `json:"control_plane_server_type"`
	WorkerCount            int                      `json:"worker_count"`
	WorkerServerType       string                   `json:"worker_server_type"`
	Distribution           string                   `json:"distribution"`
	KubernetesVersion      *string                  `json:"kubernetes_version,omitempty"`
	NodeGroups             []CreateNodeGroupRequest  `json:"node_groups,omitempty"`
}

type CreateHetznerClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionHetznerClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
}

type WorkerCountResult struct {
	WorkerCount int `json:"worker_count"`
	Min         int `json:"min"`
	Max         int `json:"max"`
}

type ScaleWorkersRequest struct {
	WorkerCount int `json:"worker_count"`
}

type ScaleWorkersResult struct {
	PreviousCount int `json:"previous_count"`
	NewCount      int `json:"new_count"`
}

type K8sVersionInfo struct {
	CurrentVersion *string `json:"current_version"`
	Distribution   string  `json:"distribution"`
}

type UpgradeK8sVersionRequest struct {
	TargetVersion string `json:"target_version"`
}

type UpgradeK8sVersionResult struct {
	PreviousVersion *string `json:"previous_version"`
	NewVersion      string  `json:"new_version"`
	NodesAffected   int     `json:"nodes_affected"`
}

func (c *Client) CreateHetznerCluster(req CreateHetznerClusterRequest) (*CreateHetznerClusterResponse, error) {
	url := c.BaseURL + "/api/v1/clusters/hetzner"
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("create failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var result CreateHetznerClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeprovisionHetznerCluster(clusterID string, force bool) (*DeprovisionHetznerClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s", c.BaseURL, clusterID)
	if force {
		url = url + "?force=true"
	}
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("deprovision failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var result DeprovisionHetznerClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetHetznerWorkerCount(clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/worker-count", c.BaseURL, clusterID)
	var result WorkerCountResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetHetznerK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/k8s-version", c.BaseURL, clusterID)
	var result K8sVersionInfo
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpgradeHetznerK8sVersion(clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/upgrade-k8s-version", c.BaseURL, clusterID)
	return c.doUpgradeK8sVersion(url, targetVersion)
}

func (c *Client) ScaleHetznerWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/scale-workers", c.BaseURL, clusterID)
	return c.doScaleWorkers(url, workerCount)
}

type NodeGroupInfo struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Min          int               `json:"min"`
	Max          int               `json:"max"`
	Labels       map[string]string `json:"labels"`
	Taints       []NodeTaint       `json:"taints"`
}

type NodeGroupListResult struct {
	NodeGroups []NodeGroupInfo `json:"node_groups"`
}

type AddNodeGroupRequest struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Labels       map[string]string `json:"labels,omitempty"`
	Taints       []NodeTaint       `json:"taints,omitempty"`
}

type AddNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Count     int    `json:"count"`
}

type ScaleNodeGroupRequest struct {
	Count int `json:"count"`
}

type ScaleNodeGroupResult struct {
	GroupName     string `json:"group_name"`
	PreviousCount int    `json:"previous_count"`
	NewCount      int    `json:"new_count"`
}

type UpdateInstanceTypeRequest struct {
	InstanceType string `json:"instance_type"`
}

type UpdateLabelsRequest struct {
	Labels map[string]string `json:"labels"`
}

type UpdateTaintsRequest struct {
	Taints []NodeTaint `json:"taints"`
}

type UpdateNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Updated   int    `json:"updated"`
}

type DeleteNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Deleted   int    `json:"deleted"`
}

func (c *Client) ListHetznerNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups", c.BaseURL, clusterID)
	var result NodeGroupListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AddHetznerNodeGroup(ctx context.Context, clusterID string, req AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups", c.BaseURL, clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doAddNodeGroup(ctx, url, payload, wait)
}

func (c *Client) ScaleHetznerNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s/scale", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doScaleNodeGroup(ctx, url, payload, wait)
}

func (c *Client) UpdateHetznerNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s/instance-type", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, url, payload, wait)
}

func (c *Client) DeleteHetznerNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s", c.BaseURL, clusterID, groupName)
	return c.doDeleteNodeGroup(ctx, url, wait)
}

func (c *Client) doAddNodeGroup(ctx context.Context, url string, payload []byte, wait bool) (*AddNodeGroupResult, bool, error) {
	var result AddNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPost, url, payload, wait, &result)
	if err != nil {
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	return &result, false, nil
}

func (c *Client) doScaleNodeGroup(ctx context.Context, url string, payload []byte, wait bool) (*ScaleNodeGroupResult, bool, error) {
	var result ScaleNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut, url, payload, wait, &result)
	if err != nil {
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	return &result, false, nil
}

func (c *Client) doUpdateNodeGroup(ctx context.Context, url string, payload []byte, wait bool) (*UpdateNodeGroupResult, bool, error) {
	var result UpdateNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut, url, payload, wait, &result)
	if err != nil {
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	return &result, false, nil
}

func (c *Client) doDeleteNodeGroup(ctx context.Context, url string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	var result DeleteNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodDelete, url, nil, wait, &result)
	if err != nil {
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	return &result, false, nil
}

func (c *Client) doScaleWorkers(url string, workerCount int) (*ScaleWorkersResult, error) {
	payload, err := json.Marshal(ScaleWorkersRequest{WorkerCount: workerCount})
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("scale failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var result ScaleWorkersResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) doUpgradeK8sVersion(url, targetVersion string) (*UpgradeK8sVersionResult, error) {
	payload, err := json.Marshal(UpgradeK8sVersionRequest{TargetVersion: targetVersion})
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
		return nil, newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("upgrade failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}

	var result UpgradeK8sVersionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}
