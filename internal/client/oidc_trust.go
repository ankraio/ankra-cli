package client

// GitHub Actions OIDC trust policies and the token exchange (ankra-4pigj8):
// /api/v1/org/organisation/oidc-trust-policies and
// /api/v1/oidc/github/token.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// GitHubOIDCAudience is the audience a workflow requests for its ID token.
const GitHubOIDCAudience = "https://ankra.ai/oidc/github"

// OIDCTrustPolicy mirrors one policy row.
type OIDCTrustPolicy struct {
	ID                string   `json:"id" yaml:"id"`
	Name              string   `json:"name" yaml:"name"`
	RepositoryOwner   string   `json:"repository_owner" yaml:"repository_owner"`
	RepositoryName    string   `json:"repository_name" yaml:"repository_name"`
	RepositoryOwnerID int64    `json:"repository_owner_id" yaml:"repository_owner_id"`
	RepositoryID      int64    `json:"repository_id" yaml:"repository_id"`
	Ref               *string  `json:"ref" yaml:"ref"`
	Environment       *string  `json:"environment" yaml:"environment"`
	JobWorkflowRef    *string  `json:"job_workflow_ref" yaml:"job_workflow_ref"`
	AllowPullRequest  bool     `json:"allow_pull_request" yaml:"allow_pull_request"`
	RoleSlug          string   `json:"role_slug" yaml:"role_slug"`
	Scopes            []string `json:"scopes" yaml:"scopes"`
	TTLSeconds        int      `json:"ttl_seconds" yaml:"ttl_seconds"`
	ServiceAccountID  string   `json:"service_account_id" yaml:"service_account_id"`
	CreatedAt         string   `json:"created_at" yaml:"created_at"`
}

// OIDCTrustPolicyRequest is the create payload; nil pointers are omitted.
type OIDCTrustPolicyRequest struct {
	Name              string   `json:"name"`
	RepositoryOwner   string   `json:"repository_owner"`
	RepositoryName    string   `json:"repository_name"`
	RepositoryOwnerID int64    `json:"repository_owner_id"`
	RepositoryID      int64    `json:"repository_id"`
	Ref               *string  `json:"ref,omitempty"`
	Environment       *string  `json:"environment,omitempty"`
	JobWorkflowRef    *string  `json:"job_workflow_ref,omitempty"`
	AllowPullRequest  bool     `json:"allow_pull_request"`
	RoleSlug          string   `json:"role_slug"`
	Scopes            []string `json:"scopes,omitempty"`
	TTLSeconds        int      `json:"ttl_seconds,omitempty"`
}

const oidcTrustPath = "/api/v1/org/organisation/oidc-trust-policies"

func (c *Client) doOIDCTrustRequest(ctx context.Context, method string, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)
	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	// Any 2xx is success: a DELETE may answer 204 with no body.
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return responseBody, nil
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		return nil, &PermissionDeniedError{Detail: oidcDetail(responseBody)}
	default:
		if detail := oidcDetail(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("oidc trust request failed",
			response.StatusCode, redactedBodyForError(responseBody, 500))
	}
}

func oidcDetail(body []byte) string {
	var parsed struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return parsed.Detail
	}
	return ""
}

// ListOIDCTrustPolicies lists the organisation's policies.
func (c *Client) ListOIDCTrustPolicies(ctx context.Context) ([]OIDCTrustPolicy, error) {
	body, requestError := c.doOIDCTrustRequest(ctx, http.MethodGet, oidcTrustPath, nil)
	if requestError != nil {
		return nil, requestError
	}
	var parsed struct {
		Policies []OIDCTrustPolicy `json:"policies"`
	}
	if decodeError := json.Unmarshal(body, &parsed); decodeError != nil {
		return nil, fmt.Errorf("decode response: %w", decodeError)
	}
	return parsed.Policies, nil
}

// CreateOIDCTrustPolicy creates one policy.
func (c *Client) CreateOIDCTrustPolicy(ctx context.Context, request OIDCTrustPolicyRequest) (*OIDCTrustPolicy, error) {
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, requestError := c.doOIDCTrustRequest(ctx, http.MethodPost, oidcTrustPath, encoded)
	if requestError != nil {
		return nil, requestError
	}
	var policy OIDCTrustPolicy
	if decodeError := json.Unmarshal(body, &policy); decodeError != nil {
		return nil, fmt.Errorf("decode response: %w", decodeError)
	}
	return &policy, nil
}

// DeleteOIDCTrustPolicy deletes a policy and revokes the tokens it minted.
func (c *Client) DeleteOIDCTrustPolicy(ctx context.Context, policyID string) error {
	_, requestError := c.doOIDCTrustRequest(ctx, http.MethodDelete, oidcTrustPath+"/"+url.PathEscape(policyID), nil)
	return requestError
}

// GitHubOIDCExchangeResult is the RFC 8693 response. The token is a secret.
type GitHubOIDCExchangeResult struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	TokenID     string `json:"token_id"`
}

// ExchangeGitHubOIDCToken trades a GitHub Actions ID token for an Ankra
// token. It is unauthenticated: the subject token is the credential. The
// platform answers every refusal with the same generic 401, so the error here
// points at the audit log rather than guessing a cause.
func ExchangeGitHubOIDCToken(ctx context.Context, httpClient *http.Client, baseURL string,
	organisationID string, subjectToken string) (*GitHubOIDCExchangeResult, error) {
	encoded, marshalError := json.Marshal(map[string]string{
		"grant_type":         "urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":      subjectToken,
		"subject_token_type": "urn:ietf:params:oauth:token-type:id_token",
		"organisation_id":    organisationID,
	})
	if marshalError != nil {
		return nil, marshalError
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/v1/oidc/github/token", bytes.NewReader(encoded))
	if requestError != nil {
		return nil, requestError
	}
	request.Header.Set("Content-Type", "application/json")
	response, sendError := httpClient.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", sendError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	switch response.StatusCode {
	case http.StatusOK:
		var result GitHubOIDCExchangeResult
		if decodeError := json.Unmarshal(body, &result); decodeError != nil || result.AccessToken == "" {
			return nil, fmt.Errorf("the platform answered 200 without an access token")
		}
		return &result, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("the platform refused the GitHub ID token: no trust policy in this organisation " +
			"matches this repository, ref, environment and event (the organisation's audit log records the reason)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("the token exchange is rate limited; retry shortly")
	default:
		return nil, fmt.Errorf("token exchange failed with status %d", response.StatusCode)
	}
}
