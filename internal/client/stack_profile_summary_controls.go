package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ProfileSummaryAdmissionRequest preserves the identity and expectations of one
// deliberate generation request. Retrying must reuse the complete request.
type ProfileSummaryAdmissionRequest struct {
	ExpectedSourceRevision  int    `json:"expected_source_revision"`
	ExpectedSummaryRevision int    `json:"expected_summary_revision"`
	IdempotencyKey          string `json:"idempotency_key"`
}

// ProfileSummaryPolicyLimits are explicit ceilings, not suggested defaults.
// Costs use integer millionths of a US dollar.
type ProfileSummaryPolicyLimits struct {
	ContentRetentionDays  int   `json:"content_retention_days"`
	MetadataRetentionDays int   `json:"metadata_retention_days"`
	DailyTokenLimit       int64 `json:"daily_token_limit"`
	DailyCostMicrousd     int64 `json:"daily_cost_microusd"`
	RequestTokenLimit     int64 `json:"request_token_limit"`
	RequestCostMicrousd   int64 `json:"request_cost_microusd"`
}

type ProfileSummaryPolicyApprovalRequest struct {
	ExpectedRevision      int64                      `json:"expected_revision"`
	ExpectedConsentDigest string                     `json:"expected_consent_digest"`
	AcceptDataUse         bool                       `json:"accept_data_use"`
	ConfirmProviderTerms  bool                       `json:"confirm_provider_terms"`
	Limits                ProfileSummaryPolicyLimits `json:"limits"`
	OpenRouterRouting     *ProfileSummaryRouting     `json:"openrouter_routing,omitempty"`
}

type ProfileSummaryPolicyRevocationRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

func (client *Client) GetProfileSummaryStatus(ctx context.Context, profileID string, version int) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryPath(profileID, version)+"-generation", nil)
}

func (client *Client) GetDraftSummaryStatus(ctx context.Context, draftID string, revision int) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, draftSummaryGenerationPath(draftID)+"?expected_source_revision="+strconv.Itoa(revision), nil)
}

func (client *Client) GenerateProfileSummary(ctx context.Context, profileID string, version int, request ProfileSummaryAdmissionRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, profileSummaryPath(profileID, version)+"-generation", request)
}

func (client *Client) GenerateDraftSummary(ctx context.Context, draftID string, request ProfileSummaryAdmissionRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, draftSummaryGenerationPath(draftID), request)
}

func (client *Client) CancelProfileSummaryGeneration(ctx context.Context, generationID string) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, profileSummaryGenerationPath(generationID)+"/cancel", struct{}{})
}

func (client *Client) GetProfileSummaryPolicy(ctx context.Context) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryPolicyPath, nil)
}

func (client *Client) ApproveProfileSummaryPolicy(ctx context.Context, request ProfileSummaryPolicyApprovalRequest) (json.RawMessage, error) {
	if request.OpenRouterRouting != nil && !request.OpenRouterRouting.Valid() {
		return nil, fmt.Errorf("invalid summary routing")
	}
	return client.profileSummaryRequest(ctx, http.MethodPut, profileSummaryPolicyPath, request)
}

func (client *Client) RevokeProfileSummaryPolicy(ctx context.Context, request ProfileSummaryPolicyRevocationRequest) (json.RawMessage, error) {
	return client.profileSummaryRequest(ctx, http.MethodPost, profileSummaryPolicyPath+"/revoke", request)
}

const profileSummaryPolicyPath = stackProfilesAPIPath + "/summary-generation-policy"

func draftSummaryGenerationPath(draftID string) string {
	return stackProfilesAPIPath + "/drafts/" + url.PathEscape(draftID) + "/summary-generation"
}
