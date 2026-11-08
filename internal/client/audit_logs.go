package client

// The organisation audit trail over GET
// /api/v1/org/organisation/audit-logs/export, the bearer twin of the
// session export (ankra-754b5). It is the one audit route an API token can
// reach: the paged listing and the per-cluster listing are session-only, so
// both the CLI's listing and its per-cluster view read this export, narrowed
// by the same filters, and the platform caps one export at 100,000 rows.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

const auditLogExportPath = "/api/v1/org/organisation/audit-logs/export"

// Audit log export formats the platform serves.
const (
	AuditLogFormatCSV  = "csv"
	AuditLogFormatJSON = "json"
)

// AuditLogActorTypes are the actor_type values the platform filters on.
var AuditLogActorTypes = []string{"agent", "human", "service_account", "system"}

// AuditLogOutcomes are the outcome values the platform filters on.
var AuditLogOutcomes = []string{"succeeded", "failed"}

// AuditLogFilters narrows the trail. Empty members are not sent.
type AuditLogFilters struct {
	Search       string
	UserID       string
	ClusterID    string
	ActorType    string
	Outcome      string
	AgentTaskID  string
	ActionType   string
	ResourceType string
	StartDate    *time.Time
	EndDate      *time.Time
}

// Query renders the filters as the export's query parameters.
func (filters AuditLogFilters) Query() neturl.Values {
	query := neturl.Values{}
	setIfPresent := func(name string, value string) {
		if value != "" {
			query.Set(name, value)
		}
	}
	setIfPresent("search", filters.Search)
	setIfPresent("user_id", filters.UserID)
	setIfPresent("cluster_id", filters.ClusterID)
	setIfPresent("actor_type", filters.ActorType)
	setIfPresent("outcome", filters.Outcome)
	setIfPresent("agent_task_id", filters.AgentTaskID)
	setIfPresent("action_type", filters.ActionType)
	setIfPresent("resource_type", filters.ResourceType)
	if filters.StartDate != nil {
		query.Set("start_date", filters.StartDate.UTC().Format(time.RFC3339))
	}
	if filters.EndDate != nil {
		query.Set("end_date", filters.EndDate.UTC().Format(time.RFC3339))
	}
	return query
}

// AuditLogEntry is one row of the audit trail, newest first.
type AuditLogEntry struct {
	CreatedAt    string         `json:"created_at" yaml:"created_at"`
	UserEmail    string         `json:"user_email" yaml:"user_email"`
	UserID       *string        `json:"user_id" yaml:"user_id"`
	ActionType   string         `json:"action_type" yaml:"action_type"`
	ResourceType string         `json:"resource_type" yaml:"resource_type"`
	ResourceID   *string        `json:"resource_id" yaml:"resource_id"`
	IPAddress    *string        `json:"ip_address" yaml:"ip_address"`
	ClusterID    *string        `json:"cluster_id" yaml:"cluster_id"`
	ClusterName  *string        `json:"cluster_name" yaml:"cluster_name"`
	ActorType    string         `json:"actor_type" yaml:"actor_type"`
	TargetKind   string         `json:"target_kind" yaml:"target_kind"`
	TargetName   string         `json:"target_name" yaml:"target_name"`
	Outcome      string         `json:"outcome" yaml:"outcome"`
	Details      map[string]any `json:"details" yaml:"details"`
}

// AuditLogTrail is the decoded JSON export: the matching rows and whether
// the platform's row cap cut them short.
type AuditLogTrail struct {
	Entries   []AuditLogEntry `json:"entries" yaml:"entries"`
	Truncated bool            `json:"truncated" yaml:"truncated"`
}

// AuditLogExportResult describes a finished download.
type AuditLogExportResult struct {
	Format       string
	BytesWritten int64
	Truncated    bool
}

