package client

import (
	"fmt"
)

type ClusterManifestListItem struct {
	Name              string      `json:"name"`
	ManifestBase64    string      `json:"manifest_base64"`
	Namespace         string      `json:"namespace"`
	Parents           []Parent    `json:"parents"`
	DeletePermanently bool        `json:"delete_permanently"`
	State             string      `json:"state"`
}

type ListClusterManifestsResponse struct {
	Manifests []ClusterManifestListItem `json:"manifests"`
}

func ListClusterManifests(token, baseURL, clusterID string) ([]ClusterManifestListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/manifests", baseURL, clusterID)

	var response ListClusterManifestsResponse
	if err := getJSON(url, token, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster manifests: %w", err)
	}

	return response.Manifests, nil
}
