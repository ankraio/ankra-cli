package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const proxmoxKind = "proxmox"

type CreateProxmoxClusterRequest struct {
	Name                     string   `json:"name"`
	Description              *string  `json:"description,omitempty"`
	CredentialID             string   `json:"credential_id"`
	SSHKeyCredentialID       string   `json:"ssh_key_credential_id"`
	Node                     string   `json:"node"`
	PlacementNodes           []string `json:"placement_nodes,omitempty"`
	Bridge                   string   `json:"bridge"`
	Storage                  string   `json:"storage,omitempty"`
	Template                 string   `json:"template,omitempty"`
	BastionInstanceType      string   `json:"bastion_instance_type"`
	ControlPlaneCount        int      `json:"control_plane_count"`
	ControlPlaneInstanceType string   `json:"control_plane_instance_type"`
	WorkerCount              int      `json:"worker_count"`
	WorkerInstanceType       string   `json:"worker_instance_type"`
	Distribution             string   `json:"distribution"`
	KubernetesVersion        *string  `json:"kubernetes_version,omitempty"`
	EtcdTopology             string   `json:"etcd_topology,omitempty"`
	EtcdNodeCount            int      `json:"etcd_node_count,omitempty"`
	EtcdInstanceType         string   `json:"etcd_instance_type,omitempty"`
	CNI                      string   `json:"cni,omitempty"`
	IncludeNetworking        bool     `json:"include_networking"`
}

type CreateProxmoxClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

// ProxmoxNode is one Proxmox VE host node from the provider catalog.
type ProxmoxNode struct {
	Node        string `json:"node"`
	Status      string `json:"status"`
	CPUCount    int    `json:"cpu_count"`
	MemoryBytes int64  `json:"memory_bytes"`
}

type ProxmoxStorage struct {
	Storage        string   `json:"storage"`
	Type           string   `json:"type"`
	Content        []string `json:"content"`
	Active         bool     `json:"active"`
	Shared         bool     `json:"shared"`
	AvailableBytes int64    `json:"available_bytes"`
	TotalBytes     int64    `json:"total_bytes"`
}

type ProxmoxBridge struct {
	Name     string  `json:"name"`
	Active   bool    `json:"active"`
	CIDR     *string `json:"cidr"`
	Comments *string `json:"comments"`
}

type ProxmoxTemplate struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	Node string `json:"node"`
}

type ProxmoxSize struct {
	Slug      string `json:"slug"`
	VCPUs     int    `json:"vcpus"`
	MemoryMB  int    `json:"memory_mb"`
	DiskGB    int    `json:"disk_gb"`
	Available bool   `json:"available"`
}

func (c *Client) CreateProxmoxCluster(request CreateProxmoxClusterRequest) (*CreateProxmoxClusterResponse, error) {
	var result CreateProxmoxClusterResponse
	if createError := c.createProviderCluster(proxmoxKind, request, &result); createError != nil {
		return nil, createError
	}
	return &result, nil
}

func (c *Client) DeprovisionProxmoxCluster(clusterID string) (*ProviderDeprovisionClusterResponse, error) {
	return c.deprovisionProviderCluster(proxmoxKind, clusterID)
}

func (c *Client) StopProxmoxCluster(clusterID string) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(proxmoxKind, clusterID)
}

func (c *Client) StartProxmoxCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(proxmoxKind, clusterID, scope)
}

func (c *Client) GetProxmoxWorkerCount(clusterID string) (*WorkerCountResult, error) {
	return c.getProviderWorkerCount(proxmoxKind, clusterID)
}

func (c *Client) ScaleProxmoxWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	return c.scaleProviderWorkers(proxmoxKind, clusterID, workerCount)
}

func (c *Client) GetProxmoxK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	return c.getProviderK8sVersion(proxmoxKind, clusterID)
}

func (c *Client) UpgradeProxmoxK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.upgradeProviderK8sVersion(proxmoxKind, clusterID, targetVersion, force)
}

func (c *Client) ListProxmoxNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	return c.listProviderNodeGroups(proxmoxKind, clusterID)
}

func (c *Client) AddProxmoxNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	return c.addProviderNodeGroup(ctx, proxmoxKind, clusterID, request, wait)
}

func (c *Client) ScaleProxmoxNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	return c.scaleProviderNodeGroup(ctx, proxmoxKind, clusterID, groupName, count, wait)
}

func (c *Client) UpdateProxmoxNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateProviderNodeGroupInstanceType(ctx, proxmoxKind, clusterID, groupName, instanceType, wait)
}

func (c *Client) UpdateProxmoxNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/proxmox/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateProxmoxNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/proxmox/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteProxmoxNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	return c.deleteProviderNodeGroup(ctx, proxmoxKind, clusterID, groupName, wait)
}

func (c *Client) GetProxmoxNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	return c.getProviderNodeGroupAutoscaling(proxmoxKind, clusterID, groupName)
}

func (c *Client) UpdateProxmoxNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	return c.updateProviderNodeGroupAutoscaling(ctx, proxmoxKind, clusterID, groupName, request, wait)
}

func (c *Client) GetProxmoxControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane(proxmoxKind, clusterID)
}

func (c *Client) ChangeProxmoxControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount(proxmoxKind, clusterID, count)
}

func (c *Client) ChangeProxmoxControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType(proxmoxKind, clusterID, instanceType)
}

func (c *Client) ListProxmoxClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(proxmoxKind, clusterID)
}

func (c *Client) GetProxmoxClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(proxmoxKind, clusterID, nodeID)
}

func (c *Client) GetProxmoxClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys(proxmoxKind, clusterID)
}

func (c *Client) UpdateProxmoxClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys(proxmoxKind, clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncProxmoxClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys(proxmoxKind, clusterID)
}

// ListProxmoxNodes lists the Proxmox VE host nodes the credential can deploy
// on.
func (c *Client) ListProxmoxNodes(credentialID string) ([]ProxmoxNode, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/proxmox/nodes?credential_id=%s", c.BaseURL, url.QueryEscape(credentialID))
	var result []ProxmoxNode
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) listProxmoxNodeScopedCatalog(catalog, credentialID, node string, result interface{}) error {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/proxmox/%s?credential_id=%s&node=%s",
		c.BaseURL, catalog, url.QueryEscape(credentialID), url.QueryEscape(node))
	return c.getJSON(endpoint, result)
}

func (c *Client) ListProxmoxStorages(credentialID, node string) ([]ProxmoxStorage, error) {
	var result []ProxmoxStorage
	if getError := c.listProxmoxNodeScopedCatalog("storages", credentialID, node, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListProxmoxBridges(credentialID, node string) ([]ProxmoxBridge, error) {
	var result []ProxmoxBridge
	if getError := c.listProxmoxNodeScopedCatalog("bridges", credentialID, node, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListProxmoxTemplates(credentialID, node string) ([]ProxmoxTemplate, error) {
	var result []ProxmoxTemplate
	if getError := c.listProxmoxNodeScopedCatalog("templates", credentialID, node, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

// ListProxmoxSizes lists the static Proxmox instance-size presets.
func (c *Client) ListProxmoxSizes() ([]ProxmoxSize, error) {
	endpoint := c.BaseURL + "/api/v1/clusters/proxmox/sizes"
	var result []ProxmoxSize
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}
