package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// CanvasPublication mirrors one published canvas page. Content is never
// returned by the platform after publishing.
type CanvasPublication struct {
	ID        string `json:"id" yaml:"id"`
	Slug      string `json:"slug" yaml:"slug"`
	URL       string `json:"url" yaml:"url"`
	Title     string `json:"title" yaml:"title"`
	Language  string `json:"language" yaml:"language"`
	SizeBytes int    `json:"size_bytes" yaml:"size_bytes"`
	Status    string `json:"status" yaml:"status"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	UpdatedAt string `json:"updated_at" yaml:"updated_at"`
	ExpiresAt string `json:"expires_at" yaml:"expires_at"`
}

// CanvasPublicationRequest is the publish payload. Publishing the same
// conversation and artifact pair again updates the existing page in place and
// keeps its URL.
type CanvasPublicationRequest struct {
	ConversationID string `json:"conversation_id"`
	ArtifactID     string `json:"artifact_id"`
	Title          string `json:"title"`
	Language       string `json:"language"`
	Content        string `json:"content"`
	TTLHours       *int   `json:"ttl_hours,omitempty"`
}

// CanvasPublicationResult is a publish response. Created is true when the
// platform answered 201 (a new page) and false for an in-place update (200).
type CanvasPublicationResult struct {
	CanvasPublication
	Created bool `json:"-" yaml:"-"`
}

// CanvasPublicationsListResult is one page of the organisation's publications.
type CanvasPublicationsListResult struct {
	Items      []CanvasPublication `json:"items" yaml:"items"`
	NextCursor string              `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
}

// ErrCanvasPublicationNotFound is returned when the platform reports 404 for a
// publication id.
var ErrCanvasPublicationNotFound = errors.New("canvas publication not found")

// CanvasPagesError carries a refusal from the canvas publication routes with
// the platform's machine-readable code (for example canvas_pages_disabled,
// unsupported_language, content_too_large, invalid_ttl or
// publication_limit_reached).
type CanvasPagesError struct {
	StatusCode int
	Code       string
	Detail     string
}

func (e *CanvasPagesError) Error() string {
	switch {
	case e.Code != "" && e.Detail != "" && e.Detail != e.Code:
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	case e.Code != "":
		return e.Code
	case e.Detail != "":
		return e.Detail
	}
	return fmt.Sprintf("canvas publication request failed with status %d", e.StatusCode)
}

const canvasPublicationsPath = "/api/v1/org/canvas-publications"

func (c *Client) PublishCanvasPublication(ctx context.Context, request CanvasPublicationRequest) (*CanvasPublicationResult, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, status, err := c.doCanvasPublicationRequest(ctx, http.MethodPost, c.BaseURL+canvasPublicationsPath, payload)
	if err != nil {
		return nil, err
	}
	var out CanvasPublicationResult
	if err := json.Unmarshal(body, &out.CanvasPublication); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	out.Created = status == http.StatusCreated
	return &out, nil
}

func (c *Client) ListCanvasPublications(ctx context.Context, cursor string, limit int) (*CanvasPublicationsListResult, error) {
	query := neturl.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	target := c.BaseURL + canvasPublicationsPath
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	body, _, err := c.doCanvasPublicationRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	var out CanvasPublicationsListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) UnpublishCanvasPublication(ctx context.Context, publicationID string) error {
	target := fmt.Sprintf("%s%s/%s", c.BaseURL, canvasPublicationsPath, neturl.PathEscape(publicationID))
	_, _, err := c.doCanvasPublicationRequest(ctx, http.MethodDelete, target, nil)
	return err
}

func (c *Client) doCanvasPublicationRequest(ctx context.Context, method, target string, body []byte) ([]byte, int, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	var request *http.Request
	var err error
	if bodyReader != nil {
		request, err = http.NewRequestWithContext(ctx, method, target, bodyReader)
	} else {
		request, err = http.NewRequestWithContext(ctx, method, target, nil)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	responseBody, err := readResponseBody(response)
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return responseBody, response.StatusCode, nil
	case http.StatusUnauthorized:
		return nil, response.StatusCode, ErrUnauthorized
	case http.StatusNotFound:
		return nil, response.StatusCode, ErrCanvasPublicationNotFound
	case http.StatusForbidden:
		code, detail := canvasErrorFromBody(responseBody)
		if code == "permission_denied" || detail == "" {
			return nil, response.StatusCode, &PermissionDeniedError{}
		}
		return nil, response.StatusCode, &PermissionDeniedError{Detail: detail}
	case http.StatusBadRequest, http.StatusConflict, http.StatusServiceUnavailable:
		code, detail := canvasErrorFromBody(responseBody)
		if code != "" || detail != "" {
			return nil, response.StatusCode, &CanvasPagesError{StatusCode: response.StatusCode, Code: code, Detail: detail}
		}
	}
	return nil, response.StatusCode, newUnexpectedResponseError("canvas publication request failed", response.StatusCode, truncateForError(responseBody, 500))
}

// canvasErrorFromBody extracts the machine code and human sentence from the
// error envelope. The platform writes either {"detail": "<code>"},
// {"detail": {"code": "...", "message": "..."}} or {"code": "...", "detail": "..."}.
func canvasErrorFromBody(body []byte) (string, string) {
	var envelope struct {
		Code    string          `json:"code"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", ""
	}
	code := envelope.Code
	if code == "" {
		code = envelope.Error
	}
	detail := envelope.Message
	if len(envelope.Detail) > 0 {
		var detailText string
		if err := json.Unmarshal(envelope.Detail, &detailText); err == nil {
			if code == "" {
				code = detailText
			} else if detail == "" {
				detail = detailText
			}
		} else {
			var detailObject struct {
				Code    string `json:"code"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(envelope.Detail, &detailObject); err == nil {
				if code == "" {
					code = detailObject.Code
				}
				if code == "" {
					code = detailObject.Error
				}
				if detailObject.Message != "" {
					detail = detailObject.Message
				}
			}
		}
	}
	return code, detail
}
