package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
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
	// Reads and uncertain writes have a bounded total duration. The shared retry
	// transport retries only bodyless reads, never admission or policy decisions.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var content io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal summary request: %w", err)
		}
		content = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, content)
	if err != nil {
		return nil, fmt.Errorf("create summary request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("summary request failed: %w", err)
	}
	defer closeBody(response)
	const maximum = 1 << 20
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read summary response: %w", err)
	}
	if len(payload) > maximum {
		return nil, fmt.Errorf("summary response exceeded the size limit; read back before retrying a write")
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return nil, &PermissionDeniedError{Detail: "Permission to perform this summary operation was denied."}
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusAccepted {
		return nil, newUnexpectedResponseError(summaryRefusalMessage(response.StatusCode, payload), response.StatusCode, "")
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("summary response was invalid; read back before retrying a write")
	}
	return json.RawMessage(payload), nil
}

// Only closed server codes and HTTP classes enter diagnostics, never provider
// text or a private candidate accidentally included in an error body.
func summaryRefusalMessage(status int, payload []byte) string {
	var refusal struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(payload, &refusal)
	switch refusal.Code {
	case "generation_unavailable":
		return "Profile summary generation is unavailable; existing summaries and review controls remain available."
	case "route_unavailable":
		return "The summary provider route is unavailable; inspect summary policy get and the organisation model settings."
	case "outcome_unknown":
		return "The write outcome is unknown. Read status before deciding what to do; retry generation only with the original request key and revisions."
	}
	switch status {
	case http.StatusNotFound:
		return "The requested summary source or generation was not found."
	case http.StatusConflict:
		return "The source, revision, request key or consent conflicts with current state. Inspect status and policy; do not replace an uncertain generation request with a new key."
	case http.StatusGone:
		return "This summary content or request identity has expired. Inspect current source status before starting new work."
	case http.StatusUnprocessableEntity:
		return "The platform refused the summary request as invalid. Check the supplied identities, revisions and limits."
	case http.StatusTooManyRequests:
		return "The summary generation budget is exhausted; inspect the organisation policy and usage before requesting more work."
	default:
		return "Profile summary operation could not be confirmed; read back before retrying a write and retain the original generation request key."
	}
}
