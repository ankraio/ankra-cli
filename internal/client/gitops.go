package client

import (
	"context"
	"fmt"
	"net/http"
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

// GitOps merge conflicts: GET /api/v1/org/clusters/{cluster_id}/gitops/conflicts
// and the two resolve routes beside it, the bearer twins of the routes the
// portal's GitOps panel uses. A conflict is a resource changed both in the
// GitOps repository and on the platform since the two last agreed; GitOps sync
// applies nothing from Git while any conflict is undecided.

// Resolution choices the resolve routes accept: keep Git's version (the
// platform's record is overwritten with it) or keep the cluster's version
// (it is pushed back to the repository).
const (
	GitopsConflictKeepGit     = "git"
	GitopsConflictKeepCluster = "cluster"
)

// GitopsConflict is one open conflict.
type GitopsConflict struct {
	// ResourceKey identifies the resource (for example
	// "stack:web/addon:nginx") and is what the resolve route takes.
	ResourceKey  string  `json:"resource_key" yaml:"resource_key"`
	StackName    *string `json:"stack_name" yaml:"stack_name"`
	ResourceKind string  `json:"resource_kind" yaml:"resource_kind"`
	ResourceName *string `json:"resource_name" yaml:"resource_name"`
	// GitChangeType and DBChangeType say how the repository and the
	// platform each changed the resource ("added", "modified", "removed").
	GitChangeType        string  `json:"git_change_type" yaml:"git_change_type"`
	DBChangeType         string  `json:"db_change_type" yaml:"db_change_type"`
	LastAppliedCommitSHA *string `json:"last_applied_commit_sha" yaml:"last_applied_commit_sha"`
	DetectedAt           *string `json:"detected_at" yaml:"detected_at"`
	// ResolutionChoice is the decision already recorded ("git" or
	// "cluster") that the next sync applies; nil while undecided.
	ResolutionChoice *string `json:"resolution_choice" yaml:"resolution_choice"`
	ResolvedAt       *string `json:"resolved_at" yaml:"resolved_at"`
}

// GitopsConflictList is the list route's answer.
type GitopsConflictList struct {
	Conflicts []GitopsConflict `json:"conflicts" yaml:"conflicts"`
	Total     int              `json:"total" yaml:"total"`
}

// GitopsConflictResolution is both resolve routes' answer. ClearedCount is
// how many conflicts the choice was recorded on; SyncTriggered says whether
// the sync that applies it was started, and when it was not the next
// periodic reconcile applies it instead.
type GitopsConflictResolution struct {
	ClearedCount  int    `json:"cleared_count" yaml:"cleared_count"`
	SyncTriggered bool   `json:"sync_triggered" yaml:"sync_triggered"`
	Message       string `json:"message" yaml:"message"`
}

// gitopsConflictResolveBody is the body of both resolve routes. Resolution
// is always sent: the routes read a missing one as "clear the conflicts
// undecided and re-check", which is not a decision and not what any CLI
// verb asks for.
type gitopsConflictResolveBody struct {
	ResourceKey string `json:"resource_key,omitempty"`
	Resolution  string `json:"resolution"`
}

func clusterGitopsConflictsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/gitops/conflicts", baseURL, neturl.PathEscape(clusterID))
}

// validateGitopsConflictKeep refuses anything but the two resolution
// choices before a request is sent, so no caller can reach the routes'
// undecided clear by passing an empty or misspelled side.
func validateGitopsConflictKeep(keep string) error {
	if keep != GitopsConflictKeepGit && keep != GitopsConflictKeepCluster {
		return fmt.Errorf("resolution must be %q or %q, got %q", GitopsConflictKeepGit, GitopsConflictKeepCluster, keep)
	}
	return nil
}

// ListClusterGitopsConflicts lists a cluster's open GitOps conflicts.
func (c *Client) ListClusterGitopsConflicts(ctx context.Context, clusterID string) (*GitopsConflictList, error) {
	var conflicts GitopsConflictList
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		clusterGitopsConflictsURL(c.BaseURL, clusterID), nil, &conflicts); getError != nil {
		return nil, getError
	}
	return &conflicts, nil
}

// ResolveClusterGitopsConflict records keep ("git" or "cluster") on one open
// conflict. The platform gates it on clusters.write for the cluster.
func (c *Client) ResolveClusterGitopsConflict(ctx context.Context, clusterID string, resourceKey string, keep string) (*GitopsConflictResolution, error) {
	if validationError := validateGitopsConflictKeep(keep); validationError != nil {
		return nil, validationError
	}
	if resourceKey == "" {
		return nil, fmt.Errorf("a resource key is required")
	}
	var resolution GitopsConflictResolution
	if postError := c.sendJSONContext(ctx, http.MethodPost,
		clusterGitopsConflictsURL(c.BaseURL, clusterID)+"/resolve-resource",
		gitopsConflictResolveBody{ResourceKey: resourceKey, Resolution: keep}, &resolution); postError != nil {
		return nil, postError
	}
	return &resolution, nil
}

// ResolveAllClusterGitopsConflicts records keep ("git" or "cluster") on
// every conflict open on the cluster when the platform receives the request.
func (c *Client) ResolveAllClusterGitopsConflicts(ctx context.Context, clusterID string, keep string) (*GitopsConflictResolution, error) {
	if validationError := validateGitopsConflictKeep(keep); validationError != nil {
		return nil, validationError
	}
	var resolution GitopsConflictResolution
	if postError := c.sendJSONContext(ctx, http.MethodPost,
		clusterGitopsConflictsURL(c.BaseURL, clusterID)+"/resolve",
		gitopsConflictResolveBody{Resolution: keep}, &resolution); postError != nil {
		return nil, postError
	}
	return &resolution, nil
}
