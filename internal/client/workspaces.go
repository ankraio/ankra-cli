package client

// Ankra Workspaces (cluster go/internal/workspacesapi): a member's long-lived
// pod on the organisation's CI pool that `ankra exec` runs a coding agent's
// heavy commands in. Every call rides the bearer-token lane
// (/api/v1/org/workspaces...), the twin of the browser /org routes.
//
// Lifecycle (ankra-b5c3as.6.3):
//
//	POST   /api/v1/org/workspaces                 bring one up (idempotent)
//	GET    /api/v1/org/workspaces                 list
//	GET    /api/v1/org/workspaces/{workspace_id}  read one (moves provisioning on)
//	DELETE /api/v1/org/workspaces/{workspace_id}  tear one down (answers it)
//
// Runs (ankra-b5c3as.6.4) are in workspace_runs.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

const workspacesBasePath = "/api/v1/org/workspaces"

// The states a workspace moves through.
const (
	WorkspaceStatusProvisioning = "provisioning"
	WorkspaceStatusReady        = "ready"
	WorkspaceStatusFailed       = "failed"
	WorkspaceStatusTearingDown  = "tearing_down"
	WorkspaceStatusDestroyed    = "destroyed"
)

// Workspace is one workspace as the API answers it. ProgressMessage says
// what a provisioning workspace is waiting on (no node can take its pod yet,
// a container restarting, the preflight's last answer); it is nil otherwise.
type Workspace struct {
	ID                   string     `json:"id" yaml:"id"`
	OrganisationID       string     `json:"organisation_id" yaml:"organisation_id"`
	UserID               string     `json:"user_id" yaml:"user_id"`
	RepositoryID         string     `json:"repository_id" yaml:"repository_id"`
	Kind                 string     `json:"kind" yaml:"kind"`
	SandboxEnvironmentID *string    `json:"sandbox_environment_id" yaml:"sandbox_environment_id"`
	ClusterID            *string    `json:"cluster_id" yaml:"cluster_id"`
	Namespace            *string    `json:"namespace" yaml:"namespace"`
	PodName              *string    `json:"pod_name" yaml:"pod_name"`
	Image                *string    `json:"image" yaml:"image"`
	ImageSource          *string    `json:"image_source" yaml:"image_source"`
	SpecHash             *string    `json:"spec_hash" yaml:"spec_hash"`
	Status               string     `json:"status" yaml:"status"`
	LastError            *string    `json:"last_error" yaml:"last_error"`
	ProgressMessage      *string    `json:"progress_message" yaml:"progress_message"`
	IdleTTLHours         int        `json:"idle_ttl_hours" yaml:"idle_ttl_hours"`
	LastUsedAt           time.Time  `json:"last_used_at" yaml:"last_used_at"`
	ExpiresAt            *time.Time `json:"expires_at" yaml:"expires_at"`
	CreatedAt            time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" yaml:"updated_at"`
}

// WorkspaceList is GET /org/workspaces.
type WorkspaceList struct {
	Items    []Workspace `json:"items" yaml:"items"`
	IsCapped bool        `json:"is_capped" yaml:"is_capped"`
}

// WorkspaceUpRequest names the workspace POST /org/workspaces brings up: the
// repository by id, or by the provider, owner and name read off a git remote.
type WorkspaceUpRequest struct {
	RepositoryID string `json:"repository_id,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Owner        string `json:"owner,omitempty"`
	Name         string `json:"name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	IdleTTLHours int    `json:"idle_ttl_hours,omitempty"`
}

// ListWorkspacesOptions is the GET /org/workspaces query.
type ListWorkspacesOptions struct {
	Kind         string
	RepositoryID string
	// AllMembers lists every member's workspaces (workspaces.manage).
	AllMembers bool
}

// WorkspaceAPIError is a workspace route's refusal: its status, the frozen
// degraded-state code when it carried one (CLUSTER_OFFLINE, NO_AGENT,
// AGENT_TIMEOUT, SANDBOX_MODE, WORKSPACE_NOT_READY), the platform's own
// sentence and the Retry-After it asked for.
type WorkspaceAPIError struct {
	StatusCode        int
	Code              string
	Detail            string
	RetryAfterSeconds int
}

func (workspaceError *WorkspaceAPIError) Error() string {
	if workspaceError == nil {
		return ""
	}
	detail := workspaceError.Detail
	if detail == "" {
		detail = fmt.Sprintf("workspace request failed with status %d", workspaceError.StatusCode)
	}
	if workspaceError.Code != "" {
		return fmt.Sprintf("%s (%s)", detail, workspaceError.Code)
	}
	return detail
}

