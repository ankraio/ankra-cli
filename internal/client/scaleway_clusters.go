package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const scalewayKind = "scaleway"

// CreateScalewayClusterRequest mirrors the cluster-api decoder for
// POST /api/v1/clusters/scaleway (providerapi.decodeCreateScalewayClusterRequest).
// Omitted optional members take the server's default, so the zero value of an
// omitempty field means "let the server decide" rather than "send zero":
// gateway_type VPC-GW-S, bastion_port 61000, control_plane_type / worker_type /
// etcd_type DEV1-M, distribution k3s, etcd_topology stacked, etcd_node_count 3,
// gitops_branch master, retention_policy retain.
//
// Scaleway has no external_cloud_provider member: its networking rides the
// Public Gateway rather than a CCM.
type CreateScalewayClusterRequest struct {
	Name                 string                `json:"name"`
	Description          *string               `json:"description,omitempty"`
	CredentialID         string                `json:"credential_id"`
	RuntimeCredentialID  *string               `json:"runtime_credential_id,omitempty"`
	SSHKeyCredentialID   string                `json:"ssh_key_credential_id"`
	Region               string                `json:"region"`
	Zone                 string                `json:"zone"`
	PrivateNetworkID     *string               `json:"private_network_id,omitempty"`
	NetworkIPRange       *string               `json:"network_ip_range,omitempty"`
	GatewayType          string                `json:"gateway_type,omitempty"`
	BastionPort          int                   `json:"bastion_port,omitempty"`
	GatewayAllowedIPs    []string              `json:"gateway_allowed_ips,omitempty"`
	ControlPlaneCount    int                   `json:"control_plane_count,omitempty"`
	ControlPlaneType     string                `json:"control_plane_type,omitempty"`
	WorkerCount          *int                  `json:"worker_count,omitempty"`
	WorkerType           string                `json:"worker_type,omitempty"`
	Distribution         string                `json:"distribution,omitempty"`
	KubernetesVersion    *string               `json:"kubernetes_version,omitempty"`
	EtcdTopology         string                `json:"etcd_topology,omitempty"`
	EtcdNodeCount        int                   `json:"etcd_node_count,omitempty"`
	EtcdType             string                `json:"etcd_type,omitempty"`
	CNI                  string                `json:"cni,omitempty"`
	NodeGroups           []AddNodeGroupRequest `json:"node_groups,omitempty"`
	GitopsCredentialName *string               `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string               `json:"gitops_repository,omitempty"`
	GitopsBranch         *string               `json:"gitops_branch,omitempty"`
	IncludeNetworking    *bool                 `json:"include_networking,omitempty"`
	IncludeDNS           *bool                 `json:"include_dns,omitempty"`
	RetentionPolicy      string                `json:"retention_policy,omitempty"`
}

type CreateScalewayClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

// ScalewayPreflightItem is one check from POST /clusters/scaleway/preflight.
// Scaleway is the only provider exposing preflight as a route; the check
// proves availability, not hard quota.
type ScalewayPreflightItem struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ScalewayPreflightResult struct {
	CanProceed bool                    `json:"can_proceed"`
	Items      []ScalewayPreflightItem `json:"items"`
}

// ScalewayLocation is one region/zone pair from the locations catalog.
type ScalewayLocation struct {
	Region string   `json:"region"`
	Zones  []string `json:"zones"`
}

// ScalewayInstanceType is one commercial type from the instance-types catalog.
type ScalewayInstanceType struct {
	Name         string  `json:"name"`
	CPUs         int     `json:"cpus"`
	MemoryGB     float64 `json:"memory_gb"`
	Architecture string  `json:"architecture"`
	MonthlyPrice float64 `json:"monthly_price"`
	Currency     string  `json:"currency"`
}

type ScalewayGatewayType struct {
	Name         string  `json:"name"`
	MonthlyPrice float64 `json:"monthly_price"`
	Currency     string  `json:"currency"`
}

type ScalewayNetwork struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Subnets []string `json:"subnets"`
}

// ScalewayCatalogResult is the envelope the catalog routes return. Pricing is
// deliberately allowed to be incomplete: gateway, flexible IP and dependent
// networking prices are not published, so PricingComplete can be false with
// IncompleteReasons naming what is missing.
type ScalewayCatalogResult struct {
	Locations        []ScalewayLocation     `json:"locations,omitempty"`
	InstanceTypes    []ScalewayInstanceType `json:"instance_types,omitempty"`
	GatewayTypes     []ScalewayGatewayType  `json:"gateway_types,omitempty"`
	Networks         []ScalewayNetwork      `json:"networks,omitempty"`
	PricingComplete  bool                   `json:"pricing_complete"`
	IncompleteReason []string               `json:"incomplete_reasons,omitempty"`
}

func (c *Client) CreateScalewayCluster(request CreateScalewayClusterRequest) (*CreateScalewayClusterResponse, error) {
	var result CreateScalewayClusterResponse
	if createError := c.createProviderCluster(scalewayKind, request, &result); createError != nil {
		return nil, createError
	}
	return &result, nil
}

// PreflightScalewayCluster validates a create request without provisioning.
func (c *Client) PreflightScalewayCluster(request CreateScalewayClusterRequest) (*ScalewayPreflightResult, error) {
	endpoint := c.BaseURL + "/api/v1/clusters/scaleway/preflight"
	payload, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	httpResponse, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(httpResponse)

	body, readError := readResponseBody(httpResponse)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if httpResponse.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("preflight failed", httpResponse.StatusCode, redactedBodyForError(body, 500))
	}

	var result ScalewayPreflightResult
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) DeprovisionScalewayCluster(clusterID string) (*ProviderDeprovisionClusterResponse, error) {
	return c.deprovisionProviderCluster(scalewayKind, clusterID, false)
}

func (c *Client) StopScalewayCluster(clusterID string, force bool) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(scalewayKind, clusterID, force)
}

func (c *Client) StartScalewayCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(scalewayKind, clusterID, scope)
}

func (c *Client) GetScalewayWorkerCount(clusterID string) (*WorkerCountResult, error) {
	return c.getProviderWorkerCount(scalewayKind, clusterID)
}

func (c *Client) ScaleScalewayWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	return c.scaleProviderWorkers(scalewayKind, clusterID, workerCount)
}

func (c *Client) GetScalewayK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	return c.getProviderK8sVersion(scalewayKind, clusterID)
}

func (c *Client) UpgradeScalewayK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.upgradeProviderK8sVersion(scalewayKind, clusterID, targetVersion, force)
}

func (c *Client) ListScalewayNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	return c.listProviderNodeGroups(scalewayKind, clusterID)
}

func (c *Client) AddScalewayNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	return c.addProviderNodeGroup(ctx, scalewayKind, clusterID, request, wait)
}

func (c *Client) ScaleScalewayNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	return c.scaleProviderNodeGroup(ctx, scalewayKind, clusterID, groupName, count, wait)
}

func (c *Client) UpdateScalewayNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateProviderNodeGroupInstanceType(ctx, scalewayKind, clusterID, groupName, instanceType, wait)
}

func (c *Client) UpdateScalewayNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateScalewayNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteScalewayNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	return c.deleteProviderNodeGroup(ctx, scalewayKind, clusterID, groupName, wait)
}

func (c *Client) GetScalewayNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	return c.getProviderNodeGroupAutoscaling(scalewayKind, clusterID, groupName)
}

func (c *Client) UpdateScalewayNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	return c.updateProviderNodeGroupAutoscaling(ctx, scalewayKind, clusterID, groupName, request, wait)
}

func (c *Client) GetScalewayControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane(scalewayKind, clusterID)
}

func (c *Client) ChangeScalewayControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount(scalewayKind, clusterID, count)
}

func (c *Client) ChangeScalewayControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType(scalewayKind, clusterID, instanceType)
}

func (c *Client) ListScalewayClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(scalewayKind, clusterID)
}

func (c *Client) GetScalewayClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(scalewayKind, clusterID, nodeID)
}

func (c *Client) RestartScalewayClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode(scalewayKind, clusterID, nodeID)
}

func (c *Client) GetScalewayClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys(scalewayKind, clusterID)
}

func (c *Client) UpdateScalewayClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys(scalewayKind, clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncScalewayClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys(scalewayKind, clusterID)
}

// scalewayCatalog reads one of the credential-scoped provider catalogs.
func (c *Client) scalewayCatalog(catalog, credentialID, region, zone string) (*ScalewayCatalogResult, error) {
	query := url.Values{}
	query.Set("credential_id", credentialID)
	if region != "" {
		query.Set("region", region)
	}
	if zone != "" {
		query.Set("zone", zone)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s?%s", c.BaseURL, catalog, query.Encode())
	var result ScalewayCatalogResult
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) ListScalewayLocations(credentialID string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog("locations", credentialID, "", "")
}

func (c *Client) ListScalewayInstanceTypes(credentialID, region, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog("instance-types", credentialID, region, zone)
}

func (c *Client) ListScalewayGatewayTypes(credentialID, region, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog("gateway-types", credentialID, region, zone)
}

func (c *Client) ListScalewayNetworks(credentialID, region, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog("networks", credentialID, region, zone)
}
