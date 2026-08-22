package client

// The organisation's Ankra root domain, read and written through the same
// backend surface the portal's AI → Settings → Workspaces screen uses
// (GET/PUT /api/v1/org/ai-environment, the bearer twins of the browser
// routes). There is no second source of truth: the CLI sends only the
// dns_root_domain member, and the backend's present-vs-absent update
// contract leaves every other AI environment setting untouched.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// OrganisationDomain reports the organisation's Ankra root domain: the
// custom domain it registered (empty when it follows the platform default)
// and the platform default itself, so a caller can show what an unset choice
// resolves to.
type OrganisationDomain struct {
	DNSRootDomain        string `json:"dns_root_domain"`
	DNSRootDomainDefault string `json:"dns_root_domain_default"`
}

// OrganisationDomainBlockingClusterZone is one cluster DNS zone that refuses
// a root-domain switch until it is removed.
type OrganisationDomainBlockingClusterZone struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	FQDN        string `json:"fqdn"`
	State       string `json:"state"`
}

// OrganisationDomainBlockingDnsRecord is one DNS record that refuses a
// root-domain switch until it is removed.
type OrganisationDomainBlockingDnsRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	State      string `json:"state"`
}

// OrganisationDomainBlockedError is the backend's refusal of a root-domain
// switch, with the exact rows that have to be cleared first. Callers use
// errors.As to render the list instead of the message alone.
//
// The Truncated flags mean the backend capped that list and more blockers
// exist, so clearing everything listed will not be enough on its own.
type OrganisationDomainBlockedError struct {
	Detail                string
	ClusterZones          []OrganisationDomainBlockingClusterZone
	ClusterZonesTruncated bool
	DnsRecords            []OrganisationDomainBlockingDnsRecord
	DnsRecordsTruncated   bool
}

func (e *OrganisationDomainBlockedError) Error() string { return e.Detail }

type organisationDomainRefusal struct {
	Detail                        string                                  `json:"detail"`
	BlockingClusterZones          []OrganisationDomainBlockingClusterZone `json:"blocking_cluster_zones"`
	BlockingClusterZonesTruncated bool                                    `json:"blocking_cluster_zones_truncated"`
	BlockingDnsRecords            []OrganisationDomainBlockingDnsRecord   `json:"blocking_dns_records"`
	BlockingDnsRecordsTruncated   bool                                    `json:"blocking_dns_records_truncated"`
}

type organisationDomainSettings struct {
	DNSRootDomain        *string `json:"dns_root_domain"`
	DNSRootDomainDefault string  `json:"dns_root_domain_default"`
}

// GetOrganisationDomain reads the organisation's Ankra root domain.
func (c *Client) GetOrganisationDomain(ctx context.Context) (*OrganisationDomain, error) {
	body, err := c.doOrganisationDomainRequest(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	return decodeOrganisationDomain(body)
}

// SetOrganisationDomain registers the organisation's own root domain, or
// clears it back to the platform default when rootDomain is empty. The
// backend refuses the switch while cluster DNS zones or DNS records still
// live under the old root; that refusal comes back as an
// OrganisationDomainBlockedError carrying the blocking rows.
func (c *Client) SetOrganisationDomain(ctx context.Context, rootDomain string) (*OrganisationDomain, error) {
	payload := map[string]any{"dns_root_domain": nil}
	if rootDomain != "" {
		payload["dns_root_domain"] = rootDomain
	}
	encoded, marshalError := json.Marshal(payload)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, err := c.doOrganisationDomainRequest(ctx, http.MethodPut, encoded)
	if err != nil {
		return nil, err
	}
	return decodeOrganisationDomain(body)
}

func decodeOrganisationDomain(body []byte) (*OrganisationDomain, error) {
	var settings organisationDomainSettings
	if unmarshalError := json.Unmarshal(body, &settings); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	domain := OrganisationDomain{DNSRootDomainDefault: settings.DNSRootDomainDefault}
	if settings.DNSRootDomain != nil {
		domain.DNSRootDomain = *settings.DNSRootDomain
	}
	return &domain, nil
}

// doOrganisationDomainRequest sends an authenticated JSON request to the AI
// environment settings routes. A 400 carrying the switch guard's blocker
// inventory becomes an OrganisationDomainBlockedError; other 400/403 bodies
// surface their detail verbatim - those texts are user-actionable.
func (c *Client) doOrganisationDomainRequest(ctx context.Context, method string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, requestError := http.NewRequestWithContext(ctx, method,
		c.BaseURL+"/api/v1/org/ai-environment", bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}

	switch response.StatusCode {
	case http.StatusOK:
		return responseBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusBadRequest, http.StatusForbidden:
		var refusal organisationDomainRefusal
		if unmarshalError := json.Unmarshal(responseBody, &refusal); unmarshalError == nil && refusal.Detail != "" {
			if len(refusal.BlockingClusterZones) > 0 || len(refusal.BlockingDnsRecords) > 0 {
				return nil, &OrganisationDomainBlockedError{
					Detail:                refusal.Detail,
					ClusterZones:          refusal.BlockingClusterZones,
					ClusterZonesTruncated: refusal.BlockingClusterZonesTruncated,
					DnsRecords:            refusal.BlockingDnsRecords,
					DnsRecordsTruncated:   refusal.BlockingDnsRecordsTruncated,
				}
			}
			return nil, errors.New(refusal.Detail)
		}
		return nil, newUnexpectedResponseError("organisation domain request failed",
			response.StatusCode, truncateForError(responseBody, 500))
	default:
		return nil, newUnexpectedResponseError("organisation domain request failed",
			response.StatusCode, truncateForError(responseBody, 500))
	}
}
