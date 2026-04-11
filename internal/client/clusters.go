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

type ClusterListItem struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	IncomingNetworks  int     `json:"incoming_networks"`
	OutgoingNetworks  int     `json:"outgoing_networks"`
	State             string  `json:"state"`
	Description       string  `json:"description"`
	Environment       string  `json:"environment"`
	OrganisationID    string  `json:"organisation_id"`
	KubeDistribution  string  `json:"kube_distribution"`
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

type ClusterWithStatus struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	IncomingNetworks int     `json:"incoming_networks"`
	OutgoingNetworks int     `json:"outgoing_networks"`
	Description      string  `json:"description"`
	Environment      string  `json:"environment"`
	OrganisationID   string  `json:"organisation_id"`
	KubeDistribution string  `json:"kube_distribution"`
	KubeVersion      string  `json:"kube_version"`
	Status           *string `json:"status"`
	State            string  `json:"state"`
	CreatedAt        string  `json:"created_at"`
	DeletedAt        *string `json:"deleted_at"`
	Kind             string  `json:"kind"`
}

type ClusterWithStatusResponse struct {
	Result []ClusterWithStatus `json:"result"`
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

func (c *Client) GetCluster(name string) (ClusterWithStatus, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_name=%s", c.BaseURL, neturl.QueryEscape(name))
	var wrapper ClusterWithStatusResponse
	if err := c.getJSON(url, &wrapper); err != nil {
		return ClusterWithStatus{}, err
	}
	if len(wrapper.Result) == 0 {
		return ClusterWithStatus{}, fmt.Errorf("no cluster found for name %q", name)
	}
	return wrapper.Result[0], nil
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
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncateForError(bodyBytes, 500))
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
		return nil, fmt.Errorf("provision failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
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
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
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
		return nil, fmt.Errorf("roll-to failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result RollToClusterResourceVersionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type AnkraResourceKind string

type Parent struct {
	Name string            `json:"name"`
	Kind AnkraResourceKind `json:"kind"`
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
	Repository     string `json:"repository"`
}

type CreateResourceSpec struct {
	GitRepository *GitRepository `json:"git_repository,omitempty"`
	Stacks        []Stack        `json:"stacks"`
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
		return nil, fmt.Errorf("reconcile failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var result TriggerReconcileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) ApplyCluster(ctx context.Context, clusterReq CreateImportClusterRequest) (*ImportResponse, error) {
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
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/v1/clusters/import"
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
		var er ImportResponse
		if json.Unmarshal(body, &er) == nil && len(er.Errors) > 0 {
			return nil, fmt.Errorf("import failed: %v", er.Errors)
		}
		return nil, fmt.Errorf("import failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var ir ImportResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &ir, nil
}
