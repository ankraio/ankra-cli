package client

import (
	"context"
	"net/http"
)

// StackProfileDemoConnectionProposal answers which connection blocks a
// service package made from a stack profile could declare, read off one of the
// profile's running demos on the organisation's staging cluster
// (GET .../stack-profiles/{profile_id}/demos/{workspace_id}/connection-proposal).
//
// Observable is the three-state guard and is read before Proposals: false
// means the demo's namespace could not be observed (Reason says why), so an
// empty Proposals there is not "nothing to propose".
type StackProfileDemoConnectionProposal struct {
	DemoStatus       string                      `json:"demo_status" yaml:"demo_status"`
	Observable       bool                        `json:"observable" yaml:"observable"`
	Reason           string                      `json:"reason" yaml:"reason"`
	ObservedServices int                         `json:"observed_services" yaml:"observed_services"`
	ObservedSecrets  int                         `json:"observed_secrets" yaml:"observed_secrets"`
	Proposals        []ServiceConnectionProposal `json:"proposals" yaml:"proposals"`
	Note             string                      `json:"note" yaml:"note"`
}

// ServiceConnectionProposal is one connection block a package could declare:
// Complete when every field the protocol delivers is mapped, Unresolved
// naming the fields nothing mapped, and Reasons saying how each was read.
type ServiceConnectionProposal struct {
	Connection ServicePackageConnection `json:"connection" yaml:"connection"`
	Complete   bool                     `json:"complete" yaml:"complete"`
	Confidence string                   `json:"confidence" yaml:"confidence"`
	Unresolved []string                 `json:"unresolved" yaml:"unresolved"`
	Reasons    []string                 `json:"reasons" yaml:"reasons"`
}

// StackProfileDemoConnectionProbe is one probe of a connection against a
// profile demo: Status is queued, probed or expired, and Verdict is passed,
// failed or unknown. Only "passed" means the connection was proven.
type StackProfileDemoConnectionProbe struct {
	ProbeID     string  `json:"probe_id" yaml:"probe_id"`
	Status      string  `json:"status" yaml:"status"`
	Verdict     string  `json:"verdict" yaml:"verdict"`
	Cause       string  `json:"cause" yaml:"cause"`
	Reason      string  `json:"reason" yaml:"reason"`
	RequestedAt string  `json:"requested_at" yaml:"requested_at"`
	ProbedAt    *string `json:"probed_at" yaml:"probed_at"`
}

// GetStackProfileDemoConnectionProposal reads the connection proposal for one
// of a profile's demos.
func (c *Client) GetStackProfileDemoConnectionProposal(ctx context.Context, profileID string, workspaceID string) (*StackProfileDemoConnectionProposal, error) {
	var proposal StackProfileDemoConnectionProposal
	address := c.servicesURL("/api/v1/org/stack-profiles/%s/demos/%s/connection-proposal", nil, profileID, workspaceID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &proposal); requestError != nil {
		return nil, requestError
	}
	return &proposal, nil
}

// ProbeStackProfileDemoConnection queues a probe of a connection against one
// of a profile's demos. The answer is queued; read it again with
// GetStackProfileDemoConnectionProbe until it is probed or expired.
func (c *Client) ProbeStackProfileDemoConnection(ctx context.Context, profileID string, workspaceID string, connection ServicePackageConnection) (*StackProfileDemoConnectionProbe, error) {
	var probe StackProfileDemoConnectionProbe
	address := c.servicesURL("/api/v1/org/stack-profiles/%s/demos/%s/connection-probes", nil, profileID, workspaceID)
	body := map[string]ServicePackageConnection{"connection": connection}
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, body, &probe); requestError != nil {
		return nil, requestError
	}
	return &probe, nil
}

// GetStackProfileDemoConnectionProbe reads one probe of a profile demo.
func (c *Client) GetStackProfileDemoConnectionProbe(ctx context.Context, profileID string, workspaceID string, probeID string) (*StackProfileDemoConnectionProbe, error) {
	var probe StackProfileDemoConnectionProbe
	address := c.servicesURL("/api/v1/org/stack-profiles/%s/demos/%s/connection-probes/%s", nil, profileID, workspaceID, probeID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &probe); requestError != nil {
		return nil, requestError
	}
	return &probe, nil
}
