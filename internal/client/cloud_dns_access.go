package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// CloudDNSZone identifies a hosted zone in the organisation's Cloud account.
type CloudDNSZone struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ParentZoneID string `json:"parent_zone_id"`
}

// CloudDNSZones is the Cloud zone inventory visible to an organisation member.
type CloudDNSZones struct {
	Items []CloudDNSZone `json:"items"`
}

// CloudDNSAccessPolicy controls access to proxied hostnames in a zone.
type CloudDNSAccessPolicy struct {
	Mode           string   `json:"mode"`
	Roles          []string `json:"roles"`
	OrganisationID string   `json:"organisation_id,omitempty"`
	InheritedFrom  string   `json:"inherited_from,omitempty"`
}

type cloudDNSAccessEnvelope struct {
	Access CloudDNSAccessPolicy `json:"access"`
}

const cloudDNSZonesPath = "/api/v1/org/cloud-dns/zones"

// ErrCloudDNSZoneNotFound reports a zone outside the selected organisation or a missing zone.
var ErrCloudDNSZoneNotFound = errors.New("cloud DNS zone not found")

// CloudDNSValidationError is a policy selection the server refused as invalid.
type CloudDNSValidationError struct{ Detail string }

func (validation *CloudDNSValidationError) Error() string { return validation.Detail }

func (c *Client) cloudDNSRequest(ctx context.Context, method string, path string, payload []byte) ([]byte, error) {
	request, buildError := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	if buildError != nil {
		return nil, fmt.Errorf("create Cloud DNS request: %w", buildError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("cloud DNS request failed: %w", sendError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read Cloud DNS response: %w", readError)
	}
	switch response.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		var refusal struct {
			Detail     string `json:"detail"`
			Permission string `json:"permission"`
		}
		_ = json.Unmarshal(body, &refusal)
		if refusal.Detail == "permission_denied" {
			refusal.Detail = ""
		}
		return nil, &PermissionDeniedError{Detail: refusal.Detail, Permission: refusal.Permission}
	case http.StatusNotFound:
		return nil, ErrCloudDNSZoneNotFound
	case http.StatusBadRequest:
		if detail := dnsDetailFromBody(body); detail != "" {
			return nil, &CloudDNSValidationError{Detail: detail}
		}
		return nil, newUnexpectedResponseError("Cloud DNS request failed", response.StatusCode, truncateForError(body, 500))
	default:
		return nil, newUnexpectedResponseError("Cloud DNS request failed", response.StatusCode, truncateForError(body, 500))
	}
}

// ListCloudDNSZones lists the zones in the current organisation's Cloud account.
func (c *Client) ListCloudDNSZones(ctx context.Context) (*CloudDNSZones, error) {
	body, requestError := c.cloudDNSRequest(ctx, http.MethodGet, c.BaseURL+cloudDNSZonesPath, nil)
	if requestError != nil {
		return nil, requestError
	}
	var zones CloudDNSZones
	if decodeError := json.Unmarshal(body, &zones); decodeError != nil {
		return nil, fmt.Errorf("parse Cloud DNS zones: %w", decodeError)
	}
	return &zones, nil
}

// GetCloudDNSAccess reads the effective proxied access policy of a zone.
func (c *Client) GetCloudDNSAccess(ctx context.Context, zoneID string) (*CloudDNSAccessPolicy, error) {
	path := c.BaseURL + cloudDNSZonesPath + "/" + url.PathEscape(zoneID) + "/access"
	body, requestError := c.cloudDNSRequest(ctx, http.MethodGet, path, nil)
	if requestError != nil {
		return nil, requestError
	}
	var envelope cloudDNSAccessEnvelope
	if decodeError := json.Unmarshal(body, &envelope); decodeError != nil {
		return nil, fmt.Errorf("parse Cloud DNS access policy: %w", decodeError)
	}
	return &envelope.Access, nil
}

// UpdateCloudDNSAccess sets editable policy fields; Cloud binds the organisation server-side.
func (c *Client) UpdateCloudDNSAccess(ctx context.Context, zoneID string, policy CloudDNSAccessPolicy) (*CloudDNSAccessPolicy, error) {
	path := c.BaseURL + cloudDNSZonesPath + "/" + url.PathEscape(zoneID) + "/access"
	payload, encodeError := json.Marshal(struct {
		Mode  string   `json:"mode"`
		Roles []string `json:"roles"`
	}{Mode: policy.Mode, Roles: append([]string{}, policy.Roles...)})
	if encodeError != nil {
		return nil, fmt.Errorf("encode Cloud DNS access policy: %w", encodeError)
	}
	body, requestError := c.cloudDNSRequest(ctx, http.MethodPut, path, payload)
	if requestError != nil {
		return nil, requestError
	}
	var envelope cloudDNSAccessEnvelope
	if decodeError := json.Unmarshal(body, &envelope); decodeError != nil {
		return nil, fmt.Errorf("parse Cloud DNS access policy: %w", decodeError)
	}
	return &envelope.Access, nil
}
