package client

// Deploying a stack draft that already exists (epic ankra-0xsdd, third live
// verification pass).
//
// A draft is how every clone, every stack-profile instantiation and every
// `ankra cluster draft` lands: reviewable, not running. Deploying one was a
// portal-only move, because the CLI's only deploy was `--deploy` taken at
// clone time, and a second clone onto the same target is refused on the name.
//
// The platform has no "deploy this draft id" route. What the portal's Deploy
// button does - and what the clone's own --deploy does in-process - is the
// ordinary create-stack write with the draft's id carried on the stack:
// POST /api/v1/org/clusters/imported/{cluster_id}/stacks with
// spec.stacks[0].draft_id set. The server loads the draft's resources, sees
// that the named stack is the draft bound to that id, and promotes it.
//
// That write is NOT partial. Members the request omits are deleted from the
// stack, so the request has to carry the draft's content exactly. The CLI
// therefore never re-models it: the listing's stack object is kept as raw
// JSON and posted back unchanged except for the one member the write
// refuses. Anything the CLI does not understand survives precisely because
// the CLI does not touch it - the alternative, decoding into a struct, is
// how encrypted_paths would have been dropped on the way through, and a
// stack that deploys without its SOPS declaration is the corruption this
// design exists to make impossible.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// ClusterStackDocument is one stack from the listing, held as the object the
// API sent it as. Values stay json.RawMessage so re-marshalling reproduces
// the bytes the server rendered.
type ClusterStackDocument map[string]json.RawMessage

// stackDocumentRenderOnlyMember is the one member the listing renders and
// the stack-write spec parser refuses: a resource's live state, whose
// accepted values are creating/updating/stopping/up/down. Every member of a
// draft-only stack renders state "draft", so posting the listing object back
// unedited is a 422 on the first manifest.
//
// It is the ONLY member that has to go. The parser reads members by name and
// ignores the ones it does not know, so the listing's other render-only
// output (is_draft_only, lifecycle, has_encrypted_values, version_history,
// jobs, job, draft, health, intent_requested_at) rides along inert, and the
// three it does read - resource_id, chart_icon, force_delete - are part of
// the draft's identity and are meant to survive.
const stackDocumentRenderOnlyMember = "state"

// Name is the stack's name, empty when the object carries none.
func (document ClusterStackDocument) Name() string {
	return document.stringMember("name")
}

// DraftID is the draft the stack is bound to, empty when it is not bound to
// one - a stack that is deployed and has no edits in flight.
func (document ClusterStackDocument) DraftID() string {
	return document.stringMember("draft_id")
}

// Lifecycle is draft_only, deployed_clean or deployed_dirty.
func (document ClusterStackDocument) Lifecycle() string {
	return document.stringMember("lifecycle")
}

// IsDraftOnly reports a stack that exists only as a draft - nothing of it is
// deployed, which is what a clone leaves behind.
//
// lifecycle is consulted when the boolean is absent or undecodable, because
// the two are rendered from the same fact and the caller's "no" branch means
// "deployed, with edits in flight" - a refusal. An absent observation must
// not be read as that negative answer.
func (document ClusterStackDocument) IsDraftOnly() bool {
	raw, present := document["is_draft_only"]
	if present {
		var value bool
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
	}
	return document.Lifecycle() == stackLifecycleDraftOnly
}

// stackLifecycleDraftOnly is the lifecycle of a stack nothing of which is
// deployed.
const stackLifecycleDraftOnly = "draft_only"

