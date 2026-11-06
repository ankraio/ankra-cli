package client

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// NodeConsoleLogResult mirrors the platform's NodeRemediationResult for the
// VM console-log read: the operation it dispatched (or attached to), and,
// when the provider read finished inside the platform's wait budget,
// Completed with the report. The report carries console_output (the newest
// lines of the serial console), line_count, line_limit, line_limit_reached
// (older output may exist), truncated (the platform's byte cap dropped the
// oldest lines), redacted_values (values the platform's secret detector
// replaced with [REDACTED:...] markers) and node (name, ovh_instance_id,
// region).
type NodeConsoleLogResult struct {
	OperationID  string                 `json:"operation_id"`
	StepID       string                 `json:"step_id"`
	NodeID       string                 `json:"node_id"`
	JobName      string                 `json:"job_name"`
	Status       string                 `json:"status"`
	Completed    bool                   `json:"completed"`
	Attached     bool                   `json:"attached_to_existing_run,omitempty"`
	Report       map[string]interface{} `json:"report,omitempty"`
	ErrorExcerpt string                 `json:"error_excerpt,omitempty"`
}

// OvhNodeConsoleLog reads the newest lines of an OVH node's serial console
// through the platform's ovh_node_console_log job, which asks the OVH compute
// API rather than the node. It needs neither SSH nor the in-cluster agent, so
// it answers for a node that hung. Repeated calls attach to an in-flight or
// recent read. The platform waits up to a minute for the read, past the
// shared client's 30s response-header timeout, so the call rides
// httpClientForSlowWrite and is bounded by ctx.
func (c *Client) OvhNodeConsoleLog(ctx context.Context, clusterID, nodeID string) (*NodeConsoleLogResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/nodes/%s/console-log",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(nodeID))
	var result NodeConsoleLogResult
	if postError := c.postNodeFamilyRead(ctx, endpoint, "node console log read failed", &result); postError != nil {
		return nil, postError
	}
	return &result, nil
}

// NodeKubeletConfig is a node's kubelet configuration as the kubelet itself
// reports it on /configz: the values in force once its flags and config file
// were merged, which no Node object carries. KubeletConfig is the
// KubeletConfiguration document; the platform withholds header values that
// can carry credentials.
type NodeKubeletConfig struct {
	NodeName      string                 `json:"node_name"`
	KubeletConfig map[string]interface{} `json:"kubelet_config"`
}

// GetNodeKubeletConfig reads a node's live kubelet configuration through the
// cluster's apiserver. A node the cluster does not have, or a kubelet that
// does not serve /configz, answers 404; a kubelet the apiserver cannot reach
// answers with the platform's reason in the error.
func (c *Client) GetNodeKubeletConfig(clusterID, nodeName string) (*NodeKubeletConfig, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/nodes/%s/kubelet-config",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(nodeName))
	var config NodeKubeletConfig
	if requestError := c.sendJSON(http.MethodGet, endpoint, nil, &config); requestError != nil {
		return nil, requestError
	}
	return &config, nil
}
