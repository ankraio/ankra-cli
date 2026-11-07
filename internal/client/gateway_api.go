package client

// The Gateway API option on a cluster Ankra built (ankra-j6fpgh.4.4): GET/PUT
// /api/v1/clusters/{provider}/{cluster_id}/gateway-api, the bearer twin of
// the routes the portal's cluster network page uses.
//
// The GET is the one server-side preview the portal, this CLI and MCP all
// read: whether the option is recorded, the platform's reading of the
// cluster's Gateway API CRDs, what the Traefik and cert-manager values still
// need, and one phase saying where the enable stands. The PUT is idempotent:
// it records the option, and writes the values only once the platform reads
// the CRDs as established, so a caller repeats it until the phase is enabled.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The phases the platform reports for the option, in the order an enable
// moves through them. Every phase but GatewayAPIPhaseEnabled names what is
// still to happen; the status's Note says who makes it happen.
const (
	GatewayAPIPhaseOff                  = "off"
	GatewayAPIPhaseNoPlatformNetworking = "no_platform_networking"
	GatewayAPIPhaseConflict             = "conflict"
	GatewayAPIPhaseCRDsFailed           = "crds_failed"
	GatewayAPIPhaseCRDsPending          = "crds_pending"
	GatewayAPIPhaseValuesPending        = "values_pending"
	GatewayAPIPhaseApplyFailed          = "apply_failed"
	GatewayAPIPhaseApplying             = "applying"
	GatewayAPIPhaseEnabled              = "enabled"
)

// What a member's stored values say about the option (GatewayAPIMember.Values).
const (
	GatewayAPIValuesAdd       = "add"
	GatewayAPIValuesAlreadyOn = "already_on"
	GatewayAPIValuesConflict  = "conflict"
)

// GatewayAPICRDs is the platform's reading of the cluster's Gateway API CRDs.
// Source is where they come from once the option is recorded ("addon" or
// "preinstalled"; empty while the option is off). State is what the
// CustomResourceDefinition watch sees: present, absent, incomplete or unknown.
// AddonState is nil when the cluster carries no gateway-api-crds addon.
// Established says the Traefik and cert-manager values may be written.
type GatewayAPICRDs struct {
	Source       string   `json:"source" yaml:"source"`
	State        string   `json:"state" yaml:"state"`
	Observed     []string `json:"observed" yaml:"observed"`
	Missing      []string `json:"missing" yaml:"missing"`
	AddonState   *string  `json:"addon_state" yaml:"addon_state"`
	AddonApplied string   `json:"addon_applied" yaml:"addon_applied"`
	Established  bool     `json:"established" yaml:"established"`
	Detail       string   `json:"detail" yaml:"detail"`
}

// GatewayAPIFinding is one values key the platform refuses to write over:
// the member's own values say the opposite of what the option needs.
type GatewayAPIFinding struct {
	Addon   string `json:"addon" yaml:"addon"`
	Key     string `json:"key" yaml:"key"`
	Outcome string `json:"outcome" yaml:"outcome"`
	Detail  string `json:"detail" yaml:"detail"`
}

// GatewayAPIMember is one of the two networking members the option is
// written into. Managed is false when the cluster's networking stack carries
// no such member on the platform's chart; nothing is written then. Applied is
// the member's applied-state reading (applied, pending, failed, unknown).
type GatewayAPIMember struct {
	Addon         string              `json:"addon" yaml:"addon"`
	Managed       bool                `json:"managed" yaml:"managed"`
	Values        string              `json:"values" yaml:"values"`
	Conflicts     []GatewayAPIFinding `json:"conflicts" yaml:"conflicts"`
	Applied       string              `json:"applied" yaml:"applied"`
	LastAppliedAt *string             `json:"last_applied_at" yaml:"last_applied_at"`
}

// GatewayAPIStatus is the wire shape the GET answers with, and the standing
// the enable answers with after its writes. Available is false when the
// cluster has no Ankra-managed networking to serve the Gateway API;
// UnavailableReason says why and every other field is at its zero value.
// ValuesHold is why the values write is held back right now (offline, not
// built by Ankra, a paused GitOps sync), or empty.
type GatewayAPIStatus struct {
	Available         bool             `json:"available" yaml:"available"`
	UnavailableReason string           `json:"unavailable_reason" yaml:"unavailable_reason"`
	Enabled           bool             `json:"enabled" yaml:"enabled"`
	Phase             string           `json:"phase" yaml:"phase"`
	CRDs              GatewayAPICRDs   `json:"crds" yaml:"crds"`
	Traefik           GatewayAPIMember `json:"traefik" yaml:"traefik"`
	CertManager       GatewayAPIMember `json:"cert_manager" yaml:"cert_manager"`
	ValuesHold        string           `json:"values_hold" yaml:"values_hold"`
	ResourceIDs       []string         `json:"resource_ids" yaml:"resource_ids"`
	Applies           string           `json:"applies" yaml:"applies"`
	Note              string           `json:"note" yaml:"note"`
}

// GatewayAPIEnableResult is the PUT's answer: the status after its writes
// and what this call changed. Recorded is true when this call turned the
// option on in the cluster's definition; ValuesWritten when it wrote the
// Traefik and cert-manager values, with the GitOps commit and the operation
// that carries them. GitPushFailure is set when the values reached the
// platform but the GitOps push failed or was deferred.
type GatewayAPIEnableResult struct {
	GatewayAPIStatus `yaml:",inline"`
	Changed          bool    `json:"changed" yaml:"changed"`
	Recorded         bool    `json:"recorded" yaml:"recorded"`
	ValuesWritten    bool    `json:"values_written" yaml:"values_written"`
	CommitSHA        *string `json:"commit_sha" yaml:"commit_sha"`
	OperationID      *string `json:"operation_id" yaml:"operation_id"`
	GitPushFailure   *string `json:"git_push_failure" yaml:"git_push_failure"`
}

// clusterGatewayAPIURL builds the option's URL for one cluster. The route is
// mounted per provider, so the segment is the cluster's kind as the platform
// lists it; both segments are escaped because they reach the client straight
// from --cluster resolution.
func clusterGatewayAPIURL(baseURL string, provider string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/clusters/%s/%s/gateway-api", baseURL, url.PathEscape(provider), url.PathEscape(clusterID))
}

// GetClusterGatewayAPI reads one cluster's Gateway API standing: the record,
// the CRD reading, the values plan per member and the phase.
func (c *Client) GetClusterGatewayAPI(ctx context.Context, provider string, clusterID string) (*GatewayAPIStatus, error) {
	var status GatewayAPIStatus
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		clusterGatewayAPIURL(c.BaseURL, provider, clusterID), nil, &status); getError != nil {
		return nil, getError
	}
	return &status, nil
}

// EnableClusterGatewayAPI turns the option on for one cluster and returns
// what the call changed with the standing after it. The route takes no body.
// The platform gates the write on clusters.write and refuses with 409 when
// the cluster cannot take the option; both come back as the client's usual
// error types, the 409 carrying the platform's own sentence as its Detail.
func (c *Client) EnableClusterGatewayAPI(ctx context.Context, provider string, clusterID string) (*GatewayAPIEnableResult, error) {
	var result GatewayAPIEnableResult
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		clusterGatewayAPIURL(c.BaseURL, provider, clusterID), nil, &result); putError != nil {
		return nil, putError
	}
	return &result, nil
}
