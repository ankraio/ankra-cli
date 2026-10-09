package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OrganisationCISettings is the organisation's Ankra Pipelines settings
// (GET/PUT /api/v1/org/ci-settings): which cluster pipeline steps run on,
// whether the Ankra-operated build cluster stays available when that cluster
// cannot build, how much of the run and step schedulers the organisation may
// occupy, which images its steps may name and which private networks they may
// reach, how long artifacts, caches and run history survive, and which image
// findings block a publish.
//
// Every field the platform stores is carried, so `-o json` is the whole
// record rather than a subset a reader would have to know was incomplete.
//
// ClusterID is nil when the organisation has chosen no pipeline cluster.
// ClusterName alone is nil when the chosen cluster has since been deleted,
// which is why the id is carried separately rather than dropped with it.
type OrganisationCISettings struct {
	ClusterID            *string  `json:"ci_cluster_id" yaml:"ci_cluster_id"`
	ClusterName          *string  `json:"ci_cluster_name" yaml:"ci_cluster_name"`
	BuildFallback        string   `json:"ci_build_fallback" yaml:"ci_build_fallback"`
	MaxParallelRuns      int      `json:"ci_max_parallel_runs" yaml:"ci_max_parallel_runs"`
	MaxParallelSteps     int      `json:"ci_max_parallel_steps" yaml:"ci_max_parallel_steps"`
	AllowedImagePrefixes []string `json:"ci_allowed_image_prefixes" yaml:"ci_allowed_image_prefixes"`
	// EgressAllowedCIDRs are the private networks every pipeline step may
	// reach on top of the public internet; each becomes a peer on the step's
	// NetworkPolicy, so the platform bounds the list and refuses anything
	// outside RFC 1918 / fd00::/8.
	EgressAllowedCIDRs    []string `json:"ci_egress_allowed_cidrs" yaml:"ci_egress_allowed_cidrs"`
	ArtifactRetentionDays int      `json:"ci_artifact_retention_days" yaml:"ci_artifact_retention_days"`
	CacheRetentionDays    int      `json:"ci_cache_retention_days" yaml:"ci_cache_retention_days"`
	// RunRetentionDays is how long a repository's concluded run history is
	// kept; a live run and a repository's newest concluded run are never
	// deleted whatever this says.
	RunRetentionDays int    `json:"ci_run_retention_days" yaml:"ci_run_retention_days"`
	ImageGate        string `json:"ci_image_gate" yaml:"ci_image_gate"`
	// IgnoreUnfixed is the organisation-wide floor under the image gate: true
	// (Ankra's default) lets a pipeline's own gate stage leave findings with
	// no available fix out of the verdict; false keeps every unfixed finding
	// blocking whatever any pipeline asks for.
	IgnoreUnfixed bool `json:"ci_ignore_unfixed" yaml:"ci_ignore_unfixed"`
	// PlatformBuildsEnabled reports whether Ankra has granted the organisation
	// the platform-builders capability that gates the Ankra-operated build
	// lane. It is Ankra's grant rather than a setting, so the platform refuses
	// a write naming it. Nil means the platform predates the field: the answer
	// is unknown, not a denial, so it stays out of structured output instead
	// of reading as false.
	PlatformBuildsEnabled *bool `json:"platform_builds_enabled,omitempty" yaml:"platform_builds_enabled,omitempty"`

	// IsDefault reports that every answer above is Ankra's own default, so a
	// caller can say "this organisation runs on Ankra's defaults" without
	// inferring it from eight comparisons of its own.
	IsDefault bool       `json:"is_default" yaml:"is_default"`
	UpdatedAt *time.Time `json:"updated_at" yaml:"updated_at"`
}

