package client

import (
	"fmt"
	"net/http"
)

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
	// ProviderStatus / ProviderPowerState surface the cloud provider's live
	// status (e.g. OVH ACTIVE/SHUTOFF/ERROR) as last recorded by the
	// provider's read job; nil until the first read, or always nil for
	// providers without one.
	ProviderStatus     *string `json:"provider_status,omitempty"`
	ProviderPowerState *string `json:"provider_power_state,omitempty"`
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

func (c *Client) ListDigitaloceanClusterNodes(clusterID string) (*NodeListResult, error) {
	return c.listClusterNodes("digitalocean", clusterID)
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

func (c *Client) GetDigitaloceanClusterNode(clusterID, nodeID string) (*NodeDetail, error) {
	return c.getClusterNode("digitalocean", clusterID, nodeID)
}

// RestartNodeResult reports the operation scheduled to restart a node.
type RestartNodeResult struct {
	OperationID string `json:"operation_id"`
	NodeID      string `json:"node_id"`
	JobName     string `json:"job_name"`
}

func (c *Client) RestartHetznerClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode("hetzner", clusterID, nodeID)
}

func (c *Client) RestartOvhClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode("ovh", clusterID, nodeID)
}

func (c *Client) RestartUpcloudClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode("upcloud", clusterID, nodeID)
}

func (c *Client) RestartDigitaloceanClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode("digitalocean", clusterID, nodeID)
}

func (c *Client) RestartProxmoxClusterNode(clusterID, nodeID string) (*RestartNodeResult, error) {
	return c.restartClusterNode("proxmox", clusterID, nodeID)
}

// restartClusterNode schedules a one-shot restart operation. Unlike the
// node-group writes, this endpoint has no async accept/wait contract - it
// always runs synchronously and answers 200 with the scheduled operation.
func (c *Client) restartClusterNode(provider, clusterID, nodeID string) (*RestartNodeResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/nodes/%s/restart", c.BaseURL, provider, clusterID, nodeID)
	var result RestartNodeResult
	if err := c.sendJSON(http.MethodPost, url, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
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
