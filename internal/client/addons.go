package client

import (
	"fmt"
	"time"
)

type ClusterAddonListItem struct {
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

func ListClusterAddons(token, baseURL, clusterID string) ([]ClusterAddonListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/addons", baseURL, clusterID)
	var resp ListClusterAddonsResponse
	if err := getJSON(url, token, &resp); err != nil {
		return nil, fmt.Errorf("failed to get cluster addons: %w", err)
	}
	return resp.Result, nil
}