// IsRetryable reports whether the refusal is a passing state worth trying
// again: a degraded cluster or agent, a workspace that is not ready yet, a
// gateway error.
func (workspaceError *WorkspaceAPIError) IsRetryable() bool {
	if workspaceError == nil {
		return false
	}
	switch workspaceError.Code {
	case "CLUSTER_OFFLINE", "NO_AGENT", "AGENT_TIMEOUT", "WORKSPACE_NOT_READY":
		return true
	}
	switch workspaceError.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// workspaceErrorFromResponse maps a non-2xx workspace response: 401 and the
// RBAC 403 keep their shared errors (exit codes 6 and 7), everything else is
// a *WorkspaceAPIError carrying the detail and the code.
func workspaceErrorFromResponse(statusCode int, body []byte, retryAfterHeader string) error {
	if statusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if denied := PermissionDeniedFromResponse(statusCode, body); denied != nil {
		return denied
	}
	var envelope struct {
		Detail     json.RawMessage `json:"detail"`
		ErrorCode  string          `json:"error_code"`
		RetryAfter *int            `json:"retry_after"`
	}
	workspaceError := &WorkspaceAPIError{StatusCode: statusCode}
	if unmarshalError := json.Unmarshal(body, &envelope); unmarshalError == nil {
		workspaceError.Code = envelope.ErrorCode
		var detail string
		if json.Unmarshal(envelope.Detail, &detail) == nil {
			workspaceError.Detail = detail
		} else if message := pipelineValidationDetailFromBody(body); message != "" {
			workspaceError.Detail = message
		}
		if envelope.RetryAfter != nil {
			workspaceError.RetryAfterSeconds = *envelope.RetryAfter
		}
	}
	if workspaceError.Detail == "" && len(bytes.TrimSpace(body)) > 0 {
		workspaceError.Detail = fmt.Sprintf("status %d: %s", statusCode, redactedBodyForError(body, 300))
	}
	if workspaceError.RetryAfterSeconds == 0 {
		if seconds, parseError := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); parseError == nil && seconds > 0 {
			workspaceError.RetryAfterSeconds = seconds
		}
	}
	return workspaceError
}

// doWorkspaceRequest sends one JSON request on the token lane and decodes a
// 2xx answer into target, returning the status code.
func (c *Client) doWorkspaceRequest(ctx context.Context, method string, endpoint string,
	payload any, target any) (int, error) {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return 0, fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if requestError != nil {
		return 0, fmt.Errorf("create request: %w", requestError)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return 0, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return response.StatusCode, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, workspaceErrorFromResponse(response.StatusCode, body,
			response.Header.Get("Retry-After"))
	}
	if target == nil || len(bytes.TrimSpace(body)) == 0 {
		return response.StatusCode, nil
	}
	if unmarshalError := json.Unmarshal(body, target); unmarshalError != nil {
		return response.StatusCode, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return response.StatusCode, nil
}

func (c *Client) workspaceEndpoint(segments ...string) string {
	endpoint := c.BaseURL + workspacesBasePath
	for _, segment := range segments {
		endpoint += "/" + neturl.PathEscape(segment)
	}
	return endpoint
}

// UpWorkspace brings the caller's workspace for a repository and kind up, or
// answers the live one (POST /org/workspaces). created is true when this
// call created it.
func (c *Client) UpWorkspace(ctx context.Context, request WorkspaceUpRequest) (*Workspace, bool, error) {
	var workspace Workspace
	statusCode, requestError := c.doWorkspaceRequest(ctx, http.MethodPost, c.workspaceEndpoint(), request, &workspace)
	if requestError != nil {
		return nil, false, requestError
	}
	return &workspace, statusCode == http.StatusCreated, nil
}

// ListWorkspaces lists the caller's live workspaces (GET /org/workspaces).
func (c *Client) ListWorkspaces(ctx context.Context, options ListWorkspacesOptions) (*WorkspaceList, error) {
	query := neturl.Values{}
	if options.Kind != "" {
		query.Set("kind", options.Kind)
	}
	if options.RepositoryID != "" {
		query.Set("repository_id", options.RepositoryID)
	}
	if options.AllMembers {
		query.Set("all", "true")
	}
	endpoint := c.workspaceEndpoint()
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var listed WorkspaceList
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodGet, endpoint, nil, &listed); requestError != nil {
		return nil, requestError
	}
	return &listed, nil
}

// GetWorkspace reads one workspace (GET /org/workspaces/{id}). Reading a
// provisioning workspace runs its preflight server-side and moves it on to
// ready or failed.
func (c *Client) GetWorkspace(ctx context.Context, workspaceID string) (*Workspace, error) {
	var workspace Workspace
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodGet, c.workspaceEndpoint(workspaceID), nil,
		&workspace); requestError != nil {
		return nil, requestError
	}
	return &workspace, nil
}

// DeleteWorkspace tears one workspace down and answers it as it is now
// (DELETE /org/workspaces/{id}).
func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) (*Workspace, error) {
	var workspace Workspace
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodDelete, c.workspaceEndpoint(workspaceID), nil,
		&workspace); requestError != nil {
		return nil, requestError
	}
	return &workspace, nil
}

// IsWorkspaceNotFound reports whether err is a workspace route's 404.
func IsWorkspaceNotFound(err error) bool {
	var workspaceError *WorkspaceAPIError
	return errors.As(err, &workspaceError) && workspaceError.StatusCode == http.StatusNotFound
}
