package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// ClusterListItem mirrors cluster-2.0's ClusterListItem from
// src/usecase/cluster/list_clusters.py. Backend fields the CLI does not
// currently render (operation, agent_*, resources, *_count, etc.) are
// silently ignored by Go's JSON decoder.
type ClusterListItem struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	State             string  `json:"state"`
	Description       string  `json:"description"`
	Environment       string  `json:"environment"`
	OrganisationID    string  `json:"organisation_id"`
	KubeVersion       string  `json:"kube_version"`
	ControlPlanes     int     `json:"control_planes"`
	Nodes             int     `json:"nodes"`
	CreatedAt         string  `json:"created_at"`
	OperationalAt     *string `json:"operational_at"`
	SlatedForDeletion *string `json:"slated_for_deletion_at"`
	DeletedAt         *string `json:"deleted_at"`
	Kind              string  `json:"kind"`
}

type ClusterListResponse struct {
	Result     []ClusterListItem `json:"result"`
	Pagination Pagination        `json:"pagination"`
}

func (c *Client) ListClusters(page int, pageSize int) (*ClusterListResponse, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 25
	}

	url := fmt.Sprintf("%s/api/v1/clusters?page=%d&page_size=%d", c.BaseURL, page, pageSize)
	var response *ClusterListResponse
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}

	return response, nil
}

// GetCluster looks up a cluster by exact name. The backend's
// /api/v1/clusters?cluster_name=... mode returns a ClusterListResponse
// with the matching row in `result` (or an empty result on no match).
func (c *Client) GetCluster(name string) (ClusterListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_name=%s", c.BaseURL, neturl.QueryEscape(name))
	var wrapper ClusterListResponse
	if err := c.getJSON(url, &wrapper); err != nil {
		return ClusterListItem{}, err
	}
	for _, cluster := range wrapper.Result {
		if cluster.Name == name {
			return cluster, nil
		}
	}
	return ClusterListItem{}, fmt.Errorf("no cluster found for name %q", name)
}

// GetClusterByID looks up a cluster by its UUID. Passing an explicit page
// forces the backend's paginated ClusterListResponse shape so the matching
// row is returned in `result`. The Kind field on the result identifies the
// cloud provider (hetzner, ovh, upcloud) for provider-agnostic commands.
func (c *Client) GetClusterByID(clusterID string) (ClusterListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_id=%s&page=1&page_size=1", c.BaseURL, neturl.QueryEscape(clusterID))
	var wrapper ClusterListResponse
	if err := c.getJSON(url, &wrapper); err != nil {
		return ClusterListItem{}, err
	}
	for _, cluster := range wrapper.Result {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return ClusterListItem{}, fmt.Errorf("no cluster found for id %q", clusterID)
}

func (c *Client) DeleteCluster(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/api/v1/clusters/%s", c.BaseURL, neturl.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating DELETE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("sending DELETE to %s: %w", url, err)
	}
	defer closeBody(resp)

	bodyBytes, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, redactedBodyForError(bodyBytes, 500))
	}
	return nil
}

type ProvisionClusterResult struct {
	MarkedToStartAt string `json:"marked_to_start_at"`
}

type DeprovisionClusterResult struct {
	MarkedForDeprovisionAt string `json:"marked_for_deprovision_at"`
}

