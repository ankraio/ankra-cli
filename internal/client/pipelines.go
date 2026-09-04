package client

// Ankra Pipelines (ankra-vn0bd.2.8, WS-B item B8): the typed client for
// go/internal/pipelineapi on the cluster-api - runs, the definition of
// record, and cron schedules - plus the organisation-scoped repository
// onboarding routes cluster PRs #2490 and #2509 added (ankra-vn0bd.4.2,
// WS-D item D2): connect (optionally linking an application and a CI cluster
// override), list, get and disconnect a connected repository.
//
// The server mounts every selector-addressed route four times (session/token
// twin x by-application/by-repository twin); this client only ever speaks
// the bearer-PAT twin, and PipelineSelector picks which of the two addresses
// a call uses. The repository routes are mounted differently
// (mountOrganisation, not mountScoped): they address the organisation alone,
// so ConnectPipelineRepository, ListPipelineRepositories,
// GetPipelineRepository and DisconnectPipelineRepository take no
// PipelineSelector.
//
// PipelineSelector.RepositoryID still takes the repository's id, not
// "owner/name": the new listing filters by provider, not by owner/name, so
// resolving a name the way resolveApplicationID does for applications would
// mean paging the whole listing client-side. Left for an item that wants
// that enough to pay for it.
//
// Findings have no route yet either (WS-C item C5): there is no
// ListPipelineFindings here, deliberately, until the server has one.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
)

// PipelineSelector addresses one pipeline: either the repository directly, or
// the application it is linked to. Exactly one of the two must be set; the
// CLI's flag parsing enforces that before a selector ever reaches the client.
type PipelineSelector struct {
	ApplicationID string
	RepositoryID  string
}

// ErrPipelineSelectorRequired refuses a call whose selector names neither a
// repository nor an application.
var ErrPipelineSelectorRequired = errors.New("a pipeline repository id or an application is required")

func (selector PipelineSelector) basePath() (string, error) {
	switch {
	case selector.RepositoryID != "":
		return "/api/v1/org/pipeline-repositories/" + neturl.PathEscape(selector.RepositoryID), nil
	case selector.ApplicationID != "":
		return "/api/v1/org/applications/" + neturl.PathEscape(selector.ApplicationID), nil
	default:
		return "", ErrPipelineSelectorRequired
	}
}

