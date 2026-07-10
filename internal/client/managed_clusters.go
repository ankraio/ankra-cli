package client

import (
	"fmt"
	"net/url"
)

type ManagedK8sProvider string

const (
	ManagedK8sProviderDoks   ManagedK8sProvider = "doks"
	ManagedK8sProviderUks    ManagedK8sProvider = "uks"
	ManagedK8sProviderGke    ManagedK8sProvider = "gke"
	ManagedK8sProviderOvhMks ManagedK8sProvider = "ovh_mks"
	ManagedK8sProviderAks    ManagedK8sProvider = "aks"
	ManagedK8sProviderEks    ManagedK8sProvider = "eks"
)

type ManagedClusterNodePoolRequest struct {
	Name   string            `json:"name"`
	Size   string            `json:"size"`
	Count  int               `json:"count"`
	Labels map[string]string `json:"labels,omitempty"`
	Taints []NodeTaint       `json:"taints,omitempty"`
}

type CreateManagedClusterRequest struct {
	Name                 string                          `json:"name"`
	Description          *string                         `json:"description,omitempty"`
	CredentialID         string                          `json:"credential_id"`
	Location             string                          `json:"location"`
	KubernetesVersion    *string                         `json:"kubernetes_version,omitempty"`
	NodePools            []ManagedClusterNodePoolRequest `json:"node_pools"`
	GitopsCredentialName *string                         `json:"gitops_credential_name,omitempty"`
	GitopsRepository     *string                         `json:"gitops_repository,omitempty"`
	GitopsBranch         *string                         `json:"gitops_branch,omitempty"`
}

type CreateManagedClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionManagedClusterResponse struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	OperationID     *string  `json:"operation_id,omitempty"`
	ResourcesMarked int      `json:"resources_marked"`
	Errors          []string `json:"errors"`
}

type ScaleManagedNodePoolRequest struct {
	Count int `json:"count"`
}

type ScaleManagedNodePoolResponse struct {
	ClusterID    string `json:"cluster_id"`
	NodePoolName string `json:"node_pool_name"`
	Count        int    `json:"count"`
}

type AddManagedNodePoolRequest struct {
	Name   string            `json:"name"`
	Size   string            `json:"size"`
	Count  int               `json:"count"`
	Labels map[string]string `json:"labels,omitempty"`
	Taints []NodeTaint       `json:"taints,omitempty"`
}

type AddManagedNodePoolResponse struct {
	ClusterID    string `json:"cluster_id"`
	NodePoolName string `json:"node_pool_name"`
	Count        int    `json:"count"`
}

type DeleteManagedNodePoolResponse struct {
	ClusterID    string `json:"cluster_id"`
	NodePoolName string `json:"node_pool_name"`
}

type UpgradeManagedK8sVersionRequest struct {
	Version string `json:"version"`
}

type UpgradeManagedK8sVersionResponse struct {
	ClusterID string `json:"cluster_id"`
	Version   string `json:"version"`
}

func (c *Client) managedClusterBasePath(provider ManagedK8sProvider) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/managed/%s", c.BaseURL, url.PathEscape(string(provider)))
}

func (c *Client) CreateManagedCluster(provider ManagedK8sProvider, request CreateManagedClusterRequest) (*CreateManagedClusterResponse, error) {
	requestURL := c.managedClusterBasePath(provider)
	var response CreateManagedClusterResponse
	if err := c.postCSRFJSON(requestURL, request, &response, "create managed cluster"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) DeprovisionManagedCluster(provider ManagedK8sProvider, clusterID string, force bool) (*DeprovisionManagedClusterResponse, error) {
	requestURL := fmt.Sprintf("%s/%s", c.managedClusterBasePath(provider), url.PathEscape(clusterID))
	if force {
		requestURL += "?force=true"
	}
	var response DeprovisionManagedClusterResponse
	if err := c.deleteCSRFJSON(requestURL, &response, "deprovision managed cluster"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) ScaleManagedNodePool(provider ManagedK8sProvider, clusterID, nodePoolName string, count int) (*ScaleManagedNodePoolResponse, error) {
	requestURL := fmt.Sprintf(
		"%s/%s/node-pools/%s/scale",
		c.managedClusterBasePath(provider),
		url.PathEscape(clusterID),
		url.PathEscape(nodePoolName),
	)
	var response ScaleManagedNodePoolResponse
	requestBody := ScaleManagedNodePoolRequest{Count: count}
	if err := c.postCSRFJSON(requestURL, requestBody, &response, "scale managed node pool"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) AddManagedNodePool(provider ManagedK8sProvider, clusterID string, request AddManagedNodePoolRequest) (*AddManagedNodePoolResponse, error) {
	requestURL := fmt.Sprintf("%s/%s/node-pools", c.managedClusterBasePath(provider), url.PathEscape(clusterID))
	var response AddManagedNodePoolResponse
	if err := c.postCSRFJSON(requestURL, request, &response, "add managed node pool"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) DeleteManagedNodePool(provider ManagedK8sProvider, clusterID, nodePoolName string) (*DeleteManagedNodePoolResponse, error) {
	requestURL := fmt.Sprintf(
		"%s/%s/node-pools/%s",
		c.managedClusterBasePath(provider),
		url.PathEscape(clusterID),
		url.PathEscape(nodePoolName),
	)
	var response DeleteManagedNodePoolResponse
	if err := c.deleteCSRFJSON(requestURL, &response, "delete managed node pool"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) UpgradeManagedK8sVersion(provider ManagedK8sProvider, clusterID, version string) (*UpgradeManagedK8sVersionResponse, error) {
	requestURL := fmt.Sprintf("%s/%s/upgrade", c.managedClusterBasePath(provider), url.PathEscape(clusterID))
	var response UpgradeManagedK8sVersionResponse
	requestBody := UpgradeManagedK8sVersionRequest{Version: version}
	if err := c.postCSRFJSON(requestURL, requestBody, &response, "upgrade managed kubernetes version"); err != nil {
		return nil, err
	}
	return &response, nil
}
