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
	// Keeping is what the stack's backups keep. It is nil both when the
	// platform sent null (the deployment has not created its stack yet) and
	// when it sent no key at all (a platform that predates the block); the
	// structured output tells the two apart because it prints the platform's
	// own document (see ApplicationBackups), not this struct.
	Keeping *ApplicationBackupKeeping `json:"keeping,omitempty" yaml:"keeping,omitempty"`
}

// ApplicationBackupKeeping is what one deployment's backups keep: what the
// retention policy asks for, and the restore points actually kept now.
//
// State is known, no_policy or unknown, and a reader branches on it first.
// With unknown every other field is null or false and none of them means
// "none". With known or no_policy, KeptCount and UnattributedKeptCount are
// set (zero is a stack that keeps no restore point of its own) and a nil
// OldestKeptAt means it keeps none of its own. ProtectedSince and
// LastRestoreTest each carry their own state (known, none or unknown) and
// are set only when it is known.
type ApplicationBackupKeeping struct {
	State string `json:"state" yaml:"state"`
	// Policy is the stored retention, nil unless State is known.
	Policy *BackupRetention `json:"policy" yaml:"policy"`
	// KeepsDays is the policy's longest period in whole days, nil when the
	// policy keeps everything, keeps a minimum count only, keeps less than a
	// day, or State is not known.
	KeepsDays       *int    `json:"keeps_days" yaml:"keeps_days"`
	KeepsEverything bool    `json:"keeps_everything" yaml:"keeps_everything"`
	OldestKeptAt    *string `json:"oldest_kept_at" yaml:"oldest_kept_at"`
	KeptCount       *int    `json:"kept_count" yaml:"kept_count"`
	// UnattributedKeptCount counts restore points carrying the stack's name
	// that record no source stack, so whether they are this stack's cannot be
	// told. They are not in KeptCount.
	UnattributedKeptCount *int    `json:"unattributed_kept_count" yaml:"unattributed_kept_count"`
	ProtectedSince        *string `json:"protected_since" yaml:"protected_since"`
	// ProtectedSinceState is known (ProtectedSince is set), none (no restore
	// point of this stack ever completed) or unknown (the answer cannot be
	// told).
	ProtectedSinceState string                        `json:"protected_since_state" yaml:"protected_since_state"`
	LastRestoreTest     *ApplicationBackupRestoreTest `json:"last_restore_test" yaml:"last_restore_test"`
	// LastRestoreTestState is known (LastRestoreTest is set), none (no
	// restore was ever tested) or unknown (the answer cannot be told).
	LastRestoreTestState string `json:"last_restore_test_state" yaml:"last_restore_test_state"`
}

// ApplicationBackupRestoreTest is the newest restore test of any of the
// stack's restore points. TookSeconds and BytesBack are nil when the test did
// not record them.
type ApplicationBackupRestoreTest struct {
	TestedAt    *string `json:"tested_at" yaml:"tested_at"`
	State       string  `json:"state" yaml:"state"`
	TookSeconds *int64  `json:"took_seconds" yaml:"took_seconds"`
	BytesBack   *int64  `json:"bytes_back" yaml:"bytes_back"`
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
//
// DatabaseBackup is a pointer for the same reason. The platform's default
// for an application that has never been decided on is ON, so a bare bool
// would read a response that carries no setting as "off" - the opposite of
// what the next deploy of that application will do.
//
// The value keeps the platform's own response document beside the decode,
// and -o json|yaml prints that document: a field sent as null stays null, a
// field the platform did not send stays out, and a field this CLI does not
// declare yet is passed through rather than dropped.
type ApplicationBackups struct {
	RawDocument     `json:"-" yaml:"-"`
	ApplicationID   string                        `json:"application_id" yaml:"application_id"`
	ApplicationName string                        `json:"application_name" yaml:"application_name"`
	Database        ApplicationDatabase           `json:"database" yaml:"database"`
	DatabaseBackup  *bool                         `json:"database_backup" yaml:"database_backup"`
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

// The two deployment routes are built by their own functions rather than from
// a shared `/deployments/{id}` prefix. That prefix is not itself a route, and
// TestClusterRoutesAreRegistered resolves every /api/v1 literal in this
// package against the cluster's route census - so a helper that produces one
// is reported as a path the router does not register.

func applicationProtectURL(baseURL string, applicationID string, deploymentID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/deployments/%s/protect",
		baseURL, neturl.PathEscape(applicationID), neturl.PathEscape(deploymentID))
}

func applicationRestorePointsURL(baseURL string, applicationID string, deploymentID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/deployments/%s/restore-points",
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
		applicationProtectURL(c.BaseURL, applicationID, deploymentID), request,
		"saving the backup policy",
		fmt.Sprintf("ankra application backups %s   # the deployment reads protected with this vault "+
			"and schedule when it landed%s", applicationID, protectionReadCaveat),
		protectRetryConsequence(request.BackupNow))
}

// CreateApplicationRestorePoint asks for a capture of one deployment now and
// returns the 202: the restore point in `creating` and the run that seals it.
// POST /api/v1/org/applications/{application_id}/deployments/{deployment_id}/restore-points
func (c *Client) CreateApplicationRestorePoint(applicationID string, deploymentID string,
	request CreateRestorePointRequest) (*CreateRestorePointResult, error) {
	var result CreateRestorePointResult
	if requestError := c.sendJSON(http.MethodPost,
		applicationRestorePointsURL(c.BaseURL, applicationID, deploymentID),
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
