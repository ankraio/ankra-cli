package client

// The per-cluster agent CI settings surface (ankra-vn0bd, item 5 of the
// agent CI settings contract): GET/PUT
// /api/v1/org/clusters/{cluster_id}/agent/ci-settings, the bearer twin of
// the routes the cluster-api mounts alongside the agent upgrade route.
//
// The settings size the agent's own pipeline-step scheduler - how many
// pipeline steps it runs at once (chart value `ci_worker_count`, env
// AGENT_CI_WORKER_COUNT) and the storage class its step workspaces are
// carved from (`ci_storage_class`). Before these routes existed the only way
// to set either was a hand-run `helm upgrade --set`, which the next
// platform-driven agent upgrade rendered away again.
//
// A PUT stores the values and then tries to apply them, and ApplyState says
// which of the three outcomes happened: the agent is re-rendering its
// release now (applied), it is too old to accept chart values and the next
// upgrade will carry them (pending_upgrade), or it is not connected
// (agent_offline). The stored values are kept in all three cases, so a
// refusal to apply is never a refusal to store.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The apply states the platform reports for a stored CI setting. A newer
// platform may add one, so callers render an unrecognised state rather than
// treating the set as closed.
const (
	// AgentCIApplyStateApplied means the agent was online and new enough:
	// the platform published an upgrade at the agent's current version so
	// it re-renders its own release with the stored values.
	AgentCIApplyStateApplied = "applied"
	// AgentCIApplyStatePendingUpgrade means the agent predates chart
	// values; the stored values ride the next agent upgrade instead.
	AgentCIApplyStatePendingUpgrade = "pending_upgrade"
	// AgentCIApplyStateAgentOffline means the agent is not connected; the
	// stored values apply once it reconnects and is upgraded.
	AgentCIApplyStateAgentOffline = "agent_offline"
)

// AgentCISettings is the wire shape both the GET and the PUT answer with:
// the stored settings plus what the platform currently knows about the
// agent that has to honour them.
//
// SupportsPipelineSteps is what the agent advertised on its last check-in,
// not what the settings ask for: an agent that has not yet re-rendered its
// release still reports false with a non-zero CIWorkerCount stored.
// AgentVersion is empty for a cluster whose agent has never checked in, and
// UpdatedAt is nil until the settings are written for the first time.
type AgentCISettings struct {
	CIWorkerCount         int     `json:"ci_worker_count" yaml:"ci_worker_count"`
	CIStorageClass        string  `json:"ci_storage_class" yaml:"ci_storage_class"`
	AgentVersion          string  `json:"agent_version" yaml:"agent_version"`
	SupportsPipelineSteps bool    `json:"supports_pipeline_steps" yaml:"supports_pipeline_steps"`
	ApplyState            string  `json:"apply_state" yaml:"apply_state"`
	UpdatedAt             *string `json:"updated_at" yaml:"updated_at"`
}

// AgentCISettingsUpdate is a partial write: a nil member is left out of the
// body entirely and the platform keeps the value it has, so setting the
// worker count never silently resets the storage class.
//
// Both members are pointers rather than plain values precisely because zero
// is meaningful on this surface - 0 workers disables the pipeline-step
// scheduler and an empty storage class means "the cluster default" - and
// `omitempty` on a pointer only drops a nil one, so a pointer to 0 or to ""
// is still sent.
type AgentCISettingsUpdate struct {
	CIWorkerCount  *int    `json:"ci_worker_count,omitempty"`
	CIStorageClass *string `json:"ci_storage_class,omitempty"`
}

// agentCISettingsURL builds the settings URL for one cluster. The id is
// escaped because it reaches the client straight from --cluster resolution.
func agentCISettingsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/agent/ci-settings", baseURL, url.PathEscape(clusterID))
}

// GetAgentCISettings reads one cluster's stored agent CI settings.
func (c *Client) GetAgentCISettings(ctx context.Context, clusterID string) (*AgentCISettings, error) {
	var settings AgentCISettings
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		agentCISettingsURL(c.BaseURL, clusterID), nil, &settings); getError != nil {
		return nil, getError
	}
	return &settings, nil
}

// UpdateAgentCISettings stores a partial change to one cluster's agent CI
// settings and returns the resulting state, including how the platform
// applied it.
//
// The platform owns the vocabulary this write is validated against - the
// worker range and the storage class's DNS-1123 shape - so a rejected value
// comes back as the server's own 422 detail rather than a message this
// client invents and then has to keep in step.
func (c *Client) UpdateAgentCISettings(ctx context.Context, clusterID string,
	update AgentCISettingsUpdate) (*AgentCISettings, error) {
	var settings AgentCISettings
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		agentCISettingsURL(c.BaseURL, clusterID), update, &settings); putError != nil {
		return nil, putError
	}
	return &settings, nil
}
