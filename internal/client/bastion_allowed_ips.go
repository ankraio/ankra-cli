package client

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// UpdateBastionAllowedIPsRequest replaces a cluster's bastion SSH source
// allowlist. The list is the complete new one; an empty list clears it. It
// is always sent, even empty, because the platform reads a missing member
// as a validation error rather than as "clear".
type UpdateBastionAllowedIPsRequest struct {
	BastionAllowedIPs []string `json:"bastion_allowed_ips"`
}

// UpdateBastionAllowedIPsResult reports the bastion whose allowlist was
// written, the normalised list the platform stored (empty: SSH open from
// anywhere again) and the operation that applies it to the bastion. A null
// OperationID means nothing was scheduled: the list already matched, the
// cluster is stopped (it applies on start), or an operation already covers
// the bastion.
type UpdateBastionAllowedIPsResult struct {
	NodeID            string   `json:"node_id"`
	Kind              string   `json:"kind"`
	Name              string   `json:"name"`
	BastionAllowedIPs []string `json:"bastion_allowed_ips"`
	OperationID       *string  `json:"operation_id"`
}

func (c *Client) UpdateHetznerBastionAllowedIPs(ctx context.Context, clusterID string, allowedIPs []string) (*UpdateBastionAllowedIPsResult, error) {
	return c.updateBastionAllowedIPs(ctx, "/api/v1/clusters/hetzner/%s/bastion/allowed-ips", clusterID, allowedIPs)
}

func (c *Client) UpdateOvhBastionAllowedIPs(ctx context.Context, clusterID string, allowedIPs []string) (*UpdateBastionAllowedIPsResult, error) {
	return c.updateBastionAllowedIPs(ctx, "/api/v1/clusters/ovh/%s/bastion/allowed-ips", clusterID, allowedIPs)
}

func (c *Client) UpdateUpcloudBastionAllowedIPs(ctx context.Context, clusterID string, allowedIPs []string) (*UpdateBastionAllowedIPsResult, error) {
	return c.updateBastionAllowedIPs(ctx, "/api/v1/clusters/upcloud/%s/bastion/allowed-ips", clusterID, allowedIPs)
}

func (c *Client) UpdateDigitaloceanBastionAllowedIPs(ctx context.Context, clusterID string, allowedIPs []string) (*UpdateBastionAllowedIPsResult, error) {
	return c.updateBastionAllowedIPs(ctx, "/api/v1/clusters/digitalocean/%s/bastion/allowed-ips", clusterID, allowedIPs)
}

// updateBastionAllowedIPs PUTs the new list to pathFormat (one literal path
// per provider, with %s for the cluster id, so the route census check can
// match each one against the cluster router). The platform validates and
// normalises it (IPv4 only, no 0.0.0.0/0, at most 64 entries) and answers a
// 422 naming the bad entry, which reaches the caller as the backend's detail.
// A platform older than the endpoint answers 404 or 405.
func (c *Client) updateBastionAllowedIPs(ctx context.Context, pathFormat, clusterID string, allowedIPs []string) (*UpdateBastionAllowedIPsResult, error) {
	endpoint := c.BaseURL + fmt.Sprintf(pathFormat, neturl.PathEscape(clusterID))
	if allowedIPs == nil {
		allowedIPs = []string{}
	}
	var result UpdateBastionAllowedIPsResult
	if requestError := c.sendJSONContext(ctx, http.MethodPut, endpoint,
		UpdateBastionAllowedIPsRequest{BastionAllowedIPs: allowedIPs}, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
