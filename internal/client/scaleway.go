package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type ScalewayCredentialListItem struct {
	ID             string         `json:"id" yaml:"id"`
	Name           string         `json:"name" yaml:"name"`
	Provider       string         `json:"provider" yaml:"provider"`
	OrganisationID string         `json:"organisation_id" yaml:"organisation_id"`
	System         bool           `json:"system" yaml:"system"`
	Available      bool           `json:"available" yaml:"available"`
	State          *string        `json:"state,omitempty" yaml:"state,omitempty"`
	Health         map[string]any `json:"health,omitempty" yaml:"health,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

type CreateScalewayCredentialRequest struct {
	Name      string `json:"name"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	ProjectID string `json:"project_id"`
}

type CreateScalewayCredentialResponse struct {
	Success bool            `json:"success" yaml:"success"`
	Errors  []ResourceError `json:"errors,omitempty" yaml:"errors,omitempty"`
}

type ScalewayCNIFeatures struct {
	EBPFDataplane        bool `json:"ebpf_dataplane,omitempty" yaml:"ebpf_dataplane,omitempty"`
	Hubble               bool `json:"hubble,omitempty" yaml:"hubble,omitempty"`
	KubeProxyReplacement bool `json:"kube_proxy_replacement,omitempty" yaml:"kube_proxy_replacement,omitempty"`
	WireguardEncryption  bool `json:"wireguard_encryption,omitempty" yaml:"wireguard_encryption,omitempty"`
}

type ScalewayCreateNodeGroupRequest struct {
	Name         string                       `json:"name" yaml:"name"`
	InstanceType string                       `json:"instance_type" yaml:"instance_type"`
	Count        int                          `json:"count,omitempty" yaml:"count,omitempty"`
	Labels       map[string]string            `json:"labels,omitempty" yaml:"labels,omitempty"`
	Taints       []NodeTaint                  `json:"taints,omitempty" yaml:"taints,omitempty"`
	Autoscaling  *NodeGroupAutoscalingRequest `json:"autoscaling,omitempty" yaml:"autoscaling,omitempty"`
}

type CreateScalewayClusterRequest struct {
	Name                  string                           `json:"name" yaml:"name"`
	Description           *string                          `json:"description,omitempty" yaml:"description,omitempty"`
	CredentialID          string                           `json:"credential_id" yaml:"credential_id"`
	RuntimeCredentialID   *string                          `json:"runtime_credential_id,omitempty" yaml:"runtime_credential_id,omitempty"`
	SSHKeyCredentialID    string                           `json:"ssh_key_credential_id" yaml:"ssh_key_credential_id"`
	Region                string                           `json:"region" yaml:"region"`
	Zone                  string                           `json:"zone" yaml:"zone"`
	PrivateNetworkID      *string                          `json:"private_network_id,omitempty" yaml:"private_network_id,omitempty"`
	NetworkIPRange        *string                          `json:"network_ip_range,omitempty" yaml:"network_ip_range,omitempty"`
	GatewayType           string                           `json:"gateway_type" yaml:"gateway_type"`
	GatewayAllowedIPs     []string                         `json:"gateway_allowed_ips,omitempty" yaml:"gateway_allowed_ips,omitempty"`
	BastionPort           int                              `json:"bastion_port" yaml:"bastion_port"`
	ControlPlaneCount     int                              `json:"control_plane_count" yaml:"control_plane_count"`
	ControlPlaneType      string                           `json:"control_plane_type" yaml:"control_plane_type"`
	WorkerCount           int                              `json:"worker_count" yaml:"worker_count"`
	WorkerType            string                           `json:"worker_type" yaml:"worker_type"`
	NodeGroups            []ScalewayCreateNodeGroupRequest `json:"node_groups,omitempty" yaml:"node_groups,omitempty"`
	Distribution          string                           `json:"distribution" yaml:"distribution"`
	KubernetesVersion     *string                          `json:"kubernetes_version,omitempty" yaml:"kubernetes_version,omitempty"`
	EtcdTopology          string                           `json:"etcd_topology,omitempty" yaml:"etcd_topology,omitempty"`
	EtcdNodeCount         int                              `json:"etcd_node_count,omitempty" yaml:"etcd_node_count,omitempty"`
	EtcdType              string                           `json:"etcd_type,omitempty" yaml:"etcd_type,omitempty"`
	ExternalCloudProvider bool                             `json:"external_cloud_provider" yaml:"external_cloud_provider"`
	CNI                   string                           `json:"cni" yaml:"cni"`
	CNIFeatures           ScalewayCNIFeatures              `json:"cni_features,omitempty" yaml:"cni_features,omitempty"`
	IncludeNetworking     bool                             `json:"include_networking" yaml:"include_networking"`
	IncludeDNS            bool                             `json:"include_dns" yaml:"include_dns"`
	GitopsCredentialName  string                           `json:"gitops_credential_name,omitempty" yaml:"gitops_credential_name,omitempty"`
	GitopsRepository      string                           `json:"gitops_repository,omitempty" yaml:"gitops_repository,omitempty"`
	GitopsBranch          string                           `json:"gitops_branch,omitempty" yaml:"gitops_branch,omitempty"`
	RetentionPolicy       string                           `json:"retention_policy" yaml:"retention_policy"`
}

