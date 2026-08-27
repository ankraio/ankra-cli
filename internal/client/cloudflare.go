package client

// The Cloudflare domain surface: the organisation's connected Cloudflare
// credentials, the domains each one reaches, and the DNS records inside them.
//
// Nothing here is cached server-side - Cloudflare is authoritative for zones
// and records, and they change from outside Ankra - so a listing is a live
// call and a Cloudflare outage answers 503 rather than serving stale data.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
)

// CloudflareCredential is one connected Cloudflare API token as the surface
// shows it. The token itself is never on the wire.
type CloudflareCredential struct {
	Name           string `json:"name"`
	AccountID      string `json:"account_id,omitempty"`
	AccountName    string `json:"account_name,omitempty"`
	TokenID        string `json:"token_id,omitempty"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	TokenExpiresAt string `json:"token_expires_at,omitempty"`
	IsExpired      bool   `json:"is_expired"`
	State          string `json:"state"`
	CreatedAt      string `json:"created_at"`
}

type CloudflareCredentialsListResult struct {
	Items []CloudflareCredential `json:"items"`
}

// CloudflareVerification is what checking a token learned. It is returned by
// the verify route, which stores nothing, and echoed by connect.
type CloudflareVerification struct {
	TokenID     string   `json:"token_id,omitempty"`
	Status      string   `json:"status"`
	IsActive    bool     `json:"is_active"`
	ExpiresOn   string   `json:"expires_on,omitempty"`
	AccountID   string   `json:"account_id,omitempty"`
	AccountName string   `json:"account_name,omitempty"`
	ZoneCount   int      `json:"zone_count"`
	ZoneNames   []string `json:"zone_names"`
}

// CloudflareDomain is one Cloudflare-hosted zone.
type CloudflareDomain struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	IsActive       bool     `json:"is_active"`
	Paused         bool     `json:"paused"`
	Type           string   `json:"type"`
	CredentialName string   `json:"credential_name"`
	AccountID      string   `json:"account_id,omitempty"`
	AccountName    string   `json:"account_name,omitempty"`
	NameServers    []string `json:"name_servers"`
	CreatedOn      string   `json:"created_on,omitempty"`
	ModifiedOn     string   `json:"modified_on,omitempty"`
}

type CloudflareDomainsListResult struct {
	Items       []CloudflareDomain `json:"items"`
	IsTruncated bool               `json:"is_truncated"`
}

// CloudflareRecord is one DNS record. ManagedBy is "ankra" for records the
// platform created and "external" for everything else - the customer's own
// dashboard, their Terraform, or a cluster controller.
type CloudflareRecord struct {
	ID             string   `json:"id"`
	ZoneID         string   `json:"zone_id"`
	ZoneName       string   `json:"zone_name"`
	Name           string   `json:"name"`
	RecordType     string   `json:"record_type"`
	Content        string   `json:"content"`
	TTL            int      `json:"ttl"`
	IsTTLAutomatic bool     `json:"is_ttl_automatic"`
	Proxied        bool     `json:"proxied"`
	Proxiable      bool     `json:"proxiable"`
	Priority       *int     `json:"priority,omitempty"`
	Comment        string   `json:"comment,omitempty"`
	Tags           []string `json:"tags"`
	ManagedBy      string   `json:"managed_by"`
	CreatedOn      string   `json:"created_on,omitempty"`
	ModifiedOn     string   `json:"modified_on,omitempty"`
}

type CloudflareRecordsListResult struct {
	Domain      CloudflareDomain   `json:"domain"`
	Items       []CloudflareRecord `json:"items"`
	IsTruncated bool               `json:"is_truncated"`
}

type connectCloudflareRequest struct {
	Name      string `json:"name"`
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id,omitempty"`
}

type verifyCloudflareRequest struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id,omitempty"`
}

type connectCloudflareResult struct {
	Success      bool                    `json:"success"`
	Name         string                  `json:"name"`
	Verification *CloudflareVerification `json:"verification,omitempty"`
}

