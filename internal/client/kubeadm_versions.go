package client

import "fmt"

type KubeadmVersionEntry struct {
	Version     string `json:"version"`
	Channel     string `json:"channel"`
	PublishedAt string `json:"published_at"`
}

type ListKubeadmVersionsResult struct {
	StableVersion string                `json:"stable_version"`
	Versions      []KubeadmVersionEntry `json:"versions"`
}

func (c *Client) ListKubeadmVersions() (*ListKubeadmVersionsResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/kubeadm-versions", c.BaseURL)
	var result ListKubeadmVersionsResult
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