type ScalewayCreateClusterResponse struct {
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	Name      string `json:"name" yaml:"name"`
}

type ScalewayPreflightItem struct {
	Check   string `json:"check" yaml:"check"`
	Status  string `json:"status" yaml:"status"`
	Message string `json:"message" yaml:"message"`
}

type ScalewayPreflightResult struct {
	Items      []ScalewayPreflightItem `json:"items" yaml:"items"`
	CanProceed bool                    `json:"can_proceed" yaml:"can_proceed"`
}

type ScalewayLocation struct {
	Region string   `json:"region" yaml:"region"`
	Zones  []string `json:"zones" yaml:"zones"`
}

type ScalewayNetwork struct {
	ID      string   `json:"id" yaml:"id"`
	Name    string   `json:"name" yaml:"name"`
	Region  string   `json:"region" yaml:"region"`
	Subnets []string `json:"subnets" yaml:"subnets"`
}

type ScalewayInstanceType struct {
	Name        string  `json:"name" yaml:"name"`
	VCPUs       int     `json:"vcpus" yaml:"vcpus"`
	MemoryBytes int64   `json:"memory_bytes" yaml:"memory_bytes"`
	Arch        string  `json:"arch" yaml:"arch"`
	HourlyEUR   float64 `json:"hourly_eur" yaml:"hourly_eur"`
	MonthlyEUR  float64 `json:"monthly_eur" yaml:"monthly_eur"`
	Available   bool    `json:"available" yaml:"available"`
}

type ScalewayGatewayType struct {
	Name      string `json:"name" yaml:"name"`
	Bandwidth int    `json:"bandwidth" yaml:"bandwidth"`
	Zone      string `json:"zone" yaml:"zone"`
}

type ScalewayStoragePrice struct {
	Type       string  `json:"type" yaml:"type"`
	GBHourEUR  float64 `json:"gb_hour_eur" yaml:"gb_hour_eur"`
	GBMonthEUR float64 `json:"gb_month_eur" yaml:"gb_month_eur"`
}

type ScalewayCatalogResult struct {
	Locations         []ScalewayLocation     `json:"locations,omitempty" yaml:"locations,omitempty"`
	Networks          []ScalewayNetwork      `json:"networks,omitempty" yaml:"networks,omitempty"`
	InstanceTypes     []ScalewayInstanceType `json:"instance_types,omitempty" yaml:"instance_types,omitempty"`
	GatewayTypes      []ScalewayGatewayType  `json:"gateway_types,omitempty" yaml:"gateway_types,omitempty"`
	StoragePrices     []ScalewayStoragePrice `json:"storage_prices,omitempty" yaml:"storage_prices,omitempty"`
	PricingComplete   bool                   `json:"pricing_complete" yaml:"pricing_complete"`
	IncompleteReasons []string               `json:"incomplete_reasons,omitempty" yaml:"incomplete_reasons,omitempty"`
}

