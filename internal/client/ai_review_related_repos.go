package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// relatedRepositoriesProvider is the only provider relationships are stated
// for. The review's cross-repository pass reads related repositories through
// GitHub code search, so a relationship under another provider would be
// stored and never read.
const relatedRepositoriesProvider = "github"

// RelatedRepository is one relationship an organisation admin stated between
// two repositories for AI code review (/api/v1/org/ai-gateway/related-repos).
// The relationship has no direction - a review of either repository may read
// the other - so which name sits in RepoFullName is only the order it was
// stated in.
type RelatedRepository struct {
	ID                  string `json:"id" yaml:"id"`
	Provider            string `json:"provider" yaml:"provider"`
	RepoFullName        string `json:"repo_full_name" yaml:"repo_full_name"`
	RelatedRepoFullName string `json:"related_repo_full_name" yaml:"related_repo_full_name"`
}

// relatedRepositoryListBody decodes the listing. RelatedRepos is a pointer so
// an answer that does not carry the list at all is told apart from one that
// carries an empty list: only the second means nothing is stated.
type relatedRepositoryListBody struct {
	RelatedRepos *[]RelatedRepository `json:"related_repos"`
}

type createRelatedRepositoryBody struct {
	Provider            string `json:"provider"`
	BindingExternalID   string `json:"binding_external_id"`
	RepoFullName        string `json:"repo_full_name"`
	RelatedRepoFullName string `json:"related_repo_full_name"`
}

// ListRelatedRepositories lists the relationships stated under one GitHub App
// installation, identified by its numeric installation id.
func (c *Client) ListRelatedRepositories(ctx context.Context, installationID string) ([]RelatedRepository, error) {
	query := neturl.Values{}
	query.Set("provider", relatedRepositoriesProvider)
	query.Set("binding_external_id", installationID)
	requestURL := fmt.Sprintf("%s/api/v1/org/ai-gateway/related-repos?%s", c.BaseURL, query.Encode())

	var body relatedRepositoryListBody
	if listError := c.sendJSONContext(ctx, http.MethodGet, requestURL, nil, &body); listError != nil {
		return nil, listError
	}
	if body.RelatedRepos == nil {
		return nil, errors.New("the platform's answer did not include the related repositories list")
	}
	return *body.RelatedRepos, nil
}

// CreateRelatedRepository states that two repositories are related under one
// GitHub App installation. The platform checks with GitHub that the
// installation reaches both before it stores the pair, and answers the stored
// row.
func (c *Client) CreateRelatedRepository(ctx context.Context, installationID string,
	repoFullName string, relatedRepoFullName string) (*RelatedRepository, error) {
	request := createRelatedRepositoryBody{
		Provider:            relatedRepositoriesProvider,
		BindingExternalID:   installationID,
		RepoFullName:        repoFullName,
		RelatedRepoFullName: relatedRepoFullName,
	}
	var created RelatedRepository
	if createError := c.sendJSONContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/org/ai-gateway/related-repos", c.BaseURL), request, &created); createError != nil {
		return nil, createError
	}
	return &created, nil
}

// DeleteRelatedRepository removes one stated relationship by its id. The
// platform answers 204 with no body, and 404 for an id it does not hold.
func (c *Client) DeleteRelatedRepository(ctx context.Context, relatedRepositoryID string) error {
	return c.sendJSONContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/org/ai-gateway/related-repos/%s", c.BaseURL, neturl.PathEscape(relatedRepositoryID)),
		nil, nil)
}
