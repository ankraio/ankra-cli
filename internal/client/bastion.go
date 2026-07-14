package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
