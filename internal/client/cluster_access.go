package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ClusterAccessGrant struct {
	ID              string  `json:"id" yaml:"id"`
	OrganisationID  string  `json:"organisation_id" yaml:"organisation_id"`
	ClusterID       string  `json:"cluster_id" yaml:"cluster_id"`
	AnkraUserID     string  `json:"ankra_user_id" yaml:"ankra_user_id"`
	UserEmail       *string `json:"user_email,omitempty" yaml:"user_email,omitempty"`
	Scope           string  `json:"scope" yaml:"scope"`
	Namespace       *string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Role            string  `json:"role" yaml:"role"`
	ReconcileStatus string  `json:"reconcile_status" yaml:"reconcile_status"`
	ReconcileError  *string `json:"reconcile_error,omitempty" yaml:"reconcile_error,omitempty"`
	ReconciledAt    *string `json:"reconciled_at,omitempty" yaml:"reconciled_at,omitempty"`
	CreatedAt       string  `json:"created_at" yaml:"created_at"`
	// ExpiresAt is when a time-boxed grant ends; nil is a standing grant.
	ExpiresAt *string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	// Reason is the justification recorded with the grant.
	Reason *string `json:"reason,omitempty" yaml:"reason,omitempty"`
	// CreatedByEmail names who made the grant; nil for the creator seed.
	CreatedByEmail *string `json:"created_by_email,omitempty" yaml:"created_by_email,omitempty"`
	// OverPolicy says whether the grant would be refused if it were made
	// now (its role is above the policy ceiling, or it outlasts the
	// policy's elevated lifetime). The platform sends it on listings only;
	// nil means not evaluated or the policy could not be read, never
	// "within", so it has no omitempty: null stays null in -o json.
	OverPolicy *bool `json:"over_policy" yaml:"over_policy"`
	// Source says where the grant came from: manual, creator, role or
	// elevated. nil means the platform could not tell, never "manual".
	Source *string `json:"source" yaml:"source"`
}

// Grant sources the platform derives (ClusterAccessGrant.Source).
const (
	ClusterAccessGrantSourceManual   = "manual"
	ClusterAccessGrantSourceCreator  = "creator"
	ClusterAccessGrantSourceRole     = "role"
	ClusterAccessGrantSourceElevated = "elevated"
)

type ListClusterAccessGrantsResponse struct {
	Result []ClusterAccessGrant `json:"result"`
}

