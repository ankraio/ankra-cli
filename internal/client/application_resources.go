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
	// Components selects which of a monorepo's components the demo runs,
	// each with optional overrides; omitted deploys every recorded
	// component. EntryComponent names the one that owns the demo host's
	// root path; omitted applies the backend's entry heuristic.
	Components     []DeployApplicationDemoComponent `json:"components,omitempty"`
	EntryComponent *string                          `json:"entry_component,omitempty"`
}

// DeployApplicationDemoComponent is one components[] entry of the deploy-demo
// body: a selected component plus the overrides that apply to it alone.
type DeployApplicationDemoComponent struct {
	Name          string  `json:"name"`
	ImageTag      *string `json:"image_tag,omitempty"`
	ContainerPort *int    `json:"container_port,omitempty"`
	IngressPath   *string `json:"ingress_path,omitempty"`
}

// UpdateApplicationImageRegistryRequest mirrors the image-registry body. The
// key is always sent: an explicit null is how the declaration is cleared and
// the application handed back to the organisation's own registry project, so
// the pointer must survive marshalling rather than being omitted.
type UpdateApplicationImageRegistryRequest struct {
	ImageRegistry *ApplicationImageRegistry `json:"image_registry"`
}

// ApplicationImageRegistry is the declared registry an application publishes
// to and Ankra reads its images back from.
type ApplicationImageRegistry struct {
	URL                  string `json:"url"`
	CredentialName       string `json:"credential_name,omitempty"`
	APIURL               string `json:"api_url,omitempty"`
	PullSecretName       string `json:"pull_secret_name,omitempty"`
	UsernameSecretName   string `json:"username_secret_name,omitempty"`
	PasswordSecretName   string `json:"password_secret_name,omitempty"`
	ManageActionsSecrets bool   `json:"manage_actions_secrets,omitempty"`
	// AdminCredentialName names a registry credential with project
	// administrator rights on the declared project, which is what lets Ankra
	// mint and rotate the application's own push robot on a registry the
	// organisation operates.
	AdminCredentialName string `json:"admin_credential_name,omitempty"`
	// FlatRepositories publishes monorepo components as <project>/<component>
	// instead of <project>/<app>/<component>, matching a registry laid out
	// flat before Ankra.
	FlatRepositories bool `json:"flat_repositories,omitempty"`
	// ComponentRepositories names a component's repository inside the project
	// outright, keyed by component name.
	ComponentRepositories map[string]string `json:"component_repositories,omitempty"`
}

// SetApplicationRepositoryCredentialRequest mirrors the repository-credential
// body: the GitHub credential the application's repository calls ride on.
type SetApplicationRepositoryCredentialRequest struct {
	CredentialName string `json:"credential_name"`
}

// EnsureApplicationRegistryRobotRequest mirrors the registry-robot body:
// rotate refreshes the secret of a robot that already backs the application.
type EnsureApplicationRegistryRobotRequest struct {
	Rotate bool `json:"rotate"`
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

func (client *Client) GetApplicationDemoDetail(requestContext context.Context, applicationID string, workspaceID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/demos/"+workspaceID+"/detail"), nil, nil)
}

// GetApplicationDemoLogs fetches a bounded log tail. An empty podName lets
// the backend pick the demo's own pod, which is the single-component case;
// a multi-component demo has one pod per component and must name the one it
// wants. The tail parameter is `tail_lines` — the endpoint ignores anything
// else, so a demo's tail size silently defaulted while the CLI sent `tail`.
func (client *Client) GetApplicationDemoLogs(requestContext context.Context, applicationID string, workspaceID string, podName string, tailLines int) (json.RawMessage, error) {
	query := url.Values{}
	if tailLines > 0 {
		query.Set("tail_lines", strconv.Itoa(tailLines))
	}
	if podName != "" {
		query.Set("pod", podName)
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/demos/"+workspaceID+"/logs"), query, nil)
}

func (client *Client) GetApplicationDemoConfig(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/demo-config"), nil, nil)
}

func (client *Client) UpdateApplicationDemoConfig(requestContext context.Context, applicationID string, configuration json.RawMessage) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationPath(applicationID, "/demo-config"), nil, configuration)
}

func (client *Client) FixApplicationDemo(requestContext context.Context, applicationID string, workspaceID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/demos/"+workspaceID+"/fix"), nil, nil)
}

// --- image registry ---

func (client *Client) GetApplicationImageRegistry(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/image-registry"), nil, nil)
}

func (client *Client) UpdateApplicationImageRegistry(requestContext context.Context, applicationID string, registryRequest UpdateApplicationImageRegistryRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationPath(applicationID, "/image-registry"), nil, registryRequest)
}

// --- registry robot ---

func (client *Client) GetApplicationRegistryRobot(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/registry-robot"), nil, nil)
}

func (client *Client) EnsureApplicationRegistryRobot(requestContext context.Context, applicationID string, robotRequest EnsureApplicationRegistryRobotRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/registry-robot"), nil, robotRequest)
}

