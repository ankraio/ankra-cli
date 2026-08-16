package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// DnsZone mirrors the backend response for the organisation's own delegated
// DNS zone: the fqdn new record names hang off and its provisioning state
// ("active", "pending", or "none" when no zone exists yet).
type DnsZone struct {
	FQDN  string `json:"fqdn"`
	State string `json:"state"`
}

// DnsRecord mirrors the backend response shape for one org-managed DNS
// record. Name is the full FQDN (label + org zone); State follows the
// pending/active/deleting/failed reconciler lifecycle.
type DnsRecord struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	RecordType string  `json:"record_type"`
	Content    string  `json:"content"`
	TTL        *int    `json:"ttl,omitempty"`
	State      string  `json:"state"`
	LastError  *string `json:"last_error,omitempty"`
	CreatedBy  string  `json:"created_by"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

type DnsRecordsListResult struct {
	Items []DnsRecord `json:"items"`
}

type createDnsRecordRequest struct {
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	Content    string `json:"content"`
	TTL        *int   `json:"ttl,omitempty"`
}

type updateDnsRecordRequest struct {
	Content string `json:"content"`
	TTL     *int   `json:"ttl,omitempty"`
}

// ErrDnsRecordNotFound is returned when the backend reports 404 for a record
// id. Callers use errors.Is to distinguish it from network/auth errors.
var ErrDnsRecordNotFound = errors.New("DNS record not found")

func (c *Client) GetOrganisationDnsZone(ctx context.Context) (*DnsZone, error) {
	body, err := c.doDnsRequest(ctx, http.MethodGet, c.BaseURL+"/api/v1/org/dns/zone", nil)
	if err != nil {
		return nil, err
	}
	var out DnsZone
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) ListOrganisationDnsRecords(ctx context.Context) (*DnsRecordsListResult, error) {
	body, err := c.doDnsRequest(ctx, http.MethodGet, c.BaseURL+"/api/v1/org/dns/records", nil)
	if err != nil {
		return nil, err
	}
	var out DnsRecordsListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) CreateOrganisationDnsRecord(ctx context.Context, name, recordType, content string, ttl *int) (*DnsRecord, error) {
	payload, err := json.Marshal(createDnsRecordRequest{Name: name, RecordType: recordType, Content: content, TTL: ttl})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doDnsRequest(ctx, http.MethodPost, c.BaseURL+"/api/v1/org/dns/records", payload)
	if err != nil {
		return nil, err
	}
	var out DnsRecord
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) UpdateOrganisationDnsRecord(ctx context.Context, recordID, content string, ttl *int) (*DnsRecord, error) {
	payload, err := json.Marshal(updateDnsRecordRequest{Content: content, TTL: ttl})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/org/dns/records/%s", c.BaseURL, neturl.PathEscape(recordID))
	body, err := c.doDnsRequest(ctx, http.MethodPatch, url, payload)
	if err != nil {
		return nil, err
	}
	var out DnsRecord
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) DeleteOrganisationDnsRecord(ctx context.Context, recordID string) error {
	url := fmt.Sprintf("%s/api/v1/org/dns/records/%s", c.BaseURL, neturl.PathEscape(recordID))
	_, err := c.doDnsRequest(ctx, http.MethodDelete, url, nil)
	return err
}

// doDnsRequest sends an authenticated JSON request to the DNS record routes.
// 400/403 responses surface the backend's detail message verbatim - those
// texts are user-actionable validation and admin-gate explanations.
func (c *Client) doDnsRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bodyReader)
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
		return nil, ErrDnsRecordNotFound
	case http.StatusBadRequest, http.StatusForbidden:
		if detail := dnsDetailFromBody(respBody); detail != "" {
			return nil, errors.New(detail)
		}
		return nil, newUnexpectedResponseError("DNS record request failed", resp.StatusCode, truncateForError(respBody, 500))
	default:
		return nil, newUnexpectedResponseError("DNS record request failed", resp.StatusCode, truncateForError(respBody, 500))
	}
}

// dnsDetailFromBody extracts the {"detail": "..."} member the backend uses
// for validation and permission errors; empty when the body has another
// shape.
func dnsDetailFromBody(body []byte) string {
	var envelope struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.Detail
}
