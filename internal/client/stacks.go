package client

import (
	"fmt"
)

type ClusterStackListItem struct {
	Name                string           `json:"name"`
	Description         string           `json:"description"`
	Manifests           []StackManifest  `json:"manifests"`
	Addons              []StackAddon     `json:"addons"`
	State               string           `json:"state"`
	DeletePermanently   bool             `json:"delete_permanently"`
}

type StackManifest struct {
	Name                string        `json:"name"`
	ManifestBase64      string        `json:"manifest_base64"`
	Namespace           string        `json:"namespace"`
	Parents             []Parent      `json:"parents"`
	DeletePermanently   bool          `json:"delete_permanently"`
	State               string        `json:"state"`
}

type StackAddon struct {
	Name                string                 `json:"name"`
	ChartName           string                 `json:"chart_name"`
	ChartVersion        string                 `json:"chart_version"`
	RepositoryURL       string                 `json:"repository_url"`
	Namespace           string                 `json:"namespace"`
	ConfigurationType   string                 `json:"configuration_type"`
	Configuration       StackAddonConfig       `json:"configuration"`
	Parents             []Parent               `json:"parents"`
	State               string                 `json:"state"`
	ChartIcon           *string                `json:"chart_icon"`
	DeletePermanently   bool                   `json:"delete_permanently"`
}

type StackAddonConfig struct {
	ValuesBase64 string `json:"values_base64"`
}

type ListClusterStacksResponse struct {
	Stacks []ClusterStackListItem `json:"stacks"`
}

func ListClusterStacks(token, baseURL, clusterID string) ([]ClusterStackListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks", baseURL, clusterID)

	var response ListClusterStacksResponse
	if err := getJSON(url, token, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
	}

	return response.Stacks, nil
}
