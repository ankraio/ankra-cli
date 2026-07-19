package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ManagedK8sAPI is the provider-generic managed Kubernetes contract. Provider
// remains an argument so adding a provider does not grow the client interface.
type ManagedK8sAPI interface {
	ManagedOptions(context.Context, string, string) (*ManagedOptions, error)
	ManagedClusterOptions(context.Context, string, string) (*ManagedOptions, error)
	ManagedPreflight(context.Context, string, json.RawMessage) (*ManagedPreflightResponse, error)
	ManagedCreate(context.Context, string, json.RawMessage) (*ManagedClusterCreateResponse, error)
	ManagedDiscover(context.Context, string, string) (*ManagedDiscoveryResponse, error)
	ManagedImport(context.Context, string, ManagedImportRequest) (*ManagedImportResponse, error)
	ManagedStatus(context.Context, string, string) (*ManagedStatusResponse, error)
	ManagedDisconnect(context.Context, string, string, bool) (*ManagedDeprovisionResponse, error)
	ManagedDeleteProviderCluster(context.Context, string, string, bool, string) (*ManagedDeprovisionResponse, error)
	ManagedListPools(context.Context, string, string) (*ManagedNodePoolsResponse, error)
	ManagedPoolCatalog(context.Context, string, string) (*ManagedPoolCatalog, error)
	ManagedAddPool(context.Context, string, string, ManagedNodePoolRequest) (*ManagedPoolOperationResponse, error)
	ManagedScalePool(context.Context, string, string, string, int) (*ManagedPoolOperationResponse, error)
	ManagedUpdatePool(context.Context, string, string, string, ManagedPoolUpdateRequest) (*ManagedPoolOperationResponse, error)
	ManagedDeletePool(context.Context, string, string, string) (*ManagedPoolOperationResponse, error)
	ManagedUpgrades(context.Context, string, string) (*ManagedAvailableUpgradesResponse, error)
	ManagedUpgrade(context.Context, string, string, string) (*ManagedUpgradeResponse, error)
}

var _ ManagedK8sAPI = (*Client)(nil)

type ManagedProvenance string

const (
	ManagedProvenanceCreated  ManagedProvenance = "created"
	ManagedProvenanceImported ManagedProvenance = "imported"
	ManagedProvenanceUnknown  ManagedProvenance = "unknown"
)

type ManagedAutoscaling struct {
	Enabled  *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	MinCount int   `json:"min_count" yaml:"min_count"`
	MaxCount int   `json:"max_count" yaml:"max_count"`
}

type ManagedNodePoolRequest struct {
	Name             string              `json:"name" yaml:"name"`
	Size             string              `json:"size" yaml:"size"`
	Count            *int                `json:"count,omitempty" yaml:"count,omitempty"`
	Labels           map[string]string   `json:"labels,omitempty" yaml:"labels,omitempty"`
	Taints           []NodeTaint         `json:"taints,omitempty" yaml:"taints,omitempty"`
	ProviderID       *string             `json:"provider_id,omitempty" yaml:"provider_id,omitempty"`
	Status           *string             `json:"status,omitempty" yaml:"status,omitempty"`
	Zone             *string             `json:"zone,omitempty" yaml:"zone,omitempty"`
	RootVolumeType   *string             `json:"root_volume_type,omitempty" yaml:"root_volume_type,omitempty"`
	RootVolumeSizeGB *int                `json:"root_volume_size_gb,omitempty" yaml:"root_volume_size_gb,omitempty"`
	SecurityGroupID  *string             `json:"security_group_id,omitempty" yaml:"security_group_id,omitempty"`
	PublicIP         *bool               `json:"public_ip,omitempty" yaml:"public_ip,omitempty"`
	Autohealing      *bool               `json:"autohealing,omitempty" yaml:"autohealing,omitempty"`
	Autoscaling      *ManagedAutoscaling `json:"autoscaling,omitempty" yaml:"autoscaling,omitempty"`
	UpgradePolicy    *string             `json:"upgrade_policy,omitempty" yaml:"upgrade_policy,omitempty"`
}

