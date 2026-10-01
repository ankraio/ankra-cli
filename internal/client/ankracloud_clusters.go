package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const ankraCloudKind = "ankracloud"

// AnkraCloud distributions: kubeadm is the server default, k3s the lighter
// alternative. Both run on Ankra Cloud servers the platform builds.
const (
	AnkraCloudDistributionKubeadm = "kubeadm"
	AnkraCloudDistributionK3s     = "k3s"
)

// CreateAnkraCloudClusterRequest mirrors the cluster-api decoder for
// POST /api/v1/clusters/ankracloud (providerapi.decodeCreateAnkraCloudClusterRequest):
// a self-managed kubeadm or k3s cluster on Ankra Cloud servers. Ankra Cloud
// sizes servers by plan, so the Scaleway instance-type members are *_plan
// here, and servers boot a template (server default debian-13) rather than
// an image. Omitted optional members take the server's default: distribution
// kubeadm, control_plane_count 1, worker_count 1, etcd_topology stacked,
// etcd_node_count 3, cni cilium, gitops_branch master, retention_policy
// retain.
//
// The network is either adopted (private_network_id) or created from
// network_ip_range (an RFC 1918 /16../29; server default a /24 derived from the cluster); the
// server refuses both together. bastion_plan and control_plane_plan are
// required; worker_plan is required while worker_count is above zero and no
// node_groups are sent, and etcd_plan for an external etcd topology.
type CreateAnkraCloudClusterRequest struct {
	Name                 string                `json:"name"`
	Description          *string               `json:"description,omitempty"`
	CredentialID         string                `json:"credential_id"`
	RuntimeCredentialID  *string               `json:"runtime_credential_id,omitempty"`
	SSHKeyCredentialID   string                `json:"ssh_key_credential_id"`
	Zone                 string                `json:"zone"`
	Template             string                `json:"template,omitempty"`
	PrivateNetworkID     *string               `json:"private_network_id,omitempty"`
	NetworkIPRange       *string               `json:"network_ip_range,omitempty"`
	BastionPlan          string                `json:"bastion_plan"`
	BastionAllowedIPs    []string              `json:"bastion_allowed_ips,omitempty"`
	ControlPlaneCount    int                   `json:"control_plane_count,omitempty"`
	ControlPlanePlan     string                `json:"control_plane_plan"`
	WorkerCount          *int                  `json:"worker_count,omitempty"`
	WorkerPlan           string                `json:"worker_plan,omitempty"`
	Distribution         string                `json:"distribution,omitempty"`
	KubernetesVersion    *string               `json:"kubernetes_version,omitempty"`
	EtcdTopology         string                `json:"etcd_topology,omitempty"`
	EtcdNodeCount        int                   `json:"etcd_node_count,omitempty"`
	EtcdPlan             string                `json:"etcd_plan,omitempty"`
	CNI                  string                `json:"cni,omitempty"`
	NodeGroups           []AddNodeGroupRequest `json:"node_groups,omitempty"`
	GitopsCredentialName *string               `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string               `json:"gitops_repository,omitempty"`
	GitopsBranch         *string               `json:"gitops_branch,omitempty"`
	IncludeNetworking    *bool                 `json:"include_networking,omitempty"`
	IncludeDNS           *bool                 `json:"include_dns,omitempty"`
	RetentionPolicy      string                `json:"retention_policy,omitempty"`
}

type CreateAnkraCloudClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

// AnkraCloudPreflightItem is one check from POST /clusters/ankracloud/preflight.
type AnkraCloudPreflightItem struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type AnkraCloudPreflightResult struct {
	CanProceed bool                      `json:"can_proceed"`
	Items      []AnkraCloudPreflightItem `json:"items"`
}

// AnkraCloudZone is one zone of the Ankra Cloud account.
type AnkraCloudZone struct {
	Name            string `json:"name"`
	Region          string `json:"region"`
	DisplayName     string `json:"display_name"`
	Country         string `json:"country"`
	CustomerVisible bool   `json:"customer_visible"`
}

// AnkraCloudPlan is one server plan with its EUR-cent prices. A zero price
// means unpriced, never free.
type AnkraCloudPlan struct {
	Name                  string `json:"name"`
	Family                string `json:"family"`
	Cores                 int    `json:"cores"`
	MemoryMebibytes       int    `json:"memory_mebibytes"`
	StorageGibibytes      int    `json:"storage_gibibytes"`
	PriceMonthlyCents     int    `json:"price_monthly_cents"`
	PriceHourlyMillicents int    `json:"price_hourly_millicents"`
	Available             bool   `json:"available"`
}

// AnkraCloudTemplate is one operating-system template a server boots.
type AnkraCloudTemplate struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	OperatingSystem string `json:"operating_system"`
	Version         string `json:"version"`
}

// AnkraCloudNetwork is one private network a cluster can adopt.
type AnkraCloudNetwork struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Zone string `json:"zone"`
	CIDR string `json:"cidr"`
}

// AnkraCloudStoragePrice is one storage tier's per-GiB-month price.
type AnkraCloudStoragePrice struct {
	Tier         string `json:"tier"`
	GBMonthCents int    `json:"gb_month_cents"`
}

// AnkraCloudCatalogResult is the one envelope every catalog route answers;
// each route fills its own member. PricingComplete false with
// IncompleteReasons names what Ankra Cloud did not price.
type AnkraCloudCatalogResult struct {
	Zones                  []AnkraCloudZone         `json:"zones,omitempty"`
	Plans                  []AnkraCloudPlan         `json:"plans,omitempty"`
	Templates              []AnkraCloudTemplate     `json:"templates,omitempty"`
	Networks               []AnkraCloudNetwork      `json:"networks,omitempty"`
	StoragePrices          []AnkraCloudStoragePrice `json:"storage_prices,omitempty"`
	PublicIPv4MonthlyCents *int                     `json:"public_ipv4_monthly_cents,omitempty"`
	PricingComplete        bool                     `json:"pricing_complete"`
	IncompleteReasons      []string                 `json:"incomplete_reasons,omitempty"`
}

func (c *Client) CreateAnkraCloudCluster(request CreateAnkraCloudClusterRequest) (*CreateAnkraCloudClusterResponse, error) {
	var result CreateAnkraCloudClusterResponse
	if createError := c.createProviderCluster(ankraCloudKind, request, &result); createError != nil {
		return nil, createError
	}
	return &result, nil
}

// PreflightAnkraCloudCluster validates a create request without provisioning.
func (c *Client) PreflightAnkraCloudCluster(request CreateAnkraCloudClusterRequest) (*AnkraCloudPreflightResult, error) {
	endpoint := c.BaseURL + "/api/v1/clusters/ankracloud/preflight"
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

	var result AnkraCloudPreflightResult
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

func (c *Client) DeprovisionAnkraCloudCluster(clusterID string, options DeprovisionOptions) (*ProviderDeprovisionClusterResponse, error) {
	return c.deprovisionProviderCluster(ankraCloudKind, clusterID, options)
}

func (c *Client) StopAnkraCloudCluster(clusterID string, options StopClusterOptions) (*ProviderStopClusterResponse, error) {
	return c.stopProviderCluster(ankraCloudKind, clusterID, options)
}

func (c *Client) StartAnkraCloudCluster(clusterID string, options StartClusterOptions) (*ProviderStartClusterResult, error) {
	return c.startProviderCluster(ankraCloudKind, clusterID, options)
}

func (c *Client) GetAnkraCloudWorkerCount(clusterID string) (*WorkerCountResult, error) {
	return c.getProviderWorkerCount(ankraCloudKind, clusterID)
}

func (c *Client) ScaleAnkraCloudWorkers(clusterID string, workerCount int, drainOptions DrainOptions) (*ScaleWorkersResult, error) {
	return c.scaleProviderWorkers(ankraCloudKind, clusterID, workerCount, drainOptions)
}

func (c *Client) GetAnkraCloudK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	return c.getProviderK8sVersion(ankraCloudKind, clusterID)
}

func (c *Client) UpgradeAnkraCloudK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	return c.upgradeProviderK8sVersion(ankraCloudKind, clusterID, targetVersion, force)
}

func (c *Client) ListAnkraCloudNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	return c.listProviderNodeGroups(ankraCloudKind, clusterID)
}

func (c *Client) AddAnkraCloudNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	return c.addProviderNodeGroup(ctx, ankraCloudKind, clusterID, request, wait)
}

func (c *Client) ScaleAnkraCloudNodeGroup(ctx context.Context, clusterID, groupName string, count int, drainOptions DrainOptions, wait bool) (*ScaleNodeGroupResult, bool, error) {
	return c.scaleProviderNodeGroup(ctx, ankraCloudKind, clusterID, groupName, count, drainOptions, wait)
}

func (c *Client) UpdateAnkraCloudNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, drainOptions DrainOptions, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateProviderNodeGroupInstanceType(ctx, ankraCloudKind, clusterID, groupName, instanceType, drainOptions, wait)
}

func (c *Client) UpdateAnkraCloudNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ankracloud/%s/node-groups/%s/labels", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateLabelsRequest{Labels: labels})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) UpdateAnkraCloudNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ankracloud/%s/node-groups/%s/taints", c.BaseURL, clusterID, groupName)
	payload, marshalError := json.Marshal(UpdateTaintsRequest{Taints: taints})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	return c.doUpdateNodeGroup(ctx, endpoint, payload, wait)
}

func (c *Client) DeleteAnkraCloudNodeGroup(ctx context.Context, clusterID, groupName string, drainOptions DrainOptions, wait bool) (*DeleteNodeGroupResult, bool, error) {
	return c.deleteProviderNodeGroup(ctx, ankraCloudKind, clusterID, groupName, drainOptions, wait)
}

func (c *Client) GetAnkraCloudNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	return c.getProviderNodeGroupAutoscaling(ankraCloudKind, clusterID, groupName)
}

func (c *Client) UpdateAnkraCloudNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	return c.updateProviderNodeGroupAutoscaling(ctx, ankraCloudKind, clusterID, groupName, request, wait)
}

func (c *Client) GetAnkraCloudControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane(ankraCloudKind, clusterID)
}

func (c *Client) ChangeAnkraCloudControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount(ankraCloudKind, clusterID, count)
}

func (c *Client) ChangeAnkraCloudControlPlaneInstanceType(clusterID, instanceType string, drainOptions DrainOptions) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType(ankraCloudKind, clusterID, instanceType, drainOptions)
}

func (c *Client) ListAnkraCloudClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes(ankraCloudKind, clusterID)
}

func (c *Client) GetAnkraCloudClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode(ankraCloudKind, clusterID, nodeID)
}

func (c *Client) RestartAnkraCloudClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode(ankraCloudKind, clusterID, nodeID)
}

func (c *Client) GetAnkraCloudClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys(ankraCloudKind, clusterID)
}

func (c *Client) UpdateAnkraCloudClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys(ankraCloudKind, clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncAnkraCloudClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys(ankraCloudKind, clusterID)
}

// ankraCloudCatalog reads one of the credential-scoped catalogs. Only the
// networks catalog is zone-scoped; plans, templates and pricing are
// account-wide, so the server ignores a zone on those.
func (c *Client) ankraCloudCatalog(catalog, credentialID, zone string) (*AnkraCloudCatalogResult, error) {
	query := url.Values{}
	query.Set("credential_id", credentialID)
	if zone != "" {
		query.Set("zone", zone)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ankracloud/%s?%s", c.BaseURL, catalog, query.Encode())
	var result AnkraCloudCatalogResult
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) ListAnkraCloudZones(credentialID string) (*AnkraCloudCatalogResult, error) {
	return c.ankraCloudCatalog("zones", credentialID, "")
}

func (c *Client) ListAnkraCloudPlans(credentialID string) (*AnkraCloudCatalogResult, error) {
	return c.ankraCloudCatalog("plans", credentialID, "")
}

func (c *Client) ListAnkraCloudTemplates(credentialID string) (*AnkraCloudCatalogResult, error) {
	return c.ankraCloudCatalog("templates", credentialID, "")
}

func (c *Client) ListAnkraCloudNetworks(credentialID, zone string) (*AnkraCloudCatalogResult, error) {
	return c.ankraCloudCatalog("networks", credentialID, zone)
}

func (c *Client) ListAnkraCloudPricing(credentialID string) (*AnkraCloudCatalogResult, error) {
	return c.ankraCloudCatalog("pricing", credentialID, "")
}

// ListAnkraCloudClusterPlans reads the plan catalog under an existing
// cluster's own credential, for the day-2 node-group and resize pickers.
func (c *Client) ListAnkraCloudClusterPlans(clusterID string) (*AnkraCloudCatalogResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ankracloud/%s/plans", c.BaseURL, url.PathEscape(clusterID))
	var result AnkraCloudCatalogResult
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

func (c *Client) AnkraCloudNodeCloudInitLog(clusterID, nodeID string) (*NodeCloudInitLogResult, error) {
	return c.nodeCloudInitLog(ankraCloudKind, clusterID, nodeID)
}

// UpdateAnkraCloudBastionInstanceType moves the bastion server to another
// plan; the route keeps the shared instance-type name.
func (c *Client) UpdateAnkraCloudBastionInstanceType(ctx context.Context, clusterID, plan string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	return c.updateBastionInstanceType(ctx, ankraCloudKind, clusterID, plan, wait)
}

func (c *Client) GetAnkraCloudBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth(ankraCloudKind, clusterID)
}

func (c *Client) DiagnoseAnkraCloudBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, ankraCloudKind, clusterID)
}
