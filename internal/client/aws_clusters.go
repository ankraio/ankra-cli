package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const awsKind = "aws"

// CreateAwsClusterRequest mirrors the cluster-api decoder for
// POST /api/v1/clusters/aws (ankra-rtpno): an Ankra-managed k3s or kubeadm
// cluster on EC2 inside a VPC the operator already owns. Omitted optional
// members take the server's default, so the zero value of an omitempty field
// means "let the server decide" rather than "send zero": distribution k3s,
// etcd_topology stacked, etcd_node_count 3, retention_policy retain, and the
// egress mode is auto-detected from the node subnets when it is omitted.
//
// The VPC, the node subnets and the bastion subnet are adopted, never
// created: AWS networking is the operator's, Ankra owns only the instances,
// security groups, generated SSH key and (with egress_mode bastion_nat) the
// NAT role the bastion plays.
type CreateAwsClusterRequest struct {
	Name                  string                `json:"name"`
	Description           *string               `json:"description,omitempty"`
	CredentialID          string                `json:"credential_id"`
	SSHKeyCredentialID    string                `json:"ssh_key_credential_id"`
	Region                string                `json:"region"`
	VpcID                 string                `json:"vpc_id"`
	NodeSubnetIDs         []string              `json:"node_subnet_ids"`
	BastionSubnetID       string                `json:"bastion_subnet_id"`
	EgressMode            string                `json:"egress_mode,omitempty"`
	BastionInstanceType   string                `json:"bastion_instance_type,omitempty"`
	BastionAllowedIPs     []string              `json:"bastion_allowed_ips"`
	ControlPlaneCount     int                   `json:"control_plane_count,omitempty"`
	ControlPlaneType      string                `json:"control_plane_type,omitempty"`
	WorkerCount           *int                  `json:"worker_count,omitempty"`
	WorkerType            string                `json:"worker_type,omitempty"`
	NodeGroups            []AddNodeGroupRequest `json:"node_groups,omitempty"`
	Distribution          string                `json:"distribution,omitempty"`
	KubernetesVersion     *string               `json:"kubernetes_version,omitempty"`
	EtcdTopology          string                `json:"etcd_topology,omitempty"`
	EtcdNodeCount         int                   `json:"etcd_node_count,omitempty"`
	EtcdType              string                `json:"etcd_type,omitempty"`
	CNI                   string                `json:"cni,omitempty"`
	CNIFeatures           []string              `json:"cni_features,omitempty"`
	K3sDisabledComponents []string              `json:"k3s_disabled_components,omitempty"`
	UbuntuSeries          string                `json:"ubuntu_series,omitempty"`
	Architecture          string                `json:"architecture,omitempty"`
	RootVolumeGiB         int                   `json:"root_volume_gib,omitempty"`
	GitopsCredentialName  *string               `json:"gitops_credential_name,omitempty"`
	GitopsRepository      *string               `json:"gitops_repository,omitempty"`
	GitopsBranch          *string               `json:"gitops_branch,omitempty"`
	IncludeNetworking     *bool                 `json:"include_networking,omitempty"`
	IncludeDNS            *bool                 `json:"include_dns,omitempty"`
	RetentionPolicy       string                `json:"retention_policy,omitempty"`
	Classification        string                `json:"classification,omitempty"`
}

type CreateAwsClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

// AwsPreflightItem is one check from POST /api/v1/clusters/aws/preflight.
type AwsPreflightItem struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// AwsPreflightResult carries the checks plus the egress mode the server
// settled on: when the request left egress_mode unset the server resolves
// it from the node subnets' route tables, and ResolvedEgressMode is the
// only place that decision is reported before the cluster is built.
type AwsPreflightResult struct {
	CanProceed         bool               `json:"can_proceed"`
	Items              []AwsPreflightItem `json:"items"`
	ResolvedEgressMode string             `json:"resolved_egress_mode,omitempty"`
}

// AwsRegion is one region from the regions catalog.
type AwsRegion struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// AwsInstanceType is one EC2 instance type from the instance-types catalog.
type AwsInstanceType struct {
	Name         string  `json:"name"`
	VCPUs        int     `json:"vcpus"`
	MemoryGiB    float64 `json:"memory_gib"`
	Architecture string  `json:"architecture"`
	HourlyPrice  float64 `json:"hourly_price"`
	MonthlyPrice float64 `json:"monthly_price"`
	Currency     string  `json:"currency"`
}

