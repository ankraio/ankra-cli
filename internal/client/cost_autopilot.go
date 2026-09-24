package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// CostAutopilotQuietHours is a daily window, in an IANA timezone, when the
// autopilot starts no change of its own; an end before the start runs past
// midnight. Start and End are 24-hour HH:MM.
type CostAutopilotQuietHours struct {
	Start    string `json:"start" yaml:"start"`
	End      string `json:"end" yaml:"end"`
	Timezone string `json:"timezone" yaml:"timezone"`
}

// CostAutopilotTier describes what a tier (hands-off, scheduled, managed or
// ephemeral) lets Ankra do on its own and what it proposes instead.
type CostAutopilotTier struct {
	Tier           string `json:"tier" yaml:"tier"`
	Name           string `json:"name" yaml:"name"`
	AppliesTo      string `json:"applies_to" yaml:"applies_to"`
	Does           string `json:"does" yaml:"does"`
	Proposes       string `json:"proposes" yaml:"proposes"`
	RollbackWindow string `json:"rollback_window" yaml:"rollback_window"`
}

// CostAutopilotOverride is a tier a person set on one cluster, active until
// ExpiresAt (nil for never). SetBy is the Ankra user id of that person.
type CostAutopilotOverride struct {
	Tier      string  `json:"tier" yaml:"tier"`
	Reason    string  `json:"reason" yaml:"reason"`
	SetBy     string  `json:"set_by" yaml:"set_by"`
	SetAt     string  `json:"set_at" yaml:"set_at"`
	ExpiresAt *string `json:"expires_at" yaml:"expires_at"`
}

// CostAutopilotCluster is a live cluster's tier and why: Source is override
// or environment, Override is nil when the environment set the tier, and
// Reason is the sentence a card shows. EnvironmentKind is production,
// staging, development, preview or unknown.
type CostAutopilotCluster struct {
	ClusterID       string                 `json:"cluster_id" yaml:"cluster_id"`
	ClusterName     string                 `json:"cluster_name" yaml:"cluster_name"`
	Environment     string                 `json:"environment" yaml:"environment"`
	EnvironmentKind string                 `json:"environment_kind" yaml:"environment_kind"`
	Override        *CostAutopilotOverride `json:"override" yaml:"override"`
	Reason          string                 `json:"reason" yaml:"reason"`
	Source          string                 `json:"source" yaml:"source"`
	Tier            string                 `json:"tier" yaml:"tier"`
}

// CostAutopilotPolicy is GET /org/cloud-cost/autopilot: how much Ankra may do
// about cost unasked. Defaults holds the tier every environment kind defaults
// to. IsSet is false for an organisation that never set a policy, when every
// kind is hands-off; RecommendedDefaults is the policy the portal offers to
// turn on. QuietHours and NotificationRouteID are nil for none and for the
// organisation's default routing. UpdatedBy and UpdatedAt are nil until a
// policy is set.
type CostAutopilotPolicy struct {
	IsSet               bool                     `json:"is_set" yaml:"is_set"`
	Defaults            map[string]string        `json:"defaults" yaml:"defaults"`
	RecommendedDefaults map[string]string        `json:"recommended_defaults" yaml:"recommended_defaults"`
	QuietHours          *CostAutopilotQuietHours `json:"quiet_hours" yaml:"quiet_hours"`
	NotificationRouteID *string                  `json:"notification_route_id" yaml:"notification_route_id"`
	UpdatedBy           *string                  `json:"updated_by" yaml:"updated_by"`
	UpdatedAt           *string                  `json:"updated_at" yaml:"updated_at"`
	EnvironmentKinds    []string                 `json:"environment_kinds" yaml:"environment_kinds"`
	Tiers               []CostAutopilotTier      `json:"tiers" yaml:"tiers"`
	Clusters            []CostAutopilotCluster   `json:"clusters" yaml:"clusters"`
}

