package client

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// GitOps preserved paths: GET /api/v1/org/clusters/{cluster_id}/gitops/preserved-paths
// and POST .../preserved-paths/resolve, the bearer twins of the routes the
// portal's GitOps panel uses. A preserved path is a file in the GitOps
// repository the platform did not write: the ownership boundary keeps Git's
// version instead of overwriting it, so Git and the platform are divided on
// that file until a person decides it. Unlike a merge conflict it pauses
// nothing; the rest of the cluster keeps syncing.

// The decisions the resolve route accepts. "git" imports Git's version of the
// file into the platform (the cluster changes to match it), "cluster" writes
// the platform's version into Git, and "none" withdraws a recorded decision.
const (
	GitopsPreservedResolutionGit     = "git"
	GitopsPreservedResolutionCluster = "cluster"
	GitopsPreservedResolutionNone    = "none"
)

// The structural difference states a listing reports.
const (
	GitopsPreservedDiffComputed       = "computed"
	GitopsPreservedDiffFormattingOnly = "formatting_only"
	GitopsPreservedDiffNotStructured  = "not_structured"
	GitopsPreservedDiffNotRecorded    = "not_recorded"
)

// The kinds of one structural change, read from Git's version to Ankra's.
const (
	GitopsPreservedChangeAdded     = "added"
	GitopsPreservedChangeRemoved   = "removed"
	GitopsPreservedChangeChanged   = "changed"
	GitopsPreservedChangeReordered = "reordered"
)

// GitopsPreservedRedactedSopsEncrypted is the redaction reason of a file that
// holds SOPS-encrypted content. The platform refuses to write its own version
// over such a file.
const GitopsPreservedRedactedSopsEncrypted = "sops_encrypted"

// The structural_diff_values outcomes of a listing.
const (
	GitopsPreservedDiffValuesRevealed           = "revealed"
	GitopsPreservedDiffValuesPermissionRequired = "permission_required"
	GitopsPreservedDiffValuesUnavailable        = "unavailable"
)

// GitopsPreservedPathChange is one key or list member where the two versions
// of a preserved file differ, read from Git's version to Ankra's.
type GitopsPreservedPathChange struct {
	// Path is the key path ("spec.stacks[name=web].addons[name=nginx].chart_version").
	Path string `json:"path" yaml:"path"`
	// Change is added (only in Ankra's version), removed (only in Git's),
	// changed or reordered.
	Change string `json:"change" yaml:"change"`
	// GitValue and AnkraValue are the scalar on each side, nil when the side
	// has none there or the value is masked.
	GitValue   *string `json:"git_value" yaml:"git_value"`
	AnkraValue *string `json:"ankra_value" yaml:"ankra_value"`
	// ValueMasked says a side holds a scalar here that is not shown.
	ValueMasked bool `json:"value_masked" yaml:"value_masked"`
}

// GitopsPreservedPathDiff is where a preserved file's two versions differ.
type GitopsPreservedPathDiff struct {
	Status string `json:"status" yaml:"status"`
	// ValuesWithheldReason names why no change shows any value (the file is
	// withheld as a whole); nil when values are judged one at a time.
	ValuesWithheldReason *string                     `json:"values_withheld_reason" yaml:"values_withheld_reason"`
	Changes              []GitopsPreservedPathChange `json:"changes" yaml:"changes"`
	// Total counts every difference; Truncated says Changes stops short.
	Total     int  `json:"total" yaml:"total"`
	Truncated bool `json:"truncated" yaml:"truncated"`
}

// GitopsPreservedPath is one preserved file with both of its versions (or why
// they are withheld) and the two digests a decision must name.
type GitopsPreservedPath struct {
	Path                  string  `json:"path" yaml:"path"`
	HeadContent           string  `json:"head_content" yaml:"head_content"`
	HeadContentHash       string  `json:"head_content_hash" yaml:"head_content_hash"`
	RenderContent         string  `json:"render_content" yaml:"render_content"`
	RenderContentHash     string  `json:"render_content_hash" yaml:"render_content_hash"`
	ContentRedacted       bool    `json:"content_redacted" yaml:"content_redacted"`
	ContentRedactedReason *string `json:"content_redacted_reason" yaml:"content_redacted_reason"`
	// ContentOmitted says the listing's content budget ran out before this
	// file; its digests and decision are still exact.
	ContentOmitted bool    `json:"content_omitted" yaml:"content_omitted"`
	HeadCommitSHA  *string `json:"head_commit_sha" yaml:"head_commit_sha"`
	DetectedAt     *string `json:"detected_at" yaml:"detected_at"`
	LastSeenAt     *string `json:"last_seen_at" yaml:"last_seen_at"`
	// ResolutionChoice is nil while the file awaits a decision, "git" or
	// "cluster" once one is recorded; the next sync runs it.
	ResolutionChoice *string `json:"resolution_choice" yaml:"resolution_choice"`
	ResolvedAt       *string `json:"resolved_at" yaml:"resolved_at"`
	// ImportRefusedReason is why the last "git" decision over these two
	// versions was not imported; the decision was cleared.
	ImportRefusedReason *string                 `json:"import_refused_reason" yaml:"import_refused_reason"`
	ImportRefusedAt     *string                 `json:"import_refused_at" yaml:"import_refused_at"`
	StructuralDiff      GitopsPreservedPathDiff `json:"structural_diff" yaml:"structural_diff"`
}

