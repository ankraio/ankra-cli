package client

// Cluster access policy proposals (ankra-gsjnfb, PLA-878). The organisation
// cluster access policy is human-only: no bearer credential can change it.
// A token holding kube_access.manage may PROPOSE the policy it wants, and an
// owner or admin APPLIES it from a signed-in browser session through the
// proposal's apply_url. The client only proposes and reads; there is no
// apply or dismiss call here, by design.

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

// Proposal statuses the platform answers. A pending proposal past its
// expiry already reads as expired.
const (
	ProposalStatusPending    = "pending"
	ProposalStatusApplied    = "applied"
	ProposalStatusDismissed  = "dismissed"
	ProposalStatusExpired    = "expired"
	ProposalStatusSuperseded = "superseded"
)

// ProposeClusterAccessPolicyRequest is the body of a proposal. A nil role
// keeps the live role and an unset bound keeps the live bound; a Clear flag
// sends an explicit null, which clears that bound. Reason is required.
type ProposeClusterAccessPolicyRequest struct {
	CreatorGrantRole      *string
	MaxGrantRole          *string
	ElevatedMaxTTLSeconds *int64
	// ClearElevatedMaxTTL proposes no elevated lifetime bound.
	ClearElevatedMaxTTL   bool
	RequireReasonFromRole *string
	// ClearRequireReasonFromRole proposes that no grant needs a reason
	// (break-glass still does).
	ClearRequireReasonFromRole bool
	Reason                     string
}

// MarshalJSON writes only the fields the request sets, so an omitted field
// keeps the live value, and writes null for a cleared bound.
func (request ProposeClusterAccessPolicyRequest) MarshalJSON() ([]byte, error) {
	body := map[string]any{"reason": request.Reason}
	if request.CreatorGrantRole != nil {
		body["creator_grant_role"] = *request.CreatorGrantRole
	}
	if request.MaxGrantRole != nil {
		body["max_grant_role"] = *request.MaxGrantRole
	}
	switch {
	case request.ClearElevatedMaxTTL:
		body["elevated_max_ttl_seconds"] = nil
	case request.ElevatedMaxTTLSeconds != nil:
		body["elevated_max_ttl_seconds"] = *request.ElevatedMaxTTLSeconds
	}
	switch {
	case request.ClearRequireReasonFromRole:
		body["require_reason_from_role"] = nil
	case request.RequireReasonFromRole != nil:
		body["require_reason_from_role"] = *request.RequireReasonFromRole
	}
	return json.Marshal(body)
}

// ProposedClusterAccessPolicy is the whole policy a proposal would write.
type ProposedClusterAccessPolicy struct {
	CreatorGrantRole      string  `json:"creator_grant_role" yaml:"creator_grant_role"`
	MaxGrantRole          string  `json:"max_grant_role" yaml:"max_grant_role"`
	ElevatedMaxTTLSeconds *int64  `json:"elevated_max_ttl_seconds" yaml:"elevated_max_ttl_seconds"`
	RequireReasonFromRole *string `json:"require_reason_from_role" yaml:"require_reason_from_role"`
}

// ClusterAccessPolicyProposer names who proposed: the label a person reads
// and, when a token carried the request, that token.
type ClusterAccessPolicyProposer struct {
	UserID    *string `json:"user_id" yaml:"user_id"`
	Principal string  `json:"principal" yaml:"principal"`
	TokenID   *string `json:"token_id" yaml:"token_id"`
	TokenName *string `json:"token_name" yaml:"token_name"`
}

