package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// HelmRegistryListItem mirrors the backend's ExtendedOCIRegistry /
// ExtendedHTTPRegistry payload (see cluster-2.0's
// usecase/helm/list_helm_chart_registries.py). The kind is inferred from
// the URL prefix; the API does not include it as a discriminator field
// because each row is already an instance of the correct Pydantic
// subclass.
type HelmRegistryListItem struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	CredentialName *string `json:"credential_name,omitempty"`
	CreatedAt      *string `json:"created_at,omitempty"`
	UpdatedAt      *string `json:"updated_at,omitempty"`
	ExcludeCharts  []string `json:"exclude_charts,omitempty"`
	Indexing       bool    `json:"indexing"`
	LastIndexedAt  *string `json:"last_indexed_at,omitempty"`
	NextSyncAt     *string `json:"next_sync_at,omitempty"`
	ChartCount     int     `json:"chart_count"`
	IsGlobal       bool    `json:"is_global"`
}

// Kind returns "oci" if the URL has an oci:// scheme, otherwise "http".
// The CLI used to show a kind column populated from an explicit field,
// but the API does not surface one. URL scheme is the canonical signal.
func (item HelmRegistryListItem) Kind() string {
	if len(item.URL) >= 6 && item.URL[:6] == "oci://" {
		return "oci"
	}
	return "http"
}

type ListHelmRegistriesResponse struct {
	Result     []HelmRegistryListItem `json:"result"`
	Pagination Pagination             `json:"pagination"`
}

// GetHelmRegistryResponse mirrors cluster-2.0's GetHelmChartRegistryResult.
type GetHelmRegistryResponse struct {
	Registry        HelmRegistryDetail        `json:"registry"`
	Charts          []HelmChartVersionSummary `json:"charts"`
	Indexing        bool                      `json:"indexing"`
	LastIndexedAt   *string                   `json:"last_indexed_at,omitempty"`
	NextSyncAt      *string                   `json:"next_sync_at,omitempty"`
	ReadJobInterval *int                      `json:"read_job_interval,omitempty"`
	OrganisationID  *string                   `json:"organisation_id,omitempty"`
	ResourceState   *string                   `json:"resource_state,omitempty"`
	Pagination      Pagination                `json:"pagination"`
}

// HelmRegistryDetail covers the union of OCIRegistry and HTTPRegistry as
// returned in the `registry` field of the detail endpoint.
type HelmRegistryDetail struct {
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	CredentialName *string  `json:"credential_name,omitempty"`
	ExcludeCharts  []string `json:"exclude_charts,omitempty"`
	CreatedAt      *string  `json:"created_at,omitempty"`
	UpdatedAt      *string  `json:"updated_at,omitempty"`
}

// HelmChartVersionSummary is a slim projection of the backend's
// ChartVersion payload for use in CLI rendering.
type HelmChartVersionSummary struct {
	Name         string  `json:"name"`
	Version      string  `json:"version"`
	AppVersion   *string `json:"app_version,omitempty"`
	IsDeprecated bool    `json:"is_deprecated"`
}

type CreateHelmRegistryRequest struct {
	Spec json.RawMessage `json:"spec"`
}

type CreateHelmRegistryResponse struct {
	Errors []ResourceError `json:"errors"`
}

type DeleteHelmRegistryResponse struct {
	Success bool `json:"success"`
}