type ManagedAutoscalingSummary struct {
	Enabled  bool `json:"enabled" yaml:"enabled"`
	MinCount int  `json:"min_count" yaml:"min_count"`
	MaxCount int  `json:"max_count" yaml:"max_count"`
}

type ManagedNodePoolSummary struct {
	Name             string                     `json:"name" yaml:"name"`
	Size             string                     `json:"size" yaml:"size"`
	Count            int                        `json:"count" yaml:"count"`
	Labels           map[string]string          `json:"labels" yaml:"labels"`
	Taints           []NodeTaint                `json:"taints" yaml:"taints"`
	ProviderID       *string                    `json:"provider_id" yaml:"provider_id"`
	Status           *string                    `json:"status" yaml:"status"`
	Zone             *string                    `json:"zone" yaml:"zone"`
	RootVolumeType   *string                    `json:"root_volume_type" yaml:"root_volume_type"`
	RootVolumeSizeGB *int                       `json:"root_volume_size_gb" yaml:"root_volume_size_gb"`
	SecurityGroupID  *string                    `json:"security_group_id" yaml:"security_group_id"`
	PublicIP         *bool                      `json:"public_ip" yaml:"public_ip"`
	Autohealing      *bool                      `json:"autohealing" yaml:"autohealing"`
	Autoscaling      *ManagedAutoscalingSummary `json:"autoscaling" yaml:"autoscaling"`
	UpgradePolicy    *string                    `json:"upgrade_policy" yaml:"upgrade_policy"`
}

type ManagedCapabilities struct {
	SupportsSpot                    bool `json:"supports_spot" yaml:"supports_spot"`
	SupportsAutopilot               bool `json:"supports_autopilot" yaml:"supports_autopilot"`
	SupportsPrivateEndpoint         bool `json:"supports_private_endpoint" yaml:"supports_private_endpoint"`
	SupportsAutoscaling             bool `json:"supports_autoscaling" yaml:"supports_autoscaling"`
	SupportsHAControlPlane          bool `json:"supports_ha_control_plane" yaml:"supports_ha_control_plane"`
	SupportsMaintenanceWindow       bool `json:"supports_maintenance_window" yaml:"supports_maintenance_window"`
	SupportsPoolZones               bool `json:"supports_pool_zones" yaml:"supports_pool_zones"`
	SupportsPoolRootVolume          bool `json:"supports_pool_root_volume" yaml:"supports_pool_root_volume"`
	SupportsPoolSecurityGroups      bool `json:"supports_pool_security_groups" yaml:"supports_pool_security_groups"`
	SupportsPoolAutohealing         bool `json:"supports_pool_autohealing" yaml:"supports_pool_autohealing"`
	SupportsPoolPublicIP            bool `json:"supports_pool_public_ip" yaml:"supports_pool_public_ip"`
	SupportsPoolUpgradePolicy       bool `json:"supports_pool_upgrade_policy" yaml:"supports_pool_upgrade_policy"`
	SupportsPoolZoneUpdate          bool `json:"supports_pool_zone_update" yaml:"supports_pool_zone_update"`
	SupportsPoolRootVolumeUpdate    bool `json:"supports_pool_root_volume_update" yaml:"supports_pool_root_volume_update"`
	SupportsPoolSecurityGroupUpdate bool `json:"supports_pool_security_group_update" yaml:"supports_pool_security_group_update"`
	SupportsPoolPublicIPUpdate      bool `json:"supports_pool_public_ip_update" yaml:"supports_pool_public_ip_update"`
	SupportsPoolAutohealingUpdate   bool `json:"supports_pool_autohealing_update" yaml:"supports_pool_autohealing_update"`
	SupportsPoolUpgradePolicyUpdate bool `json:"supports_pool_upgrade_policy_update" yaml:"supports_pool_upgrade_policy_update"`
}

type ManagedVersionOption struct {
	Version        string   `json:"version" yaml:"version"`
	IsDefault      bool     `json:"is_default" yaml:"is_default"`
	AvailableCNIs  []string `json:"available_cnis" yaml:"available_cnis"`
	SupportedUntil *string  `json:"supported_until,omitempty" yaml:"supported_until,omitempty"`
}

