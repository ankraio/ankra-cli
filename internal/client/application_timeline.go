package client

// Two application reads (ankra-hq41xb.22):
//
//	GET /api/v1/org/applications/{application_id}/timeline
//	GET /api/v1/org/applications/{application_id}/launch-plan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// GetApplicationTimeline reads one page of the application's unified
// timeline; query carries limit and before.
func (client *Client) GetApplicationTimeline(requestContext context.Context, applicationID string, query url.Values) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/timeline"), query, nil)
}

// GetApplicationLaunchPlan reads the application's Launch Plan; query carries
// commit_sha, cluster_id, backup_vault_id and data_migration_choices.
func (client *Client) GetApplicationLaunchPlan(requestContext context.Context, applicationID string, query url.Values) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/launch-plan"), query, nil)
}
