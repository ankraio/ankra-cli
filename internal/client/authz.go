package client

// Platform RBAC admin surface (backend ADR 0007 + cluster-groups
// extension): roles (with bundled Kubernetes access profiles), scoped role
// assignments, and cluster groups.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// KubeGrant is one Kubernetes access level bundled into a role.
type KubeGrant struct {
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace,omitempty"`
	K8sRole   string  `json:"k8s_role"`
}

// RoleDocument is one role in the /org/organisation/roles listing.
type RoleDocument struct {
	ID          *string     `json:"id,omitempty"`
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Builtin     bool        `json:"builtin"`
	Permissions []string    `json:"permissions"`
	KubeGrants  []KubeGrant `json:"kube_grants,omitempty"`
}

type rolesResponse struct {
	Roles []RoleDocument `json:"roles"`
}

// CreateCustomRoleRequest is the custom-role create payload.
type CreateCustomRoleRequest struct {
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Permissions []string    `json:"permissions"`
	KubeGrants  []KubeGrant `json:"kube_grants"`
}

// RoleAssignment is one scoped role assignment.
type RoleAssignment struct {
	ID          string  `json:"id"`
	AnkraUserID string  `json:"ankra_user_id"`
	RoleSlug    *string `json:"role_slug"`
	RoleID      *string `json:"role_id"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *string `json:"scope_id"`
	CreatedAt   string  `json:"created_at"`
}

type assignmentsResponse struct {
	Assignments []RoleAssignment `json:"assignments"`
}

// CreateRoleAssignmentRequest is the assignment create payload; exactly one
// of RoleSlug / RoleID is set.
type CreateRoleAssignmentRequest struct {
	AnkraUserID string  `json:"ankra_user_id"`
	RoleSlug    *string `json:"role_slug,omitempty"`
	RoleID      *string `json:"role_id,omitempty"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *string `json:"scope_id,omitempty"`
}

// ClusterGroup is one cluster group.
type ClusterGroup struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Slug           string            `json:"slug"`
	Description    string            `json:"description"`
	MembershipMode string            `json:"membership_mode"`
	Selector       map[string]string `json:"selector,omitempty"`
	CreatedAt      string            `json:"created_at"`
}

type clusterGroupsResponse struct {
	Groups []ClusterGroup `json:"groups"`
}

// CreateClusterGroupRequest is the group create payload.
type CreateClusterGroupRequest struct {
	Name           string            `json:"name"`
	Slug           string            `json:"slug"`
	Description    string            `json:"description,omitempty"`
	MembershipMode string            `json:"membership_mode"`
	Selector       map[string]string `json:"selector,omitempty"`
}

type clusterGroupMembershipResponse struct {
	ClusterIDs []string `json:"cluster_ids"`
}

// sendJSON issues an authenticated JSON request and decodes the response
// into target (nil target discards the body). Non-2xx responses surface the
// backend's detail string, and RBAC 403s map to PermissionDeniedError.
func (c *Client) sendJSON(method string, url string, payload any, target any) error {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	request, requestError := http.NewRequest(method, url, bodyReader)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return denied
		}
		if detail := detailFromBody(body); detail != "" {
			return newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
		}
		return newUnexpectedResponseError("request failed", response.StatusCode, redactedBodyForError(body, 500))
	}
	if target == nil {
		return nil
	}
	if unmarshalError := json.Unmarshal(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

// OrganisationUser is one row of the organisation detail's users array
// (id is the platform user UUID that assignments target).
type OrganisationUser struct {
	ID          *string `json:"id"`
	Email       *string `json:"email"`
	Status      *string `json:"status"`
	Role        *string `json:"role"`
	UserCurrent *bool   `json:"user_current"`
}

// ListOrganisationUsers returns the organisation's members with their
// platform user ids, for resolving assignment targets by email.
func (c *Client) ListOrganisationUsers(orgID string) ([]OrganisationUser, error) {
	var response struct {
		Users []OrganisationUser `json:"users"`
	}
	url := fmt.Sprintf("%s/api/v1/org/organisation/%s", c.BaseURL, orgID)
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}
	return response.Users, nil
}

// ListRoles returns the built-in and custom roles of the organisation.
func (c *Client) ListRoles() ([]RoleDocument, error) {
	var response rolesResponse
	if err := c.getJSON(c.BaseURL+"/api/v1/org/organisation/roles", &response); err != nil {
		return nil, err
	}
	return response.Roles, nil
}

// CreateCustomRole creates an organisation custom role (optionally with a
// bundled Kubernetes access profile).
func (c *Client) CreateCustomRole(request CreateCustomRoleRequest) (*RoleDocument, error) {
	var created RoleDocument
	if err := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/org/organisation/roles", request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// ListMemberAssignments returns a member's live role assignments.
func (c *Client) ListMemberAssignments(ankraUserID string) ([]RoleAssignment, error) {
	var response assignmentsResponse
	url := fmt.Sprintf("%s/api/v1/org/organisation/members/%s/assignments", c.BaseURL, ankraUserID)
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}
	return response.Assignments, nil
}

// CreateRoleAssignment grants a role at organisation, cluster, or
// cluster-group scope.
func (c *Client) CreateRoleAssignment(request CreateRoleAssignmentRequest) (*RoleAssignment, error) {
	var created RoleAssignment
	if err := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/org/organisation/assignments", request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteRoleAssignment revokes an assignment.
func (c *Client) DeleteRoleAssignment(assignmentID string) error {
	url := fmt.Sprintf("%s/api/v1/org/organisation/assignments/%s", c.BaseURL, assignmentID)
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// ListClusterGroups returns the organisation's cluster groups.
func (c *Client) ListClusterGroups() ([]ClusterGroup, error) {
	var response clusterGroupsResponse
	if err := c.getJSON(c.BaseURL+"/api/v1/org/organisation/cluster-groups", &response); err != nil {
		return nil, err
	}
	return response.Groups, nil
}

// CreateClusterGroup creates a cluster group (static or dynamic).
func (c *Client) CreateClusterGroup(request CreateClusterGroupRequest) (*ClusterGroup, error) {
	var created ClusterGroup
	if err := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/org/organisation/cluster-groups", request, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// SetClusterGroupMembers replaces a static group's pinned cluster list.
func (c *Client) SetClusterGroupMembers(groupID string, clusterIDs []string) error {
	url := fmt.Sprintf("%s/api/v1/org/organisation/cluster-groups/%s/members", c.BaseURL, groupID)
	payload := struct {
		ClusterIDs []string `json:"cluster_ids"`
	}{ClusterIDs: clusterIDs}
	return c.sendJSON(http.MethodPut, url, payload, nil)
}

// SetClusterGroupSelector switches a group to dynamic label-selector
// membership.
func (c *Client) SetClusterGroupSelector(groupID string, selector map[string]string) error {
	url := fmt.Sprintf("%s/api/v1/org/organisation/cluster-groups/%s/selector", c.BaseURL, groupID)
	payload := struct {
		Selector map[string]string `json:"selector"`
	}{Selector: selector}
	return c.sendJSON(http.MethodPut, url, payload, nil)
}

// PreviewClusterGroup resolves the group's current member cluster ids.
func (c *Client) PreviewClusterGroup(groupID string) ([]string, error) {
	var response clusterGroupMembershipResponse
	url := fmt.Sprintf("%s/api/v1/org/organisation/cluster-groups/%s/membership", c.BaseURL, groupID)
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}
	return response.ClusterIDs, nil
}