type ScalewayLifecycleResponse struct {
	Success     bool    `json:"success" yaml:"success"`
	ClusterID   string  `json:"cluster_id" yaml:"cluster_id"`
	OperationID *string `json:"operation_id" yaml:"operation_id"`
}

type ScalewayAccessInfo struct {
	BastionIP       *string  `json:"bastion_ip" yaml:"bastion_ip"`
	BastionHost     string   `json:"bastion_host" yaml:"bastion_host"`
	BastionPort     int      `json:"bastion_port" yaml:"bastion_port"`
	BastionUser     string   `json:"bastion_user" yaml:"bastion_user"`
	TargetUser      string   `json:"target_user" yaml:"target_user"`
	ControlPlaneIP  *string  `json:"control_plane_ip" yaml:"control_plane_ip"`
	ControlPlaneIPs []string `json:"control_plane_ips" yaml:"control_plane_ips"`
	ClusterName     *string  `json:"cluster_name" yaml:"cluster_name"`
}

func (c *Client) ListScalewayCredentials() ([]ScalewayCredentialListItem, error) {
	var result []ScalewayCredentialListItem
	err := c.doManagedJSON(context.Background(), http.MethodGet, c.BaseURL+"/api/v1/credentials/scaleway", nil, &result)
	return result, err
}

func (c *Client) CreateScalewayCredential(request CreateScalewayCredentialRequest) (*CreateScalewayCredentialResponse, error) {
	var result CreateScalewayCredentialResponse
	return &result, c.doManagedJSON(context.Background(), http.MethodPost, c.BaseURL+"/api/v1/credentials/scaleway", request, &result)
}

func (c *Client) CreateScalewayCluster(ctx context.Context, request CreateScalewayClusterRequest) (*ScalewayCreateClusterResponse, error) {
	var result ScalewayCreateClusterResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.BaseURL+"/api/v1/clusters/scaleway", request, &result)
}

func (c *Client) PreflightScalewayCluster(ctx context.Context, request CreateScalewayClusterRequest) (*ScalewayPreflightResult, error) {
	var result ScalewayPreflightResult
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.BaseURL+"/api/v1/clusters/scaleway/preflight", request, &result)
}

func (c *Client) scalewayCatalog(ctx context.Context, name, credentialID string, query url.Values) (*ScalewayCatalogResult, error) {
	if query == nil {
		query = url.Values{}
	}
	if credentialID != "" {
		query.Set("credential_id", credentialID)
	}
	endpoint := c.BaseURL + "/api/v1/clusters/scaleway/" + name
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var result ScalewayCatalogResult
	return &result, c.doManagedJSON(ctx, http.MethodGet, endpoint, nil, &result)
}

func (c *Client) ListScalewayLocations(ctx context.Context, credentialID string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "locations", credentialID, nil)
}

func (c *Client) ListScalewayRegions(ctx context.Context, credentialID string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "regions", credentialID, nil)
}

func (c *Client) ListScalewayZones(ctx context.Context, credentialID string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "zones", credentialID, nil)
}

func (c *Client) ListScalewayNetworks(ctx context.Context, credentialID, region string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "networks", credentialID, url.Values{"region": []string{region}})
}

func (c *Client) ListScalewayInstanceTypes(ctx context.Context, credentialID, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "instance-types", credentialID, url.Values{"zone": []string{zone}})
}

func (c *Client) ListScalewayGatewayTypes(ctx context.Context, credentialID, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "gateway-types", credentialID, url.Values{"zone": []string{zone}})
}

