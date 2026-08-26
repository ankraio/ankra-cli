package client

// Playground: a real, writable vcluster Ankra provisions for the
// organisation on its shared playground host. Every organisation may hold
// one.

import (
	"fmt"
	neturl "net/url"
)

// CreatePlaygroundResult is the POST body.
type CreatePlaygroundResult struct {
	ClusterID string `json:"cluster_id"`
	Success   bool   `json:"success"`
}

// PlaygroundOrderedPlan is the size the environment was ordered at and its
// price of record; OrderedAt is null for a free environment nobody ordered.
type PlaygroundOrderedPlan struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"display_name"`
	Vcpus             int     `json:"vcpus"`
	MemoryGB          float64 `json:"memory_gb"`
	PriceMonthlyCents int     `json:"price_monthly_cents"`
	Currency          string  `json:"currency"`
	OrderedAt         *string `json:"ordered_at"`
}

// PlaygroundStatus is the provisioning state machine's public shape.
type PlaygroundStatus struct {
	ClusterID     string                 `json:"cluster_id"`
	Phase         string                 `json:"phase"`
	StatusMessage *string                `json:"status_message"`
	ExpiresAt     string                 `json:"expires_at"`
	Plan          *PlaygroundOrderedPlan `json:"plan"`
}

// PlaygroundPlan is one orderable playground size with its price.
type PlaygroundPlan struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"display_name"`
	Summary           string  `json:"summary"`
	Vcpus             int     `json:"vcpus"`
	MemoryGB          float64 `json:"memory_gb"`
	StorageGB         int     `json:"storage_gb"`
	MaxPods           int     `json:"max_pods"`
	PriceMonthlyCents int     `json:"price_monthly_cents"`
	Currency          string  `json:"currency"`
	Available         bool    `json:"available"`
}

// PlaygroundPlanCatalog is the GET body of the plans route.
type PlaygroundPlanCatalog struct {
	Plans         []PlaygroundPlan `json:"plans"`
	DefaultPlanID string           `json:"default_plan_id"`
	Currency      string           `json:"currency"`
	// OrganisationHasPaidPlan reports whether a paid size can be ordered at
	// all - ordering one without a paid billing plan is refused with 402.
	OrganisationHasPaidPlan bool `json:"organisation_has_paid_plan"`
}

// ListPlaygroundPlans reads the orderable sizes with their prices and
// current availability.
func (c *Client) ListPlaygroundPlans() (*PlaygroundPlanCatalog, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground/plans", c.BaseURL)
	var catalog PlaygroundPlanCatalog
	if err := c.getJSON(url, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

// createPlaygroundRequest is the order body; an empty plan is omitted and
// means the free trial.
type createPlaygroundRequest struct {
	Plan string `json:"plan"`
}

// CreatePlayground provisions the organisation's playground at the given
// size; an empty planID orders the free trial. A 409 means one already
// exists; a 503 means the shared host is at capacity; a 402 means a paid
// size needs a billing plan. All surface as the server's detail text through
// the shared unexpected-response error.
func (c *Client) CreatePlayground(planID string) (*CreatePlaygroundResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground", c.BaseURL)
	var body any
	if planID != "" {
		body = createPlaygroundRequest{Plan: planID}
	}
	var result CreatePlaygroundResult
	if err := c.postCSRFJSON(url, body, &result, "create playground"); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPlaygroundStatus reads the provisioning phase.
func (c *Client) GetPlaygroundStatus(clusterID string) (*PlaygroundStatus, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground/%s/status",
		c.BaseURL, neturl.PathEscape(clusterID))
	var status PlaygroundStatus
	if err := c.getJSON(url, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DestroyPlaygroundResult is the DELETE body: the phase the environment
// holds after the call, which is `deprovisioning` once teardown is
// scheduled, or the terminal phase it had already reached.
type DestroyPlaygroundResult struct {
	ClusterID string `json:"cluster_id"`
	Phase     string `json:"phase"`
}

// DestroyPlayground tears the organisation's playground down. Idempotent -
// an environment already deprovisioning or removed answers its current
// phase rather than an error, so a retry after a dropped response is not a
// failure.
//
// This is also the way out of a refused `ankra org domain set`: a live
// playground's wildcard DNS record is reconciled, so it is re-created every
// time an admin deletes it, and destroying the environment is what actually
// clears that blocker.
func (c *Client) DestroyPlayground(clusterID string) (*DestroyPlaygroundResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground/%s",
		c.BaseURL, neturl.PathEscape(clusterID))
	var result DestroyPlaygroundResult
	if err := c.deleteCSRFJSON(url, &result, "destroy playground"); err != nil {
		return nil, err
	}
	return &result, nil
}
