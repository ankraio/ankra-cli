package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// SCMRepositoryRule is one repository's explicit AI review rule on an SCM
// binding (/api/v1/org/ai-gateway/scm-bindings, repo_overrides). The three
// switches are always stored together: the rule replaces the binding's
// values wholesale for that repository. ReviewModel "" inherits the
// binding's model; ReviewDrafts and MaxReviewsPerPR are the resolved
// pull-request lane values the review will use.
type SCMRepositoryRule struct {
	RepoFullName    string `json:"repo_full_name" yaml:"repo_full_name"`
	AIReview        bool   `json:"ai_review" yaml:"ai_review"`
	MentionReplies  bool   `json:"mention_replies" yaml:"mention_replies"`
	PRPreviews      bool   `json:"pr_previews" yaml:"pr_previews"`
	ReviewModel     string `json:"review_model" yaml:"review_model"`
	ReviewDrafts    bool   `json:"review_drafts" yaml:"review_drafts"`
	MaxReviewsPerPR int    `json:"max_reviews_per_pr" yaml:"max_reviews_per_pr"`
}

// SCMBinding is one connected source-control integration (a GitHub App
// installation, a GitLab or Bitbucket credential) with its binding-level AI
// review settings and its per-repository rules. For GitHub, DisplayName is
// the installation's account login, which is the owner of its repositories.
type SCMBinding struct {
	Provider          string              `json:"provider" yaml:"provider"`
	BindingExternalID string              `json:"binding_external_id" yaml:"binding_external_id"`
	DisplayName       string              `json:"display_name" yaml:"display_name"`
	Detail            string              `json:"detail" yaml:"detail"`
	AIReview          bool                `json:"ai_review" yaml:"ai_review"`
	MentionReplies    bool                `json:"mention_replies" yaml:"mention_replies"`
	PRPreviews        bool                `json:"pr_previews" yaml:"pr_previews"`
	MergeWhenGreen    bool                `json:"merge_when_green" yaml:"merge_when_green"`
	ReviewModel       string              `json:"review_model" yaml:"review_model"`
	ReviewDrafts      bool                `json:"review_drafts" yaml:"review_drafts"`
	MaxReviewsPerPR   int                 `json:"max_reviews_per_pr" yaml:"max_reviews_per_pr"`
	RepoOverrides     []SCMRepositoryRule `json:"repo_overrides" yaml:"repo_overrides"`
}

// SCMRepositoryRuleWrite is the body of a repo-overrides PUT. The platform
// requires the three switches on every call; the optional fields are left
// untouched on the stored rule when nil.
type SCMRepositoryRuleWrite struct {
	RepoFullName    string  `json:"repo_full_name"`
	AIReview        bool    `json:"ai_review"`
	MentionReplies  bool    `json:"mention_replies"`
	PRPreviews      bool    `json:"pr_previews"`
	ReviewModel     *string `json:"review_model,omitempty"`
	ReviewDrafts    *bool   `json:"review_drafts,omitempty"`
	MaxReviewsPerPR *int    `json:"max_reviews_per_pr,omitempty"`
}

// SCMRepositoryRuleDeleted is the answer of a repo-overrides DELETE.
// Removed is false when the repository had no rule to remove.
type SCMRepositoryRuleDeleted struct {
	RepoFullName string `json:"repo_full_name" yaml:"repo_full_name"`
	Removed      bool   `json:"removed" yaml:"removed"`
}

// SCMBindingRepository is one repository a GitHub App installation reaches.
type SCMBindingRepository struct {
	RepoFullName string `json:"repo_full_name" yaml:"repo_full_name"`
	Name         string `json:"name" yaml:"name"`
}

type scmBindingListBody struct {
	Bindings *[]SCMBinding `json:"bindings"`
}

type scmBindingRepositoriesBody struct {
	Repositories *[]SCMBindingRepository `json:"repositories"`
}

func scmBindingURL(baseURL string, provider string, bindingExternalID string, suffix string) string {
	return fmt.Sprintf("%s/api/v1/org/ai-gateway/scm-bindings/%s/%s/%s", baseURL,
		neturl.PathEscape(provider), neturl.PathEscape(bindingExternalID), suffix)
}

// ListSCMBindings lists the organisation's source-control bindings with
// their AI review settings and per-repository rules.
func (c *Client) ListSCMBindings(ctx context.Context) ([]SCMBinding, error) {
	var body scmBindingListBody
	if listError := c.sendJSONContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/org/ai-gateway/scm-bindings", c.BaseURL), nil, &body); listError != nil {
		return nil, listError
	}
	if body.Bindings == nil {
		return nil, errors.New("the platform's answer did not include the bindings list")
	}
	return *body.Bindings, nil
}

// PutSCMRepositoryRule writes one repository's rule on a binding. The
// platform requires organisation admin.
func (c *Client) PutSCMRepositoryRule(ctx context.Context, provider string, bindingExternalID string,
	rule SCMRepositoryRuleWrite) (*SCMRepositoryRule, error) {
	var stored SCMRepositoryRule
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		scmBindingURL(c.BaseURL, provider, bindingExternalID, "repo-overrides"), rule, &stored); putError != nil {
		return nil, putError
	}
	return &stored, nil
}

// DeleteSCMRepositoryRule removes one repository's rule, reverting it to the
// binding-level settings. The platform requires organisation admin.
func (c *Client) DeleteSCMRepositoryRule(ctx context.Context, provider string, bindingExternalID string,
	repoFullName string) (*SCMRepositoryRuleDeleted, error) {
	query := neturl.Values{}
	query.Set("repo_full_name", repoFullName)
	var deleted SCMRepositoryRuleDeleted
	if deleteError := c.sendJSONContext(ctx, http.MethodDelete,
		scmBindingURL(c.BaseURL, provider, bindingExternalID, "repo-overrides")+"?"+query.Encode(),
		nil, &deleted); deleteError != nil {
		return nil, deleteError
	}
	return &deleted, nil
}

// ListSCMBindingRepositories lists the repositories a GitHub App
// installation binding reaches. Other providers answer 404.
func (c *Client) ListSCMBindingRepositories(ctx context.Context, provider string,
	bindingExternalID string) ([]SCMBindingRepository, error) {
	var body scmBindingRepositoriesBody
	if listError := c.sendJSONContext(ctx, http.MethodGet,
		scmBindingURL(c.BaseURL, provider, bindingExternalID, "repos"), nil, &body); listError != nil {
		return nil, listError
	}
	if body.Repositories == nil {
		return nil, errors.New("the platform's answer did not include the repositories list")
	}
	return *body.Repositories, nil
}