// ClusterAccessPolicyProposal is one proposal. Base is the live policy it
// was made against, Current the live policy when it was read; when
// BaseMatchesCurrent is false the policy moved since, and applying it is
// refused (propose again). ApplyURL is the page an owner or admin applies
// it from, signed in. The organisation is the one the proposal belongs to.
type ClusterAccessPolicyProposal struct {
	ID                 string                      `json:"id" yaml:"id"`
	Status             string                      `json:"status" yaml:"status"`
	Proposed           ProposedClusterAccessPolicy `json:"proposed" yaml:"proposed"`
	Base               ClusterAccessPolicy         `json:"base" yaml:"base"`
	Current            ClusterAccessPolicy         `json:"current" yaml:"current"`
	BaseMatchesCurrent bool                        `json:"base_matches_current" yaml:"base_matches_current"`
	Reason             string                      `json:"reason" yaml:"reason"`
	ProposedBy         ClusterAccessPolicyProposer `json:"proposed_by" yaml:"proposed_by"`
	CreatedAt          string                      `json:"created_at" yaml:"created_at"`
	ExpiresAt          string                      `json:"expires_at" yaml:"expires_at"`
	DecidedByUserID    *string                     `json:"decided_by_user_id" yaml:"decided_by_user_id"`
	DecidedAt          *string                     `json:"decided_at" yaml:"decided_at"`
	DecisionNote       *string                     `json:"decision_note" yaml:"decision_note"`
	ApplyURL           string                      `json:"apply_url" yaml:"apply_url"`

	ClusterAccessPolicyOrganisation `yaml:",inline"`
}

// IsFinal reports whether the proposal can no longer change status.
func (proposal ClusterAccessPolicyProposal) IsFinal() bool {
	return proposal.Status != ProposalStatusPending
}

// ClusterAccessPolicyProposalList is a proposal listing, newest first.
// IsCapped reports that more proposals matched than were returned. The
// organisation is the one that was listed.
type ClusterAccessPolicyProposalList struct {
	Proposals []ClusterAccessPolicyProposal `json:"proposals" yaml:"proposals"`
	IsCapped  bool                          `json:"is_capped" yaml:"is_capped"`

	ClusterAccessPolicyOrganisation `yaml:",inline"`
}

// ClusterAccessPolicyProposalRejectedError is a proposal the platform
// refused as invalid (422): an unknown role, a bound out of range, a
// creator role above the ceiling, a reason that says nothing, or a proposal
// identical to the live policy. Changing the invocation is the fix.
type ClusterAccessPolicyProposalRejectedError struct {
	response *UnexpectedResponseError
}

func (e *ClusterAccessPolicyProposalRejectedError) Error() string { return e.response.Error() }

func (e *ClusterAccessPolicyProposalRejectedError) Unwrap() error { return e.response }

// ProposeClusterAccessPolicy records a proposal for the caller's
// organisation. It never changes the policy.
func (c *Client) ProposeClusterAccessPolicy(ctx context.Context, request ProposeClusterAccessPolicyRequest) (*ClusterAccessPolicyProposal, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/org/cluster-access-policy/proposals", c.BaseURL)
	body, statusCode, err := c.doClusterAccessPolicyProposalRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK {
		return nil, proposalResponseError("propose cluster access policy failed", statusCode, body)
	}
	var proposal ClusterAccessPolicyProposal
	if err := parseJSON(body, &proposal); err != nil {
		return nil, err
	}
	return &proposal, nil
}

// GetClusterAccessPolicyProposal reads one proposal of the caller's
// organisation.
func (c *Client) GetClusterAccessPolicyProposal(ctx context.Context, proposalID string) (*ClusterAccessPolicyProposal, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/cluster-access-policy/proposals/%s", c.BaseURL, url.PathEscape(proposalID))
	body, statusCode, err := c.doClusterAccessPolicyProposalRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, proposalResponseError("read cluster access policy proposal failed", statusCode, body)
	}
	var proposal ClusterAccessPolicyProposal
	if err := parseJSON(body, &proposal); err != nil {
		return nil, err
	}
	return &proposal, nil
}