// The build-fallback vocabulary, mirroring enginekit/cisettings on the
// platform side. Both values are listed in the CLI's own help because the
// whole reason this surface exists is that an operator could not discover
// what the setting is called or what it may be set to.
const (
	// CIBuildFallbackPlatformBuilders keeps the Ankra-operated build cluster
	// available when the organisation's own cluster cannot build.
	CIBuildFallbackPlatformBuilders = "platform_builders"
	// CIBuildFallbackNone refuses the build instead, for organisations whose
	// source may not leave their own infrastructure.
	CIBuildFallbackNone = "none"
)

// The image-gate vocabulary, mirroring enginekit/trivygate. The stored values
// are the terse forms the generated workflows carry in ANKRA_IMAGE_GATE.
const (
	// CIImageGateApplicationDependencies blocks on fixable CRITICAL/HIGH
	// findings in packages the application itself declares.
	CIImageGateApplicationDependencies = "app"
	// CIImageGateAllFindings blocks on every finding, base image included.
	CIImageGateAllFindings = "all"
	// CIImageGateNothing publishes regardless of findings.
	CIImageGateNothing = "off"
)

// GetOrganisationCISettings reads the organisation's pipeline CI settings.
// Readable by any organisation member: a member who cannot see which cluster
// their pipeline was aimed at, or whether the build fallback is available,
// cannot explain why their run did not start.
func (c *Client) GetOrganisationCISettings(ctx context.Context) (*OrganisationCISettings, error) {
	body, requestError := c.doCISettingsRequest(ctx, http.MethodGet, nil)
	if requestError != nil {
		return nil, requestError
	}
	return decodeOrganisationCISettings(body)
}

// UpdateOrganisationCISettings writes only the settings named in changes.
//
// The endpoint reads presence, not emptiness: a key left out of the map keeps
// its stored value, so an administrator raising the artifact retention can
// never accidentally clear the organisation's image policy. A key carrying a
// nil value is the explicit null that clears the pipeline cluster.
//
// Requires organisation admin; a member gets the endpoint's own 403 detail.
func (c *Client) UpdateOrganisationCISettings(ctx context.Context,
	changes map[string]any) (*OrganisationCISettings, error) {
	encoded, marshalError := json.Marshal(changes)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, requestError := c.doCISettingsRequest(ctx, http.MethodPut, encoded)
	if requestError != nil {
		return nil, requestError
	}
	return decodeOrganisationCISettings(body)
}

// OrganisationCICapacity is the organisation's pipeline capacity
// (GET /api/v1/org/ci-settings/capacity): the CI slots of its pipeline
// cluster, the steps occupying them, and the organisation's own queued and
// pending work.
//
// A CI slot is one pipeline step the cluster's agent watches at a time, not a
// node: the nodes those steps land on come from the cluster's node group
// autoscaler. StepsInFlightOnCluster counts every step occupying a slot on
// the cluster; the Organisation* counts are this organisation's own.
//
// ClusterID and ClusterName are nil when no pipeline cluster is chosen;
// ClusterName alone is nil when the chosen cluster has been deleted. The
// cluster's numbers are zero then.
type OrganisationCICapacity struct {
	ClusterID                       *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName                     *string `json:"cluster_name" yaml:"cluster_name"`
	CIWorkerCount                   int     `json:"ci_worker_count" yaml:"ci_worker_count"`
	IsLiveResizeSupported           bool    `json:"is_live_resize_supported" yaml:"is_live_resize_supported"`
	StepsInFlightOnCluster          int     `json:"steps_in_flight_on_cluster" yaml:"steps_in_flight_on_cluster"`
	OrganisationRunsQueued          int     `json:"organisation_runs_queued" yaml:"organisation_runs_queued"`
	OrganisationRunsInFlight        int     `json:"organisation_runs_in_flight" yaml:"organisation_runs_in_flight"`
	OrganisationStepsPending        int     `json:"organisation_steps_pending" yaml:"organisation_steps_pending"`
	OrganisationStepsWaitingOnSlots int     `json:"organisation_steps_waiting_on_slots" yaml:"organisation_steps_waiting_on_slots"`
	MaxParallelRuns                 int     `json:"max_parallel_runs" yaml:"max_parallel_runs"`
	MaxParallelSteps                int     `json:"max_parallel_steps" yaml:"max_parallel_steps"`
	// IsPooled says the organisation lists CI pool members, so its runs are
	// spread across PoolMembers and OrganisationStepsWaitingOnSlots is the
	// pool's. PoolMembers is nil from a platform that predates pools, which
	// keeps it out of structured output rather than reading as "no members".
	IsPooled    bool                           `json:"is_pooled,omitempty" yaml:"is_pooled,omitempty"`
	PoolMembers []OrganisationCICapacityMember `json:"pool_members,omitempty" yaml:"pool_members,omitempty"`
}

