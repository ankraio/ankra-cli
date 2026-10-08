package client

// Removing an application from one cluster (ankra-dnytjg.2): the platform
// takes one deployment - one cluster and namespace - down and leaves the
// application and its other deployments standing. Unlike DeleteApplication,
// which tears down every deployment everywhere.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// RemoveApplicationDeploymentRequest names the deployment to remove: the
// cluster it runs on and the namespace it runs in.
type RemoveApplicationDeploymentRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
}

// RemoveApplicationDeploymentResult is the accepted removal (202). Accepted
// is not done: the installation reads "removing" until the platform's
// teardown job deletes it, or "failed" with its reason.
type RemoveApplicationDeploymentResult struct {
	ClusterID      string `json:"cluster_id" yaml:"cluster_id"`
	Namespace      string `json:"namespace" yaml:"namespace"`
	InstallationID string `json:"installation_id" yaml:"installation_id"`
	OperationID    string `json:"operation_id" yaml:"operation_id"`
	Status         string `json:"status" yaml:"status"`
}

func applicationRemoveDeploymentURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/deployments/remove", baseURL, neturl.PathEscape(applicationID))
}

// RemoveApplicationDeployment asks the platform to take the application off
// one cluster and namespace. A 409 means that deployment is already being
// removed, a 422 that nothing is deployed there; both carry the platform's
// sentence as the error.
// POST /api/v1/org/applications/{application_id}/deployments/remove
func (client *Client) RemoveApplicationDeployment(requestContext context.Context, applicationID string,
	removeRequest RemoveApplicationDeploymentRequest) (*RemoveApplicationDeploymentResult, error) {
	var result RemoveApplicationDeploymentResult
	if requestError := client.sendJSONContext(requestContext, http.MethodPost,
		applicationRemoveDeploymentURL(client.BaseURL, applicationID), removeRequest, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