// GitopsPreservedPathList is the listing route's answer. Total is the full
// count; Truncated says the listing left files or content out.
type GitopsPreservedPathList struct {
	RawDocument          `json:"-" yaml:"-"`
	PreservedPaths       []GitopsPreservedPath `json:"preserved_paths" yaml:"preserved_paths"`
	Total                int                   `json:"total" yaml:"total"`
	Truncated            bool                  `json:"truncated" yaml:"truncated"`
	StructuralDiffValues string                `json:"structural_diff_values" yaml:"structural_diff_values"`
}

// GitopsPreservedPathDecision is the resolve route's body. Both digests come
// from the listing the person decided over: the platform refuses a decision
// whose digests are not the ones on record.
type GitopsPreservedPathDecision struct {
	Path              string `json:"path"`
	Resolution        string `json:"resolution"`
	HeadContentHash   string `json:"head_content_hash"`
	RenderContentHash string `json:"render_content_hash"`
}

// GitopsPreservedPathResolution is the resolve route's 200 answer.
type GitopsPreservedPathResolution struct {
	RawDocument   `json:"-" yaml:"-"`
	Path          string `json:"path" yaml:"path"`
	Resolution    string `json:"resolution" yaml:"resolution"`
	SyncTriggered bool   `json:"sync_triggered" yaml:"sync_triggered"`
	Message       string `json:"message" yaml:"message"`
}

func clusterGitopsPreservedPathsURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/gitops/preserved-paths", baseURL, neturl.PathEscape(clusterID))
}

// validateGitopsPreservedPathDecision refuses a decision the route would
// misread before anything is sent: an unknown resolution, no path, or a
// missing digest.
func validateGitopsPreservedPathDecision(decision GitopsPreservedPathDecision) error {
	switch decision.Resolution {
	case GitopsPreservedResolutionGit, GitopsPreservedResolutionCluster, GitopsPreservedResolutionNone:
	default:
		return fmt.Errorf("resolution must be %q, %q or %q, got %q", GitopsPreservedResolutionGit,
			GitopsPreservedResolutionCluster, GitopsPreservedResolutionNone, decision.Resolution)
	}
	if decision.Path == "" {
		return fmt.Errorf("a preserved path is required")
	}
	if decision.HeadContentHash == "" || decision.RenderContentHash == "" {
		return fmt.Errorf("both content digests from the listing are required")
	}
	return nil
}

// ListClusterGitopsPreservedPaths lists a cluster's preserved GitOps paths.
func (c *Client) ListClusterGitopsPreservedPaths(ctx context.Context, clusterID string) (*GitopsPreservedPathList, error) {
	var listing GitopsPreservedPathList
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		clusterGitopsPreservedPathsURL(c.BaseURL, clusterID), nil, &listing); getError != nil {
		return nil, getError
	}
	return &listing, nil
}

// ResolveClusterGitopsPreservedPath records a decision on one preserved path.
// The platform gates it on clusters.write for the cluster.
func (c *Client) ResolveClusterGitopsPreservedPath(ctx context.Context, clusterID string,
	decision GitopsPreservedPathDecision) (*GitopsPreservedPathResolution, error) {
	if validationError := validateGitopsPreservedPathDecision(decision); validationError != nil {
		return nil, validationError
	}
	var resolution GitopsPreservedPathResolution
	if postError := c.sendJSONContext(ctx, http.MethodPost,
		clusterGitopsPreservedPathsURL(c.BaseURL, clusterID)+"/resolve", decision, &resolution); postError != nil {
		return nil, postError
	}
	return &resolution, nil
}
