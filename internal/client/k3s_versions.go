package client

import "fmt"

type K3sVersionEntry struct {
	Version     string `json:"version"`
	Channel     string `json:"channel"`
	PublishedAt string `json:"published_at"`
}

type ListK3sVersionsResult struct {
	StableVersion string            `json:"stable_version"`
	Versions      []K3sVersionEntry `json:"versions"`
}

func (c *Client) ListK3sVersions() (*ListK3sVersionsResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/k3s-versions", c.BaseURL)
	var result ListK3sVersionsResult
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