// OrganisationCICapacityMember is one CI pool member's slots and load.
// CanRunSteps says its agent exists, runs pipeline steps and is checking in;
// IsFull says every one of its workers is taken.
type OrganisationCICapacityMember struct {
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName   string `json:"cluster_name" yaml:"cluster_name"`
	Weight        int    `json:"weight" yaml:"weight"`
	IsPrimary     bool   `json:"is_primary" yaml:"is_primary"`
	IsListed      bool   `json:"is_listed" yaml:"is_listed"`
	CIWorkerCount int    `json:"ci_worker_count" yaml:"ci_worker_count"`
	StepsInFlight int    `json:"steps_in_flight" yaml:"steps_in_flight"`
	CanRunSteps   bool   `json:"can_run_steps" yaml:"can_run_steps"`
	IsFull        bool   `json:"is_full" yaml:"is_full"`
	// WaitingSteps, ScaleUpBlockers, ScaleUpBlockersState and
	// ScaleUpBlockersUnavailable say what is stopping the member from running
	// the organisation's waiting steps (ankra-q573dh.15): steps whose pods
	// have not started two minutes after the agent began them, and why - a
	// node the cloud provider refused for a quota, a node group at its
	// maximum, an image the registry refused - each with the one thing to do.
	// The state is "blocked", "none_observed" or "unknown"; an empty list
	// with "unknown" is not "nothing is wrong". All four are absent from a
	// platform that predates them and stay out of structured output then.
	WaitingSteps               *int                            `json:"waiting_steps,omitempty" yaml:"waiting_steps,omitempty"`
	ScaleUpBlockers            []OrganisationCICapacityBlocker `json:"scale_up_blockers,omitempty" yaml:"scale_up_blockers,omitempty"`
	ScaleUpBlockersState       string                          `json:"scale_up_blockers_state,omitempty" yaml:"scale_up_blockers_state,omitempty"`
	ScaleUpBlockersUnavailable *string                         `json:"scale_up_blockers_unavailable,omitempty" yaml:"scale_up_blockers_unavailable,omitempty"`
}

// OrganisationCICapacityBlocker is one reason a CI pool member is not
// running the organisation's waiting steps. Code is the platform's stable
// vocabulary (the docs list every code); Title and NextStep are its
// sentences. Detail is the cluster's or the provider's own words, already
// cleaned and bounded by the platform, nil when there were none. WaitingSteps
// is how many of the waiting steps it holds.
type OrganisationCICapacityBlocker struct {
	Code         string  `json:"code" yaml:"code"`
	Category     string  `json:"category" yaml:"category"`
	Title        string  `json:"title" yaml:"title"`
	NextStep     string  `json:"next_step" yaml:"next_step"`
	Detail       *string `json:"detail" yaml:"detail"`
	Source       string  `json:"source" yaml:"source"`
	ObservedAt   *string `json:"observed_at" yaml:"observed_at"`
	WaitingSteps int     `json:"waiting_steps" yaml:"waiting_steps"`
}

// ErrCICapacityUnavailable is a platform that does not serve the capacity
// read: it predates the endpoint and answers 404. It is "nothing to show",
// not a failure, so a caller leaves the capacity out rather than failing a
// command whose settings it did read.
var ErrCICapacityUnavailable = errors.New("the platform does not report pipeline capacity")

