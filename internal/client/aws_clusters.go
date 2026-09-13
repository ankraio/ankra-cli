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

// AwsCNIFeatures is the cni_features object of the create body: four
// booleans, each defaulting to false on the server when omitted. Sent only
// when at least one feature is switched on.
type AwsCNIFeatures struct {
	KubeProxyReplacement bool `json:"kube_proxy_replacement,omitempty"`
	Hubble               bool `json:"hubble,omitempty"`
	WireguardEncryption  bool `json:"wireguard_encryption,omitempty"`
	EbpfDataplane        bool `json:"ebpf_dataplane,omitempty"`
}

// CreateAwsClusterRequest mirrors the cluster-api decoder for
// POST /api/v1/clusters/aws (ankra-rtpno): an Ankra-managed k3s or kubeadm
// cluster on EC2. Omitted optional members take the server's default, so the
// zero value of an omitempty field means "let the server decide" rather than
// "send zero": distribution kubeadm, etcd_topology stacked, etcd_node_count
// 3, retention_policy retain, cni cilium.
//
// The network is either created or adopted, and vpc_id is the switch:
//
//   - vpc_id absent (the default): Ankra creates the whole network - VPC,
//     subnets, internet gateway, route tables and (egress_mode nat_gateway)
//     the NAT gateways - from network_ip_range (server default 10.0.0.0/16)
//     across availability_zones (server default: one zone, or three when
//     control_plane_count is 3 or more). egress_mode is nat_gateway (the
//     default) or bastion_nat; nat_gateway_single_zone puts one NAT gateway
//     in the first zone instead of one per zone. node_subnet_ids and
//     bastion_subnet_id must not be sent. The created network is Ankra's and
//     is deleted with the cluster.
//   - vpc_id given: the VPC, node_subnet_ids and bastion_subnet_id are
//     adopted, never created or deleted; egress_mode is existing or
//     bastion_nat, resolved by preflight from the node subnets' route
//     tables when omitted. network_ip_range, availability_zones and
//     nat_gateway_single_zone do not apply.
//
// In both modes Ankra owns the instances, security groups, generated SSH
// key and (with egress_mode bastion_nat) the NAT role the bastion plays.
type CreateAwsClusterRequest struct {
	Name                  string                `json:"name"`
	Description           *string               `json:"description,omitempty"`
	CredentialID          string                `json:"credential_id"`
	SSHKeyCredentialID    string                `json:"ssh_key_credential_id"`
	Region                string                `json:"region"`
	VpcID                 string                `json:"vpc_id,omitempty"`
	NodeSubnetIDs         []string              `json:"node_subnet_ids,omitempty"`
	BastionSubnetID       string                `json:"bastion_subnet_id,omitempty"`
	NetworkIPRange        string                `json:"network_ip_range,omitempty"`
	AvailabilityZones     []string              `json:"availability_zones,omitempty"`
	NatGatewaySingleZone  bool                  `json:"nat_gateway_single_zone,omitempty"`
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
	CNIFeatures           *AwsCNIFeatures       `json:"cni_features,omitempty"`
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
	Environment           *string               `json:"environment,omitempty"`
	Criticality           *string               `json:"criticality,omitempty"`
}

