package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
)

// NodeDiagnoseResult mirrors the platform's NodeRemediationResult for the
// node diagnosis: the operation it dispatched (or attached to), and, when the
// SSH round trip finished inside the platform's wait budget, Completed with
// the report. Otherwise the operation id is the poll handle.
type NodeDiagnoseResult struct {
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

// DiagnoseClusterNode dispatches the platform's read-only <provider>_node_diagnose
// job for one node - the REST twin of the assistant's ssh_diagnose_node - and
// blocks for its report. The job snapshots the node's OS-level state over the
// bastion SSH lane and changes nothing on the host; repeated calls attach to
// an in-flight run. The platform waits up to two minutes and hands back the
// operation with completed=false past that, which is beyond the shared
// client's 30s response-header timeout, so the call rides
// httpClientForSlowWrite and is bounded by ctx instead. provider is the URL
// segment the cluster's routes live under (hetzner, ovh, upcloud, ...).
func (c *Client) DiagnoseClusterNode(ctx context.Context, provider, clusterID, nodeID string) (*NodeDiagnoseResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/nodes/%s/diagnose",
		c.BaseURL, neturl.PathEscape(provider), neturl.PathEscape(clusterID), neturl.PathEscape(nodeID))
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nil))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.httpClientForSlowWrite().Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		// A missing node (404) and a provider without the SSH lane, or a node
		// not in a diagnosable state (409), arrive with the platform's own
		// wording in detail; surface it verbatim.
		if detail := detailFromBody(body); detail != "" {
			return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("node diagnose failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var result NodeDiagnoseResult
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &result, nil
}
