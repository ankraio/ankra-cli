package client

// Runs (epic ankra-0xsdd, bead ankra-0xsdd.14): the typed client for
// go/internal/runsapi on the cluster-api - the organisation's run history and
// the backup, restore and clone runs the backup lane moves data with.
//
// A run is a run, so every route needs runs.read (and runs.operate to act on
// one); a data run is also a backup object, so reading one additionally needs
// backups.read, acting on one additionally needs backups.operate, and the
// whole data half is behind the organisation's `backups` feature flag.
//
// The listing has two flavours behind one route: a filter only a data run can
// answer - cluster, stack or batch - pages over the data-movement table and
// attaches each run's payload, and anything else pages over the run table
// alone. ListRunsOptions carries both sets and the server decides.

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// Run kinds, mirroring enginekit/runs.
const (
	RunKindPipeline              = "pipeline"
	RunKindPromotion             = "promotion"
	RunKindRestore               = "restore"
	RunKindBackup                = "backup"
	RunKindClone                 = "clone"
	RunKindDisasterRecoveryDrill = "dr_drill"
	RunKindEnvironmentHydrate    = "environment_hydrate"
)

// Run statuses, mirroring enginekit/runs. The last three are terminal.
const (
	RunStatusPending          = "pending"
	RunStatusRunning          = "running"
	RunStatusBlocked          = "blocked"
	RunStatusAwaitingApproval = "awaiting_approval"
	RunStatusSucceeded        = "succeeded"
	RunStatusFailed           = "failed"
	RunStatusCancelled        = "cancelled"
)

// IsTerminalRunStatus reports whether the status ends the run's life, so a
// caller that follows a run knows when to stop.
func IsTerminalRunStatus(status string) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}

// DataRunAssetSelection is one asset a run was asked to carry.
type DataRunAssetSelection struct {
	ID             string `json:"id" yaml:"id"`
	Kind           string `json:"kind" yaml:"kind"`
	Engine         string `json:"engine" yaml:"engine"`
	Namespace      string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Name           string `json:"name" yaml:"name"`
	DatabaseEngine string `json:"database_engine,omitempty" yaml:"database_engine,omitempty"`
}

// DataRunAssetPlan is what the run was asked to move. Engine is the plan's
// DEFAULT capture engine and is empty for a mixed plan, where each asset
// carries its own; an asset's own engine always wins.
type DataRunAssetPlan struct {
	Engine     string                   `json:"engine,omitempty" yaml:"engine,omitempty"`
	Assets     []DataRunAssetSelection  `json:"assets" yaml:"assets"`
	NotCarried []RestorePointNotCarried `json:"not_carried,omitempty" yaml:"not_carried,omitempty"`
}

// DataRunStep is one attempt of one step. Every attempt is carried, not only
// the newest: a restore that took three attempts is what explains an hour
// nobody can otherwise account for.
type DataRunStep struct {
	ID              string  `json:"id" yaml:"id"`
	StepKey         string  `json:"step_key" yaml:"step_key"`
	Position        int     `json:"position" yaml:"position"`
	Attempt         int     `json:"attempt" yaml:"attempt"`
	Status          string  `json:"status" yaml:"status"`
	FailureClass    *string `json:"failure_class" yaml:"failure_class"`
	ClusterID       *string `json:"cluster_id" yaml:"cluster_id"`
	JobName         string  `json:"job_name" yaml:"job_name"`
	ExecutionID     *string `json:"execution_id" yaml:"execution_id"`
	ExecutionStepID *string `json:"execution_step_id" yaml:"execution_step_id"`
	ErrorExcerpt    *string `json:"error_excerpt" yaml:"error_excerpt"`
	StartedAt       *string `json:"started_at" yaml:"started_at"`
	FinishedAt      *string `json:"finished_at" yaml:"finished_at"`
	CreatedAt       string  `json:"created_at" yaml:"created_at"`
	UpdatedAt       string  `json:"updated_at" yaml:"updated_at"`
}

// DataRun is the payload only a backup, restore or clone run carries. Steps
// are attached by the detail read and by a retry, never by a listing.
type DataRun struct {
	ID                  string            `json:"id" yaml:"id"`
	Kind                string            `json:"kind" yaml:"kind"`
	Mode                string            `json:"mode" yaml:"mode"`
	Phase               string            `json:"phase" yaml:"phase"`
	Plan                []string          `json:"plan" yaml:"plan"`
	BackupVaultID       string            `json:"backup_vault_id" yaml:"backup_vault_id"`
	RestorePointID      *string           `json:"restore_point_id" yaml:"restore_point_id"`
	SourceClusterID     *string           `json:"source_cluster_id" yaml:"source_cluster_id"`
	SourceStackName     string            `json:"source_stack_name" yaml:"source_stack_name"`
	TargetClusterID     *string           `json:"target_cluster_id" yaml:"target_cluster_id"`
	TargetStackName     string            `json:"target_stack_name" yaml:"target_stack_name"`
	AssetPlan           DataRunAssetPlan  `json:"asset_plan" yaml:"asset_plan"`
	NamespaceMapping    map[string]string `json:"namespace_mapping" yaml:"namespace_mapping"`
	StorageClassMapping map[string]string `json:"storage_class_mapping" yaml:"storage_class_mapping"`
	BatchID             *string           `json:"batch_id" yaml:"batch_id"`
	BlockedReason       *string           `json:"blocked_reason" yaml:"blocked_reason"`
	AttemptBudget       int               `json:"attempt_budget" yaml:"attempt_budget"`
	RetryOfDataRunID    *string           `json:"retry_of_data_run_id" yaml:"retry_of_data_run_id"`
	Steps               []DataRunStep     `json:"steps,omitempty" yaml:"steps,omitempty"`
}

