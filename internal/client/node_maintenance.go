package client

import (
	"context"
	"net/http"
	neturl "net/url"
)

// NodeMaintenanceResult is the platform's answer for a dispatched node or
// bastion job (the etcd member inspection and removal, the node OS policy):
// the operation it started or attached to and, when the job finished inside
// the platform's wait budget, Completed with the report. Otherwise the
// operation id is the poll handle.
type NodeMaintenanceResult struct {
	OperationID  string         `json:"operation_id" yaml:"operation_id"`
	StepID       string         `json:"step_id,omitempty" yaml:"step_id,omitempty"`
	NodeID       string         `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	JobName      string         `json:"job_name,omitempty" yaml:"job_name,omitempty"`
	Status       string         `json:"status" yaml:"status"`
	Completed    bool           `json:"completed" yaml:"completed"`
	Attached     bool           `json:"attached_to_existing_run,omitempty" yaml:"attached_to_existing_run,omitempty"`
	Report       map[string]any `json:"report,omitempty" yaml:"report,omitempty"`
	ErrorExcerpt string         `json:"error_excerpt,omitempty" yaml:"error_excerpt,omitempty"`
}

// OSPatchPolicyUpdate is a partial write of a cluster's OS patch policy: a
// nil member is not sent and keeps its stored value.
type OSPatchPolicyUpdate struct {
	Enabled              *bool    `json:"enabled,omitempty"`
	WindowDays           []string `json:"window_days,omitempty"`
	WindowStartHourUTC   *int     `json:"window_start_hour_utc,omitempty"`
	WindowDurationHours  *int     `json:"window_duration_hours,omitempty"`
	CadenceDays          *int     `json:"cadence_days,omitempty"`
	IncludeStatefulNodes *bool    `json:"include_stateful_nodes,omitempty"`
	SecurityLagDays      *int     `json:"security_lag_days,omitempty"`
}

// OSPatchRunRequest starts a rolling OS patch run. A nil member keeps the
// policy's choice.
type OSPatchRunRequest struct {
	OutsideWindow        bool  `json:"outside_window"`
	IncludeStatefulNodes *bool `json:"include_stateful_nodes,omitempty"`
	IncludeBastion       *bool `json:"include_bastion,omitempty"`
}

// NodeOSPolicyRequest writes, or with Revert removes, the node OS update hold.
// NodeIDs and AllNodes are exclusive; at least one target is required.
type NodeOSPolicyRequest struct {
	NodeIDs        []string `json:"node_ids,omitempty"`
	AllNodes       bool     `json:"all_nodes"`
	IncludeBastion bool     `json:"include_bastion"`
	Revert         bool     `json:"revert"`
}

// GetEtcdMembers runs the read-only etcd member listing and endpoint health
// check on one control-plane node. provider is the URL segment the cluster's
// routes live under (hetzner, ovh, upcloud, ...).
func (c *Client) GetEtcdMembers(ctx context.Context, provider string, clusterID string, nodeID string) (*NodeMaintenanceResult, error) {
	var result NodeMaintenanceResult
	endpoint := c.providerClusterURL(provider, clusterID, "nodes/"+neturl.PathEscape(nodeID)+"/etcd-members")
	if postError := c.sendJSONSlow(ctx, http.MethodPost, endpoint, map[string]any{}, &result); postError != nil {
		return nil, postError
	}
	return &result, nil
}

// RemoveEtcdMember removes one etcd member, by the hex or decimal id the
// listing reported, executing on the healthy control-plane node nodeID. The
// platform refuses a healthy member, an unreadable one, the local member and
// a removal that would break quorum.
func (c *Client) RemoveEtcdMember(ctx context.Context, provider string, clusterID string, nodeID string, memberID string) (*NodeMaintenanceResult, error) {
	var result NodeMaintenanceResult
	endpoint := c.providerClusterURL(provider, clusterID, "nodes/"+neturl.PathEscape(nodeID)+"/etcd-members/remove")
	if postError := c.sendJSONSlow(ctx, http.MethodPost, endpoint, map[string]any{"member_id": memberID}, &result); postError != nil {
		return nil, postError
	}
	return &result, nil
}

// GetOSPatchStatus reads the cluster's OS patch policy, its newest run, the
// last dry run and the security-lag verdict.
func (c *Client) GetOSPatchStatus(provider string, clusterID string) (map[string]any, error) {
	var status map[string]any
	if getError := c.sendJSON(http.MethodGet, c.providerClusterURL(provider, clusterID, "os-patches"), nil, &status); getError != nil {
		return nil, getError
	}
	return status, nil
}

// PlanOSPatches runs the OS patch dry run on every host and waits for it
// within the platform's budget; a slower plan answers completed=false and its
// operation id.
func (c *Client) PlanOSPatches(ctx context.Context, provider string, clusterID string) (map[string]any, error) {
	var plan map[string]any
	if postError := c.sendJSONSlow(ctx, http.MethodPost, c.providerClusterURL(provider, clusterID, "os-patches/plan"),
		map[string]any{}, &plan); postError != nil {
		return nil, postError
	}
	return plan, nil
}

// RunOSPatches starts the rolling OS patch run, one host at a time.
func (c *Client) RunOSPatches(ctx context.Context, provider string, clusterID string, request OSPatchRunRequest) (map[string]any, error) {
	var run map[string]any
	if postError := c.sendJSONSlow(ctx, http.MethodPost, c.providerClusterURL(provider, clusterID, "os-patches/run"),
		request, &run); postError != nil {
		return nil, postError
	}
	return run, nil
}

// SetOSPatchPolicy merges update onto the cluster's OS patch policy.
func (c *Client) SetOSPatchPolicy(ctx context.Context, provider string, clusterID string, update OSPatchPolicyUpdate) (map[string]any, error) {
	var policy map[string]any
	if putError := c.sendJSONSlow(ctx, http.MethodPut, c.providerClusterURL(provider, clusterID, "os-patches/policy"),
		update, &policy); putError != nil {
		return nil, putError
	}
	return policy, nil
}

// ApplyNodeOSPolicy writes or removes the node OS update hold and waits for
// the per-host report within the platform's budget.
func (c *Client) ApplyNodeOSPolicy(ctx context.Context, provider string, clusterID string, request NodeOSPolicyRequest) (*NodeMaintenanceResult, error) {
	var result NodeMaintenanceResult
	if postError := c.sendJSONSlow(ctx, http.MethodPost, c.providerClusterURL(provider, clusterID, "node-os-policy"),
		request, &result); postError != nil {
		return nil, postError
	}
	return &result, nil
}
