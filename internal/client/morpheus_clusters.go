package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const morpheusKind = "morpheus"

type CreateMorpheusClusterRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	CredentialID       string  `json:"credential_id"`
	SSHKeyCredentialID string  `json:"ssh_key_credential_id"`
	GroupID            int64   `json:"group_id"`
	CloudID            int64   `json:"cloud_id"`
	NetworkID          int64   `json:"network_id"`
	LayoutID           int64   `json:"layout_id"`
	VirtualImageID     *int64  `json:"virtual_image_id,omitempty"`
	BastionPlanID      int64   `json:"bastion_plan_id"`
	ControlPlaneCount  int     `json:"control_plane_count"`
	ControlPlanePlanID int64   `json:"control_plane_plan_id"`
	WorkerCount        int     `json:"worker_count"`
	WorkerPlanID       int64   `json:"worker_plan_id"`
	Distribution       string  `json:"distribution"`
	KubernetesVersion  *string `json:"kubernetes_version,omitempty"`
	EtcdTopology       string  `json:"etcd_topology,omitempty"`
	EtcdNodeCount      int     `json:"etcd_node_count,omitempty"`
	EtcdPlanID         *int64  `json:"etcd_plan_id,omitempty"`
	CNI                string  `json:"cni,omitempty"`
	IncludeNetworking  bool    `json:"include_networking"`
}

type CreateMorpheusClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type MorpheusGroup struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Location *string `json:"location"`
}

type MorpheusCloud struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	CloudType *string `json:"cloud_type"`
}

type MorpheusPlan struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Code           *string `json:"code"`
	MaxCores       *int64  `json:"max_cores"`
	MaxMemoryBytes *int64  `json:"max_memory_bytes"`
}

type MorpheusLayout struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	InstanceTypeID   int64   `json:"instance_type_id"`
	InstanceTypeName string  `json:"instance_type_name"`
	ProvisionType    *string `json:"provision_type"`
}

type MorpheusNetwork struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	CIDR    *string `json:"cidr"`
	CloudID *int64  `json:"cloud_id"`
}

func (c *Client) CreateMorpheusCluster(request CreateMorpheusClusterRequest) (*CreateMorpheusClusterResponse, error) {
	var result CreateMorpheusClusterResponse
	if createError := c.createProviderCluster(morpheusKind, request, &result); createError != nil {
		return nil, createError
	}
	return &result, nil
}

func (c *Client) DeprovisionMorpheusCluster(clusterID string) (*ProviderDeprovisionClusterResponse, error) {
	return c.deprovisionProviderCluster(morpheusKind, clusterID)
}

func (c *Client) StopMorpheusCluster(clusterID string) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(morpheusKind, clusterID)
}

func (c *Client) StartMorpheusCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(morpheusKind, clusterID, scope)
}

func (c *Client) GetMorpheusWorkerCount(clusterID string) (*WorkerCountResult, error) {
	return c.getProviderWorkerCount(morpheusKind, clusterID)
}

func (c *Client) ScaleMorpheusWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	return c.scaleProviderWorkers(morpheusKind, clusterID, workerCount)
}

func (c *Client) GetMorpheusK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	return c.getProviderK8sVersion(morpheusKind, clusterID)
}

func (c *Client) UpgradeMorpheusK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.upgradeProviderK8sVersion(morpheusKind, clusterID, targetVersion, force)
}

func (c *Client) ListMorpheusNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	return c.listProviderNodeGroups(morpheusKind, clusterID)
}

func (c *Client) AddMorpheusNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	return c.addProviderNodeGroup(ctx, morpheusKind, clusterID, request, wait)
}

func (c *Client) ScaleMorpheusNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	return c.scaleProviderNodeGroup(ctx, morpheusKind, clusterID, groupName, count, wait)
}

func (c *Client) UpdateMorpheusNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateProviderNodeGroupInstanceType(ctx, morpheusKind, clusterID, groupName, instanceType, wait)
}

func (c *Client) UpdateMorpheusNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/morpheus/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateMorpheusNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/morpheus/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, err := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteMorpheusNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	return c.deleteProviderNodeGroup(ctx, morpheusKind, clusterID, groupName, wait)
}

func (c *Client) GetMorpheusNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	return c.getProviderNodeGroupAutoscaling(morpheusKind, clusterID, groupName)
}

func (c *Client) UpdateMorpheusNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	return c.updateProviderNodeGroupAutoscaling(ctx, morpheusKind, clusterID, groupName, request, wait)
}

func (c *Client) GetMorpheusControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane(morpheusKind, clusterID)
}

func (c *Client) ChangeMorpheusControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount(morpheusKind, clusterID, count)
}

func (c *Client) ChangeMorpheusControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType(morpheusKind, clusterID, instanceType)
}

func (c *Client) ListMorpheusClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(morpheusKind, clusterID)
}

func (c *Client) GetMorpheusClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(morpheusKind, clusterID, nodeID)
}

func (c *Client) GetMorpheusClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys(morpheusKind, clusterID)
}

func (c *Client) UpdateMorpheusClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys(morpheusKind, clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncMorpheusClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys(morpheusKind, clusterID)
}

func (c *Client) listMorpheusCatalog(catalog, credentialID string, result interface{}) error {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/morpheus/%s?credential_id=%s",
		c.BaseURL, catalog, url.QueryEscape(credentialID))
	return c.getJSON(endpoint, result)
}

func (c *Client) ListMorpheusGroups(credentialID string) ([]MorpheusGroup, error) {
	var result []MorpheusGroup
	if getError := c.listMorpheusCatalog("groups", credentialID, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListMorpheusClouds(credentialID string) ([]MorpheusCloud, error) {
	var result []MorpheusCloud
	if getError := c.listMorpheusCatalog("clouds", credentialID, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListMorpheusPlans(credentialID string) ([]MorpheusPlan, error) {
	var result []MorpheusPlan
	if getError := c.listMorpheusCatalog("plans", credentialID, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListMorpheusLayouts(credentialID string) ([]MorpheusLayout, error) {
	var result []MorpheusLayout
	if getError := c.listMorpheusCatalog("layouts", credentialID, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}

func (c *Client) ListMorpheusNetworks(credentialID string) ([]MorpheusNetwork, error) {
	var result []MorpheusNetwork
	if getError := c.listMorpheusCatalog("networks", credentialID, &result); getError != nil {
		return nil, getError
	}
	return result, nil
}
