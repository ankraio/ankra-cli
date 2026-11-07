package client

// The per-cluster retention window of the scheduled etcd backups
// (ankra-hn630j.21): GET/PUT
// /api/v1/org/clusters/{cluster_id}/state-snapshot-retention, the bearer twin
// of the route the portal's cluster settings use.
//
// Ankra backs up the etcd state of every running cluster it provisions every
// six hours. It keeps the four newest backups plus the newest of each UTC day
// for the cluster's window: 7 days unless a member sets another between 1 and
// 90.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// StateSnapshotRetention is the wire shape both the GET and the PUT answer
// with. RetentionDays is the window in effect; IsDefault is true when none was
// set for the cluster.
type StateSnapshotRetention struct {
	ClusterID            string `json:"cluster_id" yaml:"cluster_id"`
	RetentionDays        int    `json:"retention_days" yaml:"retention_days"`
	IsDefault            bool   `json:"is_default" yaml:"is_default"`
	DefaultRetentionDays int    `json:"default_retention_days" yaml:"default_retention_days"`
	MinimumRetentionDays int    `json:"minimum_retention_days" yaml:"minimum_retention_days"`
	MaximumRetentionDays int    `json:"maximum_retention_days" yaml:"maximum_retention_days"`
	KeepLatest           int    `json:"keep_latest" yaml:"keep_latest"`
}

type stateSnapshotRetentionUpdate struct {
	RetentionDays int `json:"retention_days"`
}

func stateSnapshotRetentionURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/state-snapshot-retention", baseURL, url.PathEscape(clusterID))
}

// GetStateSnapshotRetention reads one cluster's retention window.
func (c *Client) GetStateSnapshotRetention(ctx context.Context, clusterID string) (*StateSnapshotRetention, error) {
	var retention StateSnapshotRetention
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		stateSnapshotRetentionURL(c.BaseURL, clusterID), nil, &retention); getError != nil {
		return nil, getError
	}
	return &retention, nil
}

// SetStateSnapshotRetention stores one cluster's retention window in days and
// returns the resulting state. The platform gates the write on clusters.write
// and refuses a window outside 1..90 with a 422.
func (c *Client) SetStateSnapshotRetention(ctx context.Context, clusterID string, retentionDays int) (*StateSnapshotRetention, error) {
	var retention StateSnapshotRetention
	if putError := c.sendJSONContext(ctx, http.MethodPut, stateSnapshotRetentionURL(c.BaseURL, clusterID),
		stateSnapshotRetentionUpdate{RetentionDays: retentionDays}, &retention); putError != nil {
		return nil, putError
	}
	return &retention, nil
}
