package client

// The canvas library (ankra-hq41xb.22): the saved canvases behind the
// portal's canvas pages, their revisions and their comment threads.
//
//	GET|POST         /api/v1/org/canvases
//	GET|PATCH|DELETE /api/v1/org/canvases/{canvas_id}
//	GET              /api/v1/org/canvases/{canvas_id}/revisions[/{revision_id}]
//	GET|POST         /api/v1/org/canvases/{canvas_id}/comments
//	POST             /api/v1/org/canvases/{canvas_id}/comments/{comment_id}/resolve|reopen
//
// Every read answers the platform's document as sent. A refusal is a
// CanvasPagesError carrying the platform's reason (canvas_not_found,
// canvas_owned_by_another_member, comment_forbidden, ...) and its status.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// CanvasListOptions narrows a canvas library listing. Zero values are left
// to the platform's defaults.
type CanvasListOptions struct {
	Cursor       string
	Limit        int
	Query        string
	Kind         string
	IsPinnedOnly bool
}

// CreateCanvasRequest is the body that saves a new canvas with its first
// revision.
type CreateCanvasRequest struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

// UpdateCanvasRequest renames a canvas, changes who can read it, or both; a
// nil member is left as it is.
type UpdateCanvasRequest struct {
	Title      *string `json:"title,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
}

// CreateCanvasCommentRequest adds a comment, optionally anchored to an
// element or a revision, or as a reply to another comment.
type CreateCanvasCommentRequest struct {
	Body            string  `json:"body"`
	ElementID       *string `json:"element_id,omitempty"`
	ParentCommentID *string `json:"parent_comment_id,omitempty"`
	RevisionID      *string `json:"revision_id,omitempty"`
}

// ListCanvases reads one page of the canvases the caller can see, without
// content.
func (c *Client) ListCanvases(ctx context.Context, options CanvasListOptions) (json.RawMessage, error) {
	query := canvasPageQuery(options.Cursor, options.Limit)
	if options.Query != "" {
		query.Set("query", options.Query)
	}
	if options.Kind != "" {
		query.Set("kind", options.Kind)
	}
	if options.IsPinnedOnly {
		query.Set("pinned", "true")
	}
	return c.doCanvasRequest(ctx, http.MethodGet, withCanvasQuery(c.BaseURL+"/api/v1/org/canvases", query), nil)
}

// GetCanvas reads a canvas with the content of its draft revision.
func (c *Client) GetCanvas(ctx context.Context, canvasID string) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/org/canvases/%s", c.BaseURL, neturl.PathEscape(canvasID)), nil)
}

// CreateCanvas saves a new canvas, private to its creator.
func (c *Client) CreateCanvas(ctx context.Context, request CreateCanvasRequest) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodPost, c.BaseURL+"/api/v1/org/canvases", request)
}

// UpdateCanvas changes a canvas's title, visibility or both.
func (c *Client) UpdateCanvas(ctx context.Context, canvasID string, request UpdateCanvasRequest) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodPatch,
		fmt.Sprintf("%s/api/v1/org/canvases/%s", c.BaseURL, neturl.PathEscape(canvasID)), request)
}

// DeleteCanvas removes a canvas from the library.
func (c *Client) DeleteCanvas(ctx context.Context, canvasID string) error {
	_, requestError := c.doCanvasRequest(ctx, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/org/canvases/%s", c.BaseURL, neturl.PathEscape(canvasID)), nil)
	return requestError
}

// ListCanvasRevisions reads one page of a canvas's revisions, newest first,
// without content.
func (c *Client) ListCanvasRevisions(ctx context.Context, canvasID string, cursor string, limit int) (json.RawMessage, error) {
	target := fmt.Sprintf("%s/api/v1/org/canvases/%s/revisions", c.BaseURL, neturl.PathEscape(canvasID))
	return c.doCanvasRequest(ctx, http.MethodGet, withCanvasQuery(target, canvasPageQuery(cursor, limit)), nil)
}

// GetCanvasRevision reads one revision of a canvas with its content.
func (c *Client) GetCanvasRevision(ctx context.Context, canvasID string, revisionID string) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/org/canvases/%s/revisions/%s", c.BaseURL,
			neturl.PathEscape(canvasID), neturl.PathEscape(revisionID)), nil)
}

// ListCanvasComments reads one page of a canvas's comment threads: the open
// ones, and the resolved ones too when includeResolved is set.
func (c *Client) ListCanvasComments(ctx context.Context, canvasID string, cursor string, limit int,
	includeResolved bool) (json.RawMessage, error) {
	query := canvasPageQuery(cursor, limit)
	if includeResolved {
		query.Set("include_resolved", "true")
	}
	target := fmt.Sprintf("%s/api/v1/org/canvases/%s/comments", c.BaseURL, neturl.PathEscape(canvasID))
	return c.doCanvasRequest(ctx, http.MethodGet, withCanvasQuery(target, query), nil)
}

// CreateCanvasComment adds a comment to a canvas.
func (c *Client) CreateCanvasComment(ctx context.Context, canvasID string,
	request CreateCanvasCommentRequest) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/org/canvases/%s/comments", c.BaseURL, neturl.PathEscape(canvasID)), request)
}

// ResolveCanvasComment marks a comment thread resolved.
func (c *Client) ResolveCanvasComment(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/org/canvases/%s/comments/%s/resolve", c.BaseURL,
			neturl.PathEscape(canvasID), neturl.PathEscape(commentID)), nil)
}

// ReopenCanvasComment reopens a resolved comment thread.
func (c *Client) ReopenCanvasComment(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error) {
	return c.doCanvasRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/org/canvases/%s/comments/%s/reopen", c.BaseURL,
			neturl.PathEscape(canvasID), neturl.PathEscape(commentID)), nil)
}

func canvasPageQuery(cursor string, limit int) neturl.Values {
	query := neturl.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return query
}

func withCanvasQuery(target string, query neturl.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return target + "?" + encoded
	}
	return target
}

// doCanvasRequest sends one canvas library request and answers the response
// body. A refusal that names a reason or a detail is a CanvasPagesError with
// the status it came with; a 403 that names neither is a permission denial.
func (c *Client) doCanvasRequest(ctx context.Context, method string, target string, payload any) (json.RawMessage, error) {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	var request *http.Request
	var requestError error
	if bodyReader != nil {
		request, requestError = http.NewRequestWithContext(ctx, method, target, bodyReader)
	} else {
		request, requestError = http.NewRequestWithContext(ctx, method, target, nil)
	}
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
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
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return json.RawMessage(responseBody), nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	}
	code, detail := canvasErrorFromBody(responseBody)
	if response.StatusCode == http.StatusForbidden && (code == "" || code == "permission_denied") {
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		return nil, &PermissionDeniedError{Detail: detail}
	}
	if code != "" || detail != "" {
		return nil, &CanvasPagesError{StatusCode: response.StatusCode, Code: code, Detail: detail}
	}
	return nil, newUnexpectedResponseError("canvas request failed", response.StatusCode, truncateForError(responseBody, 500))
}
