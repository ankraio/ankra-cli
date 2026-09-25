package client

import (
	"net/http"
	"net/url"
	"strings"
)

// The object kinds GET /api/v1/org/cloud-cost/objects/{kind}/... serves a
// cost projection for (ankra-cozgu.3.5.1).
const (
	ObjectCostKindCluster     = "cluster"
	ObjectCostKindNamespace   = "namespace"
	ObjectCostKindStack       = "stack"
	ObjectCostKindApplication = "application"
	ObjectCostKindCredential  = "credential"
)

// ObjectCostProjection is what one cluster, namespace, stack, application or
// credential costs now and over the last 30 days. Every field is always
// present; a figure the platform cannot give is null, never zero. When
// Priced is false every figure is null and UnpricedReason says why.
// CoverageIncomplete true makes MonthlyCents a floor: a cluster behind the
// object could not be priced in full or contributed nothing.
type ObjectCostProjection struct {
	Kind   string            `json:"kind" yaml:"kind"`
	Object ObjectCostSubject `json:"object" yaml:"object"`
	// Currency is the display currency; every *_cents figure is in its
	// minor units.
	Currency           string          `json:"currency" yaml:"currency"`
	Priced             bool            `json:"priced" yaml:"priced"`
	UnpricedReason     *string         `json:"unpriced_reason" yaml:"unpriced_reason"`
	MonthlyCents       *int64          `json:"monthly_cents" yaml:"monthly_cents"`
	IdlePct            *float64        `json:"idle_pct" yaml:"idle_pct"`
	ShareOfFleetPct    *float64        `json:"share_of_fleet_pct" yaml:"share_of_fleet_pct"`
	Confidence         *string         `json:"confidence" yaml:"confidence"`
	CoverageIncomplete *bool           `json:"coverage_incomplete" yaml:"coverage_incomplete"`
	OpenWaste          ObjectCostWaste `json:"open_waste" yaml:"open_waste"`
	// Trend30d is 30 UTC days, oldest first.
	Trend30d                []ObjectCostDay       `json:"trend_30d" yaml:"trend_30d"`
	Clusters                []ObjectCostCluster   `json:"clusters" yaml:"clusters"`
	Namespaces              []ObjectCostNamespace `json:"namespaces" yaml:"namespaces"`
	SnapshotStaleAfterHours int                   `json:"snapshot_stale_after_hours" yaml:"snapshot_stale_after_hours"`
}

// ObjectCostSubject names the object. ClusterID and ClusterName are set for
// an object that lives on one cluster.
type ObjectCostSubject struct {
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	ClusterID   *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName *string `json:"cluster_name" yaml:"cluster_name"`
}

// ObjectCostWaste is the open cloud waste attributed to the object.
// Available false means waste is not attributed to this kind of object,
// which is unknown, not none; Reason says why.
type ObjectCostWaste struct {
	Available     bool    `json:"available" yaml:"available"`
	Reason        *string `json:"reason" yaml:"reason"`
	Count         int     `json:"count" yaml:"count"`
	MonthlyCents  *int64  `json:"monthly_cents" yaml:"monthly_cents"`
	UnpricedCount int     `json:"unpriced_count" yaml:"unpriced_count"`
}

// ObjectCostDay is one UTC day of the trend. Cents is null on a day none of
// the object's clusters was metered: unknown, never zero.
type ObjectCostDay struct {
	Date            string `json:"date" yaml:"date"`
	Cents           *int64 `json:"cents" yaml:"cents"`
	ClustersMetered int    `json:"clusters_metered" yaml:"clusters_metered"`
	ClustersTotal   int    `json:"clusters_total" yaml:"clusters_total"`
}

// ObjectCostCluster is one cluster behind the object. MonthlyCents is null
// when the cluster contributed nothing to the object's figure.
type ObjectCostCluster struct {
	ClusterID    string  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName  string  `json:"cluster_name" yaml:"cluster_name"`
	Priced       bool    `json:"priced" yaml:"priced"`
	MonthlyCents *int64  `json:"monthly_cents" yaml:"monthly_cents"`
	Confidence   *string `json:"confidence" yaml:"confidence"`
}

// ObjectCostNamespace is one namespace behind a namespace-based object.
// Shared says another application also runs in it, so its whole cost is
// counted for each.
type ObjectCostNamespace struct {
	ClusterID    string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName  string `json:"cluster_name" yaml:"cluster_name"`
	Namespace    string `json:"namespace" yaml:"namespace"`
	MonthlyCents *int64 `json:"monthly_cents" yaml:"monthly_cents"`
	Shared       bool   `json:"shared" yaml:"shared"`
}

// GetObjectCost reads one object's cost projection from
// /api/v1/org/cloud-cost/objects/{kind}/{pathSegments...}: the cluster id
// for a cluster; the cluster id and the namespace for a namespace; the
// cluster id and the stack name for a stack; the application id; or the
// credential id. Each segment is path-escaped, so a name carrying a space
// or a slash stays one segment.
func (c *Client) GetObjectCost(kind string, pathSegments ...string) (*ObjectCostProjection, error) {
	escaped := make([]string, 0, len(pathSegments)+1)
	escaped = append(escaped, url.PathEscape(kind))
	for _, segment := range pathSegments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	endpoint := c.BaseURL + "/api/v1/org/cloud-cost/objects/" + strings.Join(escaped, "/")
	var result ObjectCostProjection
	if err := c.sendJSON(http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	if result.Trend30d == nil {
		result.Trend30d = []ObjectCostDay{}
	}
	if result.Clusters == nil {
		result.Clusters = []ObjectCostCluster{}
	}
	if result.Namespaces == nil {
		result.Namespaces = []ObjectCostNamespace{}
	}
	return &result, nil
}
