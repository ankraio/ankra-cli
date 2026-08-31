package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
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
	Stacks     []ClusterStackListItem `json:"stacks"`
	Pagination Pagination             `json:"pagination"`
}

// StackVersionHistoryEntry mirrors the backend's VersionHistoryEntry: one
// stored version of a stack member resource.
type StackVersionHistoryEntry struct {
	VersionID    string           `json:"version_id"`
	CreatedAt    *time.Time       `json:"created_at"`
	Delta        []map[string]any `json:"delta"`
	UserID       string           `json:"user_id"`
	UserName     *string          `json:"user_name"`
	ExternalUser *string          `json:"external_user"`
	ChangeType   *string          `json:"change_type"`
}

// StackHistoryItem mirrors the backend's StackHistoryItem: the history is
// grouped per stack member (addon or manifest), newest version first.
type StackHistoryItem struct {
	ResourceName   string                     `json:"resource_name"`
	ResourceType   string                     `json:"resource_type"`
	ResourceID     string                     `json:"resource_id"`
	VersionHistory []StackVersionHistoryEntry `json:"version_history"`
}

// GetStackHistoryResponse mirrors GetClusterStackHistoryResult.
type GetStackHistoryResponse struct {
	History []StackHistoryItem `json:"history"`
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
	// GitPushDeferred marks a designed git-push refusal: the rename is
	// applied and live, only the commit back to Git waits on the background
	// sync. Message then carries the platform's detail verbatim.
	GitPushDeferred bool `json:"git_push_deferred,omitempty"`
}

// ListClusterStacks pages through the full stack listing. The
// /api/v1/clusters/{id}/stacks twin always serves page 1 of 25, so this
// uses the imported-cluster route, which accepts paging (page_size max 100)
// and renders the identical stack items.
func (c *Client) ListClusterStacks(clusterID string) ([]ClusterStackListItem, error) {
	var stacks []ClusterStackListItem
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks?page=%d&page_size=100",
			c.BaseURL, neturl.PathEscape(clusterID), page)
		var response ListClusterStacksResponse
		if err := c.getJSON(url, &response); err != nil {
			return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
		}
		stacks = append(stacks, response.Stacks...)
		if page >= response.Pagination.TotalPages || len(response.Stacks) == 0 {
			break
		}
	}
	return stacks, nil
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

// GetStackAddonResourceID resolves an addon's resource id through the stack
// history endpoint — the addon listing carries no id, and the uninstall
// endpoint takes only the resource UUID. max_versions=1 keeps the response
// minimal; the resource ids arrive regardless of version depth.
func (c *Client) GetStackAddonResourceID(clusterID, stackName, addonName string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history?resource_type=addon&max_versions=1",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	var resp GetStackHistoryResponse
	if err := c.getJSON(url, &resp); err != nil {
		return "", fmt.Errorf("failed to resolve addon resource id: %w", err)
	}
	for _, item := range resp.History {
		if item.ResourceType == "addon" && item.ResourceName == addonName {
			return item.ResourceID, nil
		}
	}
	return "", fmt.Errorf("addon %q: %w", addonName, ErrAddonNotFound)
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
		if deferral := gitPushDeferralFromResponse(resp.StatusCode, body); deferral != nil {
			return &RenameStackResult{Success: true, Message: deferral.Message, GitPushDeferred: true}, nil
		}
		return nil, newUnexpectedResponseError("rename failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &RenameStackResult{Success: true, Message: "Stack renamed"}, nil
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
	// ApplicationsCloned is absent from platforms that predate application
	// cloning (cluster#1971) and decodes to 0 there, which is also what those
	// platforms cloned.
	ApplicationsCloned int `json:"applications_cloned"`
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
