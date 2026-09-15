package client

// Restore points, protection and restore (epic ankra-0xsdd, bead
// ankra-0xsdd.14): the typed client for go/internal/restorepointsapi on the
// cluster-api. Every route is mounted twice - a browser twin under /org that
// authenticates with a session cookie, and a bearer-PAT twin under
// /api/v1/org - and this client only ever speaks the bearer twin, the way
// backup_vaults.go and backup_vault_imports.go address the platform.
//
// A restore point is the epic's one artifact: an immutable, self-describing
// copy of a stack's data in a backup vault. Backing up creates one, restoring
// applies one. NotCarried is served on every read rather than only on the
// detail, and this client keeps it on the list shape for the same reason the
// platform does: a listing that showed sizes and hid omissions is the silence
// the lane exists to remove.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
)

// Statuses of a restore point, mirroring enginekit/restorepoints.
const (
	RestorePointStatusCreating = "creating"
	RestorePointStatusComplete = "complete"
	RestorePointStatusFailed   = "failed"
	RestorePointStatusExpiring = "expiring"
	RestorePointStatusExpired  = "expired"
	RestorePointStatusDeleted  = "deleted"
)

// Why a restore point was created, mirroring enginekit/restorepoints.
const (
	RestorePointTriggerManual       = "manual"
	RestorePointTriggerScheduled    = "scheduled"
	RestorePointTriggerClone        = "clone"
	RestorePointTriggerMigrate      = "migrate"
	RestorePointTriggerPreRestore   = "pre_restore"
	RestorePointTriggerVerification = "verification"
)

// RestoreModeInPlace restores a stack over itself on its own cluster. It is
// the only mode the platform accepts today; restoring into another stack or
// another cluster is the clone lane.
const RestoreModeInPlace = "in_place"

// BackupStackInstalling and BackupStackReady are the two states the data
// plane that takes the captures can be in on a protected stack's cluster.
const (
	BackupStackInstalling = "installing"
	BackupStackReady      = "ready"
)

// RestorePointNotCarried is one thing a restore point does not contain, why,
// and what to do about it.
type RestorePointNotCarried struct {
	Kind   string `json:"kind" yaml:"kind"`
	Name   string `json:"name" yaml:"name"`
	Reason string `json:"reason" yaml:"reason"`
	Remedy string `json:"remedy,omitempty" yaml:"remedy,omitempty"`
}

// RestorePointAsset is one captured asset. PointInTimeFrom and PointInTimeTo
// are the manifest's pitr_from / pitr_to: the recovery window an engine that
// ships write-ahead logs can restore to, absent for everything else.
type RestorePointAsset struct {
	ID              string  `json:"id" yaml:"id"`
	Kind            string  `json:"kind" yaml:"kind"`
	Engine          string  `json:"engine" yaml:"engine"`
	Consistency     string  `json:"consistency" yaml:"consistency"`
	Namespace       string  `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Name            string  `json:"name" yaml:"name"`
	DatabaseEngine  string  `json:"database_engine,omitempty" yaml:"database_engine,omitempty"`
	SizeBytes       int64   `json:"size_bytes" yaml:"size_bytes"`
	Path            string  `json:"path,omitempty" yaml:"path,omitempty"`
	PointInTimeFrom *string `json:"pitr_from,omitempty" yaml:"pitr_from,omitempty"`
	PointInTimeTo   *string `json:"pitr_to,omitempty" yaml:"pitr_to,omitempty"`
}

// RestorePointScope is what the restore point covers.
type RestorePointScope struct {
	Kind       string   `json:"kind" yaml:"kind"`
	ClusterID  string   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	StackNames []string `json:"stack_names" yaml:"stack_names"`
}

// RestorePointSource is where the restore point was taken from, recorded so
// the artifact can be reasoned about after the source cluster is gone.
type RestorePointSource struct {
	Kind              string   `json:"kind" yaml:"kind"`
	ClusterName       string   `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	Provider          string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	KubernetesVersion string   `json:"kubernetes_version,omitempty" yaml:"kubernetes_version,omitempty"`
	Topology          string   `json:"topology,omitempty" yaml:"topology,omitempty"`
	CSISnapshots      bool     `json:"csi_snapshots" yaml:"csi_snapshots"`
	StorageClasses    []string `json:"storage_classes" yaml:"storage_classes"`
}

