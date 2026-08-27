package client

import (
	"encoding/json"
	"net/url"
)

// StackProfileDraftSummary is one open builder draft in the drafts list.
type StackProfileDraftSummary struct {
	ID          string  `json:"id"`
	ProfileID   *string `json:"profile_id"`
	Name        string  `json:"name"`
	BaseVersion *int    `json:"base_version"`
	Version     int     `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// StackProfileDraft is the full draft. The spec stays raw JSON so an edit
// round-trips it unchanged, and parameters stay generic maps so an
// annotation update never drops fields this client does not model.
type StackProfileDraft struct {
	ID          string           `json:"id"`
	ProfileID   *string          `json:"profile_id"`
	Name        string           `json:"name"`
	Spec        json.RawMessage  `json:"spec"`
	Parameters  []map[string]any `json:"parameters"`
	BaseVersion *int             `json:"base_version"`
	Version     int              `json:"version"`
}

// CreateStackProfileDraftRequest opens a draft: a blank named one, one
// editing an existing profile, or one seeded from a deployed stack.
type CreateStackProfileDraftRequest struct {
	Name            string `json:"name,omitempty"`
	ProfileID       string `json:"profile_id,omitempty"`
	SourceClusterID string `json:"source_cluster_id,omitempty"`
	SourceStackName string `json:"source_stack_name,omitempty"`
}

// UpdateStackProfileDraftRequest writes the draft back: the full spec, the
// full parameter list (annotations included), and the version read from the
// draft for optimistic concurrency.
type UpdateStackProfileDraftRequest struct {
	Spec       json.RawMessage  `json:"spec"`
	Parameters []map[string]any `json:"parameters"`
	Version    int              `json:"version"`
}

// PublishStackProfileDraftRequest publishes the draft as a profile version.
type PublishStackProfileDraftRequest struct {
	Channel    string `json:"channel,omitempty"`
	Changelog  string `json:"changelog,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// PublishStackProfileDraftResult carries the published profile and version
// as returned by the API; kept generic so new fields pass through -o json.
type PublishStackProfileDraftResult struct {
	Profile map[string]any `json:"profile"`
	Version map[string]any `json:"version"`
}

// DeleteStackProfileDraftResult reports the deletion outcome.
type DeleteStackProfileDraftResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) stackProfileDraftsBasePath() string {
	return c.BaseURL + "/api/v1/org/stack-profiles/drafts"
}

// CreateStackProfileDraft opens a builder draft.
func (c *Client) CreateStackProfileDraft(request CreateStackProfileDraftRequest) (*StackProfileDraft, error) {
	var draft StackProfileDraft
	if err := c.postCSRFJSON(c.stackProfileDraftsBasePath(), request, &draft, "create stack profile draft"); err != nil {
		return nil, err
	}
	return &draft, nil
}

// ListStackProfileDrafts lists the organisation's open builder drafts. The
// API wraps the list in a result envelope.
func (c *Client) ListStackProfileDrafts() ([]StackProfileDraftSummary, error) {
	var envelope struct {
		Result []StackProfileDraftSummary `json:"result"`
	}
	if err := c.getJSON(c.stackProfileDraftsBasePath(), &envelope); err != nil {
		return nil, err
	}
	return envelope.Result, nil
}

// GetStackProfileDraft reads one draft in full.
func (c *Client) GetStackProfileDraft(draftID string) (*StackProfileDraft, error) {
	var draft StackProfileDraft
	if err := c.getJSON(c.stackProfileDraftsBasePath()+"/"+url.PathEscape(draftID), &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

// UpdateStackProfileDraft writes the draft's spec and parameters back.
func (c *Client) UpdateStackProfileDraft(draftID string, request UpdateStackProfileDraftRequest) (*StackProfileDraft, error) {
	var draft StackProfileDraft
	if err := c.postCSRFJSON(c.stackProfileDraftsBasePath()+"/"+url.PathEscape(draftID),
		request, &draft, "update stack profile draft"); err != nil {
		return nil, err
	}
	return &draft, nil
}

// DeleteStackProfileDraft discards a draft.
func (c *Client) DeleteStackProfileDraft(draftID string) (*DeleteStackProfileDraftResult, error) {
	var result DeleteStackProfileDraftResult
	if err := c.deleteCSRFJSON(c.stackProfileDraftsBasePath()+"/"+url.PathEscape(draftID),
		&result, "delete stack profile draft"); err != nil {
		return nil, err
	}
	return &result, nil
}

// PublishStackProfileDraft publishes the draft as a new profile version
// (or a brand-new profile for a draft opened with a name).
//
// Publishing does its whole job on the request path in one transaction -
// redact, derive parameters, insert the version, move latest/current - so
// it rides the slow-write lane rather than the shared client's 30s
// response-header deadline, and a timeout is reported as an unknown
// outcome. It is not idempotent: publishing a still-open draft twice mints
// two versions, which is exactly what a retry after a misreported failure
// would do (ankra-rs107).
func (c *Client) PublishStackProfileDraft(draftID string, request PublishStackProfileDraftRequest) (*PublishStackProfileDraftResult, error) {
	var result PublishStackProfileDraftResult
	if err := c.postCSRFJSONSlowWrite(
		c.stackProfileDraftsBasePath()+"/"+url.PathEscape(draftID)+"/publish",
		request, &result, "publish stack profile draft",
		"ankra stack-profiles list   # the draft is consumed and the profile carries a new version when it landed",
		"publish a second version of the same profile",
	); err != nil {
		return nil, err
	}
	return &result, nil
}