// GetOrganisationCICapacity reads the organisation's pipeline capacity.
// Readable by any organisation member. A platform that predates the endpoint
// answers ErrCICapacityUnavailable.
func (c *Client) GetOrganisationCICapacity(ctx context.Context) (*OrganisationCICapacity, error) {
	body, requestError := c.doCISettingsRequestAt(ctx, http.MethodGet, ciCapacityPath, nil)
	if requestError != nil {
		var unexpected *UnexpectedResponseError
		if errors.As(requestError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
			return nil, ErrCICapacityUnavailable
		}
		return nil, requestError
	}
	var capacity OrganisationCICapacity
	if unmarshalError := json.Unmarshal(body, &capacity); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &capacity, nil
}

// The two paths of the organisation CI settings surface.
const (
	ciSettingsPath = "/api/v1/org/ci-settings"
	ciCapacityPath = "/api/v1/org/ci-settings/capacity"
)

func decodeOrganisationCISettings(body []byte) (*OrganisationCISettings, error) {
	var settings OrganisationCISettings
	if unmarshalError := json.Unmarshal(body, &settings); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &settings, nil
}

func (c *Client) doCISettingsRequest(ctx context.Context, method string, body []byte) ([]byte, error) {
	return c.doCISettingsRequestAt(ctx, method, ciSettingsPath, body)
}

// doCISettingsRequestAt sends one request to a path of the CI settings
// surface and maps the refusals both of its routes write.
func (c *Client) doCISettingsRequestAt(ctx context.Context, method string, path string,
	body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return responseBody, nil
	case http.StatusNotFound:
		// The pool member routes answer a not-found with the platform's own
		// sentence ("Cluster not found in this organisation.", "This cluster
		// is not listed in the organisation's CI pool."); relayed verbatim it
		// exits 3. A bare router 404 carries no detail and keeps the
		// unexpected-response shape callers already key on.
		if detail := ciSettingsRefusalDetail(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("ci settings request failed",
			response.StatusCode, redactedBodyForError(responseBody, 500))
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		// The admin gate is an RBAC refusal: the caller is who they say they
		// are and it is their role that falls short. That is exit 7, "ask an
		// admin", not the exit-6 "re-login" a bare 403 would earn. The gate
		// writes its own sentence rather than the {"detail":
		// "permission_denied"} shape, so the sentence rides along verbatim.
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		return nil, &PermissionDeniedError{Detail: ciSettingsRefusalDetail(responseBody)}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// The refusals this endpoint writes all name the setting they
		// refused - the admin gate, an unknown build fallback, a value out of
		// range. Relaying the detail verbatim is the whole point: the
		// platform's wording says which setting was refused and why, and a
		// generic "status 422" would send the operator back to guessing.
		if detail := ciSettingsRefusalDetail(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("ci settings request failed",
			response.StatusCode, redactedBodyForError(responseBody, 500))
	default:
		return nil, newUnexpectedResponseError("ci settings request failed",
			response.StatusCode, redactedBodyForError(responseBody, 500))
	}
}

// ciSettingsRefusalDetail reads the human sentence out of either error shape
// this endpoint writes: `{"detail": "..."}` for the admin gate and the
// settings' own frozen refusals, and `{"detail": [{"msg": ...}, ...]}` for the
// per-member validation errors. Returns "" when the body is neither, so the
// caller falls back to reporting the status and body rather than an empty
// error message.
func ciSettingsRefusalDetail(body []byte) string {
	var asString struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &asString) == nil && asString.Detail != "" {
		return asString.Detail
	}
	var asList struct {
		Detail []struct {
			Message string `json:"msg"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &asList) != nil {
		return ""
	}
	messages := make([]string, 0, len(asList.Detail))
	for _, entry := range asList.Detail {
		if entry.Message != "" {
			messages = append(messages, entry.Message)
		}
	}
	return strings.Join(messages, "; ")
}
