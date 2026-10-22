package client

// The data migration of an application's Launch Plan: the runs that move its
// data off the vendors it used (a database, an object store) into services
// on its cluster, behind one plan-level approval. A run copies, freezes the
// vendor sources read-only, flips the application's environment in one change
// and keeps the sources read-only through a rollback window, in which a
// rollback puts the previous environment back and makes the sources writable
// again.
//
// Like the rest of this package, it speaks only the bearer twin under
// /api/v1/org.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// DataMigrationStep is one step of a run and what happened to it.
type DataMigrationStep struct {
	Key           string  `json:"key" yaml:"key"`
	DependencyKey string  `json:"dependency_key,omitempty" yaml:"dependency_key,omitempty"`
	Stage         string  `json:"stage" yaml:"stage"`
	Phase         string  `json:"phase" yaml:"phase"`
	Summary       string  `json:"summary" yaml:"summary"`
	Status        string  `json:"status" yaml:"status"`
	Detail        string  `json:"detail,omitempty" yaml:"detail,omitempty"`
	StartedAt     *string `json:"started_at" yaml:"started_at"`
	FinishedAt    *string `json:"finished_at" yaml:"finished_at"`
}

// DataMigrationRunState is the state machine's view of a run: every step,
// the rollback steps, when the cutover happened, and why a run stopped.
type DataMigrationRunState struct {
	Status     string              `json:"status" yaml:"status"`
	Steps      []DataMigrationStep `json:"steps" yaml:"steps"`
	Rollback   []DataMigrationStep `json:"rollback" yaml:"rollback"`
	CutoverAt  *string             `json:"cutover_at" yaml:"cutover_at"`
	FailedStep string              `json:"failed_step,omitempty" yaml:"failed_step,omitempty"`
	Reason     string              `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// DataMigrationRun is one data-migration run of an application. It names the
// variables the cutover rewires, never their values.
type DataMigrationRun struct {
	ID                  string                `json:"id" yaml:"id"`
	ApplicationID       string                `json:"application_id" yaml:"application_id"`
	ApprovalID          string                `json:"approval_id" yaml:"approval_id"`
	ClusterID           string                `json:"cluster_id" yaml:"cluster_id"`
	CommitSHA           string                `json:"commit_sha" yaml:"commit_sha"`
	Status              string                `json:"status" yaml:"status"`
	Run                 DataMigrationRunState `json:"run" yaml:"run"`
	EnvironmentNames    []string              `json:"environment_names" yaml:"environment_names"`
	RollbackRequestedAt *string               `json:"rollback_requested_at" yaml:"rollback_requested_at"`
	RollbackClosesAt    *string               `json:"rollback_closes_at" yaml:"rollback_closes_at"`
	StartedBy           string                `json:"started_by" yaml:"started_by"`
	CreatedAt           string                `json:"created_at" yaml:"created_at"`
	UpdatedAt           string                `json:"updated_at" yaml:"updated_at"`
	FinishedAt          *string               `json:"finished_at" yaml:"finished_at"`
}

// DataMigrationRunList is an application's newest runs, newest first.
type DataMigrationRunList struct {
	Runs []DataMigrationRun `json:"runs" yaml:"runs"`
}

func applicationDataMigrationRunsURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/launch-plan/data-migration/runs", baseURL,
		neturl.PathEscape(applicationID))
}

// ListApplicationDataMigrationRuns reads the application's newest
// data-migration runs.
// GET /api/v1/org/applications/{application_id}/launch-plan/data-migration/runs
func (c *Client) ListApplicationDataMigrationRuns(requestContext context.Context,
	applicationID string) (*DataMigrationRunList, error) {
	var list DataMigrationRunList
	if requestError := c.sendJSONContext(requestContext, http.MethodGet,
		applicationDataMigrationRunsURL(c.BaseURL, applicationID), nil, &list); requestError != nil {
		return nil, requestError
	}
	return &list, nil
}

// GetApplicationDataMigrationRun reads one data-migration run.
// GET /api/v1/org/applications/{application_id}/launch-plan/data-migration/runs/{run_id}
func (c *Client) GetApplicationDataMigrationRun(requestContext context.Context, applicationID string,
	runID string) (*DataMigrationRun, error) {
	var run DataMigrationRun
	if requestError := c.sendJSONContext(requestContext, http.MethodGet,
		applicationDataMigrationRunsURL(c.BaseURL, applicationID)+"/"+neturl.PathEscape(runID), nil,
		&run); requestError != nil {
		return nil, requestError
	}
	return &run, nil
}

// RollbackApplicationDataMigrationRun asks a run to roll back. The platform
// records the request (202) and applies it on its next pass; it refuses (409)
// before the cutover has started, once the rollback window has closed, for a
// finished run, and while a rollback is already requested or running.
// POST /api/v1/org/applications/{application_id}/launch-plan/data-migration/runs/{run_id}/rollback
func (c *Client) RollbackApplicationDataMigrationRun(requestContext context.Context, applicationID string,
	runID string) (*DataMigrationRun, error) {
	var run DataMigrationRun
	if requestError := c.sendJSONContext(requestContext, http.MethodPost,
		applicationDataMigrationRunsURL(c.BaseURL, applicationID)+"/"+neturl.PathEscape(runID)+"/rollback", nil,
		&run); requestError != nil {
		return nil, requestError
	}
	return &run, nil
}