// PipelineRun is the wire shape of a `pipeline_runs` row
// (go/internal/pipelineapi/runs.go pipelineRunResponse).
type PipelineRun struct {
	ID                string  `json:"id"`
	RunID             string  `json:"run_id"`
	OrganisationID    string  `json:"organisation_id"`
	RepositoryID      string  `json:"repository_id"`
	ApplicationID     *string `json:"application_id"`
	ClusterID         *string `json:"cluster_id"`
	DefinitionID      string  `json:"definition_id"`
	RunNumber         int64   `json:"run_number"`
	Trigger           string  `json:"trigger"`
	TriggerRef        string  `json:"trigger_ref"`
	HeadSHA           string  `json:"head_sha"`
	BaseSHA           string  `json:"base_sha"`
	PullRequestNumber *int64  `json:"pull_request_number"`
	IsFork            bool    `json:"is_fork"`
	ConcurrencyGroup  string  `json:"concurrency_group"`
	Status            string  `json:"status"`
	Outcome           *string `json:"outcome"`
	ErrorClass        *string `json:"error_class"`
	ErrorMessage      *string `json:"error_message"`
	RequestedBy       string  `json:"requested_by"`
	RerunOfRunID      *string `json:"rerun_of_run_id"`
	QueuedAt          string  `json:"queued_at"`
	StartedAt         *string `json:"started_at"`
	FinishedAt        *string `json:"finished_at"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

// PipelineStep is the wire shape of a `pipeline_run_steps` row.
type PipelineStep struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	Stage           string          `json:"stage"`
	StepKey         string          `json:"step_key"`
	DisplayName     string          `json:"display_name"`
	Kind            string          `json:"kind"`
	Matrix          json.RawMessage `json:"matrix"`
	DependsOn       []string        `json:"depends_on"`
	Status          string          `json:"status"`
	Outcome         *string         `json:"outcome"`
	ContinueOnError bool            `json:"continue_on_error"`
	ExitCode        *int32          `json:"exit_code"`
	Attempt         int16           `json:"attempt"`
	TimeoutSeconds  int32           `json:"timeout_seconds"`
	Executor        string          `json:"executor"`
	ExecutionID     *string         `json:"execution_id"`
	ExecutionStepID *string         `json:"execution_step_id"`
	NodeName        string          `json:"node_name"`
	CacheResult     string          `json:"cache_result"`
	Outputs         json.RawMessage `json:"outputs"`
	ErrorClass      *string         `json:"error_class"`
	ErrorMessage    *string         `json:"error_message"`
	StartedAt       *string         `json:"started_at"`
	FinishedAt      *string         `json:"finished_at"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// PipelineRunList is the GET …/pipeline-runs body.
type PipelineRunList struct {
	Runs       []PipelineRun `json:"runs"`
	NextCursor *string       `json:"next_cursor"`
}

// PipelineRunDetail is the GET …/pipeline-runs/{run_id} body: the run plus
// its planned steps.
type PipelineRunDetail struct {
	PipelineRun
	Steps []PipelineStep `json:"steps"`
}

// ListPipelineRunsOptions is the GET …/pipeline-runs query.
type ListPipelineRunsOptions struct {
	Status  string
	Trigger string
	Branch  string
	HeadSHA string
	Cursor  string
	Limit   int
}

// CreatePipelineRunRequest is the POST …/pipeline-runs body: a manual
// dispatch. HeadSHA is mandatory - the server refuses ErrHeadSHARequired
// without it, because resolving a ref to a commit is the trigger lane's job,
// not the dispatch route's.
type CreatePipelineRunRequest struct {
	Ref      string            `json:"ref,omitempty"`
	HeadSHA  string            `json:"head_sha"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	SpecYAML string            `json:"spec_yaml,omitempty"`
}

// CreatePipelineRunResult is the 202 body a dispatch or a re-run answers.
type CreatePipelineRunResult struct {
	RunID         string `json:"run_id"`
	PipelineRunID string `json:"pipeline_run_id"`
	RunNumber     int64  `json:"run_number"`
}

// PipelineArtifact is the wire shape one stored run artifact will carry
// (go/internal/pipelineapi/artifacts.go). The store behind this route is WS-C
// item C1; until it lands the listing always answers empty and the download
// always answers 404.
type PipelineArtifact struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id"`
	StepID      string `json:"step_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
}

// PipelineArtifactList is the GET …/pipeline-runs/{run_id}/artifacts body.
type PipelineArtifactList struct {
	Artifacts []PipelineArtifact `json:"artifacts"`
}

// PipelineRepositoryReference names the repository a definition belongs to,
// carried on the definition response so a caller who addressed by
// application learns which repository answered.
type PipelineRepositoryReference struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	Owner         string  `json:"owner"`
	Name          string  `json:"name"`
	DefaultBranch string  `json:"default_branch"`
	ApplicationID *string `json:"application_id"`
}

// PipelineStage is one stage of a resolved definition, flattened across its
// three declaration blocks (Section is "stages", "on_failure" or "finally").
type PipelineStage struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Section string   `json:"section"`
	Needs   []string `json:"needs"`
}

// PipelineDefinition is the GET/PUT …/pipeline body: the definition of
// record. ProtectedHash is nil until the fork-policy lane (WS-D) computes it.
type PipelineDefinition struct {
	Source        string                      `json:"source"`
	SpecYAML      string                      `json:"spec_yaml"`
	SpecHash      string                      `json:"spec_hash"`
	ProtectedHash *string                     `json:"protected_hash"`
	Stages        []PipelineStage             `json:"stages"`
	Violations    []string                    `json:"violations"`
	Repository    PipelineRepositoryReference `json:"repository"`
}

// PipelinePlannedStep is one node of a dry-run DAG.
type PipelinePlannedStep struct {
	StepKey        string   `json:"step_key"`
	Stage          string   `json:"stage"`
	Kind           string   `json:"kind"`
	DependsOn      []string `json:"depends_on"`
	RunCondition   string   `json:"run_condition"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// PipelineSkippedStage is one stage a dry run would not execute.
type PipelineSkippedStage struct {
	Stage   string `json:"stage"`
	StepKey string `json:"step_key"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// PipelineEventPlan is the planner's answer for one synthetic event
// (`validate` probes a push and a pull request against the default branch).
type PipelineEventPlan struct {
	Event          string                 `json:"event"`
	Run            bool                   `json:"run"`
	Reason         *string                `json:"reason"`
	MatchedTrigger *string                `json:"matched_trigger"`
	Steps          []PipelinePlannedStep  `json:"steps"`
	Skipped        []PipelineSkippedStage `json:"skipped"`
	Diagnostics    []string               `json:"diagnostics"`
}

// PipelineValidation is the POST …/pipeline/validate body: a dry run that
// writes nothing.
type PipelineValidation struct {
	Severity   string              `json:"severity"`
	Violations []string            `json:"violations"`
	Events     []PipelineEventPlan `json:"events"`
}

// PipelineSchedule is the wire shape of a `pipeline_schedules` row.
// NextFireAt is null until the (not yet built) dispatch loop computes one,
// which is not the same as "never fires".
type PipelineSchedule struct {
	ID           string          `json:"id"`
	RepositoryID string          `json:"repository_id"`
	Cron         string          `json:"cron"`
	Timezone     string          `json:"timezone"`
	Ref          string          `json:"ref"`
	Inputs       json.RawMessage `json:"inputs"`
	Enabled      bool            `json:"enabled"`
	NextFireAt   *string         `json:"next_fire_at"`
	LastFiredAt  *string         `json:"last_fired_at"`
	LastRunID    *string         `json:"last_run_id"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

// PipelineScheduleList is the GET …/pipeline/schedules body.
type PipelineScheduleList struct {
	Schedules []PipelineSchedule `json:"schedules"`
}

// CreatePipelineScheduleRequest is the POST …/pipeline/schedules body.
type CreatePipelineScheduleRequest struct {
	Cron     string            `json:"cron"`
	Timezone string            `json:"timezone,omitempty"`
	Ref      string            `json:"ref,omitempty"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
}

// UpdatePipelineScheduleRequest is the PUT …/pipeline/schedules/{id} body.
// Every member is a pointer so an omitted field leaves the stored value
// alone - the server contract, mirrored here.
type UpdatePipelineScheduleRequest struct {
	Cron     *string            `json:"cron,omitempty"`
	Timezone *string            `json:"timezone,omitempty"`
	Ref      *string            `json:"ref,omitempty"`
	Inputs   *map[string]string `json:"inputs,omitempty"`
	Enabled  *bool              `json:"enabled,omitempty"`
}

// PipelineRepository is the wire shape of a `pipeline_repositories` row
// (go/internal/pipelineapi/repositories.go repositoryResponse): what
// `pipeline repositories list|get|connect|disconnect` (ankra-vn0bd.4.2)
// address. ApplicationID links the repository to an application inside the
// organisation without changing that application's own pipeline_source
// disposition; ClusterID overrides the organisation's declared CI cluster
// (`GET /org/ci-settings`) for this repository's pipelines. Both are nil
// when the connect named none - a repository's pipelines then fall back to
// the organisation's setting.
type PipelineRepository struct {
	ID             string  `json:"id"`
	OrganisationID string  `json:"organisation_id"`
	Provider       string  `json:"provider"`
	Owner          string  `json:"owner"`
	Name           string  `json:"name"`
	CredentialName string  `json:"credential_name"`
	DefaultBranch  string  `json:"default_branch"`
	ApplicationID  *string `json:"application_id"`
	ClusterID      *string `json:"cluster_id"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// PipelineRepositoryDefinitionOutcome is what a connect did with the
// repository's committed pipeline file (go/internal/pipelineapi/repositories.go
// definitionBootstrapResponse): one of the pipelineonboard.Definition*
// statuses ("recorded", "already_recorded", "absent", "unreadable",
// "invalid", "unknown"), the sentence behind it, the definition of record
// when one was recorded, and - when the committed file could not be read -
// why. A read failure never fails the connect itself: Status answers
// "unreadable" or "unknown" instead, and ReadError names the cause.
type PipelineRepositoryDefinitionOutcome struct {
	Status       string   `json:"status"`
	Detail       string   `json:"detail"`
	DefinitionID *string  `json:"definition_id"`
	SpecHash     *string  `json:"spec_hash"`
	Violations   []string `json:"violations"`
	ReadError    *string  `json:"read_error"`
}

// ConnectPipelineRepositoryRequest is the POST /pipelines/repositories body.
// CredentialName and DefaultBranch are optional: an absent credential
// connects the repository without reading its committed pipeline file (the
// connect result's Definition says so), and an absent branch takes the
// server's "main" default (pipelines.DefaultRepositoryBranch).
// ApplicationID and ClusterID are both optional and both addressed by id, not
// by name: an empty string connects no application and stores no CI cluster
// override. The server refuses (422) an application outside the organisation,
// a cluster outside the organisation, or a cluster whose agent has not
// advertised it can run pipeline steps
// (go/internal/usecase/pipelines/repositories.go validateRepositoryLinks).
type ConnectPipelineRepositoryRequest struct {
	Provider       string `json:"provider"`
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	CredentialName string `json:"credential_name,omitempty"`
	DefaultBranch  string `json:"default_branch,omitempty"`
	ApplicationID  string `json:"application_id,omitempty"`
	ClusterID      string `json:"cluster_id,omitempty"`
}

// ConnectPipelineRepositoryResult is the 201 body: the connected repository
// plus what the connect did with its committed pipeline file.
type ConnectPipelineRepositoryResult struct {
	PipelineRepository
	Definition PipelineRepositoryDefinitionOutcome `json:"definition"`
}

// PipelineRepositoryList is the GET /pipelines/repositories body, newest
// first. NextCursor is null when the page was the last one.
type PipelineRepositoryList struct {
	Repositories []PipelineRepository `json:"repositories"`
	NextCursor   *string              `json:"next_cursor"`
}

// ListPipelineRepositoriesOptions is the GET /pipelines/repositories query.
type ListPipelineRepositoriesOptions struct {
	// Provider filters to one provider; empty lists every provider.
	Provider string
	Cursor   string
	Limit    int
}

// PipelineRepositoryAlreadyConnectedError is the 409
// go/internal/pipelineapi/repositories.go writeRepositoryError answers for a
// repository the organisation already connected: the server's sentence
// (which already names the existing repository's id in prose) plus that id
// as its own field, so a caller does not have to parse the sentence to act
// on what it already has.
type PipelineRepositoryAlreadyConnectedError struct {
	Detail       string
	RepositoryID string
}

func (alreadyConnected *PipelineRepositoryAlreadyConnectedError) Error() string {
	if alreadyConnected == nil {
		return ""
	}
	return alreadyConnected.Detail
}

// PipelineValidationError is the platform's structured 422 for a dispatch or
// a definition write the planner refused
// (go/internal/pipelineapi/pipelineapi.go writePipelineError,
// pipelines.PlanRefusedError): the frozen reason, plus every diagnostic the
// planner recorded on the way there.
type PipelineValidationError struct {
	StatusCode  int
	Reason      string
	Diagnostics []string
}

func (validationError *PipelineValidationError) Error() string {
	if validationError == nil {
		return ""
	}
	if len(validationError.Diagnostics) == 0 {
		return validationError.Reason
	}
	return validationError.Reason + "\n  - " + strings.Join(validationError.Diagnostics, "\n  - ")
}

// PipelineLogStreamUnavailableError marks the step log relay's degraded-state
// 503 (go/internal/pipelineapi/streams.go logStreamUnavailableCode): the
// stream cannot be reached right now, distinct from a step whose log is
// simply empty. RetryAfterSeconds is the server's Retry-After, defaulting to
// 10 when the header is absent or does not parse.
type PipelineLogStreamUnavailableError struct {
	Detail            string
	RetryAfterSeconds int
}

func (unavailableError *PipelineLogStreamUnavailableError) Error() string {
	if unavailableError == nil {
		return ""
	}
	return unavailableError.Detail
}

// pipelineErrorFromResponse maps one non-2xx pipeline API response onto a
// typed or detail-carrying error. It is shared by every pipeline request path
// (JSON and SSE alike) so the mapping cannot drift between them.
func pipelineErrorFromResponse(statusCode int, body []byte, retryAfterHeader string) error {
	if statusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if denied := PermissionDeniedFromResponse(statusCode, body); denied != nil {
		return denied
	}

	// The repository-already-connected shape
	// (go/internal/pipelineapi/repositories.go writeRepositoryError):
	// {"detail": "...", "repository_id": "..."}. Checked ahead of the
	// generic refusal shape below, which would otherwise decode the same
	// body and drop the id in a bare errors.New.
	if statusCode == http.StatusConflict {
		var duplicate struct {
			Detail       string `json:"detail"`
			RepositoryID string `json:"repository_id"`
		}
		if unmarshalError := json.Unmarshal(body, &duplicate); unmarshalError == nil &&
			duplicate.Detail != "" && duplicate.RepositoryID != "" {
			return &PipelineRepositoryAlreadyConnectedError{Detail: duplicate.Detail, RepositoryID: duplicate.RepositoryID}
		}
	}

	// The planner-refusal shape: {"detail": "...", "diagnostics": [...]}.
	// json.Unmarshal fails outright when "detail" is the pydantic array shape
	// instead, so this branch is skipped for that case rather than silently
	// matching it.
	var refusal struct {
		Detail      string   `json:"detail"`
		Diagnostics []string `json:"diagnostics"`
	}
	if unmarshalError := json.Unmarshal(body, &refusal); unmarshalError == nil && refusal.Detail != "" {
		switch {
		case len(refusal.Diagnostics) > 0:
			return &PipelineValidationError{StatusCode: statusCode, Reason: refusal.Detail, Diagnostics: refusal.Diagnostics}
		case statusCode == http.StatusServiceUnavailable:
			return &PipelineLogStreamUnavailableError{
				Detail:            refusal.Detail,
				RetryAfterSeconds: parseRetryAfterSeconds(retryAfterHeader),
			}
		default:
			return errors.New(refusal.Detail)
		}
	}

	// The pydantic v2 422 shape: {"detail": [{"loc": [...], "msg": "..."}]}.
	if message := pipelineValidationDetailFromBody(body); message != "" {
		return errors.New(message)
	}

	return newUnexpectedResponseError("pipeline request failed", statusCode, redactedBodyForError(body, 500))
}

// parseRetryAfterSeconds reads the Retry-After header as seconds, defaulting
// to 10 (the server's own logStreamRetryAfterSeconds) when it is absent or
// not a plain integer.
func parseRetryAfterSeconds(header string) int {
	seconds, parseError := strconv.Atoi(strings.TrimSpace(header))
	if parseError != nil || seconds <= 0 {
		return 10
	}
	return seconds
}

// pipelineValidationDetailFromBody flattens the pydantic v2 422 body
// ({"detail": [{"loc": [...], "msg": "..."}]}) into "field: message" lines,
// mirroring mcpDetailFromBody.
func pipelineValidationDetailFromBody(body []byte) string {
	var validationEnvelope struct {
		Detail []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		} `json:"detail"`
	}
	if unmarshalError := json.Unmarshal(body, &validationEnvelope); unmarshalError != nil || len(validationEnvelope.Detail) == 0 {
		return ""
	}
	messages := make([]string, 0, len(validationEnvelope.Detail))
	for _, item := range validationEnvelope.Detail {
		var path string
		for _, member := range item.Loc {
			if member == "body" || member == "query" || member == "path" {
				continue
			}
			if path != "" {
				path += "."
			}
			path += fmt.Sprintf("%v", member)
		}
		if path != "" {
			messages = append(messages, path+": "+item.Msg)
		} else {
			messages = append(messages, item.Msg)
		}
	}
	return strings.Join(messages, "; ")
}

// doPipelineRequest issues an authenticated JSON request against the pipeline
// API and decodes a 2xx response into target (nil target discards the body,
// which is every DELETE and any GET/POST whose caller only needs the error).
func (c *Client) doPipelineRequest(ctx context.Context, method string, endpoint string,
	payload any, target any) error {
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

	request, requestError := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
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
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pipelineErrorFromResponse(response.StatusCode, body, response.Header.Get("Retry-After"))
	}
	if target == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if unmarshalError := json.Unmarshal(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

func pipelineRunsEndpoint(base string, options ListPipelineRunsOptions) string {
	query := neturl.Values{}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Trigger != "" {
		query.Set("trigger", options.Trigger)
	}
	if options.Branch != "" {
		query.Set("branch", options.Branch)
	}
	if options.HeadSHA != "" {
		query.Set("head_sha", options.HeadSHA)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	endpoint := base + "/pipeline-runs"
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return endpoint
}

// ListPipelineRuns reads one page of the selected repository's runs, newest
// first (GET …/pipeline-runs).
func (c *Client) ListPipelineRuns(ctx context.Context, selector PipelineSelector,
	options ListPipelineRunsOptions) (*PipelineRunList, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineRunList
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		c.BaseURL+pipelineRunsEndpoint(base, options), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreatePipelineRun dispatches a manual run (POST …/pipeline-runs).
func (c *Client) CreatePipelineRun(ctx context.Context, selector PipelineSelector,
	request CreatePipelineRunRequest) (*CreatePipelineRunResult, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result CreatePipelineRunResult
	if requestError := c.doPipelineRequest(ctx, http.MethodPost,
		c.BaseURL+base+"/pipeline-runs", request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPipelineRun reads one run with its steps (GET …/pipeline-runs/{run_id}).
func (c *Client) GetPipelineRun(ctx context.Context, selector PipelineSelector,
	runID string) (*PipelineRunDetail, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineRunDetail
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s%s/pipeline-runs/%s", c.BaseURL, base, neturl.PathEscape(runID)),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RerunPipelineRun opens a new run from a run that already happened (POST
// …/pipeline-runs/{run_id}/rerun). failedOnly restricts the new run to the
// steps that did not succeed and whatever depends on them.
func (c *Client) RerunPipelineRun(ctx context.Context, selector PipelineSelector,
	runID string, failedOnly bool) (*CreatePipelineRunResult, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	endpoint := fmt.Sprintf("%s%s/pipeline-runs/%s/rerun", c.BaseURL, base, neturl.PathEscape(runID))
	if failedOnly {
		endpoint += "?failed_only=true"
	}
	var result CreatePipelineRunResult
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CancelPipelineRun stops a run that has not concluded (POST
// …/pipeline-runs/{run_id}/cancel) and returns the settled run.
func (c *Client) CancelPipelineRun(ctx context.Context, selector PipelineSelector,
	runID string) (*PipelineRun, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineRun
	if requestError := c.doPipelineRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s%s/pipeline-runs/%s/cancel", c.BaseURL, base, neturl.PathEscape(runID)),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListPipelineArtifacts lists a run's stored artifacts (GET
// …/pipeline-runs/{run_id}/artifacts). The store is WS-C item C1; until it
// lands this always answers an empty list for a run the caller can see.
func (c *Client) ListPipelineArtifacts(ctx context.Context, selector PipelineSelector,
	runID string) (*PipelineArtifactList, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineArtifactList
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s%s/pipeline-runs/%s/artifacts", c.BaseURL, base, neturl.PathEscape(runID)),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DownloadPipelineArtifact follows the 302 the download route answers
// (GET …/artifacts/{artifact_id}/download) and streams the artifact into
// destination. Until WS-C item C1 lands, the route always answers 404 with
// pipelines.ErrArtifactsUnavailable.
func (c *Client) DownloadPipelineArtifact(ctx context.Context, selector PipelineSelector,
	artifactID string, destination io.Writer) error {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return selectorError
	}
	endpoint := fmt.Sprintf("%s%s/artifacts/%s/download", c.BaseURL, base, neturl.PathEscape(artifactID))
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	// The default client's redirect policy strips Authorization when a
	// redirect crosses hosts, which is exactly right here: the 302 target is
	// a presigned vault URL on a different host, and the cluster-api bearer
	// token must never reach it.
	response, doError := c.StreamingHTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	if response.StatusCode != http.StatusOK {
		body, readError := readResponseBody(response)
		if readError != nil {
			return fmt.Errorf("read response: %w", readError)
		}
		return pipelineErrorFromResponse(response.StatusCode, body, "")
	}
	if _, copyError := io.Copy(destination, response.Body); copyError != nil {
		return fmt.Errorf("download artifact: %w", copyError)
	}
	return nil
}

// GetPipelineDefinition reads the definition of record (GET …/pipeline).
func (c *Client) GetPipelineDefinition(ctx context.Context, selector PipelineSelector) (*PipelineDefinition, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineDefinition
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, c.BaseURL+base+"/pipeline",
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// PutPipelineDefinition stores a generated-spec override as the repository's
// definition of record (PUT …/pipeline).
func (c *Client) PutPipelineDefinition(ctx context.Context, selector PipelineSelector,
	specYAML string) (*PipelineDefinition, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineDefinition
	if requestError := c.doPipelineRequest(ctx, http.MethodPut, c.BaseURL+base+"/pipeline",
		struct {
			SpecYAML string `json:"spec_yaml"`
		}{SpecYAML: specYAML}, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ValidatePipelineDefinition dry-runs a definition without writing anything
// (POST …/pipeline/validate). An empty specYAML validates the stored
// definition instead of one the caller supplies.
func (c *Client) ValidatePipelineDefinition(ctx context.Context, selector PipelineSelector,
	specYAML string) (*PipelineValidation, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineValidation
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, c.BaseURL+base+"/pipeline/validate",
		struct {
			SpecYAML string `json:"spec_yaml"`
		}{SpecYAML: specYAML}, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListPipelineSchedules lists the repository's cron entries (GET
// …/pipeline/schedules).
func (c *Client) ListPipelineSchedules(ctx context.Context, selector PipelineSelector) (*PipelineScheduleList, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineScheduleList
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, c.BaseURL+base+"/pipeline/schedules",
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreatePipelineSchedule adds a cron entry (POST …/pipeline/schedules).
func (c *Client) CreatePipelineSchedule(ctx context.Context, selector PipelineSelector,
	request CreatePipelineScheduleRequest) (*PipelineSchedule, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineSchedule
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, c.BaseURL+base+"/pipeline/schedules",
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// UpdatePipelineSchedule changes a cron entry (PUT
// …/pipeline/schedules/{schedule_id}).
func (c *Client) UpdatePipelineSchedule(ctx context.Context, selector PipelineSelector,
	scheduleID string, request UpdatePipelineScheduleRequest) (*PipelineSchedule, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineSchedule
	if requestError := c.doPipelineRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s%s/pipeline/schedules/%s", c.BaseURL, base, neturl.PathEscape(scheduleID)),
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DeletePipelineSchedule removes a cron entry (DELETE
// …/pipeline/schedules/{schedule_id}). A schedule that was not there answers
// 404 rather than a silent success.
func (c *Client) DeletePipelineSchedule(ctx context.Context, selector PipelineSelector, scheduleID string) error {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return selectorError
	}
	return c.doPipelineRequest(ctx, http.MethodDelete,
		fmt.Sprintf("%s%s/pipeline/schedules/%s", c.BaseURL, base, neturl.PathEscape(scheduleID)),
		nil, nil)
}

// pipelineRepositoriesBasePath is the organisation-scoped route the
// repository onboarding surface is mounted on
// (go/internal/pipelineapi/repositories.go mountOrganisation). Unlike every
// other pipeline route it addresses no single repository or application, so
// it is a fixed path rather than something PipelineSelector.basePath builds.
const pipelineRepositoriesBasePath = "/api/v1/org/pipelines/repositories"

// pipelineRepositoriesEndpoint appends the GET …/pipelines/repositories
// query (provider filter, page cursor, limit) to the base path.
func pipelineRepositoriesEndpoint(base string, options ListPipelineRepositoriesOptions) string {
	query := neturl.Values{}
	if options.Provider != "" {
		query.Set("provider", options.Provider)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	endpoint := base
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return endpoint
}

// ListPipelineRepositories reads one page of the organisation's connected
// repositories, newest first (GET /org/pipelines/repositories).
func (c *Client) ListPipelineRepositories(ctx context.Context,
	options ListPipelineRepositoriesOptions) (*PipelineRepositoryList, error) {
	var result PipelineRepositoryList
	endpoint := pipelineRepositoriesEndpoint(c.BaseURL+pipelineRepositoriesBasePath, options)
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPipelineRepository reads one connected repository by id
// (GET /org/pipelines/repositories/{repository_id}).
func (c *Client) GetPipelineRepository(ctx context.Context, repositoryID string) (*PipelineRepository, error) {
	var result PipelineRepository
	endpoint := fmt.Sprintf("%s%s/%s", c.BaseURL, pipelineRepositoriesBasePath, neturl.PathEscape(repositoryID))
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ConnectPipelineRepository connects a bare Git repository to Ankra
// Pipelines (POST /org/pipelines/repositories) so a push/PR/tag webhook on it
// can start a run. A repository the organisation already connected - by
// provider, owner and name, compared without case - answers
// *PipelineRepositoryAlreadyConnectedError with the existing row's id rather
// than a second row.
func (c *Client) ConnectPipelineRepository(ctx context.Context,
	request ConnectPipelineRepositoryRequest) (*ConnectPipelineRepositoryResult, error) {
	var result ConnectPipelineRepositoryResult
	endpoint := c.BaseURL + pipelineRepositoriesBasePath
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, endpoint, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DisconnectPipelineRepository disconnects a connected repository
// (DELETE /org/pipelines/repositories/{repository_id}), answering 204 with no
// body on success. Disconnecting is reversible by construction - connecting
// the same identity again revives the row - but a repository with a pipeline
// run still queued or running refuses with the server's own 409 sentence
// ("This repository has a pipeline run that is queued or running",
// pipelines.ErrRepositoryHasLiveRun), rendered here as a plain error, and a
// repository that was never connected, or was already disconnected, answers
// the same 404 as an unknown id (pipelines.ErrRepositoryNotFound).
func (c *Client) DisconnectPipelineRepository(ctx context.Context, repositoryID string) error {
	endpoint := fmt.Sprintf("%s%s/%s", c.BaseURL, pipelineRepositoriesBasePath, neturl.PathEscape(repositoryID))
	return c.doPipelineRequest(ctx, http.MethodDelete, endpoint, nil, nil)
}
