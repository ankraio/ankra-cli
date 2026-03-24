package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type CreateUpcloudClusterRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	CredentialID       string  `json:"credential_id"`
	SSHKeyCredentialID string  `json:"ssh_key_credential_id"`
	Zone               string  `json:"zone"`
	NetworkIPRange     string  `json:"network_ip_range"`
	BastionPlan        string  `json:"bastion_plan"`
	ControlPlaneCount  int     `json:"control_plane_count"`
	ControlPlanePlan   string  `json:"control_plane_plan"`
	WorkerCount        int     `json:"worker_count"`
	WorkerPlan         string  `json:"worker_plan"`
	Distribution       string  `json:"distribution"`
	KubernetesVersion  *string `json:"kubernetes_version,omitempty"`
}

type CreateUpcloudClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionUpcloudClusterResponse struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	OperationID     *string  `json:"operation_id,omitempty"`
	ResourcesMarked int      `json:"resources_marked"`
	Errors          []string `json:"errors"`
}

func (c *Client) CreateUpcloudCluster(req CreateUpcloudClusterRequest) (*CreateUpcloudClusterResponse, error) {
	url := c.BaseURL + "/api/v1/clusters/upcloud"
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
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result CreateUpcloudClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeprovisionUpcloudCluster(clusterID string) (*DeprovisionUpcloudClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s", c.BaseURL, clusterID)
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
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result DeprovisionUpcloudClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetUpcloudWorkerCount(clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/worker-count", c.BaseURL, clusterID)
	var result WorkerCountResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetUpcloudK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/k8s-version", c.BaseURL, clusterID)
	var result K8sVersionInfo
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpgradeUpcloudK8sVersion(clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/upgrade-k8s-version", c.BaseURL, clusterID)
	return c.doUpgradeK8sVersion(url, targetVersion)
}

func (c *Client) ListUpcloudNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups", c.BaseURL, clusterID)
	var result NodeGroupListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AddUpcloudNodeGroup(clusterID string, req AddNodeGroupRequest) (*AddNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups", c.BaseURL, clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doAddNodeGroup(url, payload)
}

func (c *Client) ScaleUpcloudNodeGroup(clusterID, groupName string, count int) (*ScaleNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s/scale", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doScaleNodeGroup(url, payload)
}

func (c *Client) UpdateUpcloudNodeGroupInstanceType(clusterID, groupName, instanceType string) (*UpdateNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s/instance-type", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroupInstanceType(url, payload)
}

func (c *Client) DeleteUpcloudNodeGroup(clusterID, groupName string) (*DeleteNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s", c.BaseURL, clusterID, groupName)
	return c.doDeleteNodeGroup(url)
}

func (c *Client) ScaleUpcloudWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/scale-workers", c.BaseURL, clusterID)
	return c.doScaleWorkers(url, workerCount)
}
