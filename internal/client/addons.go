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

type ClusterAddonListItem struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ChartName     string    `json:"chart_name"`
	ChartVersion  string    `json:"chart_version"`
	RepositoryURL string    `json:"repository_url"`
	Namespace     string    `json:"namespace"`
	Health        *string   `json:"health,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	State         *string   `json:"state,omitempty"`
	ThroughAnkra  bool      `json:"through_ankra"`
}

type ListClusterAddonsResponse struct {
	Result     []ClusterAddonListItem `json:"result"`
	Pagination Pagination             `json:"pagination"`
}

type AddonSettings struct {
	RetryPolicy          *RetryPolicy `json:"retry_policy,omitempty"`
	SyncPolicy           *SyncPolicy  `json:"sync_policy,omitempty"`
	RevisionHistoryLimit *int         `json:"revision_history_limit,omitempty"`
}

type RetryPolicy struct {
	Limit   int      `json:"limit"`
	Backoff *Backoff `json:"backoff,omitempty"`
}

type Backoff struct {
	Duration    string `json:"duration"`
	Factor      int    `json:"factor"`
	MaxDuration string `json:"max_duration"`
}

type SyncPolicy struct {
	Automated   bool     `json:"automated"`
	SelfHeal    bool     `json:"self_heal"`
	AutoPrune   bool     `json:"auto_prune"`
	SyncOptions []string `json:"sync_options,omitempty"`
}

type GetAddonSettingsResponse struct {
	AddonName string        `json:"addon_name"`
	Settings  AddonSettings `json:"settings"`
}

type UninstallAddonResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type AvailableAddon struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	ChartName   string  `json:"chart_name"`
	Version     string  `json:"version"`
	Category    *string `json:"category,omitempty"`
}

type ListAvailableAddonsResponse struct {
	Result []AvailableAddon `json:"result"`
}

func (c *Client) ListClusterAddons(clusterID string) ([]ClusterAddonListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/addons", c.BaseURL, neturl.PathEscape(clusterID))
	var resp ListClusterAddonsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get cluster addons: %w", err)
	}
	return resp.Result, nil
}

func (c *Client) ListAvailableAddons(clusterID string) ([]AvailableAddon, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/available", c.BaseURL, neturl.PathEscape(clusterID))
	var resp ListAvailableAddonsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get available addons: %w", err)
	}
	return resp.Result, nil
}

func (c *Client) GetAddonSettings(clusterID, addonName string) (*GetAddonSettingsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonName))
	var resp GetAddonSettingsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get addon settings: %w", err)
	}
	return &resp, nil
}

func (c *Client) UpdateAddonSettings(ctx context.Context, clusterID, addonName string, settings AddonSettings) error {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonName))
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	return nil
}

func (c *Client) UninstallAddon(ctx context.Context, clusterID, addonResourceID string, deletePermanently bool) (*UninstallAddonResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s?delete=%t",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonResourceID), deletePermanently)
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
		return nil, fmt.Errorf("uninstall failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	return &UninstallAddonResult{Success: true, Message: "Addon uninstalled"}, nil
}

func (c *Client) GetAddonByName(clusterID, addonName string) (*ClusterAddonListItem, error) {
	addons, err := c.ListClusterAddons(clusterID)
	if err != nil {
		return nil, err
	}
	for i := range addons {
		if addons[i].Name == addonName {
			return &addons[i], nil
		}
	}
	return nil, fmt.Errorf("addon %q not found", addonName)
}