func (c *Client) ListScalewayPricing(ctx context.Context, credentialID, zone string) (*ScalewayCatalogResult, error) {
	return c.scalewayCatalog(ctx, "pricing", credentialID, url.Values{"zone": []string{zone}})
}

func (c *Client) GetScalewayClusterInstanceTypes(ctx context.Context, clusterID string) (*ScalewayCatalogResult, error) {
	var result ScalewayCatalogResult
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/instance-types", c.BaseURL, url.PathEscape(clusterID))
	return &result, c.doManagedJSON(ctx, http.MethodGet, endpoint, nil, &result)
}

func (c *Client) GetScalewayClusterGatewayTypes(ctx context.Context, clusterID string) (*ScalewayCatalogResult, error) {
	var result ScalewayCatalogResult
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/gateway-types", c.BaseURL, url.PathEscape(clusterID))
	return &result, c.doManagedJSON(ctx, http.MethodGet, endpoint, nil, &result)
}

func (c *Client) DeprovisionScalewayCluster(clusterID string, force bool) (*ScalewayLifecycleResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/scaleway/%s", c.BaseURL, url.PathEscape(clusterID))
	if force {
		endpoint += "?force=true"
	}
	var result ScalewayLifecycleResponse
	return &result, c.doManagedJSON(context.Background(), http.MethodDelete, endpoint, nil, &result)
}

func (c *Client) StopScalewayCluster(clusterID string) (*ScalewayLifecycleResponse, error) {
	var result ScalewayLifecycleResponse
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/stop", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) StartScalewayCluster(clusterID string) (*StartUpcloudClusterResult, error) {
	var result StartUpcloudClusterResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/start", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) GetScalewayAccessInfo(clusterID string) (*ScalewayAccessInfo, error) {
	var result ScalewayAccessInfo
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/access-info", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) GetScalewayWorkerCount(clusterID string) (*WorkerCountResult, error) {
	var result WorkerCountResult
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/worker-count", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) ScaleScalewayWorkers(clusterID string, count int) (*ScaleWorkersResult, error) {
	var result ScaleWorkersResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/scale-workers", c.BaseURL, url.PathEscape(clusterID)),
		struct {
			WorkerCount int `json:"worker_count"`
		}{count}, &result)
}

func (c *Client) GetScalewayK8sVersion(clusterID string) (*K8sVersionInfo, error) {
	var result K8sVersionInfo
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/k8s-version", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) UpgradeScalewayK8sVersion(clusterID, targetVersion string, force bool) (*UpgradeK8sVersionResult, error) {
	var result UpgradeK8sVersionResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/upgrade-k8s-version", c.BaseURL, url.PathEscape(clusterID)),
		struct {
			TargetVersion string `json:"target_version"`
			Force         bool   `json:"force"`
		}{targetVersion, force}, &result)
}

func (c *Client) ListScalewayNodeGroups(clusterID string) (*NodeGroupListResult, error) {
	var result NodeGroupListResult
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) AddScalewayNodeGroup(ctx context.Context, clusterID string, request AddNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	var result AddNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups", c.BaseURL, url.PathEscape(clusterID)), payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) AddScalewayNodeGroupFull(ctx context.Context, clusterID string, request ScalewayCreateNodeGroupRequest, wait bool) (*AddNodeGroupResult, bool, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}
	var result AddNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups", c.BaseURL, url.PathEscape(clusterID)), payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) ScaleScalewayNodeGroup(ctx context.Context, clusterID, groupName string, count int, wait bool) (*ScaleNodeGroupResult, bool, error) {
	payload, _ := json.Marshal(struct {
		Count int `json:"count"`
	}{count})
	var result ScaleNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/scale", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName)),
		payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) UpdateScalewayNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	payload, _ := json.Marshal(struct {
		InstanceType string `json:"instance_type"`
	}{instanceType})
	var result UpdateNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/instance-type", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName)),
		payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) DeleteScalewayNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*DeleteNodeGroupResult, bool, error) {
	var result DeleteNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName)),
		nil, wait, &result)
	return &result, submitted, err
}