// RestorePointSizes is what the capture measured.
type RestorePointSizes struct {
	TotalBytes     int64 `json:"total_bytes" yaml:"total_bytes"`
	VolumesBytes   int64 `json:"volumes_bytes" yaml:"volumes_bytes"`
	DatabasesBytes int64 `json:"databases_bytes" yaml:"databases_bytes"`
}

// RestorePointManifest is manifest.json verbatim: the self-describing index
// that makes a restore point readable without the source cluster. Served on
// the detail read only.
type RestorePointManifest struct {
	SchemaVersion  int                      `json:"schema_version" yaml:"schema_version"`
	RestorePointID string                   `json:"restore_point_id" yaml:"restore_point_id"`
	OrganisationID string                   `json:"organisation_id" yaml:"organisation_id"`
	CreatedAt      string                   `json:"created_at" yaml:"created_at"`
	Trigger        string                   `json:"trigger" yaml:"trigger"`
	ObjectPrefix   string                   `json:"object_prefix,omitempty" yaml:"object_prefix,omitempty"`
	Scope          RestorePointScope        `json:"scope" yaml:"scope"`
	Source         RestorePointSource       `json:"source" yaml:"source"`
	Assets         []RestorePointAsset      `json:"assets" yaml:"assets"`
	NotCarried     []RestorePointNotCarried `json:"not_carried" yaml:"not_carried"`
	Sizes          RestorePointSizes        `json:"sizes" yaml:"sizes"`
	Warnings       []string                 `json:"warnings" yaml:"warnings"`
}

// RestorePointRun is the producing run reduced to what a restore point's
// reader needs. The full run, with every attempt of every step, is
// `ankra runs get <id>`.
type RestorePointRun struct {
	ID            string  `json:"id" yaml:"id"`
	Kind          string  `json:"kind" yaml:"kind"`
	Status        string  `json:"status" yaml:"status"`
	Phase         string  `json:"phase" yaml:"phase"`
	BlockedReason string  `json:"blocked_reason,omitempty" yaml:"blocked_reason,omitempty"`
	ErrorExcerpt  *string `json:"error_excerpt" yaml:"error_excerpt"`
	StartedAt     *string `json:"started_at" yaml:"started_at"`
	FinishedAt    *string `json:"finished_at" yaml:"finished_at"`
}

