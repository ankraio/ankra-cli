package client

// Removing an application from one cluster (ankra-dnytjg.2): the platform
// takes one deployment - one cluster and namespace - down and leaves the
// application and its other deployments standing. Unlike DeleteApplication,
// which tears down every deployment everywhere. A deployment the deploy
// wizard made has no installation and is named by its stack instead
// (ankra-dnytjg.3).

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// RemoveApplicationDeploymentRequest names the deployment to remove: the
// cluster it runs on and either the namespace its installation runs in or the
// stack a deploy-wizard deployment runs as. The platform refuses a body that
// names both (422), so the empty one is left out of the JSON.
type RemoveApplicationDeploymentRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace,omitempty"`
	StackName string `json:"stack_name,omitempty"`
}

// RemoveApplicationDeploymentResult is the accepted removal (202). Accepted
// is not done: the installation reads "removing" until the platform's
// teardown job deletes it, or "failed" with its reason. An installation's
// removal carries InstallationID; a stack's carries StackName, an empty
// Namespace, and RetainedStacks: the other stacks the same deploy runs
// created, which are left standing.
type RemoveApplicationDeploymentResult struct {
	ClusterID      string   `json:"cluster_id" yaml:"cluster_id"`
	Namespace      string   `json:"namespace" yaml:"namespace"`
	InstallationID string   `json:"installation_id,omitempty" yaml:"installation_id,omitempty"`
	StackName      string   `json:"stack_name,omitempty" yaml:"stack_name,omitempty"`
	RetainedStacks []string `json:"retained_stacks,omitempty" yaml:"retained_stacks,omitempty"`
	OperationID    string   `json:"operation_id" yaml:"operation_id"`
	Status         string   `json:"status" yaml:"status"`
}

func applicationRemoveDeploymentURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/deployments/remove", baseURL, neturl.PathEscape(applicationID))
}

// RemoveApplicationDeployment asks the platform to take the application off
// one cluster: one namespace's installation, or one deploy-wizard stack. A
// 409 means that deployment is already being removed, a 422 that nothing is
// deployed there or the stack is not this application's to remove; both
// carry the platform's sentence as the error.
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
