package client

import (
	"fmt"
	"net/url"
)

// ClusterDNSZoneResponse reports the cluster's generated public DNS zone
// after the opt-in: the fqdn every ingress hostname can live under, and the
// zone's reconciliation state (pending until published, then active).
type ClusterDNSZoneResponse struct {
	Success bool   `json:"success"`
	FQDN    string `json:"fqdn"`
	State   string `json:"state"`
}

// EnableClusterDNSZone queues the cluster's generated ankra.cc DNS zone and
// returns its fqdn and state. The call is idempotent: a cluster that already
// has a zone reports the existing fqdn, so it doubles as a domain lookup.
func (c *Client) EnableClusterDNSZone(clusterID string) (*ClusterDNSZoneResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone", c.BaseURL, url.PathEscape(clusterID))
	var response ClusterDNSZoneResponse
	if err := c.postCSRFJSON(requestURL, nil, &response, "enable cluster dns zone"); err != nil {
		return nil, err
	}
	return &response, nil
}
