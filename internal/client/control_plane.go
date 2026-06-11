package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type ControlPlaneInfo struct {
	Count           int      `json:"count"`
	SupportedCounts []int    `json:"supported_counts"`
	InstanceType    string   `json:"instance_type"`
	CanChange       bool     `json:"can_change"`
	Reason          *string  `json:"reason,omitempty"`
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

type ChangeControlPlaneInstanceTypeResult struct {
	PreviousInstanceType string `json:"previous_instance_type"`
	NewInstanceType      string `json:"new_instance_type"`
	Updated              int    `json:"updated"`
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

func (c *Client) ChangeHetznerControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("hetzner", clusterID, count)
}

func (c *Client) ChangeOvhControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("ovh", clusterID, count)
}

func (c *Client) ChangeUpcloudControlPlaneCount(clusterID string, count int) (*ChangeControlPlaneCountResult, error) {
	return c.changeControlPlaneCount("upcloud", clusterID, count)
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