func (c *Client) GetScalewayNodeGroupAutoscaling(clusterID, groupName string) (*NodeGroupAutoscalingResult, error) {
	var result NodeGroupAutoscalingResult
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/autoscaling", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName)), nil, &result)
}

func (c *Client) UpdateScalewayNodeGroupAutoscaling(ctx context.Context, clusterID, groupName string, request NodeGroupAutoscalingRequest, wait bool) (*NodeGroupAutoscalingResult, bool, error) {
	payload, _ := json.Marshal(request)
	var result NodeGroupAutoscalingResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/autoscaling", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName)),
		payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) updateScalewayNodeGroupMetadata(ctx context.Context, clusterID, groupName, field string, body any, wait bool) (*UpdateNodeGroupResult, bool, error) {
	payload, _ := json.Marshal(body)
	var result UpdateNodeGroupResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/node-groups/%s/%s", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(groupName), field),
		payload, wait, &result)
	return &result, submitted, err
}

func (c *Client) UpdateScalewayNodeGroupLabels(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateScalewayNodeGroupMetadata(ctx, clusterID, groupName, "labels", struct {
		Labels map[string]string `json:"labels"`
	}{labels}, wait)
}

func (c *Client) UpdateScalewayNodeGroupTaints(ctx context.Context, clusterID, groupName string, taints []NodeTaint, wait bool) (*UpdateNodeGroupResult, bool, error) {
	return c.updateScalewayNodeGroupMetadata(ctx, clusterID, groupName, "taints", struct {
		Taints []NodeTaint `json:"taints"`
	}{taints}, wait)
}

func (c *Client) GetScalewayControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	var result ControlPlaneInfo
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/control-plane", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) ChangeScalewayControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	var result ChangeControlPlaneCountResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/control-plane", c.BaseURL, url.PathEscape(clusterID)),
		struct {
			Count int `json:"count"`
		}{count}, &result)
}

func (c *Client) ChangeScalewayControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	var result ChangeControlPlaneInstanceTypeResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/control-plane/instance-type", c.BaseURL, url.PathEscape(clusterID)),
		struct {
			InstanceType string `json:"instance_type"`
		}{instanceType}, &result)
}

func (c *Client) ListScalewayClusterNodes(clusterID string) (*NodeListResult, error) {
	var result NodeListResult
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/nodes", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) GetScalewayClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	var result NodeDetail
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/nodes/%s", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(nodeID)), nil, &result)
}

func (c *Client) RestartScalewayClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	var result RestartNodeResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/nodes/%s/restart", c.BaseURL, url.PathEscape(clusterID), url.PathEscape(nodeID)), nil, &result)
}

func (c *Client) GetScalewayClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	var result ClusterSSHKeysResult
	return &result, c.doManagedJSON(context.Background(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/ssh-keys", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) UpdateScalewayClusterSSHKeys(clusterID string, IDs []string) (*UpdateClusterSSHKeysResult, error) {
	var result UpdateClusterSSHKeysResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/ssh-keys", c.BaseURL, url.PathEscape(clusterID)),
		UpdateClusterSSHKeysRequest{SSHKeyCredentialIDs: IDs}, &result)
}

func (c *Client) ResyncScalewayClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	var result ResyncSSHKeysResult
	return &result, c.doManagedJSON(context.Background(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/ssh-keys/resync", c.BaseURL, url.PathEscape(clusterID)), nil, &result)
}

func (c *Client) UpdateScalewayBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	payload, _ := json.Marshal(struct {
		InstanceType string `json:"instance_type"`
	}{instanceType})
	var result UpdateBastionInstanceTypeResult
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/api/v1/clusters/scaleway/%s/bastion/instance-type", c.BaseURL, url.PathEscape(clusterID)),
		payload, wait, &result)
	return &result, submitted, err
}
