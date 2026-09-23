package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// Each cost route is written out as a whole /api/v1 literal rather than
// composed from a shared prefix: TestClusterRoutesAreRegistered checks the
// literals it can see against the cluster route census, and a prefix plus a
// suffix hides the full path from it.

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
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/summary", nil, &result); err != nil {
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
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/settings", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateCostSettings replaces the organisation's pricing configuration. The
// route is organisation-admin only and guarded by the CSRF double-submit.
// PUT /api/v1/org/cloud-cost/settings
func (c *Client) UpdateCostSettings(settings CostSettings) (*CostSettings, error) {
	var result CostSettings
	if err := c.putCSRFJSON(c.BaseURL+"/api/v1/org/cloud-cost/settings", settings, &result, "update cost settings"); err != nil {
		return nil, err
	}
	return &result, nil
}

// CloudSavingsEvidence carries the numbers a recommendation was computed
// from, per kind; members that do not apply to the kind are null.
type CloudSavingsEvidence struct {
	IdleMonthlyCents        *int64  `json:"idle_monthly_cents" yaml:"idle_monthly_cents"`
	UnallocatedMonthlyCents *int64  `json:"unallocated_monthly_cents" yaml:"unallocated_monthly_cents"`
	UnallocatedSharePercent *int    `json:"unallocated_share_percent" yaml:"unallocated_share_percent"`
	StoppableMonthlyCents   *int64  `json:"stoppable_monthly_cents" yaml:"stoppable_monthly_cents"`
	OffHoursSharePercent    *int    `json:"off_hours_share_percent" yaml:"off_hours_share_percent"`
	StopCron                *string `json:"stop_cron" yaml:"stop_cron"`
	StartCron               *string `json:"start_cron" yaml:"start_cron"`
}

// CloudSavingsRecommendation is one lever on one cluster. Kind is one of
// right_size_idle, reduce_unallocated or off_hours_schedule; ID is stable
// across reads (kind and cluster).
type CloudSavingsRecommendation struct {
	ID                  string               `json:"id" yaml:"id"`
	Kind                string               `json:"kind" yaml:"kind"`
	ClusterID           string               `json:"cluster_id" yaml:"cluster_id"`
	ClusterName         string               `json:"cluster_name" yaml:"cluster_name"`
	Provider            string               `json:"provider" yaml:"provider"`
	Environment         *string              `json:"environment" yaml:"environment"`
	MonthlySavingsCents int64                `json:"monthly_savings_cents" yaml:"monthly_savings_cents"`
	MonthlyCostCents    int64                `json:"monthly_cost_cents" yaml:"monthly_cost_cents"`
	SharePercent        int                  `json:"share_percent" yaml:"share_percent"`
	Evidence            CloudSavingsEvidence `json:"evidence" yaml:"evidence"`
}

// CloudSavingsComponent is one billed component of a cluster's run rate.
type CloudSavingsComponent struct {
	Key          string `json:"key" yaml:"key"`
	Label        string `json:"label" yaml:"label"`
	MonthlyCents int64  `json:"monthly_cents" yaml:"monthly_cents"`
}

// CloudSavingsNamespace is one namespace's allocated share of its cluster's
// run rate.
type CloudSavingsNamespace struct {
	ClusterID    string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName  string `json:"cluster_name" yaml:"cluster_name"`
	Namespace    string `json:"namespace" yaml:"namespace"`
	MonthlyCents int64  `json:"monthly_cents" yaml:"monthly_cents"`
	SharePercent int    `json:"share_percent" yaml:"share_percent"`
}

// CloudSavingsBreakdown is one analysed cluster's run rate split by
// component, with the idle and unallocated shares the recommendations read.
type CloudSavingsBreakdown struct {
	ClusterID               string                  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName             string                  `json:"cluster_name" yaml:"cluster_name"`
	Provider                string                  `json:"provider" yaml:"provider"`
	MonthlyCents            int64                   `json:"monthly_cents" yaml:"monthly_cents"`
	Components              []CloudSavingsComponent `json:"components" yaml:"components"`
	IdleMonthlyCents        int64                   `json:"idle_monthly_cents" yaml:"idle_monthly_cents"`
	UnallocatedMonthlyCents int64                   `json:"unallocated_monthly_cents" yaml:"unallocated_monthly_cents"`
	BestRecommendationID    *string                 `json:"best_recommendation_id" yaml:"best_recommendation_id"`
	TopNamespaces           []CloudSavingsNamespace `json:"top_namespaces" yaml:"top_namespaces"`
}

// CloudSavingsCluster names a cluster the model could not analyse: never
// priced (unpriced_clusters), priced before but with stalled metering
// (stale_clusters), or priced but unreadable on this pass
// (unreadable_clusters, where Kind is empty).
type CloudSavingsCluster struct {
	ClusterID   string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string `json:"cluster_name" yaml:"cluster_name"`
	Kind        string `json:"kind,omitempty" yaml:"kind,omitempty"`
}

