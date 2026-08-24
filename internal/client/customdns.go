package client

import (
	"fmt"
	"net/url"
)

// Custom DNS zones: the zones a cluster also serves with the organisation's
// own external-dns webhook credential, alongside the delegated zone Ankra
// serves itself. The platform renders one isolated external-dns controller
// per declared zone (cluster#1804); these calls only declare and withdraw
// the bindings.

// CustomDNSZone is one declared binding on a cluster.
type CustomDNSZone struct {
	Zone           string `json:"zone"`
	CredentialName string `json:"credential_name"`
}

type listCustomDNSZonesResponse struct {
	Success bool            `json:"success"`
	Zones   []CustomDNSZone `json:"zones"`
}

// DNSCredentialSummary is one org DNS credential as the provider listing
// reports it. The webhook provider URL is deliberately absent: it embeds the
// token and the platform never echoes it.
type DNSCredentialSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	CreatedAt string `json:"created_at"`
}

// CreateDNSCredentialResponse reports the stored credential's identity, and
// never its webhook URL.
type CreateDNSCredentialResponse struct {
	Success bool            `json:"success"`
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

// ListClusterCustomDNSZones reads the zones declared on a cluster. An empty
// list is a complete answer: the cluster serves none beyond its delegated
// zone.
func (c *Client) ListClusterCustomDNSZones(clusterID string) ([]CustomDNSZone, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/custom-dns-zones",
		c.BaseURL, url.PathEscape(clusterID))
	var response listCustomDNSZonesResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, err
	}
	if response.Zones == nil {
		return []CustomDNSZone{}, nil
	}
	return response.Zones, nil
}

// AddClusterCustomDNSZone declares a zone the cluster also serves. The
// platform refuses a zone that is not a domain name, a credential the
// organisation does not have, and a zone overlapping the delegated zone it
// already serves; those come back as the response detail.
func (c *Client) AddClusterCustomDNSZone(clusterID string, zone string,
	credentialName string) (*CustomDNSZone, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/custom-dns-zones",
		c.BaseURL, url.PathEscape(clusterID))
	payload := map[string]string{"zone": zone, "credential_name": credentialName}
	var response struct {
		Success        bool   `json:"success"`
		Zone           string `json:"zone"`
		CredentialName string `json:"credential_name"`
	}
	if err := c.postCSRFJSON(requestURL, payload, &response, "declare custom dns zone"); err != nil {
		return nil, err
	}
	return &CustomDNSZone{Zone: response.Zone, CredentialName: response.CredentialName}, nil
}

// RemoveClusterCustomDNSZone withdraws a declared zone. The platform tears
// its controller down on the next reconciler pass; the zone's records are the
// operator's and are left alone. A zone the cluster does not serve answers
// 404 rather than succeeding silently.
func (c *Client) RemoveClusterCustomDNSZone(clusterID string, zone string) (string, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/custom-dns-zones/%s",
		c.BaseURL, url.PathEscape(clusterID), url.PathEscape(zone))
	var response struct {
		Success bool   `json:"success"`
		Zone    string `json:"zone"`
	}
	if err := c.deleteCSRFJSON(requestURL, &response, "withdraw custom dns zone"); err != nil {
		return "", err
	}
	return response.Zone, nil
}

// ListDNSCredentials lists the organisation's DNS webhook credentials.
func (c *Client) ListDNSCredentials() ([]DNSCredentialSummary, error) {
	requestURL := c.BaseURL + "/api/v1/credentials/dns"
	var credentials []DNSCredentialSummary
	if err := c.getJSON(requestURL, &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// CreateDNSCredential stores an external-dns webhook credential for the
// organisation. The webhook provider URL embeds the provider token; the
// platform writes it to its secret store and no read surface returns it.
func (c *Client) CreateDNSCredential(name string, webhookProviderURL string) (*CreateDNSCredentialResponse, error) {
	requestURL := c.BaseURL + "/api/v1/credentials/dns"
	payload := map[string]string{"name": name, "webhook_provider_url": webhookProviderURL}
	var response CreateDNSCredentialResponse
	if err := c.postCSRFJSON(requestURL, payload, &response, "create dns credential"); err != nil {
		return nil, err
	}
	return &response, nil
}
