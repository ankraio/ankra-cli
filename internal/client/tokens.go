package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type APIToken struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ExpiresAt  string  `json:"expires_at"`
	CreatedAt  string  `json:"created_at"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	Revoked    bool    `json:"revoked"`
}

type CreateAPITokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type CreateAPITokenResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Type      string `json:"type"`
}

type RevokeAPITokenResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type DeleteAPITokenResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) ListAPITokens() ([]APIToken, error) {
	url := c.BaseURL + "/api/v1/org/account/tokens"
	var tokens []APIToken
	if err := c.getJSON(url, &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func (c *Client) CreateAPIToken(name string, expiresAt *string) (*CreateAPITokenResponse, error) {
	url := c.BaseURL + "/api/v1/org/account/tokens"
	reqBody := CreateAPITokenRequest{Name: name, ExpiresAt: expiresAt}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
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
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var createResp CreateAPITokenResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &createResp, nil
}

func (c *Client) RevokeAPIToken(tokenID string) (*RevokeAPITokenResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/account/tokens/%s/revoke", c.BaseURL, tokenID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("revoke failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	return &RevokeAPITokenResponse{Success: true, Message: "Token revoked"}, nil
}

func (c *Client) DeleteAPIToken(tokenID string) (*DeleteAPITokenResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/account/tokens/%s", c.BaseURL, tokenID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
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
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	return &DeleteAPITokenResponse{Success: true, Message: "Token deleted"}, nil
}
