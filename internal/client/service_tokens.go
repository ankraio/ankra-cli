package client

// Organisation service tokens (GET/POST /api/v1/org/organisation/service-
// tokens): bearer credentials minted against an organisation-owned service
// account rather than a person. Listing and minting need members.manage;
// the platform answers a caller without it with a plain-detail 403, which
// is relayed as a *PermissionDeniedError so it exits 7 (role), not 6
// (credentials).

import (
	"errors"
	"net/http"
)

// ServiceToken is one row of the organisation's service-token listing. The
// token value is never part of it.
type ServiceToken struct {
	ID          string `json:"id" yaml:"id"`
	AccountID   string `json:"account_id" yaml:"account_id"`
	AccountName string `json:"account_name" yaml:"account_name"`
	Label       string `json:"label" yaml:"label"`
	// RoleSlug is empty when the account no longer holds a live role
	// assignment (it was stood down): it still authenticates, at the
	// viewer floor.
	RoleSlug  string `json:"role_slug" yaml:"role_slug"`
	ExpiresAt string `json:"expires_at" yaml:"expires_at"`
	// DaysLeft is the platform's whole days before expiry: 0 inside the
	// last day, negative once expired. Nil from a platform that omits it.
	DaysLeft    *int     `json:"days_left" yaml:"days_left"`
	CreatedAt   string   `json:"created_at" yaml:"created_at"`
	RevokedAt   *string  `json:"revoked_at" yaml:"revoked_at"`
	LastUsedAt  *string  `json:"last_used_at" yaml:"last_used_at"`
	Revoked     bool     `json:"revoked" yaml:"revoked"`
	Scopes      []string `json:"scopes" yaml:"scopes"`
	CreatedBy   *string  `json:"created_by" yaml:"created_by"`
	ProjectID   *string  `json:"project_id" yaml:"project_id"`
	ProjectName *string  `json:"project_name" yaml:"project_name"`
}

// CreateServiceTokenRequest mints a token for the service account Name,
// creating the account (with RoleSlug) when the organisation has none of
// that name.
type CreateServiceTokenRequest struct {
	Name      string   `json:"name"`
	Label     string   `json:"label,omitempty"`
	RoleSlug  string   `json:"role_slug"`
	ExpiresAt *string  `json:"expires_at,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	// ProjectID pins the token to one project of the organisation (ADR
	// 0022); omitted, the platform pins it to the caller's current project.
	ProjectID *string `json:"project_id,omitempty"`
}

// CreateServiceTokenResponse carries the new token value, shown once.
type CreateServiceTokenResponse struct {
	ID          string `json:"id" yaml:"id"`
	Token       string `json:"token" yaml:"token"`
	AccountID   string `json:"account_id" yaml:"account_id"`
	AccountName string `json:"account_name" yaml:"account_name"`
	Label       string `json:"label" yaml:"label"`
	RoleSlug    string `json:"role_slug" yaml:"role_slug"`
	ExpiresAt   string `json:"expires_at" yaml:"expires_at"`
	Type        string `json:"type" yaml:"type"`
}

// ListServiceTokens returns the organisation's service tokens, live and
// revoked.
func (c *Client) ListServiceTokens() ([]ServiceToken, error) {
	var response struct {
		Tokens []ServiceToken `json:"tokens"`
	}
	target := c.BaseURL + "/api/v1/org/organisation/service-tokens"
	if err := c.sendJSON(http.MethodGet, target, nil, &response); err != nil {
		return nil, serviceTokenError(err)
	}
	return response.Tokens, nil
}

// CreateServiceToken mints an organisation service token.
func (c *Client) CreateServiceToken(request CreateServiceTokenRequest) (*CreateServiceTokenResponse, error) {
	var created CreateServiceTokenResponse
	target := c.BaseURL + "/api/v1/org/organisation/service-tokens"
	if err := c.sendJSON(http.MethodPost, target, request, &created); err != nil {
		return nil, serviceTokenError(err)
	}
	return &created, nil
}

// serviceTokenError turns the service-token routes' plain-detail 403 ("You
// do not have permission to view service tokens") into a role refusal.
func serviceTokenError(err error) error {
	var unexpected *UnexpectedResponseError
	if errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusForbidden && unexpected.Detail != "" {
		return &PermissionDeniedError{Detail: unexpected.Detail}
	}
	return err
}
