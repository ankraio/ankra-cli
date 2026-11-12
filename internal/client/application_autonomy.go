package client

// How much of an application Ankra runs unasked, and the two application
// routes that sit beside it: the database backup default and the cost
// estimate of a proposed change. Every method answers the platform's own
// document.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
)

// GetApplicationAutonomy reads who runs each of the application's
// environments. GET /api/v1/org/applications/{application_id}/autonomy
func (c *Client) GetApplicationAutonomy(ctx context.Context, applicationID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, applicationPath(applicationID, "/autonomy"), nil)
}

// SetApplicationAutonomy replaces the application's stored "who runs it"
// layer, or one environment's when environmentID is given. The layer is
// written whole: level, areas and limits.
// PUT /api/v1/org/applications/{application_id}/autonomy
// PUT /api/v1/org/applications/{application_id}/environments/{environment_id}/autonomy
func (c *Client) SetApplicationAutonomy(ctx context.Context, applicationID string, environmentID string, layer map[string]any) (json.RawMessage, error) {
	suffix := "/autonomy"
	if environmentID != "" {
		suffix = "/environments/" + neturl.PathEscape(environmentID) + "/autonomy"
	}
	return c.requestDocument(ctx, http.MethodPut, applicationPath(applicationID, suffix), layer)
}

// GetApplicationAutonomyPause reads whether a person paused Ankra's
// autonomous actions for the application.
// GET /api/v1/org/applications/{application_id}/autonomy-pause
func (c *Client) GetApplicationAutonomyPause(ctx context.Context, applicationID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, applicationPath(applicationID, "/autonomy-pause"), nil)
}

// PauseApplicationAutonomy pauses Ankra's autonomous actions for the
// application. PUT /api/v1/org/applications/{application_id}/autonomy-pause
func (c *Client) PauseApplicationAutonomy(ctx context.Context, applicationID string, reason string) (json.RawMessage, error) {
	pause := map[string]any{}
	if reason != "" {
		pause["reason"] = reason
	}
	return c.requestDocument(ctx, http.MethodPut, applicationPath(applicationID, "/autonomy-pause"), pause)
}

// ResumeApplicationAutonomy lifts the application's pause.
// DELETE /api/v1/org/applications/{application_id}/autonomy-pause
func (c *Client) ResumeApplicationAutonomy(ctx context.Context, applicationID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodDelete, applicationPath(applicationID, "/autonomy-pause"), nil)
}

// GetApplicationAutoFixBuilds reads whether Ankra fixes the application's
// failed builds on its own.
// GET /api/v1/org/applications/{application_id}/auto-fix-builds
func (c *Client) GetApplicationAutoFixBuilds(ctx context.Context, applicationID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, applicationPath(applicationID, "/auto-fix-builds"), nil)
}

// SetApplicationAutoFixBuilds turns automatic build fixing on or off.
// PUT /api/v1/org/applications/{application_id}/auto-fix-builds
func (c *Client) SetApplicationAutoFixBuilds(ctx context.Context, applicationID string, enabled bool) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, applicationPath(applicationID, "/auto-fix-builds"),
		map[string]any{"enabled": enabled})
}

// GetApplicationSetupAutoMerge reads whether Ankra may merge the
// application's setup pull requests on its own.
// GET /api/v1/org/applications/{application_id}/auto-merge-setup-pull-requests
func (c *Client) GetApplicationSetupAutoMerge(ctx context.Context, applicationID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, applicationPath(applicationID, "/auto-merge-setup-pull-requests"), nil)
}

// SetApplicationSetupAutoMerge stores the application's own answer; a nil
// enabled clears it so the application follows its organisation again.
// PUT /api/v1/org/applications/{application_id}/auto-merge-setup-pull-requests
func (c *Client) SetApplicationSetupAutoMerge(ctx context.Context, applicationID string, enabled *bool) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, applicationPath(applicationID, "/auto-merge-setup-pull-requests"),
		map[string]any{"enabled": enabled})
}

// SetApplicationDatabaseBackup turns the application's database backup
// default on or off and answers its Backups section.
// PUT /api/v1/org/applications/{application_id}/backups/database-backup
func (c *Client) SetApplicationDatabaseBackup(ctx context.Context, applicationID string, enabled bool) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, applicationPath(applicationID, "/backups/database-backup"),
		map[string]any{"enabled": enabled})
}

// EstimateApplicationCost prices a proposed change to the application as the
// monthly change to its cost.
// POST /api/v1/org/applications/{application_id}/cost/estimate
func (c *Client) EstimateApplicationCost(ctx context.Context, applicationID string, change map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, applicationPath(applicationID, "/cost/estimate"), change)
}
