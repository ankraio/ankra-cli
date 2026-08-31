package client

import (
	"fmt"
	"net/http"
)

const clusterMeshAPIPath = "/api/v1/org/cluster-meshes"

// ClusterMeshMember is one cluster in a mesh.
type ClusterMeshMember struct {
	ClusterID         string `json:"cluster_id"`
	CiliumClusterID   int    `json:"cilium_cluster_id"`
	CiliumClusterName string `json:"cilium_cluster_name"`
	Status            string `json:"status"`
}

// ClusterMesh is a mesh and the clusters in it.
type ClusterMesh struct {
	ID      string              `json:"id"`
	Slug    string              `json:"slug"`
	Name    string              `json:"name"`
	Status  string              `json:"status"`
	Members []ClusterMeshMember `json:"members"`
}

// ClusterMeshReadinessItem is one checked precondition for joining a mesh.
type ClusterMeshReadinessItem struct {
	Name       string `json:"name"`
	Ready      bool   `json:"ready"`
	Detail     string `json:"detail"`
	Remediable bool   `json:"remediable"`
}

// ClusterMeshReadiness is one cluster's answer to "could this mesh?".
type ClusterMeshReadiness struct {
	Ready bool                       `json:"ready"`
	Items []ClusterMeshReadinessItem `json:"items"`
}

// ListClusterMeshes returns every mesh of the organisation with its members.
func (c *Client) ListClusterMeshes() ([]ClusterMesh, error) {
	var response struct {
		ClusterMeshes []ClusterMesh `json:"cluster_meshes"`
	}
	if err := c.sendJSON(http.MethodGet, c.BaseURL+clusterMeshAPIPath, nil, &response); err != nil {
		return nil, err
	}
	return response.ClusterMeshes, nil
}

// GetClusterMesh returns one mesh.
func (c *Client) GetClusterMesh(meshID string) (*ClusterMesh, error) {
	var response struct {
		ClusterMesh ClusterMesh `json:"cluster_mesh"`
	}
	url := fmt.Sprintf("%s%s/%s", c.BaseURL, clusterMeshAPIPath, meshID)
	if err := c.sendJSON(http.MethodGet, url, nil, &response); err != nil {
		return nil, err
	}
	return &response.ClusterMesh, nil
}

// CreateClusterMesh declares a new, empty mesh.
func (c *Client) CreateClusterMesh(name string) (*ClusterMesh, error) {
	var response struct {
		ClusterMesh ClusterMesh `json:"cluster_mesh"`
	}
	body := map[string]any{"name": name}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+clusterMeshAPIPath, body, &response); err != nil {
		return nil, err
	}
	return &response.ClusterMesh, nil
}

// DeleteClusterMesh removes an empty mesh.
func (c *Client) DeleteClusterMesh(meshID string) error {
	url := fmt.Sprintf("%s%s/%s", c.BaseURL, clusterMeshAPIPath, meshID)
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// JoinClusterMesh admits a cluster to a mesh.
func (c *Client) JoinClusterMesh(meshID string, clusterID string) error {
	url := fmt.Sprintf("%s%s/%s/members", c.BaseURL, clusterMeshAPIPath, meshID)
	return c.sendJSON(http.MethodPost, url, map[string]any{"cluster_id": clusterID}, nil)
}

// LeaveClusterMesh removes a cluster from a mesh.
func (c *Client) LeaveClusterMesh(meshID string, clusterID string) error {
	url := fmt.Sprintf("%s%s/%s/members/%s", c.BaseURL, clusterMeshAPIPath, meshID, clusterID)
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

// ClusterMeshMakeReadyResult is what make-ready reports back: the identity
// the cluster now carries and how many resources were set converging.
type ClusterMeshMakeReadyResult struct {
	ClusterID             string `json:"cluster_id"`
	CiliumClusterID       int    `json:"cilium_cluster_id"`
	CiliumClusterName     string `json:"cilium_cluster_name"`
	IdentityAllocated     bool   `json:"identity_allocated"`
	TransitionedResources int    `json:"transitioned_resources"`
}

// MakeClusterMeshReady turns an existing cluster mesh-capable, day-2.
func (c *Client) MakeClusterMeshReady(clusterID string, sitePublicIP string) (*ClusterMeshMakeReadyResult, error) {
	var response struct {
		MakeReady ClusterMeshMakeReadyResult `json:"make_ready"`
	}
	body := map[string]any{"cluster_id": clusterID}
	if sitePublicIP != "" {
		body["site_public_ip"] = sitePublicIP
	}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+clusterMeshAPIPath+"/make-ready", body, &response); err != nil {
		return nil, err
	}
	if response.MakeReady.CiliumClusterID == 0 && response.MakeReady.CiliumClusterName == "" {
		return nil, fmt.Errorf("the platform answered without a make_ready result; the API and CLI may be out of step")
	}
	return &response.MakeReady, nil
}

// CheckClusterMeshReadiness answers, per cluster, whether the given set could
// mesh together and why not.
func (c *Client) CheckClusterMeshReadiness(clusterIDs []string) (map[string]ClusterMeshReadiness, error) {
	var response struct {
		Readiness map[string]ClusterMeshReadiness `json:"readiness"`
	}
	body := map[string]any{"cluster_ids": clusterIDs}
	if err := c.sendJSON(http.MethodPost, c.BaseURL+clusterMeshAPIPath+"/readiness", body, &response); err != nil {
		return nil, err
	}
	return response.Readiness, nil
}
