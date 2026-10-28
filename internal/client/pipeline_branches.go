package client

// The branch listing (ankra-n8q38.2): GET …/pipeline-branches, one row per
// ref of a pipeline's repository with the newest run on it. It is the read
// behind `ankra pipeline branches`, where the unit of attention is a branch
// rather than a run.

import (
	"context"
	"net/http"
	neturl "net/url"
	"strconv"
)

// The kinds a PipelineBranch.Kind carries, mirroring
// enginekit/pipelinerun's BranchKind* vocabulary. A ref the platform could
// not classify is "other" rather than "branch", so a caller can tell a
// classified ref from one the vocabulary did not recognise.
const (
	PipelineBranchKindBranch      = "branch"
	PipelineBranchKindPullRequest = "pull_request"
	PipelineBranchKindTag         = "tag"
	PipelineBranchKindOther       = "other"
)

// PipelineBranch is the wire shape of one ref of a pipeline's repository
// (go/internal/pipelineapi/branches.go pipelineBranchResponse).
type PipelineBranch struct {
	// Ref is the stored trigger_ref, which is what ListPipelineRunsOptions.Branch
	// filters on; Name is its short form, what a person calls the branch or tag.
	Ref  string `json:"ref"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// IsDefault reports that this ref is the repository's recorded default
	// branch. False on a repository whose default branch Ankra was never told
	// is "not known to be the default", not "known not to be".
	IsDefault bool `json:"is_default"`
	// PullRequestNumber is the pull request the LATEST run belongs to, null
	// for a run that belongs to none. A merge-request run records its target
	// branch as its ref, so this is how the newest thing to happen on a
	// branch shows as a pull request rather than a push.
	PullRequestNumber *int64 `json:"pull_request_number"`
	// LatestRun is the newest run on the ref, in the same shape
	// ListPipelineRuns and GetPipelineRun answer.
	LatestRun PipelineRun `json:"latest_run"`
	// PreviousOutcome is the outcome of the newest concluded run before
	// LatestRun, null when the ref has no earlier concluded run.
	PreviousOutcome *string `json:"previous_outcome"`
	// RunCount is every run ever recorded on the ref; SupersededCount how
	// many of them a newer run on the same concurrency group cancelled, a
	// subset of the first.
	RunCount        int64 `json:"run_count"`
	SupersededCount int64 `json:"superseded_count"`
	// LastActivityAt is when the newest run on the ref was created.
	LastActivityAt string `json:"last_activity_at"`
	// Stale reports that no run has been created on the ref for fourteen
	// days. It is that rule alone: whether a pull request on the ref is still
	// open is not derivable from what the platform stores, and the listing
	// does not call the repository's host to find out.
	Stale bool `json:"stale"`
}

// PipelineBranchList is the GET …/pipeline-branches body. NextCursor is null
// when the page was the last one.
type PipelineBranchList struct {
	Branches   []PipelineBranch `json:"branches"`
	NextCursor *string          `json:"next_cursor"`
}

// ListPipelineBranchesOptions is the GET …/pipeline-branches query.
type ListPipelineBranchesOptions struct {
	Cursor string
	Limit  int
}

// ListPipelineBranches reads one page of a pipeline's refs, the default
// branch first and then by most recent activity.
func (c *Client) ListPipelineBranches(ctx context.Context, selector PipelineSelector,
	options ListPipelineBranchesOptions) (*PipelineBranchList, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineBranchList
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		c.BaseURL+pipelineBranchesEndpoint(base, options), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

func pipelineBranchesEndpoint(base string, options ListPipelineBranchesOptions) string {
	query := neturl.Values{}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	endpoint := base + "/pipeline-branches"
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return endpoint
}
