package client

// The per-cluster agent tolerations surface (ankra-o4jy76.2): GET/PUT
// /api/v1/org/clusters/{cluster_id}/agent/tolerations, beside the agent
// resources routes.
//
// The list is stored on the platform and every agent install, upgrade and
// re-render it generates carries it, on top of the tolerations the agent's
// own Helm release lists, which the platform neither reads nor changes here.
//
// A PUT replaces the whole list and then tries to apply it; ApplyState says
// which outcome happened and Message is the platform's own sentence for it.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The apply states the platform reports for a stored list. A newer platform
// may add one, so callers render an unrecognised state rather than treating
// the set as closed.
const (
	// AgentTolerationsApplyStateApplied means the agent accepts stored
	// tolerations and has checked in since they were stored.
	AgentTolerationsApplyStateApplied = "applied"
	// AgentTolerationsApplyStatePendingUpgrade means the list is stored and
	// not yet seen live, for example because the agent predates it.
	AgentTolerationsApplyStatePendingUpgrade = "pending_upgrade"
	// AgentTolerationsApplyStateAgentOffline means the agent is not
	// connected.
	AgentTolerationsApplyStateAgentOffline = "agent_offline"
	// AgentTolerationsApplyStateNotSet means the platform stores none.
	AgentTolerationsApplyStateNotSet = "not_set"
)

// AgentToleration is one taint the agent tolerates. Operator is Equal, which
// matches Value, or Exists, which matches any value of the key.
type AgentToleration struct {
	Key      string `json:"key" yaml:"key"`
	Operator string `json:"operator" yaml:"operator"`
	Value    string `json:"value,omitempty" yaml:"value,omitempty"`
	Effect   string `json:"effect" yaml:"effect"`
}

// AgentTolerations is the wire shape both the GET and the PUT answer with.
// UpdatedAt is nil until the first write. Message is the platform's sentence
// describing ApplyState, to be shown to the user as it came.
type AgentTolerations struct {
	Tolerations []AgentToleration `json:"tolerations" yaml:"tolerations"`
	UpdatedAt   *string           `json:"updated_at" yaml:"updated_at"`
	ApplyState  string            `json:"apply_state" yaml:"apply_state"`
	Message     string            `json:"message" yaml:"message"`
}

// agentTolerationsUpdate is the write: the whole list, always present. The
// platform reads a missing or null list as a malformed request, never as a
// clear, so an empty one has to be sent as [].
type agentTolerationsUpdate struct {
	Tolerations []AgentToleration `json:"tolerations"`
}

// agentTolerationsURL builds the tolerations URL for one cluster. The id is
// escaped because it reaches the client straight from --cluster resolution.
func agentTolerationsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/agent/tolerations", baseURL, url.PathEscape(clusterID))
}

// GetAgentTolerations reads the tolerations the platform stores for one
// cluster's agent.
func (c *Client) GetAgentTolerations(ctx context.Context, clusterID string) (*AgentTolerations, error) {
	var tolerations AgentTolerations
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		agentTolerationsURL(c.BaseURL, clusterID), nil, &tolerations); getError != nil {
		return nil, getError
	}
	return &tolerations, nil
}

// ReplaceAgentTolerations replaces the tolerations the platform stores for
// one cluster's agent, an empty list clearing them, and returns the resulting
// state. The platform owns the validation, so a rejected list comes back as
// the server's own 422 detail.
func (c *Client) ReplaceAgentTolerations(ctx context.Context, clusterID string,
	tolerations []AgentToleration) (*AgentTolerations, error) {
	update := agentTolerationsUpdate{Tolerations: append([]AgentToleration{}, tolerations...)}
	var stored AgentTolerations
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		agentTolerationsURL(c.BaseURL, clusterID), update, &stored); putError != nil {
		return nil, putError
	}
	return &stored, nil
}