// CostAutopilotPolicyUpdate is a partial policy write. Only what is set is
// sent, so every other part of the policy keeps its value: Defaults names
// only the environment kinds it changes, QuietHours or ClearQuietHours sets
// or clears the quiet hours (null), and NotificationRouteID or
// ClearNotificationRoute sets or clears the pre-notice route (null).
type CostAutopilotPolicyUpdate struct {
	Defaults               map[string]string
	QuietHours             *CostAutopilotQuietHours
	ClearQuietHours        bool
	NotificationRouteID    *string
	ClearNotificationRoute bool
}

// Body is the JSON object the write sends: the given parts and nothing else.
func (update CostAutopilotPolicyUpdate) Body() map[string]any {
	body := map[string]any{}
	if len(update.Defaults) > 0 {
		body["defaults"] = update.Defaults
	}
	switch {
	case update.ClearQuietHours:
		body["quiet_hours"] = nil
	case update.QuietHours != nil:
		body["quiet_hours"] = *update.QuietHours
	}
	switch {
	case update.ClearNotificationRoute:
		body["notification_route_id"] = nil
	case update.NotificationRouteID != nil:
		body["notification_route_id"] = *update.NotificationRouteID
	}
	return body
}

// CostAutopilotOverrideRequest puts one cluster in a tier whatever its
// environment kind. ExpiresAt is sent only when set; omitted means the
// override never expires.
type CostAutopilotOverrideRequest struct {
	Tier      string  `json:"tier"`
	Reason    string  `json:"reason"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// GetCostAutopilot returns the organisation's autopilot policy with every
// live cluster's tier under it.
// GET /api/v1/org/cloud-cost/autopilot
func (c *Client) GetCostAutopilot() (*CostAutopilotPolicy, error) {
	var result CostAutopilotPolicy
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/autopilot", nil, &result); err != nil {
		return nil, err
	}
	normaliseCostAutopilotPolicy(&result)
	return &result, nil
}

// UpdateCostAutopilot writes the given parts of the policy. It needs
// billing.manage and clusters.write at the organisation.
// PUT /api/v1/org/cloud-cost/autopilot
func (c *Client) UpdateCostAutopilot(update CostAutopilotPolicyUpdate) (*CostAutopilotPolicy, error) {
	var result CostAutopilotPolicy
	if err := c.sendJSON(http.MethodPut, c.BaseURL+"/api/v1/org/cloud-cost/autopilot", update.Body(), &result); err != nil {
		return nil, err
	}
	normaliseCostAutopilotPolicy(&result)
	return &result, nil
}

// SetCostAutopilotOverride puts one cluster in a tier, replacing its previous
// override, and returns the cluster's tier as it now stands. It needs
// billing.manage and clusters.write at the cluster.
// PUT /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id}
func (c *Client) SetCostAutopilotOverride(clusterID string, request CostAutopilotOverrideRequest) (*CostAutopilotCluster, error) {
	url := fmt.Sprintf("%s/api/v1/org/cloud-cost/autopilot/clusters/%s", c.BaseURL, neturl.PathEscape(clusterID))
	var result CostAutopilotCluster
	if err := c.sendJSON(http.MethodPut, url, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ClearCostAutopilotOverride returns one cluster to its environment's default
// tier and returns the cluster's tier as it now stands.
// DELETE /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id}
func (c *Client) ClearCostAutopilotOverride(clusterID string) (*CostAutopilotCluster, error) {
	url := fmt.Sprintf("%s/api/v1/org/cloud-cost/autopilot/clusters/%s", c.BaseURL, neturl.PathEscape(clusterID))
	var result CostAutopilotCluster
	if err := c.sendJSON(http.MethodDelete, url, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func normaliseCostAutopilotPolicy(policy *CostAutopilotPolicy) {
	if policy.Defaults == nil {
		policy.Defaults = map[string]string{}
	}
	if policy.RecommendedDefaults == nil {
		policy.RecommendedDefaults = map[string]string{}
	}
	if policy.EnvironmentKinds == nil {
		policy.EnvironmentKinds = []string{}
	}
	if policy.Tiers == nil {
		policy.Tiers = []CostAutopilotTier{}
	}
	if policy.Clusters == nil {
		policy.Clusters = []CostAutopilotCluster{}
	}
}