// Run is one row of the organisation's run history. DataRun is attached for
// the backup, restore and clone kinds and absent for every other, so one
// client renders every run and asks for the extra block only where there is
// one.
type Run struct {
	ID              string   `json:"id" yaml:"id"`
	OrganisationID  string   `json:"organisation_id" yaml:"organisation_id"`
	Kind            string   `json:"kind" yaml:"kind"`
	Status          string   `json:"status" yaml:"status"`
	DisplayName     string   `json:"display_name" yaml:"display_name"`
	InitiatedBy     string   `json:"initiated_by" yaml:"initiated_by"`
	AnkraUserID     *string  `json:"ankra_user_id" yaml:"ankra_user_id"`
	ApplicationID   *string  `json:"application_id" yaml:"application_id"`
	EnvironmentID   *string  `json:"environment_id" yaml:"environment_id"`
	PromotionID     *string  `json:"promotion_id" yaml:"promotion_id"`
	CommitSHA       string   `json:"commit_sha" yaml:"commit_sha"`
	ArtefactDigest  string   `json:"artefact_digest" yaml:"artefact_digest"`
	ParentRunID     *string  `json:"parent_run_id" yaml:"parent_run_id"`
	ErrorExcerpt    *string  `json:"error_excerpt" yaml:"error_excerpt"`
	LastHeartbeatAt *string  `json:"last_heartbeat_at" yaml:"last_heartbeat_at"`
	StartedAt       *string  `json:"started_at" yaml:"started_at"`
	FinishedAt      *string  `json:"finished_at" yaml:"finished_at"`
	CreatedAt       string   `json:"created_at" yaml:"created_at"`
	UpdatedAt       string   `json:"updated_at" yaml:"updated_at"`
	DataRun         *DataRun `json:"data_run,omitempty" yaml:"data_run,omitempty"`
}

// RunListResult is one keyset page. NextCursor is nil when the page was the
// last one.
type RunListResult struct {
	Runs       []Run   `json:"runs" yaml:"runs"`
	NextCursor *string `json:"next_cursor" yaml:"next_cursor"`
}

// ListRunsOptions are the listing's filters. ClusterID, StackName and BatchID
// are the ones only a data run can answer: naming any of them pages over the
// data-movement table and attaches each run's payload.
type ListRunsOptions struct {
	Kind          string
	Status        string
	ClusterID     string
	StackName     string
	BatchID       string
	ApplicationID string
	EnvironmentID string
	Cursor        string
	Limit         int
}

func (options ListRunsOptions) query() neturl.Values {
	query := neturl.Values{}
	for name, value := range map[string]string{
		"kind":           options.Kind,
		"status":         options.Status,
		"cluster_id":     options.ClusterID,
		"stack_name":     options.StackName,
		"batch_id":       options.BatchID,
		"application_id": options.ApplicationID,
		"environment_id": options.EnvironmentID,
		"cursor":         options.Cursor,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	return query
}

// ListRuns returns one keyset page of the organisation's runs.
// GET /api/v1/org/runs
func (c *Client) ListRuns(options ListRunsOptions) (*RunListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/runs", c.BaseURL)
	if query := options.query(); len(query) > 0 {
		url += "?" + query.Encode()
	}
	var result RunListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetRun returns one run. A backup, restore or clone comes back with its
// data-movement payload and every attempt of every step.
// GET /api/v1/org/runs/{run_id}
func (c *Client) GetRun(runID string) (*Run, error) {
	url := fmt.Sprintf("%s/api/v1/org/runs/%s", c.BaseURL, neturl.PathEscape(runID))
	var result Run
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CancelRun stops a run that has not finished. A run that already concluded
// answers 409 rather than reporting a state change that did not happen.
// POST /api/v1/org/runs/{run_id}/cancel
func (c *Client) CancelRun(runID string) (*Run, error) {
	url := fmt.Sprintf("%s/api/v1/org/runs/%s/cancel", c.BaseURL, neturl.PathEscape(runID))
	var result Run
	if requestError := c.sendJSON(http.MethodPost, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RetryRun opens a fresh run resuming a failed backup, restore or clone from
// the step that failed. The answer is the NEW run: the old row will never
// move again, so polling it would wait forever.
// POST /api/v1/org/runs/{run_id}/retry
func (c *Client) RetryRun(runID string) (*Run, error) {
	url := fmt.Sprintf("%s/api/v1/org/runs/%s/retry", c.BaseURL, neturl.PathEscape(runID))
	var result Run
	if requestError := c.sendJSON(http.MethodPost, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
