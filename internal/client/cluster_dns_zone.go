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

// EnableClusterDNSZone queues the cluster's generated Ankra DNS zone (under
// the organisation's selected root domain) and returns its fqdn and state.
// The call is idempotent: a cluster that already has a zone reports the
// existing fqdn, so it doubles as a domain lookup.
func (c *Client) EnableClusterDNSZone(clusterID string) (*ClusterDNSZoneResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone", c.BaseURL, url.PathEscape(clusterID))
	var response ClusterDNSZoneResponse
	if err := c.postCSRFJSON(requestURL, nil, &response, "enable cluster dns zone"); err != nil {
		return nil, err
	}
	return &response, nil
}

// DisableClusterDNSZone hands the cluster's generated DNS zone to the
// platform for teardown and returns its fqdn and (deleting) state. This is
// the removal step before an organisation switches its Ankra root domain.
// A cluster without a zone answers 404.
func (c *Client) DisableClusterDNSZone(clusterID string) (*ClusterDNSZoneResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone", c.BaseURL, url.PathEscape(clusterID))
	var response ClusterDNSZoneResponse
	if err := c.deleteCSRFJSON(requestURL, &response, "disable cluster dns zone"); err != nil {
		return nil, err
	}
	return &response, nil
}
