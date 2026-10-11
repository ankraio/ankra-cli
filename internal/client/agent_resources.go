package client

// The per-cluster agent resources surface (ankra-jr4e7h): GET/PUT
// /api/v1/org/clusters/{cluster_id}/agent/resources, the twin of the
// agent CI settings routes, mounted beside them by the cluster-api.
//
// The settings size the cluster agent's own pod - its memory and CPU limits
// and requests. Before these routes existed the only way to give an agent
// more memory was a hand-run `helm upgrade --set resources.limits.memory=`,
// which the next platform-driven agent upgrade rendered away again, so an
// agent on a large cluster went back to being OOM-killed. The values now
// live on the platform and every install or upgrade it renders carries them.
//
// A PUT stores the values and then tries to apply them; ApplyState says
// which outcome happened and Message is the platform's own sentence for it,
// which the CLI prints verbatim rather than keeping a second vocabulary in
// step with the server.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The apply states the platform reports for the stored agent resources. A
// newer platform may add one, so callers render an unrecognised state rather
// than treating the set as closed.
const (
	// AgentResourcesApplyStateApplied means the agent was online and new
	// enough to re-render its release with the stored values.
	AgentResourcesApplyStateApplied = "applied"
	// AgentResourcesApplyStatePendingUpgrade means the agent predates the
	// values; they ride its next upgrade.
	AgentResourcesApplyStatePendingUpgrade = "pending_upgrade"
	// AgentResourcesApplyStateAgentOffline means the agent is not
	// connected; the values apply once it reconnects and is upgraded.
	AgentResourcesApplyStateAgentOffline = "agent_offline"
	// AgentResourcesApplyStateNotSet means nothing is stored: every member
	// is the agent chart's default (or the platform's live sizing).
	AgentResourcesApplyStateNotSet = "not_set"
)

// AgentResources is the wire shape both the GET and the PUT answer with.
// Each sizing member is a Kubernetes quantity ("2Gi", "500m") or nil when
// the cluster stores none, in which case the agent chart's default (or the
// platform's live sizing) applies. UpdatedAt is nil until the first write.
// Message is the platform's sentence describing ApplyState, to be shown to
// the user as it came.
type AgentResources struct {
	MemoryLimit   *string `json:"memory_limit" yaml:"memory_limit"`
	MemoryRequest *string `json:"memory_request" yaml:"memory_request"`
	CPULimit      *string `json:"cpu_limit" yaml:"cpu_limit"`
	CPURequest    *string `json:"cpu_request" yaml:"cpu_request"`
	UpdatedAt     *string `json:"updated_at" yaml:"updated_at"`
	ApplyState    string  `json:"apply_state" yaml:"apply_state"`
	Message       string  `json:"message" yaml:"message"`
}

// AgentResourcesUpdate is a partial write. A nil member is left out of the
// body and the platform keeps what it stores; a pointer to a quantity sets
// that member; a pointer to "" clears it, putting the member back on the
// chart default. `omitempty` on a pointer only drops a nil one, so the
// clearing "" is still sent.
type AgentResourcesUpdate struct {
	MemoryLimit   *string `json:"memory_limit,omitempty"`
	MemoryRequest *string `json:"memory_request,omitempty"`
	CPULimit      *string `json:"cpu_limit,omitempty"`
	CPURequest    *string `json:"cpu_request,omitempty"`
}

// NamesAny reports whether the write names at least one member. The platform
// refuses a body naming none with a 422, so the CLI refuses it first.
func (update AgentResourcesUpdate) NamesAny() bool {
	return update.MemoryLimit != nil || update.MemoryRequest != nil ||
		update.CPULimit != nil || update.CPURequest != nil
}

// agentResourcesURL builds the resources URL for one cluster. The id is
// escaped because it reaches the client straight from --cluster resolution.
func agentResourcesURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/agent/resources", baseURL, url.PathEscape(clusterID))
}

// GetAgentResources reads one cluster's stored agent resource sizing.
func (c *Client) GetAgentResources(ctx context.Context, clusterID string) (*AgentResources, error) {
	var resources AgentResources
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		agentResourcesURL(c.BaseURL, clusterID), nil, &resources); getError != nil {
		return nil, getError
	}
	return &resources, nil
}

// UpdateAgentResources stores a partial change to one cluster's agent
// resource sizing and returns the resulting state, including how the
// platform applied it.
//
// The platform owns the validation - what a quantity looks like, a request
// above its limit - so a rejected value comes back as the server's own 422
// detail rather than a message this client invents.
func (c *Client) UpdateAgentResources(ctx context.Context, clusterID string,
	update AgentResourcesUpdate) (*AgentResources, error) {
	var resources AgentResources
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		agentResourcesURL(c.BaseURL, clusterID), update, &resources); putError != nil {
		return nil, putError
	}
	return &resources, nil
}
