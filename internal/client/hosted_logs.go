package client

// The per-cluster "Ship logs to Ankra" switch (ankra-t5jf5.34.11.7): GET/PUT
// /api/v1/org/clusters/{cluster_id}/hosted-logs, the bearer twin of the
// routes the portal's cluster settings use.
//
// Hosted log shipping is opt-in per cluster: customer log content leaves a
// cluster only after a member with clusters.write turns the switch on, and
// it is off by default. The platform stores the switch and hands it to the
// cluster's agent on every check-in; an agent too old to read that
// directive does not follow it, which AgentSupportsSwitch reports.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ClusterHostedLogs is the wire shape both the GET and the PUT answer with.
//
// Available says whether Ankra's hosted log store is live on this platform.
// While it is false the switch can still be stored, but nothing ships and
// nothing is read until it becomes true. AgentSupportsSwitch is what the
// cluster's agent advertised at its last identify, not a property of the
// switch. ChangedAt is nil until the switch is written for the first time.
type ClusterHostedLogs struct {
	ClusterID           string  `json:"cluster_id" yaml:"cluster_id"`
	ShippingEnabled     bool    `json:"shipping_enabled" yaml:"shipping_enabled"`
	Available           bool    `json:"available" yaml:"available"`
	AgentSupportsSwitch bool    `json:"agent_supports_switch" yaml:"agent_supports_switch"`
	ChangedAt           *string `json:"changed_at" yaml:"changed_at"`
}

// clusterHostedLogsUpdate is the PUT body. ShippingEnabled is always sent:
// the route refuses a body without a boolean shipping_enabled (422).
type clusterHostedLogsUpdate struct {
	ShippingEnabled bool `json:"shipping_enabled"`
}

// clusterHostedLogsURL builds the switch URL for one cluster. The id is
// escaped because it reaches the client straight from --cluster resolution.
func clusterHostedLogsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/hosted-logs", baseURL, url.PathEscape(clusterID))
}

// GetClusterHostedLogs reads one cluster's hosted log shipping switch.
func (c *Client) GetClusterHostedLogs(ctx context.Context, clusterID string) (*ClusterHostedLogs, error) {
	var state ClusterHostedLogs
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		clusterHostedLogsURL(c.BaseURL, clusterID), nil, &state); getError != nil {
		return nil, getError
	}
	return &state, nil
}

// SetClusterHostedLogShipping turns one cluster's hosted log shipping on or
// off and returns the resulting state. The platform gates the write on
// clusters.write; a refusal comes back as the client's usual error types.
func (c *Client) SetClusterHostedLogShipping(ctx context.Context, clusterID string, enabled bool) (*ClusterHostedLogs, error) {
	var state ClusterHostedLogs
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		clusterHostedLogsURL(c.BaseURL, clusterID), clusterHostedLogsUpdate{ShippingEnabled: enabled}, &state); putError != nil {
		return nil, putError
	}
	return &state, nil
}
