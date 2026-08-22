package client

import (
	"fmt"
	"net/url"
)

// ClusterDNSZoneResponse reports the cluster's generated public DNS zone
// after the opt-in: the fqdn every ingress hostname can live under, and the
// zone's reconciliation state (pending until published, then active).
//
// OptedOut separates the two ways state "none" happens: the cluster never
// had a zone, or one was removed and the removal is being held so nothing
// re-creates it. Older backends omit the member and it reads false, which is
// the right answer for every cluster they know about.
type ClusterDNSZoneResponse struct {
	Success  bool   `json:"success"`
	FQDN     string `json:"fqdn"`
	State    string `json:"state"`
	OptedOut bool   `json:"opted_out"`
}

// GetClusterDNSZone reads the cluster's generated public DNS zone without
// creating one: the non-mutating lookup twin of EnableClusterDNSZone. A
// cluster that has no zone reports state "none" with an empty fqdn, so
// checking a cluster's domain never enables it - and never adds a blocker to
// a pending organisation root-domain switch.
func (c *Client) GetClusterDNSZone(clusterID string) (*ClusterDNSZoneResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone", c.BaseURL, url.PathEscape(clusterID))
	var response ClusterDNSZoneResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, err
	}
	return &response, nil
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
