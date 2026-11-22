package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RemoveNodeGroupNodeResult is the platform's answer to removing one named
// worker from its node group: the worker, the group's node count before and
// after, and the operation draining and deleting it.
//
// Accepted is set when the platform answered that it accepted the removal
// without waiting (202). When it had decided the removal before answering,
// that answer names the worker, the counts and operation_id as well; when it
// was still deciding, it names nothing, and NodeID is empty.
//
// OperationID is nil when the request created no operation: the platform
// could not start it with the request (another node operation is still
// running, the cluster's agent is offline, and other reasons), and its
// reconcile drains and deletes the worker later.
type RemoveNodeGroupNodeResult struct {
	RawDocument   `json:"-" yaml:"-"`
	Accepted      bool    `json:"-" yaml:"-"`
	GroupName     string  `json:"group_name"`
	NodeName      string  `json:"node_name"`
	NodeID        string  `json:"node_id"`
	PreviousCount int     `json:"previous_count"`
	NewCount      int     `json:"new_count"`
	OperationID   *string `json:"operation_id"`
}

// RemoveNodeGroupNode removes the worker node names from node group
// groupName of a cloud-managed cluster of the given kind. node is the
// worker's server id, its server name, its Kubernetes node name, or the
// providerID its node reports; it is path-escaped, so a providerID's slashes
// reach the platform intact.
//
// The worker is drained through its PodDisruptionBudgets before it is
// deleted, and a refused drain keeps it; drainOptions.ForceDrain takes the
// teardown's drain instead. The platform answers once it has marked the
// worker for removal and started the operation that drains and deletes it,
// never after the drain and the delete.
//
// With wait the platform answers once it has decided, and the result keeps
// its document. Without it the platform may answer that it accepted the
// removal: the result is then Accepted, decoded from that one answer.
//
// The platform's refusals (409) and its answer for a node the cluster does
// not have (404) come back as an *UnexpectedResponseError whose Detail is the
// platform's reason, so a caller can tell that 404 from a platform that does
// not serve the route at all (a 404 with no detail, or the router's own).
func (c *Client) RemoveNodeGroupNode(ctx context.Context, kind, clusterID, groupName, node string,
	drainOptions DrainOptions, wait bool) (*RemoveNodeGroupNodeResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/node-groups/%s/nodes/%s", c.BaseURL,
		url.PathEscape(kind), url.PathEscape(clusterID), url.PathEscape(groupName), url.PathEscape(node))
	// One answer decodes into result: with wait the platform's 200 as the
	// target, and a 202 as its members. Without wait the platform answers
	// only a 202 or a refusal, so a 200 there stays the unexpected status it
	// is on every async write.
	var result RemoveNodeGroupNodeResult
	accepted, err := c.doJSONWriteRequestAcceptedWithMembers(ctx, http.MethodDelete, endpoint+drainOptions.query(),
		nil, wait, &result, &result)
	if err != nil {
		return nil, err
	}
	result.Accepted = accepted != nil
	return &result, nil
}
