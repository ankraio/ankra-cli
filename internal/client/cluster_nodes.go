package client

import "fmt"

type NodeSummary struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	Role         *string `json:"role,omitempty"`
	NodeGroup    *string `json:"node_group,omitempty"`
	InstanceType string  `json:"instance_type"`
	Location     string  `json:"location"`
	State        string  `json:"state"`
	PrivateIP    *string `json:"private_ip,omitempty"`
	IsDeleted    bool    `json:"is_deleted"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

type NodeListResult struct {
	Nodes []NodeSummary `json:"nodes"`
}

type NodeDetail struct {
	ID            string                       `json:"id"`
	Kind          string                       `json:"kind"`
	Name          string                       `json:"name"`
	Role          *string                      `json:"role,omitempty"`
	NodeGroup     *string                      `json:"node_group,omitempty"`
	State         string                       `json:"state"`
	IsDeleted     bool                         `json:"is_deleted"`
	CreatedAt     string                       `json:"created_at"`
	UpdatedAt     string                       `json:"updated_at"`
	Definition    map[string]interface{}       `json:"definition"`
	Info          map[string]interface{}       `json:"info,omitempty"`
	Data          map[string]interface{}       `json:"data,omitempty"`
	Dependencies  map[string][]string          `json:"dependencies"`
	Relationships map[string][]string          `json:"relationships"`
	Groups        map[string][]string          `json:"groups"`
}

func (c *Client) ListHetznerClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes("hetzner", clusterID)
}

func (c *Client) ListOvhClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes("ovh", clusterID)
}

func (c *Client) ListUpcloudClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes("upcloud", clusterID)
}

func (c *Client) GetHetznerClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode("hetzner", clusterID, nodeID)
}

func (c *Client) GetOvhClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode("ovh", clusterID, nodeID)
}

func (c *Client) GetUpcloudClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode("upcloud", clusterID, nodeID)
}

func (c *Client) listClusterNodes(provider, clusterID string) (*NodeListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/nodes", c.BaseURL, provider, clusterID)
	var result NodeListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) getClusterNode(provider, clusterID, nodeID string) (*NodeDetail, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/nodes/%s", c.BaseURL, provider, clusterID, nodeID)
	var result NodeDetail
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
