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
	ID              string  `json:"id"`
	OrganisationID  string  `json:"organisation_id"`
	ClusterID       string  `json:"cluster_id"`
	AnkraUserID     string  `json:"ankra_user_id"`
	UserEmail       *string `json:"user_email,omitempty"`
	Scope           string  `json:"scope"`
	Namespace       *string `json:"namespace,omitempty"`
	Role            string  `json:"role"`
	ReconcileStatus string  `json:"reconcile_status"`
	ReconcileError  *string `json:"reconcile_error,omitempty"`
	ReconciledAt    *string `json:"reconciled_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
	// ExpiresAt is when a time-boxed grant ends; nil is a standing grant.
	ExpiresAt *string `json:"expires_at,omitempty"`
	// Reason is the justification recorded with the grant.
	Reason *string `json:"reason,omitempty"`
	// CreatedByEmail names who made the grant; nil for the creator seed.
	CreatedByEmail *string `json:"created_by_email,omitempty"`
}

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
	CreatorGrantRole      string  `json:"creator_grant_role"`
	MaxGrantRole          string  `json:"max_grant_role"`
	ElevatedMaxTTLSeconds *int64  `json:"elevated_max_ttl_seconds"`
	RequireReasonFromRole *string `json:"require_reason_from_role"`
	IsConfigured          bool    `json:"is_configured"`
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
	switch refusal.Detail {
	case "cluster_access_policy_violation":
		return newBackendDetailError(statusCode, fmt.Sprintf(
			"refused by the organisation access policy: the most access anyone can be given is %q", refusal.MaxGrantRole))
	case "cluster_access_grant_expiry_required":
		return newBackendDetailError(statusCode, fmt.Sprintf(
			"refused by the organisation access policy: this grant must expire within %s; pass --expires", lifetime()))
	case "cluster_access_grant_lifetime_exceeded":
		return newBackendDetailError(statusCode, fmt.Sprintf(
			"refused by the organisation access policy: this grant may last at most %s; pass a shorter --expires", lifetime()))
	case "cluster_access_grant_reason_required":
		return newBackendDetailError(statusCode,
			"refused by the organisation access policy: this grant needs a reason; pass --reason")
	case "permission_denied":
		if refusal.Permission != "" {
			return newBackendDetailError(statusCode, fmt.Sprintf("permission denied: this needs %s", refusal.Permission))
		}
	}
	return newBackendDetailError(statusCode, refusal.Detail)
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
		return nil, newUnexpectedResponseError("list access grants failed", resp.StatusCode, redactedBodyForError(body, 500))
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
		return nil, accessResponseError("revoke access grant failed", resp.StatusCode, body)
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
