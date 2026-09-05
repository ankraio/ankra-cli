package client

import (
	"bytes"
	"context"
	"encoding/json"
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
// occupy, which images its steps may name, how long artifacts and caches
// survive, and which image findings block a publish.
//
// ClusterID is nil when the organisation has chosen no pipeline cluster.
// ClusterName alone is nil when the chosen cluster has since been deleted,
// which is why the id is carried separately rather than dropped with it.
type OrganisationCISettings struct {
	ClusterID             *string  `json:"ci_cluster_id" yaml:"ci_cluster_id"`
	ClusterName           *string  `json:"ci_cluster_name" yaml:"ci_cluster_name"`
	BuildFallback         string   `json:"ci_build_fallback" yaml:"ci_build_fallback"`
	MaxParallelRuns       int      `json:"ci_max_parallel_runs" yaml:"ci_max_parallel_runs"`
	MaxParallelSteps      int      `json:"ci_max_parallel_steps" yaml:"ci_max_parallel_steps"`
	AllowedImagePrefixes  []string `json:"ci_allowed_image_prefixes" yaml:"ci_allowed_image_prefixes"`
	ArtifactRetentionDays int      `json:"ci_artifact_retention_days" yaml:"ci_artifact_retention_days"`
	CacheRetentionDays    int      `json:"ci_cache_retention_days" yaml:"ci_cache_retention_days"`
	ImageGate             string   `json:"ci_image_gate" yaml:"ci_image_gate"`

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

func decodeOrganisationCISettings(body []byte) (*OrganisationCISettings, error) {
	var settings OrganisationCISettings
	if unmarshalError := json.Unmarshal(body, &settings); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &settings, nil
}

func (c *Client) doCISettingsRequest(ctx context.Context, method string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, requestError := http.NewRequestWithContext(ctx, method,
		c.BaseURL+"/api/v1/org/ci-settings", bodyReader)
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
	case http.StatusOK:
		return responseBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden, http.StatusBadRequest, http.StatusUnprocessableEntity:
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