// CreateAwsClusterResponse is AwsCreateClusterResponse: the record the
// create wrote plus the operation carrying the build. OperationID is null
// when the create scheduled no work yet.
type CreateAwsClusterResponse struct {
	ClusterID   string  `json:"cluster_id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	State       string  `json:"state"`
	OperationID *string `json:"operation_id"`
}

// AwsPreflightItem is one check from POST /api/v1/clusters/aws/preflight;
// Status is ok, warning or error.
type AwsPreflightItem struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// AwsNetworkOwnership is the network_ownership a preflight reports: created
// (Ankra builds the VPC and everything in it) or adopted (the request named
// a vpc_id and Ankra builds inside it).
const (
	AwsNetworkOwnershipCreated = "created"
	AwsNetworkOwnershipAdopted = "adopted"
)

// AwsPreflightResult carries the checks plus what the server settled on
// before anything is built: the egress mode (resolved from the node
// subnets' route tables when an adopted-network request left it unset), the
// network ownership, and the availability zones a created network will span
// (the request's own, or the server's one-or-three default). Each is the
// only place that decision is reported before the cluster exists.
//
// ResolvedEgressMode is null when the preflight could not resolve one - a
// failed check, not a mode - so a nil here is "unknown", never "existing".
// NetworkOwnership is empty and ResolvedAvailabilityZones nil when the
// server did not report them, which is likewise "unknown": an empty
// ownership is not "adopted" and a nil zone list is not "no zones".
type AwsPreflightResult struct {
	Items                     []AwsPreflightItem `json:"items"`
	CanProceed                bool               `json:"can_proceed"`
	ResolvedEgressMode        *string            `json:"resolved_egress_mode"`
	NetworkOwnership          string             `json:"network_ownership,omitempty"`
	ResolvedAvailabilityZones []string           `json:"resolved_availability_zones,omitempty"`
}

// AwsRegion is one region from the regions catalog: Slug is the API name
// (eu-north-1), Name the human one (Europe (Stockholm)).
type AwsRegion struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// AwsRegionsCatalog is GET /api/v1/clusters/aws/regions.
type AwsRegionsCatalog struct {
	Regions         []AwsRegion `json:"regions"`
	PricingComplete bool        `json:"pricing_complete"`
}

// AwsInstanceType is one EC2 instance type from the instance-types and
// pricing catalogs. The two prices are USD on-demand and nullable: a nil
// price is one the pricing API did not publish for this type in this
// region, which is not a free instance, so callers render it as unknown.
type AwsInstanceType struct {
	Name              string   `json:"name"`
	VCPUs             int      `json:"vcpus"`
	MemoryGiB         float64  `json:"memory_gib"`
	Architecture      string   `json:"architecture"`
	Category          string   `json:"category"`
	HourlyPriceUSD    *float64 `json:"hourly_price_usd"`
	MonthlyPriceUSD   *float64 `json:"monthly_price_usd"`
	CurrentGeneration bool     `json:"current_generation"`
}

// AwsInstanceTypesCatalog is GET /api/v1/clusters/aws/instance-types and
// GET /api/v1/clusters/aws/{cluster_id}/instance-types. PricingComplete
// and IncompleteReasons say when the price columns are partial.
type AwsInstanceTypesCatalog struct {
	InstanceTypes     []AwsInstanceType `json:"instance_types"`
	PricingComplete   bool              `json:"pricing_complete"`
	IncompleteReasons []string          `json:"incomplete_reasons"`
}

// AwsVpc is one VPC the credential can adopt. DhcpDomainNameState is
// three-valued - set (DhcpDomainName holds the DHCP option set's domain),
// empty (the option set names none) or unknown (the option set could not be
// read) - so a nil DhcpDomainName is not on its own "no domain".
type AwsVpc struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	CIDR                string  `json:"cidr"`
	IsDefault           bool    `json:"is_default"`
	DhcpDomainName      *string `json:"dhcp_domain_name"`
	DhcpDomainNameState string  `json:"dhcp_domain_name_state"`
}

// AwsVpcsCatalog is GET /api/v1/clusters/aws/vpcs.
type AwsVpcsCatalog struct {
	Vpcs []AwsVpc `json:"vpcs"`
}

// AwsSubnetEgress is how a subnet's route table reaches the internet:
// nat_gateway, nat_instance, internet_gateway, transit, none or unknown.
type AwsSubnetEgress struct {
	Kind string `json:"kind"`
}

// AwsSubnet is one subnet of a VPC. Egress decides which egress mode a
// create can use; ForeignInstanceCount is the number of instances in the
// subnet Ankra did not create (bastion_nat is refused when it is non-zero)
// and is null when the instances could not be listed.
type AwsSubnet struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	CIDR                 string          `json:"cidr"`
	AvailabilityZone     string          `json:"availability_zone"`
	MapPublicIPOnLaunch  bool            `json:"map_public_ip_on_launch"`
	Egress               AwsSubnetEgress `json:"egress"`
	ForeignInstanceCount *int            `json:"foreign_instance_count"`
}

// AwsSubnetsCatalog is GET /api/v1/clusters/aws/subnets.
type AwsSubnetsCatalog struct {
	Subnets []AwsSubnet `json:"subnets"`
}

// AwsAvailabilityZone is one zone of a region: Name is the zone name
// (eu-north-1a), ID the account-independent zone id (eun1-az1).
type AwsAvailabilityZone struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	State string `json:"state"`
}

// AwsAvailabilityZonesCatalog is GET /api/v1/clusters/aws/availability-zones.
type AwsAvailabilityZonesCatalog struct {
	Zones []AwsAvailabilityZone `json:"zones"`
}

// AwsImagesCatalog is GET /api/v1/clusters/aws/images: the Ubuntu series
// and CPU architectures a create may ask for, not individual AMIs - the
// platform resolves the AMI itself at build time.
type AwsImagesCatalog struct {
	UbuntuSeries  []string `json:"ubuntu_series"`
	Architectures []string `json:"architectures"`
	DefaultSeries string   `json:"default_series"`
}

// AwsStoragePrice is the EBS line of the pricing catalog; the gp3 price is
// nullable like the instance prices.
type AwsStoragePrice struct {
	Gp3GiBMonthUSD *float64 `json:"gp3_gib_month_usd"`
}

// AwsPricingCatalog is GET /api/v1/clusters/aws/pricing: the on-demand
// instance prices a cluster estimate is built from plus the root-volume
// storage price.
type AwsPricingCatalog struct {
	InstanceTypes     []AwsInstanceType `json:"instance_types"`
	Storage           AwsStoragePrice   `json:"storage"`
	PricingComplete   bool              `json:"pricing_complete"`
	IncompleteReasons []string          `json:"incomplete_reasons"`
}

// AwsAccessInfo is GET /api/v1/clusters/aws/{cluster_id}/access-info: the
// bastion's public address and the SSH users on either side of the jump,
// plus the control plane's private addresses. BastionIP and ControlPlaneIP
// are null while the instances have no address yet.
type AwsAccessInfo struct {
	BastionIP       *string  `json:"bastion_ip"`
	BastionHost     string   `json:"bastion_host"`
	BastionPort     int      `json:"bastion_port"`
	BastionUser     string   `json:"bastion_user"`
	TargetUser      string   `json:"target_user"`
	ControlPlaneIP  *string  `json:"control_plane_ip"`
	ControlPlaneIPs []string `json:"control_plane_ips"`
	ClusterName     *string  `json:"cluster_name"`
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
// SSH users and port for the jump.
func (c *Client) GetAwsAccessInfo(clusterID string) (*AwsAccessInfo, error) {
	var result AwsAccessInfo
	if getError := c.getJSON(c.providerClusterURL(awsKind, clusterID, "access-info"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

// awsCatalogURL builds the URL of one credential-scoped AWS catalog. Every
// catalog takes the credential; region and vpc_id are sent only when given,
// so a region-less read (regions) and a VPC-scoped one (subnets) share the
// path shape.
func (c *Client) awsCatalogURL(catalog, credentialID, region, vpcID string) string {
	query := url.Values{}
	query.Set("credential_id", credentialID)
	if region != "" {
		query.Set("region", region)
	}
	if vpcID != "" {
		query.Set("vpc_id", vpcID)
	}
	return fmt.Sprintf("%s/api/v1/clusters/aws/%s?%s", c.BaseURL, catalog, query.Encode())
}

// awsCatalog reads one catalog into its own response type: each AWS
// catalog is a separate endpoint with its own envelope, so there is no
// shared result the members could be missing from.
func awsCatalog[T any](c *Client, catalog, credentialID, region, vpcID string) (*T, error) {
	var result T
	if getError := c.getJSON(c.awsCatalogURL(catalog, credentialID, region, vpcID), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) ListAwsRegions(credentialID string) (*AwsRegionsCatalog, error) {
	return awsCatalog[AwsRegionsCatalog](c, "regions", credentialID, "", "")
}

func (c *Client) ListAwsInstanceTypes(credentialID, region string) (*AwsInstanceTypesCatalog, error) {
	return awsCatalog[AwsInstanceTypesCatalog](c, "instance-types", credentialID, region, "")
}

func (c *Client) ListAwsVpcs(credentialID, region string) (*AwsVpcsCatalog, error) {
	return awsCatalog[AwsVpcsCatalog](c, "vpcs", credentialID, region, "")
}

func (c *Client) ListAwsSubnets(credentialID, region, vpcID string) (*AwsSubnetsCatalog, error) {
	return awsCatalog[AwsSubnetsCatalog](c, "subnets", credentialID, region, vpcID)
}

func (c *Client) ListAwsAvailabilityZones(credentialID, region string) (*AwsAvailabilityZonesCatalog, error) {
	return awsCatalog[AwsAvailabilityZonesCatalog](c, "availability-zones", credentialID, region, "")
}

func (c *Client) ListAwsImages(credentialID, region string) (*AwsImagesCatalog, error) {
	return awsCatalog[AwsImagesCatalog](c, "images", credentialID, region, "")
}

func (c *Client) ListAwsPricing(credentialID, region string) (*AwsPricingCatalog, error) {
	return awsCatalog[AwsPricingCatalog](c, "pricing", credentialID, region, "")
}

// ListAwsClusterInstanceTypes reads the instance types available to an
// existing cluster, scoped by the cluster's own credential and region so the
// caller does not have to repeat them.
func (c *Client) ListAwsClusterInstanceTypes(clusterID string) (*AwsInstanceTypesCatalog, error) {
	var result AwsInstanceTypesCatalog
	if getError := c.getJSON(c.providerClusterURL(awsKind, clusterID, "instance-types"), &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}