type ManagedLocationOption struct {
	Slug              string  `json:"slug" yaml:"slug"`
	Name              string  `json:"name" yaml:"name"`
	ProviderReference *string `json:"provider_reference,omitempty" yaml:"provider_reference,omitempty"`
}

type ManagedZoneOption struct {
	Slug   string `json:"slug" yaml:"slug"`
	Region string `json:"region" yaml:"region"`
}

type ManagedNetworkOption struct {
	ID                string `json:"id" yaml:"id"`
	Name              string `json:"name" yaml:"name"`
	Region            string `json:"region" yaml:"region"`
	ProviderReference string `json:"provider_reference" yaml:"provider_reference"`
}

type ManagedSecurityGroupOption struct {
	ID                string `json:"id" yaml:"id"`
	Name              string `json:"name" yaml:"name"`
	Region            string `json:"region" yaml:"region"`
	Zone              string `json:"zone" yaml:"zone"`
	ProviderReference string `json:"provider_reference" yaml:"provider_reference"`
}

type ManagedSizeOption struct {
	Slug         string   `json:"slug" yaml:"slug"`
	Label        string   `json:"label" yaml:"label"`
	Region       string   `json:"region" yaml:"region"`
	IsGPU        bool     `json:"is_gpu" yaml:"is_gpu"`
	VCPUs        *int     `json:"vcpus" yaml:"vcpus"`
	MemoryGB     *float64 `json:"memory_gb" yaml:"memory_gb"`
	DiskGB       *int     `json:"disk_gb" yaml:"disk_gb"`
	PriceMonthly *float64 `json:"price_monthly" yaml:"price_monthly"`
	Category     string   `json:"category" yaml:"category"`
	GPUCount     *int     `json:"gpu_count" yaml:"gpu_count"`
	GPUModel     string   `json:"gpu_model" yaml:"gpu_model"`
}

type ManagedOptions struct {
	Locations          []ManagedLocationOption      `json:"locations" yaml:"locations"`
	Versions           []string                     `json:"versions" yaml:"versions"`
	VersionOptions     []ManagedVersionOption       `json:"version_options" yaml:"version_options"`
	Sizes              []ManagedSizeOption          `json:"sizes" yaml:"sizes"`
	ClusterPlans       []string                     `json:"cluster_plans" yaml:"cluster_plans"`
	Capabilities       ManagedCapabilities          `json:"capabilities" yaml:"capabilities"`
	PricingUnavailable bool                         `json:"pricing_unavailable" yaml:"pricing_unavailable"`
	Zones              []ManagedZoneOption          `json:"zones" yaml:"zones"`
	PrivateNetworks    []ManagedNetworkOption       `json:"private_networks" yaml:"private_networks"`
	SecurityGroups     []ManagedSecurityGroupOption `json:"security_groups" yaml:"security_groups"`
	RootVolumeTypes    []string                     `json:"root_volume_types" yaml:"root_volume_types"`
	Incomplete         bool                         `json:"incomplete" yaml:"incomplete"`
	IncompleteRegions  []string                     `json:"incomplete_regions" yaml:"incomplete_regions"`
}

type ManagedPoolCatalog struct {
	Sizes              []ManagedSizeOption          `json:"sizes" yaml:"sizes"`
	Zones              []ManagedZoneOption          `json:"zones" yaml:"zones"`
	PrivateNetworks    []ManagedNetworkOption       `json:"private_networks" yaml:"private_networks"`
	SecurityGroups     []ManagedSecurityGroupOption `json:"security_groups" yaml:"security_groups"`
	RootVolumeTypes    []string                     `json:"root_volume_types" yaml:"root_volume_types"`
	Capabilities       ManagedCapabilities          `json:"capabilities" yaml:"capabilities"`
	PricingUnavailable bool                         `json:"pricing_unavailable" yaml:"pricing_unavailable"`
	Incomplete         bool                         `json:"incomplete" yaml:"incomplete"`
	IncompleteRegions  []string                     `json:"incomplete_regions" yaml:"incomplete_regions"`
}