// AwsVpc is one VPC the credential can adopt.
type AwsVpc struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	IsDefault bool   `json:"is_default"`
}

// AwsSubnet is one subnet of a VPC; Public reports whether its route table
// reaches an internet gateway, which is what decides the egress mode.
type AwsSubnet struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	CIDR             string `json:"cidr"`
	AvailabilityZone string `json:"availability_zone"`
	VpcID            string `json:"vpc_id"`
	Public           bool   `json:"public"`
}

type AwsAvailabilityZone struct {
	Name   string `json:"name"`
	ZoneID string `json:"zone_id"`
	State  string `json:"state"`
}

// AwsImage is one Ubuntu AMI the platform will boot nodes from.
type AwsImage struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	UbuntuSeries string `json:"ubuntu_series"`
	Architecture string `json:"architecture"`
	CreatedAt    string `json:"created_at"`
}

// AwsPriceItem is one priced line from the pricing catalog.
type AwsPriceItem struct {
	Item         string  `json:"item"`
	HourlyPrice  float64 `json:"hourly_price"`
	MonthlyPrice float64 `json:"monthly_price"`
	Currency     string  `json:"currency"`
}

// AwsCatalogResult is the envelope the AWS catalog routes return. Each route
// fills its own member; PricingComplete and IncompleteReasons follow the
// Scaleway catalog convention so a partial price list says what is missing.
type AwsCatalogResult struct {
	Regions           []AwsRegion           `json:"regions,omitempty"`
	InstanceTypes     []AwsInstanceType     `json:"instance_types,omitempty"`
	Vpcs              []AwsVpc              `json:"vpcs,omitempty"`
	Subnets           []AwsSubnet           `json:"subnets,omitempty"`
	AvailabilityZones []AwsAvailabilityZone `json:"availability_zones,omitempty"`
	Images            []AwsImage            `json:"images,omitempty"`
	Pricing           []AwsPriceItem        `json:"pricing,omitempty"`
	PricingComplete   bool                  `json:"pricing_complete"`
	IncompleteReasons []string              `json:"incomplete_reasons,omitempty"`
}

func (c *Client) CreateAwsCluster(request CreateAwsClusterRequest) (*CreateAwsClusterResponse, error) {
	var result CreateAwsClusterResponse
	if createError := c.createProviderCluster(awsKind, request, &result); createError != nil {
		return nil, createError
	}
	return &result, nil
}

// PreflightAwsCluster validates a create request without provisioning.
func (c *Client) PreflightAwsCluster(request CreateAwsClusterRequest) (*AwsPreflightResult, error) {
	endpoint := c.BaseURL + "/api/v1/clusters/aws/preflight"
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

	var result AwsPreflightResult
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) DeprovisionAwsCluster(clusterID string) (*ProviderDeprovisionClusterResponse, error) {
	return c.deprovisionProviderCluster(awsKind, clusterID, false)
}

func (c *Client) StopAwsCluster(clusterID string, force bool) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(awsKind, clusterID, force)
}

func (c *Client) StartAwsCluster(clusterID, scope string) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(awsKind, clusterID, scope)
}

func (c *Client) GetAwsWorkerCount(clusterID string) (*WorkerCountResult, error) {
	return c.getProviderWorkerCount(awsKind, clusterID)
}

func (c *Client) ScaleAwsWorkers(clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	return c.scaleProviderWorkers(awsKind, clusterID, workerCount)
}

func (c *Client) GetAwsK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	return c.getProviderK8sVersion(awsKind, clusterID)
}

func (c *Client) UpgradeAwsK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.upgradeProviderK8sVersion(awsKind, clusterID, targetVersion, force)
}

func (c *Client) ListAwsNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	return c.listProviderNodeGroups(awsKind, clusterID)
}

func (c *Client) AddAwsNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	return c.addProviderNodeGroup(ctx, awsKind, clusterID, request, wait)
}

func (c *Client) ScaleAwsNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	return c.scaleProviderNodeGroup(ctx, awsKind, clusterID, groupName, count, wait)
}

func (c *Client) UpdateAwsNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateProviderNodeGroupInstanceType(ctx, awsKind, clusterID, groupName, instanceType, wait)
}

