package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// ProfileSummaryOutcome selects trusted facts. It cannot carry new evidence or prose.
type ProfileSummaryOutcome struct {
	Kind    string   `json:"kind"`
	FactIDs []string `json:"fact_ids"`
}

// ProfileSummaryCandidateRequest pins the candidate the author inspected.
type ProfileSummaryCandidateRequest struct {
	ExpectedCandidateRevision int                     `json:"expected_candidate_revision"`
	Outcomes                  []ProfileSummaryOutcome `json:"outcomes"`
}

// ProfileSummaryReviewRequest binds a decision to three independent revisions.
// Zero is a meaningful initial approval revision and must remain on the wire.
type ProfileSummaryReviewRequest struct {
	ExpectedSourceRevision    int    `json:"expected_source_revision"`
	ExpectedCandidateRevision int    `json:"expected_candidate_revision"`
	ExpectedApprovedRevision  int    `json:"expected_approved_revision"`
	Decision                  string `json:"decision"`
}

// ProfileSummaryWithdrawalRequest hides a publication without erasing history.
type ProfileSummaryWithdrawalRequest struct {
	ExpectedSummaryRevision int    `json:"expected_summary_revision"`
	Reason                  string `json:"reason"`
}

// GetProfileSummary returns the exact version's approved summary or explicit absence.
func (client *Client) GetProfileSummary(ctx context.Context, profileID string, version int) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryPath(profileID, version), nil)
}

// GetProfileSummaryEvidence reads only evidence selected by the current approval.
func (client *Client) GetProfileSummaryEvidence(ctx context.Context, profileID string, version int, factID string) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryPath(profileID, version)+"/evidence/"+url.PathEscape(factID), nil)
}

// GetProfileSummaryGeneration reads private candidate and revision information.
func (client *Client) GetProfileSummaryGeneration(ctx context.Context, generationID string) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryGenerationPath(generationID), nil)
}

// EditProfileSummaryCandidate edits the selection without changing its publication.
func (client *Client) EditProfileSummaryCandidate(ctx context.Context, generationID string, request ProfileSummaryCandidateRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPut, profileSummaryGenerationPath(generationID)+"/candidate", request)
}

// ReviewProfileSummary records a human approval or an author's rejection.
func (client *Client) ReviewProfileSummary(ctx context.Context, generationID string, request ProfileSummaryReviewRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, profileSummaryGenerationPath(generationID)+"/review", request)
}

// WithdrawProfileSummary hides the current version approval with optimistic concurrency.
func (client *Client) WithdrawProfileSummary(ctx context.Context, profileID string, version int, request ProfileSummaryWithdrawalRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, profileSummaryPath(profileID, version)+"/withdraw", request)
}

func profileSummaryPath(profileID string, version int) string {
	return stackProfilePath(profileID, "/versions/"+strconv.Itoa(version)+"/summary")
}

func profileSummaryGenerationPath(generationID string) string {
	return stackProfilesAPIPath + "/summary-generations/" + url.PathEscape(generationID)
}

// profileSummaryRequest preserves summary authorization refusals as exit-7 RBAC
// errors. This namespace uses literal 403 sentences, not permission_denied JSON.
func (client *Client) profileSummaryRequest(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	response, requestError := client.stackProfileResourceRequest(ctx, method, path, nil, body)
	var refusal *UnexpectedResponseError
	if errors.As(requestError, &refusal) && refusal.StatusCode == http.StatusForbidden {
		return nil, &PermissionDeniedError{Detail: refusal.Error()}
	}
	return response, requestError
}
