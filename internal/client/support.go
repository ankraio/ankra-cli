package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// SupportTicketComment mirrors the backend customer-visible comment shape.
// Linear identifiers are intentionally never present on the wire.
type SupportTicketComment struct {
	ID          string    `json:"id"`
	AuthorType  string    `json:"author_type"`
	AuthorLabel string    `json:"author_label"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
}

// SupportTicket mirrors the backend Ticket wire model. It carries no Linear
// fields by design; the only team-tracking signal is IsTrackedByTeam.
type SupportTicket struct {
	ID              string                 `json:"id"`
	OrganisationID  string                 `json:"organisation_id"`
	ClusterID       *string                `json:"cluster_id,omitempty"`
	ClusterName     *string                `json:"cluster_name,omitempty"`
	Source          string                 `json:"source"`
	Category        string                 `json:"category"`
	Subject         string                 `json:"subject"`
	Description     string                 `json:"description"`
	Status          string                 `json:"status"`
	Severity        *string                `json:"severity,omitempty"`
	AISummary       *string                `json:"ai_summary,omitempty"`
	AIReviewStatus  string                 `json:"ai_review_status"`
	IsTrackedByTeam bool                   `json:"is_tracked_by_team"`
	Comments        []SupportTicketComment `json:"comments"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type SupportTicketSummary struct {
	ID              string    `json:"id"`
	ClusterID       *string   `json:"cluster_id,omitempty"`
	ClusterName     *string   `json:"cluster_name,omitempty"`
	Source          string    `json:"source"`
	Category        string    `json:"category"`
	Subject         string    `json:"subject"`
	Status          string    `json:"status"`
	Severity        *string   `json:"severity,omitempty"`
	AIReviewStatus  string    `json:"ai_review_status"`
	IsTrackedByTeam bool      `json:"is_tracked_by_team"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SupportTicketPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalPages int `json:"total_pages"`
	TotalCount int `json:"total_count"`
}

type SupportTicketListResponse struct {
	Result     []SupportTicketSummary  `json:"result"`
	Pagination SupportTicketPagination `json:"pagination"`
}

// CreateSupportTicketRequest is the wire shape for the create POST. When
// ReviewID is set the backend reads the subject/description/category/cluster
// from the stored review and only honours Severity, Acknowledged and the review
// reference; otherwise the raw fields are used and the backend runs the AI
// review inline.
type CreateSupportTicketRequest struct {
	ReviewID     *string `json:"review_id,omitempty"`
	Subject      string  `json:"subject,omitempty"`
	Description  string  `json:"description,omitempty"`
	Category     string  `json:"category,omitempty"`
	ClusterID    *string `json:"cluster_id,omitempty"`
	Severity     *string `json:"severity,omitempty"`
	Source       string  `json:"source"`
	Acknowledged bool    `json:"acknowledged"`
}

// ReviewSupportTicketRequest is the wire shape for the pre-submission AI review.
// The backend grades quality, enriches the ticket and detects duplicates so the
// caller can show the customer actionable feedback before creating the ticket.
type ReviewSupportTicketRequest struct {
	Subject     string  `json:"subject"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	ClusterID   *string `json:"cluster_id,omitempty"`
	Source      string  `json:"source"`
}

// SupportTicketEnrichment is the AI-suggested summary/severity/category.
type SupportTicketEnrichment struct {
	Summary  *string `json:"summary,omitempty"`
	Severity *string `json:"severity,omitempty"`
	Category *string `json:"category,omitempty"`
}

// SupportDuplicateCandidate is an already-tracked ticket the review believes is
// the same problem. Linear identifiers are intentionally absent from the wire.
type SupportDuplicateCandidate struct {
	CandidateID     string `json:"candidate_id"`
	Summary         string `json:"summary"`
	StatusLabel     string `json:"status_label"`
	Confidence      string `json:"confidence"`
	AlreadyResolved bool   `json:"already_resolved"`
}

// SupportTicketReview mirrors the backend TicketReviewResult. Quality is either
// "pass" or "flag"; a flagged ticket needs acknowledgement to be submitted.
type SupportTicketReview struct {
	ReviewID            string                      `json:"review_id"`
	Enrichment          SupportTicketEnrichment     `json:"enrichment"`
	Quality             string                      `json:"quality"`
	QualityFlags        []string                    `json:"quality_flags"`
	ClarifyingQuestions []string                    `json:"clarifying_questions"`
	DuplicateCandidates []SupportDuplicateCandidate `json:"duplicate_candidates"`
	RecommendedAction   string                      `json:"recommended_action"`
	ExpiresAt           time.Time                   `json:"expires_at"`
}

type addSupportCommentRequest struct {
	Body string `json:"body"`
}

// ListSupportTicketsOptions carries the optional list filters.
type ListSupportTicketsOptions struct {
	Page     int
	PageSize int
	Status   []string
	Query    string
}

// ErrSupportTicketNotFound is returned when the backend reports 404.
var ErrSupportTicketNotFound = errors.New("support ticket not found")

// ErrSupportReviewRequired is returned when the backend flags the ticket in
// review (HTTP 409) and the request did not acknowledge the warnings. The CLI
// surfaces this with guidance to retry using --force.
var ErrSupportReviewRequired = errors.New("ticket flagged in review; retry with --force to submit anyway")

func (c *Client) CreateSupportTicket(ctx context.Context, req CreateSupportTicketRequest) (*SupportTicket, error) {
	url := c.BaseURL + "/api/v1/org/support/tickets"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doSupportRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var out SupportTicket
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) ReviewSupportTicket(ctx context.Context, req ReviewSupportTicketRequest) (*SupportTicketReview, error) {
	url := c.BaseURL + "/api/v1/org/support/tickets/review"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doSupportRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var out SupportTicketReview
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) ListSupportTickets(ctx context.Context, opts ListSupportTicketsOptions) (*SupportTicketListResponse, error) {
	query := neturl.Values{}
	if opts.Page > 0 {
		query.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		query.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	for _, status := range opts.Status {
		if status != "" {
			query.Add("status", status)
		}
	}
	if opts.Query != "" {
		query.Set("q", opts.Query)
	}
	url := c.BaseURL + "/api/v1/org/support/tickets"
	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}
	body, err := c.doSupportRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var out SupportTicketListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) GetSupportTicket(ctx context.Context, ticketID string) (*SupportTicket, error) {
	url := fmt.Sprintf("%s/api/v1/org/support/tickets/%s", c.BaseURL, neturl.PathEscape(ticketID))
	body, err := c.doSupportRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var out SupportTicket
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) CommentSupportTicket(ctx context.Context, ticketID, comment string) (*SupportTicket, error) {
	url := fmt.Sprintf("%s/api/v1/org/support/tickets/%s/comments", c.BaseURL, neturl.PathEscape(ticketID))
	payload, err := json.Marshal(addSupportCommentRequest{Body: comment})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doSupportRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var out SupportTicket
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) UploadSupportAttachment(ctx context.Context, ticketID, filePath string) (*SupportTicket, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("build form: %w", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		return nil, fmt.Errorf("write form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close form: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/org/support/tickets/%s/attachments", c.BaseURL, neturl.PathEscape(ticketID))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out SupportTicket
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		return &out, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrSupportTicketNotFound
	default:
		return nil, newUnexpectedResponseError("attachment upload failed", resp.StatusCode, truncateForError(respBody, 500))
	}
}

func (c *Client) CloseSupportTicket(ctx context.Context, ticketID string) (*SupportTicket, error) {
	url := fmt.Sprintf("%s/api/v1/org/support/tickets/%s/close", c.BaseURL, neturl.PathEscape(ticketID))
	body, err := c.doSupportRequest(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	var out SupportTicket
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// doSupportRequest is the shared transport helper for support-ticket calls. It
// maps 401/404 to friendly errors and 409 to ErrSupportReviewRequired, and
// otherwise returns the raw body for the caller to JSON-unmarshal.
func (c *Client) doSupportRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var request *http.Request
	var err error
	if body != nil {
		request, err = http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	} else {
		request, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return respBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrSupportTicketNotFound
	case http.StatusConflict:
		return nil, fmt.Errorf("%w: %s", ErrSupportReviewRequired, extractDetail(respBody))
	default:
		return nil, newUnexpectedResponseError("support request failed", resp.StatusCode, truncateForError(respBody, 500))
	}
}

func extractDetail(body []byte) string {
	var parsed struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Detail != "" {
		return parsed.Detail
	}
	return truncateForError(body, 300)
}
