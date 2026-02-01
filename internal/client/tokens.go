package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// APIToken represents a user API token
type APIToken struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ExpiresAt  string  `json:"expires_at"`
	CreatedAt  string  `json:"created_at"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	Revoked    bool    `json:"revoked"`
}

// CreateAPITokenRequest is the request to create a new API token
type CreateAPITokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// CreateAPITokenResponse is the response from creating a new API token
type CreateAPITokenResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Type      string `json:"type"`
}

// RevokeAPITokenResponse is the response from revoking an API token
type RevokeAPITokenResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// DeleteAPITokenResponse is the response from deleting an API token
type DeleteAPITokenResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ListAPITokens returns all API tokens for the user
func ListAPITokens(token, baseURL string) ([]APIToken, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/account/tokens"
	var tokens []APIToken
	if err := getJSON(url, token, &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

// CreateAPIToken creates a new API token
func CreateAPIToken(token, baseURL, name string, expiresAt *string) (*CreateAPITokenResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/account/tokens"
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
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp CreateAPITokenResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &createResp, nil
}

// RevokeAPIToken revokes an API token
func RevokeAPIToken(token, baseURL, tokenID string) (*RevokeAPITokenResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/account/tokens/%s/revoke", strings.TrimRight(baseURL, "/"), tokenID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("revoke failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &RevokeAPITokenResponse{Success: true, Message: "Token revoked"}, nil
}

// DeleteAPIToken deletes an API token
func DeleteAPIToken(token, baseURL, tokenID string) (*DeleteAPITokenResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/account/tokens/%s", strings.TrimRight(baseURL, "/"), tokenID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &DeleteAPITokenResponse{Success: true, Message: "Token deleted"}, nil
}
