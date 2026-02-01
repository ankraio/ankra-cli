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

// StackHistoryEntry represents an entry in stack history
type StackHistoryEntry struct {
	ID          string  `json:"id"`
	Version     int     `json:"version"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CreatedBy   *string `json:"created_by,omitempty"`
	ChangeType  string  `json:"change_type"`
}

// GetStackHistoryResponse is the response from getting stack history
type GetStackHistoryResponse struct {
	StackName string              `json:"stack_name"`
	History   []StackHistoryEntry `json:"history"`
}

// DeleteStackResult is the response from deleting a stack
type DeleteStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// RenameStackRequest is the request to rename a stack
type RenameStackRequest struct {
	NewName string `json:"new_name"`
}

// RenameStackResult is the response from renaming a stack
type RenameStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CreateStackRequest is the request to create a stack
type CreateStackRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CreateStackResult is the response from creating a stack
type CreateStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func ListClusterStacks(token, baseURL, clusterID string) ([]ClusterStackListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks", baseURL, clusterID)

	var response ListClusterStacksResponse
	if err := getJSON(url, token, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
	}

	return response.Stacks, nil
}

// GetStackHistory returns the history of changes for a stack
func GetStackHistory(token, baseURL, clusterID, stackName string) (*GetStackHistoryResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history",
		strings.TrimRight(baseURL, "/"), clusterID, stackName)
	var resp GetStackHistoryResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to get stack history: %w", err)
	}
	return &resp, nil
}

// DeleteStack deletes a stack from a cluster
func DeleteStack(ctx context.Context, token, baseURL, clusterID, stackName string) (*DeleteStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s",
		strings.TrimRight(baseURL, "/"), clusterID, stackName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &DeleteStackResult{Success: true, Message: "Stack deleted"}, nil
}

// RenameStack renames a stack
func RenameStack(ctx context.Context, token, baseURL, clusterID, stackName, newName string) (*RenameStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/rename-stack",
		strings.TrimRight(baseURL, "/"), clusterID, stackName)
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
		return nil, fmt.Errorf("rename failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &RenameStackResult{Success: true, Message: "Stack renamed"}, nil
}

// CreateStack creates a new stack on a cluster
func CreateStack(ctx context.Context, token, baseURL, clusterID, name, description string) (*CreateStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks",
		strings.TrimRight(baseURL, "/"), clusterID)
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &CreateStackResult{Success: true, Message: "Stack created"}, nil
}
