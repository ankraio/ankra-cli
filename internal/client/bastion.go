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

// UpdateBastionInstanceTypeResult reports the mutated bastion/gateway node.
type UpdateBastionInstanceTypeResult struct {
	NodeID       string `json:"node_id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	InstanceType string `json:"instance_type"`
}

func (c *Client) UpdateHetznerBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	return c.updateBastionInstanceType(ctx, "hetzner", clusterID, instanceType, wait)
}

func (c *Client) UpdateOvhBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	return c.updateBastionInstanceType(ctx, "ovh", clusterID, instanceType, wait)
}

func (c *Client) UpdateUpcloudBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	return c.updateBastionInstanceType(ctx, "upcloud", clusterID, instanceType, wait)
}

func (c *Client) UpdateDigitaloceanBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	return c.updateBastionInstanceType(ctx, "digitalocean", clusterID, instanceType, wait)
}

// updateBastionInstanceType follows the node-group instance-type async
// accept/wait contract: without --wait the platform answers 202 and applies
// the resize in the background; with --wait it blocks for the resized
// bastion/gateway node.
func (c *Client) updateBastionInstanceType(ctx context.Context, provider, clusterID, instanceType string, wait bool) (*UpdateBastionInstanceTypeResult, bool, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/bastion/instance-type", c.BaseURL, provider, clusterID)
	payload, marshalError := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if marshalError != nil {
		return nil, false, fmt.Errorf("marshal request: %w", marshalError)
	}
	var result UpdateBastionInstanceTypeResult
	submitted, requestError := c.doJSONWriteRequest(ctx, http.MethodPut, url, payload, wait, &result)
	if requestError != nil {
		return nil, false, requestError
	}
	if submitted {
		return nil, true, nil
	}
	return &result, false, nil
}

// BastionHealthResult is the verdict the platform's bastion_health
// maintenance loop last recorded for the cluster's bastion/gateway. It is a
// plain read: the platform reports what its last probe saw and does not touch
// the host, so State is empty until the bastion has been probed once.
// DiagnoseSupported is false for providers with no bastion diagnose job lane
// (Scaleway's managed Public Gateway), which still carry a verdict.
type BastionHealthResult struct {
	ResourceID          string `json:"resource_id"`
	Kind                string `json:"kind"`
	Provider            string `json:"provider"`
	State               string `json:"state"`
	Hop                 string `json:"hop"`
	Detail              string `json:"detail"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	VMStatus            string `json:"vm_status"`
	CheckedAt           string `json:"checked_at"`
	DiagnoseSupported   bool   `json:"diagnose_supported"`
}

// BastionDiagnoseResult is the dispatched diagnosis: the operation handle
// always, and the structured report once the job finished inside the
// platform's wait budget. Completed=false means the budget elapsed first and
// the report has to be polled from the operation. Health is the platform's
// own recorded verdict at dispatch time, so a caller can tell "the host is
// unreachable" from "the host is up and something on it is wrong".
type BastionDiagnoseResult struct {
	OperationID  string         `json:"operation_id"`
	StepID       string         `json:"step_id"`
	ResourceID   string         `json:"resource_id"`
	JobName      string         `json:"job_name"`
	Status       string         `json:"status"`
	Completed    bool           `json:"completed"`
	Report       map[string]any `json:"report,omitempty"`
	ErrorExcerpt string         `json:"error_excerpt,omitempty"`
	Health       map[string]any `json:"health"`
}

func (c *Client) GetHetznerBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth("hetzner", clusterID)
}

func (c *Client) GetOvhBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth("ovh", clusterID)
}

func (c *Client) GetUpcloudBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth("upcloud", clusterID)
}

func (c *Client) GetDigitaloceanBastionHealth(clusterID string) (*BastionHealthResult, error) {
	return c.getBastionHealth("digitalocean", clusterID)
}

func (c *Client) DiagnoseHetznerBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, "hetzner", clusterID)
}

func (c *Client) DiagnoseOvhBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, "ovh", clusterID)
}

func (c *Client) DiagnoseUpcloudBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, "upcloud", clusterID)
}

func (c *Client) DiagnoseDigitaloceanBastion(ctx context.Context, clusterID string) (*BastionDiagnoseResult, error) {
	return c.diagnoseBastion(ctx, "digitalocean", clusterID)
}

// getBastionHealth reads the recorded verdict. sendJSON rather than getJSON
// because the platform answers a cluster with no bastion resource - imported
// and managed-Kubernetes clusters - with a 404 whose `detail` is the reason,
// and that wording is the whole answer for the user.
func (c *Client) getBastionHealth(provider, clusterID string) (*BastionHealthResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/bastion/health",
		c.BaseURL, provider, neturl.PathEscape(clusterID))
	var result BastionHealthResult
	if requestError := c.sendJSON(http.MethodGet, endpoint, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// diagnoseBastion dispatches the read-only <provider>_bastion_diagnose job
// and blocks for its report. There is no wait=false half to this endpoint:
// the platform always waits, up to its own two-minute budget, and hands back
// the operation handle with completed=false if the job outlives it. That is
// well past the shared client's 30s response-header timeout, so the call
// rides httpClientForSlowWrite and is bounded by ctx instead.
func (c *Client) diagnoseBastion(ctx context.Context, provider, clusterID string) (*BastionDiagnoseResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/bastion/diagnose",
		c.BaseURL, provider, neturl.PathEscape(clusterID))
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
		// Every refusal - no bastion, a provider with no diagnose lane, a
		// diagnosis already running - arrives as a 409 whose detail is the
		// platform's own wording; surface it verbatim.
		if detail := detailFromBody(body); detail != "" {
			return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("bastion diagnose failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var result BastionDiagnoseResult
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &result, nil
}