func (document ClusterStackDocument) stringMember(key string) string {
	raw, present := document[key]
	if !present {
		return ""
	}
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// asDeployableSpec returns the stack object with the render-only state
// removed - from the stack itself and from the entries of every member array
// it holds.
//
// The array walk is generic rather than a list of the member names known
// today: "strip state wherever it appears" is the same rule the write
// enforces, so a member list added later is covered without this file
// learning about it. It cannot strip anything the write needs, because state
// is the one member the write refuses. Members that are not arrays of
// objects - variables, encrypted_paths, scalars - never decode and are
// passed through as the exact bytes the server sent.
func (document ClusterStackDocument) asDeployableSpec() (map[string]json.RawMessage, error) {
	specification := make(map[string]json.RawMessage, len(document))
	for key, value := range document {
		if key == stackDocumentRenderOnlyMember {
			continue
		}
		stripped, wasStripped, stripError := stripRenderOnlyStateFromArray(value)
		if stripError != nil {
			return nil, fmt.Errorf("re-encoding the stack's %s: %w", key, stripError)
		}
		if wasStripped {
			specification[key] = stripped
			continue
		}
		specification[key] = value
	}
	return specification, nil
}

// stripRenderOnlyStateFromArray removes the render-only state from each entry
// of an array-of-objects member. It reports false, and changes nothing, for
// any value that is not one.
func stripRenderOnlyStateFromArray(value json.RawMessage) (json.RawMessage, bool, error) {
	var entries []map[string]json.RawMessage
	if json.Unmarshal(value, &entries) != nil {
		return nil, false, nil
	}
	carriesState := false
	for _, entry := range entries {
		if _, present := entry[stackDocumentRenderOnlyMember]; present {
			delete(entry, stackDocumentRenderOnlyMember)
			carriesState = true
		}
	}
	if !carriesState {
		return nil, false, nil
	}
	encoded, marshalError := json.Marshal(entries)
	if marshalError != nil {
		return nil, false, marshalError
	}
	return encoded, true, nil
}

// stackListingPageSize is the listing's maximum accepted page size.
// maximumStackListingPages bounds the walk so a server that answers a full
// page forever cannot spin here; 100 pages is 10,000 stacks on one cluster.
const (
	stackListingPageSize     = 100
	maximumStackListingPages = 100
)

type listClusterStackDocumentsResponse struct {
	Stacks     []ClusterStackDocument `json:"stacks"`
	Pagination Pagination             `json:"pagination"`
}

// StackWriteResult is what the create-stack write answers. Errors non-empty
// means the write was refused and nothing was dispatched; OperationID names
// the operation the accepted deploy runs under.
type StackWriteResult struct {
	StackName         string               `json:"stack_name" yaml:"stack_name"`
	Errors            []StackResourceError `json:"errors,omitempty" yaml:"errors,omitempty"`
	CommitSHA         *string              `json:"commit_sha,omitempty" yaml:"commit_sha,omitempty"`
	CommitURL         *string              `json:"commit_url,omitempty" yaml:"commit_url,omitempty"`
	OperationID       *string              `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
	JobCount          int                  `json:"job_count" yaml:"job_count"`
	SettingsOnlyCount int                  `json:"settings_only_count" yaml:"settings_only_count"`
	NoopCount         int                  `json:"noop_count" yaml:"noop_count"`
	Warnings          []ValidationWarning  `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// StackResourceError is one refused member and the reasons it was refused.
type StackResourceError struct {
	Name   string                   `json:"name" yaml:"name"`
	Kind   string                   `json:"kind" yaml:"kind"`
	Errors []StackResourceErrorItem `json:"errors" yaml:"errors"`
}

// StackResourceErrorItem is one reason, keyed by the field it is about.
type StackResourceErrorItem struct {
	Key     string `json:"key" yaml:"key"`
	Message string `json:"message" yaml:"message"`
}

// ListClusterStackDocuments pages the stack listing, keeping each stack as
// the raw object the API rendered. ListClusterStacks decodes the same
// listing into the display structs; this one exists because a stack that is
// going to be posted back must not lose the members those structs omit.
func (c *Client) ListClusterStackDocuments(clusterID string) ([]ClusterStackDocument, error) {
	var documents []ClusterStackDocument
	for page := 1; page <= maximumStackListingPages; page++ {
		url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks?page=%d&page_size=%d",
			c.BaseURL, neturl.PathEscape(clusterID), page, stackListingPageSize)
		var response listClusterStackDocumentsResponse
		if requestError := c.getJSON(url, &response); requestError != nil {
			return nil, fmt.Errorf("failed to list cluster stacks: %w", requestError)
		}
		documents = append(documents, response.Stacks...)
		if len(response.Stacks) == 0 {
			break
		}
		// total_pages is authoritative when the server sends it, including
		// when it caps page_size below the size asked for - every page is
		// then "short" and a size-only rule would stop after the first.
		// Only when the block is absent (it decodes as 0) does a short page
		// end the walk, which is what keeps an absent block from truncating
		// the listing and reporting a draft on a later page as not found.
		if response.Pagination.TotalPages > 0 {
			if page >= response.Pagination.TotalPages {
				break
			}
			continue
		}
		if len(response.Stacks) < stackListingPageSize {
			break
		}
	}
	return documents, nil
}

// DeployClusterStackDraft promotes the draft the document is bound to,
// through the same create-stack write the portal's Deploy button runs.
func (c *Client) DeployClusterStackDraft(ctx context.Context, clusterID string, document ClusterStackDocument) (*StackWriteResult, error) {
	specification, specificationError := document.asDeployableSpec()
	if specificationError != nil {
		return nil, specificationError
	}
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks",
		c.BaseURL, neturl.PathEscape(clusterID))
	payload := map[string]any{"spec": map[string]any{
		"stacks": []map[string]json.RawMessage{specification},
	}}
	var result StackWriteResult
	if requestError := c.sendJSONContext(ctx, "POST", url, payload, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// Deploying the draft of a stack that is already deployed (bead
// ankra-bjkszh.10).
//
// `ankra cluster draft -f` on an existing stack, and the stack builder, both
// leave a draft bound to a live stack. The create-stack write above refuses
// it - the stack exists - so it could only be deployed from the builder,
// which promotes it through PATCH /stacks/{stack_name} with the draft's id.
// This is that write. Two things differ from the draft-only deploy:
//
//   - The content comes from the draft read, not the listing. The listing
//     elides manifest bodies and add-on values, and the update write - unlike
//     the create write - does not reload the draft's rows, so an elided body
//     would deploy the LIVE body under the draft's name and then close the
//     draft as if its change had shipped.
//   - The write is partial. A draft of a deployed stack holds only the
//     members it changes; a full write deletes every member a request omits,
//     so promoting the draft whole would delete the stack's untouched members.
//     The partial promote writes the draft's members, removes them from the
//     draft, and closes the draft once nothing is left in it.

// stackDraftReadCodeUnknown and stackDraftReadCodeTombstoned are the codes the
// draft read answers for a draft that never existed and for one that ended.
const (
	stackDraftReadCodeUnknown    = "DRAFT_UNKNOWN"
	stackDraftReadCodeTombstoned = "DRAFT_TOMBSTONED"
)

// StackDraftReadError is a draft read the platform did not answer with the
// draft. Code is the platform's code when it sent one; a 404 without a code
// is a platform that does not serve the read at all.
type StackDraftReadError struct {
	StatusCode int
	Code       string
	Message    string
}

func (readError *StackDraftReadError) Error() string {
	switch {
	case readError.Code == stackDraftReadCodeTombstoned:
		return "the draft has ended: it was discarded or deployed since the stack was listed"
	case readError.Code == stackDraftReadCodeUnknown:
		return "the draft no longer exists on the cluster"
	case readError.StatusCode == http.StatusNotFound && readError.Message == "":
		return "this Ankra platform does not serve stack draft reads to the CLI yet; deploy the draft from the stack builder"
	case readError.Message != "":
		return fmt.Sprintf("reading the draft failed: status %d: %s", readError.StatusCode, readError.Message)
	default:
		return fmt.Sprintf("reading the draft failed: status %d", readError.StatusCode)
	}
}

// IsGone reports a draft that no longer exists - never created, discarded,
// or already deployed - as opposed to a read that could not be made.
func (readError *StackDraftReadError) IsGone() bool {
	return readError.Code == stackDraftReadCodeUnknown || readError.Code == stackDraftReadCodeTombstoned
}

// stackDraftReadErrorFromResponse decodes the draft read's refusal. Its
// detail is an object ({code, message}) for the draft's own answers and a
// string for the gates in front of it (an unknown cluster).
func stackDraftReadErrorFromResponse(statusCode int, body []byte) *StackDraftReadError {
	readError := &StackDraftReadError{StatusCode: statusCode}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Detail) == 0 {
		return readError
	}
	var structured struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(envelope.Detail, &structured) == nil {
		readError.Code = structured.Code
		readError.Message = structured.Message
		return readError
	}
	var sentence string
	if json.Unmarshal(envelope.Detail, &sentence) == nil {
		readError.Message = sentence
	}
	return readError
}

// GetClusterStackDraft reads one stack draft with the full content of the
// members it holds: GET /api/v1/org/clusters/imported/{cluster_id}/drafts/stacks/{draft_id}.
// The stack is kept as the raw object the API rendered, for the same reason
// the listing's is.
func (c *Client) GetClusterStackDraft(ctx context.Context, clusterID string, draftID string) (ClusterStackDocument, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/drafts/stacks/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(draftID))
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode != http.StatusOK {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		return nil, stackDraftReadErrorFromResponse(response.StatusCode, body)
	}
	var draftRead struct {
		Stack ClusterStackDocument `json:"stack"`
	}
	if unmarshalError := json.Unmarshal(body, &draftRead); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	if draftRead.Stack == nil {
		return nil, errors.New("the draft read carried no stack")
	}
	return draftRead.Stack, nil
}

// promoteStackDraftRequest is the builder's promote body, as a partial write.
type promoteStackDraftRequest struct {
	Spec         map[string]any `json:"spec"`
	PartialStack bool           `json:"partial_stack"`
	DraftID      string         `json:"draft_id"`
}

// PromoteClusterStackDraft promotes the draft of an already-deployed stack
// through PATCH /stacks/{stack_name} with its draft_id, carrying exactly the
// members the draft holds. document is the draft read's stack, never the
// listing's: see the comment above StackDraftReadError.
func (c *Client) PromoteClusterStackDraft(ctx context.Context, clusterID string, document ClusterStackDocument) (*PatchStackResult, error) {
	stackName := document.Name()
	draftID := document.DraftID()
	if stackName == "" || draftID == "" {
		return nil, errors.New("the draft read must name the stack and its draft")
	}
	specification, specificationError := document.asDeployableSpec()
	if specificationError != nil {
		return nil, specificationError
	}
	return c.patchClusterStack(ctx, clusterID, stackName, promoteStackDraftRequest{
		Spec:         map[string]any{"stacks": []map[string]json.RawMessage{specification}},
		PartialStack: true,
		DraftID:      draftID,
	})
}
