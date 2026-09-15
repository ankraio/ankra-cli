package client

// The cluster Backups read (epic ankra-0xsdd, bead ankra-0xsdd.46): the typed
// client for GET /api/v1/org/clusters/imported/{cluster_id}/backups, the one
// route behind the portal's cluster Backups tab.
//
// It is the terminal's answer to "is this cluster's data protected, and what
// can I restore it from". `restore-points list` answers that for one stack at
// a time and only about restore points; nothing before this served the verdict
// per stack, the schedule that produces it, or the state of the backup data
// plane the captures depend on.
//
// Bearer twin only, like backup_vaults.go and restore_points.go.

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// The protection verdicts, mirroring usecase/backupposture. There are three,
// and "could not tell" is a different answer from "not protected": a cluster
// whose inventory has not synced is not a cluster with nothing to protect.
const (
	ProtectionStateProtected   = "protected"
	ProtectionStateUnprotected = "unprotected"
	ProtectionStateUnknown     = "unknown"
)

// ProtectionStates is the closed set, for a flag that refuses a typo rather
// than sending it and getting back an empty page.
var ProtectionStates = []string{ProtectionStateProtected, ProtectionStateUnprotected, ProtectionStateUnknown}

// IsProtectionState reports whether the value is one of the three verdicts.
func IsProtectionState(value string) bool {
	for _, state := range ProtectionStates {
		if state == value {
			return true
		}
	}
	return false
}

// The state of the platform-authored ankra-backup stack on a cluster. Four,
// not three: an installed data plane that is DOWN is neither installing,
// ready nor absent.
const (
	ClusterBackupStackAbsent     = "absent"
	ClusterBackupStackInstalling = "installing"
	ClusterBackupStackReady      = "ready"
	ClusterBackupStackDegraded   = "degraded"
)

// StackRestorePointSummary is the newest restore point covering a stack.
type StackRestorePointSummary struct {
	ID        string `json:"id" yaml:"id"`
	Status    string `json:"status" yaml:"status"`
	SizeBytes int64  `json:"size_bytes" yaml:"size_bytes"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
}

// StackRunSummary is the newest data run of any kind that named the stack as
// its source. The id is the umbrella run's, which is what `ankra runs get`
// addresses.
type StackRunSummary struct {
	ID        string `json:"id" yaml:"id"`
	Kind      string `json:"kind" yaml:"kind"`
	Status    string `json:"status" yaml:"status"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
}

// StackBackupsRow is one stack's line of the cluster's backup posture.
//
// DataAssetCountKnown is a separate fact from DataAssetCount for the same
// reason ProtectionState has three values: an inventory that has not landed
// reports zero assets, and zero assets is not "this stack holds no data".
type StackBackupsRow struct {
	StackName string `json:"stack_name" yaml:"stack_name"`
	// SystemStack marks the platform-authored ankra-backup stack, which is in
	// the listing so the counts are the posture's counts.
	SystemStack         bool                      `json:"system_stack" yaml:"system_stack"`
	Stateful            bool                      `json:"stateful" yaml:"stateful"`
	ProtectionState     string                    `json:"protection_state" yaml:"protection_state"`
	UnprotectedReason   string                    `json:"unprotected_reason,omitempty" yaml:"unprotected_reason,omitempty"`
	UnknownReason       string                    `json:"unknown_reason,omitempty" yaml:"unknown_reason,omitempty"`
	DataAssetCount      int                       `json:"data_asset_count" yaml:"data_asset_count"`
	DataAssetCountKnown bool                      `json:"data_asset_count_known" yaml:"data_asset_count_known"`
	DatabaseAssetCount  int                       `json:"database_asset_count" yaml:"database_asset_count"`
	VaultName           string                    `json:"vault_name,omitempty" yaml:"vault_name,omitempty"`
	VaultState          string                    `json:"vault_state,omitempty" yaml:"vault_state,omitempty"`
	Schedule            string                    `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	NextScheduledAt     *string                   `json:"next_scheduled_at" yaml:"next_scheduled_at"`
	FailingRuns24h      int                       `json:"failing_runs_24h" yaml:"failing_runs_24h"`
	LastRestorePoint    *StackRestorePointSummary `json:"last_restore_point" yaml:"last_restore_point"`
	LatestRun           *StackRunSummary          `json:"latest_run" yaml:"latest_run"`
}

// ClusterBackupsRollup is the cluster's counts, plus the organisation's vault
// readiness. VaultsKnown false means the vault listing could not be read, so
// the two counts are not established rather than zero.
type ClusterBackupsRollup struct {
	TotalStacks               int     `json:"total_stacks" yaml:"total_stacks"`
	StatefulStacks            int     `json:"stateful_stacks" yaml:"stateful_stacks"`
	ProtectedStacks           int     `json:"protected_stacks" yaml:"protected_stacks"`
	UnprotectedStatefulStacks int     `json:"unprotected_stateful_stacks" yaml:"unprotected_stateful_stacks"`
	UnknownStacks             int     `json:"unknown_stacks" yaml:"unknown_stacks"`
	FailingSchedules          int     `json:"failing_schedules" yaml:"failing_schedules"`
	LastRestorePointAt        *string `json:"last_restore_point_at" yaml:"last_restore_point_at"`
	VaultsTotal               int     `json:"vaults_total" yaml:"vaults_total"`
	VaultsReady               int     `json:"vaults_ready" yaml:"vaults_ready"`
	VaultsKnown               bool    `json:"vaults_known" yaml:"vaults_known"`
}

// ClusterBackupsPage is one keyset page of the cluster's stacks, with the
// rollup computed over every one of them rather than over the page.
type ClusterBackupsPage struct {
	ClusterID        string               `json:"cluster_id" yaml:"cluster_id"`
	BackupStackState string               `json:"backup_stack_state" yaml:"backup_stack_state"`
	Rollup           ClusterBackupsRollup `json:"rollup" yaml:"rollup"`
	Stacks           []StackBackupsRow    `json:"stacks" yaml:"stacks"`
	NextCursor       *string              `json:"next_cursor" yaml:"next_cursor"`
}

// ClusterBackupsOptions selects one page. ProtectionStates narrows the rows
// and never the rollup, which answers for the cluster.
type ClusterBackupsOptions struct {
	ProtectionStates []string
	Cursor           string
	Limit            int
}

func (options ClusterBackupsOptions) query() neturl.Values {
	query := neturl.Values{}
	for _, state := range options.ProtectionStates {
		query.Add("protection_state", state)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	return query
}

func clusterBackupsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/backups", baseURL, neturl.PathEscape(clusterID))
}

// GetClusterBackups returns one page of a cluster's backup posture.
// GET /api/v1/org/clusters/imported/{cluster_id}/backups
func (c *Client) GetClusterBackups(clusterID string, options ClusterBackupsOptions) (*ClusterBackupsPage, error) {
	url := clusterBackupsURL(c.BaseURL, clusterID)
	if query := options.query(); len(query) > 0 {
		url += "?" + query.Encode()
	}
	var page ClusterBackupsPage
	if requestError := c.sendJSON(http.MethodGet, url, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}