func (c *Client) ProvisionCluster(ctx context.Context, clusterID string) (*ProvisionClusterResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/provision", c.BaseURL, neturl.PathEscape(clusterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provision failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result ProvisionClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeprovisionCluster(ctx context.Context, clusterID string, autoDelete, force bool) (*DeprovisionClusterResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/deprovision", c.BaseURL, neturl.PathEscape(clusterID))
	if autoDelete || force {
		params := "?"
		if autoDelete {
			params += "auto_delete=true&"
		}
		if force {
			params += "force=true&"
		}
		endpoint += strings.TrimRight(params, "&")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result DeprovisionClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type RollToClusterResourceVersionRequest struct {
	ClusterID string `json:"cluster_id"`
	VersionID string `json:"version_id"`
}

type RollToClusterResourceVersionResult struct {
	Ok bool `json:"ok"`
}

func (c *Client) RollToClusterResourceVersion(ctx context.Context, clusterID, versionID string) (*RollToClusterResourceVersionResult, error) {
	url := c.BaseURL + "/api/v1/clusters/resources/roll-to"
	reqBody := RollToClusterResourceVersionRequest{
		ClusterID: clusterID,
		VersionID: versionID,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("roll-to failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result RollToClusterResourceVersionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type AnkraResourceKind string

type Parent struct {
	Name string            `json:"name" yaml:"name"`
	Kind AnkraResourceKind `json:"kind" yaml:"kind"`
}

type AddonStandaloneConfiguration struct {
	ValuesBase64 string `json:"values_base64,omitempty"`
}

type Addon struct {
	Name                   string                 `json:"name"`
	ChartName              string                 `json:"chart_name"`
	ChartVersion           string                 `json:"chart_version"`
	RepositoryURL          string                 `json:"repository_url,omitempty"`
	Namespace              string                 `json:"namespace,omitempty"`
	Configuration          interface{}            `json:"configuration,omitempty"`
	Parents                []Parent               `json:"parents"`
	RegistryName           string                 `json:"registry_name,omitempty"`
	RegistryURL            string                 `json:"registry_url,omitempty"`
	RegistryCredentialName string                 `json:"registry_credential_name,omitempty"`
	Settings               map[string]interface{} `json:"settings,omitempty"`
}

type Manifest struct {
	Name           string   `json:"name"`
	ManifestBase64 string   `json:"manifest_base64"`
	Namespace      string   `json:"namespace,omitempty"`
	Parents        []Parent `json:"parents"`
	EncryptedPaths []string `json:"encrypted_paths,omitempty"`
}

type Stack struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Manifests   []Manifest `json:"manifests,omitempty"`
	Addons      []Addon    `json:"addons,omitempty"`
}

type GitRepository struct {
	Provider       string `json:"provider"`
	CredentialName string `json:"credential_name"`
	Branch         string `json:"branch"`
	Repository     string `json:"repository,omitempty"`
	Workspace      string `json:"workspace,omitempty"`
	RepoSlug       string `json:"repo_slug,omitempty"`
	ProjectKey     string `json:"project_key,omitempty"`
	InstanceURL    string `json:"instance_url,omitempty"`
}

type PrometheusMetrics struct {
	Endpoint       string `json:"endpoint"`
	CredentialName string `json:"credential_name,omitempty"`
	Flavor         string `json:"flavor,omitempty"`
}

type CreateResourceSpec struct {
	GitRepository     *GitRepository     `json:"git_repository,omitempty"`
	PrometheusMetrics *PrometheusMetrics `json:"prometheus_metrics,omitempty"`
	Stacks            []Stack            `json:"stacks"`
}

type CreateImportClusterRequest struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Spec        CreateResourceSpec `json:"spec"`
}

type ImportResponseErrorItem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

type ImportResponseResourceError struct {
	Name   string                    `json:"name"`
	Kind   string                    `json:"kind"`
	Errors []ImportResponseErrorItem `json:"errors"`
}

type ImportResponse struct {
	Name          string                        `json:"name"`
	ClusterId     string                        `json:"cluster_id"`
	ImportCommand string                        `json:"import_command"`
	Errors        []ImportResponseResourceError `json:"errors,omitempty"`
}

type TriggerReconcileResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) TriggerReconcile(ctx context.Context, clusterID string) (*TriggerReconcileResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/reconcile", c.BaseURL, neturl.PathEscape(clusterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reconcile failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result TriggerReconcileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) ApplyCluster(ctx context.Context, clusterReq CreateImportClusterRequest, wait bool) (*ImportResponse, bool, error) {
	for i := range clusterReq.Spec.Stacks {
		if clusterReq.Spec.Stacks[i].Manifests == nil {
			clusterReq.Spec.Stacks[i].Manifests = make([]Manifest, 0)
		}
		if clusterReq.Spec.Stacks[i].Addons == nil {
			clusterReq.Spec.Stacks[i].Addons = make([]Addon, 0)
		}
	}
	payload, err := json.Marshal(clusterReq)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.BaseURL + "/api/v1/clusters/import"
	var importResponse ImportResponse
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPost, endpoint, payload, wait, &importResponse)
	if err != nil {
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	if len(importResponse.Errors) > 0 {
		return nil, false, fmt.Errorf("import failed: %v", importResponse.Errors)
	}
	return &importResponse, false, nil
}

type ValidateClusterRequest struct {
	Spec          CreateResourceSpec `json:"spec"`
	StrictSecrets bool               `json:"strict_secrets"`
}

type ValidationWarning struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Key      string `json:"key"`
	Message  string `json:"message"`
	Category string `json:"category"`
}

