package client

// Security remediation runs, a finding's base-image rebuild and repository
// code finding dispositions (ankra-hq41xb.23), under /api/v1/org/security.
// Every answer is returned as the platform sent it.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"strconv"
)

const securityAPIPath = "/api/v1/org/security"

// SecurityRemediationsOptions narrows a remediation run listing.
type SecurityRemediationsOptions struct {
	FindingID     string
	ApplicationID string
	Limit         int
}

// ListSecurityRemediations reads the organisation's remediation runs, newest
// first, under the security project scope.
// GET /api/v1/org/security/remediations
func (c *Client) ListSecurityRemediations(ctx context.Context, options SecurityRemediationsOptions) (json.RawMessage, error) {
	query := neturl.Values{}
	if options.FindingID != "" {
		query.Set("finding_id", options.FindingID)
	}
	if options.ApplicationID != "" {
		query.Set("application_id", options.ApplicationID)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	path := pathWithQuery(securityAPIPath+"/remediations", query)
	return c.requestDocument(ctx, http.MethodGet, c.securityScopedURL(path), nil)
}

// GetSecurityRemediation reads one remediation run with its receipt trail.
// GET /api/v1/org/security/remediations/{run_id}
func (c *Client) GetSecurityRemediation(ctx context.Context, runID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, securityAPIPath+"/remediations/"+neturl.PathEscape(runID), nil)
}

// StartSecurityRemediations opens or reuses the fix pull request of every
// application repository a finding affects and starts a run for each.
// POST /api/v1/org/security/findings/{finding_id}/remediations
func (c *Client) StartSecurityRemediations(ctx context.Context, findingID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, securityAPIPath+"/findings/"+neturl.PathEscape(findingID)+"/remediations", nil)
}

// CancelSecurityRemediation stops a live run; its pull request stays.
// POST /api/v1/org/security/remediations/{run_id}/cancel
func (c *Client) CancelSecurityRemediation(ctx context.Context, runID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, securityAPIPath+"/remediations/"+neturl.PathEscape(runID)+"/cancel", nil)
}

// GetSecurityFindingRebuildPlan plans the base-image bump pull requests of a
// finding. A read: nothing is opened.
// GET /api/v1/org/security/findings/{finding_id}/rebuild-plan
func (c *Client) GetSecurityFindingRebuildPlan(ctx context.Context, findingID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, securityAPIPath+"/findings/"+neturl.PathEscape(findingID)+"/rebuild-plan", nil)
}

// RebuildSecurityFinding opens one base-image bump pull request per affected
// application repository.
// POST /api/v1/org/security/findings/{finding_id}/rebuild
func (c *Client) RebuildSecurityFinding(ctx context.Context, findingID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, securityAPIPath+"/findings/"+neturl.PathEscape(findingID)+"/rebuild", nil)
}

// SecurityCodeDispositionsOptions narrows a code disposition listing.
type SecurityCodeDispositionsOptions struct {
	ApplicationID  string
	RepositoryID   string
	IncludeRevoked bool
	Limit          int
}

// ListSecurityCodeDispositions reads the repository code finding
// dispositions, newest first, under the security project scope.
// GET /api/v1/org/security/code-dispositions
func (c *Client) ListSecurityCodeDispositions(ctx context.Context, options SecurityCodeDispositionsOptions) (json.RawMessage, error) {
	query := neturl.Values{}
	if options.ApplicationID != "" {
		query.Set("application_id", options.ApplicationID)
	}
	if options.RepositoryID != "" {
		query.Set("repository_id", options.RepositoryID)
	}
	if options.IncludeRevoked {
		query.Set("include_revoked", "true")
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	path := pathWithQuery(securityAPIPath+"/code-dispositions", query)
	return c.requestDocument(ctx, http.MethodGet, c.securityScopedURL(path), nil)
}

// SecurityCodeDispositionRequest names one Semgrep or Checkov finding of a
// pipeline repository and how it is disposed of. The repository is named by
// ApplicationID or RepositoryID.
type SecurityCodeDispositionRequest struct {
	ApplicationID string  `json:"application_id,omitempty"`
	RepositoryID  string  `json:"repository_id,omitempty"`
	Tool          string  `json:"tool"`
	RuleID        string  `json:"rule_id"`
	Path          string  `json:"path"`
	Disposition   string  `json:"disposition"`
	Reason        string  `json:"reason,omitempty"`
	ExpiresAt     *string `json:"expires_at,omitempty"`
}

// CreateSecurityCodeDisposition records a code finding as a false positive
// or an accepted risk.
// POST /api/v1/org/security/code-dispositions
func (c *Client) CreateSecurityCodeDisposition(ctx context.Context, request SecurityCodeDispositionRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, securityAPIPath+"/code-dispositions", request)
}

// PreviewSecurityCodeDisposition reports the finding a disposition would
// cover and whether it can be created, writing nothing.
// POST /api/v1/org/security/code-dispositions/preview
func (c *Client) PreviewSecurityCodeDisposition(ctx context.Context, request SecurityCodeDispositionRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, securityAPIPath+"/code-dispositions/preview", request)
}

// RevokeSecurityCodeDisposition revokes a code finding disposition; the
// image gate blocks on the finding again from its next run.
// POST /api/v1/org/security/code-dispositions/{disposition_id}/revoke
func (c *Client) RevokeSecurityCodeDisposition(ctx context.Context, dispositionID string, reason string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost,
		securityAPIPath+"/code-dispositions/"+neturl.PathEscape(dispositionID)+"/revoke", map[string]string{"reason": reason})
}
