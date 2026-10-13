package client

// The per-cluster control-plane self-heal switch (ankra-no2iak, ankra-c550m2):
// GET/PUT /api/v1/org/clusters/{cluster_id}/control-plane-self-heal, the
// bearer twin of the route the portal's cluster settings use.
//
// When a control plane has been NotReady for a while and the rest of the
// control plane and etcd are healthy, Ankra may restart it through the
// cloud provider. The switch is on by default and is the customer's to turn
// off. It stops the restart only: the NotReady card stays visible.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ControlPlaneHealIncident is the latest NotReady episode of a control plane.
// Mode is "shadow" when the platform only recorded what it would have done
// and "active" when it was allowed to restart the server. Outcome is empty
// while the episode is open.
type ControlPlaneHealIncident struct {
	NodeName        string  `json:"node_name" yaml:"node_name"`
	State           string  `json:"state" yaml:"state"`
	Outcome         string  `json:"outcome" yaml:"outcome"`
	Reason          string  `json:"reason" yaml:"reason"`
	RestartAttempts int     `json:"restart_attempts" yaml:"restart_attempts"`
	NotReadySince   string  `json:"not_ready_since" yaml:"not_ready_since"`
	LastRestartAt   *string `json:"last_restart_at" yaml:"last_restart_at"`
	ClosedAt        *string `json:"closed_at" yaml:"closed_at"`
	Mode            string  `json:"mode" yaml:"mode"`
}

// ControlPlaneHeal is the wire shape both the GET and the PUT answer with.
// LatestIncident is nil when the cluster never had a NotReady control plane.
type ControlPlaneHeal struct {
	ClusterID      string                    `json:"cluster_id" yaml:"cluster_id"`
	Enabled        bool                      `json:"enabled" yaml:"enabled"`
	LatestIncident *ControlPlaneHealIncident `json:"latest_incident" yaml:"latest_incident"`
}

// controlPlaneHealUpdate is the PUT body. Enabled is always sent: false is
// the point of turning the switch off, and the route refuses a body without
// a boolean.
type controlPlaneHealUpdate struct {
	Enabled bool `json:"enabled"`
}

func controlPlaneHealURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/control-plane-self-heal", baseURL, url.PathEscape(clusterID))
}

// GetControlPlaneHeal reads one cluster's switch and latest episode.
func (c *Client) GetControlPlaneHeal(ctx context.Context, clusterID string) (*ControlPlaneHeal, error) {
	var state ControlPlaneHeal
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		controlPlaneHealURL(c.BaseURL, clusterID), nil, &state); getError != nil {
		return nil, getError
	}
	return &state, nil
}

// SetControlPlaneHeal turns one cluster's automatic control-plane restart on
// or off and returns the resulting state. The platform gates the write on
// clusters.write.
func (c *Client) SetControlPlaneHeal(ctx context.Context, clusterID string, enabled bool) (*ControlPlaneHeal, error) {
	var state ControlPlaneHeal
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		controlPlaneHealURL(c.BaseURL, clusterID), controlPlaneHealUpdate{Enabled: enabled}, &state); putError != nil {
		return nil, putError
	}
	return &state, nil
}