type ManagedPreflightItem struct {
	Check   string `json:"check" yaml:"check"`
	Status  string `json:"status" yaml:"status"`
	Message string `json:"message" yaml:"message"`
}

type ManagedPreflightResponse struct {
	Items      []ManagedPreflightItem `json:"items" yaml:"items"`
	CanProceed bool                   `json:"can_proceed" yaml:"can_proceed"`
}

type ManagedClusterCreateResponse struct {
	ClusterID  string            `json:"cluster_id" yaml:"cluster_id"`
	Name       string            `json:"name" yaml:"name"`
	Provenance ManagedProvenance `json:"provenance" yaml:"provenance"`
}

type ManagedDiscoveredCluster struct {
	ProviderClusterID string                   `json:"provider_cluster_id" yaml:"provider_cluster_id"`
	Name              string                   `json:"name" yaml:"name"`
	Location          string                   `json:"location" yaml:"location"`
	Status            string                   `json:"status" yaml:"status"`
	NodeCount         int                      `json:"node_count" yaml:"node_count"`
	AlreadyImported   bool                     `json:"already_imported" yaml:"already_imported"`
	CNI               *string                  `json:"cni" yaml:"cni"`
	PrivateNetworkID  *string                  `json:"private_network_id" yaml:"private_network_id"`
	Version           *string                  `json:"version" yaml:"version"`
	NodePools         []ManagedNodePoolSummary `json:"node_pools" yaml:"node_pools"`
	Ownership         ManagedProvenance        `json:"ownership" yaml:"ownership"`
}

type ManagedDiscoveryResponse struct {
	Clusters          []ManagedDiscoveredCluster `json:"clusters" yaml:"clusters"`
	Incomplete        bool                       `json:"incomplete" yaml:"incomplete"`
	IncompleteRegions []string                   `json:"incomplete_regions" yaml:"incomplete_regions"`
}

type ManagedImportRequest struct {
	ProviderClusterID string  `json:"provider_cluster_id"`
	CredentialID      string  `json:"credential_id"`
	Name              *string `json:"name,omitempty"`
	Description       *string `json:"description,omitempty"`
}

type ManagedImportResponse struct {
	ClusterID         string            `json:"cluster_id" yaml:"cluster_id"`
	Name              string            `json:"name" yaml:"name"`
	ProviderClusterID string            `json:"provider_cluster_id" yaml:"provider_cluster_id"`
	Provenance        ManagedProvenance `json:"provenance" yaml:"provenance"`
}

type ManagedStatusResponse struct {
	ClusterID         string                   `json:"cluster_id" yaml:"cluster_id"`
	ProviderClusterID string                   `json:"provider_cluster_id" yaml:"provider_cluster_id"`
	Status            string                   `json:"status" yaml:"status"`
	NodePools         []ManagedNodePoolSummary `json:"node_pools" yaml:"node_pools"`
	Endpoint          *string                  `json:"endpoint" yaml:"endpoint"`
	Version           *string                  `json:"version" yaml:"version"`
	CNI               *string                  `json:"cni" yaml:"cni"`
	Ownership         ManagedProvenance        `json:"ownership" yaml:"ownership"`
}

