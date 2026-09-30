package client

import (
	"fmt"
	neturl "net/url"
)

// ClusterGitopsStatus mirrors the backend's GitOpsStatusResponse for
// GET /api/v1/clusters/{cluster_id}/gitops/status: which repository, branch,
// and credential a cluster syncs from, plus the latest sync outcome. A cluster
// without GitOps history answers the same shape with null fields, and a
// missing cluster row answers sync_status "not_configured".
type ClusterGitopsStatus struct {
	SyncStatus          *string `json:"sync_status" yaml:"sync_status"`
	LastSyncedAt        *string `json:"last_synced_at" yaml:"last_synced_at"`
	LastSyncedFrom      *string `json:"last_synced_from" yaml:"last_synced_from"`
	LastCommitSHA       *string `json:"last_commit_sha" yaml:"last_commit_sha"`
	LastCommitTimestamp *string `json:"last_commit_timestamp" yaml:"last_commit_timestamp"`
	PendingCommitSHA    *string `json:"pending_commit_sha" yaml:"pending_commit_sha"`
	SyncPhase           *string `json:"sync_phase" yaml:"sync_phase"`
	SyncProgressMessage *string `json:"sync_progress_message" yaml:"sync_progress_message"`
	// AppliedWithFailedMembers marks a "synced" answer whose member deploy
	// jobs are failing, so "synced" alone does not over-promise.
	AppliedWithFailedMembers bool `json:"applied_with_failed_members" yaml:"applied_with_failed_members"`
	// Error is one of the backend's error info objects (general, validation,
	// or multiple-validation), kept schemaless so every arm round-trips
	// through -o json|yaml unchanged.
	Error          map[string]interface{} `json:"error" yaml:"error"`
	RetryCount     int                    `json:"retry_count" yaml:"retry_count"`
	ClusterName    *string                `json:"cluster_name" yaml:"cluster_name"`
	ClusterShortID *string                `json:"cluster_short_id" yaml:"cluster_short_id"`
	GitRepo        *ClusterGitopsRepo     `json:"git_repo" yaml:"git_repo"`
	// OpenConflictCount is the number of GitOps merge conflicts awaiting a
	// decision. While it is above zero the platform applies nothing from Git,
	// and sync_status reads "conflict". Nil when the platform does not report
	// it (older platforms), which is not the same as zero.
	OpenConflictCount *int `json:"open_conflict_count" yaml:"open_conflict_count"`
	// OpenConflictKeys names those conflicts' resource keys. The platform caps
	// the list; OpenConflictCount is always the full total.
	OpenConflictKeys []string `json:"open_conflict_keys" yaml:"open_conflict_keys"`
}

// ClusterGitopsRepo mirrors the git_repo member of the GitOps status payload.
// The owner/name pair is provider-shaped: repo_owner/repo_name for GitHub,
// workspace/repo_slug for Bitbucket Cloud, project_key/repo_slug (plus
// instance_url) for Bitbucket Data Center.
type ClusterGitopsRepo struct {
	Provider       string  `json:"provider" yaml:"provider"`
	Branch         string  `json:"branch" yaml:"branch"`
	WebURL         string  `json:"web_url" yaml:"web_url"`
	RepoOwner      *string `json:"repo_owner" yaml:"repo_owner"`
	RepoName       *string `json:"repo_name" yaml:"repo_name"`
	Workspace      *string `json:"workspace" yaml:"workspace"`
	RepoSlug       *string `json:"repo_slug" yaml:"repo_slug"`
	ProjectKey     *string `json:"project_key" yaml:"project_key"`
	InstanceURL    *string `json:"instance_url" yaml:"instance_url"`
	CredentialName *string `json:"credential_name" yaml:"credential_name"`
}

// GetClusterGitopsStatus fetches the GitOps sync snapshot for a cluster.
func (c *Client) GetClusterGitopsStatus(clusterID string) (*ClusterGitopsStatus, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/gitops/status", c.BaseURL, neturl.PathEscape(clusterID))
	var status ClusterGitopsStatus
	if err := c.getJSON(url, &status); err != nil {
		return nil, err
	}
	return &status, nil
}
