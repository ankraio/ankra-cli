package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"
)

// A backup vault's contents: what the bucket actually holds, as opposed to
// what the platform's rows say it holds. The two are not the same, which is
// the reason this endpoint exists - see `ankra backup vaults contents`.

// VaultPrefixUsage is what one prefix in the bucket holds.
type VaultPrefixUsage struct {
	Prefix      string `json:"prefix"`
	ObjectCount int    `json:"object_count"`
	TotalBytes  int64  `json:"total_bytes"`
}

// VaultRestorePointContents is one restore point's footprint.
//
// DeclaredObjectPrefix is where the platform records the objects; LocatedPrefixes
// is where they are. A delete sweeps the first.
type VaultRestorePointContents struct {
	RestorePointID    string     `json:"restore_point_id"`
	Status            string     `json:"status"`
	StackNames        []string   `json:"stack_names"`
	SourceClusterID   string     `json:"source_cluster_id,omitempty"`
	SourceClusterName string     `json:"source_cluster_name,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	RowDeletedAt      *time.Time `json:"row_deleted_at,omitempty"`

	DeclaredObjectPrefix string           `json:"declared_object_prefix"`
	DeclaredPrefixUsage  VaultPrefixUsage `json:"declared_prefix_usage"`

	LocatedPrefixes []VaultPrefixUsage `json:"located_prefixes"`
	ObjectCount     int                `json:"object_count"`
	TotalBytes      int64              `json:"total_bytes"`

	SharedRepositories []string `json:"shared_repositories,omitempty"`
	RecordedTotalBytes int64    `json:"recorded_total_bytes"`
	Notes              []string `json:"notes,omitempty"`
}

// VaultSharedRepository is one uploader repository. Its bytes belong to every
// backup of the cluster and namespace, never to one restore point.
type VaultSharedRepository struct {
	VaultPrefixUsage
	ClusterID                 string   `json:"cluster_id"`
	Namespace                 string   `json:"namespace,omitempty"`
	ReferencedByRestorePoints []string `json:"referenced_by_restore_points"`
	Unreferenced              bool     `json:"unreferenced"`
	Note                      string   `json:"note,omitempty"`
}

// VaultOrphanGroup is objects no row accounts for.
type VaultOrphanGroup struct {
	VaultPrefixUsage
	Kind           string     `json:"kind"`
	RestorePointID string     `json:"restore_point_id,omitempty"`
	ClusterID      string     `json:"cluster_id,omitempty"`
	RowDeletedAt   *time.Time `json:"row_deleted_at,omitempty"`
	Reason         string     `json:"reason"`
}

// VaultObject is one object key.
type VaultObject struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
}

// BackupVaultContents is the whole listing.
//
// Complete=false means the listing stopped short, so the totals are a floor
// and the orphan list is withheld rather than guessed at.
type BackupVaultContents struct {
	VaultID   string `json:"vault_id"`
	VaultName string `json:"vault_name"`
	Bucket    string `json:"bucket"`
	Endpoint  string `json:"endpoint"`

	ScannedPrefix string `json:"scanned_prefix,omitempty"`
	Complete      bool   `json:"complete"`

	// OrphansDetermined says whether Orphans is a finding or was simply not
	// computed. A narrowed read walks its scope completely and still cannot
	// support "nothing accounts for this", so this is its own field.
	OrphansDetermined bool `json:"orphans_determined"`

	ObjectCount int   `json:"object_count"`
	TotalBytes  int64 `json:"total_bytes"`

	RestorePoints      []VaultRestorePointContents `json:"restore_points"`
	SharedRepositories []VaultSharedRepository     `json:"shared_repositories"`
	Orphans            []VaultOrphanGroup          `json:"orphans"`
	Other              []VaultPrefixUsage          `json:"other"`

	Objects  []VaultObject `json:"objects,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// BackupVaultContentsRequest narrows the listing.
type BackupVaultContentsRequest struct {
	Prefix         string
	RestorePointID string
	IncludeObjects bool
}

// GetBackupVaultContents reads what the vault's bucket holds.
//
// A vault whose bucket cannot be read answers 502 with a detail saying so, and
// that error is returned rather than an empty listing: an unreadable vault and
// an empty vault are different facts.
// GET /api/v1/org/backup-vaults/{vault_id}/contents
func (c *Client) GetBackupVaultContents(vaultID string,
	request BackupVaultContentsRequest) (*BackupVaultContents, error) {
	query := neturl.Values{}
	if request.Prefix != "" {
		query.Set("prefix", request.Prefix)
	}
	if request.RestorePointID != "" {
		query.Set("restore_point_id", request.RestorePointID)
	}
	if request.IncludeObjects {
		query.Set("objects", strconv.FormatBool(true))
	}

	url := fmt.Sprintf("%s/api/v1/org/backup-vaults/%s/contents", c.BaseURL, neturl.PathEscape(vaultID))
	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}

	var result BackupVaultContents
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
