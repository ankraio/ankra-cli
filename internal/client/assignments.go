package client

// RBAC role assignments (platform ADR 0007) and the token twin of the
// membership role change. An assignment grants a member a role at one
// scope: the whole organisation, one cluster, one cluster group or one
// project. Both routes answer through the organisation the request is
// scoped to.

import (
	"errors"
	"net/http"
	"strings"
)

// roleAssignmentsPath is the assignments collection route.
const roleAssignmentsPath = "/api/v1/org/organisation/assignments"

// memberRolePath is the bearer twin of the console's membership role change.
const memberRolePath = "/api/v1/org/organisation/member/role"

// ErrMemberRoleRouteMissing is a platform that does not yet serve the
// membership role change to API tokens: the route is not registered, which
// answers a bare 404 or a 405.
var ErrMemberRoleRouteMissing = errors.New("this platform does not support changing a member's role with a token yet")

// ErrRoleAssignmentsRouteMissing is a platform that predates the
// assignments API for API tokens.
var ErrRoleAssignmentsRouteMissing = errors.New("this platform does not serve role assignments to API tokens: " + roleAssignmentsPath + " is not registered")

// CreateRoleAssignmentRequest is the create payload. Exactly one of RoleSlug
// (a built-in role) or RoleID (an organisation custom role) is set; ScopeID
// is nil for the organisation scope and required for every other scope.
type CreateRoleAssignmentRequest struct {
	AnkraUserID string  `json:"ankra_user_id"`
	RoleSlug    *string `json:"role_slug,omitempty"`
	RoleID      *string `json:"role_id,omitempty"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *string `json:"scope_id"`
}

// RoleAssignment is one live assignment as the platform returns it.
type RoleAssignment struct {
	ID          string  `json:"id" yaml:"id"`
	AnkraUserID string  `json:"ankra_user_id" yaml:"ankra_user_id"`
	RoleSlug    *string `json:"role_slug" yaml:"role_slug"`
	RoleID      *string `json:"role_id" yaml:"role_id"`
	ScopeType   string  `json:"scope_type" yaml:"scope_type"`
	ScopeID     *string `json:"scope_id" yaml:"scope_id"`
	CreatedAt   string  `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

// UpdateMemberRoleRequest changes a member's organisation membership role.
// ProjectID and ProjectRoleSlug are only sent with Role "project".
type UpdateMemberRoleRequest struct {
	UserID          string  `json:"user_id"`
	OrganisationID  string  `json:"organisation_id"`
	Role            string  `json:"role"`
	ProjectID       *string `json:"project_id,omitempty"`
	ProjectRoleSlug *string `json:"project_role_slug,omitempty"`
}

// UpdateMemberRoleResponse is the platform's answer to a role change.
type UpdateMemberRoleResponse struct {
	Success bool   `json:"success" yaml:"success"`
	Message string `json:"message" yaml:"message"`
}

// CreateRoleAssignment grants a member a role at one scope.
// POST /api/v1/org/organisation/assignments
func (c *Client) CreateRoleAssignment(request CreateRoleAssignmentRequest) (*RoleAssignment, error) {
	var assignment RoleAssignment
	if err := c.sendJSON(http.MethodPost, c.BaseURL+roleAssignmentsPath, request, &assignment); err != nil {
		if isRouteMissing(err) {
			return nil, ErrRoleAssignmentsRouteMissing
		}
		return nil, err
	}
	return &assignment, nil
}

// UpdateMemberRole changes a member's membership role, including to a
// project-only membership.
// PATCH /api/v1/org/organisation/member/role
func (c *Client) UpdateMemberRole(request UpdateMemberRoleRequest) (*UpdateMemberRoleResponse, error) {
	var response UpdateMemberRoleResponse
	if err := c.sendJSON(http.MethodPatch, c.BaseURL+memberRolePath, request, &response); err != nil {
		if isRouteMissing(err) {
			return nil, ErrMemberRoleRouteMissing
		}
		return nil, err
	}
	return &response, nil
}

// isRouteMissing reports an answer that comes from the router rather than a
// handler: a 405, or a 404 without the API's detail (or with the router's
// generic one). A handler's own 404 always names what it did not find.
func isRouteMissing(err error) bool {
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) {
		return false
	}
	switch unexpected.StatusCode {
	case http.StatusMethodNotAllowed:
		return true
	case http.StatusNotFound:
		detail := strings.TrimSpace(unexpected.Detail)
		return detail == "" || strings.EqualFold(detail, "Not Found")
	}
	return false
}