func (c *Client) UpdateAwsNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/aws/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateAwsNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/aws/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteAwsNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	return c.deleteProviderNodeGroup(ctx, awsKind, clusterID, groupName, wait)
}

func (c *Client) GetAwsNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	return c.getProviderNodeGroupAutoscaling(awsKind, clusterID, groupName)
}

func (c *Client) UpdateAwsNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	return c.updateProviderNodeGroupAutoscaling(ctx, awsKind, clusterID, groupName, request, wait)
}

func (c *Client) GetAwsControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane(awsKind, clusterID)
}

func (c *Client) ChangeAwsControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount(awsKind, clusterID, count)
}

func (c *Client) ChangeAwsControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType(awsKind, clusterID, instanceType)
}

func (c *Client) ListAwsClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(awsKind, clusterID)
}

func (c *Client) GetAwsClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(awsKind, clusterID, nodeID)
}

func (c *Client) RestartAwsClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode(awsKind, clusterID, nodeID)
}

func (c *Client) AwsNodeCloudInitLog(clusterID, nodeID string) (*NodeCloudInitLogResult, error) {
	return c.nodeCloudInitLog(awsKind, clusterID, nodeID)
}

func (c *Client) GetAwsBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth(awsKind, clusterID)
}

func (c *Client) DiagnoseAwsBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, awsKind, clusterID)
}

func (c *Client) GetAwsClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys(awsKind, clusterID)
}

func (c *Client) UpdateAwsClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys(awsKind, clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncAwsClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys(awsKind, clusterID)
}

// GetAwsAccessInfo reads the bastion and control plane addresses plus the
// SSH jump details, the same shape the OVH access-info route answers.
func (c *Client) GetAwsAccessInfo(clusterID string) (*ClusterAccessInfo, error) {
	var result ClusterAccessInfo
	if getError := c.getJSON(c.providerClusterURL(awsKind, clusterID, "access-info"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

// awsCatalog reads one of the credential-scoped AWS catalogs. Every catalog
// takes the credential; region and vpc_id are sent only when given, so a
// region-less read (regions) and a VPC-scoped one (subnets) share the path
// shape.
func (c *Client) awsCatalog(catalog, credentialID, region, vpcID string) (*AwsCatalogResult, error) {
	query := url.Values{}
	query.Set("credential_id", credentialID)
	if region != "" {
		query.Set("region", region)
	}
	if vpcID != "" {
		query.Set("vpc_id", vpcID)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/aws/%s?%s", c.BaseURL, catalog, query.Encode())
	var result AwsCatalogResult
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) ListAwsRegions(credentialID string) (*AwsCatalogResult, error) {
	return c.awsCatalog("regions", credentialID, "", "")
}

func (c *Client) ListAwsInstanceTypes(credentialID, region string) (*AwsCatalogResult, error) {
	return c.awsCatalog("instance-types", credentialID, region, "")
}

func (c *Client) ListAwsVpcs(credentialID, region string) (*AwsCatalogResult, error) {
	return c.awsCatalog("vpcs", credentialID, region, "")
}

func (c *Client) ListAwsSubnets(credentialID, region, vpcID string) (*AwsCatalogResult, error) {
	return c.awsCatalog("subnets", credentialID, region, vpcID)
}

func (c *Client) ListAwsAvailabilityZones(credentialID, region string) (*AwsCatalogResult, error) {
	return c.awsCatalog("availability-zones", credentialID, region, "")
}

func (c *Client) ListAwsImages(credentialID, region string) (*AwsCatalogResult, error) {
	return c.awsCatalog("images", credentialID, region, "")
}

func (c *Client) ListAwsPricing(credentialID, region string) (*AwsCatalogResult, error) {
	return c.awsCatalog("pricing", credentialID, region, "")
}

// ListAwsClusterInstanceTypes reads the instance types available to an
// existing cluster, scoped by the cluster's own credential and region so the
// caller does not have to repeat them.
func (c *Client) ListAwsClusterInstanceTypes(clusterID string) (*AwsCatalogResult, error) {
	var result AwsCatalogResult
	if getError := c.getJSON(c.providerClusterURL(awsKind, clusterID, "instance-types"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}