func (c *Client) ListHelmRegistries() (*ListHelmRegistriesResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries", c.BaseURL)
	var response ListHelmRegistriesResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetHelmRegistry(registryName string) (*GetHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s", c.BaseURL, url.PathEscape(registryName))
	var response GetHelmRegistryResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CreateHelmRegistry(req CreateHelmRegistryRequest) (*CreateHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries", c.BaseURL)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var response CreateHelmRegistryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

func (c *Client) DeleteHelmRegistry(registryName string) (*DeleteHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s", c.BaseURL, url.PathEscape(registryName))
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("delete failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var response DeleteHelmRegistryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

// UpdateHelmRegistryRequest mirrors cluster-2.0's
// UpdateHelmChartRegistryRequest. The only mutable field on the wire is the
// read job interval (seconds between automatic background syncs).
type UpdateHelmRegistryRequest struct {
	ReadJobInterval *int `json:"read_job_interval,omitempty"`
}

// SyncHelmRegistryResponse mirrors cluster-2.0's SyncHelmChartRegistryResult.
type SyncHelmRegistryResponse struct {
	CreatedJobs int `json:"created_jobs"`
}

// SyncJobTask mirrors cluster-2.0's SyncJobTask projection.
type SyncJobTask struct {
	ActionLabel string  `json:"action_label"`
	Status      string  `json:"status"`
	Stdout      string  `json:"stdout"`
	Stderr      string  `json:"stderr"`
	Command     *string `json:"command,omitempty"`
}

// SyncJob mirrors cluster-2.0's SyncJob projection returned by the
// registry sync-jobs endpoint.
type SyncJob struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	ResourceKind string        `json:"resource_kind"`
	ResourceName string        `json:"resource_name"`
	CreatedAt    string        `json:"created_at"`
	StartAt      *string       `json:"start_at,omitempty"`
	StopAt       *string       `json:"stop_at,omitempty"`
	Summary      *string       `json:"summary,omitempty"`
	ErrorMessage *string       `json:"error_message,omitempty"`
	Tasks        []SyncJobTask `json:"tasks,omitempty"`
}

// ListRegistrySyncJobsResponse mirrors ListRegistrySyncJobsResult.
type ListRegistrySyncJobsResponse struct {
	Jobs       []SyncJob  `json:"jobs"`
	Pagination Pagination `json:"pagination"`
}

func (c *Client) UpdateHelmRegistry(registryName string, readJobInterval *int) error {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s", c.BaseURL, url.PathEscape(registryName))
	payload, err := json.Marshal(UpdateHelmRegistryRequest{ReadJobInterval: readJobInterval})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("registry '%s' not found", registryName)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}
	return nil
}

func (c *Client) SyncHelmRegistry(registryName string) (*SyncHelmRegistryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s/sync", c.BaseURL, url.PathEscape(registryName))

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
	if resp.StatusCode != http.StatusOK {
		if detail := detailFromBody(body); detail != "" {
			return nil, fmt.Errorf("sync failed: status %d: %s", resp.StatusCode, detail)
		}
		return nil, fmt.Errorf("sync failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var response SyncHelmRegistryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

func (c *Client) ListHelmRegistrySyncJobs(registryName string, page, pageSize int) (*ListRegistrySyncJobsResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/registries/%s/sync-jobs?page=%d&page_size=%d",
		c.BaseURL, url.PathEscape(registryName), page, pageSize)
	var response ListRegistrySyncJobsResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// HelmCredentialListItem mirrors cluster-2.0's
// RegistryCredentialSummary returned by GET /api/v1/org/helm/credentials.
type HelmCredentialListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// ListHelmCredentialsResponse mirrors ListRegistryCredentialsResult.
type ListHelmCredentialsResponse struct {
	Credentials []HelmCredentialListItem `json:"credentials"`
	TotalCount  int                      `json:"total_count"`
}

type CreateHelmCredentialRequest struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type CreateHelmCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors"`
}

type DeleteHelmCredentialResponse struct {
	Success bool `json:"success"`
}

func (c *Client) ListHelmRegistryCredentials() (*ListHelmCredentialsResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials", c.BaseURL)
	var response ListHelmCredentialsResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CreateHelmRegistryCredential(req CreateHelmCredentialRequest) (*CreateHelmCredentialResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials", c.BaseURL)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var response CreateHelmCredentialResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}

// GetHelmCredentialResponse mirrors cluster-2.0's
// GetRegistryCredentialResult. The password is never returned by the API.
type GetHelmCredentialResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	Provider  string `json:"provider"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type UpdateHelmCredentialRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *Client) GetHelmRegistryCredential(credentialName string) (*GetHelmCredentialResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials/%s", c.BaseURL, url.PathEscape(credentialName))
	var response GetHelmCredentialResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) UpdateHelmRegistryCredential(credentialName string, req UpdateHelmCredentialRequest) error {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials/%s", c.BaseURL, url.PathEscape(credentialName))
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("credential '%s' not found", credentialName)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}
	return nil
}

func (c *Client) DeleteHelmRegistryCredential(credentialName string) (*DeleteHelmCredentialResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/helm/credentials/%s", c.BaseURL, url.PathEscape(credentialName))
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("delete failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var response DeleteHelmCredentialResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &response, nil
}
