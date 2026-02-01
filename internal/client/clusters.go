package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	Pagination Pagination        `json:"pagination"` // uses the Pagination type from helpers.go
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

func ListClusters(token string, baseURL string, page int, pageSize int) (*ClusterListResponse, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 25
	}

	url := fmt.Sprintf("%s/api/v1/clusters?page=%d&page_size=%d", baseURL, page, pageSize)
	var response *ClusterListResponse
	if err := getJSON(url, token, &response); err != nil {
		return nil, err
	}

	return response, nil
}

// func ListClusters(token, baseURL string) ([]ClusterListItem, error) {
// 	url := strings.TrimRight(baseURL, "/") + "/api/v1/clusters"
// 	var resp ClusterListResponse
// 	if err := getJSON(url, token, &resp); err != nil {
// 		return nil, err
// 	}
// 	return resp.Result, nil
// }

func GetCluster(token, baseURL, name string) (ClusterWithStatus, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_name=%s",
		strings.TrimRight(baseURL, "/"), name)
	var wrapper ClusterWithStatusResponse
	if err := getJSON(url, token, &wrapper); err != nil {
		return ClusterWithStatus{}, err
	}
	if len(wrapper.Result) == 0 {
		return ClusterWithStatus{}, fmt.Errorf("no cluster found for name %q", name)
	}
	return wrapper.Result[0], nil
}

func DeleteCluster(ctx context.Context, token, baseURL, name string) error {
	url := fmt.Sprintf("%s/api/v1/clusters/%s", strings.TrimRight(baseURL, "/"), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating DELETE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending DELETE to %s: %w", url, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	bodyBytes, _ := io.ReadAll(resp.Body)
	body := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return nil
}

type AnkraResourceKind string

type Parent struct {
	Name string            `json:"name"`
	Kind AnkraResourceKind `json:"kind"`
}

type AddonProfileInput struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type AddonProfile struct {
	Name     string              `json:"name"`
	Owner    string              `json:"owner"`
	Revision string              `json:"revision"`
	Inputs   []AddonProfileInput `json:"inputs,omitempty"`
}

type AddonProfileConfiguration struct {
	Profile AddonProfile `json:"profile"`
}

type AddonStandaloneConfiguration struct {
	ValuesBase64 string `json:"values_base64,omitempty"`
}

type AddonConfigurationType string

const (
	StandaloneType AddonConfigurationType = "standalone"
	ProfileType    AddonConfigurationType = "profile"
)

type Addon struct {
	Name                   string                 `json:"name"`
	ChartName              string                 `json:"chart_name"`
	ChartVersion           string                 `json:"chart_version"`
	RepositoryURL          string                 `json:"repository_url,omitempty"`
	Namespace              string                 `json:"namespace,omitempty"`
	ConfigurationType      string                 `json:"configuration_type,omitempty"`
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

// TriggerReconcileResult is the response from triggering a cluster reconcile
type TriggerReconcileResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// TriggerReconcile triggers a reconciliation for a cluster
func TriggerReconcile(ctx context.Context, token, baseURL, clusterID string) (*TriggerReconcileResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/reconcile", strings.TrimRight(baseURL, "/"), clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reconcile failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result TriggerReconcileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ApplyCluster(ctx context.Context, token, baseURL string, req CreateImportClusterRequest) (*ImportResponse, error) {
	for i := range req.Spec.Stacks {
		if req.Spec.Stacks[i].Manifests == nil {
			req.Spec.Stacks[i].Manifests = make([]Manifest, 0)
		}
		if req.Spec.Stacks[i].Addons == nil {
			req.Spec.Stacks[i].Addons = make([]Addon, 0)
		}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/v1/clusters/import"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er ImportResponse
		if json.Unmarshal(body, &er) == nil && len(er.Errors) > 0 {
			return nil, fmt.Errorf("import failed: %v", er.Errors)
		}
		return nil, fmt.Errorf("import failed: status %d, body: %s", resp.StatusCode, bodyStr)
	}

	var ir ImportResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &ir, nil
}