type ManagedDeprovisionResponse struct {
	Success         bool     `json:"success" yaml:"success"`
	ClusterID       string   `json:"cluster_id" yaml:"cluster_id"`
	OperationID     *string  `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
	ResourcesMarked int      `json:"resources_marked" yaml:"resources_marked"`
	Errors          []string `json:"errors" yaml:"errors"`
	RetentionPolicy *string  `json:"retention_policy,omitempty" yaml:"retention_policy,omitempty"`
}

type ManagedNodePoolsResponse struct {
	NodePools []ManagedNodePoolSummary `json:"node_pools" yaml:"node_pools"`
}

type ManagedPoolOperationResponse struct {
	ClusterID          string  `json:"cluster_id" yaml:"cluster_id"`
	NodePoolName       string  `json:"node_pool_name" yaml:"node_pool_name"`
	Count              *int    `json:"count" yaml:"count"`
	AutoscalingEnabled *bool   `json:"autoscaling_enabled" yaml:"autoscaling_enabled"`
	AutoscalingMin     *int    `json:"autoscaling_min" yaml:"autoscaling_min"`
	AutoscalingMax     *int    `json:"autoscaling_max" yaml:"autoscaling_max"`
	Autohealing        *bool   `json:"autohealing" yaml:"autohealing"`
	UpgradePolicy      *string `json:"upgrade_policy" yaml:"upgrade_policy"`
}

type ManagedPoolUpdateRequest struct {
	Count              *int    `json:"count,omitempty"`
	AutoscalingEnabled *bool   `json:"autoscaling_enabled,omitempty"`
	AutoscalingMin     *int    `json:"autoscaling_min,omitempty"`
	AutoscalingMax     *int    `json:"autoscaling_max,omitempty"`
	Zone               *string `json:"zone,omitempty"`
	RootVolumeType     *string `json:"root_volume_type,omitempty"`
	RootVolumeSizeGB   *int    `json:"root_volume_size_gb,omitempty"`
	SecurityGroupID    *string `json:"security_group_id,omitempty"`
	PublicIP           *bool   `json:"public_ip,omitempty"`
	Autohealing        *bool   `json:"autohealing,omitempty"`
	UpgradePolicy      *string `json:"upgrade_policy,omitempty"`
}

type ManagedAvailableUpgradesResponse struct {
	ClusterID      string                 `json:"cluster_id" yaml:"cluster_id"`
	CurrentVersion *string                `json:"current_version" yaml:"current_version"`
	Upgrades       []ManagedVersionOption `json:"upgrades" yaml:"upgrades"`
}

type ManagedUpgradeResponse struct {
	ClusterID   string `json:"cluster_id" yaml:"cluster_id"`
	Version     string `json:"version" yaml:"version"`
	OperationID string `json:"operation_id" yaml:"operation_id"`
}

func (c *Client) managedPath(provider string, suffix ...string) string {
	path := c.BaseURL + "/api/v1/clusters/managed/" + url.PathEscape(provider)
	for _, part := range suffix {
		path += "/" + url.PathEscape(part)
	}
	return path
}

func (c *Client) doManagedJSON(ctx context.Context, method, endpoint string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)
	responseBody, err := readResponseBody(response)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
		return denied
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newUnexpectedResponseErrorWithMessage(response.StatusCode,
			fmt.Sprintf("managed Kubernetes request failed: status %d: %s", response.StatusCode, redactedBodyForError(responseBody, 1000)))
	}
	if target == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func (c *Client) ManagedOptions(ctx context.Context, provider, credentialID string) (*ManagedOptions, error) {
	endpoint := c.managedPath(provider, "options") + "?credential_id=" + url.QueryEscape(credentialID)
	var result ManagedOptions
	return &result, c.doManagedJSON(ctx, http.MethodGet, endpoint, nil, &result)
}

func (c *Client) ManagedClusterOptions(ctx context.Context, provider, clusterID string) (*ManagedOptions, error) {
	var result ManagedOptions
	return &result, c.doManagedJSON(ctx, http.MethodGet, c.managedPath(provider, clusterID, "options"), nil, &result)
}

func (c *Client) ManagedPreflight(ctx context.Context, provider string, request json.RawMessage) (*ManagedPreflightResponse, error) {
	var result ManagedPreflightResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider, "preflight"), request, &result)
}

func (c *Client) ManagedCreate(ctx context.Context, provider string, request json.RawMessage) (*ManagedClusterCreateResponse, error) {
	var result ManagedClusterCreateResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider), request, &result)
}

func (c *Client) ManagedDiscover(ctx context.Context, provider, credentialID string) (*ManagedDiscoveryResponse, error) {
	endpoint := c.managedPath(provider, "discover") + "?credential_id=" + url.QueryEscape(credentialID)
	var result ManagedDiscoveryResponse
	return &result, c.doManagedJSON(ctx, http.MethodGet, endpoint, nil, &result)
}

func (c *Client) ManagedImport(ctx context.Context, provider string, request ManagedImportRequest) (*ManagedImportResponse, error) {
	var result ManagedImportResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider, "import"), request, &result)
}

func (c *Client) ManagedStatus(ctx context.Context, provider, clusterID string) (*ManagedStatusResponse, error) {
	var result ManagedStatusResponse
	return &result, c.doManagedJSON(ctx, http.MethodGet, c.managedPath(provider, clusterID, "status"), nil, &result)
}

func (c *Client) ManagedDisconnect(ctx context.Context, provider, clusterID string, force bool) (*ManagedDeprovisionResponse, error) {
	endpoint := c.managedPath(provider, clusterID, "disconnect")
	if force {
		endpoint += "?force=true"
	}
	var result ManagedDeprovisionResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, endpoint, nil, &result)
}

func (c *Client) ManagedDeleteProviderCluster(ctx context.Context, provider, clusterID string, force bool, retention string) (*ManagedDeprovisionResponse, error) {
	query := url.Values{"retention_policy": []string{retention}}
	if force {
		query.Set("force", "true")
	}
	var result ManagedDeprovisionResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost,
		c.managedPath(provider, clusterID, "delete-provider-cluster")+"?"+query.Encode(), nil, &result)
}

func (c *Client) ManagedListPools(ctx context.Context, provider, clusterID string) (*ManagedNodePoolsResponse, error) {
	var result ManagedNodePoolsResponse
	return &result, c.doManagedJSON(ctx, http.MethodGet, c.managedPath(provider, clusterID, "node-pools"), nil, &result)
}

func (c *Client) ManagedPoolCatalog(ctx context.Context, provider, clusterID string) (*ManagedPoolCatalog, error) {
	var result ManagedPoolCatalog
	return &result, c.doManagedJSON(ctx, http.MethodGet, c.managedPath(provider, clusterID, "node-pools", "catalog"), nil, &result)
}

func (c *Client) ManagedAddPool(ctx context.Context, provider, clusterID string, request ManagedNodePoolRequest) (*ManagedPoolOperationResponse, error) {
	var result ManagedPoolOperationResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider, clusterID, "node-pools"), request, &result)
}

func (c *Client) ManagedScalePool(ctx context.Context, provider, clusterID, poolName string, count int) (*ManagedPoolOperationResponse, error) {
	var result ManagedPoolOperationResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider, clusterID, "node-pools", poolName, "scale"),
		struct {
			Count int `json:"count"`
		}{Count: count}, &result)
}

func (c *Client) ManagedUpdatePool(ctx context.Context, provider, clusterID, poolName string, request ManagedPoolUpdateRequest) (*ManagedPoolOperationResponse, error) {
	var result ManagedPoolOperationResponse
	return &result, c.doManagedJSON(ctx, http.MethodPatch, c.managedPath(provider, clusterID, "node-pools", poolName), request, &result)
}

func (c *Client) ManagedDeletePool(ctx context.Context, provider, clusterID, poolName string) (*ManagedPoolOperationResponse, error) {
	var result ManagedPoolOperationResponse
	return &result, c.doManagedJSON(ctx, http.MethodDelete, c.managedPath(provider, clusterID, "node-pools", poolName), nil, &result)
}

func (c *Client) ManagedUpgrades(ctx context.Context, provider, clusterID string) (*ManagedAvailableUpgradesResponse, error) {
	var result ManagedAvailableUpgradesResponse
	return &result, c.doManagedJSON(ctx, http.MethodGet, c.managedPath(provider, clusterID, "upgrades"), nil, &result)
}

func (c *Client) ManagedUpgrade(ctx context.Context, provider, clusterID, version string) (*ManagedUpgradeResponse, error) {
	var result ManagedUpgradeResponse
	return &result, c.doManagedJSON(ctx, http.MethodPost, c.managedPath(provider, clusterID, "upgrade"),
		struct {
			Version string `json:"version"`
		}{Version: version}, &result)
}
