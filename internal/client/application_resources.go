package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const applicationsAPIPath = "/api/v1/org/applications"

// DeployApplicationRequest mirrors the platform deploy_application body.
type DeployApplicationRequest struct {
	ClusterID  string            `json:"cluster_id"`
	Namespace  string            `json:"namespace,omitempty"`
	DeployMode string            `json:"deploy_mode,omitempty"`
	Inputs     map[string]string `json:"inputs,omitempty"`
}

// SetApplicationPackageVisibilityRequest mirrors the set_package_visibility
// body.
type SetApplicationPackageVisibilityRequest struct {
	Kind       string `json:"kind"`
	Visibility string `json:"visibility"`
}

// ApplicationFileUpdate mirrors a single FileUpdate entry.
type ApplicationFileUpdate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// UpdateApplicationFilesRequest mirrors the update_application_files body.
type UpdateApplicationFilesRequest struct {
	Files         []ApplicationFileUpdate `json:"files"`
	DeletedPaths  []string                `json:"deleted_paths,omitempty"`
	CommitMessage string                  `json:"commit_message,omitempty"`
}

// DeployApplicationDemoRequest mirrors the deploy-demo body. Every field is
// optional; only the flags the user set are sent.
type DeployApplicationDemoRequest struct {
	Branch        *string `json:"branch,omitempty"`
	PRNumber      *int    `json:"pr_number,omitempty"`
	ImageTag      *string `json:"image_tag,omitempty"`
	TTLHours      *int    `json:"ttl_hours,omitempty"`
	ContainerPort *int    `json:"container_port,omitempty"`
}

// applicationResourceRequest performs a bearer-authenticated request against
// an application subresource and returns the raw JSON body on success. The
// FastAPI `detail` string is surfaced as the error message on non-200 so the
// CLI can print the backend's human-readable reason, and the HTTP status is
// preserved for exit-code classification.
func (client *Client) applicationResourceRequest(
	requestContext context.Context,
	method string,
	path string,
	query url.Values,
	requestBody any,
) (json.RawMessage, error) {
	var bodyReader io.Reader
	if requestBody != nil {
		encoded, marshalError := json.Marshal(requestBody)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	requestURL := client.BaseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	request, requestError := http.NewRequestWithContext(requestContext, method, requestURL, bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, sendError := client.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode != http.StatusOK {
		if permissionDenied := PermissionDeniedFromResponse(response.StatusCode, responseBody); permissionDenied != nil {
			return nil, permissionDenied
		}
		message := detailFromBody(responseBody)
		if message == "" {
			message = "application request failed"
		}
		return nil, newUnexpectedResponseError(
			message,
			response.StatusCode,
			redactedBodyForError(responseBody, 500),
		)
	}
	return json.RawMessage(responseBody), nil
}

func applicationPath(applicationID string, suffix string) string {
	return applicationsAPIPath + "/" + applicationID + suffix
}

// --- lifecycle reads / writes ---

func (client *Client) ListApplicationsRaw(requestContext context.Context, page int, pageSize int, search string) (json.RawMessage, error) {
	query := url.Values{}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	if search != "" {
		query.Set("search", search)
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationsAPIPath, query, nil)
}

func (client *Client) GetApplicationRaw(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, ""), nil, nil)
}

func (client *Client) GetApplicationJobs(requestContext context.Context, applicationID string, page int, pageSize int) (json.RawMessage, error) {
	query := url.Values{}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/jobs"), query, nil)
}

func (client *Client) RetryApplication(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/retry"), nil, nil)
}

func (client *Client) ReconcileApplication(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/reconcile"), nil, nil)
}

func (client *Client) DeleteApplication(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete, applicationPath(applicationID, ""), nil, nil)
}

func (client *Client) DeployApplication(requestContext context.Context, applicationID string, deployRequest DeployApplicationRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/deploy"), nil, deployRequest)
}

// --- deployment / installation / chart reads ---

func (client *Client) GetApplicationDeployments(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/deployments"), nil, nil)
}

func (client *Client) GetApplicationInstallations(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/installations"), nil, nil)
}

func (client *Client) GetApplicationChartVersions(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/chart-versions"), nil, nil)
}

func (client *Client) GetApplicationExistingPlatform(requestContext context.Context, applicationID string, clusterID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet,
		applicationPath(applicationID, "/deploy/clusters/"+clusterID+"/platform"), nil, nil)
}

// --- workflow (CI/CD) reads / writes ---

func (client *Client) GetApplicationWorkflowRuns(requestContext context.Context, applicationID string, status string, page int, pageSize int) (json.RawMessage, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/workflow-runs"), query, nil)
}

func (client *Client) GetApplicationWorkflowRunJobs(requestContext context.Context, applicationID string, runID int64) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet,
		applicationPath(applicationID, "/workflow-runs/"+strconv.FormatInt(runID, 10)+"/jobs"), nil, nil)
}

func (client *Client) RerunApplicationWorkflowRun(requestContext context.Context, applicationID string, runID int64) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost,
		applicationPath(applicationID, "/workflow-runs/"+strconv.FormatInt(runID, 10)+"/rerun"), nil, nil)
}

func (client *Client) GetApplicationPullRequestReviews(requestContext context.Context, applicationID string, limit int) (json.RawMessage, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/pull-request-reviews"), query, nil)
}

func (client *Client) UpgradeApplicationWorkflow(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/upgrade-workflow"), nil, nil)
}

// --- repository files ---

func (client *Client) GetApplicationBranches(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/branches"), nil, nil)
}

func (client *Client) GetApplicationBranchFiles(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/branch-files"), nil, nil)
}

func (client *Client) UpdateApplicationFiles(requestContext context.Context, applicationID string, filesRequest UpdateApplicationFilesRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPatch, applicationPath(applicationID, "/files"), nil, filesRequest)
}

// --- security / publishing ---

func (client *Client) GetApplicationPublishReadiness(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/publish-readiness"), nil, nil)
}

func (client *Client) GetApplicationContainerSecurity(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/container-security"), nil, nil)
}

func (client *Client) GetApplicationCodeSecurity(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/code-security"), nil, nil)
}

func (client *Client) GetApplicationPackageVisibility(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/package-visibility"), nil, nil)
}

func (client *Client) SetApplicationPackageVisibility(requestContext context.Context, applicationID string, visibilityRequest SetApplicationPackageVisibilityRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/package-visibility"), nil, visibilityRequest)
}

func (client *Client) MakeApplicationPackagesPublic(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/make-public"), nil, nil)
}

// --- demos ---

func (client *Client) GetApplicationDemos(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/demos"), nil, nil)
}

func (client *Client) CheckApplicationDemoBuild(requestContext context.Context, applicationID string, branch string) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("branch", branch)
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/demos/build"), query, nil)
}

func (client *Client) DeployApplicationDemo(requestContext context.Context, applicationID string, demoRequest DeployApplicationDemoRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/demos"), nil, demoRequest)
}

func (client *Client) StopApplicationDemo(requestContext context.Context, applicationID string, workspaceID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete, applicationPath(applicationID, "/demos/"+workspaceID), nil, nil)
}
