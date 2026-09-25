package client

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// NamespaceCostHistory is one cluster's namespace allocations over a window,
// as GET /api/v1/org/clusters/{cluster_id}/cost/namespaces/history serves it
// (ankra-cozgu.3.3.1).
type NamespaceCostHistory struct {
	ClusterID   string                `json:"cluster_id"`
	Currency    string                `json:"currency"`
	Granularity string                `json:"granularity"`
	Days        int                   `json:"days"`
	From        time.Time             `json:"from"`
	To          time.Time             `json:"to"`
	Buckets     []NamespaceCostBucket `json:"buckets"`
	Namespaces  []NamespaceCostSeries `json:"namespaces"`
}

// NamespaceCostBucket is one day or hour of the window. AttributedHours is
// how many of its Hours the metering attributed to namespaces; with none,
// every namespace's value for the bucket is unknown.
type NamespaceCostBucket struct {
	Start           time.Time `json:"start"`
	Hours           int       `json:"hours"`
	AttributedHours int       `json:"attributed_hours"`
}

// NamespaceCostSeries is one namespace's allocation per bucket, in minor
// units of the history's currency. A nil value is a bucket the metering did
// not attribute (unknown); a 0 is one it attributed and gave the namespace
// nothing.
type NamespaceCostSeries struct {
	Namespace  string   `json:"namespace"`
	StackID    *string  `json:"stack_id"`
	CostCents  []*int64 `json:"cost_cents"`
	TotalCents int64    `json:"total_cents"`
}

// GetNamespaceCostHistory returns a cluster's namespace cost history. days
// 0 and an empty granularity leave the window to the platform's defaults
// (30 days by the day, 7 by the hour).
func (c *Client) GetNamespaceCostHistory(clusterID string, days int, granularity string) (*NamespaceCostHistory, error) {
	query := url.Values{}
	if days != 0 {
		query.Set("days", strconv.Itoa(days))
	}
	if granularity != "" {
		query.Set("granularity", granularity)
	}
	endpoint := c.BaseURL + "/api/v1/org/clusters/" + url.PathEscape(clusterID) + "/cost/namespaces/history"
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var result NamespaceCostHistory
	if err := c.sendJSON(http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	if result.Buckets == nil {
		result.Buckets = []NamespaceCostBucket{}
	}
	if result.Namespaces == nil {
		result.Namespaces = []NamespaceCostSeries{}
	}
	for index := range result.Namespaces {
		if result.Namespaces[index].CostCents == nil {
			result.Namespaces[index].CostCents = []*int64{}
		}
	}
	return &result, nil
}
