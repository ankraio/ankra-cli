package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

const costBasePath = "/api/v1/org/cloud-cost"

// FleetProviderCost is one provider's slice of the organisation-wide cost
// rollup. Amounts are integer cents in the organisation's display currency.
type FleetProviderCost struct {
	Provider                 string `json:"provider" yaml:"provider"`
	ClusterCount             int    `json:"cluster_count" yaml:"cluster_count"`
	MonthlyCostEstimateCents int64  `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
	MonthToDateCents         int64  `json:"month_to_date_cents" yaml:"month_to_date_cents"`
	ProjectedMonthEndCents   int64  `json:"projected_month_end_cents" yaml:"projected_month_end_cents"`
}

// FleetClusterCost is one of the organisation's costliest clusters.
type FleetClusterCost struct {
	ClusterID                string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName              string `json:"cluster_name" yaml:"cluster_name"`
	Provider                 string `json:"provider" yaml:"provider"`
	MonthlyCostEstimateCents int64  `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
	MonthToDateCents         int64  `json:"month_to_date_cents" yaml:"month_to_date_cents"`
	ProjectedMonthEndCents   int64  `json:"projected_month_end_cents" yaml:"projected_month_end_cents"`
	ConfidenceLevel          string `json:"confidence_level" yaml:"confidence_level"`
}

// FleetCloudCost is the organisation-wide cost rollup: every cluster whose
// latest snapshot is younger than a day, in the display currency.
type FleetCloudCost struct {
	Currency                 string              `json:"currency" yaml:"currency"`
	ClusterCount             int                 `json:"cluster_count" yaml:"cluster_count"`
	MonthlyCostEstimateCents int64               `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
	MonthToDateCents         int64               `json:"month_to_date_cents" yaml:"month_to_date_cents"`
	ProjectedMonthEndCents   int64               `json:"projected_month_end_cents" yaml:"projected_month_end_cents"`
	ByProvider               []FleetProviderCost `json:"by_provider" yaml:"by_provider"`
	TopClusters              []FleetClusterCost  `json:"top_clusters" yaml:"top_clusters"`
}

// CostReadiness explains why a cluster has no estimate yet (or that it is
// ready). State is one of ready, no_credential, unsupported_provider,
// cluster_offline, awaiting_nodes, awaiting_pricing, estimate_pending.
type CostReadiness struct {
	State              string  `json:"state" yaml:"state"`
	Provider           *string `json:"provider" yaml:"provider"`
	HasCloudCredential bool    `json:"has_cloud_credential" yaml:"has_cloud_credential"`
	NodesSynced        bool    `json:"nodes_synced" yaml:"nodes_synced"`
	ClusterOnline      bool    `json:"cluster_online" yaml:"cluster_online"`
	PricingAvailable   bool    `json:"pricing_available" yaml:"pricing_available"`
}

// ClusterCostSummary is a cluster's latest cost snapshot. The component
// fields (compute, storage, network, control plane, infrastructure, idle,
// unallocated) are HOURLY cents; the monthly, month-to-date and projected
// totals are already monthly.
type ClusterCostSummary struct {
	Provider                 string  `json:"provider" yaml:"provider"`
	Currency                 string  `json:"currency" yaml:"currency"`
	TotalNodeCount           int     `json:"total_node_count" yaml:"total_node_count"`
	PricedNodeCount          int     `json:"priced_node_count" yaml:"priced_node_count"`
	CoverageIncomplete       bool    `json:"coverage_incomplete" yaml:"coverage_incomplete"`
	HourlyCostCents          int64   `json:"hourly_cost_cents" yaml:"hourly_cost_cents"`
	MonthlyCostEstimateCents int64   `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
	MonthToDateCents         int64   `json:"month_to_date_cents" yaml:"month_to_date_cents"`
	ProjectedMonthEndCents   int64   `json:"projected_month_end_cents" yaml:"projected_month_end_cents"`
	ConfidenceLevel          string  `json:"confidence_level" yaml:"confidence_level"`
	IsEstimated              bool    `json:"is_estimated" yaml:"is_estimated"`
	SnapshotAt               string  `json:"snapshot_at" yaml:"snapshot_at"`
	ComputeOnDemandCents     int64   `json:"compute_on_demand_cents" yaml:"compute_on_demand_cents"`
	ComputeSpotCents         int64   `json:"compute_spot_cents" yaml:"compute_spot_cents"`
	StorageCents             int64   `json:"storage_cents" yaml:"storage_cents"`
	NetworkCents             int64   `json:"network_cents" yaml:"network_cents"`
	ControlPlaneCents        int64   `json:"control_plane_cents" yaml:"control_plane_cents"`
	InfrastructureCents      int64   `json:"infrastructure_cents" yaml:"infrastructure_cents"`
	IdleHourlyCents          int64   `json:"idle_hourly_cents" yaml:"idle_hourly_cents"`
	UnallocatedHourlyCents   int64   `json:"unallocated_hourly_cents" yaml:"unallocated_hourly_cents"`
	SpotNodeCount            int     `json:"spot_node_count" yaml:"spot_node_count"`
	OnDemandNodeCount        int     `json:"on_demand_node_count" yaml:"on_demand_node_count"`
	StorageVolumeCount       int     `json:"storage_volume_count" yaml:"storage_volume_count"`
	UnpricedVolumeCount      int     `json:"unpriced_volume_count" yaml:"unpriced_volume_count"`
	AppliedDiscountPct       float64 `json:"applied_discount_pct" yaml:"applied_discount_pct"`
}