// CloudSavingsWaste summarises the open cloud-waste findings. Available is
// false when the waste scan could not be read, which is not the same as no
// waste.
type CloudSavingsWaste struct {
	Available             bool    `json:"available" yaml:"available"`
	HasData               bool    `json:"has_data" yaml:"has_data"`
	ScannedAt             *string `json:"scanned_at" yaml:"scanned_at"`
	TotalMonthlyCostCents int64   `json:"total_monthly_cost_cents" yaml:"total_monthly_cost_cents"`
	FindingCount          int     `json:"finding_count" yaml:"finding_count"`
	UnpricedFindingCount  int     `json:"unpriced_finding_count" yaml:"unpriced_finding_count"`
}

// CloudSavingsThresholds echoes the model's constants, so a reading can be
// explained without hard-coding them again.
type CloudSavingsThresholds struct {
	MinimumSavingsCents         int64   `json:"minimum_savings_cents" yaml:"minimum_savings_cents"`
	MinimumOffHoursMonthlyCents int64   `json:"minimum_off_hours_monthly_cents" yaml:"minimum_off_hours_monthly_cents"`
	UnallocatedShareThreshold   float64 `json:"unallocated_share_threshold" yaml:"unallocated_share_threshold"`
	OffHoursShare               float64 `json:"off_hours_share" yaml:"off_hours_share"`
	AnalysedClusterLimit        int     `json:"analysed_cluster_limit" yaml:"analysed_cluster_limit"`
}

// CloudSavings is GET /org/cloud-cost/savings: the organisation's savings
// model in the display currency. TotalMonthlySavingsCents counts each
// cluster once, at its best lever. Only the biggest priced clusters are
// analysed; the unanalysed, unpriced, stale and unreadable ones are named
// so an unknown never reads as nothing to save.
type CloudSavings struct {
	Currency                 string                       `json:"currency" yaml:"currency"`
	GeneratedAt              string                       `json:"generated_at" yaml:"generated_at"`
	TotalMonthlySavingsCents int64                        `json:"total_monthly_savings_cents" yaml:"total_monthly_savings_cents"`
	Recommendations          []CloudSavingsRecommendation `json:"recommendations" yaml:"recommendations"`
	Breakdowns               []CloudSavingsBreakdown      `json:"breakdowns" yaml:"breakdowns"`
	Namespaces               []CloudSavingsNamespace      `json:"namespaces" yaml:"namespaces"`
	AnalysedClusterCount     int                          `json:"analysed_cluster_count" yaml:"analysed_cluster_count"`
	UnanalysedClusterCount   int                          `json:"unanalysed_cluster_count" yaml:"unanalysed_cluster_count"`
	PricedClusterCount       int                          `json:"priced_cluster_count" yaml:"priced_cluster_count"`
	UnpricedClusterCount     int                          `json:"unpriced_cluster_count" yaml:"unpriced_cluster_count"`
	UnpricedClusters         []CloudSavingsCluster        `json:"unpriced_clusters" yaml:"unpriced_clusters"`
	StaleClusterCount        int                          `json:"stale_cluster_count" yaml:"stale_cluster_count"`
	StaleClusters            []CloudSavingsCluster        `json:"stale_clusters" yaml:"stale_clusters"`
	UnreadableClusters       []CloudSavingsCluster        `json:"unreadable_clusters" yaml:"unreadable_clusters"`
	Waste                    CloudSavingsWaste            `json:"waste" yaml:"waste"`
	Thresholds               CloudSavingsThresholds       `json:"thresholds" yaml:"thresholds"`
}

// GetCloudSavings returns the organisation's savings model.
// GET /api/v1/org/cloud-cost/savings
func (c *Client) GetCloudSavings() (*CloudSavings, error) {
	var result CloudSavings
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/savings", nil, &result); err != nil {
		return nil, err
	}
	if result.Recommendations == nil {
		result.Recommendations = []CloudSavingsRecommendation{}
	}
	if result.Breakdowns == nil {
		result.Breakdowns = []CloudSavingsBreakdown{}
	}
	if result.Namespaces == nil {
		result.Namespaces = []CloudSavingsNamespace{}
	}
	if result.UnpricedClusters == nil {
		result.UnpricedClusters = []CloudSavingsCluster{}
	}
	if result.StaleClusters == nil {
		result.StaleClusters = []CloudSavingsCluster{}
	}
	if result.UnreadableClusters == nil {
		result.UnreadableClusters = []CloudSavingsCluster{}
	}
	return &result, nil
}

// CloudLedgerCounts is how many ledger rows sit in each measurement state.
// Unmeasured covers both unmeasured states (coverage moved, no snapshots).
// The counts cover the whole ledger, not only the rows returned.
type CloudLedgerCounts struct {
	Pending    int `json:"pending" yaml:"pending"`
	Measured   int `json:"measured" yaml:"measured"`
	Unmeasured int `json:"unmeasured" yaml:"unmeasured"`
	Reverted   int `json:"reverted" yaml:"reverted"`
}

