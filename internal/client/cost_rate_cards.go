package client

// Private-capacity rate cards, cloud-waste cleanup and the cluster what-if
// estimate (ankra-hq41xb.23). Every answer is returned as the platform sent
// it.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	neturl "net/url"
	"strconv"
)

const (
	costRateCardsPath = "/api/v1/org/cloud-cost/rate-cards"
	cloudWastePath    = "/api/v1/org/cloud-cost/waste"

	// CostRateCardOrganisation names the organisation's fallback rate card
	// where a credential id is expected.
	CostRateCardOrganisation = "organisation"
)

// ListCostRateCards reads the private credentials with the rates that price
// them, the organisation fallback card and the provider defaults.
// GET /api/v1/org/cloud-cost/rate-cards
func (c *Client) ListCostRateCards(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, costRateCardsPath, nil)
}

// CostRateCardRequest states the monthly rates of one private credential,
// or of the organisation fallback card when CredentialID is
// CostRateCardOrganisation. Amounts are decimal strings; one left empty is
// not sent.
type CostRateCardRequest struct {
	CredentialID           string `json:"credential_id"`
	Currency               string `json:"currency"`
	VCPUMonthlyAmount      string `json:"vcpu_monthly_amount,omitempty"`
	MemoryGBMonthlyAmount  string `json:"memory_gb_monthly_amount,omitempty"`
	StorageGBMonthlyAmount string `json:"storage_gb_monthly_amount,omitempty"`
}

// SetCostRateCard writes a rate card.
// PUT /api/v1/org/cloud-cost/rate-cards
func (c *Client) SetCostRateCard(ctx context.Context, request CostRateCardRequest) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, costRateCardsPath, request)
}

// DeleteCostRateCard clears a stated card, so its capacity falls back to
// the provider default.
// DELETE /api/v1/org/cloud-cost/rate-cards/{credential_id}
func (c *Client) DeleteCostRateCard(ctx context.Context, credentialID string) error {
	return c.sendJSONContext(ctx, http.MethodDelete, c.BaseURL+costRateCardsPath+"/"+neturl.PathEscape(credentialID), nil, nil)
}

// CloudWasteResolveRequest carries what a cleanup that cannot be recovered
// needs: the written consent and, for a volume, the age it must have been
// seen unattached. FindingIDs is sent by the batch route only.
type CloudWasteResolveRequest struct {
	FindingIDs        []string `json:"finding_ids,omitempty"`
	Consent           string   `json:"consent,omitempty"`
	MinUnattachedDays *int     `json:"min_unattached_days,omitempty"`
}

// ResolveCloudWasteFinding starts the cleanup of one open cloud-waste
// finding and answers the operation that does it.
// POST /api/v1/org/cloud-cost/waste/{finding_id}/resolve
func (c *Client) ResolveCloudWasteFinding(ctx context.Context, findingID string, request CloudWasteResolveRequest) (json.RawMessage, error) {
	single := CloudWasteResolveRequest{Consent: request.Consent, MinUnattachedDays: request.MinUnattachedDays}
	return c.requestDocument(ctx, http.MethodPost, cloudWastePath+"/"+neturl.PathEscape(findingID)+"/resolve", single)
}

// ErrCloudWasteNoneStarted is what ResolveCloudWasteBatch returns, together
// with the outcome document, when no cleanup in the batch started.
var ErrCloudWasteNoneStarted = errors.New("no cloud-waste cleanup started")

// ResolveCloudWasteBatch starts the cleanup of up to 100 findings, one
// operation each, and answers every finding's outcome. The platform answers
// 409 when none started, with the same outcome document as its body; that
// document comes back with ErrCloudWasteNoneStarted, so the caller can show
// each finding's reason.
// POST /api/v1/org/cloud-cost/waste/resolve-batch
func (c *Client) ResolveCloudWasteBatch(ctx context.Context, request CloudWasteResolveRequest) (json.RawMessage, error) {
	document, requestError := c.requestDocument(ctx, http.MethodPost, cloudWastePath+"/resolve-batch", request)
	var unexpected *UnexpectedResponseError
	if errors.As(requestError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
		var outcome struct {
			Results []json.RawMessage `json:"results"`
		}
		if json.Unmarshal(unexpected.body, &outcome) == nil && len(outcome.Results) > 0 {
			return json.RawMessage(unexpected.body), ErrCloudWasteNoneStarted
		}
	}
	return document, requestError
}

// GetResolvedCloudWaste totals what Ankra's cleanup resolved in the last
// days days (the platform's default when zero), under the cost project
// scope.
// GET /api/v1/org/cloud-cost/waste/resolved
func (c *Client) GetResolvedCloudWaste(ctx context.Context, days int) (json.RawMessage, error) {
	query := neturl.Values{}
	if days > 0 {
		query.Set("days", strconv.Itoa(days))
	}
	return c.requestDocument(ctx, http.MethodGet, c.costScopedURL(pathWithQuery(cloudWastePath+"/resolved", query)), nil)
}

func clusterCostEstimatePath(clusterID string) string {
	return "/api/v1/org/clusters/" + neturl.PathEscape(clusterID) + "/cost/estimate"
}

// GetClusterCostEstimate prices what the cluster runs today.
// GET /api/v1/org/clusters/{cluster_id}/cost/estimate
func (c *Client) GetClusterCostEstimate(ctx context.Context, clusterID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, clusterCostEstimatePath(clusterID), nil)
}

// EstimateClusterCost prices a draft configuration beside the cluster's
// current one. Nothing is stored.
// POST /api/v1/org/clusters/{cluster_id}/cost/estimate
func (c *Client) EstimateClusterCost(ctx context.Context, clusterID string, draft map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, clusterCostEstimatePath(clusterID), draft)
}
