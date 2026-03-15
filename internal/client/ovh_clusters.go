package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type CreateOvhClusterRequest struct {
	Name                 string  `json:"name"`
	Description          *string `json:"description,omitempty"`
	CredentialID         string  `json:"credential_id"`
	SSHKeyCredentialID   string  `json:"ssh_key_credential_id"`
	Region               string  `json:"region"`
	NetworkVlanID        int     `json:"network_vlan_id"`
	SubnetCIDR           string  `json:"subnet_cidr"`
	DHCPStart            string  `json:"dhcp_start"`
	DHCPEnd              string  `json:"dhcp_end"`
	GatewayFlavorID      string  `json:"gateway_flavor_id"`
	ControlPlaneCount    int     `json:"control_plane_count"`
	ControlPlaneFlavorID string  `json:"control_plane_flavor_id"`
	WorkerCount          int     `json:"worker_count"`
	WorkerFlavorID       string  `json:"worker_flavor_id"`
	Distribution         string  `json:"distribution"`
	KubernetesVersion    *string `json:"kubernetes_version,omitempty"`
}

type CreateOvhClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionOvhClusterResponse struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	DeletedServers  []string `json:"deleted_servers"`
	DeletedNetworks []string `json:"deleted_networks"`
	DeletedSSHKeys  []string `json:"deleted_ssh_keys"`
	Errors          []string `json:"errors"`
}

func (c *Client) CreateOvhCluster(req CreateOvhClusterRequest) (*CreateOvhClusterResponse, error) {
	url := c.BaseURL + "/api/v1/clusters/ovh"
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result CreateOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeprovisionOvhCluster(clusterID string) (*DeprovisionOvhClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s", c.BaseURL, clusterID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result DeprovisionOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetOvhWorkerCount(clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/worker-count", c.BaseURL, clusterID)
	var result WorkerCountResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetOvhK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/k8s-version", c.BaseURL, clusterID)
	var result K8sVersionInfo
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpgradeOvhK8sVersion(clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/upgrade-k8s-version", c.BaseURL, clusterID)
	return c.doUpgradeK8sVersion(url, targetVersion)
}

func (c *Client) ListOvhNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups", c.BaseURL, clusterID)
	var result NodeGroupListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AddOvhNodeGroup(clusterID string, req AddNodeGroupRequest) (*AddNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups", c.BaseURL, clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doAddNodeGroup(url, payload)
}

func (c *Client) ScaleOvhNodeGroup(clusterID, groupName string, count int) (*ScaleNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/scale", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doScaleNodeGroup(url, payload)
}

func (c *Client) UpdateOvhNodeGroupInstanceType(clusterID, groupName, instanceType string) (*UpdateNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/instance-type", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroupInstanceType(url, payload)
}

func (c *Client) DeleteOvhNodeGroup(clusterID, groupName string) (*DeleteNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s", c.BaseURL, clusterID, groupName)
	return c.doDeleteNodeGroup(url)
}

func (c *Client) ScaleOvhWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/scale-workers", c.BaseURL, clusterID)
	return c.doScaleWorkers(url, workerCount)
}
