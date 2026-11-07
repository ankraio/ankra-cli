package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ApplicationBranchBuild is the newest published build of one branch of an
// application: the image tag a deploy following that branch would run.
type ApplicationBranchBuild struct {
	Branch    string `json:"branch" yaml:"branch"`
	ImageTag  string `json:"image_tag" yaml:"image_tag"`
	CommitSHA string `json:"commit_sha" yaml:"commit_sha"`
	BuiltAt   string `json:"built_at" yaml:"built_at"`
}

// ApplicationBranchBuilds is the newest published build of each branch,
// newest first, beside the branch an environment without a rule follows
// (nil when the application tracks none).
type ApplicationBranchBuilds struct {
	ApplicationID string                   `json:"application_id" yaml:"application_id"`
	TrackedBranch *string                  `json:"tracked_branch" yaml:"tracked_branch"`
	Builds        []ApplicationBranchBuild `json:"builds" yaml:"builds"`
}

// GetApplicationBranchBuilds reads the newest published build of each branch
// of one application.
func (client *Client) GetApplicationBranchBuilds(requestContext context.Context, applicationID string) (*ApplicationBranchBuilds, error) {
	payload, requestError := client.applicationResourceRequest(requestContext, http.MethodGet,
		applicationPath(applicationID, "/branch-builds"), nil, nil)
	if requestError != nil {
		return nil, fmt.Errorf("application branch builds request failed: %w", requestError)
	}
	var branchBuilds ApplicationBranchBuilds
	if parseError := json.Unmarshal(payload, &branchBuilds); parseError != nil {
		return nil, fmt.Errorf("parse application branch builds: %w", parseError)
	}
	if branchBuilds.Builds == nil {
		branchBuilds.Builds = []ApplicationBranchBuild{}
	}
	return &branchBuilds, nil
}
