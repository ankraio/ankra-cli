package client

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

const (
	environmentsBasePath = "/api/v1/org/environments"
	hostTargetsBasePath  = "/api/v1/org/host-targets"
	deploymentsBasePath  = "/api/v1/org/deployments"
)

// Environment is one named deploy environment of the organisation. Host
// targets register into an environment and deploy stages name one.
type Environment struct {
	ID        string `json:"id" yaml:"id"`
	Name      string `json:"name" yaml:"name"`
	CreatedAt string `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// EnvironmentList is the GET /environments answer.
type EnvironmentList struct {
	Environments []Environment `json:"environments" yaml:"environments"`
}

// CreateHostJoinTokenRequest is the POST .../host-join-tokens body.
// TTLSeconds zero leaves the lifetime to the platform (one hour); the
// platform refuses anything above 24 hours.
type CreateHostJoinTokenRequest struct {
	TTLSeconds int `json:"ttl_seconds,omitempty"`
}

// HostJoinToken is the single-use token a host registers with. The platform
// returns JoinToken exactly once and stores only its hash.
type HostJoinToken struct {
	JoinToken   string `json:"join_token" yaml:"join_token"`
	ExpiresAt   string `json:"expires_at" yaml:"expires_at"`
	Environment string `json:"environment" yaml:"environment"`
}

// HostTargetRelease is one release a host reported as running.
type HostTargetRelease struct {
	Release     string `json:"release" yaml:"release"`
	Digest      string `json:"digest" yaml:"digest"`
	ActivatedAt string `json:"activated_at,omitempty" yaml:"activated_at,omitempty"`
}

// HostTarget is a host registered as a deploy target. The host-reported
// fields stay nil until the agent's first report; Status is derived by the
// platform from the last heartbeat: online, offline, or unknown when the
// agent never checked in.
type HostTarget struct {
	ID                string              `json:"id" yaml:"id"`
	Name              string              `json:"name" yaml:"name"`
	Environment       string              `json:"environment" yaml:"environment"`
	EnvironmentID     string              `json:"environment_id,omitempty" yaml:"environment_id,omitempty"`
	Labels            map[string]string   `json:"labels,omitempty" yaml:"labels,omitempty"`
	Status            string              `json:"status" yaml:"status"`
	AgentVersion      *string             `json:"agent_version,omitempty" yaml:"agent_version,omitempty"`
	Hostname          *string             `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	OS                *string             `json:"os,omitempty" yaml:"os,omitempty"`
	Arch              *string             `json:"arch,omitempty" yaml:"arch,omitempty"`
	SupportedJobTypes []string            `json:"supported_job_types,omitempty" yaml:"supported_job_types,omitempty"`
	Policy            any                 `json:"policy,omitempty" yaml:"policy,omitempty"`
	Running           []HostTargetRelease `json:"running,omitempty" yaml:"running,omitempty"`
	LastHeartbeatAt   *string             `json:"last_heartbeat_at,omitempty" yaml:"last_heartbeat_at,omitempty"`
	RegisteredAt      *string             `json:"registered_at,omitempty" yaml:"registered_at,omitempty"`
	RevokedAt         *string             `json:"revoked_at,omitempty" yaml:"revoked_at,omitempty"`
	RevokedBy         *string             `json:"revoked_by,omitempty" yaml:"revoked_by,omitempty"`
	CreatedBy         *string             `json:"created_by,omitempty" yaml:"created_by,omitempty"`
	CreatedAt         string              `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt         string              `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// IsRevoked reports whether the platform revoked the target.
func (target HostTarget) IsRevoked() bool {
	return target.RevokedAt != nil && *target.RevokedAt != ""
}

// HostTargetList is the GET /host-targets answer.
type HostTargetList struct {
	HostTargets []HostTarget `json:"host_targets" yaml:"host_targets"`
}

// DeploymentTarget is one host's job within a deployment; its id is the job
// id the host agent claims.
type DeploymentTarget struct {
	ID             string  `json:"id" yaml:"id"`
	HostTargetID   string  `json:"host_target_id" yaml:"host_target_id"`
	HostTargetName string  `json:"host_target_name,omitempty" yaml:"host_target_name,omitempty"`
	Status         string  `json:"status" yaml:"status"`
	Attempt        int     `json:"attempt" yaml:"attempt"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	ClaimedAt      *string `json:"claimed_at,omitempty" yaml:"claimed_at,omitempty"`
	HeartbeatAt    *string `json:"heartbeat_at,omitempty" yaml:"heartbeat_at,omitempty"`
	TimesOutAt     *string `json:"times_out_at,omitempty" yaml:"times_out_at,omitempty"`
	FinishedAt     *string `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	PreviousDigest *string `json:"previous_digest,omitempty" yaml:"previous_digest,omitempty"`
	RunningDigest  *string `json:"running_digest,omitempty" yaml:"running_digest,omitempty"`
	Result         any     `json:"result,omitempty" yaml:"result,omitempty"`
	ErrorClass     *string `json:"error_class,omitempty" yaml:"error_class,omitempty"`
	ErrorMessage   *string `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	LogArtifactID  *string `json:"log_artifact_id,omitempty" yaml:"log_artifact_id,omitempty"`
}

// Deployment is one release of a published artefact digest to the active
// host targets of an environment. Targets is filled by GET
// /deployments/{deployment_id} only.
type Deployment struct {
	ID                 string             `json:"id" yaml:"id"`
	Environment        string             `json:"environment" yaml:"environment"`
	EnvironmentID      string             `json:"environment_id,omitempty" yaml:"environment_id,omitempty"`
	RepositoryID       *string            `json:"repository_id,omitempty" yaml:"repository_id,omitempty"`
	Repository         *string            `json:"repository,omitempty" yaml:"repository,omitempty"`
	PipelineRunID      *string            `json:"pipeline_run_id,omitempty" yaml:"pipeline_run_id,omitempty"`
	PipelineStepID     *string            `json:"pipeline_step_id,omitempty" yaml:"pipeline_step_id,omitempty"`
	StepAttempt        int                `json:"step_attempt,omitempty" yaml:"step_attempt,omitempty"`
	ReleaseName        string             `json:"release_name" yaml:"release_name"`
	ArtifactRepository string             `json:"artifact_repository" yaml:"artifact_repository"`
	ArtifactDigest     string             `json:"artifact_digest" yaml:"artifact_digest"`
	ArtifactTag        *string            `json:"artifact_tag,omitempty" yaml:"artifact_tag,omitempty"`
	SignatureKeyID     *string            `json:"signature_key_id,omitempty" yaml:"signature_key_id,omitempty"`
	CommitSHA          *string            `json:"commit_sha,omitempty" yaml:"commit_sha,omitempty"`
	State              string             `json:"state" yaml:"state"`
	TargetCount        int                `json:"target_count" yaml:"target_count"`
	SucceededCount     int                `json:"succeeded_count" yaml:"succeeded_count"`
	FailedCount        int                `json:"failed_count" yaml:"failed_count"`
	ErrorClass         *string            `json:"error_class,omitempty" yaml:"error_class,omitempty"`
	ErrorMessage       *string            `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	RequestedBy        *string            `json:"requested_by,omitempty" yaml:"requested_by,omitempty"`
	StartedAt          *string            `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	FinishedAt         *string            `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	CreatedAt          string             `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt          string             `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Targets            []DeploymentTarget `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// DeploymentList is one page of GET /deployments, newest first.
type DeploymentList struct {
	Deployments []Deployment `json:"deployments" yaml:"deployments"`
	NextCursor  *string      `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
}

// ListDeploymentsOptions is the GET /deployments query. Empty fields are
// left out of the query.
type ListDeploymentsOptions struct {
	Environment  string
	RepositoryID string
	Cursor       string
	Limit        int
}

// ListEnvironments returns the organisation's deploy environments.
// GET /api/v1/org/environments
func (c *Client) ListEnvironments(ctx context.Context) (*EnvironmentList, error) {
	var result EnvironmentList
	if requestError := c.sendJSONContext(ctx, http.MethodGet, c.BaseURL+environmentsBasePath, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreateHostJoinToken mints a single-use join token for the environment,
// which the platform creates when it does not exist yet. The token is in the
// answer exactly once.
// POST /api/v1/org/environments/{environment_name}/host-join-tokens
func (c *Client) CreateHostJoinToken(ctx context.Context, environmentName string,
	request CreateHostJoinTokenRequest) (*HostJoinToken, error) {
	endpoint := fmt.Sprintf("%s%s/%s/host-join-tokens", c.BaseURL, environmentsBasePath, neturl.PathEscape(environmentName))
	var result HostJoinToken
	if requestError := c.sendJSONContext(ctx, http.MethodPost, endpoint, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListHostTargets returns the organisation's host targets, narrowed to one
// environment when environmentName is not empty.
// GET /api/v1/org/host-targets?environment=
func (c *Client) ListHostTargets(ctx context.Context, environmentName string) (*HostTargetList, error) {
	endpoint := c.BaseURL + hostTargetsBasePath
	if environmentName != "" {
		endpoint += "?" + neturl.Values{"environment": []string{environmentName}}.Encode()
	}
	var result HostTargetList
	if requestError := c.sendJSONContext(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetHostTarget returns one host target by id.
// GET /api/v1/org/host-targets/{host_target_id}
func (c *Client) GetHostTarget(ctx context.Context, hostTargetID string) (*HostTarget, error) {
	endpoint := fmt.Sprintf("%s%s/%s", c.BaseURL, hostTargetsBasePath, neturl.PathEscape(hostTargetID))
	var result HostTarget
	if requestError := c.sendJSONContext(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RevokeHostTarget revokes a host target: its identity token and every
// session stop working, and it receives no further deploy jobs.
// POST /api/v1/org/host-targets/{host_target_id}/revoke
func (c *Client) RevokeHostTarget(ctx context.Context, hostTargetID string) (*HostTarget, error) {
	endpoint := fmt.Sprintf("%s%s/%s/revoke", c.BaseURL, hostTargetsBasePath, neturl.PathEscape(hostTargetID))
	var result HostTarget
	if requestError := c.sendJSONContext(ctx, http.MethodPost, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListDeployments returns one page of the organisation's deployments.
// GET /api/v1/org/deployments?environment=&repository_id=&cursor=&limit=
func (c *Client) ListDeployments(ctx context.Context, options ListDeploymentsOptions) (*DeploymentList, error) {
	query := neturl.Values{}
	if options.Environment != "" {
		query.Set("environment", options.Environment)
	}
	if options.RepositoryID != "" {
		query.Set("repository_id", options.RepositoryID)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	endpoint := c.BaseURL + deploymentsBasePath
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var result DeploymentList
	if requestError := c.sendJSONContext(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetDeployment returns one deployment with its per-host targets.
// GET /api/v1/org/deployments/{deployment_id}
func (c *Client) GetDeployment(ctx context.Context, deploymentID string) (*Deployment, error) {
	endpoint := fmt.Sprintf("%s%s/%s", c.BaseURL, deploymentsBasePath, neturl.PathEscape(deploymentID))
	var result Deployment
	if requestError := c.sendJSONContext(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
