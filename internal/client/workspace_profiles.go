package client

// A repository's workspace profiles (cluster go/internal/workspacesapi): what
// its workspace of each kind runs. `ankra dev` reads them to pick the kind a
// routed command runs in.
//
//	GET /api/v1/org/pipeline-repositories/{repository_id}/workspace-profiles
//
// The platform answers this read only to a caller holding workspaces.manage;
// everyone else gets the RBAC 403 (a *PermissionDeniedError).

import (
	"context"
	"net/http"
	neturl "net/url"
)

// WorkspaceProfileResources is a profile's compute ceiling.
type WorkspaceProfileResources struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
}

// WorkspaceProfile is what a repository's workspace of one kind runs. Every
// field is optional: an empty image falls back to the repository's pipeline.
type WorkspaceProfile struct {
	Image      string                    `json:"image,omitempty" yaml:"image,omitempty"`
	Resources  WorkspaceProfileResources `json:"resources,omitempty" yaml:"resources,omitempty"`
	VolumeSize string                    `json:"volume_size,omitempty" yaml:"volume_size,omitempty"`
}

// WorkspaceProfiles is a repository's workspace profiles keyed by kind; an
// empty map when it has none.
type WorkspaceProfiles struct {
	RepositoryID string                      `json:"repository_id" yaml:"repository_id"`
	Profiles     map[string]WorkspaceProfile `json:"profiles" yaml:"profiles"`
}

// Kinds answers the kinds the repository has a profile for.
func (profiles *WorkspaceProfiles) Kinds() []string {
	if profiles == nil {
		return nil
	}
	kinds := make([]string, 0, len(profiles.Profiles))
	for kind := range profiles.Profiles {
		kinds = append(kinds, kind)
	}
	return kinds
}

// GetWorkspaceProfiles reads a connected pipeline repository's workspace
// profiles. A refusal is the workspace routes' own error: ErrUnauthorized, a
// *PermissionDeniedError without workspaces.manage, a *WorkspaceAPIError
// otherwise (404 for a repository that is not connected).
func (c *Client) GetWorkspaceProfiles(ctx context.Context, repositoryID string) (*WorkspaceProfiles, error) {
	endpoint := c.BaseURL + "/api/v1/org/pipeline-repositories/" + neturl.PathEscape(repositoryID) +
		"/workspace-profiles"
	var profiles WorkspaceProfiles
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodGet, endpoint, nil, &profiles); requestError != nil {
		return nil, requestError
	}
	return &profiles, nil
}
