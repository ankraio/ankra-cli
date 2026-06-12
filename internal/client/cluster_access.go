package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type ClusterAccessGrant struct {
	ID              string  `json:"id"`
	OrganisationID  string  `json:"organisation_id"`
	ClusterID       string  `json:"cluster_id"`
	AnkraUserID     string  `json:"ankra_user_id"`
	UserEmail       *string `json:"user_email,omitempty"`
	Scope           string  `json:"scope"`
	Namespace       *string `json:"namespace,omitempty"`
	Role            string  `json:"role"`
	ReconcileStatus string  `json:"reconcile_status"`
	ReconcileError  *string `json:"reconcile_error,omitempty"`
	ReconciledAt    *string `json:"reconciled_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

type ListClusterAccessGrantsResponse struct {
	Result []ClusterAccessGrant `json:"result"`
}

type CreateClusterAccessGrantRequest struct {
	UserEmail string  `json:"user_email"`
	Scope     string  `json:"scope"`
	Namespace *string `json:"namespace,omitempty"`
	Role      string  `json:"role"`
}

type CreateClusterAccessGrantResponse struct {
	Grant ClusterAccessGrant `json:"grant"`
}

type DeleteClusterAccessGrantResponse struct {
	Deleted bool `json:"deleted"`
}

func (c *Client) ListClusterAccessGrants(ctx context.Context, clusterID string) (*ListClusterAccessGrantsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants", c.BaseURL, clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("list access grants failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var grants ListClusterAccessGrantsResponse
	if err := parseJSON(body, &grants); err != nil {
		return nil, err
	}
	return &grants, nil
}

func (c *Client) CreateClusterAccessGrant(ctx context.Context, clusterID string, request CreateClusterAccessGrantRequest) (*CreateClusterAccessGrantResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants", c.BaseURL, clusterID)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("create access grant failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var created CreateClusterAccessGrantResponse
	if err := parseJSON(body, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *Client) DeleteClusterAccessGrant(ctx context.Context, clusterID string, grantID string) (*DeleteClusterAccessGrantResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/access/grants/%s", c.BaseURL, clusterID, grantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("revoke access grant failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var deleted DeleteClusterAccessGrantResponse
	if err := parseJSON(body, &deleted); err != nil {
		return nil, err
	}
	return &deleted, nil
}
