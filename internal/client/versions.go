package client

// VersionEntry is one Kubernetes version the platform can provision or
// upgrade to; the same shape serves both the k3s and kubeadm listings.
type VersionEntry struct {
	Version     string `json:"version"`
	Channel     string `json:"channel"`
	PublishedAt string `json:"published_at"`
}

type ListVersionsResult struct {
	StableVersion string         `json:"stable_version"`
	Versions      []VersionEntry `json:"versions"`
}

func (c *Client) listVersions(endpoint string) (*ListVersionsResult, error) {
	var result ListVersionsResult
	if err := c.getJSON(c.BaseURL+endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListK3sVersions() (*ListVersionsResult, error) {
	return c.listVersions("/api/v1/clusters/k3s-versions")
}

func (c *Client) ListKubeadmVersions() (*ListVersionsResult, error) {
	return c.listVersions("/api/v1/clusters/kubeadm-versions")
}