// CostTrendPoint is one day of the monthly estimate series.
type CostTrendPoint struct {
	Day                      string `json:"day" yaml:"day"`
	MonthlyCostEstimateCents int64  `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
}

// NamespaceCost is a namespace's allocated share of the cluster estimate.
type NamespaceCost struct {
	Namespace                    string  `json:"namespace" yaml:"namespace"`
	StackID                      *string `json:"stack_id" yaml:"stack_id"`
	AllocatedHourlyCents         int64   `json:"allocated_hourly_cents" yaml:"allocated_hourly_cents"`
	AllocatedMonthlyCents        int64   `json:"allocated_monthly_cents" yaml:"allocated_monthly_cents"`
	AllocatedComputeMonthlyCents int64   `json:"allocated_compute_monthly_cents" yaml:"allocated_compute_monthly_cents"`
	AllocatedStorageMonthlyCents int64   `json:"allocated_storage_monthly_cents" yaml:"allocated_storage_monthly_cents"`
	CPUShare                     float64 `json:"cpu_share" yaml:"cpu_share"`
	MemoryShare                  float64 `json:"memory_share" yaml:"memory_share"`
	AllocationSource             string  `json:"allocation_source" yaml:"allocation_source"`
}

// ClusterCost is GET /org/clusters/{cluster_id}/cost. HasData false means no
// snapshot exists yet; Readiness then says why.
type ClusterCost struct {
	HasData    bool                `json:"has_data" yaml:"has_data"`
	Summary    *ClusterCostSummary `json:"summary" yaml:"summary"`
	Trend      []CostTrendPoint    `json:"trend" yaml:"trend"`
	Namespaces []NamespaceCost     `json:"namespaces" yaml:"namespaces"`
	Readiness  *CostReadiness      `json:"readiness,omitempty" yaml:"readiness,omitempty"`
}

// CostSettings is the organisation's pricing configuration: the display
// currency (usd, eur or gbp), the effective discount in percent applied on
// top of list prices, and whether a network egress estimate is included.
type CostSettings struct {
	EffectiveDiscountPct         float64 `json:"effective_discount_pct" yaml:"effective_discount_pct"`
	Currency                     string  `json:"currency" yaml:"currency"`
	IncludeNetworkEgressEstimate bool    `json:"include_network_egress_estimate" yaml:"include_network_egress_estimate"`
}

// GetFleetCloudCost returns the organisation-wide cost rollup.
// GET /api/v1/org/cloud-cost/summary
func (c *Client) GetFleetCloudCost() (*FleetCloudCost, error) {
	var result FleetCloudCost
	if err := c.sendJSON(http.MethodGet, c.BaseURL+costBasePath+"/summary", nil, &result); err != nil {
		return nil, err
	}
	if result.ByProvider == nil {
		result.ByProvider = []FleetProviderCost{}
	}
	if result.TopClusters == nil {
		result.TopClusters = []FleetClusterCost{}
	}
	return &result, nil
}

// GetClusterCost returns a cluster's latest cost estimate with its namespace
// allocation and daily trend, or its readiness when there is no estimate yet.
// GET /api/v1/org/clusters/{cluster_id}/cost
func (c *Client) GetClusterCost(clusterID string) (*ClusterCost, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/%s/cost", c.BaseURL, neturl.PathEscape(clusterID))
	var result ClusterCost
	if err := c.sendJSON(http.MethodGet, url, nil, &result); err != nil {
		return nil, err
	}
	if result.Trend == nil {
		result.Trend = []CostTrendPoint{}
	}
	if result.Namespaces == nil {
		result.Namespaces = []NamespaceCost{}
	}
	return &result, nil
}

// GetCostSettings returns the organisation's pricing configuration.
// GET /api/v1/org/cloud-cost/settings
func (c *Client) GetCostSettings() (*CostSettings, error) {
	var result CostSettings
	if err := c.sendJSON(http.MethodGet, c.BaseURL+costBasePath+"/settings", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateCostSettings replaces the organisation's pricing configuration. The
// route is organisation-admin only and guarded by the CSRF double-submit.
// PUT /api/v1/org/cloud-cost/settings
func (c *Client) UpdateCostSettings(settings CostSettings) (*CostSettings, error) {
	var result CostSettings
	if err := c.putCSRFJSON(c.BaseURL+costBasePath+"/settings", settings, &result, "update cost settings"); err != nil {
		return nil, err
	}
	return &result, nil
}
