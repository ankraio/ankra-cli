package client

// LatestStateSnapshot is the newest captured state snapshot a start of the cluster may restore.
type LatestStateSnapshot struct {
	ID          string  `json:"id"`
	Trigger     string  `json:"trigger"`
	CapturedAt  *string `json:"captured_at"`
	KubeVersion *string `json:"kube_version"`
}

// LatestStateSnapshotResponse answers GET /api/v1/clusters/{kind}/{cluster_id}/state-snapshots/latest; Snapshot is
// nil when the cluster has no restorable capture.
type LatestStateSnapshotResponse struct {
	ClusterID string               `json:"cluster_id"`
	Snapshot  *LatestStateSnapshot `json:"snapshot"`
}

// GetLatestStateSnapshot reads the newest state snapshot a start of the cluster may restore.
func (c *Client) GetLatestStateSnapshot(kind, clusterID string) (*LatestStateSnapshotResponse, error) {
	var result LatestStateSnapshotResponse
	if err := c.getJSON(c.providerClusterURL(kind, clusterID, "state-snapshots/latest"), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
