package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// DecisionTimeWindow is a closed-open [from, to) interval.
type DecisionTimeWindow struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
}

// DecisionVerificationDay is one judged 24h day of a right-size's
// verification. State is clear, breach or unknown; a nil share is a resource
// no node reported that day.
type DecisionVerificationDay struct {
	Day            int      `json:"day" yaml:"day"`
	From           string   `json:"from" yaml:"from"`
	To             string   `json:"to" yaml:"to"`
	State          string   `json:"state" yaml:"state"`
	CPUP95Share    *float64 `json:"cpu_p95_share" yaml:"cpu_p95_share"`
	MemoryP95Share *float64 `json:"memory_p95_share" yaml:"memory_p95_share"`
	HottestNode    string   `json:"hottest_node,omitempty" yaml:"hottest_node,omitempty"`
	Nodes          int      `json:"nodes" yaml:"nodes"`
	Reporting      int      `json:"reporting" yaml:"reporting"`
	Reason         string   `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// DecisionReceiptStep is one step of running a proposal. Outcome is
// dispatched, succeeded, failed, refused, error or abandoned.
type DecisionReceiptStep struct {
	Name     string         `json:"name" yaml:"name"`
	Outcome  string         `json:"outcome" yaml:"outcome"`
	Detail   string         `json:"detail,omitempty" yaml:"detail,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// DecisionReceipt records running a proposal: who ran it and when, the
// platform operation it became, the steps and how it ended.
type DecisionReceipt struct {
	ClaimedAt       string                `json:"claimed_at,omitempty" yaml:"claimed_at,omitempty"`
	ExecutedBy      string                `json:"executed_by,omitempty" yaml:"executed_by,omitempty"`
	DispatchedAt    string                `json:"dispatched_at,omitempty" yaml:"dispatched_at,omitempty"`
	OperationID     string                `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
	CompletedAt     string                `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
	ExecutionStatus string                `json:"execution_status,omitempty" yaml:"execution_status,omitempty"`
	Steps           []DecisionReceiptStep `json:"steps" yaml:"steps"`
}

// DecisionProposal is one proposal of the decision ledger: what it is about,
// what the surface computed (Plan, whose parameters the executable kinds are
// called with), where it stands, who decided and what running it did. Status
// is proposed, approved, held, set_aside, running, succeeded, failed or
// superseded. The money fields are USD cents and nil when unknown, never
// zero. ParentID names the right_size_ladder a wave belongs to; RunAfter is
// the earliest time the cost autopilot may run a proposal it approved itself.
type DecisionProposal struct {
	ID                   string                    `json:"id" yaml:"id"`
	Area                 string                    `json:"area" yaml:"area"`
	Kind                 string                    `json:"kind" yaml:"kind"`
	Subject              map[string]any            `json:"subject" yaml:"subject"`
	Summary              string                    `json:"summary" yaml:"summary"`
	Plan                 map[string]any            `json:"plan" yaml:"plan"`
	Evidence             map[string]any            `json:"evidence" yaml:"evidence"`
	Status               string                    `json:"status" yaml:"status"`
	Source               string                    `json:"source" yaml:"source"`
	CreatedBy            *string                   `json:"created_by" yaml:"created_by"`
	DecidedBy            *string                   `json:"decided_by" yaml:"decided_by"`
	DecidedAt            *string                   `json:"decided_at" yaml:"decided_at"`
	DecisionNote         *string                   `json:"decision_note" yaml:"decision_note"`
	OperationID          *string                   `json:"operation_id" yaml:"operation_id"`
	Receipt              *DecisionReceipt          `json:"receipt" yaml:"receipt"`
	DedupeKey            string                    `json:"dedupe_key" yaml:"dedupe_key"`
	Executable           bool                      `json:"executable" yaml:"executable"`
	ExpectedMonthlyCents *int64                    `json:"expected_monthly_cents" yaml:"expected_monthly_cents"`
	BaselineMonthlyCents *int64                    `json:"baseline_monthly_cents" yaml:"baseline_monthly_cents"`
	BaselineWindow       *DecisionTimeWindow       `json:"baseline_window" yaml:"baseline_window"`
	MeasuredMonthlyCents *int64                    `json:"measured_monthly_cents" yaml:"measured_monthly_cents"`
	MeasuredAt           *string                   `json:"measured_at" yaml:"measured_at"`
	MeasurementStatus    string                    `json:"measurement_status" yaml:"measurement_status"`
	VerifyUntil          *string                   `json:"verify_until" yaml:"verify_until"`
	SubjectClusterID     *string                   `json:"subject_cluster_id" yaml:"subject_cluster_id"`
	VerificationStatus   string                    `json:"verification_status" yaml:"verification_status"`
	VerificationDays     []DecisionVerificationDay `json:"verification_days" yaml:"verification_days"`
	RollbackOf           *string                   `json:"rollback_of" yaml:"rollback_of"`
	ParentID             *string                   `json:"parent_id" yaml:"parent_id"`
	CreatedAt            string                    `json:"created_at" yaml:"created_at"`
	UpdatedAt            string                    `json:"updated_at" yaml:"updated_at"`
	RunAfter             *string                   `json:"run_after" yaml:"run_after"`
}

// DecisionProposalList is GET /org/decisions: the proposals newest first,
// and the running ones this read could not settle against their platform
// operation. Those are served as they stand and mean "could not check", not
// "still running".
type DecisionProposalList struct {
	Proposals            []DecisionProposal `json:"proposals" yaml:"proposals"`
	UnsettledProposalIDs []string           `json:"unsettled_proposal_ids" yaml:"unsettled_proposal_ids"`
}

// DecisionEvent is one thing that happened to a proposal: proposed,
// reopened, approved, set_aside, superseded, execution_started, dispatched,
// succeeded, failed and the rest, with its actor, note and detail.
type DecisionEvent struct {
	ID         string         `json:"id" yaml:"id"`
	ProposalID string         `json:"proposal_id" yaml:"proposal_id"`
	EventType  string         `json:"event_type" yaml:"event_type"`
	Actor      *string        `json:"actor" yaml:"actor"`
	Note       *string        `json:"note" yaml:"note"`
	Detail     map[string]any `json:"detail" yaml:"detail"`
	CreatedAt  string         `json:"created_at" yaml:"created_at"`
}

// DecisionActivity is GET /org/decisions/{decision_id}/activity, newest
// first.
type DecisionActivity struct {
	Events []DecisionEvent `json:"events" yaml:"events"`
}

// DecisionListFilter narrows GET /org/decisions. Empty fields and a zero
// Limit are not sent, so the platform's own defaults apply.
type DecisionListFilter struct {
	Area   string
	Status string
	Limit  int
}

// DecisionExecuteOptions is the optional body of execute: the written consent
// a delete with no snapshot needs, and the minimum age of what it deletes.
// Each is sent only when set: consent is never assumed.
type DecisionExecuteOptions struct {
	Consent           string
	MinUnattachedDays *int
}

// Body is the JSON object execute sends, or nil when nothing was given.
func (options DecisionExecuteOptions) Body() map[string]any {
	body := map[string]any{}
	if options.Consent != "" {
		body["consent"] = options.Consent
	}
	if options.MinUnattachedDays != nil {
		body["min_unattached_days"] = *options.MinUnattachedDays
	}
	if len(body) == 0 {
		return nil
	}
	return body
}

// DecisionConsentRequiredError is execute's 422 with error_code
// consent_required: the plan deletes something no snapshot keeps, so it runs
// only with written consent. Nothing was attempted and the proposal stays
// approved.
type DecisionConsentRequiredError struct {
	Detail string
}

func (e *DecisionConsentRequiredError) Error() string {
	if e == nil {
		return ""
	}
	return e.Detail
}

// decisionConsentRequiredCode is the stable code execute answers a run that
// needs written consent with.
const decisionConsentRequiredCode = "consent_required"

// Each decision route is written out as a whole /api/v1 literal, so
// TestClusterRoutesAreRegistered checks every one of them against the cluster
// route census; a shared prefix plus an appended action would hide the action.

// ListDecisions returns the organisation's proposals, newest first.
// GET /api/v1/org/decisions
func (c *Client) ListDecisions(filter DecisionListFilter) (*DecisionProposalList, error) {
	query := neturl.Values{}
	if filter.Area != "" {
		query.Set("area", filter.Area)
	}
	if filter.Status != "" {
		query.Set("status", filter.Status)
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	url := c.BaseURL + "/api/v1/org/decisions"
	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}
	var result DecisionProposalList
	if err := c.sendJSON(http.MethodGet, url, nil, &result); err != nil {
		return nil, err
	}
	if result.Proposals == nil {
		result.Proposals = []DecisionProposal{}
	}
	if result.UnsettledProposalIDs == nil {
		result.UnsettledProposalIDs = []string{}
	}
	return &result, nil
}

// GetDecision returns one proposal with its plan, evidence, decision and
// receipt; a running one is settled against its platform operation first.
// GET /api/v1/org/decisions/{decision_id}
func (c *Client) GetDecision(decisionID string) (*DecisionProposal, error) {
	var result DecisionProposal
	if err := c.sendJSON(http.MethodGet, fmt.Sprintf("%s/api/v1/org/decisions/%s", c.BaseURL, neturl.PathEscape(decisionID)), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetDecisionActivity returns everything that happened to a proposal, newest
// first.
// GET /api/v1/org/decisions/{decision_id}/activity
func (c *Client) GetDecisionActivity(decisionID string) (*DecisionActivity, error) {
	var result DecisionActivity
	if err := c.sendJSON(http.MethodGet, fmt.Sprintf("%s/api/v1/org/decisions/%s/activity", c.BaseURL, neturl.PathEscape(decisionID)), nil, &result); err != nil {
		return nil, err
	}
	if result.Events == nil {
		result.Events = []DecisionEvent{}
	}
	return &result, nil
}

// decisionNoteBody is the approve, set-aside, hold and release body: the note
// when one was given, and an empty object otherwise.
func decisionNoteBody(note *string) map[string]any {
	body := map[string]any{}
	if note != nil {
		body["note"] = *note
	}
	return body
}

// ApproveDecision records the caller's approval, with the note when one is
// given. It needs the area's permission (billing.manage for cost).
// POST /api/v1/org/decisions/{decision_id}/approve
func (c *Client) ApproveDecision(decisionID string, note *string) (*DecisionProposal, error) {
	var result DecisionProposal
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/decisions/%s/approve", c.BaseURL, neturl.PathEscape(decisionID)), decisionNoteBody(note), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HoldDecision holds an approved proposal before it runs, with the note when
// one is given: nothing runs a held proposal, neither the cost autopilot nor
// execute, until it is released or set aside. It needs the area's permission.
// POST /api/v1/org/decisions/{decision_id}/hold
func (c *Client) HoldDecision(decisionID string, note *string) (*DecisionProposal, error) {
	var result DecisionProposal
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/decisions/%s/hold", c.BaseURL, neturl.PathEscape(decisionID)), decisionNoteBody(note), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReleaseDecision returns a held proposal to approved, with the note when one
// is given. One the cost autopilot filed then runs on its next pass once its
// run_after has passed. It needs the area's permission.
// POST /api/v1/org/decisions/{decision_id}/release
func (c *Client) ReleaseDecision(decisionID string, note *string) (*DecisionProposal, error) {
	var result DecisionProposal
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/decisions/%s/release", c.BaseURL, neturl.PathEscape(decisionID)), decisionNoteBody(note), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetAsideDecision records that the caller declined the proposal, with the
// note when one is given.
// POST /api/v1/org/decisions/{decision_id}/set-aside
func (c *Client) SetAsideDecision(decisionID string, note *string) (*DecisionProposal, error) {
	var result DecisionProposal
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/decisions/%s/set-aside", c.BaseURL, neturl.PathEscape(decisionID)), decisionNoteBody(note), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExecuteDecision runs an approved proposal and returns it as the run left
// it: running with its operation id, succeeded, or failed with its receipt. A
// plan that needs written consent the options did not carry comes back as
// *DecisionConsentRequiredError; every other refusal is shaped as sendJSON
// shapes it.
// POST /api/v1/org/decisions/{decision_id}/execute
func (c *Client) ExecuteDecision(decisionID string, options DecisionExecuteOptions) (*DecisionProposal, error) {
	url := fmt.Sprintf("%s/api/v1/org/decisions/%s/execute", c.BaseURL, neturl.PathEscape(decisionID))
	var payload []byte
	if body := options.Body(); body != nil {
		encoded, marshalError := json.Marshal(body)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		payload = encoded
	}
	request, requestError := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnprocessableEntity {
		var refusal struct {
			Detail    string `json:"detail"`
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal(responseBody, &refusal) == nil && refusal.ErrorCode == decisionConsentRequiredCode {
			return nil, &DecisionConsentRequiredError{Detail: refusal.Detail}
		}
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case response.StatusCode < 200 || response.StatusCode >= 300:
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("request failed", response.StatusCode, redactedBodyForError(responseBody, 500))
	}
	var result DecisionProposal
	if unmarshalError := json.Unmarshal(responseBody, &result); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &result, nil
}
