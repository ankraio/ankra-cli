package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Control-plane change lanes. Offline rewrites a stopped cluster's plan and
// takes effect at the next start; rolling resizes a running cluster's
// controllers one at a time and keeps the Kubernetes API up throughout.
const (
	ControlPlaneChangeModeOffline = "offline"
	ControlPlaneChangeModeRolling = "rolling"
)

// ControlPlaneChangeCapability answers one control: whether it can be changed
// right now, by which lane, and when it cannot, why not.
//
// The count and the instance type are answered separately because they stopped
// having the same answer: a running cluster with three controllers can have its
// instance type rolled live while its count still needs a stopped cluster.
type ControlPlaneChangeCapability struct {
	Allowed bool    `json:"allowed"`
	Mode    string  `json:"mode"`
	Reason  *string `json:"reason"`
}

// ControlPlaneInfo describes the control plane and what may be changed about it.
//
// CountChange and InstanceTypeChange are pointers on purpose: a server that
// predates the split omits them, and nil means "this server did not say" rather
// than "not allowed". Callers fall back to the legacy CanChange/Reason pair.
type ControlPlaneInfo struct {
	Count              int                           `json:"count"`
	SupportedCounts    []int                         `json:"supported_counts"`
	InstanceType       string                        `json:"instance_type"`
	CanChange          bool                          `json:"can_change"`
	Reason             *string                       `json:"reason,omitempty"`
	CountChange        *ControlPlaneChangeCapability `json:"count_change,omitempty"`
	InstanceTypeChange *ControlPlaneChangeCapability `json:"instance_type_change,omitempty"`
}

type ChangeControlPlaneCountRequest struct {
	Count int `json:"count"`
}

type ChangeControlPlaneCountResult struct {
	PreviousCount int `json:"previous_count"`
	NewCount      int `json:"new_count"`
}

type ChangeControlPlaneInstanceTypeRequest struct {
	InstanceType string `json:"instance_type"`
}

// ChangeControlPlaneInstanceTypeResult reports what the change did.
//
// Mode names the lane, not whether work was dispatched. OperationID is what
// says whether there is anything to poll: it is nil on the offline lane, and
// nil on either lane when the controllers already run the requested type.
type ChangeControlPlaneInstanceTypeResult struct {
	PreviousInstanceType string  `json:"previous_instance_type"`
	NewInstanceType      string  `json:"new_instance_type"`
	Updated              int     `json:"updated"`
	Mode                 string  `json:"mode,omitempty"`
	OperationID          *string `json:"operation_id,omitempty"`
}

func (c *Client) GetHetznerControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane("hetzner", clusterID)
}

func (c *Client) GetOvhControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane("ovh", clusterID)
}

func (c *Client) GetUpcloudControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane("upcloud", clusterID)
}

func (c *Client) GetDigitaloceanControlPlane(clusterID string) (*ControlPlaneInfo, error) {
	return c.getControlPlane("digitalocean", clusterID)
}

func (c *Client) ChangeHetznerControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("hetzner", clusterID, count)
}

func (c *Client) ChangeOvhControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("ovh", clusterID, count)
}

func (c *Client) ChangeUpcloudControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("upcloud", clusterID, count)
}

func (c *Client) ChangeDigitaloceanControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("digitalocean", clusterID, count)
}

func (c *Client) ChangeHetznerControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType("hetzner", clusterID, instanceType)
}

func (c *Client) ChangeOvhControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType("ovh", clusterID, instanceType)
}

func (c *Client) ChangeUpcloudControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType("upcloud", clusterID, instanceType)
}

func (c *Client) ChangeDigitaloceanControlPlaneInstanceType(clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	return c.changeControlPlaneInstanceType("digitalocean", clusterID, instanceType)
}

func (c *Client) getControlPlane(provider, clusterID string) (*ControlPlaneInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/control-plane", c.BaseURL, provider, clusterID)
	var result ControlPlaneInfo
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) changeControlPlaneCount(provider, clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/control-plane", c.BaseURL, provider, clusterID)
	payload, err := json.Marshal(ChangeControlPlaneCountRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var result ChangeControlPlaneCountResult
	if err := c.doPutJSON(url, payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) changeControlPlaneInstanceType(provider, clusterID, instanceType string) (*ChangeControlPlaneInstanceTypeResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/%s/control-plane/instance-type", c.BaseURL, provider, clusterID)
	payload, err := json.Marshal(ChangeControlPlaneInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var result ChangeControlPlaneInstanceTypeResult
	if err := c.doPutJSON(url, payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) doPutJSON(url string, payload []byte, target interface{}) error {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)
	body, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, redactedBodyForError(body, 500)))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}
