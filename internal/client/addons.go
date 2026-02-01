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

// AddonSettings represents addon settings configuration
type AddonSettings struct {
	RetryPolicy           *RetryPolicy `json:"retry_policy,omitempty"`
	SyncPolicy            *SyncPolicy  `json:"sync_policy,omitempty"`
	RevisionHistoryLimit  *int         `json:"revision_history_limit,omitempty"`
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

// GetAddonSettingsResponse is the response from getting addon settings
type GetAddonSettingsResponse struct {
	AddonName string        `json:"addon_name"`
	Settings  AddonSettings `json:"settings"`
}

// UninstallAddonResult is the response from uninstalling an addon
type UninstallAddonResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AvailableAddon represents an addon available for installation
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

func ListClusterAddons(token, baseURL, clusterID string) ([]ClusterAddonListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/addons", baseURL, clusterID)
	var resp ListClusterAddonsResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to get cluster addons: %w", err)
	}
	return resp.Result, nil
}

// ListAvailableAddons returns addons available for installation on a cluster
func ListAvailableAddons(token, baseURL, clusterID string) ([]AvailableAddon, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/available", strings.TrimRight(baseURL, "/"), clusterID)
	var resp ListAvailableAddonsResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to get available addons: %w", err)
	}
	return resp.Result, nil
}

// GetAddonSettings returns the settings for a specific addon
func GetAddonSettings(token, baseURL, clusterID, addonName string) (*GetAddonSettingsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		strings.TrimRight(baseURL, "/"), clusterID, addonName)
	var resp GetAddonSettingsResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to get addon settings: %w", err)
	}
	return &resp, nil
}

// UpdateAddonSettings updates settings for an addon
func UpdateAddonSettings(ctx context.Context, token, baseURL, clusterID, addonName string, settings AddonSettings) error {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		strings.TrimRight(baseURL, "/"), clusterID, addonName)
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// UninstallAddon uninstalls an addon from a cluster
func UninstallAddon(ctx context.Context, token, baseURL, clusterID, addonResourceID string, deletePermanently bool) (*UninstallAddonResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s?delete=%t",
		strings.TrimRight(baseURL, "/"), clusterID, addonResourceID, deletePermanently)
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
		return nil, fmt.Errorf("uninstall failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &UninstallAddonResult{Success: true, Message: "Addon uninstalled"}, nil
}

// GetAddonByName finds an addon by name and returns its details including ID
func GetAddonByName(token, baseURL, clusterID, addonName string) (*ClusterAddonListItem, error) {
	addons, err := ListClusterAddons(token, baseURL, clusterID)
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