func (client *Client) RevokeApplicationRegistryRobot(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete, applicationPath(applicationID, "/registry-robot"), nil, nil)
}

// --- repository credential ---

func (client *Client) GetApplicationRepositoryCredential(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/repository-credential"), nil, nil)
}

func (client *Client) SetApplicationRepositoryCredential(requestContext context.Context, applicationID string, credentialRequest SetApplicationRepositoryCredentialRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationPath(applicationID, "/repository-credential"), nil, credentialRequest)
}

// --- AI lane configuration ---

func (client *Client) GetApplicationAIConfig(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/ai-config"), nil, nil)
}

func (client *Client) UpdateApplicationAIConfig(requestContext context.Context, applicationID string, configuration json.RawMessage) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationPath(applicationID, "/ai-config"), nil, configuration)
}

func (client *Client) ResetApplicationAIConfig(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete, applicationPath(applicationID, "/ai-config"), nil, nil)
}

// --- catalog publishing ---

// PublishApplicationAddonRequest mirrors the publish-addon body; every field
// is optional and defaults from the application's descriptor.
type PublishApplicationAddonRequest struct {
	Version     string `json:"version,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Changelog   string `json:"changelog,omitempty"`
}

func (client *Client) PublishApplicationAddon(requestContext context.Context, applicationID string, publishRequest PublishApplicationAddonRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost, applicationPath(applicationID, "/publish-addon"), nil, publishRequest)
}

func (client *Client) GetApplicationPublishedAddon(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/published-addon"), nil, nil)
}

// --- environment secrets ---

// SetApplicationEnvSecretRequest mirrors the env-secret PUT body. The value is
// the only inbound secret on this surface and there is no route that hands a
// stored value back, so nothing here is ever populated from a response.
type SetApplicationEnvSecretRequest struct {
	Value string `json:"value"`
}

// ListApplicationEnvSecrets reports the keys an application's generated
// manifests need and the state of each. Values never travel outbound.
func (client *Client) ListApplicationEnvSecrets(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/env-secrets"), nil, nil)
}

func (client *Client) SetApplicationEnvSecret(requestContext context.Context, applicationID string, secretKey string, value string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut,
		applicationPath(applicationID, "/env-secrets/"+url.PathEscape(secretKey)), nil,
		SetApplicationEnvSecretRequest{Value: value})
}

func (client *Client) DeleteApplicationEnvSecret(requestContext context.Context, applicationID string, secretKey string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete,
		applicationPath(applicationID, "/env-secrets/"+url.PathEscape(secretKey)), nil, nil)
}

// ApplyApplicationEnvSecrets re-seals the stored values into every deployment
// of the application and rolls the workloads that read them. It carries no
// body: the values it applies are the ones already stored.
func (client *Client) ApplyApplicationEnvSecrets(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost,
		applicationPath(applicationID, "/env-secrets/apply"), nil, nil)
}

// --- push-to-deploy switch ---

// SetApplicationAutoDeployRequest mirrors the auto-deploy PUT body.
type SetApplicationAutoDeployRequest struct {
	Enabled bool `json:"enabled"`
}

func (client *Client) GetApplicationAutoDeploy(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/auto-deploy"), nil, nil)
}

func (client *Client) SetApplicationAutoDeploy(requestContext context.Context, applicationID string, enabled bool) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationPath(applicationID, "/auto-deploy"), nil,
		SetApplicationAutoDeployRequest{Enabled: enabled})
}

// --- organisation-level application settings ---

// UpdateApplicationSettingsRequest mirrors the settings PUT body. The key is
// always sent and the pointer must survive marshalling: an explicit null is
// how the organisation's runner choice is cleared, which is a different
// request from not mentioning the field at all (the backend rejects that with
// a 422 missing-field error).
type UpdateApplicationSettingsRequest struct {
	CIRunnerLabel *string `json:"ci_runner_label"`
}

func (client *Client) GetApplicationSettings(requestContext context.Context) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, applicationsAPIPath+"/settings", nil, nil)
}

func (client *Client) UpdateApplicationSettings(requestContext context.Context, ciRunnerLabel *string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPut, applicationsAPIPath+"/settings", nil,
		UpdateApplicationSettingsRequest{CIRunnerLabel: ciRunnerLabel})
}

// --- branch build repair ---

// FixApplicationBuildRequest mirrors the fix-build body: the branch whose
// build is missing.
type FixApplicationBuildRequest struct {
	Branch string `json:"branch"`
}

// FixApplicationBuild dispatches the branch-build repair lane. It answers with
// a pointer to the dispatched mission rather than its result, so the caller
// follows the agent run it names.
func (client *Client) FixApplicationBuild(requestContext context.Context, applicationID string, branch string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost,
		applicationPath(applicationID, "/demos/fix-build"), nil,
		FixApplicationBuildRequest{Branch: branch})
}