// openAuditLogExport sends the export request and returns the response of a
// 2xx answer for the caller to read and close. The platform assembles the
// whole document before it answers, so the request rides the client without
// the shared 30s response-header deadline; the overall timeout still bounds it.
func (c *Client) openAuditLogExport(ctx context.Context, filters AuditLogFilters, format string) (*http.Response, error) {
	query := filters.Query()
	query.Set("format", format)
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+auditLogExportPath+"?"+query.Encode(), nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, sendError := c.httpClientForSlowWrite().Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer closeBody(response)
	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	return nil, auditLogResponseError(response.StatusCode, responseBody)
}

func auditLogResponseError(statusCode int, body []byte) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		if denied := PermissionDeniedFromResponse(statusCode, body); denied != nil {
			return denied
		}
		return &PermissionDeniedError{Permission: "audit.read", Detail: detailFromBody(body)}
	}
	if detail := detailFromBody(body); detail != "" {
		return newBackendDetailError(statusCode, detail)
	}
	if refusals := validationRefusals(body); refusals != "" {
		return newBackendDetailError(statusCode, refusals)
	}
	return newUnexpectedResponseError("audit log export failed", statusCode, redactedBodyForError(body, 500))
}

// validationRefusals renders a 422 in the pydantic list shape as
// "parameter: message" pairs.
func validationRefusals(body []byte) string {
	var parsed struct {
		Detail []struct {
			Location []any  `json:"loc"`
			Message  string `json:"msg"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Detail) == 0 {
		return ""
	}
	refusals := make([]string, 0, len(parsed.Detail))
	for _, refusal := range parsed.Detail {
		parameter := ""
		if len(refusal.Location) > 0 {
			parameter = fmt.Sprint(refusal.Location[len(refusal.Location)-1])
		}
		if parameter == "" {
			refusals = append(refusals, refusal.Message)
			continue
		}
		refusals = append(refusals, parameter+": "+refusal.Message)
	}
	return strings.Join(refusals, "; ")
}

// ListAuditLogs reads the trail matching filters as the JSON export.
func (c *Client) ListAuditLogs(ctx context.Context, filters AuditLogFilters) (*AuditLogTrail, error) {
	response, openError := c.openAuditLogExport(ctx, filters, AuditLogFormatJSON)
	if openError != nil {
		return nil, openError
	}
	defer closeBody(response)
	var trail AuditLogTrail
	if decodeError := json.NewDecoder(response.Body).Decode(&trail); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	if trail.Entries == nil {
		trail.Entries = []AuditLogEntry{}
	}
	return &trail, nil
}

// ExportAuditLogs streams the trail matching filters, in format, to
// destination as the platform renders it. A CSV says it was cut short in a
// response header, a JSON document in its truncated member; both are
// reported on the result.
func (c *Client) ExportAuditLogs(ctx context.Context, filters AuditLogFilters, format string,
	destination io.Writer) (*AuditLogExportResult, error) {
	response, openError := c.openAuditLogExport(ctx, filters, format)
	if openError != nil {
		return nil, openError
	}
	defer closeBody(response)
	result := &AuditLogExportResult{Format: format}
	counter := &countingWriter{destination: destination}
	if format == AuditLogFormatJSON {
		teedBody := io.TeeReader(response.Body, counter)
		var marker struct {
			Truncated bool `json:"truncated"`
		}
		if decodeError := json.NewDecoder(teedBody).Decode(&marker); decodeError != nil {
			return nil, fmt.Errorf("parse response: %w", decodeError)
		}
		if _, drainError := io.Copy(io.Discard, teedBody); drainError != nil {
			return nil, fmt.Errorf("read response: %w", drainError)
		}
		result.Truncated = marker.Truncated
	} else {
		if _, copyError := io.Copy(counter, response.Body); copyError != nil {
			return nil, fmt.Errorf("read response: %w", copyError)
		}
		result.Truncated = strings.EqualFold(response.Header.Get("X-Ankra-Export-Truncated"), "true")
	}
	result.BytesWritten = counter.written
	return result, nil
}

type countingWriter struct {
	destination io.Writer
	written     int64
}

func (writer *countingWriter) Write(data []byte) (int, error) {
	count, writeError := writer.destination.Write(data)
	writer.written += int64(count)
	return count, writeError
}