// CloudLedgerVerificationDay is one judged day of a right-size's seven-day
// usage verification. State is clear, breach or unknown; a nil share is a
// resource no node reported that day.
type CloudLedgerVerificationDay struct {
	Day            int      `json:"day" yaml:"day"`
	From           string   `json:"from" yaml:"from"`
	To             string   `json:"to" yaml:"to"`
	State          string   `json:"state" yaml:"state"`
	CPUP95Share    *float64 `json:"cpu_p95_share" yaml:"cpu_p95_share"`
	MemoryP95Share *float64 `json:"memory_p95_share" yaml:"memory_p95_share"`
	HottestNode    string   `json:"hottest_node,omitempty" yaml:"hottest_node,omitempty"`
	Nodes          int      `json:"nodes" yaml:"nodes"`
	Reporting      int      `json:"reporting" yaml:"reporting"`
	Reason         string   `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// CloudLedgerRow is one cost decision that was approved, is running or has
// run: what it was expected to save and what was measured seven days after
// it ran. Every money figure is monthly, in the response currency, and nil
// when it is not known: nil is unknown, never zero. A negative measured
// figure is a real measurement (the run rate rose).
//
// MeasurementStatus is not_applicable, pending (inside the verification
// window), measured, unmeasured_coverage_moved, unmeasured_no_snapshots or
// reverted; MeasurementReason is the platform's sentence for an unmeasured
// or reverted row. Days is how many whole days of the window have passed.
//
// VerificationStatus and VerificationDays are a right-size's usage
// verification (verifying, passed, failed, unverified_no_metrics, or
// not_applicable for every other lever). Platforms that predate it do not
// send them, and they stay absent in structured output rather than reading
// as an empty verification.
type CloudLedgerRow struct {
	DecisionID           string                        `json:"decision_id" yaml:"decision_id"`
	ClusterID            *string                       `json:"cluster_id" yaml:"cluster_id"`
	ClusterName          *string                       `json:"cluster_name" yaml:"cluster_name"`
	Lever                string                        `json:"lever" yaml:"lever"`
	Summary              string                        `json:"summary" yaml:"summary"`
	Status               string                        `json:"status" yaml:"status"`
	ExpectedMonthlyCents *int64                        `json:"expected_monthly_cents" yaml:"expected_monthly_cents"`
	BaselineMonthlyCents *int64                        `json:"baseline_monthly_cents" yaml:"baseline_monthly_cents"`
	MeasuredMonthlyCents *int64                        `json:"measured_monthly_cents" yaml:"measured_monthly_cents"`
	MeasurementStatus    string                        `json:"measurement_status" yaml:"measurement_status"`
	MeasurementReason    *string                       `json:"measurement_reason" yaml:"measurement_reason"`
	Days                 *int                          `json:"days" yaml:"days"`
	DecidedAt            *string                       `json:"decided_at" yaml:"decided_at"`
	ExecutedAt           *string                       `json:"executed_at" yaml:"executed_at"`
	VerifyUntil          *string                       `json:"verify_until" yaml:"verify_until"`
	MeasuredAt           *string                       `json:"measured_at" yaml:"measured_at"`
	VerificationStatus   *string                       `json:"verification_status,omitempty" yaml:"verification_status,omitempty"`
	VerificationDays     *[]CloudLedgerVerificationDay `json:"verification_days,omitempty" yaml:"verification_days,omitempty"`
}

// CloudLedger is GET /org/cloud-cost/ledger: the measured outcomes of the
// organisation's cost decisions, newest first. MeasuredTotalCents sums the
// measured rows over the whole ledger (never an expectation);
// MeasuredThisMonthCents is the same sum over the rows measured in Month
// (the current UTC calendar month, YYYY-MM); RunningTotalCents sums the
// expectations of the changes approved, running or still verifying. The
// totals and counts cover every row even when Truncated says the row list
// stopped at the newest ones.
type CloudLedger struct {
	Currency               string            `json:"currency" yaml:"currency"`
	GeneratedAt            string            `json:"generated_at" yaml:"generated_at"`
	MeasuredTotalCents     int64             `json:"measured_total_cents" yaml:"measured_total_cents"`
	Month                  string            `json:"month" yaml:"month"`
	MeasuredThisMonthCents int64             `json:"measured_this_month_cents" yaml:"measured_this_month_cents"`
	RunningTotalCents      int64             `json:"running_total_cents" yaml:"running_total_cents"`
	Counts                 CloudLedgerCounts `json:"counts" yaml:"counts"`
	Rows                   []CloudLedgerRow  `json:"rows" yaml:"rows"`
	Truncated              bool              `json:"truncated" yaml:"truncated"`
}

// GetCloudLedger returns the measured outcomes of the organisation's cost
// decisions.
// GET /api/v1/org/cloud-cost/ledger
func (c *Client) GetCloudLedger() (*CloudLedger, error) {
	var result CloudLedger
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/ledger", nil, &result); err != nil {
		return nil, err
	}
	if result.Rows == nil {
		result.Rows = []CloudLedgerRow{}
	}
	return &result, nil
}
