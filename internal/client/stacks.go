package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

type ClusterStackListItem struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Manifests   []StackManifest `json:"manifests"`
	Addons      []StackAddon    `json:"addons"`
	// DeployWave orders stacks against each other (nil = unordered).
	DeployWave        *int   `json:"deploy_wave,omitempty"`
	State             string `json:"state"`
	DeletePermanently bool   `json:"delete_permanently"`
}

type StackManifest struct {
	Name              string   `json:"name"`
	ManifestBase64    string   `json:"manifest_base64"`
	Namespace         string   `json:"namespace"`
	Parents           []Parent `json:"parents"`
	DeletePermanently bool     `json:"delete_permanently"`
	State             string   `json:"state"`
}

type StackAddon struct {
	Name              string           `json:"name"`
	ChartName         string           `json:"chart_name"`
	ChartVersion      string           `json:"chart_version"`
	RepositoryURL     string           `json:"repository_url"`
	Namespace         string           `json:"namespace"`
	Configuration     StackAddonConfig `json:"configuration"`
	Parents           []Parent         `json:"parents"`
	State             string           `json:"state"`
	ChartIcon         *string          `json:"chart_icon"`
	DeletePermanently bool             `json:"delete_permanently"`
}

type StackAddonConfig struct {
	ValuesBase64 string `json:"values_base64"`
}

type ListClusterStacksResponse struct {
	Stacks []ClusterStackListItem `json:"stacks"`
}

type StackHistoryEntry struct {
	ID          string  `json:"id"`
	Version     int     `json:"version"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CreatedBy   *string `json:"created_by,omitempty"`
	ChangeType  string  `json:"change_type"`
}

type GetStackHistoryResponse struct {
	StackName string              `json:"stack_name"`
	History   []StackHistoryEntry `json:"history"`
}

type DeleteStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type RenameStackRequest struct {
	NewName string `json:"new_name"`
}

type RenameStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CreateStackRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CreateStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) ListClusterStacks(clusterID string) ([]ClusterStackListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks", c.BaseURL, neturl.PathEscape(clusterID))

	var response ListClusterStacksResponse
	if err := c.getJSON(url, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
	}

	return response.Stacks, nil
}

func (c *Client) GetStackHistory(clusterID, stackName string) (*GetStackHistoryResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	var resp GetStackHistoryResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get stack history: %w", err)
	}
	return &resp, nil
}

func (c *Client) DeleteStack(ctx context.Context, clusterID, stackName string) (*DeleteStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
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
		return nil, newUnexpectedResponseError("delete failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &DeleteStackResult{Success: true, Message: "Stack deleted"}, nil
}

func (c *Client) RenameStack(ctx context.Context, clusterID, stackName, newName string) (*RenameStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/rename-stack",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	reqBody := RenameStackRequest{NewName: newName}
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
		return nil, newUnexpectedResponseError("rename failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &RenameStackResult{Success: true, Message: "Stack renamed"}, nil
}

// CreateStack is kept for interface compatibility with the APIClient
// abstraction but is no longer used by any cobra command. The platform's
// POST /api/v1/org/clusters/imported/{cluster_id}/stacks endpoint expects
// a full ResourceSpecification (see cluster
// usecase/cluster/stacks/create_cluster_stack.py:CreateClusterStackRequest);
// the bare `{name, description}` payload this method used to send was
// rejected with HTTP 422. New work should go through `cluster apply -f
// cluster.yaml`.
//
// Returning an error here keeps the interface contract intact while
// preventing any accidental future callers from sending an incompatible
// request to production.
func (c *Client) CreateStack(_ context.Context, _, _, _ string) (*CreateStackResult, error) {
	return nil, fmt.Errorf("CreateStack: removed; use ApplyCluster with a cluster YAML instead")
}

type CloneStackToClusterRequest struct {
	SourceClusterID            string `json:"source_cluster_id"`
	StackName                  string `json:"stack_name"`
	NewStackName               string `json:"new_stack_name,omitempty"`
	IncludeAddonConfigurations bool   `json:"include_addon_configurations"`
}

type CloneStackToClusterResult struct {
	DraftID         string   `json:"draft_id"`
	StackName       string   `json:"stack_name"`
	Warnings        []string `json:"warnings"`
	AddonsCloned    int      `json:"addons_cloned"`
	ManifestsCloned int      `json:"manifests_cloned"`
}

func (c *Client) CloneStackToCluster(ctx context.Context, targetClusterID string, cloneReq CloneStackToClusterRequest) (*CloneStackToClusterResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/clone",
		c.BaseURL, neturl.PathEscape(targetClusterID))

	payload, err := json.Marshal(cloneReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
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
		return nil, newUnexpectedResponseError("clone failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result CloneStackToClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}
