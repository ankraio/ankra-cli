package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RemoveNodeGroupNodeResult is the platform's answer to removing one named
// worker from its node group: the worker, the group's node count before and
// after, and the operation draining and deleting it. OperationID is nil when
// the platform created none - the cluster is stopped, or another operation is
// still changing a node of the cluster and the platform's reconcile follows
// with the removal once it has finished.
type RemoveNodeGroupNodeResult struct {
	GroupName     string  `json:"group_name"`
	NodeName      string  `json:"node_name"`
	NodeID        string  `json:"node_id"`
	PreviousCount int     `json:"previous_count"`
	NewCount      int     `json:"new_count"`
	OperationID   *string `json:"operation_id"`
}

// RemoveNodeGroupNode removes the worker node names from node group
// groupName of a cloud-managed cluster of the given kind (hetzner, ovh,
// upcloud, digitalocean, scaleway, ankracloud, aws, proxmox, morpheus). node
// is the worker's server name, its Kubernetes node name, the providerID its
// node reports, or its node id; it is path-escaped, so a providerID's
// slashes reach the platform intact.
//
// The worker is drained through its PodDisruptionBudgets before it is
// deleted, and a refused drain keeps it; drainOptions.ForceDrain takes the
// teardown's drain instead. With wait the request returns the result; without
// it the platform may answer that it accepted the removal, which is returned
// as accepted (with the operation, when the platform named one) and a nil
// result.
//
// The platform's refusals (409) and its answer for a node the cluster does
// not have (404) come back as an *UnexpectedResponseError whose Detail is the
// platform's reason, so a caller can tell that 404 from a platform that does
// not serve the route at all (a 404 with no detail).
func (c *Client) RemoveNodeGroupNode(ctx context.Context, kind, clusterID, groupName, node string,
	drainOptions DrainOptions, wait bool) (*RemoveNodeGroupNodeResult, *AsyncWriteAcceptedResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/node-groups/%s/nodes/%s", c.BaseURL,
		url.PathEscape(kind), url.PathEscape(clusterID), url.PathEscape(groupName), url.PathEscape(node))
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		appendWaitQuery(endpoint+drainOptions.query(), wait), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, err := c.httpClientForAsyncWrite(wait).Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)
	responseBody, err := readResponseBody(response)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusConflict {
		if detail := detailFromBody(responseBody); detail != "" {
			return nil, nil, newBackendDetailError(response.StatusCode, detail)
		}
	}
	var result RemoveNodeGroupNodeResult
	accepted, err := parseAsyncWriteResponse(response, responseBody, wait, &result)
	if err != nil {
		return nil, nil, err
	}
	if accepted != nil {
		return nil, accepted, nil
	}
	return &result, nil, nil
}