// RestorePoint is one restore point on the wire. Manifest, Assets and Run are
// carried by the detail read only; every other field is on both shapes.
type RestorePoint struct {
	ID                 string                   `json:"id" yaml:"id"`
	OrganisationID     string                   `json:"organisation_id" yaml:"organisation_id"`
	BackupVaultID      string                   `json:"backup_vault_id" yaml:"backup_vault_id"`
	ScopeKind          string                   `json:"scope_kind" yaml:"scope_kind"`
	SourceClusterID    *string                  `json:"source_cluster_id" yaml:"source_cluster_id"`
	SourceClusterName  string                   `json:"source_cluster_name" yaml:"source_cluster_name"`
	StackNames         []string                 `json:"stack_names" yaml:"stack_names"`
	Trigger            string                   `json:"trigger" yaml:"trigger"`
	Status             string                   `json:"status" yaml:"status"`
	ObjectPrefix       string                   `json:"object_prefix" yaml:"object_prefix"`
	TotalBytes         int64                    `json:"total_bytes" yaml:"total_bytes"`
	ImmutabilityMode   string                   `json:"immutability_mode" yaml:"immutability_mode"`
	RetainUntil        *string                  `json:"retain_until" yaml:"retain_until"`
	HoldReason         *string                  `json:"hold_reason" yaml:"hold_reason"`
	ExpiresAt          *string                  `json:"expires_at" yaml:"expires_at"`
	VerificationStatus string                   `json:"verification_status" yaml:"verification_status"`
	VerifiedAt         *string                  `json:"verified_at" yaml:"verified_at"`
	ErrorExcerpt       *string                  `json:"error_excerpt" yaml:"error_excerpt"`
	CreatedByUserID    *string                  `json:"created_by_user_id" yaml:"created_by_user_id"`
	CreatedAt          string                   `json:"created_at" yaml:"created_at"`
	UpdatedAt          string                   `json:"updated_at" yaml:"updated_at"`
	CompletedAt        *string                  `json:"completed_at" yaml:"completed_at"`
	RunID              *string                  `json:"run_id" yaml:"run_id"`
	AssetCount         int                      `json:"asset_count" yaml:"asset_count"`
	NotCarried         []RestorePointNotCarried `json:"not_carried" yaml:"not_carried"`
	Assets             []RestorePointAsset      `json:"assets,omitempty" yaml:"assets,omitempty"`
	Manifest           *RestorePointManifest    `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Run                *RestorePointRun         `json:"run,omitempty" yaml:"run,omitempty"`
}

// RestorePointListResult is one keyset page, newest first. NextCursor is nil
// when the page was the last one.
type RestorePointListResult struct {
	RestorePoints []RestorePoint `json:"restore_points" yaml:"restore_points"`
	NextCursor    *string        `json:"next_cursor" yaml:"next_cursor"`
}

// ListRestorePointsOptions are the filters both the per-stack and the
// organisation-wide listing accept. ClusterID and StackName are query
// parameters on the organisation-wide listing and path segments on the
// per-stack one, so ListStackRestorePoints takes them separately.
type ListRestorePointsOptions struct {
	ClusterID string
	StackName string
	Statuses  []string
	Triggers  []string
	VaultID   string
	Cursor    string
	Limit     int
}

func (options ListRestorePointsOptions) sharedQuery() neturl.Values {
	query := neturl.Values{}
	for _, status := range options.Statuses {
		query.Add("status", status)
	}
	for _, trigger := range options.Triggers {
		query.Add("trigger", trigger)
	}
	if options.VaultID != "" {
		query.Set("backup_vault_id", options.VaultID)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	return query
}

// RestorePointSelection is what a capture is asked to carry. Databases is a
// pointer because the platform tells an omitted field from an explicit
// false: omitting the whole selection falls back to the stack's stored one,
// while `{"databases": false}` excludes them and needs
// ConfirmExcludeDatabases alongside it.
type RestorePointSelection struct {
	Databases               *bool    `json:"databases,omitempty"`
	PersistentVolumeClaims  []string `json:"persistent_volume_claims,omitempty"`
	ConfirmExcludeDatabases bool     `json:"confirm_exclude_databases,omitempty"`
}

// CreateRestorePointRequest is the "back up now" body. Every field is
// optional: the vault falls back to the stack's policy and then to the
// organisation's single ready vault, and the selection falls back to the
// stack's stored selection and then to the default.
type CreateRestorePointRequest struct {
	VaultID   string                 `json:"vault_id,omitempty"`
	Selection *RestorePointSelection `json:"selection,omitempty"`
	Note      string                 `json:"note,omitempty"`
}

// CreateRestorePointResult is the 202: what now exists, and the run to watch.
// Nothing is dispatched on the request path, so the run is where the capture
// becomes observable.
type CreateRestorePointResult struct {
	RestorePointID string       `json:"restore_point_id" yaml:"restore_point_id"`
	RunID          string       `json:"run_id" yaml:"run_id"`
	RestorePoint   RestorePoint `json:"restore_point" yaml:"restore_point"`
	Warnings       []string     `json:"warnings" yaml:"warnings"`
}

// DeleteRestorePointResult is the 202 a delete answers. OperationID is the
// execution sweeping the objects out of the bucket and is nil when no sweep
// could be dispatched - the source cluster is gone, the vault is gone, or the
// agent does not carry the job - in which case ObjectsRetainedReason says
// which, because a delete that quietly left the objects behind would
// understate somebody's storage bill forever.
type DeleteRestorePointResult struct {
	RestorePointID        string  `json:"restore_point_id" yaml:"restore_point_id"`
	Status                string  `json:"status" yaml:"status"`
	OperationID           *string `json:"operation_id" yaml:"operation_id"`
	ObjectsRetainedReason string  `json:"objects_retained_reason,omitempty" yaml:"objects_retained_reason,omitempty"`
}

// RestoreRestorePointRequest is the restore body. Confirm must be the stack's
// own name, and Force overrides the drift refusal.
type RestoreRestorePointRequest struct {
	Mode    string `json:"mode"`
	Confirm string `json:"confirm"`
	Force   bool   `json:"force"`
}

// RestoreRestorePointResult is the 202 a restore answers. Sequence is what
// the restore will do, in order, so a caller can show it afterwards as well
// as before.
type RestoreRestorePointResult struct {
	RestorePointID string       `json:"restore_point_id" yaml:"restore_point_id"`
	RunID          string       `json:"run_id" yaml:"run_id"`
	Mode           string       `json:"mode" yaml:"mode"`
	Sequence       []string     `json:"sequence" yaml:"sequence"`
	Warnings       []string     `json:"warnings" yaml:"warnings"`
	RestorePoint   RestorePoint `json:"restore_point" yaml:"restore_point"`
}

// BackupRetention is the grandfather-father-son retention a policy keeps.
// MinimumCount and MinimumAge are the floors retention never prunes below.
type BackupRetention struct {
	Hourly       int    `json:"hourly" yaml:"hourly"`
	Daily        int    `json:"daily" yaml:"daily"`
	Weekly       int    `json:"weekly" yaml:"weekly"`
	Monthly      int    `json:"monthly" yaml:"monthly"`
	Yearly       int    `json:"yearly" yaml:"yearly"`
	MinimumCount int    `json:"minimum_count" yaml:"minimum_count"`
	MinimumAge   string `json:"minimum_age,omitempty" yaml:"minimum_age,omitempty"`
}

// BackupPolicySelection is the selection a stored policy settled on.
type BackupPolicySelection struct {
	Databases              bool     `json:"databases" yaml:"databases"`
	PersistentVolumeClaims []string `json:"persistent_volume_claims" yaml:"persistent_volume_claims"`
}

// BackupPolicy is the block protect writes onto the stack's definition.
type BackupPolicy struct {
	Enabled   bool                  `json:"enabled" yaml:"enabled"`
	Vault     string                `json:"vault" yaml:"vault"`
	Schedule  string                `json:"schedule" yaml:"schedule"`
	Retention BackupRetention       `json:"retention" yaml:"retention"`
	Selection BackupPolicySelection `json:"selection" yaml:"selection"`
}

// ProtectStackRequest turns protection on for a stack. VaultID is required -
// a protected stack with nowhere to write to is not protected. Schedule is
// hourly, daily, weekly or a five-field cron expression.
type ProtectStackRequest struct {
	VaultID   string                 `json:"vault_id"`
	Schedule  string                 `json:"schedule,omitempty"`
	Retention *BackupRetention       `json:"retention,omitempty"`
	Selection *RestorePointSelection `json:"selection,omitempty"`
	BackupNow bool                   `json:"backup_now,omitempty"`
}

// UnprotectStackRequest clears the block. Confirm must be the stack's name.
type UnprotectStackRequest struct {
	Confirm string `json:"confirm"`
}

// StackProtection is what a protect or an unprotect answers with.
// BackupStack reports whether the data plane that takes the captures exists
// on the cluster yet, so a caller is never told "protected" while the Velero
// that would do the protecting is still being installed.
type StackProtection struct {
	StackName      string        `json:"stack_name" yaml:"stack_name"`
	Policy         BackupPolicy  `json:"policy" yaml:"policy"`
	BackupStack    string        `json:"backup_stack" yaml:"backup_stack"`
	Warnings       []string      `json:"warnings" yaml:"warnings"`
	RestorePointID *string       `json:"restore_point_id" yaml:"restore_point_id"`
	RunID          *string       `json:"run_id" yaml:"run_id"`
	RestorePoint   *RestorePoint `json:"restore_point,omitempty" yaml:"restore_point,omitempty"`
	CommitSHA      *string       `json:"commit_sha" yaml:"commit_sha"`
	CommitURL      *string       `json:"commit_url" yaml:"commit_url"`
	GitPushMessage string        `json:"git_push_message,omitempty" yaml:"git_push_message,omitempty"`
}

// BackupPolicyViolation names one field-level refusal of a protect.
type BackupPolicyViolation struct {
	Key     string `json:"key" yaml:"key"`
	Message string `json:"message" yaml:"message"`
}

// BackupPolicyValidationError carries every reason a policy was refused, not
// just the first. The platform answers all of them at once precisely so a
// caller can fix a schedule and a retention in one edit instead of
// discovering them one round-trip at a time.
type BackupPolicyValidationError struct {
	Detail     string
	Violations []BackupPolicyViolation
}

func (validationError *BackupPolicyValidationError) Error() string {
	if validationError == nil {
		return ""
	}
	detail := validationError.Detail
	if detail == "" {
		detail = "The backup policy is not valid."
	}
	reasons := make([]string, 0, len(validationError.Violations))
	for _, violation := range validationError.Violations {
		reasons = append(reasons, fmt.Sprintf("%s: %s", violation.Key, violation.Message))
	}
	if len(reasons) == 0 {
		return detail
	}
	return detail + " " + strings.Join(reasons, " ")
}

func stackRestorePointsURL(baseURL string, clusterID string, stackName string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/restore-points",
		baseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
}

// ListStackRestorePoints returns one keyset page of a stack's restore points,
// newest first.
// GET /api/v1/org/clusters/imported/{cluster_id}/stacks/{stack_name}/restore-points
func (c *Client) ListStackRestorePoints(clusterID string, stackName string,
	options ListRestorePointsOptions) (*RestorePointListResult, error) {
	url := stackRestorePointsURL(c.BaseURL, clusterID, stackName)
	if query := options.sharedQuery(); len(query) > 0 {
		url += "?" + query.Encode()
	}
	var result RestorePointListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListOrganisationRestorePoints returns one keyset page of the
// organisation's restore points across every cluster.
// GET /api/v1/org/restore-points
func (c *Client) ListOrganisationRestorePoints(options ListRestorePointsOptions) (*RestorePointListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/restore-points", c.BaseURL)
	query := options.sharedQuery()
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	if options.StackName != "" {
		query.Set("stack_name", options.StackName)
	}
	if len(query) > 0 {
		url += "?" + query.Encode()
	}
	var result RestorePointListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetStackRestorePoint returns one restore point with its manifest, assets
// and producing run. A restore point belonging to another stack answers 404
// through this stack's path, the same as an id that never existed.
// GET .../restore-points/{restore_point_id}
func (c *Client) GetStackRestorePoint(clusterID string, stackName string,
	restorePointID string) (*RestorePoint, error) {
	url := stackRestorePointsURL(c.BaseURL, clusterID, stackName) + "/" + neturl.PathEscape(restorePointID)
	var result RestorePoint
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreateStackRestorePoint asks for a capture now and returns the 202: the
// restore point in `creating` and the run that will seal it.
// POST .../restore-points
func (c *Client) CreateStackRestorePoint(clusterID string, stackName string,
	request CreateRestorePointRequest) (*CreateRestorePointResult, error) {
	url := stackRestorePointsURL(c.BaseURL, clusterID, stackName)
	var result CreateRestorePointResult
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DeleteStackRestorePoint removes a restore point and dispatches the sweep of
// its objects.
// DELETE .../restore-points/{restore_point_id}
func (c *Client) DeleteStackRestorePoint(clusterID string, stackName string,
	restorePointID string) (*DeleteRestorePointResult, error) {
	url := stackRestorePointsURL(c.BaseURL, clusterID, stackName) + "/" + neturl.PathEscape(restorePointID)
	var result DeleteRestorePointResult
	if requestError := c.sendJSON(http.MethodDelete, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RestoreStackRestorePoint restores a restore point over the stack it was
// taken from and returns the 202 with the sequence the restore will follow.
// POST .../restore-points/{restore_point_id}/restore
func (c *Client) RestoreStackRestorePoint(clusterID string, stackName string, restorePointID string,
	request RestoreRestorePointRequest) (*RestoreRestorePointResult, error) {
	url := stackRestorePointsURL(c.BaseURL, clusterID, stackName) +
		"/" + neturl.PathEscape(restorePointID) + "/restore"
	var result RestoreRestorePointResult
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

func stackProtectURL(baseURL string, clusterID string, stackName string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/protect",
		baseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
}

// ProtectStack turns protection on for a stack: it writes the backup block
// onto the stack's definition, which the scheduler's backup_stack_ensure loop
// converges on by installing the backup data plane.
// POST .../stacks/{stack_name}/protect
func (c *Client) ProtectStack(clusterID string, stackName string,
	request ProtectStackRequest) (*StackProtection, error) {
	return c.sendProtectionRequest(http.MethodPost, stackProtectURL(c.BaseURL, clusterID, stackName), request)
}

// UnprotectStack clears the backup block. Every restore point already taken
// is kept and stays restorable: removing protection is a decision about the
// future.
// DELETE .../stacks/{stack_name}/protect
func (c *Client) UnprotectStack(clusterID string, stackName string,
	request UnprotectStackRequest) (*StackProtection, error) {
	return c.sendProtectionRequest(http.MethodDelete, stackProtectURL(c.BaseURL, clusterID, stackName), request)
}

// sendProtectionRequest is sendJSON with one addition: a 422 carrying
// field-level violations becomes a *BackupPolicyValidationError rather than
// the bare detail sentence, so the command can print every reason the policy
// was refused instead of only "The backup policy is not valid."
func (c *Client) sendProtectionRequest(method string, url string, payload any) (*StackProtection, error) {
	encoded, marshalError := json.Marshal(payload)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	request, requestError := http.NewRequest(method, url, bytes.NewReader(encoded))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if validationError := backupPolicyValidationFromBody(response.StatusCode, body); validationError != nil {
			return nil, validationError
		}
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("request failed", response.StatusCode, redactedBodyForError(body, 500))
	}
	var result StackProtection
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &result, nil
}

// backupPolicyValidationFromBody parses the protect route's 422 body; nil for
// any other status or a body carrying no violations, so an ordinary
// validation refusal keeps its existing handling.
func backupPolicyValidationFromBody(statusCode int, body []byte) *BackupPolicyValidationError {
	if statusCode != http.StatusUnprocessableEntity {
		return nil
	}
	var parsed struct {
		Detail     string                  `json:"detail"`
		Violations []BackupPolicyViolation `json:"violations"`
	}
	if unmarshalError := json.Unmarshal(body, &parsed); unmarshalError != nil || len(parsed.Violations) == 0 {
		return nil
	}
	return &BackupPolicyValidationError{Detail: parsed.Detail, Violations: parsed.Violations}
}
