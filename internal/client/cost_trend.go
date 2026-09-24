package client

import (
	"fmt"
	"net/http"
)

// FleetCostTrendPoint is one UTC day of the fleet line. MonthlyCostEstimateCents
// is the run rates of the clusters priced that day, summed, and nil on a day
// no cluster was priced: that day is unknown, not free. A day with fewer fully
// priced clusters than priced ones is a floor. Current marks the last point,
// which is the summary's current run rate rather than a closed day.
type FleetCostTrendPoint struct {
	Day                      string `json:"day" yaml:"day"`
	MonthlyCostEstimateCents *int64 `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
	PricedClusterCount       int    `json:"priced_cluster_count" yaml:"priced_cluster_count"`
	FullyPricedClusterCount  int    `json:"fully_priced_cluster_count" yaml:"fully_priced_cluster_count"`
	Current                  bool   `json:"current" yaml:"current"`
}

// CostCoverageChangeCluster is a cluster that entered or left pricing on a
// coverage change, with its run rate that day (entered) or the day before
// (left).
type CostCoverageChangeCluster struct {
	ClusterID                string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName              string `json:"cluster_name" yaml:"cluster_name"`
	MonthlyCostEstimateCents int64  `json:"monthly_cost_estimate_cents" yaml:"monthly_cost_estimate_cents"`
}

// CostCoverageChange is a day the set of priced clusters changed.
// CoverageDeltaMonthlyCents is what that moved the fleet line by (entering
// run rates less leaving ones): a coverage move, never a spend move.
type CostCoverageChange struct {
	Day                       string                      `json:"day" yaml:"day"`
	PricedClusterCountBefore  int                         `json:"priced_cluster_count_before" yaml:"priced_cluster_count_before"`
	PricedClusterCountAfter   int                         `json:"priced_cluster_count_after" yaml:"priced_cluster_count_after"`
	Entered                   []CostCoverageChangeCluster `json:"entered" yaml:"entered"`
	Left                      []CostCoverageChangeCluster `json:"left" yaml:"left"`
	CoverageDeltaMonthlyCents int64                       `json:"coverage_delta_monthly_cents" yaml:"coverage_delta_monthly_cents"`
}

// FleetCostTrendCluster is one cluster's coverage over the trend window.
// FirstPricedAt is exact only when FirstPricedExact; otherwise it is the
// earliest priced hour still known (the cluster was priced by then, possibly
// long before), and nil when it cannot be read.
type FleetCostTrendCluster struct {
	ClusterID        string  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName      string  `json:"cluster_name" yaml:"cluster_name"`
	Deleted          bool    `json:"deleted" yaml:"deleted"`
	FirstPricedAt    *string `json:"first_priced_at" yaml:"first_priced_at"`
	FirstPricedExact bool    `json:"first_priced_exact" yaml:"first_priced_exact"`
	FirstDay         string  `json:"first_day" yaml:"first_day"`
	LastDay          string  `json:"last_day" yaml:"last_day"`
	PricedDays       int     `json:"priced_days" yaml:"priced_days"`
}

// FleetCostTrend is GET /org/cloud-cost/trend: the organisation's run rate
// per UTC day over every priced cluster, oldest first, in the display
// currency, with the days the set of priced clusters changed drawn apart from
// the spend.
type FleetCostTrend struct {
	Currency        string                  `json:"currency" yaml:"currency"`
	GeneratedAt     string                  `json:"generated_at" yaml:"generated_at"`
	Days            int                     `json:"days" yaml:"days"`
	Points          []FleetCostTrendPoint   `json:"points" yaml:"points"`
	CoverageChanges []CostCoverageChange    `json:"coverage_changes" yaml:"coverage_changes"`
	Clusters        []FleetCostTrendCluster `json:"clusters" yaml:"clusters"`
}

// CostEvent is one thing that moved the cost run rate. Kind is one of
// application_release, node_count, power_schedule, power_manual, decision,
// coverage or waste_resolved. DeltaStatus says how the move was measured:
// isolated (alone in its window, so DeltaMonthlyCents is its own), shared
// (the window's move, WindowDeltaMonthlyCents, is several events' together),
// no_snapshots (no priced hour on one side: unknown, never zero), coverage (a
// cluster entering or leaving pricing, not spend), own_cost (a resolved
// finding's own monthly cost), unpriced, or not_applied (the action did not
// take effect). A nil delta is unknown, never zero. ClusterID is nil for an
// account-level event.
type CostEvent struct {
	At                      string  `json:"at" yaml:"at"`
	Day                     string  `json:"day" yaml:"day"`
	Kind                    string  `json:"kind" yaml:"kind"`
	ClusterID               *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName             *string `json:"cluster_name" yaml:"cluster_name"`
	Subject                 string  `json:"subject" yaml:"subject"`
	Actor                   *string `json:"actor" yaml:"actor"`
	DeltaMonthlyCents       *int64  `json:"delta_monthly_cents" yaml:"delta_monthly_cents"`
	WindowDeltaMonthlyCents *int64  `json:"window_delta_monthly_cents" yaml:"window_delta_monthly_cents"`
	DeltaStatus             string  `json:"delta_status" yaml:"delta_status"`
	Note                    string  `json:"note" yaml:"note"`
}

// CostEventSource is one kind of event and whether it could be read. A kind
// that is not available is missing from the events, not quiet.
type CostEventSource struct {
	Kind      string `json:"kind" yaml:"kind"`
	Available bool   `json:"available" yaml:"available"`
}

// CostEvents is GET /org/cloud-cost/events: every move on the cost line over
// the last Days UTC days, newest first. Truncated says the platform stopped at
// its newest 500.
type CostEvents struct {
	Currency    string            `json:"currency" yaml:"currency"`
	GeneratedAt string            `json:"generated_at" yaml:"generated_at"`
	Days        int               `json:"days" yaml:"days"`
	Events      []CostEvent       `json:"events" yaml:"events"`
	Truncated   bool              `json:"truncated" yaml:"truncated"`
	Sources     []CostEventSource `json:"sources" yaml:"sources"`
}

// costTrendDaysQuery is the ?days= suffix, or nothing when days is zero so
// the platform applies its own default window.
func costTrendDaysQuery(days int) string {
	if days == 0 {
		return ""
	}
	return fmt.Sprintf("?days=%d", days)
}

// GetFleetCostTrend returns the organisation's run rate per day over the last
// days UTC days (0 leaves the window to the platform's default).
// GET /api/v1/org/cloud-cost/trend
func (c *Client) GetFleetCostTrend(days int) (*FleetCostTrend, error) {
	var result FleetCostTrend
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/trend"+costTrendDaysQuery(days), nil, &result); err != nil {
		return nil, err
	}
	if result.Points == nil {
		result.Points = []FleetCostTrendPoint{}
	}
	if result.CoverageChanges == nil {
		result.CoverageChanges = []CostCoverageChange{}
	}
	for index := range result.CoverageChanges {
		if result.CoverageChanges[index].Entered == nil {
			result.CoverageChanges[index].Entered = []CostCoverageChangeCluster{}
		}
		if result.CoverageChanges[index].Left == nil {
			result.CoverageChanges[index].Left = []CostCoverageChangeCluster{}
		}
	}
	if result.Clusters == nil {
		result.Clusters = []FleetCostTrendCluster{}
	}
	return &result, nil
}

// GetCostEvents returns the events that moved the organisation's cost run
// rate over the last days UTC days (0 leaves the window to the platform's
// default).
// GET /api/v1/org/cloud-cost/events
func (c *Client) GetCostEvents(days int) (*CostEvents, error) {
	var result CostEvents
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/events"+costTrendDaysQuery(days), nil, &result); err != nil {
		return nil, err
	}
	if result.Events == nil {
		result.Events = []CostEvent{}
	}
	if result.Sources == nil {
		result.Sources = []CostEventSource{}
	}
	return &result, nil
}
