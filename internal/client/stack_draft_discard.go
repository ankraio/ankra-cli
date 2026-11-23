package client

// Discarding a stack draft (ankra-0xsdd.135): DELETE
// /api/v1/org/clusters/imported/{cluster_id}/drafts/stacks/{draft_id}, the
// write the stack builder's Discard button and the platform's
// discard_stack_draft tool use.
//
// The platform keeps a draft an AI conversation is bound to, or has just
// written, unless the discard is forced: that answers 200 with
// already_discarded true and reason "protected", and writes nothing. So a
// 200 is not proof of a discard; the caller reads Discarded().

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

// StackDraftDiscardReasonProtected is the reason the platform answers for a
// draft it kept because an AI conversation owns it.
const StackDraftDiscardReasonProtected = "protected"

// StackDraftDiscardResult is the platform's answer to a discard.
// AlreadyDiscarded is true when the call discarded nothing: the draft had
// already ended (Reason is why it ended) or it is protected.
type StackDraftDiscardResult struct {
	Success          bool    `json:"success" yaml:"success"`
	StackName        *string `json:"stack_name" yaml:"stack_name"`
	AlreadyDiscarded bool    `json:"already_discarded" yaml:"already_discarded"`
	Reason           *string `json:"reason" yaml:"reason"`
}

// Discarded reports whether this call discarded the draft.
func (result *StackDraftDiscardResult) Discarded() bool {
	return result.Success && !result.AlreadyDiscarded
}

// IsProtected reports a draft the platform kept because an AI conversation
// owns it; only a forced discard removes it.
func (result *StackDraftDiscardResult) IsProtected() bool {
	return result.AlreadyDiscarded && result.Reason != nil && *result.Reason == StackDraftDiscardReasonProtected
}

// StackDraftDiscardError is a discard the platform refused. Code is the
// platform's code when it sent one (DRAFT_UNKNOWN for a draft it does not
// hold).
type StackDraftDiscardError struct {
	StatusCode int
	Code       string
	Message    string
}

func (discardError *StackDraftDiscardError) Error() string {
	switch {
	case discardError.Code == stackDraftReadCodeUnknown:
		return "the draft no longer exists on the cluster"
	case discardError.StatusCode == http.StatusConflict && discardError.Message != "":
		return "the platform kept the draft: " + discardError.Message
	case discardError.Message != "":
		return fmt.Sprintf("discarding the draft failed: status %d: %s", discardError.StatusCode, discardError.Message)
	default:
		return fmt.Sprintf("discarding the draft failed: status %d", discardError.StatusCode)
	}
}

// IsGone reports a draft the platform does not hold, as opposed to a
// discard that could not be made.
func (discardError *StackDraftDiscardError) IsGone() bool {
	return discardError.Code == stackDraftReadCodeUnknown
}

// DiscardClusterStackDraft discards one stack draft. force discards a draft
// an AI conversation owns, which an unforced call keeps.
func (c *Client) DiscardClusterStackDraft(ctx context.Context, clusterID string, draftID string,
	force bool) (*StackDraftDiscardResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/drafts/stacks/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(draftID))
	if force {
		url += "?force=true"
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
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
		readRefusal := stackDraftReadErrorFromResponse(response.StatusCode, body)
		return nil, &StackDraftDiscardError{
			StatusCode: readRefusal.StatusCode, Code: readRefusal.Code, Message: readRefusal.Message,
		}
	}
	var result StackDraftDiscardResult
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &result, nil
}