// ListClusterAccessPolicyProposals lists the organisation's proposals,
// newest first. An empty status lists every status.
func (c *Client) ListClusterAccessPolicyProposals(ctx context.Context, status string) (*ClusterAccessPolicyProposalList, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/cluster-access-policy/proposals", c.BaseURL)
	if status != "" {
		endpoint += "?status=" + url.QueryEscape(status)
	}
	body, statusCode, err := c.doClusterAccessPolicyProposalRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, proposalResponseError("list cluster access policy proposals failed", statusCode, body)
	}
	var list ClusterAccessPolicyProposalList
	if err := parseJSON(body, &list); err != nil {
		return nil, err
	}
	if list.Proposals == nil {
		list.Proposals = []ClusterAccessPolicyProposal{}
	}
	return &list, nil
}

func (c *Client) doClusterAccessPolicyProposalRequest(ctx context.Context, method string, endpoint string, payload []byte) ([]byte, int, error) {
	var requestBody io.Reader
	if payload != nil {
		requestBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)
	body, err := readResponseBody(resp)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// proposalNotFoundError reads a proposal 404 that names the organisation
// searched, and says it: a proposal lives in the organisation it was
// created in, so an id someone created answers not found from any other.
// It is nil for a 404 that names no organisation (an older platform, or a
// route that does not exist), which the generic mapping handles.
func proposalNotFoundError(body []byte) error {
	var notFound struct {
		Detail string `json:"detail"`
		ClusterAccessPolicyOrganisation
	}
	if json.Unmarshal(body, &notFound) != nil || notFound.Detail == "" || notFound.OrganisationID == "" {
		return nil
	}
	return &UnexpectedResponseError{
		StatusCode: http.StatusNotFound,
		Detail:     notFound.Detail,
		message: fmt.Sprintf("%s in organisation %s. A proposal belongs to the organisation it was created in: "+
			"if you know it exists, check that --org names that organisation", notFound.Detail, notFound.Describe()),
	}
}

// proposalResponseError maps a refused proposal call: a 422 becomes a
// ClusterAccessPolicyProposalRejectedError naming the platform's reasons,
// a 404 naming the organisation searched says which one, and anything else
// goes through the cluster access refusal mapping (a 403 admin sentence is
// an RBAC denial, a 404 is not-found).
func proposalResponseError(operation string, statusCode int, body []byte) error {
	if statusCode == http.StatusNotFound {
		if notFound := proposalNotFoundError(body); notFound != nil {
			return notFound
		}
	}
	if statusCode == http.StatusUnprocessableEntity {
		if message := validationMessages(body); message != "" {
			return &ClusterAccessPolicyProposalRejectedError{response: newBackendDetailError(statusCode, message)}
		}
		return &ClusterAccessPolicyProposalRejectedError{
			response: newUnexpectedResponseError(operation, statusCode, redactedBodyForError(body, 500)),
		}
	}
	return accessResponseError(operation, statusCode, body)
}

// validationMessages reads a FastAPI-style 422 body ({"detail": [{"loc":
// [...], "msg": "..."}]}) or a plain {"detail": "..."} into one line per
// problem, naming the field when the platform named one.
func validationMessages(body []byte) string {
	var plain struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &plain) == nil && plain.Detail != "" {
		return plain.Detail
	}
	var parsed struct {
		Detail []struct {
			Location []any  `json:"loc"`
			Message  string `json:"msg"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Detail) == 0 {
		return ""
	}
	messages := make([]string, 0, len(parsed.Detail))
	for _, entry := range parsed.Detail {
		message := strings.TrimPrefix(entry.Message, "Value error, ")
		if field := validationField(entry.Location); field != "" {
			message = field + ": " + message
		}
		messages = append(messages, message)
	}
	return strings.Join(messages, "; ")
}

// validationField is the last named element of a pydantic location, unless
// it is only the body or query root.
func validationField(location []any) string {
	if len(location) < 2 {
		return ""
	}
	field, isString := location[len(location)-1].(string)
	if !isString {
		return ""
	}
	return field
}

// ClusterAccessPolicyProposalResponseError is proposalResponseError for
// callers outside this package (the command tests build the platform's
// refusals with it).
func ClusterAccessPolicyProposalResponseError(operation string, statusCode int, body []byte) error {
	return proposalResponseError(operation, statusCode, body)
}