type ValidateClusterResponse struct {
	Errors   []ImportResponseResourceError `json:"errors"`
	Warnings []ValidationWarning           `json:"warnings"`
}

// ValidateCluster runs the server-side validation that the offline checks
// cannot — chart existence in connected registries, plaintext-secret
// detection, and parent references against live cluster state. A non-empty
// clusterID validates the spec against that cluster's existing resources.
func (c *Client) ValidateCluster(ctx context.Context, spec CreateResourceSpec, strictSecrets bool, clusterID string) (*ValidateClusterResponse, error) {
	for i := range spec.Stacks {
		if spec.Stacks[i].Manifests == nil {
			spec.Stacks[i].Manifests = make([]Manifest, 0)
		}
		if spec.Stacks[i].Addons == nil {
			spec.Stacks[i].Addons = make([]Addon, 0)
		}
	}

	payload, err := json.Marshal(ValidateClusterRequest{Spec: spec, StrictSecrets: strictSecrets})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/v1/clusters/validate"
	if clusterID != "" {
		url += "?cluster_id=" + neturl.QueryEscape(clusterID)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("validation request failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result ValidateClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type stackDraftRequest struct {
	Spec stackDraftSpec `json:"spec"`
}

type stackDraftSpec struct {
	Stacks []Stack `json:"stacks"`
}

// StackDraftResult captures the outcome of staging a single stack as a draft.
// NoChange is true when the stack already matches the cluster's desired state
// (the server reports no diff to save); Errors holds per-resource validation
// failures when the draft could not be created.
type StackDraftResult struct {
	DraftID  string
	NoChange bool
	Errors   []ImportResponseResourceError
}

// CreateStackDraft stages a single stack as a reviewable resource draft on an
// existing cluster, without deploying anything. It reuses the same backend
// path the stack builder uses.
func (c *Client) CreateStackDraft(ctx context.Context, clusterID string, stack Stack) (*StackDraftResult, error) {
	if stack.Manifests == nil {
		stack.Manifests = make([]Manifest, 0)
	}
	if stack.Addons == nil {
		stack.Addons = make([]Addon, 0)
	}

	payload, err := json.Marshal(stackDraftRequest{Spec: stackDraftSpec{Stacks: []Stack{stack}}})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks/draft", c.BaseURL, neturl.PathEscape(clusterID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return &StackDraftResult{NoChange: true}, nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		var detail struct {
			Detail []ImportResponseResourceError `json:"detail"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			return nil, fmt.Errorf("draft failed: status 422, body: %s", redactedBodyForError(body, 500))
		}
		return &StackDraftResult{Errors: detail.Detail}, nil
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("draft request failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var parsed struct {
		DraftID string `json:"draft_id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &StackDraftResult{DraftID: parsed.DraftID}, nil
}
