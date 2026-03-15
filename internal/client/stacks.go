package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type ClusterStackListItem struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Manifests         []StackManifest `json:"manifests"`
	Addons            []StackAddon    `json:"addons"`
	State             string          `json:"state"`
	DeletePermanently bool            `json:"delete_permanently"`
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
	ConfigurationType string           `json:"configuration_type"`
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
	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks", c.BaseURL, clusterID)

	var response ListClusterStacksResponse
	if err := c.getJSON(url, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
	}

	return response.Stacks, nil
}

func (c *Client) GetStackHistory(clusterID, stackName string) (*GetStackHistoryResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history",
		c.BaseURL, clusterID, stackName)
	var resp GetStackHistoryResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get stack history: %w", err)
	}
	return &resp, nil
}

func (c *Client) DeleteStack(ctx context.Context, clusterID, stackName string) (*DeleteStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s",
		c.BaseURL, clusterID, stackName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
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
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &DeleteStackResult{Success: true, Message: "Stack deleted"}, nil
}

func (c *Client) RenameStack(ctx context.Context, clusterID, stackName, newName string) (*RenameStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/rename-stack",
		c.BaseURL, clusterID, stackName)
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rename failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &RenameStackResult{Success: true, Message: "Stack renamed"}, nil
}

func (c *Client) CreateStack(ctx context.Context, clusterID, name, description string) (*CreateStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks",
		c.BaseURL, clusterID)
	reqBody := CreateStackRequest{Name: name, Description: description}
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &CreateStackResult{Success: true, Message: "Stack created"}, nil
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
		c.BaseURL, targetClusterID)

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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("clone failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result CloneStackToClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}
