package client

// Projects beneath organisations (ADR 0022): the bearer twins under
// /api/v1/org/projects. A project is a team-sized scope inside an
// organisation; every project route answers 404 "Projects are not enabled
// for this organisation" while the organisation's projects flag is off, and
// that one answer is what ErrProjectsDisabled stands for.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// projectsPath is the projects collection route.
const projectsPath = "/api/v1/org/projects"

// projectsDisabledDetail is the platform's refusal while the organisation
// has projects off, verbatim.
const projectsDisabledDetail = "Projects are not enabled for this organisation"

// projectNotFoundDetail is the platform's not-found for a project id the
// organisation does not have, verbatim.
const projectNotFoundDetail = "Project not found"

// ErrProjectsDisabled is the answer of every project route while the
// organisation has projects off.
var ErrProjectsDisabled = errors.New("projects are not enabled for this organisation")

// ErrProjectNotFound is a project id the organisation does not have.
var ErrProjectNotFound = errors.New("project not found")

// ErrProjectsRouteMissing is a platform that predates projects: the route
// is not registered at all, which answers a bare 404 with no detail.
var ErrProjectsRouteMissing = errors.New("this platform does not serve projects: " + projectsPath + " is not registered")

// ProjectSummary is one project as the listing and the detail document
// carry it. IsCurrent is true for the project the request acted in.
type ProjectSummary struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Slug        string `json:"slug" yaml:"slug"`
	Description string `json:"description" yaml:"description"`
	IsDefault   bool   `json:"is_default" yaml:"is_default"`
	IsCurrent   bool   `json:"is_current" yaml:"is_current"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

// ProjectMember is one member reaching a project, and how: Via is
// "organisation" (a membership role or organisation-wide assignment reaches
// every project) or "project" (an assignment scoped to this project).
type ProjectMember struct {
	AnkraUserID string `json:"ankra_user_id" yaml:"ankra_user_id"`
	Email       string `json:"email" yaml:"email"`
	Via         string `json:"via" yaml:"via"`
	RoleSlug    string `json:"role_slug" yaml:"role_slug"`
}

// CreateProjectRequest is the create payload. The slug is immutable once
// created: it ends up in kubeconfig entries, CLI config files and deep links.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

// UpdateProjectRequest is the patch payload; a nil field is left alone.
type UpdateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// SwitchProjectResponse is the answer of a project switch.
type SwitchProjectResponse struct {
	Success   bool   `json:"success" yaml:"success"`
	Message   string `json:"message" yaml:"message"`
	ProjectID string `json:"project_id" yaml:"project_id"`
}

// DeleteProjectResponse is the delete document: what the platform retired
// or repointed when the project went.
type DeleteProjectResponse struct {
	Success            bool `json:"success" yaml:"success"`
	RetiredAssignments int  `json:"retired_assignments" yaml:"retired_assignments"`
	RepointedMembers   int  `json:"repointed_members" yaml:"repointed_members"`
	PinnedTokens       int  `json:"pinned_tokens" yaml:"pinned_tokens"`
}

// ProjectMoveRequest moves one resource to another project of the
// organisation. The destination is a tri-state: a project id names the
// destination, OrganisationWide makes a shared kind (credentials,
// registries, backup vaults, buckets, organisation variables, notification
// routes, DNS zones) visible in every project, and neither leaves the
// platform to move it into the caller's current project.
type ProjectMoveRequest struct {
	ResourceKind     string
	ResourceID       string
	ProjectID        string
	OrganisationWide bool
}

// body renders the wire tri-state: a UUID, an explicit null, or no key.
func (request ProjectMoveRequest) body() map[string]any {
	body := map[string]any{
		"resource_kind": request.ResourceKind,
		"resource_id":   request.ResourceID,
	}
	switch {
	case request.OrganisationWide:
		body["project_id"] = nil
	case request.ProjectID != "":
		body["project_id"] = request.ProjectID
	}
	return body
}

// ProjectMoveResponse is the move document: where the resource came from
// and went, how many derived rows followed it per table, and how many
// notification routes pinned to another project were detached to
// organisation-wide. A nil project id is organisation-wide.
type ProjectMoveResponse struct {
	ResourceKind               string         `json:"resource_kind" yaml:"resource_kind"`
	ResourceID                 string         `json:"resource_id" yaml:"resource_id"`
	ResourceName               string         `json:"resource_name" yaml:"resource_name"`
	FromProjectID              *string        `json:"from_project_id" yaml:"from_project_id"`
	ToProjectID                *string        `json:"to_project_id" yaml:"to_project_id"`
	Followed                   map[string]int `json:"followed" yaml:"followed"`
	FollowedNotificationRoutes int            `json:"followed_notification_routes" yaml:"followed_notification_routes"`
}

// The move refusal codes the platform answers a 409 with.
const (
	ProjectMoveRefusalSameProject        = "same_project"
	ProjectMoveRefusalOperationsInFlight = "operations_in_flight"
	ProjectMoveRefusalFollowsCluster     = "follows_cluster"
)

// ProjectMoveRefusedError is a precondition the resource failed, as the
// platform's 409 carries it: the code lets a script act without parsing the
// message, Conflicts names what stands in the way (the running operations,
// the cluster a zone follows).
type ProjectMoveRefusedError struct {
	Code      string
	Detail    string
	Conflicts []string
}

func (e *ProjectMoveRefusedError) Error() string {
	if len(e.Conflicts) == 0 {
		return e.Detail
	}
	return fmt.Sprintf("%s (%s)", e.Detail, strings.Join(e.Conflicts, ", "))
}

// projectsError turns the project routes' answers into the errors the
// commands key on: the flag-off 404 into ErrProjectsDisabled, the route's
// own not-found into ErrProjectNotFound, a bare 404 into
// ErrProjectsRouteMissing, and a plain-detail 403 into a role refusal.
func projectsError(err error) error {
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) {
		return err
	}
	switch {
	case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == projectsDisabledDetail:
		return ErrProjectsDisabled
	case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == projectNotFoundDetail:
		return ErrProjectNotFound
	case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == "":
		return ErrProjectsRouteMissing
	case unexpected.StatusCode == http.StatusForbidden && unexpected.Detail != "":
		return &PermissionDeniedError{Detail: unexpected.Detail}
	}
	return err
}

// ListProjects returns the organisation's projects, the caller's current
// one marked.
// GET /api/v1/org/projects
func (c *Client) ListProjects() ([]ProjectSummary, error) {
	var response struct {
		Projects []ProjectSummary `json:"projects"`
	}
	if err := c.sendJSON(http.MethodGet, c.BaseURL+projectsPath, nil, &response); err != nil {
		return nil, projectsError(err)
	}
	if response.Projects == nil {
		response.Projects = []ProjectSummary{}
	}
	return response.Projects, nil
}

// GetCurrentProject returns the project this request acts in: the token's
// pin, the X-Ankra-Project-Id override, or the member's current project.
// GET /api/v1/org/projects/current
func (c *Client) GetCurrentProject() (*ProjectSummary, error) {
	var project ProjectSummary
	if err := c.sendJSON(http.MethodGet, c.BaseURL+projectsPath+"/current", nil, &project); err != nil {
		return nil, projectsError(err)
	}
	return &project, nil
}

// GetProject returns one project of the organisation by id.
// GET /api/v1/org/projects/{project_id}
func (c *Client) GetProject(projectID string) (*ProjectSummary, error) {
	var project ProjectSummary
	target := fmt.Sprintf("%s/api/v1/org/projects/%s", c.BaseURL, url.PathEscape(projectID))
	if err := c.sendJSON(http.MethodGet, target, nil, &project); err != nil {
		return nil, projectsError(err)
	}
	return &project, nil
}

// CreateProject adds a project; needs projects.manage.
// POST /api/v1/org/projects
func (c *Client) CreateProject(request CreateProjectRequest) (*ProjectSummary, error) {
	var project ProjectSummary
	if err := c.sendJSON(http.MethodPost, c.BaseURL+projectsPath, request, &project); err != nil {
		return nil, projectsError(err)
	}
	return &project, nil
}

// UpdateProject changes a project's name or description; the slug never
// changes.
// PATCH /api/v1/org/projects/{project_id}
func (c *Client) UpdateProject(projectID string, request UpdateProjectRequest) (*ProjectSummary, error) {
	var project ProjectSummary
	target := fmt.Sprintf("%s/api/v1/org/projects/%s", c.BaseURL, url.PathEscape(projectID))
	if err := c.sendJSON(http.MethodPatch, target, request, &project); err != nil {
		return nil, projectsError(err)
	}
	return &project, nil
}

// DeleteProject removes an empty, non-default project. A project that
// still holds resources is refused with a 409 naming what it holds.
// DELETE /api/v1/org/projects/{project_id}
func (c *Client) DeleteProject(projectID string) (*DeleteProjectResponse, error) {
	var response DeleteProjectResponse
	target := fmt.Sprintf("%s/api/v1/org/projects/%s", c.BaseURL, url.PathEscape(projectID))
	if err := c.sendJSON(http.MethodDelete, target, nil, &response); err != nil {
		return nil, projectsError(err)
	}
	return &response, nil
}

// SwitchProject moves the caller's server-side current project, the pointer
// the portal and the CLI share. A token pinned to a project cannot switch.
// POST /api/v1/org/projects/switch
func (c *Client) SwitchProject(projectID string) (*SwitchProjectResponse, error) {
	var response SwitchProjectResponse
	body := map[string]string{"project_id": projectID}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+projectsPath+"/switch", body, &response); err != nil {
		return nil, projectsError(err)
	}
	return &response, nil
}

// ListProjectMembers returns who reaches a project and how.
// GET /api/v1/org/projects/{project_id}/members
func (c *Client) ListProjectMembers(projectID string) ([]ProjectMember, error) {
	var response struct {
		Members []ProjectMember `json:"members"`
	}
	target := fmt.Sprintf("%s/api/v1/org/projects/%s/members", c.BaseURL, url.PathEscape(projectID))
	if err := c.sendJSON(http.MethodGet, target, nil, &response); err != nil {
		return nil, projectsError(err)
	}
	if response.Members == nil {
		response.Members = []ProjectMember{}
	}
	return response.Members, nil
}

// MoveResourceToProject moves one resource between projects, or to
// organisation-wide for a shared kind. A refused precondition is a
// *ProjectMoveRefusedError carrying the platform's code and conflicts; the
// request is read by hand because sendJSON keeps only the detail of a 409.
// POST /api/v1/org/projects/move
func (c *Client) MoveResourceToProject(request ProjectMoveRequest) (*ProjectMoveResponse, error) {
	encoded, marshalError := json.Marshal(request.body())
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	httpRequest, requestError := http.NewRequest(http.MethodPost, c.BaseURL+projectsPath+"/move", bytes.NewReader(encoded))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(httpRequest)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case response.StatusCode == http.StatusConflict:
		var refusal struct {
			Detail    string   `json:"detail"`
			Code      string   `json:"code"`
			Conflicts []string `json:"conflicts"`
		}
		if parseError := json.Unmarshal(body, &refusal); parseError == nil && refusal.Code != "" {
			return nil, &ProjectMoveRefusedError{Code: refusal.Code, Detail: refusal.Detail, Conflicts: refusal.Conflicts}
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("move failed", response.StatusCode, redactedBodyForError(body, 500))
	case response.StatusCode < 200 || response.StatusCode >= 300:
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, projectsError(newBackendDetailError(response.StatusCode, detail))
		}
		return nil, projectsError(newUnexpectedResponseError("move failed", response.StatusCode, redactedBodyForError(body, 500)))
	}
	var moved ProjectMoveResponse
	if parseError := json.Unmarshal(body, &moved); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	if moved.Followed == nil {
		moved.Followed = map[string]int{}
	}
	return &moved, nil
}

// ProjectShowbackRow is one project's share of the organisation's worker
// vCPU-hours over the window.
type ProjectShowbackRow struct {
	ProjectID         string  `json:"project_id" yaml:"project_id"`
	ProjectName       string  `json:"project_name" yaml:"project_name"`
	ClusterCount      int     `json:"cluster_count" yaml:"cluster_count"`
	CurrentWorkerVcpu int     `json:"current_worker_vcpu" yaml:"current_worker_vcpu"`
	WorkerVcpuHours   float64 `json:"worker_vcpu_hours" yaml:"worker_vcpu_hours"`
	SharePct          float64 `json:"share_pct" yaml:"share_pct"`
}

// ProjectShowback is the organisation's worker vCPU-hours split by project
// over the last WindowHours. Billing stays one invoice per organisation; this
// is showback, not a split bill.
type ProjectShowback struct {
	WindowHours                 int                  `json:"window_hours" yaml:"window_hours"`
	TotalWorkerVcpuHours        float64              `json:"total_worker_vcpu_hours" yaml:"total_worker_vcpu_hours"`
	UnattributedWorkerVcpuHours float64              `json:"unattributed_worker_vcpu_hours" yaml:"unattributed_worker_vcpu_hours"`
	UnattributedClusterCount    int                  `json:"unattributed_cluster_count" yaml:"unattributed_cluster_count"`
	Projects                    []ProjectShowbackRow `json:"projects" yaml:"projects"`
}

// GetProjectShowback reads the by-project usage split over the last hours
// (1 to 8760; 0 leaves the platform's default week).
// GET /api/v1/org/billing/analytics/by-project
func (c *Client) GetProjectShowback(hours int) (*ProjectShowback, error) {
	target := c.BaseURL + "/api/v1/org/billing/analytics/by-project"
	if hours > 0 {
		target += "?hours=" + url.QueryEscape(fmt.Sprint(hours))
	}
	var showback ProjectShowback
	if err := c.sendJSON(http.MethodGet, target, nil, &showback); err != nil {
		return nil, err
	}
	if showback.Projects == nil {
		showback.Projects = []ProjectShowbackRow{}
	}
	return &showback, nil
}