// CreateCloudflareRecordInput is the create request. TTL zero is sent as
// Cloudflare's automatic sentinel by the backend, so an unset TTL means Auto.
type CreateCloudflareRecordInput struct {
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	Content    string `json:"content"`
	TTL        *int   `json:"ttl,omitempty"`
	Proxied    *bool  `json:"proxied,omitempty"`
	Priority   *int   `json:"priority,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// UpdateCloudflareRecordInput is the edit request. Every optional member is
// omitted rather than sent zero when the caller did not set it: the backend's
// PATCH leaves an absent field as it stands, which is how "keep the record's
// current TTL" is expressed.
type UpdateCloudflareRecordInput struct {
	Content  string `json:"content"`
	TTL      *int   `json:"ttl,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// ErrCloudflareNotConnected is returned when the organisation has connected no
// Cloudflare credential. Callers use errors.Is to answer with the "connect one
// first" guidance rather than a bare 404.
var ErrCloudflareNotConnected = errors.New("no Cloudflare credential is connected for this organisation")

// ErrCloudflareNotFound is returned for a domain or record the organisation's
// credential cannot see. It covers "does not exist" and "not this token's
// zone", which the backend does not distinguish and neither may this.
var ErrCloudflareNotFound = errors.New("domain or record not found in Cloudflare")

func (c *Client) cloudflareURL(path string, credentialName string, extra neturl.Values) string {
	query := neturl.Values{}
	for key, values := range extra {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	if credentialName != "" {
		query.Set("credential_name", credentialName)
	}
	if len(query) == 0 {
		return c.BaseURL + path
	}
	return c.BaseURL + path + "?" + query.Encode()
}

func (c *Client) ListCloudflareCredentials(ctx context.Context) (*CloudflareCredentialsListResult, error) {
	body, err := c.doCloudflareRequest(ctx, http.MethodGet, c.BaseURL+"/api/v1/org/cloudflare/credentials", nil)
	if err != nil {
		return nil, err
	}
	var out CloudflareCredentialsListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// VerifyCloudflareToken checks a token against Cloudflare and reports what it
// reaches, storing nothing.
func (c *Client) VerifyCloudflareToken(ctx context.Context, apiToken, accountID string) (*CloudflareVerification, error) {
	payload, err := json.Marshal(verifyCloudflareRequest{APIToken: apiToken, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doCloudflareRequest(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/org/cloudflare/credentials/verify", payload)
	if err != nil {
		return nil, err
	}
	var out CloudflareVerification
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// ConnectCloudflareCredential stores a verified token. The backend verifies it
// against Cloudflare first and refuses a token that reaches no zones, so a
// success here means the credential actually works.
func (c *Client) ConnectCloudflareCredential(ctx context.Context, name, apiToken, accountID string) (*CloudflareVerification, error) {
	payload, err := json.Marshal(connectCloudflareRequest{Name: name, APIToken: apiToken, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doCloudflareRequest(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/org/cloudflare/credentials", payload)
	if err != nil {
		return nil, err
	}
	var out connectCloudflareResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return out.Verification, nil
}

// ListCloudflareDomains lists the domains the credential reaches. domainName
// narrows to one by exact name, resolved server-side in a single lookup.
func (c *Client) ListCloudflareDomains(ctx context.Context, credentialName, domainName string) (*CloudflareDomainsListResult, error) {
	extra := neturl.Values{}
	if domainName != "" {
		extra.Set("name", domainName)
	}
	body, err := c.doCloudflareRequest(ctx, http.MethodGet,
		c.cloudflareURL("/api/v1/org/cloudflare/domains", credentialName, extra), nil)
	if err != nil {
		return nil, err
	}
	var out CloudflareDomainsListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// ListCloudflareRecords lists a domain's records, optionally filtered.
func (c *Client) ListCloudflareRecords(ctx context.Context, credentialName, domainID, nameFilter, typeFilter string) (*CloudflareRecordsListResult, error) {
	extra := neturl.Values{}
	if nameFilter != "" {
		extra.Set("name", nameFilter)
	}
	if typeFilter != "" {
		extra.Set("record_type", typeFilter)
	}
	path := "/api/v1/org/cloudflare/domains/" + neturl.PathEscape(domainID) + "/records"
	body, err := c.doCloudflareRequest(ctx, http.MethodGet,
		c.cloudflareURL(path, credentialName, extra), nil)
	if err != nil {
		return nil, err
	}
	var out CloudflareRecordsListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) CreateCloudflareRecord(ctx context.Context, credentialName, domainID string, input CreateCloudflareRecordInput) (*CloudflareRecord, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	path := "/api/v1/org/cloudflare/domains/" + neturl.PathEscape(domainID) + "/records"
	body, err := c.doCloudflareRequest(ctx, http.MethodPost,
		c.cloudflareURL(path, credentialName, nil), payload)
	if err != nil {
		return nil, err
	}
	var out CloudflareRecord
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) UpdateCloudflareRecord(ctx context.Context, credentialName, domainID, recordID string, input UpdateCloudflareRecordInput) (*CloudflareRecord, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	path := "/api/v1/org/cloudflare/domains/" + neturl.PathEscape(domainID) +
		"/records/" + neturl.PathEscape(recordID)
	body, err := c.doCloudflareRequest(ctx, http.MethodPatch,
		c.cloudflareURL(path, credentialName, nil), payload)
	if err != nil {
		return nil, err
	}
	var out CloudflareRecord
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) DeleteCloudflareRecord(ctx context.Context, credentialName, domainID, recordID string) error {
	path := "/api/v1/org/cloudflare/domains/" + neturl.PathEscape(domainID) +
		"/records/" + neturl.PathEscape(recordID)
	_, err := c.doCloudflareRequest(ctx, http.MethodDelete,
		c.cloudflareURL(path, credentialName, nil), nil)
	return err
}

func (c *Client) doCloudflareRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return respBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		// The backend answers 404 both for "no credential connected" and for
		// a domain or record the credential cannot see. Its detail text is
		// what tells them apart, and each has a different next step.
		detail := dnsDetailFromBody(respBody)
		if detail != "" && strings.Contains(strings.ToLower(detail), "no cloudflare credential is connected") {
			return nil, ErrCloudflareNotConnected
		}
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", ErrCloudflareNotFound, detail)
		}
		return nil, ErrCloudflareNotFound
	case http.StatusServiceUnavailable:
		// Cloudflare is unreachable or rate-limiting. The Retry-After it sends
		// is the honest thing to tell the user, because retrying does help.
		detail := dnsDetailFromBody(respBody)
		if detail == "" {
			detail = "Cloudflare is currently unavailable."
		}
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 {
				return nil, fmt.Errorf("%s Cloudflare asked for a %d second wait", detail, seconds)
			}
		}
		return nil, errors.New(detail)
	case http.StatusBadRequest, http.StatusForbidden, http.StatusConflict:
		// Every refusal the operator can act on - a rejected token, a token
		// that reaches no zones, a record name outside the domain - arrives
		// as a detail string worth showing verbatim.
		if detail := dnsDetailFromBody(respBody); detail != "" {
			return nil, errors.New(detail)
		}
		return nil, newUnexpectedResponseError("Cloudflare request failed", resp.StatusCode, truncateForError(respBody, 500))
	default:
		return nil, newUnexpectedResponseError("Cloudflare request failed", resp.StatusCode, truncateForError(respBody, 500))
	}
}
