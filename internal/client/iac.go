package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// ErrClusterEmpty is returned by GetClusterIaC when the backend reports that
// the cluster has no resources to generate IaC from
// (GetClusterInfrastructureAsCodeEmptyResourceError, surfaced as a 404 by the
// REST layer). Callers can render a clean "nothing to upgrade" message.
var ErrClusterEmpty = errors.New("cluster has no resources to upgrade")

// IacResponse mirrors GetClusterInfrastructureAsCodeResult from the backend.
type IacResponse struct {
	YamlStringBase64 string `json:"yaml_string_base64"`
}

// AddonConfigurationSpec is the patch-friendly view of an addon's
// configuration block. Distinct from client.AddonStandaloneConfiguration
// (used by apply) so that omitempty works correctly during partial patches.
type AddonConfigurationSpec struct {
	ValuesBase64   string   `json:"values_base64,omitempty" yaml:"values_base64,omitempty"`
	EncryptedPaths []string `json:"encrypted_paths,omitempty" yaml:"encrypted_paths,omitempty"`
	FromFile       string   `json:"from_file,omitempty" yaml:"from_file,omitempty"`
}

// AddonSpec is the typed addon shape used for partial-stack patches and
// round-trip parsing of the IaC YAML returned by GET /iac.
type AddonSpec struct {
	Name                   string                  `json:"name" yaml:"name"`
	ChartName              string                  `json:"chart_name" yaml:"chart_name"`
	ChartVersion           string                  `json:"chart_version" yaml:"chart_version"`
	Namespace              string                  `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	RegistryName           string                  `json:"registry_name,omitempty" yaml:"registry_name,omitempty"`
	RegistryURL            string                  `json:"registry_url,omitempty" yaml:"registry_url,omitempty"`
	RegistryCredentialName string                  `json:"registry_credential_name,omitempty" yaml:"registry_credential_name,omitempty"`
	Configuration          *AddonConfigurationSpec `json:"configuration,omitempty" yaml:"configuration,omitempty"`
	Parents                []Parent                `json:"parents,omitempty" yaml:"parents,omitempty"`
	Settings               map[string]interface{}  `json:"settings,omitempty" yaml:"settings,omitempty"`
}

// ManifestSpec is the typed manifest shape used for partial-stack patches.
type ManifestSpec struct {
	Name           string   `json:"name" yaml:"name"`
	Namespace      string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	ManifestBase64 string   `json:"manifest_base64,omitempty" yaml:"manifest_base64,omitempty"`
	FromFile       string   `json:"from_file,omitempty" yaml:"from_file,omitempty"`
	Parents        []Parent `json:"parents,omitempty" yaml:"parents,omitempty"`
	EncryptedPaths []string `json:"encrypted_paths,omitempty" yaml:"encrypted_paths,omitempty"`
}

// StackSpec is the typed stack shape used for partial-stack patches.
type StackSpec struct {
	Name                string            `json:"name" yaml:"name"`
	Description         string            `json:"description,omitempty" yaml:"description,omitempty"`
	DescriptionFromFile string            `json:"description_from_file,omitempty" yaml:"description_from_file,omitempty"`
	Variables           map[string]string `json:"variables,omitempty" yaml:"variables,omitempty"`
	Manifests           []ManifestSpec    `json:"manifests" yaml:"manifests"`
	Addons              []AddonSpec       `json:"addons" yaml:"addons"`
}

// ResourceSpecSpec is the spec envelope expected by the PATCH endpoint
// (ResourceSpecification on the backend). We only need stacks for partial
// patches; everything else is left untouched server-side.
type ResourceSpecSpec struct {
	Stacks []StackSpec `json:"stacks"`
}

// PatchStackRequest is the body for PATCH /stacks/{stack_name}.
type PatchStackRequest struct {
	Spec         ResourceSpecSpec `json:"spec"`
	PartialStack bool             `json:"partial_stack"`
}

// PatchStackResourceError mirrors the backend ResourceError shape returned in
// UpdateClusterStackResult.errors when validation fails per-resource.
type PatchStackResourceError struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

// PatchStackResult mirrors UpdateClusterStackResult on the backend.
type PatchStackResult struct {
	StackName   string                    `json:"stack_name"`
	Errors      []PatchStackResourceError `json:"errors,omitempty"`
	CommitSHA   string                    `json:"commit_sha,omitempty"`
	CommitURL   string                    `json:"commit_url,omitempty"`
	OperationID string                    `json:"operation_id,omitempty"`
	JobCount    int                       `json:"job_count"`
}

// PatchStackError carries the HTTP status code and raw body for the PATCH
// /stacks call so cmd-level callers can pattern-match on status without
// re-parsing JSON.
type PatchStackError struct {
	Err        error
	StatusCode int
	Body       []byte
}

func (e *PatchStackError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("patch stack failed: status %d, body: %s", e.StatusCode, truncateForError(e.Body, 500))
}

func (e *PatchStackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// emptyResourceDetailMarker is a substring of the FastAPI 404 detail message
// raised when get_cluster_infrastructure_as_code returns an empty-resource
// error. Used to distinguish a genuinely empty cluster from a route/cluster
// not found.
const emptyResourceDetailMarker = "Unable to generate infrastructure as code"

// fastAPIErrorBody is the canonical FastAPI error envelope: { "detail": "..." }.
type fastAPIErrorBody struct {
	Detail string `json:"detail"`
}

// GetClusterIaC fetches the full ImportCluster YAML for a cluster and returns
// it base64-decoded. Returns ErrClusterEmpty when the backend reports the
// cluster has no resources.
func (c *Client) GetClusterIaC(ctx context.Context, clusterID string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/iac",
		c.BaseURL, neturl.PathEscape(clusterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		// Two distinct 404 cases:
		//   1. The cluster has no IaC resources to generate from
		//      (GetClusterInfrastructureAsCodeEmptyResourceError).
		//   2. The route literally doesn't exist on this backend, or the
		//      cluster is unknown to the org.
		// Inspect the detail message to disambiguate so callers can render a
		// useful error in case (2) instead of a misleading "no resources".
		var parsedErr fastAPIErrorBody
		_ = json.Unmarshal(body, &parsedErr)
		if strings.Contains(parsedErr.Detail, emptyResourceDetailMarker) {
			return "", ErrClusterEmpty
		}
		if parsedErr.Detail != "" {
			return "", fmt.Errorf("get IaC failed: %s", parsedErr.Detail)
		}
		return "", fmt.Errorf("get IaC failed: status 404, body: %s", truncateForError(body, 500))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("get IaC failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500)))
	}

	var parsed IacResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(parsed.YamlStringBase64)
	if err != nil {
		return "", fmt.Errorf("base64-decode IaC yaml: %w", err)
	}
	return string(decoded), nil
}

// PatchClusterStackPartial calls PATCH /stacks/{stack_name} with
// partial_stack=true. On non-2xx it returns *PatchStackError so callers can
// inspect StatusCode and Body without re-parsing.
func (c *Client) PatchClusterStackPartial(ctx context.Context, clusterID, stackName string, body PatchStackRequest) (*PatchStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.httpClientForSlowWrite().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		perr := &PatchStackError{StatusCode: resp.StatusCode, Body: respBody}
		if resp.StatusCode == http.StatusUnauthorized {
			// Carry ErrUnauthorized at the source so any caller inspecting the
			// error (not just cmd's mapPatchError) can detect auth failures with
			// errors.Is instead of matching message text.
			perr.Err = ErrUnauthorized
		}
		return nil, perr
	}

	var result PatchStackResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}