type CreateClusterAccessGrantRequest struct {
	UserEmail string  `json:"user_email"`
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace,omitempty"`
	Role      string  `json:"role"`
	// ExpiresIn is a relative window ("30m", "4h", "7d"); ExpiresAt an
	// RFC 3339 time. At most one is set; neither is a standing grant.
	ExpiresIn *string `json:"expires_in,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Reason    *string `json:"reason,omitempty"`
}

// ElevateClusterAccessRequest is break-glass for the caller itself (POST
// /api/v1/clusters/{id}/access/elevate): no member is named, because the
// grantee is whoever the token belongs to, a service account included.
type ElevateClusterAccessRequest struct {
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace,omitempty"`
	Role      string  `json:"role"`
	ExpiresIn *string `json:"expires_in,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Reason    string  `json:"reason"`
}

// ClusterAccessPolicy is the organisation's cluster access policy: the
// limits every kubectl access grant is held to.
type ClusterAccessPolicy struct {
	CreatorGrantRole      string  `json:"creator_grant_role" yaml:"creator_grant_role"`
	MaxGrantRole          string  `json:"max_grant_role" yaml:"max_grant_role"`
	ElevatedMaxTTLSeconds *int64  `json:"elevated_max_ttl_seconds" yaml:"elevated_max_ttl_seconds"`
	RequireReasonFromRole *string `json:"require_reason_from_role" yaml:"require_reason_from_role"`
	IsConfigured          bool    `json:"is_configured" yaml:"is_configured"`
}

type clusterAccessPolicyResponse struct {
	Policy ClusterAccessPolicy `json:"policy"`
}

// accessRefusal is the body of a grant refused by the organisation policy
// or by the access rules, as the platform answers it.
type accessRefusal struct {
	Detail                string  `json:"detail"`
	MaxGrantRole          string  `json:"max_grant_role"`
	MaxLifetimeSeconds    *int64  `json:"max_lifetime_seconds"`
	RequireReasonFromRole *string `json:"require_reason_from_role"`
	Permission            string  `json:"permission"`
}

// Refusal codes the platform answers when the organisation access policy
// bounds a grant. Each one names a flag the caller can change.
const (
	ClusterAccessPolicyViolation        = "cluster_access_policy_violation"
	ClusterAccessGrantExpiryRequired    = "cluster_access_grant_expiry_required"
	ClusterAccessGrantLifetimeExceeded  = "cluster_access_grant_lifetime_exceeded"
	ClusterAccessGrantReasonRequired    = "cluster_access_grant_reason_required"
	clusterAccessPermissionDeniedDetail = "permission_denied"
)

// ClusterAccessRefusalError is a cluster access request the platform
// refused. Error() is the readable sentence accessResponseError built; the
// chain carries the *UnexpectedResponseError (status code and detail) and,
// for a 403 that is not a policy refusal, a *PermissionDeniedError, so the
// CLI's exit-code classification sees an RBAC denial (exit 7) rather than a
// credential problem.
type ClusterAccessRefusalError struct {
	// Code is the platform's refusal code (one of the ClusterAccess*
	// constants, or "permission_denied"), or the platform's own sentence
	// when it answered one.
	Code     string
	response *UnexpectedResponseError
	denied   *PermissionDeniedError
}

func (e *ClusterAccessRefusalError) Error() string { return e.response.Error() }

func (e *ClusterAccessRefusalError) Unwrap() []error {
	if e.denied != nil {
		return []error{e.response, e.denied}
	}
	return []error{e.response}
}

// PolicyRefusal reports whether the organisation access policy refused the
// request: the role is above the ceiling, the grant must expire or expires
// too late, or it needs a reason. Changing the invocation (a lower --role,
// --expires, --reason) is the fix, whatever the caller's role.
func (e *ClusterAccessRefusalError) PolicyRefusal() bool {
	switch e.Code {
	case ClusterAccessPolicyViolation, ClusterAccessGrantExpiryRequired,
		ClusterAccessGrantLifetimeExceeded, ClusterAccessGrantReasonRequired:
		return true
	}
	return false
}

// accessResponseError turns a refused cluster access request into a
// sentence that says what to change. A policy refusal names the bound; any
// other platform sentence is passed on as it is; a body the platform did
// not shape keeps the generic status-and-body error.
func accessResponseError(operation string, statusCode int, body []byte) error {
	var refusal accessRefusal
	if json.Unmarshal(body, &refusal) != nil || refusal.Detail == "" {
		return newUnexpectedResponseError(operation, statusCode, redactedBodyForError(body, 500))
	}
	lifetime := func() string {
		if refusal.MaxLifetimeSeconds == nil {
			return "the policy's limit"
		}
		return (time.Duration(*refusal.MaxLifetimeSeconds) * time.Second).String()
	}
	message := refusal.Detail
	switch refusal.Detail {
	case ClusterAccessPolicyViolation:
		message = fmt.Sprintf(
			"refused by the organisation access policy: the most access anyone can be given is %q", refusal.MaxGrantRole)
	case ClusterAccessGrantExpiryRequired:
		message = fmt.Sprintf(
			"refused by the organisation access policy: this grant must expire within %s; pass --expires", lifetime())
	case ClusterAccessGrantLifetimeExceeded:
		message = fmt.Sprintf(
			"refused by the organisation access policy: this grant may last at most %s; pass a shorter --expires", lifetime())
	case ClusterAccessGrantReasonRequired:
		message = "refused by the organisation access policy: this grant needs a reason; pass --reason"
	case clusterAccessPermissionDeniedDetail:
		message = "permission denied: your role does not allow this. Ask an organisation admin to change your role"
		if refusal.Permission != "" {
			message = fmt.Sprintf("permission denied: this needs %s", refusal.Permission)
		}
	}
	refused := &ClusterAccessRefusalError{Code: refusal.Detail, response: newBackendDetailError(statusCode, message)}
	if refused.PolicyRefusal() {
		return refused
	}
	if statusCode != http.StatusForbidden {
		return refused.response
	}
	refused.denied = &PermissionDeniedError{Permission: refusal.Permission}
	if refusal.Detail != clusterAccessPermissionDeniedDetail {
		refused.denied.Detail = refusal.Detail
	}
	return refused
}

// ClusterAccessResponseError is accessResponseError for callers outside this
// package (the command tests build the platform's refusals with it).
func ClusterAccessResponseError(operation string, statusCode int, body []byte) error {
	return accessResponseError(operation, statusCode, body)
}

// ClusterAccessRoleGrantError is a revoke the platform refused (409) because
// the grant comes from the member's organisation role: the role sync would
// re-create it, so the platform keeps it and names the role as the way to
// remove it. Error() is the platform's sentence; the chain carries the
// *UnexpectedResponseError (status 409). Nothing about the caller's
// credentials, role or timing changes the answer, so retrying never helps.
type ClusterAccessRoleGrantError struct {
	response error
}

func (e *ClusterAccessRoleGrantError) Error() string { return e.response.Error() }

func (e *ClusterAccessRoleGrantError) Unwrap() error { return e.response }

// revokeResponseError is accessResponseError for the revoke route, where a
// 409 has exactly one cause: the grant comes from an organisation role.
func revokeResponseError(statusCode int, body []byte) error {
	refused := accessResponseError("revoke access grant failed", statusCode, body)
	if statusCode == http.StatusConflict {
		return &ClusterAccessRoleGrantError{response: refused}
	}
	return refused
}

// ClusterAccessRevokeResponseError is revokeResponseError for callers outside
// this package (the command tests build the platform's refusals with it).
func ClusterAccessRevokeResponseError(statusCode int, body []byte) error {
	return revokeResponseError(statusCode, body)
}

type CreateClusterAccessGrantResponse struct {
	Grant ClusterAccessGrant `json:"grant"`
}

type DeleteClusterAccessGrantResponse struct {
	Deleted bool `json:"deleted"`
}

func (c *Client) ListClusterAccessGrants(ctx context.Context, clusterID string) (*ListClusterAccessGrantsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants", c.BaseURL, clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, accessResponseError("list access grants failed", resp.StatusCode, body)
	}

	var grants ListClusterAccessGrantsResponse
	if err := parseJSON(body, &grants); err != nil {
		return nil, err
	}
	return &grants, nil
}

func (c *Client) CreateClusterAccessGrant(ctx context.Context, clusterID string, request CreateClusterAccessGrantRequest) (*CreateClusterAccessGrantResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants", c.BaseURL, clusterID)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, accessResponseError("create access grant failed", resp.StatusCode, body)
	}

	var created CreateClusterAccessGrantResponse
	if err := parseJSON(body, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *Client) DeleteClusterAccessGrant(ctx context.Context, clusterID string, grantID string) (*DeleteClusterAccessGrantResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants/%s", c.BaseURL, clusterID, grantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, revokeResponseError(resp.StatusCode, body)
	}

	var deleted DeleteClusterAccessGrantResponse
	if err := parseJSON(body, &deleted); err != nil {
		return nil, err
	}
	return &deleted, nil
}

// ElevateClusterAccess grants the caller itself time-boxed access to a
// cluster (break-glass, kube_access.elevate).
func (c *Client) ElevateClusterAccess(ctx context.Context, clusterID string, request ElevateClusterAccessRequest) (*CreateClusterAccessGrantResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/elevate", c.BaseURL, clusterID)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, accessResponseError("elevate cluster access failed", resp.StatusCode, body)
	}
	var created CreateClusterAccessGrantResponse
	if err := parseJSON(body, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// GetClusterAccessPolicy reads the organisation's cluster access policy.
func (c *Client) GetClusterAccessPolicy(ctx context.Context) (*ClusterAccessPolicy, error) {
	url := fmt.Sprintf("%s/api/v1/org/cluster-access-policy", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, accessResponseError("read cluster access policy failed", resp.StatusCode, body)
	}
	var policy clusterAccessPolicyResponse
	if err := parseJSON(body, &policy); err != nil {
		return nil, err
	}
	return &policy.Policy, nil
}
