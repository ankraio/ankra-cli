package client

// The pipeline routes around a run rather than of one: the approvals inbox a
// run raises a request into when it asks for protected settings nobody
// approved, the organisation's pipeline policy (the ceiling every pipeline's
// authority is held to), the keyed cache volumes a repository keeps between
// runs, and a repository's workspace profiles as the platform stores them.
// Every method answers the platform's own document. The reads and writes
// here ride sendJSON rather than the run routes' doPipelineRequest, so a 404
// or a 403 keeps its status and the command exits 3 or 7 on it.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"strconv"
)

const (
	pipelineAuthorityRequestsPath = "/api/v1/org/pipelines/authority-requests"
	pipelinePolicyPath            = "/api/v1/org/pipeline-policy"
)

// PipelinePageCap is the most entries one page of pipeline caches or
// authority requests holds.
const PipelinePageCap = 100

// ListPipelineAuthorityRequestsOptions narrows the approvals inbox. Zero
// values are omitted.
type ListPipelineAuthorityRequestsOptions struct {
	Status        string
	RepositoryID  string
	ApplicationID string
	Cursor        string
	Limit         int
}

func pipelinePageQuery(cursor string, limit int) neturl.Values {
	query := neturl.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return query
}

func pathWithQuery(path string, query neturl.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

// ListPipelineAuthorityRequests reads one page of the approvals inbox, newest
// first. GET /api/v1/org/pipelines/authority-requests
func (c *Client) ListPipelineAuthorityRequests(ctx context.Context, options ListPipelineAuthorityRequestsOptions) (json.RawMessage, error) {
	query := pipelinePageQuery(options.Cursor, options.Limit)
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.RepositoryID != "" {
		query.Set("repository_id", options.RepositoryID)
	}
	if options.ApplicationID != "" {
		query.Set("application_id", options.ApplicationID)
	}
	return c.requestDocument(ctx, http.MethodGet, pathWithQuery(pipelineAuthorityRequestsPath, query), nil)
}

// GetPipelineAuthorityRequest reads one request: the run that raised it and
// the protected settings it asks for against the approved authority.
// GET /api/v1/org/pipelines/authority-requests/{id}
func (c *Client) GetPipelineAuthorityRequest(ctx context.Context, requestID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, pipelineAuthorityRequestsPath+"/"+neturl.PathEscape(requestID), nil)
}

// DismissPipelineAuthorityRequest declines a pending request with a reason
// the run's check run and run page then show.
// POST /api/v1/org/pipelines/authority-requests/{id}/dismiss
func (c *Client) DismissPipelineAuthorityRequest(ctx context.Context, requestID string, reason string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost,
		pipelineAuthorityRequestsPath+"/"+neturl.PathEscape(requestID)+"/dismiss", map[string]any{"reason": reason})
}

// GetPipelinePolicy reads the organisation's pipeline authority ceiling.
// GET /api/v1/org/pipeline-policy
func (c *Client) GetPipelinePolicy(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, pipelinePolicyPath, nil)
}

// UpdatePipelinePolicy changes the pipeline policy settings changes names and
// leaves the rest as they are. PUT /api/v1/org/pipeline-policy
func (c *Client) UpdatePipelinePolicy(ctx context.Context, changes map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, pipelinePolicyPath, changes)
}

// ListPipelineCaches reads one page of the keyed cache volumes a pipeline
// repository keeps between runs, most recently used first.
// GET …/pipeline-caches under the selector's application or repository.
func (c *Client) ListPipelineCaches(ctx context.Context, selector PipelineSelector, cursor string, limit int) (json.RawMessage, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	return c.requestDocument(ctx, http.MethodGet, pathWithQuery(base+"/pipeline-caches", pipelinePageQuery(cursor, limit)), nil)
}

func workspaceProfilesEndpoint(baseURL string, repositoryID string) string {
	return baseURL + "/api/v1/org/pipeline-repositories/" + neturl.PathEscape(repositoryID) + "/workspace-profiles"
}

// GetWorkspaceProfilesDocument reads a repository's workspace profiles as the
// platform stores them, services, setup and tools included, which the typed
// GetWorkspaceProfiles does not carry. A refusal is the workspace routes' own
// error. GET /api/v1/org/pipeline-repositories/{repository_id}/workspace-profiles
func (c *Client) GetWorkspaceProfilesDocument(ctx context.Context, repositoryID string) (json.RawMessage, error) {
	var document json.RawMessage
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodGet,
		workspaceProfilesEndpoint(c.BaseURL, repositoryID), nil, &document); requestError != nil {
		return nil, requestError
	}
	return document, nil
}

// PutWorkspaceProfiles replaces a repository's workspace profiles, keyed by
// kind; a nil or empty profiles clears them.
// PUT /api/v1/org/pipeline-repositories/{repository_id}/workspace-profiles
func (c *Client) PutWorkspaceProfiles(ctx context.Context, repositoryID string, profiles map[string]any) (json.RawMessage, error) {
	var document json.RawMessage
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodPut,
		workspaceProfilesEndpoint(c.BaseURL, repositoryID), map[string]any{"profiles": profiles}, &document); requestError != nil {
		return nil, requestError
	}
	return document, nil
}
