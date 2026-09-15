package client

// Application backups (epic ankra-0xsdd, bead ankra-0xsdd.44): the typed
// client for the application half of go/internal/restorepointsapi.
//
// An application deploys as one stack per cluster, so these routes are the
// stack backup lane addressed the way a person thinks about it - "back up my
// application on production" rather than "back up stack deploy-shop on
// cluster 7f3c...". A deployment is named by its CLUSTER; the stack is
// resolved on the server from the application's own deployment records and is
// never sent, so the CLI cannot aim an application's protect at a stack that
// application does not own.
//
// Like the rest of this package, it speaks only the bearer twin under
// /api/v1/org.

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// ApplicationDatabase is the deploy contract's answer to "does this
// application ship a database". Status is three-valued - recorded, absent,
// unknown - and a caller must not render the last two the same way: `absent`
// is a repository Ankra read that declares none, and `unknown` is one it
// could not finish reading.
type ApplicationDatabase struct {
	Status       string `json:"status" yaml:"status"`
	Engine       string `json:"engine,omitempty" yaml:"engine,omitempty"`
	Operator     string `json:"operator,omitempty" yaml:"operator,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty" yaml:"manifest_path,omitempty"`
	Reason       string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// ApplicationBackupDeployment is one deployment of the application with the
// backup answer for the stack it runs as. Posture is nil while the stack does
// not exist yet or its posture could not be read; absent is not "unprotected".
type ApplicationBackupDeployment struct {
	// DeploymentID is the cluster id, and is what `--cluster` resolves to.
	DeploymentID     string              `json:"deployment_id" yaml:"deployment_id"`
	ClusterID        string              `json:"cluster_id" yaml:"cluster_id"`
	ClusterName      string              `json:"cluster_name" yaml:"cluster_name"`
	StackName        *string             `json:"stack_name" yaml:"stack_name"`
	Namespace        *string             `json:"namespace" yaml:"namespace"`
	DeploymentStatus string              `json:"deployment_status,omitempty" yaml:"deployment_status,omitempty"`
	HasDatabase      bool                `json:"has_database" yaml:"has_database"`
	HasDatabaseKnown bool                `json:"has_database_known" yaml:"has_database_known"`
	Posture          *StackBackupPosture `json:"posture" yaml:"posture"`
}

// StackBackupPosture is the stack's protection verdict as the backup posture
// surface answers it.
type StackBackupPosture struct {
	ClusterID              string  `json:"cluster_id" yaml:"cluster_id"`
	StackName              string  `json:"stack_name" yaml:"stack_name"`
	ProtectionState        string  `json:"protection_state" yaml:"protection_state"`
	UnprotectedReason      string  `json:"unprotected_reason,omitempty" yaml:"unprotected_reason,omitempty"`
	UnknownReason          string  `json:"unknown_reason,omitempty" yaml:"unknown_reason,omitempty"`
	Stateful               bool    `json:"stateful" yaml:"stateful"`
	VaultName              string  `json:"vault_name,omitempty" yaml:"vault_name,omitempty"`
	VaultState             string  `json:"vault_state,omitempty" yaml:"vault_state,omitempty"`
	Schedule               string  `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	DataAssetCount         int     `json:"data_asset_count" yaml:"data_asset_count"`
	DataAssetCountKnown    bool    `json:"data_asset_count_known" yaml:"data_asset_count_known"`
	DatabaseAssetCount     int     `json:"database_asset_count" yaml:"database_asset_count"`
	LastRestorePointAt     *string `json:"last_restore_point_at" yaml:"last_restore_point_at"`
	LastRestorePointStatus *string `json:"last_restore_point_status" yaml:"last_restore_point_status"`
	FailingRuns24h         int     `json:"failing_runs_24h" yaml:"failing_runs_24h"`
	NextScheduledAt        *string `json:"next_scheduled_at" yaml:"next_scheduled_at"`
}

// ApplicationBackups is the application's Backups section in one read.
//
// ReadyVaultKnown is false when the vault listing could not be read: the
// count is then not established rather than zero, and nothing may render
// "backups are not set up" from it.
type ApplicationBackups struct {
	ApplicationID   string                        `json:"application_id" yaml:"application_id"`
	ApplicationName string                        `json:"application_name" yaml:"application_name"`
	Database        ApplicationDatabase           `json:"database" yaml:"database"`
	DatabaseBackup  bool                          `json:"database_backup" yaml:"database_backup"`
	Deployments     []ApplicationBackupDeployment `json:"deployments" yaml:"deployments"`
	ReadyVaultCount int                           `json:"ready_vault_count" yaml:"ready_vault_count"`
	ReadyVaultKnown bool                          `json:"ready_vault_known" yaml:"ready_vault_known"`
	Warnings        []string                      `json:"warnings" yaml:"warnings"`
}

// ProtectApplicationDeploymentRequest turns protection on for one deployment.
// VaultID is optional here, unlike the stack route: an organisation with
// exactly one ready vault needs nobody to name it, and one with several is
// refused rather than chosen for.
type ProtectApplicationDeploymentRequest struct {
	VaultID   string                 `json:"vault_id,omitempty"`
	Schedule  string                 `json:"schedule,omitempty"`
	Retention *BackupRetention       `json:"retention,omitempty"`
	Selection *RestorePointSelection `json:"selection,omitempty"`
	BackupNow bool                   `json:"backup_now,omitempty"`
}

func applicationBackupsURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/backups", baseURL, neturl.PathEscape(applicationID))
}

func applicationDeploymentURL(baseURL string, applicationID string, deploymentID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/deployments/%s",
		baseURL, neturl.PathEscape(applicationID), neturl.PathEscape(deploymentID))
}

// GetApplicationBackups reads the application's Backups section: one answer
// per deployment, the declared database, and the database-backup setting.
// GET /api/v1/org/applications/{application_id}/backups
func (c *Client) GetApplicationBackups(applicationID string) (*ApplicationBackups, error) {
	var result ApplicationBackups
	if requestError := c.sendJSON(http.MethodGet,
		applicationBackupsURL(c.BaseURL, applicationID), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ProtectApplicationDeployment turns scheduled protection on for one
// deployment's stack, resolved on the server from the cluster named here.
// POST /api/v1/org/applications/{application_id}/deployments/{deployment_id}/protect
func (c *Client) ProtectApplicationDeployment(applicationID string, deploymentID string,
	request ProtectApplicationDeploymentRequest) (*StackProtection, error) {
	return c.sendProtectionRequest(http.MethodPost,
		applicationDeploymentURL(c.BaseURL, applicationID, deploymentID)+"/protect", request)
}

// CreateApplicationRestorePoint asks for a capture of one deployment now and
// returns the 202: the restore point in `creating` and the run that seals it.
// POST /api/v1/org/applications/{application_id}/deployments/{deployment_id}/restore-points
func (c *Client) CreateApplicationRestorePoint(applicationID string, deploymentID string,
	request CreateRestorePointRequest) (*CreateRestorePointResult, error) {
	var result CreateRestorePointResult
	if requestError := c.sendJSON(http.MethodPost,
		applicationDeploymentURL(c.BaseURL, applicationID, deploymentID)+"/restore-points",
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
