package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type OvhRegionListResult struct {
	Regions []string `json:"regions"`
}

type CreateOvhClusterRequest struct {
	Name                  string  `json:"name"`
	Description           *string `json:"description,omitempty"`
	CredentialID          string  `json:"credential_id"`
	SSHKeyCredentialID    string  `json:"ssh_key_credential_id"`
	Region                string  `json:"region"`
	NetworkVlanID         int     `json:"network_vlan_id"`
	SubnetCIDR            string  `json:"subnet_cidr"`
	DHCPStart             string  `json:"dhcp_start"`
	DHCPEnd               string  `json:"dhcp_end"`
	GatewayFlavorID       string  `json:"gateway_flavor_id"`
	ControlPlaneCount     int     `json:"control_plane_count"`
	ControlPlaneFlavorID  string  `json:"control_plane_flavor_id"`
	WorkerCount           int     `json:"worker_count"`
	WorkerFlavorID        string  `json:"worker_flavor_id"`
	Distribution          string  `json:"distribution"`
	KubernetesVersion     *string `json:"kubernetes_version,omitempty"`
	ExternalCloudProvider bool    `json:"external_cloud_provider"`
	IncludeNetworking     bool    `json:"include_networking"`
	GitopsCredentialName  *string `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string `json:"gitops_repository,omitempty"`
	GitopsBranch          *string `json:"gitops_branch,omitempty"`
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

type StopOvhClusterResponse struct {
	Success     bool    `json:"success"`
	ClusterID   string  `json:"cluster_id"`
	OperationID *string `json:"operation_id,omitempty"`
}

type StartOvhClusterResult struct {
	MarkedToStartAt   string `json:"marked_to_start_at"`
	Scope             string `json:"scope"`
	CreatedOperations int    `json:"created_operations"`
}

type ClusterSSHKeyEntry struct {
	CredentialID string `json:"credential_id"`
	Name         string `json:"name"`
}

type ClusterSSHKeysResult struct {
	SSHKeyCredentialIDs []string             `json:"ssh_key_credential_ids"`
	AvailableSSHKeys    []ClusterSSHKeyEntry `json:"available_ssh_keys"`
}

type UpdateClusterSSHKeysRequest struct {
	SSHKeyCredentialIDs []string `json:"ssh_key_credential_ids"`
}

type UpdateClusterSSHKeysResult struct {
	SSHKeyCredentialIDs []string `json:"ssh_key_credential_ids"`
}

type ClusterAccessInfo struct {
	BastionIP       *string  `json:"bastion_ip"`
	ControlPlaneIP  *string  `json:"control_plane_ip"`
	ControlPlaneIPs []string `json:"control_plane_ips"`
	ClusterName     *string  `json:"cluster_name"`
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
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
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
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result DeprovisionOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) ListOvhRegions(credentialID string) (*OvhRegionListResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/regions?credential_id=%s", c.BaseURL, url.QueryEscape(credentialID))
	var result OvhRegionListResult
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
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

func (c *Client) AddOvhNodeGroup(ctx context.Context, clusterID string, req AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups", c.BaseURL, clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doAddNodeGroup(ctx, url, payload, wait)
}

func (c *Client) ScaleOvhNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/scale", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doScaleNodeGroup(ctx, url, payload, wait)
}

func (c *Client) UpdateOvhNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/instance-type", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, url, payload, wait)
}

func (c *Client) UpdateOvhNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateOvhNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteOvhNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s", c.BaseURL, clusterID, groupName)
	return c.doDeleteNodeGroup(ctx, url, wait)
}

func (c *Client) ScaleOvhWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/scale-workers", c.BaseURL, clusterID)
	return c.doScaleWorkers(url, workerCount)
}

func (c *Client) StopOvhCluster(clusterID string) (*StopOvhClusterResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/stop", c.BaseURL, url.PathEscape(clusterID))
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

	var result StopOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) StartOvhCluster(clusterID, scope string) (*StartOvhClusterResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/start", c.BaseURL, url.PathEscape(clusterID))
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

	var result StartOvhClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetOvhClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/ssh-keys", c.BaseURL, url.PathEscape(clusterID))
	var result ClusterSSHKeysResult
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpdateOvhClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/ssh-keys", c.BaseURL, url.PathEscape(clusterID))
	payload, err := json.Marshal(UpdateClusterSSHKeysRequest{SSHKeyCredentialIDs: sshKeyCredentialIDs})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
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
		return nil, fmt.Errorf("update ssh keys failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result UpdateClusterSSHKeysResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetOvhAccessInfo(clusterID string) (*ClusterAccessInfo, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/access-info", c.BaseURL, url.PathEscape(clusterID))
	var result ClusterAccessInfo
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
