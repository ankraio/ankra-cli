package client

// Playground: a real, writable vcluster Ankra provisions for the
// organisation on its shared playground host. Every organisation may hold
// one.

import (
	"fmt"
	neturl "net/url"
)

// CreatePlaygroundResult is the POST body.
type CreatePlaygroundResult struct {
	ClusterID string `json:"cluster_id"`
	Success   bool   `json:"success"`
}

// PlaygroundStatus is the provisioning state machine's public shape.
type PlaygroundStatus struct {
	ClusterID     string  `json:"cluster_id"`
	Phase         string  `json:"phase"`
	StatusMessage *string `json:"status_message"`
	ExpiresAt     string  `json:"expires_at"`
}

// CreatePlayground provisions the organisation's playground. A 409 means one
// already exists; a 503 means the shared host is at capacity. Both surface as
// the server's detail text through the shared unexpected-response error.
func (c *Client) CreatePlayground() (*CreatePlaygroundResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground", c.BaseURL)
	var result CreatePlaygroundResult
	if err := c.postCSRFJSON(url, nil, &result, "create playground"); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPlaygroundStatus reads the provisioning phase.
func (c *Client) GetPlaygroundStatus(clusterID string) (*PlaygroundStatus, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground/%s/status",
		c.BaseURL, neturl.PathEscape(clusterID))
	var status PlaygroundStatus
	if err := c.getJSON(url, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DestroyPlaygroundResult is the DELETE body: the phase the environment
// holds after the call, which is `deprovisioning` once teardown is
// scheduled, or the terminal phase it had already reached.
type DestroyPlaygroundResult struct {
	ClusterID string `json:"cluster_id"`
	Phase     string `json:"phase"`
}

// DestroyPlayground tears the organisation's playground down. Idempotent -
// an environment already deprovisioning or removed answers its current
// phase rather than an error, so a retry after a dropped response is not a
// failure.
//
// This is also the way out of a refused `ankra org domain set`: a live
// playground's wildcard DNS record is reconciled, so it is re-created every
// time an admin deletes it, and destroying the environment is what actually
// clears that blocker.
func (c *Client) DestroyPlayground(clusterID string) (*DestroyPlaygroundResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/playground/%s",
		c.BaseURL, neturl.PathEscape(clusterID))
	var result DestroyPlaygroundResult
	if err := c.deleteCSRFJSON(url, &result, "destroy playground"); err != nil {
		return nil, err
	}
	return &result, nil
}
